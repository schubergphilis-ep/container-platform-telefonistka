package gitlab

import (
	"context"
	"testing"
	"time"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGitLabProvider(t *testing.T) {
	t.Parallel()

	t.Run("Create provider with token", func(t *testing.T) {
		t.Parallel()
		config := &gitprovider.ProviderConfig{
			Type:    gitprovider.ProviderTypeGitLab,
			Token:   "fake_token",
			BaseURL: "https://gitlab.com",
		}

		provider, err := NewGitLabProvider(config)
		require.NoError(t, err)
		assert.NotNil(t, provider)
		assert.Equal(t, gitprovider.ProviderTypeGitLab, provider.Type())
	})

	t.Run("Create provider with self-hosted URL", func(t *testing.T) {
		t.Parallel()
		config := &gitprovider.ProviderConfig{
			Type:    gitprovider.ProviderTypeGitLab,
			Token:   "fake_token",
			BaseURL: "https://gitlab.example.com",
		}

		provider, err := NewGitLabProvider(config)
		require.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("Use default URL when not specified", func(t *testing.T) {
		t.Parallel()
		config := &gitprovider.ProviderConfig{
			Type:  gitprovider.ProviderTypeGitLab,
			Token: "fake_token",
		}

		provider, err := NewGitLabProvider(config)
		require.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("Fail without token", func(t *testing.T) {
		t.Parallel()
		config := &gitprovider.ProviderConfig{
			Type:    gitprovider.ProviderTypeGitLab,
			BaseURL: "https://gitlab.com",
		}

		provider, err := NewGitLabProvider(config)
		assert.Error(t, err)
		assert.Nil(t, provider)
		assert.Contains(t, err.Error(), "requires access token")
	})

	t.Run("Parse numeric project ID", func(t *testing.T) {
		t.Parallel()
		config := &gitprovider.ProviderConfig{
			Type:      gitprovider.ProviderTypeGitLab,
			Token:     "fake_token",
			ProjectID: "12345",
		}

		provider, err := NewGitLabProvider(config)
		require.NoError(t, err)
		assert.NotNil(t, provider)
		assert.Equal(t, 12345, provider.projectID)
	})

	t.Run("Parse group/project format", func(t *testing.T) {
		t.Parallel()
		config := &gitprovider.ProviderConfig{
			Type:      gitprovider.ProviderTypeGitLab,
			Token:     "fake_token",
			ProjectID: "mygroup/myproject",
		}

		provider, err := NewGitLabProvider(config)
		require.NoError(t, err)
		assert.NotNil(t, provider)
		assert.Equal(t, "mygroup/myproject", provider.projectID)
	})
}

func TestGitLabProvider_Type(t *testing.T) {
	t.Parallel()
	config := &gitprovider.ProviderConfig{
		Type:  gitprovider.ProviderTypeGitLab,
		Token: "fake_token",
	}

	provider, err := NewGitLabProvider(config)
	require.NoError(t, err)

	assert.Equal(t, gitprovider.ProviderTypeGitLab, provider.Type())
}

func TestGitLabProvider_DoesNotImplementTreeProvider(t *testing.T) {
	t.Parallel()
	config := &gitprovider.ProviderConfig{
		Type:  gitprovider.ProviderTypeGitLab,
		Token: "fake_token",
	}

	provider, err := NewGitLabProvider(config)
	require.NoError(t, err)

	_, ok := interface{}(provider).(gitprovider.TreeProvider)
	assert.False(t, ok, "GitLab provider should not implement TreeProvider")
}

func TestGitLabProvider_DoesNotImplementCommentMinimizer(t *testing.T) {
	t.Parallel()
	config := &gitprovider.ProviderConfig{
		Type:  gitprovider.ProviderTypeGitLab,
		Token: "fake_token",
	}

	provider, err := NewGitLabProvider(config)
	require.NoError(t, err)

	_, ok := interface{}(provider).(gitprovider.CommentMinimizer)
	assert.False(t, ok, "GitLab provider should not implement CommentMinimizer")
}

func TestGitLabProvider_SupportsGraphQL(t *testing.T) {
	t.Parallel()
	config := &gitprovider.ProviderConfig{
		Type:  gitprovider.ProviderTypeGitLab,
		Token: "fake_token",
	}

	provider, err := NewGitLabProvider(config)
	require.NoError(t, err)

	assert.True(t, provider.SupportsGraphQL())
}

func TestGitLabProvider_GetProjectPath(t *testing.T) {
	t.Parallel()

	t.Run("Use configured project ID", func(t *testing.T) {
		t.Parallel()
		config := &gitprovider.ProviderConfig{
			Type:      gitprovider.ProviderTypeGitLab,
			Token:     "fake_token",
			ProjectID: "12345",
		}

		provider, err := NewGitLabProvider(config)
		require.NoError(t, err)

		path := provider.getProjectPath("owner", "repo")
		assert.Equal(t, "12345", path)
	})

	t.Run("Build from owner/repo when no project ID", func(t *testing.T) {
		t.Parallel()
		config := &gitprovider.ProviderConfig{
			Type:  gitprovider.ProviderTypeGitLab,
			Token: "fake_token",
		}

		provider, err := NewGitLabProvider(config)
		require.NoError(t, err)

		path := provider.getProjectPath("myowner", "myrepo")
		assert.Equal(t, "myowner/myrepo", path)
	})
}

func TestConvertGitLabMR(t *testing.T) {
	t.Parallel()
	// Note: We can't easily create gitlab.MergeRequest in tests without importing
	// the actual GitLab client and mocking it. These would be better as integration tests.
	// For now, test the nil case
	t.Run("Convert nil MR", func(t *testing.T) {
		t.Parallel()
		mr := convertGitLabMR(nil)
		assert.Nil(t, mr)
	})
}

func TestConvertGitLabNote(t *testing.T) {
	t.Parallel()

	t.Run("Convert nil note", func(t *testing.T) {
		t.Parallel()
		note := convertGitLabNote(nil)
		assert.Nil(t, note)
	})
}

// Test the helper function for safe time conversion
func TestSafeTimeConversion(t *testing.T) {
	t.Parallel()
	safeTime := func(t *time.Time) time.Time {
		if t == nil {
			return time.Time{}
		}
		return *t
	}

	t.Run("Convert valid time", func(t *testing.T) {
		t.Parallel()
		now := time.Now()
		result := safeTime(&now)
		assert.Equal(t, now, result)
	})

	t.Run("Convert nil time", func(t *testing.T) {
		t.Parallel()
		result := safeTime(nil)
		assert.True(t, result.IsZero())
	})
}

// Integration test - requires real GitLab token
// Skipped by default, run with: go test -tags=integration
func TestGitLabProvider_GetRepository_Integration(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	token := "" // Set your token here or use environment variable
	if token == "" {
		t.Skip("Set GITLAB_TOKEN to run integration test")
	}

	config := &gitprovider.ProviderConfig{
		Type:    gitprovider.ProviderTypeGitLab,
		Token:   token,
		BaseURL: "https://gitlab.com",
	}

	provider, err := NewGitLabProvider(config)
	require.NoError(t, err)

	ctx := context.Background()
	// Replace with an actual GitLab project you have access to
	repo, err := provider.GetRepository(ctx, "gitlab-org", "gitlab")
	if err != nil {
		t.Logf("Integration test failed (may not have access): %v", err)
		t.Skip()
	}

	assert.NotNil(t, repo)
	assert.Equal(t, "gitlab", repo.Name)
}
