package gitlab

import (
	"context"
	"net/http"
	"testing"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

func newTestProvider(t *testing.T) *GitLabProvider {
	t.Helper()
	config := &gitprovider.ProviderConfig{
		Type:  gitprovider.ProviderTypeGitLab,
		Token: "fake_token",
	}
	provider, err := NewGitLabProvider(config)
	require.NoError(t, err)
	return provider
}

func TestValidateWebhookSignature_ValidToken(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/webhook", nil)
	req.Header.Set("X-Gitlab-Token", "my-secret-token")

	err := provider.ValidateWebhookSignature(req, []byte("my-secret-token"))
	assert.NoError(t, err)
}

func TestValidateWebhookSignature_InvalidToken(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/webhook", nil)
	req.Header.Set("X-Gitlab-Token", "wrong-token")

	err := provider.ValidateWebhookSignature(req, []byte("my-secret-token"))
	assert.Error(t, err)
	assert.IsType(t, &gitprovider.WebhookValidationError{}, err)
	assert.Contains(t, err.Error(), "invalid webhook token")
}

func TestValidateWebhookSignature_MissingHeader(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/webhook", nil)

	err := provider.ValidateWebhookSignature(req, []byte("my-secret-token"))
	assert.Error(t, err)
	assert.IsType(t, &gitprovider.WebhookValidationError{}, err)
}

func TestValidateWebhookSignature_EmptySecretRejected(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/webhook", nil)
	req.Header.Set("X-Gitlab-Token", "")

	err := provider.ValidateWebhookSignature(req, []byte(""))
	assert.Error(t, err)
	assert.IsType(t, &gitprovider.WebhookValidationError{}, err)
	assert.Contains(t, err.Error(), "webhook secret is not configured")
}

func TestValidateWebhookSignature_DifferentLength(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/webhook", nil)
	req.Header.Set("X-Gitlab-Token", "short")

	err := provider.ValidateWebhookSignature(req, []byte("much-longer-secret-token"))
	assert.Error(t, err)
}

func TestMergeRequestEvent_ActionMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		gitlabAction string
		expected     string
	}{
		{"open", "opened"},
		{"reopen", "reopened"},
		{"update", "synchronize"},
		{"merge", "closed"},
		{"close", "closed"},
		{"approved", "approved"},
	}

	for _, tt := range tests {
		t.Run(tt.gitlabAction, func(t *testing.T) {
			t.Parallel()
			event := &GitLabMergeRequestEvent{
				event: &gitlab.MergeEvent{
					ObjectAttributes: gitlab.MergeEventObjectAttributes{
						Action: tt.gitlabAction,
					},
				},
			}
			assert.Equal(t, tt.expected, event.Action())
		})
	}
}

func TestMergeRequestEvent_PullRequest(t *testing.T) {
	t.Parallel()

	event := &GitLabMergeRequestEvent{
		event: &gitlab.MergeEvent{
			User: &gitlab.EventUser{Username: "testuser"},
			ObjectAttributes: gitlab.MergeEventObjectAttributes{
				IID:          42,
				Title:        "Test MR",
				Description:  "Test body",
				State:        "merged",
				SourceBranch: "feature-branch",
				TargetBranch: "main",
				URL:          "https://gitlab.com/group/repo/-/merge_requests/42",
				LastCommit:   gitlab.EventMergeRequestLastCommit{ID: "abc123"},
			},
		},
	}

	pr := event.PullRequest()
	assert.Equal(t, 42, pr.Number)
	assert.Equal(t, "Test MR", pr.Title)
	assert.Equal(t, "Test body", pr.Body)
	assert.Equal(t, "merged", pr.State)
	assert.Equal(t, "testuser", pr.Author)
	assert.Equal(t, "feature-branch", pr.HeadRef)
	assert.Equal(t, "main", pr.BaseRef)
	assert.Equal(t, "abc123", pr.HeadSHA)
	assert.True(t, pr.Merged)
	assert.Equal(t, "https://gitlab.com/group/repo/-/merge_requests/42", pr.HTMLURL)
	assert.Empty(t, pr.Labels)
}

func TestMergeRequestEvent_Type(t *testing.T) {
	t.Parallel()
	event := &GitLabMergeRequestEvent{event: &gitlab.MergeEvent{}}
	assert.Equal(t, gitprovider.EventTypeMergeRequest, event.Type())
}

func TestMergeRequestEvent_Repository(t *testing.T) {
	t.Parallel()

	event := &GitLabMergeRequestEvent{
		event: &gitlab.MergeEvent{
			Project: gitlab.MergeEventProject{
				ID:                123,
				Name:              "myrepo",
				PathWithNamespace: "mygroup/myrepo",
				Namespace:         "mygroup",
				DefaultBranch:     "main",
				WebURL:            "https://gitlab.com/mygroup/myrepo",
				GitHTTPURL:        "https://gitlab.com/mygroup/myrepo.git",
				Visibility:        gitlab.PrivateVisibility,
			},
		},
	}

	repo := event.Repository()
	assert.Equal(t, int64(123), repo.ID)
	assert.Equal(t, "myrepo", repo.Name)
	assert.Equal(t, "mygroup/myrepo", repo.FullName)
	assert.Equal(t, "mygroup", repo.Owner)
	assert.Equal(t, "main", repo.DefaultBranch)
	assert.True(t, repo.Private)
	assert.Equal(t, "https://gitlab.com/mygroup/myrepo", repo.HTMLURL)
	assert.Equal(t, "https://gitlab.com/mygroup/myrepo.git", repo.CloneURL)
}

func TestPushEvent_Fields(t *testing.T) {
	t.Parallel()

	event := &GitLabPushEvent{
		event: &gitlab.PushEvent{
			Ref:          "refs/heads/main",
			Before:       "aaa111",
			After:        "bbb222",
			UserUsername: "pusher",
			ProjectID:    99,
			Project: gitlab.PushEventProject{
				Name:              "testrepo",
				PathWithNamespace: "group/testrepo",
				Namespace:         "group",
				DefaultBranch:     "main",
			},
			Commits: []*gitlab.PushEventCommit{
				{
					ID:      "commit1",
					Message: "first commit",
					Author:  gitlab.EventCommitAuthor{Name: "Author One", Email: "a@b.com"},
					URL:     "https://gitlab.com/commit/1",
				},
			},
		},
	}

	assert.Equal(t, gitprovider.EventTypePush, event.Type())
	assert.Equal(t, "refs/heads/main", event.Ref())
	assert.Equal(t, "aaa111", event.Before())
	assert.Equal(t, "bbb222", event.After())
	assert.Equal(t, "pusher", event.Sender())

	commits := event.Commits()
	require.Len(t, commits, 1)
	assert.Equal(t, "commit1", commits[0].SHA)
	assert.Equal(t, "first commit", commits[0].Message)
	assert.Equal(t, "Author One", commits[0].Author)

	repo := event.Repository()
	assert.Equal(t, int64(99), repo.ID)
	assert.Equal(t, "group/testrepo", repo.FullName)
}

func TestNoteEvent_Fields(t *testing.T) {
	t.Parallel()

	event := &GitLabNoteEvent{
		event: &gitlab.MergeCommentEvent{
			User:      &gitlab.EventUser{Username: "commenter"},
			ProjectID: 55,
			Project: gitlab.MergeCommentEventProject{
				Name:              "noterepo",
				PathWithNamespace: "org/noterepo",
				Namespace:         "org",
				DefaultBranch:     "main",
			},
			ObjectAttributes: gitlab.MergeCommentEventObjectAttributes{
				ID:   999,
				Note: "LGTM!",
				URL:  "https://gitlab.com/org/noterepo/-/notes/999",
			},
			MergeRequest: gitlab.MergeCommentEventMergeRequest{
				IID:          10,
				Title:        "Fix bug",
				Description:  "Fixes the bug",
				State:        "opened",
				SourceBranch: "fix-branch",
				TargetBranch: "main",
				URL:          "https://gitlab.com/org/noterepo/-/merge_requests/10",
			},
		},
	}

	assert.Equal(t, gitprovider.EventTypeNote, event.Type())
	assert.Equal(t, "created", event.Action())
	assert.Equal(t, "commenter", event.Sender())

	pr := event.Issue()
	assert.Equal(t, 10, pr.Number)
	assert.Equal(t, "Fix bug", pr.Title)
	assert.Equal(t, "fix-branch", pr.HeadRef)
	assert.Equal(t, "main", pr.BaseRef)

	comment := event.Comment()
	assert.Equal(t, int64(999), comment.ID)
	assert.Equal(t, "LGTM!", comment.Body)
	assert.Equal(t, "commenter", comment.Author)
}

func TestUnknownEvent(t *testing.T) {
	t.Parallel()
	event := &GitLabUnknownEvent{}
	assert.Equal(t, gitprovider.EventTypeUnknown, event.Type())
	assert.Nil(t, event.Repository())
}
