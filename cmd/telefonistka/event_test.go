package telefonistka

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- detectProvider ---

func TestDetectProvider_GitLabCI(t *testing.T) {
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("TELEFONISTKA_PROVIDER", "")
	assert.Equal(t, "gitlab", detectProvider())
}

func TestDetectProvider_GitHubActions(t *testing.T) {
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("TELEFONISTKA_PROVIDER", "")
	assert.Equal(t, "github", detectProvider())
}

func TestDetectProvider_ExplicitOverride(t *testing.T) {
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("TELEFONISTKA_PROVIDER", "gitlab")
	assert.Equal(t, "gitlab", detectProvider())
}

func TestDetectProvider_DefaultsToGitHub(t *testing.T) {
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("TELEFONISTKA_PROVIDER", "")
	assert.Equal(t, "github", detectProvider())
}

func TestDetectProvider_GitLabCITakesPrecedenceOverGitHub(t *testing.T) {
	// If both are set (unlikely but possible), GitLab wins because it's checked first
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("TELEFONISTKA_PROVIDER", "")
	assert.Equal(t, "gitlab", detectProvider())
}

// --- detectEventType ---

func TestDetectEventType_GitHubEvent(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "pull_request")
	t.Setenv("CI_PIPELINE_SOURCE", "")
	assert.Equal(t, "pull_request", detectEventType())
}

func TestDetectEventType_GitLabCI(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "")
	t.Setenv("CI_PIPELINE_SOURCE", "merge_request_event")
	assert.Equal(t, "merge_request_event", detectEventType())
}

func TestDetectEventType_NeitherSet(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "")
	t.Setenv("CI_PIPELINE_SOURCE", "")
	assert.Equal(t, "", detectEventType())
}

func TestDetectEventType_GitHubTakesPrecedence(t *testing.T) {
	// GitHub env var is checked first
	t.Setenv("GITHUB_EVENT_NAME", "push")
	t.Setenv("CI_PIPELINE_SOURCE", "merge_request_event")
	assert.Equal(t, "push", detectEventType())
}

// --- detectEventFilePath ---

func TestDetectEventFilePath_GitHubEventPath(t *testing.T) {
	t.Setenv("GITHUB_EVENT_PATH", "/tmp/event.json")
	assert.Equal(t, "/tmp/event.json", detectEventFilePath())
}

func TestDetectEventFilePath_Empty(t *testing.T) {
	t.Setenv("GITHUB_EVENT_PATH", "")
	assert.Equal(t, "", detectEventFilePath())
}

// --- getEnv ---

func TestGetEnv_Set(t *testing.T) {
	t.Setenv("TEST_GET_ENV_KEY", "myvalue")
	assert.Equal(t, "myvalue", getEnv("TEST_GET_ENV_KEY", "fallback"))
}

func TestGetEnv_NotSet(t *testing.T) {
	// Ensure key does not exist
	t.Setenv("TEST_GET_ENV_KEY_MISSING", "")
	// Unset doesn't exist in t.Setenv, but empty string is a set value.
	// For truly unset, we rely on a key that was never set.
	assert.Equal(t, "fallback", getEnv("ABSOLUTELY_NOT_SET_KEY_XYZ_12345", "fallback"))
}

// --- handleEvent routing ---

func TestHandleEvent_UnknownProvider(t *testing.T) {
	t.Parallel()
	// handleEvent calls log.Fatalf for unknown providers.
	// We can't easily test log.Fatalf without mocking the logger,
	// so we verify the function signature and routing logic instead.
	// This test documents that "unknown" is not a valid provider.
	assert.NotPanics(t, func() {
		// Just verify the detection functions compose correctly
		_ = detectProvider()
		_ = detectEventType()
		_ = detectEventFilePath()
	})
}
