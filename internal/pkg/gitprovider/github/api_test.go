package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-github/v62/github"
	"github.com/migueleliasweb/go-github-mock/src/mock"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestProvider creates a GitHubProvider with a mocked HTTP client.
func newTestProvider(mocks ...mock.MockBackendOption) *GitHubProvider {
	httpClient := mock.NewMockedHTTPClient(mocks...)
	v3Client := github.NewClient(httpClient)
	return &GitHubProvider{v3Client: v3Client}
}

// newTestProviderFromHandler creates a GitHubProvider backed by an httptest server.
// The caller must call the returned cleanup function when done.
func newTestProviderFromHandler(handler http.Handler) (*GitHubProvider, func()) {
	ts := httptest.NewServer(handler)
	client := github.NewClient(nil)
	baseURL := fmt.Sprintf("%s/", ts.URL)
	url, _ := client.BaseURL.Parse(baseURL)
	client.BaseURL = url
	client.UploadURL = url
	return &GitHubProvider{v3Client: client}, ts.Close
}

// ---------------------------------------------------------------------------
// git_operations.go tests
// ---------------------------------------------------------------------------

func TestGetRef_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.GetReposGitRefByOwnerByRepoByRef,
			github.Reference{
				Ref:    github.String("refs/heads/main"),
				NodeID: github.String("node123"),
				Object: &github.GitObject{SHA: github.String("abc123")},
			},
		),
	)

	ref, err := p.GetRef(context.Background(), "owner", "repo", "refs/heads/main")
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/main", ref.Ref)
	assert.Equal(t, "abc123", ref.SHA)
	assert.Equal(t, "node123", ref.NodeID)
}

func TestGetRef_ShortRefAutoPrefix(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.GetReposGitRefByOwnerByRepoByRef,
			github.Reference{
				Ref:    github.String("refs/heads/feature"),
				NodeID: github.String("n1"),
				Object: &github.GitObject{SHA: github.String("sha1")},
			},
		),
	)

	// Pass short ref without "refs/" prefix; method should auto-prefix.
	ref, err := p.GetRef(context.Background(), "owner", "repo", "feature")
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/feature", ref.Ref)
	assert.Equal(t, "sha1", ref.SHA)
}

func TestGetRef_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.GetReposGitRefByOwnerByRepoByRef,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusNotFound, "not found")
			}),
		),
	)

	_, err := p.GetRef(context.Background(), "owner", "repo", "refs/heads/nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get ref")
}

func TestCreateRef_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.PostReposGitRefsByOwnerByRepo,
			github.Reference{
				Ref:    github.String("refs/heads/new-branch"),
				NodeID: github.String("node456"),
				Object: &github.GitObject{SHA: github.String("def456")},
			},
		),
	)

	ref, err := p.CreateRef(context.Background(), "owner", "repo", "refs/heads/new-branch", "def456")
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/new-branch", ref.Ref)
	assert.Equal(t, "def456", ref.SHA)
	assert.Equal(t, "node456", ref.NodeID)
}

func TestCreateRef_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.PostReposGitRefsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusUnprocessableEntity, "reference already exists")
			}),
		),
	)

	_, err := p.CreateRef(context.Background(), "owner", "repo", "refs/heads/existing", "abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create ref")
}

func TestUpdateRef_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.PatchReposGitRefsByOwnerByRepoByRef,
			github.Reference{
				Ref:    github.String("refs/heads/main"),
				NodeID: github.String("n2"),
				Object: &github.GitObject{SHA: github.String("newsha")},
			},
		),
	)

	ref, err := p.UpdateRef(context.Background(), "owner", "repo", "refs/heads/main", "newsha", false)
	require.NoError(t, err)
	assert.Equal(t, "newsha", ref.SHA)
}

func TestUpdateRef_Force(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.PatchReposGitRefsByOwnerByRepoByRef,
			github.Reference{
				Ref:    github.String("refs/heads/main"),
				NodeID: github.String("n3"),
				Object: &github.GitObject{SHA: github.String("forcedsha")},
			},
		),
	)

	ref, err := p.UpdateRef(context.Background(), "owner", "repo", "main", "forcedsha", true)
	require.NoError(t, err)
	assert.Equal(t, "forcedsha", ref.SHA)
}

func TestUpdateRef_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.PatchReposGitRefsByOwnerByRepoByRef,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusInternalServerError, "server error")
			}),
		),
	)

	_, err := p.UpdateRef(context.Background(), "owner", "repo", "refs/heads/main", "sha", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update ref")
}

func TestDeleteRef_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.DeleteReposGitRefsByOwnerByRepoByRef,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}),
		),
	)

	err := p.DeleteRef(context.Background(), "owner", "repo", "refs/heads/old-branch")
	require.NoError(t, err)
}

func TestDeleteRef_ShortRef(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.DeleteReposGitRefsByOwnerByRepoByRef,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}),
		),
	)

	// Short ref should be auto-prefixed.
	err := p.DeleteRef(context.Background(), "owner", "repo", "old-branch")
	require.NoError(t, err)
}

func TestDeleteRef_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.DeleteReposGitRefsByOwnerByRepoByRef,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusNotFound, "ref not found")
			}),
		),
	)

	err := p.DeleteRef(context.Background(), "owner", "repo", "refs/heads/ghost")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete ref")
}

// ---------------------------------------------------------------------------
// pr_operations.go tests
// ---------------------------------------------------------------------------

func TestGetPullRequest_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.GetReposPullsByOwnerByRepoByPullNumber,
			github.PullRequest{
				Number:  github.Int(42),
				Title:   github.String("Add feature"),
				Body:    github.String("description"),
				State:   github.String("open"),
				User:    &github.User{Login: github.String("alice")},
				Head:    &github.PullRequestBranch{Ref: github.String("feat"), SHA: github.String("headsha")},
				Base:    &github.PullRequestBranch{Ref: github.String("main"), SHA: github.String("basesha")},
				HTMLURL: github.String("https://github.com/owner/repo/pull/42"),
			},
		),
	)

	pr, err := p.GetPullRequest(context.Background(), "owner", "repo", 42)
	require.NoError(t, err)
	assert.Equal(t, 42, pr.Number)
	assert.Equal(t, "Add feature", pr.Title)
	assert.Equal(t, "description", pr.Body)
	assert.Equal(t, "open", pr.State)
	assert.Equal(t, "alice", pr.Author)
	assert.Equal(t, "feat", pr.HeadRef)
	assert.Equal(t, "main", pr.BaseRef)
	assert.Equal(t, "headsha", pr.HeadSHA)
}

func TestGetPullRequest_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.GetReposPullsByOwnerByRepoByPullNumber,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusNotFound, "pull request not found")
			}),
		),
	)

	_, err := p.GetPullRequest(context.Background(), "owner", "repo", 999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get pull request")
}

func TestCreatePullRequest_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.PostReposPullsByOwnerByRepo,
			github.PullRequest{
				Number:  github.Int(10),
				Title:   github.String("New PR"),
				Body:    github.String("body text"),
				State:   github.String("open"),
				User:    &github.User{Login: github.String("bob")},
				Head:    &github.PullRequestBranch{Ref: github.String("feature"), SHA: github.String("h1")},
				Base:    &github.PullRequestBranch{Ref: github.String("main"), SHA: github.String("b1")},
				HTMLURL: github.String("https://github.com/owner/repo/pull/10"),
			},
		),
	)

	pr, err := p.CreatePullRequest(context.Background(), "owner", "repo", &gitprovider.NewPullRequest{
		Title:               "New PR",
		Body:                "body text",
		Head:                "feature",
		Base:                "main",
		MaintainerCanModify: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 10, pr.Number)
	assert.Equal(t, "New PR", pr.Title)
	assert.Equal(t, "feature", pr.HeadRef)
	assert.Equal(t, "main", pr.BaseRef)
}

func TestCreatePullRequest_VerifyRequestBody(t *testing.T) {
	t.Parallel()
	var capturedTitle string
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.PostReposPullsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Decode the request body to verify it
				var newPR github.NewPullRequest
				decoder := json.NewDecoder(r.Body)
				if err := decoder.Decode(&newPR); err == nil {
					capturedTitle = newPR.GetTitle()
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"number":1,"title":"My Title","state":"open","head":{"ref":"h","sha":"s"},"base":{"ref":"b","sha":"s2"}}`))
			}),
		),
	)

	_, err := p.CreatePullRequest(context.Background(), "owner", "repo", &gitprovider.NewPullRequest{
		Title: "My Title",
		Body:  "desc",
		Head:  "feature",
		Base:  "main",
	})
	require.NoError(t, err)
	assert.Equal(t, "My Title", capturedTitle)
}

func TestListPullRequestFiles_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.GetReposPullsFilesByOwnerByRepoByPullNumber,
			[]github.CommitFile{
				{
					Filename:  github.String("main.go"),
					Status:    github.String("modified"),
					Additions: github.Int(5),
					Deletions: github.Int(2),
					Changes:   github.Int(7),
				},
				{
					Filename:  github.String("README.md"),
					Status:    github.String("added"),
					Additions: github.Int(10),
					Deletions: github.Int(0),
					Changes:   github.Int(10),
				},
			},
		),
	)

	files, err := p.ListPullRequestFiles(context.Background(), "owner", "repo", 1)
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "main.go", files[0].Filename)
	assert.Equal(t, "modified", files[0].Status)
	assert.Equal(t, 5, files[0].Additions)
	assert.Equal(t, "README.md", files[1].Filename)
	assert.Equal(t, "added", files[1].Status)
}

func TestListPullRequestFiles_EmptyList(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.GetReposPullsFilesByOwnerByRepoByPullNumber,
			[]github.CommitFile{},
		),
	)

	files, err := p.ListPullRequestFiles(context.Background(), "owner", "repo", 1)
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestListPullRequestFiles_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.GetReposPullsFilesByOwnerByRepoByPullNumber,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusInternalServerError, "internal error")
			}),
		),
	)

	_, err := p.ListPullRequestFiles(context.Background(), "owner", "repo", 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list PR files")
}

func TestMergePullRequest_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.PutReposPullsMergeByOwnerByRepoByPullNumber,
			github.PullRequestMergeResult{
				Merged:  github.Bool(true),
				Message: github.String("Pull Request successfully merged"),
			},
		),
	)

	err := p.MergePullRequest(context.Background(), "owner", "repo", 1, &gitprovider.MergeOptions{
		MergeMethod:   "squash",
		CommitMessage: "squash merge",
	})
	require.NoError(t, err)
}

func TestMergePullRequest_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.PutReposPullsMergeByOwnerByRepoByPullNumber,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusMethodNotAllowed, "not mergeable")
			}),
		),
	)

	err := p.MergePullRequest(context.Background(), "owner", "repo", 1, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to merge pull request")
}

func TestCommentOnPullRequest_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.PostReposIssuesCommentsByOwnerByRepoByIssueNumber,
			github.IssueComment{
				ID:   github.Int64(100),
				Body: github.String("looks good"),
				User: &github.User{Login: github.String("bot")},
			},
		),
	)

	comment, err := p.CommentOnPullRequest(context.Background(), "owner", "repo", 1, "looks good")
	require.NoError(t, err)
	assert.Equal(t, int64(100), comment.ID)
	assert.Equal(t, "looks good", comment.Body)
	assert.Equal(t, "bot", comment.Author)
}

func TestListPullRequestComments_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.GetReposIssuesCommentsByOwnerByRepoByIssueNumber,
			[]github.IssueComment{
				{
					ID:   github.Int64(1),
					Body: github.String("first"),
					User: &github.User{Login: github.String("u1")},
				},
				{
					ID:   github.Int64(2),
					Body: github.String("second"),
					User: &github.User{Login: github.String("u2")},
				},
			},
		),
	)

	comments, err := p.ListPullRequestComments(context.Background(), "owner", "repo", 1)
	require.NoError(t, err)
	require.Len(t, comments, 2)
	assert.Equal(t, "first", comments[0].Body)
	assert.Equal(t, "second", comments[1].Body)
}

func TestApprovePullRequest_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.PostReposPullsReviewsByOwnerByRepoByPullNumber,
			github.PullRequestReview{
				ID:    github.Int64(50),
				User:  &github.User{Login: github.String("approver")},
				State: github.String("APPROVED"),
				Body:  github.String(""),
			},
		),
	)

	review, err := p.ApprovePullRequest(context.Background(), "owner", "repo", 5)
	require.NoError(t, err)
	assert.Equal(t, int64(50), review.ID)
	assert.Equal(t, gitprovider.ReviewStateApproved, review.State)
}

func TestListPullRequestReviews_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.GetReposPullsReviewsByOwnerByRepoByPullNumber,
			[]github.PullRequestReview{
				{
					ID:    github.Int64(1),
					User:  &github.User{Login: github.String("r1")},
					State: github.String("APPROVED"),
				},
				{
					ID:    github.Int64(2),
					User:  &github.User{Login: github.String("r2")},
					State: github.String("CHANGES_REQUESTED"),
				},
			},
		),
	)

	reviews, err := p.ListPullRequestReviews(context.Background(), "owner", "repo", 3)
	require.NoError(t, err)
	require.Len(t, reviews, 2)
	assert.Equal(t, gitprovider.ReviewStateApproved, reviews[0].State)
	assert.Equal(t, gitprovider.ReviewStateChangesRequested, reviews[1].State)
}

// ---------------------------------------------------------------------------
// status.go tests
// ---------------------------------------------------------------------------

func TestSetCommitStatus_Happy(t *testing.T) {
	t.Parallel()
	p, cleanup := newTestProviderFromHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// POST /repos/{owner}/{repo}/statuses/{sha}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/statuses/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(github.RepoStatus{
				State:       github.String("success"),
				TargetURL:   github.String("https://ci.example.com"),
				Description: github.String("Build passed"),
				Context:     github.String("ci/build"),
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer cleanup()

	err := p.SetCommitStatus(context.Background(), "owner", "repo", "abc123", &gitprovider.Status{
		State:       "success",
		TargetURL:   "https://ci.example.com",
		Description: "Build passed",
		Context:     "ci/build",
	})
	require.NoError(t, err)
}

func TestSetCommitStatus_Error(t *testing.T) {
	t.Parallel()
	p, cleanup := newTestProviderFromHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"server error"}`))
	}))
	defer cleanup()

	err := p.SetCommitStatus(context.Background(), "owner", "repo", "abc123", &gitprovider.Status{
		State:   "success",
		Context: "ci/build",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to set commit status")
}

func TestGetCommitStatus_Happy(t *testing.T) {
	t.Parallel()
	p, cleanup := newTestProviderFromHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GET /repos/{owner}/{repo}/commits/{ref}/statuses
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/statuses") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]github.RepoStatus{
				{
					State:       github.String("success"),
					Context:     github.String("ci/build"),
					Description: github.String("passed"),
					TargetURL:   github.String("https://ci.example.com/1"),
				},
				{
					State:       github.String("pending"),
					Context:     github.String("ci/lint"),
					Description: github.String("running"),
					TargetURL:   github.String("https://ci.example.com/2"),
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer cleanup()

	statuses, err := p.GetCommitStatus(context.Background(), "owner", "repo", "sha1")
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	assert.Equal(t, "success", statuses[0].State)
	assert.Equal(t, "ci/build", statuses[0].Context)
	assert.Equal(t, "pending", statuses[1].State)
	assert.Equal(t, "ci/lint", statuses[1].Context)
}

func TestGetCombinedStatus_Happy(t *testing.T) {
	t.Parallel()
	p, cleanup := newTestProviderFromHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GET /repos/{owner}/{repo}/commits/{ref}/status
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/status") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(github.CombinedStatus{
				State: github.String("success"),
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer cleanup()

	state, err := p.GetCombinedStatus(context.Background(), "owner", "repo", "sha1")
	require.NoError(t, err)
	assert.Equal(t, "success", state)
}

// ---------------------------------------------------------------------------
// client.go — repository / file content tests
// ---------------------------------------------------------------------------

func TestGetRepository_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.GetReposByOwnerByRepo,
			github.Repository{
				ID:            github.Int64(1001),
				Name:          github.String("myrepo"),
				FullName:      github.String("owner/myrepo"),
				Owner:         &github.User{Login: github.String("owner")},
				DefaultBranch: github.String("main"),
				Private:       github.Bool(false),
				HTMLURL:       github.String("https://github.com/owner/myrepo"),
				CloneURL:      github.String("https://github.com/owner/myrepo.git"),
			},
		),
	)

	repo, err := p.GetRepository(context.Background(), "owner", "myrepo")
	require.NoError(t, err)
	assert.Equal(t, int64(1001), repo.ID)
	assert.Equal(t, "myrepo", repo.Name)
	assert.Equal(t, "owner/myrepo", repo.FullName)
	assert.Equal(t, "owner", repo.Owner)
	assert.Equal(t, "main", repo.DefaultBranch)
	assert.False(t, repo.Private)
	assert.Equal(t, "https://github.com/owner/myrepo", repo.HTMLURL)
	assert.Equal(t, "https://github.com/owner/myrepo.git", repo.CloneURL)
}

func TestGetRepository_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.GetReposByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusNotFound, "repo not found")
			}),
		),
	)

	_, err := p.GetRepository(context.Background(), "owner", "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get repository")
}

func TestGetDefaultBranch_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.GetReposByOwnerByRepo,
			github.Repository{
				ID:            github.Int64(1),
				Name:          github.String("repo"),
				FullName:      github.String("owner/repo"),
				Owner:         &github.User{Login: github.String("owner")},
				DefaultBranch: github.String("develop"),
			},
		),
	)

	branch, err := p.GetDefaultBranch(context.Background(), "owner", "repo")
	require.NoError(t, err)
	assert.Equal(t, "develop", branch)
}

func TestGetFileContent_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.GetReposContentsByOwnerByRepoByPath,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// The GitHub API returns a RepositoryContent JSON for a single file.
				// Content must be base64-encoded.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{
					"type": "file",
					"name": "hello.txt",
					"path": "hello.txt",
					"sha": "abc",
					"size": 13,
					"encoding": "base64",
					"content": "SGVsbG8sIFdvcmxkIQ=="
				}`))
			}),
		),
	)

	content, err := p.GetFileContent(context.Background(), "owner", "repo", "hello.txt", "main")
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", string(content))
}

func TestGetFileContent_NotFound(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.GetReposContentsByOwnerByRepoByPath,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusNotFound, "file not found")
			}),
		),
	)

	_, err := p.GetFileContent(context.Background(), "owner", "repo", "missing.txt", "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get file content")
}

func TestGetDirectoryContent_Happy(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.GetReposContentsByOwnerByRepoByPath,
			[]github.RepositoryContent{
				{
					Type:    github.String("file"),
					Name:    github.String("file1.go"),
					Path:    github.String("src/file1.go"),
					Size:    github.Int(100),
					SHA:     github.String("sha1"),
					HTMLURL: github.String("https://github.com/owner/repo/blob/main/src/file1.go"),
				},
				{
					Type:    github.String("dir"),
					Name:    github.String("sub"),
					Path:    github.String("src/sub"),
					Size:    github.Int(0),
					SHA:     github.String("sha2"),
					HTMLURL: github.String("https://github.com/owner/repo/tree/main/src/sub"),
				},
			},
		),
	)

	nodes, err := p.GetDirectoryContent(context.Background(), "owner", "repo", "src", "main")
	require.NoError(t, err)
	require.Len(t, nodes, 2)

	assert.Equal(t, "file1.go", nodes[0].Name)
	assert.Equal(t, "src/file1.go", nodes[0].Path)
	assert.Equal(t, "file", nodes[0].Type)
	assert.Equal(t, 100, nodes[0].Size)
	assert.Equal(t, "sha1", nodes[0].SHA)

	assert.Equal(t, "sub", nodes[1].Name)
	assert.Equal(t, "dir", nodes[1].Type)
}

func TestGetDirectoryContent_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.GetReposContentsByOwnerByRepoByPath,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusNotFound, "directory not found")
			}),
		),
	)

	_, err := p.GetDirectoryContent(context.Background(), "owner", "repo", "nonexistent", "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get directory content")
}

// ---------------------------------------------------------------------------
// Additional edge-case tests
// ---------------------------------------------------------------------------

func TestMergePullRequest_NilOptions(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.PutReposPullsMergeByOwnerByRepoByPullNumber,
			github.PullRequestMergeResult{
				Merged:  github.Bool(true),
				Message: github.String("merged"),
			},
		),
	)

	// nil options should default to "merge" method.
	err := p.MergePullRequest(context.Background(), "owner", "repo", 1, nil)
	require.NoError(t, err)
}

func TestGetCombinedStatus_Error(t *testing.T) {
	t.Parallel()
	p, cleanup := newTestProviderFromHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"server error"}`))
	}))
	defer cleanup()

	_, err := p.GetCombinedStatus(context.Background(), "owner", "repo", "sha")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get combined status")
}

func TestGetCommitStatus_Error(t *testing.T) {
	t.Parallel()
	p, cleanup := newTestProviderFromHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"server error"}`))
	}))
	defer cleanup()

	_, err := p.GetCommitStatus(context.Background(), "owner", "repo", "sha")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get commit statuses")
}

func TestCommentOnPullRequest_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.PostReposIssuesCommentsByOwnerByRepoByIssueNumber,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusForbidden, "forbidden")
			}),
		),
	)

	_, err := p.CommentOnPullRequest(context.Background(), "owner", "repo", 1, "hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create comment")
}

func TestApprovePullRequest_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.PostReposPullsReviewsByOwnerByRepoByPullNumber,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusUnprocessableEntity, "cannot approve own PR")
			}),
		),
	)

	_, err := p.ApprovePullRequest(context.Background(), "owner", "repo", 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to approve pull request")
}

func TestCreateRef_ShortRef(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatch(
			mock.PostReposGitRefsByOwnerByRepo,
			github.Reference{
				Ref:    github.String("refs/heads/short"),
				NodeID: github.String("n1"),
				Object: &github.GitObject{SHA: github.String("s1")},
			},
		),
	)

	// Short ref without "refs/" prefix
	ref, err := p.CreateRef(context.Background(), "owner", "repo", "short", "s1")
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/short", ref.Ref)
}

func TestGetDefaultBranch_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.GetReposByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusNotFound, "not found")
			}),
		),
	)

	_, err := p.GetDefaultBranch(context.Background(), "owner", "repo")
	require.Error(t, err)
}

func TestListPullRequestReviews_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.GetReposPullsReviewsByOwnerByRepoByPullNumber,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusInternalServerError, "server error")
			}),
		),
	)

	_, err := p.ListPullRequestReviews(context.Background(), "owner", "repo", 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list reviews")
}

func TestListPullRequestComments_Error(t *testing.T) {
	t.Parallel()
	p := newTestProvider(
		mock.WithRequestMatchHandler(
			mock.GetReposIssuesCommentsByOwnerByRepoByIssueNumber,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusInternalServerError, "server error")
			}),
		),
	)

	_, err := p.ListPullRequestComments(context.Background(), "owner", "repo", 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list comments")
}
