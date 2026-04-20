package promotion

import (
	"context"
	"fmt"
	"testing"

	cfg "github.com/schubergphilis/container-platform-telefonistka/internal/pkg/configuration"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildTestPromotion() PromotionInstance {
	return PromotionInstance{
		Metadata: PromotionInstanceMetaData{
			SourcePath:                     "env/dev/",
			TargetPaths:                    []string{"env/staging/"},
			TargetDescription:              "staging",
			ComponentNames:                 []string{"myapp"},
			PerComponentSkippedTargetPaths: map[string][]string{},
		},
		ComputedSyncPaths: map[string]string{
			"env/staging/myapp": "env/dev/myapp",
		},
	}
}

func buildTestConfig() *cfg.Config {
	return &cfg.Config{
		PromotionPRLabels: []string{"promotion"},
	}
}

// sourceTargetProvider creates a MockProvider with source and target directories.
// Source has one file that differs from target.
func sourceTargetProvider() *testutils.MockProvider {
	return &testutils.MockProvider{
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
}

func TestExecutePromotion_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()
	config := buildTestConfig()
	promotion := buildTestPromotion()

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		42, "feature-branch", "alice", "https://gitlab.com/owner/repo",
		config, promotion, nil, "!", testutils.TestLogger())

	require.NoError(t, err)

	// Verify the full flow: GetRef → GetBranch → CreateBranch → CreateCommit → CreatePullRequest
	assert.Equal(t, 1, provider.CallCount("GetRef"))
	assert.Equal(t, 1, provider.CallCount("CreateBranch"))
	assert.Equal(t, 1, provider.CallCount("CreateCommit"))
	assert.Equal(t, 1, provider.CallCount("CreatePullRequest"))

	// Verify PR was created with correct fields
	require.NotNil(t, provider.LastCreatePR)
	assert.Equal(t, "main", provider.LastCreatePR.Base)
	assert.Contains(t, provider.LastCreatePR.Title, "myapp")
	assert.Contains(t, provider.LastCreatePR.Title, "staging")
	assert.Equal(t, []string{"promotion"}, provider.LastCreatePR.Labels)
	assert.Equal(t, []string{"alice"}, provider.LastCreatePR.Assignees)

	// Verify commit had the right actions
	require.NotNil(t, provider.LastCommitOpts)
	assert.Len(t, provider.LastCommitOpts.CommitActions, 1)
	assert.Equal(t, "update", provider.LastCommitOpts.CommitActions[0].Action)
	assert.Equal(t, "env/staging/myapp/values.yaml", provider.LastCommitOpts.CommitActions[0].FilePath)
}

func TestExecutePromotion_NoChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Source and target have same SHAs → no sync actions
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/myapp@main": {
				{Name: "values.yaml", Path: "env/dev/myapp/values.yaml", Type: "file", SHA: "same_sha"},
			},
			"env/staging/myapp@main": {
				{Name: "values.yaml", Path: "env/staging/myapp/values.yaml", Type: "file", SHA: "same_sha"},
			},
		},
		Files: map[string][]byte{},
	}

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		1, "branch", "author", "", buildTestConfig(), buildTestPromotion(), nil, "#", testutils.TestLogger())

	require.NoError(t, err)
	// No branch/commit/PR should be created when there are no changes
	assert.Equal(t, 0, provider.CallCount("CreateBranch"))
	assert.Equal(t, 0, provider.CallCount("CreateCommit"))
	assert.Equal(t, 0, provider.CallCount("CreatePullRequest"))
}

func TestExecutePromotion_CreateCommitError_CleansBranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()
	provider.Errors = map[string]error{
		"CreateCommit": fmt.Errorf("API error: 500"),
	}

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		1, "branch", "author", "", buildTestConfig(), buildTestPromotion(), nil, "#", testutils.TestLogger())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API error: 500")
	// Branch should be cleaned up after commit failure
	assert.Equal(t, 1, provider.CallCount("DeleteBranch"))
	// PR should NOT be created
	assert.Equal(t, 0, provider.CallCount("CreatePullRequest"))
}

func TestExecutePromotion_CreatePRError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()
	provider.Errors = map[string]error{
		"CreatePullRequest": fmt.Errorf("merge request already exists"),
	}

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		1, "branch", "author", "", buildTestConfig(), buildTestPromotion(), nil, "#", testutils.TestLogger())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "merge request already exists")
}

func TestExecutePromotion_AutoApprove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()
	approver := &testutils.MockProvider{}

	config := buildTestConfig()
	config.AutoApprovePromotionPrs = true

	err := ExecutePromotion(ctx, provider, approver, "owner", "repo", "main",
		1, "branch", "author", "", config, buildTestPromotion(), nil, "!", testutils.TestLogger())

	require.NoError(t, err)
	assert.Equal(t, 1, approver.CallCount("ApprovePullRequest"))
}

func TestExecutePromotion_AutoMerge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()

	promotion := buildTestPromotion()
	promotion.Metadata.AutoMerge = true

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		1, "branch", "author", "", buildTestConfig(), promotion, nil, "!", testutils.TestLogger())

	require.NoError(t, err)
	assert.GreaterOrEqual(t, provider.CallCount("MergePullRequest"), 1)
	assert.GreaterOrEqual(t, provider.CallCount("CommentOnPullRequest"), 1)
}

func TestExecutePromotion_ExistingBranchDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()
	// GetBranch succeeds → branch exists → should be deleted before recreating
	provider.BranchResponse = &gitprovider.Reference{Ref: "refs/heads/old", SHA: "oldsha"}

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		1, "branch", "author", "", buildTestConfig(), buildTestPromotion(), nil, "#", testutils.TestLogger())

	require.NoError(t, err)
	assert.Equal(t, 1, provider.CallCount("DeleteBranch"))
	assert.Equal(t, 1, provider.CallCount("CreateBranch"))
}

func TestExecutePromotion_ChainedMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()

	existingMeta := &PrMetadata{
		OriginalPrAuthor: "original-author",
		PreviousPromotionMetadata: map[int]PromotionPathMetadata{
			10: {SourcePath: "env/dev/", TargetPaths: []string{"env/staging/"}},
		},
	}

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		20, "branch", "current-author", "https://example.com/repo",
		buildTestConfig(), buildTestPromotion(), existingMeta, "!", testutils.TestLogger())

	require.NoError(t, err)
	require.NotNil(t, provider.LastCreatePR)
	// PR should be assigned to the original author, not current
	assert.Equal(t, []string{"original-author"}, provider.LastCreatePR.Assignees)
	// Body should contain metadata for chained promotion
	assert.Contains(t, provider.LastCreatePR.Body, "Telefonistka data")
}

func TestExecutePromotion_GetRefError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()
	provider.Errors = map[string]error{
		"GetRef": fmt.Errorf("ref not found"),
	}

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		1, "branch", "author", "", buildTestConfig(), buildTestPromotion(), nil, "#", testutils.TestLogger())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ref not found")
	assert.Equal(t, 0, provider.CallCount("CreateBranch"))
}

// --- Edge-case tests (T3) ---

func TestExecutePromotion_CreateBranchError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()
	provider.Errors = map[string]error{
		"CreateBranch": fmt.Errorf("permission denied"),
	}

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		1, "branch", "author", "", buildTestConfig(), buildTestPromotion(), nil, "#", testutils.TestLogger())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	// No commit or PR should be attempted
	assert.Equal(t, 0, provider.CallCount("CreateCommit"))
	assert.Equal(t, 0, provider.CallCount("CreatePullRequest"))
}

func TestExecutePromotion_BranchCleanupFailureStillReturnsCommitError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()
	provider.Errors = map[string]error{
		"CreateCommit": fmt.Errorf("commit failed"),
		"DeleteBranch": fmt.Errorf("delete failed"),
	}

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		1, "branch", "author", "", buildTestConfig(), buildTestPromotion(), nil, "#", testutils.TestLogger())

	// The commit error should be returned, not the branch cleanup error
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "commit failed")
	// DeleteBranch should still have been attempted (cleanup)
	assert.Equal(t, 1, provider.CallCount("DeleteBranch"))
}

func TestExecutePromotion_AutoMergeFailure_CommentsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()
	provider.Errors = map[string]error{
		"MergePullRequest": fmt.Errorf("permission denied: permanent"),
	}

	promotion := buildTestPromotion()
	promotion.Metadata.AutoMerge = true

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		1, "branch", "author", "", buildTestConfig(), promotion, nil, "!", testutils.TestLogger())

	// Auto-merge failure should NOT fail the whole promotion
	require.NoError(t, err)
	// PR should have been created
	assert.Equal(t, 1, provider.CallCount("CreatePullRequest"))
	// A comment about the failure should have been posted
	assert.GreaterOrEqual(t, provider.CallCount("CommentOnPullRequest"), 2)
	assert.Contains(t, provider.LastComment, "Auto-merge failed")
}

func TestExecutePromotion_AutoApproveFailure_DoesNotBlockPR(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()
	approver := &testutils.MockProvider{
		Errors: map[string]error{
			"ApprovePullRequest": fmt.Errorf("forbidden"),
		},
	}

	config := buildTestConfig()
	config.AutoApprovePromotionPrs = true

	err := ExecutePromotion(ctx, provider, approver, "owner", "repo", "main",
		1, "branch", "author", "", config, buildTestPromotion(), nil, "!", testutils.TestLogger())

	// Approval failure should not block the promotion
	require.NoError(t, err)
	assert.Equal(t, 1, provider.CallCount("CreatePullRequest"))
	assert.Equal(t, 1, approver.CallCount("ApprovePullRequest"))
}

func TestExecutePromotion_NilApproverSkipsApproval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := sourceTargetProvider()

	config := buildTestConfig()
	config.AutoApprovePromotionPrs = true // enabled but no approver provided

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		1, "branch", "author", "", config, buildTestPromotion(), nil, "!", testutils.TestLogger())

	require.NoError(t, err)
	assert.Equal(t, 1, provider.CallCount("CreatePullRequest"))
}

func TestExecutePromotion_SyncActionError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Provider with source dir but GetFileContent will fail
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/myapp@main": {
				{Name: "values.yaml", Path: "env/dev/myapp/values.yaml", Type: "file", SHA: "sha_new"},
			},
			"env/staging/myapp@main": {
				{Name: "values.yaml", Path: "env/staging/myapp/values.yaml", Type: "file", SHA: "sha_old"},
			},
		},
		Files:  map[string][]byte{}, // empty → GetFileContent will fail
		Errors: map[string]error{},
	}

	err := ExecutePromotion(ctx, provider, nil, "owner", "repo", "main",
		1, "branch", "author", "", buildTestConfig(), buildTestPromotion(), nil, "#", testutils.TestLogger())

	assert.Error(t, err)
	// No branch/commit/PR because sync actions failed
	assert.Equal(t, 0, provider.CallCount("CreateBranch"))
}

// --- GeneratePromotionPrBody tests ---

func TestGeneratePromotionPrBody_GitLabLinks(t *testing.T) {
	t.Parallel()
	promotion := buildTestPromotion()

	body := GeneratePromotionPrBody(42, "myapp", promotion, "alice",
		"https://gitlab.com/org/repo", nil, "!")

	assert.Contains(t, body, "/-/merge_requests/42")
	assert.Contains(t, body, "!42")
	assert.Contains(t, body, "Telefonistka data")
}

func TestGeneratePromotionPrBody_GitHubLinks(t *testing.T) {
	t.Parallel()
	promotion := buildTestPromotion()

	body := GeneratePromotionPrBody(42, "myapp", promotion, "alice",
		"https://github.com/org/repo", nil, "#")

	assert.Contains(t, body, "/pull/42")
	assert.Contains(t, body, "#42")
	assert.NotContains(t, body, "merge_requests")
}

func TestGeneratePromotionPrBody_NoRepoURL(t *testing.T) {
	t.Parallel()
	promotion := buildTestPromotion()

	body := GeneratePromotionPrBody(42, "myapp", promotion, "alice", "", nil, "!")

	// Should use plain reference without link
	assert.Contains(t, body, "!42")
	assert.NotContains(t, body, "https://")
}

func TestGeneratePromotionPrBody_PushTriggered(t *testing.T) {
	t.Parallel()
	promotion := buildTestPromotion()

	// PR number 0 indicates push-triggered promotion
	body := GeneratePromotionPrBody(0, "myapp", promotion, "alice",
		"https://gitlab.com/org/repo", nil, "!")

	assert.Contains(t, body, "push")
}

func TestGeneratePromotionPrBody_ChainedMetadata(t *testing.T) {
	t.Parallel()
	promotion := buildTestPromotion()
	existing := &PrMetadata{
		OriginalPrAuthor: "original",
		PreviousPromotionMetadata: map[int]PromotionPathMetadata{
			10: {SourcePath: "env/dev/", TargetPaths: []string{"env/staging/"}},
		},
	}

	body := GeneratePromotionPrBody(20, "myapp", promotion, "original",
		"https://gitlab.com/org/repo", existing, "!")

	// Should contain both the original and new promotion in the chain
	assert.Contains(t, body, "!10")
	assert.Contains(t, body, "!20")
	assert.Contains(t, body, "Telefonistka data")
}

func TestGeneratePromotionPrBody_SkippedTargetPaths(t *testing.T) {
	t.Parallel()
	promotion := buildTestPromotion()
	promotion.Metadata.PerComponentSkippedTargetPaths = map[string][]string{
		"myapp": {"env/prod/"},
	}

	body := GeneratePromotionPrBody(1, "myapp", promotion, "alice", "", nil, "#")

	assert.Contains(t, body, "Skipped target paths")
	assert.Contains(t, body, "env/prod/")
}

// --- PrMetadata serialization round-trip ---

func TestPrMetadata_SerializeDeserialize_RoundTrip(t *testing.T) {
	t.Parallel()
	original := PrMetadata{
		OriginalPrAuthor: "alice",
		OriginalPrNumber: 42,
		PromotedPaths:    []string{"env/staging/myapp"},
		PreviousPromotionMetadata: map[int]PromotionPathMetadata{
			10: {SourcePath: "env/dev/", TargetPaths: []string{"env/staging/"}},
			20: {SourcePath: "env/staging/", TargetPaths: []string{"env/prod/us-east1/", "env/prod/us-west1/"}},
		},
	}

	serialized, err := original.Serialize()
	require.NoError(t, err)
	assert.NotEmpty(t, serialized)

	var deserialized PrMetadata
	err = deserialized.DeSerialize(serialized)
	require.NoError(t, err)

	assert.Equal(t, original.OriginalPrAuthor, deserialized.OriginalPrAuthor)
	assert.Equal(t, original.OriginalPrNumber, deserialized.OriginalPrNumber)
	assert.Equal(t, original.PromotedPaths, deserialized.PromotedPaths)
	assert.Equal(t, len(original.PreviousPromotionMetadata), len(deserialized.PreviousPromotionMetadata))
	for k, v := range original.PreviousPromotionMetadata {
		dv, ok := deserialized.PreviousPromotionMetadata[k]
		require.True(t, ok, "missing key %d", k)
		assert.Equal(t, v.SourcePath, dv.SourcePath)
		assert.Equal(t, v.TargetPaths, dv.TargetPaths)
	}
}

func TestPrMetadata_DeSerialize_InvalidBase64(t *testing.T) {
	t.Parallel()
	var pm PrMetadata
	err := pm.DeSerialize("not-valid-base64!!!")
	assert.Error(t, err)
}

func TestPrMetadata_DeSerialize_InvalidJSON(t *testing.T) {
	t.Parallel()
	var pm PrMetadata
	// Valid base64 but not valid JSON
	err := pm.DeSerialize("bm90LWpzb24=") // "not-json" in base64
	assert.Error(t, err)
}

func TestParsePrMetadata_FromBody(t *testing.T) {
	t.Parallel()
	original := PrMetadata{
		OriginalPrAuthor: "bob",
		PromotedPaths:    []string{"env/staging/app"},
	}
	serialized, err := original.Serialize()
	require.NoError(t, err)

	body := fmt.Sprintf("Some PR description\n\n<!--|Telefonistka data, do not delete|%s|-->", serialized)
	parsed := ParsePrMetadata(body)
	require.NotNil(t, parsed)
	assert.Equal(t, "bob", parsed.OriginalPrAuthor)
}

func TestParsePrMetadata_NoMetadata(t *testing.T) {
	t.Parallel()
	parsed := ParsePrMetadata("Just a regular PR body with no metadata")
	assert.Nil(t, parsed)
}

func TestParsePrMetadata_EmptyBody(t *testing.T) {
	t.Parallel()
	parsed := ParsePrMetadata("")
	assert.Nil(t, parsed)
}

func TestParsePrMetadata_CorruptMetadata(t *testing.T) {
	t.Parallel()
	body := "<!--|Telefonistka data|not-valid-base64!!!|-->"
	parsed := ParsePrMetadata(body)
	assert.Nil(t, parsed)
}

func TestIsMergeErrorRetryable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		msg       string
		retryable bool
	}{
		{"405 Method Not Allowed", true},
		{"try the merge again", true},
		{"merge request is not mergeable", true},
		{"Pipeline still running", true},
		{"permission denied", false},
		{"not found", false},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.retryable, IsMergeErrorRetryable(tt.msg))
		})
	}
}
