package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// newTestServer creates an httptest server and a GitLabProvider wired to it.
// The handlers map keys are matched against the *decoded* request path (after
// stripping the /api/v4 prefix). This is necessary because the GitLab client
// URL-encodes the project path (e.g., "owner/repo" -> "owner%2Frepo"), and
// the Go HTTP ServeMux treats %2F as "/" which causes routing confusion.
func newTestServer(t *testing.T, handlers map[string]http.HandlerFunc) (*httptest.Server, *GitLabProvider) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Decode the raw path so "owner%2Frepo" becomes "owner/repo"
		decoded, err := url.PathUnescape(r.URL.RawPath)
		if err != nil || decoded == "" {
			decoded = r.URL.Path
		}

		// Strip /api/v4 prefix for simpler handler keys
		path := strings.TrimPrefix(decoded, "/api/v4")

		for pattern, handler := range handlers {
			if path == pattern {
				handler(w, r)
				return
			}
		}
		// Fallback: 404
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, `{"message":"404 Not Found: %s"}`, path)
	}))
	t.Cleanup(server.Close)

	client, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(server.URL+"/api/v4"))
	require.NoError(t, err)

	provider := &GitLabProvider{client: client}
	return server, provider
}

// ---------- GetPullRequest ----------

func TestGetPullRequest_Happy(t *testing.T) {
	t.Parallel()

	now := time.Now().Truncate(time.Second)
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/42": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"iid":                   42,
				"title":                 "My MR",
				"description":           "MR body",
				"state":                 "opened",
				"source_branch":         "feature",
				"target_branch":         "main",
				"sha":                   "headsha123",
				"web_url":               "https://gitlab.com/owner/repo/-/merge_requests/42",
				"merge_commit_sha":      "",
				"detailed_merge_status": "mergeable",
				"labels":                []string{"bug", "urgent"},
				"author":                map[string]interface{}{"username": "alice"},
				"diff_refs": map[string]interface{}{
					"base_sha": "basesha456",
				},
				"created_at": now.Format(time.RFC3339),
				"updated_at": now.Format(time.RFC3339),
			})
		},
	})

	pr, err := provider.GetPullRequest(context.Background(), "owner", "repo", 42)
	require.NoError(t, err)

	assert.Equal(t, 42, pr.Number)
	assert.Equal(t, "My MR", pr.Title)
	assert.Equal(t, "MR body", pr.Body)
	assert.Equal(t, "open", pr.State)
	assert.Equal(t, "feature", pr.HeadRef)
	assert.Equal(t, "main", pr.BaseRef)
	assert.Equal(t, "headsha123", pr.HeadSHA)
	assert.Equal(t, "basesha456", pr.BaseSHA)
	assert.Equal(t, "alice", pr.Author)
	assert.True(t, pr.Mergeable)
	assert.False(t, pr.Merged)
	assert.Equal(t, []string{"bug", "urgent"}, pr.Labels)
	assert.Equal(t, "https://gitlab.com/owner/repo/-/merge_requests/42", pr.HTMLURL)
}

func TestGetPullRequest_Error(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/99": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "404 Not Found"})
		},
	})

	_, err := provider.GetPullRequest(context.Background(), "owner", "repo", 99)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get merge request")
}

func TestGetPullRequest_MergedState(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/10": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"iid":              10,
				"title":            "Merged MR",
				"description":      "",
				"state":            "merged",
				"source_branch":    "feat",
				"target_branch":    "main",
				"sha":              "sha1",
				"merge_commit_sha": "mergesha",
				"author":           map[string]interface{}{"username": "bob"},
				"diff_refs":        map[string]interface{}{"base_sha": "base1"},
			})
		},
	})

	pr, err := provider.GetPullRequest(context.Background(), "owner", "repo", 10)
	require.NoError(t, err)
	assert.Equal(t, "merged", pr.State)
	assert.True(t, pr.Merged)
	assert.Equal(t, "mergesha", pr.MergeCommit)
}

func TestGetPullRequest_ClosedState(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/11": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"iid":           11,
				"title":         "Closed MR",
				"state":         "closed",
				"source_branch": "feat",
				"target_branch": "main",
				"sha":           "sha1",
				"author":        map[string]interface{}{"username": "bob"},
				"diff_refs":     map[string]interface{}{"base_sha": "base1"},
			})
		},
	})

	pr, err := provider.GetPullRequest(context.Background(), "owner", "repo", 11)
	require.NoError(t, err)
	assert.Equal(t, "closed", pr.State)
	assert.False(t, pr.Merged)
}

// ---------- CreatePullRequest ----------

func TestCreatePullRequest_Happy(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedBody)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"iid":           1,
				"title":         "New Feature",
				"description":   "desc",
				"state":         "opened",
				"source_branch": "feature",
				"target_branch": "main",
				"sha":           "newsha",
				"author":        map[string]interface{}{"username": "alice"},
				"diff_refs":     map[string]interface{}{"base_sha": "base"},
			})
		},
	})

	pr, err := provider.CreatePullRequest(context.Background(), "owner", "repo", &gitprovider.NewPullRequest{
		Title: "New Feature",
		Body:  "desc",
		Head:  "feature",
		Base:  "main",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, pr.Number)
	assert.Equal(t, "New Feature", pr.Title)
	assert.Equal(t, "feature", pr.HeadRef)
	assert.Equal(t, "main", pr.BaseRef)

	// Verify the request body sent to GitLab
	assert.Equal(t, "New Feature", capturedBody["title"])
	assert.Equal(t, "desc", capturedBody["description"])
	assert.Equal(t, "feature", capturedBody["source_branch"])
	assert.Equal(t, "main", capturedBody["target_branch"])
}

func TestCreatePullRequest_WithLabels(t *testing.T) {
	t.Parallel()

	labelUpdateCalled := false
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"iid":           2,
					"title":         "With Labels",
					"state":         "opened",
					"source_branch": "feat",
					"target_branch": "main",
					"sha":           "sha",
					"author":        map[string]interface{}{"username": "alice"},
					"diff_refs":     map[string]interface{}{"base_sha": "base"},
				})
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		},
		"/projects/owner/repo/merge_requests/2": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				labelUpdateCalled = true
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"iid":    2,
					"labels": []string{"enhancement", "ready"},
				})
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		},
	})

	pr, err := provider.CreatePullRequest(context.Background(), "owner", "repo", &gitprovider.NewPullRequest{
		Title:  "With Labels",
		Body:   "",
		Head:   "feat",
		Base:   "main",
		Labels: []string{"enhancement", "ready"},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, pr.Number)
	assert.True(t, labelUpdateCalled, "should have called update to add labels")
	assert.Equal(t, []string{"enhancement", "ready"}, pr.Labels)
}

// ---------- ListPullRequestFiles ----------

func TestListPullRequestFiles_Happy(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/5/changes": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"iid": 5,
				"diff_refs": map[string]interface{}{
					"base_sha": "base111",
					"head_sha": "head222",
				},
			})
		},
		"/projects/owner/repo/repository/compare": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "base111", r.URL.Query().Get("from"))
			assert.Equal(t, "head222", r.URL.Query().Get("to"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"diffs": []map[string]interface{}{
					{
						"new_path":     "file1.go",
						"old_path":     "file1.go",
						"new_file":     true,
						"deleted_file": false,
						"renamed_file": false,
						"diff":         "+package main\n+\n+func main() {}\n",
					},
					{
						"new_path":     "file2.go",
						"old_path":     "file2.go",
						"new_file":     false,
						"deleted_file": true,
						"renamed_file": false,
						"diff":         "-old line\n",
					},
				},
			})
		},
	})

	files, err := provider.ListPullRequestFiles(context.Background(), "owner", "repo", 5)
	require.NoError(t, err)
	require.Len(t, files, 2)

	assert.Equal(t, "file1.go", files[0].Filename)
	assert.Equal(t, "added", files[0].Status)
	assert.Equal(t, 3, files[0].Additions)
	assert.Equal(t, 0, files[0].Deletions)
	assert.Equal(t, 3, files[0].Changes)

	assert.Equal(t, "file2.go", files[1].Filename)
	assert.Equal(t, "removed", files[1].Status)
	assert.Equal(t, 0, files[1].Additions)
	assert.Equal(t, 1, files[1].Deletions)
}

func TestListPullRequestFiles_NoDiffRefs(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/7/changes": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"iid": 7,
				"diff_refs": map[string]interface{}{
					"base_sha": "",
					"head_sha": "",
				},
			})
		},
	})

	_, err := provider.ListPullRequestFiles(context.Background(), "owner", "repo", 7)
	require.Error(t, err)
	assert.ErrorIs(t, err, gitprovider.ErrNoDiffRefs)
}

func TestListPullRequestFiles_Error(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/8/changes": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "403 Forbidden"})
		},
	})

	_, err := provider.ListPullRequestFiles(context.Background(), "owner", "repo", 8)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list MR changes")
}

func TestListPullRequestFiles_RenamedFile(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/o/r/merge_requests/1/changes": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"iid": 1,
				"diff_refs": map[string]interface{}{
					"base_sha": "base",
					"head_sha": "head",
				},
			})
		},
		"/projects/o/r/repository/compare": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"diffs": []map[string]interface{}{
					{
						"new_path":     "new_name.go",
						"old_path":     "old_name.go",
						"new_file":     false,
						"deleted_file": false,
						"renamed_file": true,
						"diff":         "",
					},
				},
			})
		},
	})

	files, err := provider.ListPullRequestFiles(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "renamed", files[0].Status)
	assert.Equal(t, "new_name.go", files[0].Filename)
}

// ---------- MergePullRequest ----------

func TestMergePullRequest_Happy(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/3/merge": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPut, r.Method)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"iid":   3,
				"state": "merged",
			})
		},
	})

	err := provider.MergePullRequest(context.Background(), "owner", "repo", 3, nil)
	require.NoError(t, err)
}

func TestMergePullRequest_WithOptions(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/3/merge": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedBody)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"iid": 3, "state": "merged"})
		},
	})

	err := provider.MergePullRequest(context.Background(), "owner", "repo", 3, &gitprovider.MergeOptions{
		CommitMessage: "Merge it",
		SHA:           "expectedsha",
		MergeMethod:   gitprovider.MergeMethodSquash,
	})
	require.NoError(t, err)
	assert.Equal(t, "Merge it", capturedBody["merge_commit_message"])
	assert.Equal(t, "expectedsha", capturedBody["sha"])
	assert.Equal(t, true, capturedBody["squash"])
}

func TestMergePullRequest_Error(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/3/merge": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "cannot merge"})
		},
	})

	err := provider.MergePullRequest(context.Background(), "owner", "repo", 3, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to merge")
}

// ---------- CommentOnPullRequest ----------

func TestCommentOnPullRequest_Happy(t *testing.T) {
	t.Parallel()

	now := time.Now().Truncate(time.Second)
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/4/notes": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			body, _ := io.ReadAll(r.Body)
			var req map[string]string
			_ = json.Unmarshal(body, &req)
			assert.Equal(t, "Nice work!", req["body"])

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":         101,
				"body":       "Nice work!",
				"author":     map[string]interface{}{"username": "bot"},
				"created_at": now.Format(time.RFC3339),
				"updated_at": now.Format(time.RFC3339),
			})
		},
	})

	comment, err := provider.CommentOnPullRequest(context.Background(), "owner", "repo", 4, "Nice work!")
	require.NoError(t, err)
	assert.Equal(t, int64(101), comment.ID)
	assert.Equal(t, "Nice work!", comment.Body)
	assert.Equal(t, "bot", comment.Author)
}

// ---------- ApprovePullRequest ----------

func TestApprovePullRequest_Happy(t *testing.T) {
	t.Parallel()

	now := time.Now().Truncate(time.Second)
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/6/approve": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":         6,
				"created_at": now.Format(time.RFC3339),
			})
		},
		"/user": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       1,
				"username": "approver-bot",
			})
		},
	})

	review, err := provider.ApprovePullRequest(context.Background(), "owner", "repo", 6)
	require.NoError(t, err)
	assert.Equal(t, gitprovider.ReviewStateApproved, review.State)
	assert.Equal(t, "approver-bot", review.Author)
	assert.Equal(t, "Approved", review.Body)
}

func TestApprovePullRequest_Error(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/merge_requests/6/approve": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "forbidden"})
		},
	})

	_, err := provider.ApprovePullRequest(context.Background(), "owner", "repo", 6)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to approve")
}

// ---------- GetRef ----------

func TestGetRef_Happy(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/branches/feature": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"name":   "feature",
				"commit": map[string]interface{}{"id": "abc123"},
			})
		},
	})

	ref, err := provider.GetRef(context.Background(), "owner", "repo", "refs/heads/feature")
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/feature", ref.Ref)
	assert.Equal(t, "abc123", ref.SHA)
}

func TestGetRef_WithoutPrefix(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/branches/mybranch": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"name":   "mybranch",
				"commit": map[string]interface{}{"id": "sha1"},
			})
		},
	})

	ref, err := provider.GetRef(context.Background(), "owner", "repo", "mybranch")
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/mybranch", ref.Ref)
}

func TestGetRef_Error(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/branches/nonexistent": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "404 Branch Not Found"})
		},
	})

	_, err := provider.GetRef(context.Background(), "owner", "repo", "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get ref")
}

// ---------- CreateRef ----------

func TestCreateRef_Happy(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/branches": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			body, _ := io.ReadAll(r.Body)
			var req map[string]string
			_ = json.Unmarshal(body, &req)
			assert.Equal(t, "new-branch", req["branch"])
			assert.Equal(t, "sha999", req["ref"])

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"name":   "new-branch",
				"commit": map[string]interface{}{"id": "sha999"},
			})
		},
	})

	ref, err := provider.CreateRef(context.Background(), "owner", "repo", "refs/heads/new-branch", "sha999")
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/new-branch", ref.Ref)
	assert.Equal(t, "sha999", ref.SHA)
}

func TestCreateRef_Error(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/branches": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Branch already exists"})
		},
	})

	_, err := provider.CreateRef(context.Background(), "owner", "repo", "existing-branch", "sha1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create ref")
}

// ---------- UpdateRef ----------

func TestUpdateRef_NonForceReturnsError(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	_, err := provider.UpdateRef(context.Background(), "owner", "repo", "main", "sha", false)
	require.Error(t, err)
	var pnse *gitprovider.ProviderNotSupportedError
	assert.ErrorAs(t, err, &pnse)
}

func TestUpdateRef_ForceAlreadyAtSHA(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/branches/mybranch": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"name":   "mybranch",
				"commit": map[string]interface{}{"id": "targetsha"},
			})
		},
	})

	ref, err := provider.UpdateRef(context.Background(), "owner", "repo", "mybranch", "targetsha", true)
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/mybranch", ref.Ref)
	assert.Equal(t, "targetsha", ref.SHA)
}

func TestUpdateRef_ForceHappy(t *testing.T) {
	t.Parallel()

	deleteCalled := false
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/branches/mybranch": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				deleteCalled = true
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// GET
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"name":   "mybranch",
				"commit": map[string]interface{}{"id": "oldsha"},
			})
		},
		"/projects/owner/repo/repository/branches": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"name":   "mybranch",
					"commit": map[string]interface{}{"id": "newsha"},
				})
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		},
	})

	ref, err := provider.UpdateRef(context.Background(), "owner", "repo", "mybranch", "newsha", true)
	require.NoError(t, err)
	assert.True(t, deleteCalled)
	assert.Equal(t, "refs/heads/mybranch", ref.Ref)
	assert.Equal(t, "newsha", ref.SHA)
}

func TestUpdateRef_ForceDeleteFails(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/branches/mybranch": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "protected branch"})
				return
			}
			// GET
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"name":   "mybranch",
				"commit": map[string]interface{}{"id": "oldsha"},
			})
		},
	})

	_, err := provider.UpdateRef(context.Background(), "owner", "repo", "mybranch", "newsha", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete branch")
}

func TestUpdateRef_ForceRollbackOnCreateFailure(t *testing.T) {
	t.Parallel()

	createCallCount := 0
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/branches/mybranch": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// GET
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"name":   "mybranch",
				"commit": map[string]interface{}{"id": "oldsha"},
			})
		},
		"/projects/owner/repo/repository/branches": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				createCallCount++
				if createCallCount == 1 {
					// First create (at new SHA) fails with 422 (not retried by client)
					w.WriteHeader(http.StatusUnprocessableEntity)
					_ = json.NewEncoder(w).Encode(map[string]string{"message": "invalid reference"})
					return
				}
				// Second create (rollback to old SHA) succeeds
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"name":   "mybranch",
					"commit": map[string]interface{}{"id": "oldsha"},
				})
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		},
	})

	_, err := provider.UpdateRef(context.Background(), "owner", "repo", "mybranch", "newsha", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rolled back to oldsha")
	assert.Equal(t, 2, createCallCount, "should have called create twice: once for new SHA and once for rollback")
}

// ---------- DeleteRef ----------

func TestDeleteRef_Happy(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/branches/old-branch": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodDelete, r.Method)
			w.WriteHeader(http.StatusNoContent)
		},
	})

	err := provider.DeleteRef(context.Background(), "owner", "repo", "refs/heads/old-branch")
	require.NoError(t, err)
}

func TestDeleteRef_Error(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/branches/protected": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "forbidden"})
		},
	})

	err := provider.DeleteRef(context.Background(), "owner", "repo", "protected")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete ref")
}

// ---------- CreateCommit ----------

func TestCreateCommit_Happy(t *testing.T) {
	t.Parallel()

	now := time.Now().Truncate(time.Second)
	var capturedBody map[string]interface{}
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/commits": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedBody)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":             "newcommitsha",
				"message":        "add files",
				"author_name":    "Test Author",
				"committer_name": "Test Committer",
				"authored_date":  now.Format(time.RFC3339),
				"parent_ids":     []string{"parentsha"},
				"web_url":        "https://gitlab.com/owner/repo/-/commit/newcommitsha",
			})
		},
	})

	commit, err := provider.CreateCommit(context.Background(), "owner", "repo", &gitprovider.CommitOptions{
		Message: "add files",
		Branch:  "feature",
		Author: &gitprovider.CommitAuthor{
			Name:  "Test Author",
			Email: "test@example.com",
		},
		CommitActions: []*gitprovider.CommitAction{
			{Action: "create", FilePath: "new.txt", Content: "hello"},
			{Action: "update", FilePath: "existing.txt", Content: "updated", Encoding: "text"},
			{Action: "delete", FilePath: "old.txt"},
			{Action: "move", FilePath: "renamed.txt", PreviousPath: "original.txt", Content: "content"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "newcommitsha", commit.SHA)
	assert.Equal(t, "add files", commit.Message)
	assert.Equal(t, "Test Author", commit.Author)
	assert.Equal(t, []string{"parentsha"}, commit.Parents)

	// Verify actions were sent correctly
	actions, ok := capturedBody["actions"].([]interface{})
	require.True(t, ok)
	require.Len(t, actions, 4)

	a0 := actions[0].(map[string]interface{})
	assert.Equal(t, "create", a0["action"])
	assert.Equal(t, "new.txt", a0["file_path"])
	assert.Equal(t, "hello", a0["content"])

	a1 := actions[1].(map[string]interface{})
	assert.Equal(t, "update", a1["action"])
	assert.Equal(t, "text", a1["encoding"])

	a2 := actions[2].(map[string]interface{})
	assert.Equal(t, "delete", a2["action"])
	// delete action should not have content
	_, hasContent := a2["content"]
	assert.False(t, hasContent)

	a3 := actions[3].(map[string]interface{})
	assert.Equal(t, "move", a3["action"])
	assert.Equal(t, "original.txt", a3["previous_path"])

	// Verify branch and message
	assert.Equal(t, "feature", capturedBody["branch"])
	assert.Equal(t, "add files", capturedBody["commit_message"])
	assert.Equal(t, "Test Author", capturedBody["author_name"])
	assert.Equal(t, "test@example.com", capturedBody["author_email"])
}

func TestCreateCommit_NoActions(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	_, err := provider.CreateCommit(context.Background(), "owner", "repo", &gitprovider.CommitOptions{
		Message: "empty",
		Branch:  "main",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CommitActions must be provided")
}

func TestCreateCommit_NoBranch(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	_, err := provider.CreateCommit(context.Background(), "owner", "repo", &gitprovider.CommitOptions{
		Message: "no branch",
		CommitActions: []*gitprovider.CommitAction{
			{Action: "create", FilePath: "f.txt", Content: "x"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch must be specified")
}

// ---------- SetCommitStatus ----------

func TestSetCommitStatus_Happy(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/statuses/abc123": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedBody)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     1,
				"status": "success",
			})
		},
	})

	err := provider.SetCommitStatus(context.Background(), "owner", "repo", "abc123", &gitprovider.Status{
		State:       "success",
		TargetURL:   "https://ci.example.com/build/1",
		Description: "Build passed",
		Context:     "ci/build",
	})
	require.NoError(t, err)
	assert.Equal(t, "success", capturedBody["state"])
	assert.Equal(t, "https://ci.example.com/build/1", capturedBody["target_url"])
	assert.Equal(t, "Build passed", capturedBody["description"])
	assert.Equal(t, "ci/build", capturedBody["name"])
}

func TestSetCommitStatus_MapsFailureState(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/statuses/sha1": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedBody)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": 2, "status": "failed"})
		},
	})

	err := provider.SetCommitStatus(context.Background(), "owner", "repo", "sha1", &gitprovider.Status{
		State:   "failure",
		Context: "test",
	})
	require.NoError(t, err)
	assert.Equal(t, "failed", capturedBody["state"])
}

func TestSetCommitStatus_MapsErrorState(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}
	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/statuses/sha2": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedBody)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": 3, "status": "failed"})
		},
	})

	err := provider.SetCommitStatus(context.Background(), "owner", "repo", "sha2", &gitprovider.Status{
		State:   "error",
		Context: "test",
	})
	require.NoError(t, err)
	assert.Equal(t, "failed", capturedBody["state"])
}

// ---------- GetCombinedStatus ----------

func TestGetCombinedStatus_AllSuccess(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/commits/sha1/statuses": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"status": "success", "name": "ci/build", "target_url": "", "description": ""},
				{"status": "success", "name": "ci/test", "target_url": "", "description": ""},
			})
		},
	})

	status, err := provider.GetCombinedStatus(context.Background(), "owner", "repo", "sha1")
	require.NoError(t, err)
	assert.Equal(t, "success", status)
}

func TestGetCombinedStatus_HasFailure(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/commits/sha1/statuses": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"status": "success", "name": "ci/build"},
				{"status": "failed", "name": "ci/test"},
			})
		},
	})

	status, err := provider.GetCombinedStatus(context.Background(), "owner", "repo", "sha1")
	require.NoError(t, err)
	assert.Equal(t, "failure", status)
}

func TestGetCombinedStatus_HasPending(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/commits/sha1/statuses": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"status": "success", "name": "ci/build"},
				{"status": "pending", "name": "ci/test"},
			})
		},
	})

	status, err := provider.GetCombinedStatus(context.Background(), "owner", "repo", "sha1")
	require.NoError(t, err)
	assert.Equal(t, "pending", status)
}

func TestGetCombinedStatus_NoStatuses(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/commits/sha1/statuses": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
		},
	})

	status, err := provider.GetCombinedStatus(context.Background(), "owner", "repo", "sha1")
	require.NoError(t, err)
	assert.Equal(t, "pending", status)
}

func TestGetCombinedStatus_FailureTakesPrecedenceOverPending(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/commits/sha1/statuses": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"status": "pending", "name": "ci/build"},
				{"status": "failed", "name": "ci/test"},
				{"status": "success", "name": "ci/lint"},
			})
		},
	})

	status, err := provider.GetCombinedStatus(context.Background(), "owner", "repo", "sha1")
	require.NoError(t, err)
	assert.Equal(t, "failure", status)
}

// ---------- GetRepository ----------

func TestGetRepository_Happy(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/mygroup/myrepo": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                  123,
				"name":                "myrepo",
				"path_with_namespace": "mygroup/myrepo",
				"default_branch":      "main",
				"visibility":          "private",
				"web_url":             "https://gitlab.com/mygroup/myrepo",
				"http_url_to_repo":    "https://gitlab.com/mygroup/myrepo.git",
				"namespace":           map[string]interface{}{"full_path": "mygroup"},
			})
		},
	})

	repo, err := provider.GetRepository(context.Background(), "mygroup", "myrepo")
	require.NoError(t, err)
	assert.Equal(t, int64(123), repo.ID)
	assert.Equal(t, "myrepo", repo.Name)
	assert.Equal(t, "mygroup/myrepo", repo.FullName)
	assert.Equal(t, "mygroup", repo.Owner)
	assert.Equal(t, "main", repo.DefaultBranch)
	assert.True(t, repo.Private)
	assert.Equal(t, "https://gitlab.com/mygroup/myrepo", repo.HTMLURL)
	assert.Equal(t, "https://gitlab.com/mygroup/myrepo.git", repo.CloneURL)
}

func TestGetRepository_PublicVisibility(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/g/r": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                  1,
				"name":                "r",
				"path_with_namespace": "g/r",
				"default_branch":      "main",
				"visibility":          "public",
				"namespace":           map[string]interface{}{"full_path": "g"},
			})
		},
	})

	repo, err := provider.GetRepository(context.Background(), "g", "r")
	require.NoError(t, err)
	assert.False(t, repo.Private)
}

// ---------- GetDefaultBranch ----------

func TestGetDefaultBranch_Happy(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                  1,
				"name":                "repo",
				"path_with_namespace": "owner/repo",
				"default_branch":      "develop",
				"namespace":           map[string]interface{}{"full_path": "owner"},
			})
		},
	})

	branch, err := provider.GetDefaultBranch(context.Background(), "owner", "repo")
	require.NoError(t, err)
	assert.Equal(t, "develop", branch)
}

// ---------- GetFileContent ----------

func TestGetFileContent_Happy(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/files/path/to/file.yaml": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "main", r.URL.Query().Get("ref"))
			w.Header().Set("Content-Type", "application/json")
			// GitLab returns base64 content
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"file_name": "file.yaml",
				"file_path": "path/to/file.yaml",
				"content":   "aGVsbG8gd29ybGQ=", // "hello world"
				"encoding":  "base64",
			})
		},
	})

	content, err := provider.GetFileContent(context.Background(), "owner", "repo", "path/to/file.yaml", "main")
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(content))
}

func TestGetFileContent_NotFound(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/files/missing.txt": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "404 File Not Found"})
		},
	})

	_, err := provider.GetFileContent(context.Background(), "owner", "repo", "missing.txt", "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get file content")
}

// ---------- GetDirectoryContent ----------

func TestGetDirectoryContent_Happy(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/tree": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "src", r.URL.Query().Get("path"))
			assert.Equal(t, "main", r.URL.Query().Get("ref"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "sha1", "name": "main.go", "type": "blob", "path": "src/main.go"},
				{"id": "sha2", "name": "utils", "type": "tree", "path": "src/utils"},
				{"id": "sha3", "name": "vendor", "type": "commit", "path": "src/vendor"},
			})
		},
	})

	nodes, err := provider.GetDirectoryContent(context.Background(), "owner", "repo", "src", "main")
	require.NoError(t, err)
	require.Len(t, nodes, 3)

	assert.Equal(t, "main.go", nodes[0].Name)
	assert.Equal(t, "file", nodes[0].Type)
	assert.Equal(t, "sha1", nodes[0].SHA)
	assert.Equal(t, "src/main.go", nodes[0].Path)

	assert.Equal(t, "utils", nodes[1].Name)
	assert.Equal(t, "dir", nodes[1].Type)

	assert.Equal(t, "vendor", nodes[2].Name)
	assert.Equal(t, "submodule", nodes[2].Type)
}

// ---------- ValidateWebhookSignature ----------

func TestAPIValidateWebhookSignature_ValidToken(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", nil)
	req.Header.Set("X-Gitlab-Token", "my-secret")

	err := provider.ValidateWebhookSignature(req, []byte("my-secret"))
	assert.NoError(t, err)
}

func TestAPIValidateWebhookSignature_InvalidToken(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", nil)
	req.Header.Set("X-Gitlab-Token", "wrong")

	err := provider.ValidateWebhookSignature(req, []byte("correct"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid webhook token")
	var wve *gitprovider.WebhookValidationError
	assert.ErrorAs(t, err, &wve)
}

func TestAPIValidateWebhookSignature_EmptySecret(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", nil)

	err := provider.ValidateWebhookSignature(req, []byte{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook secret is not configured")
}

// ---------- ParseWebhook ----------

func TestParseWebhook_MergeEvent(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	payload := `{
		"object_kind": "merge_request",
		"user": {"username": "testuser"},
		"project": {"id": 1, "name": "repo", "path_with_namespace": "g/repo", "namespace": "g", "default_branch": "main", "web_url": "https://gitlab.com/g/repo"},
		"object_attributes": {
			"iid": 42,
			"title": "Test MR",
			"description": "body",
			"state": "opened",
			"source_branch": "feature",
			"target_branch": "main",
			"action": "open",
			"url": "https://gitlab.com/g/repo/-/merge_requests/42",
			"last_commit": {"id": "sha123"}
		}
	}`

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Gitlab-Event", "Merge Request Hook")

	event, err := provider.ParseWebhook(req, nil)
	require.NoError(t, err)
	assert.Equal(t, gitprovider.EventTypeMergeRequest, event.Type())

	mrEvent, ok := event.(*GitLabMergeRequestEvent)
	require.True(t, ok)
	assert.Equal(t, "opened", mrEvent.Action())
	assert.Equal(t, "testuser", mrEvent.Sender())

	pr := mrEvent.PullRequest()
	assert.Equal(t, 42, pr.Number)
	assert.Equal(t, "Test MR", pr.Title)
	assert.Equal(t, "feature", pr.HeadRef)
	assert.Equal(t, "main", pr.BaseRef)
}

func TestParseWebhook_PushEvent(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	payload := `{
		"object_kind": "push",
		"ref": "refs/heads/main",
		"before": "aaa",
		"after": "bbb",
		"user_username": "pusher",
		"project_id": 1,
		"project": {"name": "repo", "path_with_namespace": "g/repo", "namespace": "g", "default_branch": "main"}
	}`

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Gitlab-Event", "Push Hook")

	event, err := provider.ParseWebhook(req, nil)
	require.NoError(t, err)
	assert.Equal(t, gitprovider.EventTypePush, event.Type())

	pushEvent, ok := event.(*GitLabPushEvent)
	require.True(t, ok)
	assert.Equal(t, "refs/heads/main", pushEvent.Ref())
	assert.Equal(t, "aaa", pushEvent.Before())
	assert.Equal(t, "bbb", pushEvent.After())
	assert.Equal(t, "pusher", pushEvent.Sender())
}

func TestParseWebhook_NoteEvent(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	// GitLab API field name (intentional spelling)
	noteField := "noteable" + "_type" //nolint:misspell // GitLab API field
	payload := `{
		"object_kind": "note",
		"event_type": "note",
		"user": {"username": "commenter"},
		"project_id": 1,
		"project": {"name": "repo", "path_with_namespace": "g/repo", "namespace": "g", "default_branch": "main"},
		"object_attributes": {"id": 100, "note": "LGTM", "` + noteField + `": "MergeRequest", "url": "https://gitlab.com/note/100"},
		"merge_request": {"iid": 5, "title": "Fix", "description": "desc", "state": "opened", "source_branch": "fix", "target_branch": "main", "url": "https://gitlab.com/mr/5"}
	}`

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Gitlab-Event", "Note Hook")

	event, err := provider.ParseWebhook(req, nil)
	require.NoError(t, err)
	assert.Equal(t, gitprovider.EventTypeNote, event.Type())

	noteEvent, ok := event.(*GitLabNoteEvent)
	require.True(t, ok)
	assert.Equal(t, "created", noteEvent.Action())
	assert.Equal(t, "commenter", noteEvent.Sender())

	comment := noteEvent.Comment()
	assert.Equal(t, int64(100), comment.ID)
	assert.Equal(t, "LGTM", comment.Body)
}

func TestParseWebhook_UnknownEvent(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	payload := `{"object_kind": "pipeline"}`

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Gitlab-Event", "Pipeline Hook")

	event, err := provider.ParseWebhook(req, nil)
	require.NoError(t, err)
	assert.Equal(t, gitprovider.EventTypeUnknown, event.Type())
	assert.Nil(t, event.Repository())
}

func TestParseWebhook_InvalidBody(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader("not json"))
	req.Header.Set("X-Gitlab-Event", "Merge Request Hook")

	_, err := provider.ParseWebhook(req, nil)
	require.Error(t, err)
	var wve *gitprovider.WebhookValidationError
	assert.ErrorAs(t, err, &wve)
}

func TestParseWebhook_EmptyBody(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", bytes.NewReader(nil))
	req.Header.Set("X-Gitlab-Event", "Merge Request Hook")

	_, err := provider.ParseWebhook(req, nil)
	require.Error(t, err)
}

// ---------- getProjectPath ----------

func TestAPIGetProjectPath_WithIntProjectID(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})
	provider.projectID = 42

	assert.Equal(t, "42", provider.getProjectPath("ignored", "ignored"))
}

func TestAPIGetProjectPath_WithStringProjectID(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})
	provider.projectID = "my-group/my-project"

	assert.Equal(t, "my-group/my-project", provider.getProjectPath("ignored", "ignored"))
}

func TestAPIGetProjectPath_NoProjectID(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	assert.Equal(t, "owner/repo", provider.getProjectPath("owner", "repo"))
}

// ---------- countDiffStats ----------

func TestCountDiffStats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patch    string
		wantAdds int
		wantDels int
	}{
		{
			name:     "mixed additions and deletions",
			patch:    "--- a/file.go\n+++ b/file.go\n@@ -1,3 +1,3 @@\n context\n-old line\n+new line\n context\n",
			wantAdds: 1,
			wantDels: 1,
		},
		{
			name:     "only additions",
			patch:    "+line1\n+line2\n+line3\n",
			wantAdds: 3,
			wantDels: 0,
		},
		{
			name:     "only deletions",
			patch:    "-line1\n-line2\n",
			wantAdds: 0,
			wantDels: 2,
		},
		{
			name:     "empty patch",
			patch:    "",
			wantAdds: 0,
			wantDels: 0,
		},
		{
			name:     "header lines ignored",
			patch:    "--- a/f.go\n+++ b/f.go\n+real add\n-real del\n",
			wantAdds: 1,
			wantDels: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			adds, dels := countDiffStats(tt.patch)
			assert.Equal(t, tt.wantAdds, adds)
			assert.Equal(t, tt.wantDels, dels)
		})
	}
}

// ---------- GetCommitStatus ----------

func TestGetCommitStatus_Happy(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/projects/owner/repo/repository/commits/sha1/statuses": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"status": "success", "name": "ci/build", "target_url": "https://ci.example.com/1", "description": "Build passed"},
				{"status": "failed", "name": "ci/test", "target_url": "", "description": "Test failed"},
				{"status": "running", "name": "ci/deploy", "target_url": "", "description": ""},
				{"status": "canceled", "name": "ci/optional", "target_url": "", "description": ""},
			})
		},
	})

	statuses, err := provider.GetCommitStatus(context.Background(), "owner", "repo", "sha1")
	require.NoError(t, err)
	require.Len(t, statuses, 4)

	assert.Equal(t, "success", statuses[0].State)
	assert.Equal(t, "ci/build", statuses[0].Context)
	assert.Equal(t, "https://ci.example.com/1", statuses[0].TargetURL)

	// "failed" maps to "failure"
	assert.Equal(t, "failure", statuses[1].State)

	// "running" maps to "pending"
	assert.Equal(t, "pending", statuses[2].State)

	// "canceled" maps to "error"
	assert.Equal(t, "error", statuses[3].State)
}

// ---------- updateLastResponse ----------

func TestUpdateLastResponse_Nil(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	// Nil response should not panic
	provider.updateLastResponse(nil)
	assert.Nil(t, provider.GetAPIResponse())
}

func TestUpdateLastResponse_WithRateLimitHeaders(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	resetTime := time.Now().Add(time.Hour)
	resp := &gitlab.Response{
		Response: &http.Response{
			StatusCode: 200,
			Header: http.Header{
				"X-Request-Id":        []string{"req-123"},
				"Ratelimit-Limit":     []string{"2000"},
				"Ratelimit-Remaining": []string{"1999"},
				"Ratelimit-Reset":     []string{fmt.Sprintf("%d", resetTime.Unix())},
			},
		},
	}
	provider.updateLastResponse(resp)

	apiResp := provider.GetAPIResponse()
	require.NotNil(t, apiResp)
	assert.Equal(t, 200, apiResp.StatusCode)
	assert.Equal(t, "req-123", apiResp.RequestID)
	require.NotNil(t, apiResp.RateLimit)
	assert.Equal(t, 2000, apiResp.RateLimit.Limit)
	assert.Equal(t, 1999, apiResp.RateLimit.Remaining)
	assert.Equal(t, resetTime.Unix(), apiResp.RateLimit.Reset.Unix())
}

func TestUpdateLastResponse_WithoutRateLimitHeaders(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{})

	resp := &gitlab.Response{
		Response: &http.Response{
			StatusCode: 201,
			Header:     http.Header{},
		},
	}
	provider.updateLastResponse(resp)

	apiResp := provider.GetAPIResponse()
	require.NotNil(t, apiResp)
	assert.Equal(t, 201, apiResp.StatusCode)
	assert.Nil(t, apiResp.RateLimit)
}

// ---------- GetBotIdentity ----------

func TestGetBotIdentity_Happy(t *testing.T) {
	t.Parallel()

	_, provider := newTestServer(t, map[string]http.HandlerFunc{
		"/user": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       42,
				"username": "bot-user",
				"name":     "Bot User",
				"email":    "bot@example.com",
				"web_url":  "https://gitlab.com/bot-user",
				"bot":      true,
			})
		},
	})

	user, err := provider.GetBotIdentity(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(42), user.ID)
	assert.Equal(t, "bot-user", user.Login)
	assert.Equal(t, "Bot User", user.Name)
	assert.Equal(t, "bot@example.com", user.Email)
	assert.Equal(t, "https://gitlab.com/bot-user", user.HTMLURL)
	assert.True(t, user.IsBot)
}
