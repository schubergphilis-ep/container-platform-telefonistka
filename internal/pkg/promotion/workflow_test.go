package promotion

import (
	"context"
	"encoding/base64"
	"testing"

	cfg "github.com/schubergphilis/container-platform-telefonistka/internal/pkg/configuration"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func devStagingConfig() *cfg.Config {
	return &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath: "env/dev/",
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"env/staging/"}, TargetDescription: "staging"},
				},
			},
		},
		PromotionPRLabels: []string{"promotion"},
	}
}

func devStagingProdConfig() *cfg.Config {
	return &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath: "env/dev/",
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"env/staging/"}, TargetDescription: "staging"},
				},
			},
			{
				SourcePath: "env/staging/",
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"env/prod/"}, TargetDescription: "prod"},
				},
			},
		},
		PromotionPRLabels: []string{"promotion"},
	}
}

func configYAML(c *cfg.Config) []byte {
	// Manually build YAML for the configs used in tests.
	// This keeps the test self-contained without importing yaml marshalling.
	var out string
	out += "promotionPaths:\n"
	for _, pp := range c.PromotionPaths {
		out += "  - sourcePath: \"" + pp.SourcePath + "\"\n"
		if pp.Conditions.PrHasLabels != nil {
			out += "    conditions:\n"
			out += "      prHasLabels:\n"
			for _, l := range pp.Conditions.PrHasLabels {
				out += "        - \"" + l + "\"\n"
			}
		}
		out += "    promotionPrs:\n"
		for _, pr := range pp.PromotionPrs {
			out += "      - targetDescription: \"" + pr.TargetDescription + "\"\n"
			out += "        targetPaths:\n"
			for _, tp := range pr.TargetPaths {
				out += "          - \"" + tp + "\"\n"
			}
			if len(pr.BlockList) > 0 {
				out += "        blockList:\n"
				for _, b := range pr.BlockList {
					out += "          - \"" + b + "\"\n"
				}
			}
		}
	}
	if len(c.PromotionPRLabels) > 0 {
		out += "promotionPRLabels:\n"
		for _, l := range c.PromotionPRLabels {
			out += "  - \"" + l + "\"\n"
		}
	}
	return []byte(out)
}

// ---------------------------------------------------------------------------
// Full E2E workflow tests
// ---------------------------------------------------------------------------

func TestE2E_SingleComponentPromotion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Arrange: one component "myapp" changed in env/dev/
	config := devStagingConfig()
	changedFiles := []string{"env/dev/myapp/values.yaml"}
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/myapp@main": {
				{Name: "values.yaml", Path: "env/dev/myapp/values.yaml", Type: "file", SHA: "sha_new"},
			},
			"env/staging/myapp@main": {
				{Name: "values.yaml", Path: "env/staging/myapp/values.yaml", Type: "file", SHA: "sha_old"},
			},
		},
		Files: map[string][]byte{
			"env/dev/myapp/values.yaml@main": []byte("image: app:v2\n"),
		},
	}

	// Act: generate plan
	plan, err := GeneratePromotionPlan(ctx, provider, "owner", "repo", changedFiles, nil, config, "main", testutils.TestLogger())
	require.NoError(t, err)
	require.Len(t, plan, 1)

	// Verify plan contents
	for _, promo := range plan {
		assert.Equal(t, "env/dev/", promo.Metadata.SourcePath)
		assert.Contains(t, promo.Metadata.ComponentNames, "myapp")
		assert.Equal(t, []string{"env/staging/"}, promo.Metadata.TargetPaths)
		assert.Len(t, promo.ComputedSyncPaths, 1)

		// Act: generate sync actions
		for target, source := range promo.ComputedSyncPaths {
			actions, syncErr := GenerateSyncCommitActions(ctx, provider, "owner", "repo", source, target, "main", nil, testutils.TestLogger())
			require.NoError(t, syncErr)
			require.Len(t, actions, 1)
			assert.Equal(t, "update", actions[0].Action)
			assert.Equal(t, "env/staging/myapp/values.yaml", actions[0].FilePath)
		}

		// Act: execute promotion end-to-end
		execErr := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
			42, "feature-branch", "alice", "https://gitlab.com/owner/repo",
			config, promo, nil, "!", testutils.TestLogger())
		require.NoError(t, execErr)
	}

	// Assert: full chain was executed
	assert.Equal(t, 1, provider.CallCount("CreateBranch"))
	assert.Equal(t, 1, provider.CallCount("CreateCommit"))
	assert.Equal(t, 1, provider.CallCount("CreatePullRequest"))
	require.NotNil(t, provider.LastCreatePR)
	assert.Contains(t, provider.LastCreatePR.Title, "myapp")
	assert.Equal(t, "main", provider.LastCreatePR.Base)
}

func TestE2E_MultiComponentPromotion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	config := devStagingConfig()
	changedFiles := []string{
		"env/dev/app-a/values.yaml",
		"env/dev/app-b/deployment.yaml",
	}
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/app-a@main": {
				{Name: "values.yaml", Path: "env/dev/app-a/values.yaml", Type: "file", SHA: "sha_a_new"},
			},
			"env/dev/app-b@main": {
				{Name: "deployment.yaml", Path: "env/dev/app-b/deployment.yaml", Type: "file", SHA: "sha_b_new"},
			},
			"env/staging/app-a@main": {
				{Name: "values.yaml", Path: "env/staging/app-a/values.yaml", Type: "file", SHA: "sha_a_old"},
			},
			"env/staging/app-b@main": {
				{Name: "deployment.yaml", Path: "env/staging/app-b/deployment.yaml", Type: "file", SHA: "sha_b_old"},
			},
		},
		Files: map[string][]byte{
			"env/dev/app-a/values.yaml@main":     []byte("v2"),
			"env/dev/app-b/deployment.yaml@main": []byte("v2"),
		},
	}

	// Act
	plan, err := GeneratePromotionPlan(ctx, provider, "owner", "repo", changedFiles, nil, config, "main", testutils.TestLogger())
	require.NoError(t, err)
	require.Len(t, plan, 1, "both components should be in one promotion PR")

	for _, promo := range plan {
		assert.Len(t, promo.Metadata.ComponentNames, 2)
		assert.Contains(t, promo.Metadata.ComponentNames, "app-a")
		assert.Contains(t, promo.Metadata.ComponentNames, "app-b")
		assert.Len(t, promo.ComputedSyncPaths, 2)

		// Execute
		execErr := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
			10, "multi-branch", "bob", "", config, promo, nil, "#", testutils.TestLogger())
		require.NoError(t, execErr)
	}

	assert.Equal(t, 1, provider.CallCount("CreatePullRequest"))
	require.NotNil(t, provider.LastCommitOpts)
	assert.Len(t, provider.LastCommitOpts.CommitActions, 2)
}

func TestE2E_ChainedPromotion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	config := devStagingProdConfig()

	// Step 1: dev -> staging promotion
	changedFilesDev := []string{"env/dev/myapp/values.yaml"}
	providerStep1 := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/myapp@main": {
				{Name: "values.yaml", Path: "env/dev/myapp/values.yaml", Type: "file", SHA: "sha_new"},
			},
			"env/staging/myapp@main": {
				{Name: "values.yaml", Path: "env/staging/myapp/values.yaml", Type: "file", SHA: "sha_old"},
			},
		},
		Files: map[string][]byte{
			"env/dev/myapp/values.yaml@main": []byte("image: app:v3\n"),
		},
	}

	plan1, err := GeneratePromotionPlan(ctx, providerStep1, "owner", "repo", changedFilesDev, nil, config, "main", testutils.TestLogger())
	require.NoError(t, err)
	require.Len(t, plan1, 1)

	for _, promo := range plan1 {
		err = ExecutePromotion(ctx, providerStep1, nil, "owner", "repo", "main",
			10, "dev-branch", "alice", "https://example.com/repo",
			config, promo, nil, "!", testutils.TestLogger())
		require.NoError(t, err)
	}

	// Extract metadata from the created PR body
	require.NotNil(t, providerStep1.LastCreatePR)
	metadata1 := ParsePrMetadata(providerStep1.LastCreatePR.Body)
	require.NotNil(t, metadata1)
	assert.Equal(t, "alice", metadata1.OriginalPrAuthor)

	// Step 2: staging -> prod promotion (chained), using metadata from step 1
	changedFilesStaging := []string{"env/staging/myapp/values.yaml"}
	providerStep2 := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/staging/myapp@main": {
				{Name: "values.yaml", Path: "env/staging/myapp/values.yaml", Type: "file", SHA: "sha_staging_new"},
			},
			"env/prod/myapp@main": {
				{Name: "values.yaml", Path: "env/prod/myapp/values.yaml", Type: "file", SHA: "sha_prod_old"},
			},
		},
		Files: map[string][]byte{
			"env/staging/myapp/values.yaml@main": []byte("image: app:v3\n"),
		},
	}

	plan2, err := GeneratePromotionPlan(ctx, providerStep2, "owner", "repo", changedFilesStaging, nil, config, "main", testutils.TestLogger())
	require.NoError(t, err)
	require.Len(t, plan2, 1)

	for _, promo := range plan2 {
		err = ExecutePromotion(ctx, providerStep2, nil, "owner", "repo", "main",
			20, "staging-branch", "bot-user", "https://example.com/repo",
			config, promo, metadata1, "!", testutils.TestLogger())
		require.NoError(t, err)
	}

	// Assert: chained metadata carries forward the original author
	require.NotNil(t, providerStep2.LastCreatePR)
	assert.Equal(t, []string{"alice"}, providerStep2.LastCreatePR.Assignees)
	metadata2 := ParsePrMetadata(providerStep2.LastCreatePR.Body)
	require.NotNil(t, metadata2)
	assert.Equal(t, "alice", metadata2.OriginalPrAuthor)
	// Should have both hops in the metadata
	assert.Contains(t, metadata2.PreviousPromotionMetadata, 10)
	assert.Contains(t, metadata2.PreviousPromotionMetadata, 20)
}

func TestE2E_DriftDetectionThenPromotion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/myapp@main": {
				{Name: "values.yaml", Path: "env/dev/myapp/values.yaml", Type: "file", SHA: "sha_dev"},
			},
			"env/staging/myapp@main": {
				{Name: "values.yaml", Path: "env/staging/myapp/values.yaml", Type: "file", SHA: "sha_staging_drifted"},
			},
		},
		Files: map[string][]byte{
			"env/dev/myapp/values.yaml@main":     []byte("image: app:v2\n"),
			"env/staging/myapp/values.yaml@main": []byte("image: app:v1-hotfix\n"),
		},
	}

	// Act: detect drift
	hasDiff, diffOutput, err := CompareRepoDirectories(ctx, provider, "owner", "repo",
		"env/dev/myapp", "env/staging/myapp", "main", "", nil, testutils.TestLogger())
	require.NoError(t, err)
	assert.True(t, hasDiff, "drift should be detected")
	assert.Contains(t, diffOutput, "app:v2")
	assert.Contains(t, diffOutput, "app:v1-hotfix")

	// Act: sync to correct the drift
	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"env/dev/myapp", "env/staging/myapp", "main", nil, testutils.TestLogger())
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "update", actions[0].Action)
	assert.Equal(t, "env/staging/myapp/values.yaml", actions[0].FilePath)

	// Verify content is base64-encoded source content
	decoded, decErr := base64.StdEncoding.DecodeString(actions[0].Content)
	require.NoError(t, decErr)
	assert.Equal(t, "image: app:v2\n", string(decoded))
}

func TestE2E_NoTelefonistkaYaml(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Files: map[string][]byte{},
	}

	// Act
	config, err := GetRepoConfig(ctx, provider, "owner", "repo", "main", testutils.TestLogger())

	// Assert
	assert.Error(t, err)
	assert.Nil(t, config)
}

func TestE2E_EmptyMR(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	config := devStagingConfig()
	provider := &testutils.MockProvider{}

	// Act: no changed files
	plan, err := GeneratePromotionPlan(ctx, provider, "owner", "repo", []string{}, nil, config, "main", testutils.TestLogger())
	require.NoError(t, err)

	// Assert
	assert.Empty(t, plan, "no promotions should be generated for empty MR")
}

func TestE2E_BlockListSkipsFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/myapp@main": {
				{Name: "values.yaml", Path: "env/dev/myapp/values.yaml", Type: "file", SHA: "sha1"},
				{Name: "secret.yaml", Path: "env/dev/myapp/secret.yaml", Type: "file", SHA: "sha2"},
			},
			"env/staging/myapp@main": {
				{Name: "values.yaml", Path: "env/staging/myapp/values.yaml", Type: "file", SHA: "sha_old"},
				{Name: "secret.yaml", Path: "env/staging/myapp/secret.yaml", Type: "file", SHA: "sha_old2"},
			},
		},
		Files: map[string][]byte{
			"env/dev/myapp/values.yaml@main":     []byte("v2"),
			"env/dev/myapp/secret.yaml@main":     []byte("supersecret"),
			"env/staging/myapp/values.yaml@main": []byte("v1"),
			"env/staging/myapp/secret.yaml@main": []byte("oldsecret"),
		},
	}
	blockList := []string{"secret.yaml"}

	// Act: sync with blockList
	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"env/dev/myapp", "env/staging/myapp", "main", blockList, testutils.TestLogger())
	require.NoError(t, err)

	// Assert: only values.yaml should be synced, secret.yaml is blocked
	require.Len(t, actions, 1)
	assert.Equal(t, "env/staging/myapp/values.yaml", actions[0].FilePath)

	// Act: drift detection with blockList
	hasDiff, diffOutput, err := CompareRepoDirectories(ctx, provider, "owner", "repo",
		"env/dev/myapp", "env/staging/myapp", "main", "", blockList, testutils.TestLogger())
	require.NoError(t, err)
	assert.True(t, hasDiff)
	// The diff output should not mention secret.yaml
	assert.NotContains(t, diffOutput, "secret.yaml")
}

func TestE2E_DeletionPromotion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Source directory does not exist (deleted), target still has files
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			// no "env/dev/myapp@main" entry → GetDirectoryContent will error
			"env/staging/myapp@main": {
				{Name: "values.yaml", Path: "env/staging/myapp/values.yaml", Type: "file", SHA: "sha1"},
				{Name: "config.yaml", Path: "env/staging/myapp/config.yaml", Type: "file", SHA: "sha2"},
			},
		},
		Files: map[string][]byte{},
	}

	// Act
	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"env/dev/myapp", "env/staging/myapp", "main", nil, testutils.TestLogger())
	require.NoError(t, err)

	// Assert: all target files should be deleted
	require.Len(t, actions, 2)
	for _, a := range actions {
		assert.Equal(t, "delete", a.Action)
	}
	paths := []string{actions[0].FilePath, actions[1].FilePath}
	assert.Contains(t, paths, "env/staging/myapp/values.yaml")
	assert.Contains(t, paths, "env/staging/myapp/config.yaml")
}

// ---------------------------------------------------------------------------
// GenerateSyncCommitActions edge cases
// ---------------------------------------------------------------------------

func TestSync_NewTargetDirectory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Target directory does not exist yet (first-time promotion)
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/myapp@main": {
				{Name: "values.yaml", Path: "env/dev/myapp/values.yaml", Type: "file", SHA: "sha1"},
				{Name: "chart.yaml", Path: "env/dev/myapp/chart.yaml", Type: "file", SHA: "sha2"},
			},
			// no "env/staging/myapp@main" → target not found
		},
		Files: map[string][]byte{
			"env/dev/myapp/values.yaml@main": []byte("v1"),
			"env/dev/myapp/chart.yaml@main":  []byte("chart"),
		},
	}

	// Act
	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"env/dev/myapp", "env/staging/myapp", "main", nil, testutils.TestLogger())
	require.NoError(t, err)

	// Assert: all files should be "create"
	require.Len(t, actions, 2)
	for _, a := range actions {
		assert.Equal(t, "create", a.Action)
	}
}

func TestSync_FileDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Source has file A, target has file A and B → B should be deleted
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/myapp@main": {
				{Name: "values.yaml", Path: "env/dev/myapp/values.yaml", Type: "file", SHA: "same_sha"},
			},
			"env/staging/myapp@main": {
				{Name: "values.yaml", Path: "env/staging/myapp/values.yaml", Type: "file", SHA: "same_sha"},
				{Name: "obsolete.yaml", Path: "env/staging/myapp/obsolete.yaml", Type: "file", SHA: "sha_obs"},
			},
		},
		Files: map[string][]byte{},
	}

	// Act
	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"env/dev/myapp", "env/staging/myapp", "main", nil, testutils.TestLogger())
	require.NoError(t, err)

	// Assert: one delete action for the obsolete file
	require.Len(t, actions, 1)
	assert.Equal(t, "delete", actions[0].Action)
	assert.Equal(t, "env/staging/myapp/obsolete.yaml", actions[0].FilePath)
}

func TestSync_IdenticalFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/myapp@main": {
				{Name: "values.yaml", Path: "env/dev/myapp/values.yaml", Type: "file", SHA: "identical_sha"},
			},
			"env/staging/myapp@main": {
				{Name: "values.yaml", Path: "env/staging/myapp/values.yaml", Type: "file", SHA: "identical_sha"},
			},
		},
		Files: map[string][]byte{},
	}

	// Act
	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"env/dev/myapp", "env/staging/myapp", "main", nil, testutils.TestLogger())
	require.NoError(t, err)

	// Assert: no actions
	assert.Empty(t, actions)
}

func TestSync_MixedCreateUpdateDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/myapp@main": {
				{Name: "existing.yaml", Path: "env/dev/myapp/existing.yaml", Type: "file", SHA: "sha_updated"},
				{Name: "new-file.yaml", Path: "env/dev/myapp/new-file.yaml", Type: "file", SHA: "sha_new"},
				{Name: "unchanged.yaml", Path: "env/dev/myapp/unchanged.yaml", Type: "file", SHA: "sha_same"},
			},
			"env/staging/myapp@main": {
				{Name: "existing.yaml", Path: "env/staging/myapp/existing.yaml", Type: "file", SHA: "sha_old"},
				{Name: "removed.yaml", Path: "env/staging/myapp/removed.yaml", Type: "file", SHA: "sha_removed"},
				{Name: "unchanged.yaml", Path: "env/staging/myapp/unchanged.yaml", Type: "file", SHA: "sha_same"},
			},
		},
		Files: map[string][]byte{
			"env/dev/myapp/existing.yaml@main": []byte("updated"),
			"env/dev/myapp/new-file.yaml@main": []byte("brand new"),
		},
	}

	// Act
	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"env/dev/myapp", "env/staging/myapp", "main", nil, testutils.TestLogger())
	require.NoError(t, err)

	// Assert: 1 update + 1 create + 1 delete = 3 actions
	require.Len(t, actions, 3)

	actionMap := make(map[string]string) // filePath -> action
	for _, a := range actions {
		actionMap[a.FilePath] = a.Action
	}

	assert.Equal(t, "update", actionMap["env/staging/myapp/existing.yaml"])
	assert.Equal(t, "create", actionMap["env/staging/myapp/new-file.yaml"])
	assert.Equal(t, "delete", actionMap["env/staging/myapp/removed.yaml"])
}

// ---------------------------------------------------------------------------
// GeneratePromotionPlan edge cases
// ---------------------------------------------------------------------------

func TestPlan_ConditionLabelMatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	config := &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath: "env/dev/",
				Conditions: cfg.Condition{
					PrHasLabels: []string{"approved-for-staging"},
				},
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"env/staging/"}, TargetDescription: "staging"},
				},
			},
		},
	}
	changedFiles := []string{"env/dev/myapp/values.yaml"}
	provider := &testutils.MockProvider{}

	// Act: labels do NOT match
	plan, err := GeneratePromotionPlan(ctx, provider, "owner", "repo", changedFiles,
		[]string{"unrelated-label"}, config, "main", testutils.TestLogger())
	require.NoError(t, err)

	// Assert: no promotion generated
	assert.Empty(t, plan)
}

func TestPlan_ConditionLabelMatch_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	config := &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath: "env/dev/",
				Conditions: cfg.Condition{
					PrHasLabels: []string{"approved-for-staging"},
				},
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"env/staging/"}, TargetDescription: "staging"},
				},
			},
		},
	}
	changedFiles := []string{"env/dev/myapp/values.yaml"}
	provider := &testutils.MockProvider{}

	// Act: labels DO match
	plan, err := GeneratePromotionPlan(ctx, provider, "owner", "repo", changedFiles,
		[]string{"approved-for-staging"}, config, "main", testutils.TestLogger())
	require.NoError(t, err)

	// Assert: promotion generated
	require.Len(t, plan, 1)
	for _, promo := range plan {
		assert.Contains(t, promo.Metadata.ComponentNames, "myapp")
	}
}

func TestPlan_NoMatchingFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	config := devStagingConfig()
	changedFiles := []string{"unrelated/path/file.yaml", "another/dir/data.json"}
	provider := &testutils.MockProvider{}

	// Act
	plan, err := GeneratePromotionPlan(ctx, provider, "owner", "repo", changedFiles, nil, config, "main", testutils.TestLogger())
	require.NoError(t, err)

	// Assert
	assert.Empty(t, plan)
}

func TestPlan_MultipleTargets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	config := &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath: "env/dev/",
				PromotionPrs: []cfg.PromotionPr{
					{
						TargetPaths:       []string{"env/staging-eu/", "env/staging-us/"},
						TargetDescription: "all staging",
					},
				},
			},
		},
	}
	changedFiles := []string{"env/dev/myapp/values.yaml"}
	provider := &testutils.MockProvider{}

	// Act
	plan, err := GeneratePromotionPlan(ctx, provider, "owner", "repo", changedFiles, nil, config, "main", testutils.TestLogger())
	require.NoError(t, err)
	require.Len(t, plan, 1)

	// Assert: both target paths should be in ComputedSyncPaths
	for _, promo := range plan {
		assert.Len(t, promo.ComputedSyncPaths, 2)
		// Keys are targetPath+componentName
		_, hasEU := promo.ComputedSyncPaths["env/staging-eu/myapp"]
		_, hasUS := promo.ComputedSyncPaths["env/staging-us/myapp"]
		assert.True(t, hasEU, "should have EU target")
		assert.True(t, hasUS, "should have US target")
		assert.Equal(t, "all staging", promo.Metadata.TargetDescription)
	}
}

// ---------------------------------------------------------------------------
// GetRepoConfig tests
// ---------------------------------------------------------------------------

func TestGetRepoConfig_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cfgObj := devStagingConfig()
	provider := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": configYAML(cfgObj),
		},
	}

	// Act
	config, err := GetRepoConfig(ctx, provider, "owner", "repo", "main", testutils.TestLogger())

	// Assert
	require.NoError(t, err)
	require.NotNil(t, config)
	require.Len(t, config.PromotionPaths, 1)
	assert.Equal(t, "env/dev/", config.PromotionPaths[0].SourcePath)
	assert.Equal(t, []string{"env/staging/"}, config.PromotionPaths[0].PromotionPrs[0].TargetPaths)
}

func TestGetRepoConfig_FileNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Files: map[string][]byte{},
	}

	// Act
	config, err := GetRepoConfig(ctx, provider, "owner", "repo", "main", testutils.TestLogger())

	// Assert
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "file not found")
}

func TestGetRepoConfig_InvalidYAML(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": []byte("{{{{ not valid yaml ::::"),
		},
	}

	// Act
	config, err := GetRepoConfig(ctx, provider, "owner", "repo", "main", testutils.TestLogger())

	// Assert: yaml parse error
	assert.Error(t, err)
	assert.Nil(t, config)
}

func TestGetRepoConfig_WithBlockList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cfgObj := &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath: "env/dev/",
				PromotionPrs: []cfg.PromotionPr{
					{
						TargetPaths:       []string{"env/staging/"},
						TargetDescription: "staging",
						BlockList:         []string{"**/secret.yaml", "kustomization.yaml"},
					},
				},
			},
		},
	}
	provider := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": configYAML(cfgObj),
		},
	}

	// Act
	config, err := GetRepoConfig(ctx, provider, "owner", "repo", "main", testutils.TestLogger())

	// Assert
	require.NoError(t, err)
	require.NotNil(t, config)
	require.Len(t, config.PromotionPaths[0].PromotionPrs[0].BlockList, 2)
	assert.Equal(t, "**/secret.yaml", config.PromotionPaths[0].PromotionPrs[0].BlockList[0])
	assert.Equal(t, "kustomization.yaml", config.PromotionPaths[0].PromotionPrs[0].BlockList[1])
}

// --- GetComponentConfig tests ---

func TestGetComponentConfig_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Files: map[string][]byte{
			"env/dev/myapp/telefonistka.yaml@main": []byte(`
promotionTargetBlockList:
  - env/staging/europe-west4/.*
promotionTargetAllowList:
  - env/prod/.*
disableArgoCDDiff: true
`),
		},
	}

	config, err := GetComponentConfig(ctx, provider, "owner", "repo", "env/dev/myapp", "main", testutils.TestLogger())
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, []string{"env/staging/europe-west4/.*"}, config.PromotionTargetBlockList)
	assert.Equal(t, []string{"env/prod/.*"}, config.PromotionTargetAllowList)
	assert.True(t, config.DisableArgoCDDiff)
}

func TestGetComponentConfig_FileNotFound_ReturnsEmptyConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Files: map[string][]byte{}, // no component config
	}

	config, err := GetComponentConfig(ctx, provider, "owner", "repo", "env/dev/myapp", "main", testutils.TestLogger())
	require.NoError(t, err) // not finding is NOT an error
	require.NotNil(t, config)
	assert.Nil(t, config.PromotionTargetBlockList)
	assert.Nil(t, config.PromotionTargetAllowList)
	assert.False(t, config.DisableArgoCDDiff)
}

func TestGetComponentConfig_InvalidYAML(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Files: map[string][]byte{
			"env/dev/myapp/telefonistka.yaml@main": []byte("{{{{not yaml"),
		},
	}

	config, err := GetComponentConfig(ctx, provider, "owner", "repo", "env/dev/myapp", "main", testutils.TestLogger())
	assert.Error(t, err)
	assert.Nil(t, config)
}

func TestGetComponentConfig_OnlyBlockList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Files: map[string][]byte{
			"comp/telefonistka.yaml@main": []byte("promotionTargetBlockList:\n  - env/prod/.*\n"),
		},
	}

	config, err := GetComponentConfig(ctx, provider, "owner", "repo", "comp", "main", testutils.TestLogger())
	require.NoError(t, err)
	assert.Equal(t, []string{"env/prod/.*"}, config.PromotionTargetBlockList)
	assert.Nil(t, config.PromotionTargetAllowList)
}

func TestGetComponentConfig_EmptyFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Files: map[string][]byte{
			"comp/telefonistka.yaml@main": []byte(""),
		},
	}

	config, err := GetComponentConfig(ctx, provider, "owner", "repo", "comp", "main", testutils.TestLogger())
	require.NoError(t, err)
	require.NotNil(t, config)
}
