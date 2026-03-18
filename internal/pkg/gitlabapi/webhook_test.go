package gitlabapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- parseRepoFullName ---

func TestParseRepoFullName_Simple(t *testing.T) {
	t.Parallel()
	owner, repo, err := parseRepoFullName("mygroup/myrepo")
	require.NoError(t, err)
	assert.Equal(t, "mygroup", owner)
	assert.Equal(t, "myrepo", repo)
}

func TestParseRepoFullName_NestedGroups(t *testing.T) {
	t.Parallel()
	owner, repo, err := parseRepoFullName("org/subgroup/deep/myrepo")
	require.NoError(t, err)
	assert.Equal(t, "org/subgroup/deep", owner)
	assert.Equal(t, "myrepo", repo)
}

func TestParseRepoFullName_NoSlash(t *testing.T) {
	t.Parallel()
	_, _, err := parseRepoFullName("noslash")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected owner/repo format")
}

func TestParseRepoFullName_Empty(t *testing.T) {
	t.Parallel()
	_, _, err := parseRepoFullName("")
	assert.Error(t, err)
}

func TestParseRepoFullName_TrailingSlash(t *testing.T) {
	t.Parallel()
	owner, repo, err := parseRepoFullName("group/")
	require.NoError(t, err)
	assert.Equal(t, "group", owner)
	assert.Equal(t, "", repo)
}

// --- ReceiveGitLabWebhook ---
// These tests use t.Setenv and cannot be parallel.

func newCaches(t *testing.T) (*lru.Cache[string, gitprovider.GitProvider], *lru.Cache[string, gitprovider.GitProvider]) {
	t.Helper()
	main, err := lru.New[string, gitprovider.GitProvider](4)
	require.NoError(t, err)
	approver, err := lru.New[string, gitprovider.GitProvider](4)
	require.NoError(t, err)
	return main, approver
}

func TestReceiveGitLabWebhook_MissingGitLabToken(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	mainCache, approverCache := newCaches(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(`{}`))
	req.Header.Set("X-Gitlab-Event", "Merge Request Hook")
	req.Header.Set("Content-Type", "application/json")

	err := ReceiveGitLabWebhook(req, mainCache, approverCache, []byte("secret"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GITLAB_TOKEN is required")
}

func TestReceiveGitLabWebhook_InvalidSignature(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "test-token")
	t.Setenv("GITLAB_URL", "https://gitlab.com")
	mainCache, approverCache := newCaches(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(`{}`))
	req.Header.Set("X-Gitlab-Event", "Merge Request Hook")
	req.Header.Set("X-Gitlab-Token", "wrong-token")
	req.Header.Set("Content-Type", "application/json")

	err := ReceiveGitLabWebhook(req, mainCache, approverCache, []byte("correct-secret"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid webhook token")
}

func TestReceiveGitLabWebhook_MissingTokenHeader(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "test-token")
	t.Setenv("GITLAB_URL", "https://gitlab.com")
	mainCache, approverCache := newCaches(t)

	// No X-Gitlab-Token header → empty token vs non-empty secret
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(`{}`))
	req.Header.Set("X-Gitlab-Event", "Merge Request Hook")
	req.Header.Set("Content-Type", "application/json")

	err := ReceiveGitLabWebhook(req, mainCache, approverCache, []byte("a-secret"))
	assert.Error(t, err)
}

func TestReceiveGitLabWebhook_EmptySecretLogsWarningButProceeds(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "test-token")
	t.Setenv("GITLAB_URL", "https://gitlab.com")
	mainCache, approverCache := newCaches(t)

	payload := `{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"project": {"id": 1, "name": "test", "path_with_namespace": "group/test", "namespace": "group", "default_branch": "main"},
		"user": {"username": "testuser"},
		"object_attributes": {
			"iid": 1,
			"action": "open",
			"state": "opened",
			"title": "Test MR",
			"source_branch": "feature",
			"target_branch": "main",
			"last_commit": {"id": "abc123"}
		}
	}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Gitlab-Event", "Merge Request Hook")
	req.Header.Set("Content-Type", "application/json")

	// Empty secret → validation is skipped (ALLOW_UNSIGNED_WEBHOOKS mode),
	// a warning is logged but processing continues.
	err := ReceiveGitLabWebhook(req, mainCache, approverCache, []byte(""))
	assert.NoError(t, err)
}

func TestReceiveGitLabWebhook_ValidSignatureAndPayload(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "test-token")
	t.Setenv("GITLAB_URL", "https://gitlab.com")
	mainCache, approverCache := newCaches(t)

	payload := `{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"project": {"id": 1, "name": "test", "path_with_namespace": "group/test", "namespace": "group", "default_branch": "main"},
		"user": {"username": "testuser"},
		"object_attributes": {
			"iid": 42,
			"action": "open",
			"state": "opened",
			"title": "Test MR",
			"source_branch": "feature",
			"target_branch": "main",
			"last_commit": {"id": "deadbeef"}
		}
	}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Gitlab-Event", "Merge Request Hook")
	req.Header.Set("X-Gitlab-Token", "my-webhook-secret")
	req.Header.Set("Content-Type", "application/json")

	err := ReceiveGitLabWebhook(req, mainCache, approverCache, []byte("my-webhook-secret"))
	assert.NoError(t, err)
}

func TestReceiveGitLabWebhook_PushEvent(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "test-token")
	t.Setenv("GITLAB_URL", "https://gitlab.com")
	mainCache, approverCache := newCaches(t)

	payload := `{
		"object_kind": "push",
		"event_name": "push",
		"ref": "refs/heads/main",
		"before": "0000000000000000000000000000000000000000",
		"after": "abc123",
		"project_id": 1,
		"project": {"name": "test", "path_with_namespace": "group/test", "namespace": "group", "default_branch": "main"},
		"user_username": "pusher",
		"commits": []
	}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Gitlab-Event", "Push Hook")
	req.Header.Set("X-Gitlab-Token", "secret")
	req.Header.Set("Content-Type", "application/json")

	err := ReceiveGitLabWebhook(req, mainCache, approverCache, []byte("secret"))
	assert.NoError(t, err)
}

func TestReceiveGitLabWebhook_NoteEvent(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "test-token")
	t.Setenv("GITLAB_URL", "https://gitlab.com")
	mainCache, approverCache := newCaches(t)

	// GitLab API field name (intentional spelling)
	noteableType := "noteable" + "_type" //nolint:misspell // GitLab API field
	payload := `{
		"object_kind": "note",
		"event_type": "note",
		"project_id": 1,
		"project": {"name": "test", "path_with_namespace": "group/test", "namespace": "group", "default_branch": "main"},
		"user": {"username": "commenter"},
		"object_attributes": {
			"id": 100,
			"note": "LGTM!",
			"` + noteableType + `": "MergeRequest",
			"url": "https://gitlab.com/group/test/-/notes/100"
		},
		"merge_request": {
			"iid": 5,
			"title": "Fix bug",
			"state": "opened",
			"source_branch": "fix",
			"target_branch": "main",
			"url": "https://gitlab.com/group/test/-/merge_requests/5"
		}
	}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Gitlab-Event", "Note Hook")
	req.Header.Set("X-Gitlab-Token", "secret")
	req.Header.Set("Content-Type", "application/json")

	err := ReceiveGitLabWebhook(req, mainCache, approverCache, []byte("secret"))
	assert.NoError(t, err)
}

func TestReceiveGitLabWebhook_MalformedPayload(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "test-token")
	t.Setenv("GITLAB_URL", "https://gitlab.com")
	mainCache, approverCache := newCaches(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader("not json at all"))
	req.Header.Set("X-Gitlab-Event", "Merge Request Hook")
	req.Header.Set("X-Gitlab-Token", "secret")
	req.Header.Set("Content-Type", "application/json")

	err := ReceiveGitLabWebhook(req, mainCache, approverCache, []byte("secret"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse webhook")
}

func TestReceiveGitLabWebhook_CachesProvider(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "test-token")
	t.Setenv("GITLAB_URL", "https://gitlab.com")
	mainCache, approverCache := newCaches(t)

	payload := `{
		"object_kind": "push",
		"event_name": "push",
		"ref": "refs/heads/main",
		"before": "aaa",
		"after": "bbb",
		"project_id": 1,
		"project": {"name": "test", "path_with_namespace": "group/test", "namespace": "group", "default_branch": "main"},
		"user_username": "user",
		"commits": []
	}`

	// First call creates provider
	req1 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(payload))
	req1.Header.Set("X-Gitlab-Event", "Push Hook")
	req1.Header.Set("X-Gitlab-Token", "secret")
	req1.Header.Set("Content-Type", "application/json")
	err := ReceiveGitLabWebhook(req1, mainCache, approverCache, []byte("secret"))
	require.NoError(t, err)
	assert.Equal(t, 1, mainCache.Len(), "provider should be cached after first call")

	// Second call uses cached provider
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(payload))
	req2.Header.Set("X-Gitlab-Event", "Push Hook")
	req2.Header.Set("X-Gitlab-Token", "secret")
	req2.Header.Set("Content-Type", "application/json")
	err = ReceiveGitLabWebhook(req2, mainCache, approverCache, []byte("secret"))
	require.NoError(t, err)
	assert.Equal(t, 1, mainCache.Len(), "should still be 1 (reused cache)")
}

// --- HandleGitLabEvent ---

func TestHandleGitLabEvent_UnknownEventType(t *testing.T) {
	t.Parallel()
	event := &unknownEvent{}
	err := HandleGitLabEvent(event, nil, nil)
	assert.NoError(t, err) // unknown events are logged but not an error
}

func TestHandleGitLabEvent_RecoverFromPanic(t *testing.T) {
	t.Parallel()
	event := &panicEvent{}
	err := HandleGitLabEvent(event, nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "panic in HandleGitLabEvent")
}

func TestHandleGitLabEvent_PushEventDoesNotPanic(t *testing.T) {
	t.Parallel()
	event := &minimalPushEvent{}
	err := HandleGitLabEvent(event, nil, nil)
	assert.NoError(t, err)
}

func TestHandleGitLabEvent_NoteEventDoesNotPanic(t *testing.T) {
	t.Parallel()
	event := &minimalNoteEvent{body: "just a comment"}
	err := HandleGitLabEvent(event, nil, nil)
	assert.NoError(t, err)
}

// --- test helpers ---

// unknownEvent satisfies gitprovider.Event but not any specific sub-interface
type unknownEvent struct{}

func (e *unknownEvent) Type() gitprovider.EventType         { return gitprovider.EventTypeUnknown }
func (e *unknownEvent) Repository() *gitprovider.Repository { return nil }

// panicEvent triggers a panic in HandleGitLabEvent's type switch
// by satisfying PullRequestEvent but panicking in PullRequest()
type panicEvent struct{}

func (e *panicEvent) Type() gitprovider.EventType           { return gitprovider.EventTypeMergeRequest }
func (e *panicEvent) Repository() *gitprovider.Repository   { return nil }
func (e *panicEvent) Action() string                        { return "opened" }
func (e *panicEvent) Sender() string                        { return "user" }
func (e *panicEvent) PullRequest() *gitprovider.PullRequest { panic("intentional test panic") }

var (
	_ gitprovider.Event            = (*unknownEvent)(nil)
	_ gitprovider.PullRequestEvent = (*panicEvent)(nil)
)

// --- Provider detection tests ---

func TestProviderDetection_GitLabHeader(t *testing.T) {
	t.Parallel()
	headers := http.Header{}
	headers.Set("X-Gitlab-Event", "Merge Request Hook")
	assert.Equal(t, gitprovider.ProviderTypeGitLab, gitprovider.DetectProviderFromWebhook(headers))
}

func TestProviderDetection_GitHubHeader(t *testing.T) {
	t.Parallel()
	headers := http.Header{}
	headers.Set("X-Github-Event", "pull_request")
	assert.Equal(t, gitprovider.ProviderTypeGitHub, gitprovider.DetectProviderFromWebhook(headers))
}

func TestProviderDetection_NoHeaders(t *testing.T) {
	t.Parallel()
	assert.Equal(t, gitprovider.ProviderTypeUnknown, gitprovider.DetectProviderFromWebhook(http.Header{}))
}

// --- minimal event types for testing ---

type minimalPushEvent struct{}

func (e *minimalPushEvent) Type() gitprovider.EventType { return gitprovider.EventTypePush }
func (e *minimalPushEvent) Repository() *gitprovider.Repository {
	return &gitprovider.Repository{FullName: "group/repo"}
}
func (e *minimalPushEvent) Ref() string                   { return "refs/heads/main" }
func (e *minimalPushEvent) Before() string                { return "aaa" }
func (e *minimalPushEvent) After() string                 { return "bbb" }
func (e *minimalPushEvent) Commits() []gitprovider.Commit { return nil }
func (e *minimalPushEvent) Sender() string                { return "user" }

var _ gitprovider.PushEvent = (*minimalPushEvent)(nil)

type minimalNoteEvent struct{ body string }

func (e *minimalNoteEvent) Type() gitprovider.EventType { return gitprovider.EventTypeNote }
func (e *minimalNoteEvent) Repository() *gitprovider.Repository {
	return &gitprovider.Repository{FullName: "group/repo"}
}
func (e *minimalNoteEvent) Action() string { return "created" }
func (e *minimalNoteEvent) Issue() *gitprovider.PullRequest {
	return &gitprovider.PullRequest{Number: 1, Title: "test"}
}

func (e *minimalNoteEvent) Comment() *gitprovider.Comment {
	return &gitprovider.Comment{ID: 1, Body: e.body, Author: "user"}
}
func (e *minimalNoteEvent) Sender() string { return "user" }

var _ gitprovider.IssueCommentEvent = (*minimalNoteEvent)(nil)
