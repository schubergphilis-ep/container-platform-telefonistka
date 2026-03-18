package gitlabapi

import (
	"testing"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Realistic end-to-end handler tests ---
// These test the full flow through HandleGitLabEvent → handler → promotion/drift
// using realistic event objects (not minimal stubs).

// realisticMREvent creates an event that looks like a real GitLab MR webhook.
type realisticMREvent struct {
	action string
	merged bool
}

func (e *realisticMREvent) Type() gitprovider.EventType { return gitprovider.EventTypeMergeRequest }
func (e *realisticMREvent) Action() string              { return e.action }
func (e *realisticMREvent) Sender() string              { return "developer" }
func (e *realisticMREvent) Repository() *gitprovider.Repository {
	return &gitprovider.Repository{
		Name:          "infra-repo",
		FullName:      "platform-team/infra-repo",
		Owner:         "platform-team",
		DefaultBranch: "main",
		HTMLURL:       "https://gitlab.example.com/platform-team/infra-repo",
	}
}

func (e *realisticMREvent) PullRequest() *gitprovider.PullRequest {
	return &gitprovider.PullRequest{
		Number:  42,
		Title:   "Update app-x image to v2.0",
		HeadRef: "feat/update-app-x",
		BaseRef: "main",
		HeadSHA: "abc123def456",
		BaseSHA: "000000aaaaaa",
		Author:  "developer",
		State:   "opened",
		Merged:  e.merged,
		Labels:  []string{"promotion"},
		HTMLURL: "https://gitlab.example.com/platform-team/infra-repo/-/merge_requests/42",
	}
}

var _ gitprovider.PullRequestEvent = (*realisticMREvent)(nil)

// realisticPushEvent creates a push-to-default-branch event.
type realisticPushEvent struct{}

func (e *realisticPushEvent) Type() gitprovider.EventType { return gitprovider.EventTypePush }
func (e *realisticPushEvent) Sender() string              { return "developer" }
func (e *realisticPushEvent) Ref() string                 { return "main" }
func (e *realisticPushEvent) Before() string              { return "aaa111" }
func (e *realisticPushEvent) After() string               { return "bbb222" }
func (e *realisticPushEvent) Commits() []gitprovider.Commit {
	return []gitprovider.Commit{
		{SHA: "bbb222", Message: "Merge branch 'feat/update' into 'main'\n\nSee merge request platform-team/infra-repo!42", Author: "developer"},
	}
}

func (e *realisticPushEvent) Repository() *gitprovider.Repository {
	return &gitprovider.Repository{
		Name:          "infra-repo",
		FullName:      "platform-team/infra-repo",
		Owner:         "platform-team",
		DefaultBranch: "main",
		HTMLURL:       "https://gitlab.example.com/platform-team/infra-repo",
	}
}

var _ gitprovider.PushEvent = (*realisticPushEvent)(nil)

// realisticNoteEvent creates a /retrigger comment on an MR.
type realisticNoteEvent struct {
	body string
}

func (e *realisticNoteEvent) Type() gitprovider.EventType { return gitprovider.EventTypeNote }
func (e *realisticNoteEvent) Action() string              { return "created" }
func (e *realisticNoteEvent) Sender() string              { return "reviewer" }
func (e *realisticNoteEvent) Repository() *gitprovider.Repository {
	return &gitprovider.Repository{
		Name:          "infra-repo",
		FullName:      "platform-team/infra-repo",
		Owner:         "platform-team",
		DefaultBranch: "main",
		HTMLURL:       "https://gitlab.example.com/platform-team/infra-repo",
	}
}

func (e *realisticNoteEvent) Issue() *gitprovider.PullRequest {
	return &gitprovider.PullRequest{
		Number:  42,
		Title:   "Update app-x image to v2.0",
		HeadRef: "feat/update-app-x",
		HeadSHA: "abc123def456",
		Author:  "developer",
		State:   "opened",
	}
}

func (e *realisticNoteEvent) Comment() *gitprovider.Comment {
	return &gitprovider.Comment{ID: 999, Body: e.body, Author: "reviewer"}
}

var _ gitprovider.IssueCommentEvent = (*realisticNoteEvent)(nil)

// fullProvider creates a MockProvider with telefonistka.yaml, source, and target directories.
func fullProvider() *testutils.MockProvider {
	return &testutils.MockProvider{
		DefaultBranchName: "main",
		Files: map[string][]byte{
			"telefonistka.yaml@main": []byte(`
promotionPaths:
  - sourcePath: "env/dev/"
    promotionPrs:
      - targetPaths: ["env/staging/"]
        targetDescription: "staging"
`),
			"env/dev/app-x/values.yaml@main":     []byte("image: app-x:v2.0\n"),
			"env/staging/app-x/values.yaml@main": []byte("image: app-x:v1.0\n"),
		},
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/app-x@main": {
				{Name: "values.yaml", Path: "env/dev/app-x/values.yaml", Type: "file", SHA: "sha_new"},
			},
			"env/staging/app-x@main": {
				{Name: "values.yaml", Path: "env/staging/app-x/values.yaml", Type: "file", SHA: "sha_old"},
			},
		},
		PullRequestFiles: []*gitprovider.CommitFile{
			{Filename: "env/dev/app-x/values.yaml"},
		},
		PullRequestResponse: &gitprovider.PullRequest{
			Number:  42,
			Title:   "Update app-x image to v2.0",
			Body:    "Changed app-x image",
			HeadRef: "feat/update-app-x",
			HeadSHA: "abc123def456",
			Author:  "developer",
			State:   "merged",
			Merged:  true,
			Labels:  []string{"promotion"},
		},
	}
}

// --- MR Opened → Drift Detection ---

func TestE2E_MROpened_DriftDetection_NoDrift(t *testing.T) {
	t.Parallel()
	provider := fullProvider()
	// Make source and target identical → no drift
	provider.Directories["env/staging/app-x@main"] = []*gitprovider.FileNode{
		{Name: "values.yaml", Path: "env/staging/app-x/values.yaml", Type: "file", SHA: "sha_new"},
	}

	event := &realisticMREvent{action: "opened"}
	err := HandleGitLabEvent(event, provider, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, provider.CallCount("GetDefaultBranch"))
	assert.GreaterOrEqual(t, 1, provider.CallCount("ListPullRequestFiles"))
	// No drift comment should be posted (source SHA == target SHA)
	assert.Equal(t, 0, provider.CallCount("CommentOnPullRequest"))
	// Commit status should be set (pending → success)
	assert.GreaterOrEqual(t, len(provider.StatusUpdates), 2)
	assert.Contains(t, provider.StatusUpdates[0], "pending")
}

func TestE2E_MROpened_DriftDetection_DriftFound(t *testing.T) {
	t.Parallel()
	provider := fullProvider()
	// Source and target have different SHAs → drift detected

	event := &realisticMREvent{action: "opened"}
	err := HandleGitLabEvent(event, provider, nil)

	require.NoError(t, err)
	// Drift comment should be posted
	assert.GreaterOrEqual(t, provider.CallCount("CommentOnPullRequest"), 1)
	assert.Contains(t, provider.LastComment, "drift")
}

func TestE2E_MROpened_NoConfig(t *testing.T) {
	t.Parallel()
	provider := &testutils.MockProvider{
		DefaultBranchName: "main",
		Files:             map[string][]byte{}, // no telefonistka.yaml
		PullRequestFiles: []*gitprovider.CommitFile{
			{Filename: "env/dev/app-x/values.yaml"},
		},
	}

	event := &realisticMREvent{action: "opened"}
	err := HandleGitLabEvent(event, provider, nil)

	// Should return error (no config)
	assert.Error(t, err)
	// Error comment should be posted
	assert.GreaterOrEqual(t, provider.CallCount("CommentOnPullRequest"), 1)
}

// --- MR Merged → Promotion ---

func TestE2E_MRMerged_PromotionCreated(t *testing.T) {
	t.Parallel()
	provider := fullProvider()

	event := &realisticMREvent{action: "merged", merged: true}
	err := HandleGitLabEvent(event, provider, nil)

	require.NoError(t, err)
	// Should create a promotion PR
	assert.Equal(t, 1, provider.CallCount("CreatePullRequest"))
	require.NotNil(t, provider.LastCreatePR)
	assert.Contains(t, provider.LastCreatePR.Title, "app-x")
	assert.Contains(t, provider.LastCreatePR.Title, "staging")
	assert.Equal(t, "main", provider.LastCreatePR.Base)
	// Should have commit actions
	assert.Equal(t, 1, provider.CallCount("CreateCommit"))
}

func TestE2E_MRMerged_NoMatchingFiles(t *testing.T) {
	t.Parallel()
	provider := fullProvider()
	// Changed files don't match any promotion path
	provider.PullRequestFiles = []*gitprovider.CommitFile{
		{Filename: "docs/README.md"},
	}

	event := &realisticMREvent{action: "merged", merged: true}
	err := HandleGitLabEvent(event, provider, nil)

	require.NoError(t, err)
	// No promotion PR should be created
	assert.Equal(t, 0, provider.CallCount("CreatePullRequest"))
}

func TestE2E_MRClosed_NotMerged(t *testing.T) {
	t.Parallel()
	provider := fullProvider()

	event := &realisticMREvent{action: "closed", merged: false}
	err := HandleGitLabEvent(event, provider, nil)

	require.NoError(t, err)
	// Nothing should happen — no drift, no promotion
	assert.Equal(t, 0, provider.CallCount("CreatePullRequest"))
}

func TestE2E_MRAction_Ignored(t *testing.T) {
	t.Parallel()
	provider := fullProvider()

	event := &realisticMREvent{action: "approved"}
	err := HandleGitLabEvent(event, provider, nil)

	require.NoError(t, err)
	// Unrecognized action → no API calls beyond status
	assert.Equal(t, 0, provider.CallCount("CreatePullRequest"))
	assert.Equal(t, 0, provider.CallCount("ListPullRequestFiles"))
}

// --- Comment Events ---

func TestE2E_CommentRetrigger(t *testing.T) {
	t.Parallel()
	provider := fullProvider()
	// Set up PullRequestResponse so the retrigger can fetch full MR context
	provider.PullRequestResponse = &gitprovider.PullRequest{
		Number:  42,
		HeadRef: "feat/update-app-x",
		HeadSHA: "abc123def456",
		Author:  "developer",
		Labels:  []string{"promotion"},
	}

	event := &realisticNoteEvent{body: "/retrigger"}
	err := HandleGitLabEvent(event, provider, nil)

	require.NoError(t, err)
	// Should fetch MR and run drift detection
	assert.GreaterOrEqual(t, provider.CallCount("GetPullRequest"), 1)
	assert.GreaterOrEqual(t, provider.CallCount("GetDefaultBranch"), 1)
}

func TestE2E_CommentNotCommand(t *testing.T) {
	t.Parallel()
	provider := fullProvider()

	event := &realisticNoteEvent{body: "Looks good to me!"}
	err := HandleGitLabEvent(event, provider, nil)

	require.NoError(t, err)
	// Non-command comment → no retrigger, no PR fetch
	assert.Equal(t, 0, provider.CallCount("GetPullRequest"))
}

func TestE2E_CommentFromBot_Ignored(t *testing.T) {
	t.Parallel()
	provider := fullProvider()

	// The bot's identity is "telefonistka-bot" (MockProvider default)
	// Sender is "reviewer" which != bot → should NOT be ignored
	// Let's test the opposite: sender IS the bot
	botEvent := &realisticNoteEvent{body: "/retrigger"}
	// Override sender to match bot identity
	err := HandleGitLabEvent(&botSenderNoteEvent{realisticNoteEvent: *botEvent}, provider, nil)

	require.NoError(t, err)
	// Bot comment should be ignored — no MR fetch
	assert.Equal(t, 0, provider.CallCount("GetPullRequest"))
}

// botSenderNoteEvent wraps realisticNoteEvent but returns the bot's own username as sender
type botSenderNoteEvent struct {
	realisticNoteEvent
}

func (e *botSenderNoteEvent) Sender() string { return "telefonistka-bot" }

var _ gitprovider.IssueCommentEvent = (*botSenderNoteEvent)(nil)

// --- Push Events ---

func TestE2E_PushEvent_LogsWarning(t *testing.T) {
	t.Parallel()
	provider := fullProvider()

	event := &realisticPushEvent{}
	err := HandleGitLabEvent(event, provider, nil)

	require.NoError(t, err)
	// Push handler logs a warning but doesn't fail
	// No promotion should be created (push handling not implemented for webhook path)
	assert.Equal(t, 0, provider.CallCount("CreatePullRequest"))
}

// --- Nil guard tests ---

func TestE2E_MREvent_NilPullRequest(t *testing.T) {
	t.Parallel()
	event := &nilPREvent{}
	err := HandleGitLabEvent(event, nil, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestE2E_CommentEvent_NilFields(t *testing.T) {
	t.Parallel()
	event := &nilCommentEvent{}
	err := HandleGitLabEvent(event, nil, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

// nilPREvent returns nil from PullRequest() to test nil guard
type nilPREvent struct{}

func (e *nilPREvent) Type() gitprovider.EventType           { return gitprovider.EventTypeMergeRequest }
func (e *nilPREvent) Action() string                        { return "opened" }
func (e *nilPREvent) Sender() string                        { return "user" }
func (e *nilPREvent) Repository() *gitprovider.Repository   { return nil }
func (e *nilPREvent) PullRequest() *gitprovider.PullRequest { return nil }

var _ gitprovider.PullRequestEvent = (*nilPREvent)(nil)

// nilCommentEvent returns nil from all fields
type nilCommentEvent struct{}

func (e *nilCommentEvent) Type() gitprovider.EventType         { return gitprovider.EventTypeNote }
func (e *nilCommentEvent) Action() string                      { return "created" }
func (e *nilCommentEvent) Sender() string                      { return "user" }
func (e *nilCommentEvent) Repository() *gitprovider.Repository { return nil }
func (e *nilCommentEvent) Issue() *gitprovider.PullRequest     { return nil }
func (e *nilCommentEvent) Comment() *gitprovider.Comment       { return nil }

var _ gitprovider.IssueCommentEvent = (*nilCommentEvent)(nil)
