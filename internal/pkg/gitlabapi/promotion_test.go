package gitlabapi

import (
	"context"
	"strings"
	"testing"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	promlib "github.com/schubergphilis/container-platform-telefonistka/internal/pkg/promotion"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------
// Existing tests
// -----------------------------------------------------------------------

func TestIsFileBlocked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		relativePath string
		blockList    []string
		expected     bool
	}{
		{
			name:         "matches doublestar pattern for application.yaml",
			relativePath: "ingress/application.yaml",
			blockList:    []string{"**/application.yaml"},
			expected:     true,
		},
		{
			name:         "matches doublestar pattern for nested path",
			relativePath: "deep/nested/dir/application.yaml",
			blockList:    []string{"**/application.yaml"},
			expected:     true,
		},
		{
			name:         "matches top-level file",
			relativePath: "application.yaml",
			blockList:    []string{"**/application.yaml"},
			expected:     true,
		},
		{
			name:         "matches values-env.yaml pattern",
			relativePath: "manifests/values-env.yaml",
			blockList:    []string{"**/values-env.yaml"},
			expected:     true,
		},
		{
			name:         "does not match unrelated file",
			relativePath: "manifests/values.yaml",
			blockList:    []string{"**/application.yaml", "**/values-env.yaml"},
			expected:     false,
		},
		{
			name:         "empty blockList matches nothing",
			relativePath: "application.yaml",
			blockList:    []string{},
			expected:     false,
		},
		{
			name:         "nil blockList matches nothing",
			relativePath: "application.yaml",
			blockList:    nil,
			expected:     false,
		},
		{
			name:         "matches simple glob pattern",
			relativePath: "manifests/secret.yaml",
			blockList:    []string{"manifests/secret.yaml"},
			expected:     true,
		},
		{
			name:         "matches wildcard in directory",
			relativePath: "manifests/values-env.yaml",
			blockList:    []string{"manifests/*-env.yaml"},
			expected:     true,
		},
		{
			name:         "multiple patterns - second matches",
			relativePath: "manifests/values-env.yaml",
			blockList:    []string{"**/application.yaml", "**/values-env.yaml"},
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := promlib.IsFileBlocked(tt.relativePath, tt.blockList)
			if result != tt.expected {
				t.Errorf("promlib.IsFileBlocked(%q, %v) = %v, want %v", tt.relativePath, tt.blockList, result, tt.expected)
			}
		})
	}
}

// -----------------------------------------------------------------------
// Helpers and constants for promotion/drift tests
// -----------------------------------------------------------------------

// basicConfig returns a minimal telefonistka.yaml that promotes env/dev/ -> env/staging/.
const basicConfig = `
promotionPaths:
  - sourcePath: "env/dev/"
    promotionPrs:
      - targetPaths: ["env/staging/"]
        targetDescription: "staging"
`

// basicConfigWithBlockList is like basicConfig but adds a blockList entry.
const basicConfigWithBlockList = `
promotionPaths:
  - sourcePath: "env/dev/"
    promotionPrs:
      - targetPaths: ["env/staging/"]
        targetDescription: "staging"
        blockList: ["secret.yaml"]
`

const dryRunConfig = `
dryRunMode: true
promotionPaths:
  - sourcePath: "env/dev/"
    promotionPrs:
      - targetPaths: ["env/staging/"]
        targetDescription: "staging"
`

// newDetails builds a ProviderClientDetails pointing at the given mock.
func newDetails(provider gitprovider.GitProvider, prNumber int) ProviderClientDetails {
	return ProviderClientDetails{
		Provider: provider,
		Owner:    "myorg",
		Repo:     "myrepo",
		PrNumber: prNumber,
		PrSHA:    "abc123",
		Ref:      "feature-branch",
		RepoURL:  "https://gitlab.example.com/myorg/myrepo",
		PrAuthor: "alice",
		PrLogger: testutils.TestLogger(),
		Labels:   nil,
	}
}

// identicalDirEntries returns directory and file entries where source and target
// have the same SHA, so drift detection reports no diff.
func identicalDirEntries() (map[string][]*gitprovider.FileNode, map[string][]byte) {
	dirs := map[string][]*gitprovider.FileNode{
		"env/dev/app@main": {
			{Name: "values.yaml", Path: "env/dev/app/values.yaml", Type: "file", SHA: "sha1"},
		},
		"env/staging/app@main": {
			{Name: "values.yaml", Path: "env/staging/app/values.yaml", Type: "file", SHA: "sha1"},
		},
	}
	files := map[string][]byte{
		"telefonistka.yaml@main": []byte(basicConfig),
	}
	return dirs, files
}

// driftedDirEntries returns directory entries where source and target have different SHAs.
func driftedDirEntries() (map[string][]*gitprovider.FileNode, map[string][]byte) {
	dirs := map[string][]*gitprovider.FileNode{
		"env/dev/app@main": {
			{Name: "values.yaml", Path: "env/dev/app/values.yaml", Type: "file", SHA: "sha-source"},
		},
		"env/staging/app@main": {
			{Name: "values.yaml", Path: "env/staging/app/values.yaml", Type: "file", SHA: "sha-target"},
		},
	}
	files := map[string][]byte{
		"telefonistka.yaml@main":           []byte(basicConfig),
		"env/dev/app/values.yaml@main":     []byte("image: v2\n"),
		"env/staging/app/values.yaml@main": []byte("image: v1\n"),
	}
	return dirs, files
}

// promotionMockForMergedMR builds a MockProvider that has enough data for
// ExecutePromotion to succeed (directories for sync actions, file content, etc.).
func promotionMockForMergedMR(config string) *testutils.MockProvider {
	return &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": []byte(config),
			// Source file content for sync action generation.
			"env/dev/app/values.yaml@main": []byte("image: v2\n"),
		},
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/app@main": {
				{Name: "values.yaml", Path: "env/dev/app/values.yaml", Type: "file", SHA: "sha-new"},
			},
			// Target dir does not exist yet (first-time promotion) -- GetDirectoryContent returns error by default.
		},
		PullRequestFiles: []*gitprovider.CommitFile{
			{Filename: "env/dev/app/values.yaml"},
		},
		PullRequestResponse: &gitprovider.PullRequest{
			Number: 42,
			State:  "merged",
			Body:   "my MR body",
			Author: "alice",
		},
	}
}

// -----------------------------------------------------------------------
// DetectDrift tests
// -----------------------------------------------------------------------

func TestDetectDrift_NoDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dirs, files := identicalDirEntries()
	mock := &testutils.MockProvider{
		Directories: dirs,
		Files:       files,
		PullRequestFiles: []*gitprovider.CommitFile{
			{Filename: "env/dev/app/values.yaml"},
		},
	}

	details := newDetails(mock, 1)
	err := DetectDrift(ctx, details)

	require.NoError(t, err)
	// No drift found means no comment should be posted.
	assert.Equal(t, 0, mock.CallCount("CommentOnPullRequest"))
}

func TestDetectDrift_DriftFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dirs, files := driftedDirEntries()
	mock := &testutils.MockProvider{
		Directories: dirs,
		Files:       files,
		PullRequestFiles: []*gitprovider.CommitFile{
			{Filename: "env/dev/app/values.yaml"},
		},
	}

	details := newDetails(mock, 1)
	err := DetectDrift(ctx, details)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, mock.CallCount("CommentOnPullRequest"), 1, "should post a drift comment")
	assert.Contains(t, mock.LastComment, "drift", "comment body should mention drift")
}

func TestDetectDrift_NoConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &testutils.MockProvider{
		Files: map[string][]byte{}, // no telefonistka.yaml
	}

	details := newDetails(mock, 1)
	err := DetectDrift(ctx, details)

	require.Error(t, err, "should fail when config is missing")
	// Should still have commented the error on the MR.
	assert.GreaterOrEqual(t, mock.CallCount("CommentOnPullRequest"), 1)
	assert.Contains(t, mock.LastComment, "Failed to get configuration")
}

func TestDetectDrift_NoDiffRefs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": []byte(basicConfig),
		},
		Errors: map[string]error{
			"ListPullRequestFiles": gitprovider.ErrNoDiffRefs,
		},
	}

	details := newDetails(mock, 1)
	err := DetectDrift(ctx, details)

	require.NoError(t, err, "ErrNoDiffRefs should be treated as skip, not error")
	assert.Equal(t, 0, mock.CallCount("CommentOnPullRequest"))
}

func TestDetectDrift_NoChangedFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": []byte(basicConfig),
		},
		PullRequestFiles: []*gitprovider.CommitFile{
			// File outside any promotion path
			{Filename: "README.md"},
		},
	}

	details := newDetails(mock, 1)
	err := DetectDrift(ctx, details)

	require.NoError(t, err)
	// No promotions matched, so no drift check, no comment.
	assert.Equal(t, 0, mock.CallCount("CommentOnPullRequest"))
}

func TestDetectDrift_BlockListExcludes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Source and target differ only in secret.yaml which is on the blockList.
	dirs := map[string][]*gitprovider.FileNode{
		"env/dev/app@main": {
			{Name: "values.yaml", Path: "env/dev/app/values.yaml", Type: "file", SHA: "sha1"},
			{Name: "secret.yaml", Path: "env/dev/app/secret.yaml", Type: "file", SHA: "sha-secret-src"},
		},
		"env/staging/app@main": {
			{Name: "values.yaml", Path: "env/staging/app/values.yaml", Type: "file", SHA: "sha1"},
			{Name: "secret.yaml", Path: "env/staging/app/secret.yaml", Type: "file", SHA: "sha-secret-tgt"},
		},
	}
	files := map[string][]byte{
		"telefonistka.yaml@main": []byte(basicConfigWithBlockList),
	}

	mock := &testutils.MockProvider{
		Directories: dirs,
		Files:       files,
		PullRequestFiles: []*gitprovider.CommitFile{
			{Filename: "env/dev/app/secret.yaml"},
		},
	}

	details := newDetails(mock, 1)
	err := DetectDrift(ctx, details)

	require.NoError(t, err)
	// The only drifted file is blocked, so no drift comment.
	assert.Equal(t, 0, mock.CallCount("CommentOnPullRequest"))
}

// -----------------------------------------------------------------------
// HandleMergedMR tests
// -----------------------------------------------------------------------

func TestHandleMergedMR_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := promotionMockForMergedMR(basicConfig)
	details := newDetails(mock, 42)

	err := HandleMergedMR(ctx, details, nil, "main")

	require.NoError(t, err)
	assert.GreaterOrEqual(t, mock.CallCount("CreatePullRequest"), 1, "promotion PR should be created")
	require.NotNil(t, mock.LastCreatePR)
	assert.Contains(t, mock.LastCreatePR.Title, "Promotion")
	assert.Equal(t, "main", mock.LastCreatePR.Base)
}

func TestHandleMergedMR_NoConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &testutils.MockProvider{
		Files: map[string][]byte{}, // no config
	}
	details := newDetails(mock, 42)

	err := HandleMergedMR(ctx, details, nil, "main")

	require.Error(t, err)
	assert.GreaterOrEqual(t, mock.CallCount("CommentOnPullRequest"), 1)
	assert.Contains(t, mock.LastComment, "Failed to get configuration")
}

func TestHandleMergedMR_NoDiffRefs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": []byte(basicConfig),
		},
		PullRequestResponse: &gitprovider.PullRequest{
			Number: 42,
			State:  "merged",
			Body:   "",
		},
		Errors: map[string]error{
			"ListPullRequestFiles": gitprovider.ErrNoDiffRefs,
		},
	}
	details := newDetails(mock, 42)

	err := HandleMergedMR(ctx, details, nil, "main")

	require.NoError(t, err, "ErrNoDiffRefs should be treated as skip")
	assert.Equal(t, 0, mock.CallCount("CreatePullRequest"))
}

func TestHandleMergedMR_NoPromotionsNeeded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": []byte(basicConfig),
		},
		PullRequestFiles: []*gitprovider.CommitFile{
			{Filename: "docs/README.md"}, // outside any promotion path
		},
		PullRequestResponse: &gitprovider.PullRequest{
			Number: 42,
			State:  "merged",
			Body:   "",
		},
	}
	details := newDetails(mock, 42)

	err := HandleMergedMR(ctx, details, nil, "main")

	require.NoError(t, err)
	assert.Equal(t, 0, mock.CallCount("CreatePullRequest"), "no promotion PR should be created")
}

func TestHandleMergedMR_DryRunMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": []byte(dryRunConfig),
		},
		PullRequestFiles: []*gitprovider.CommitFile{
			{Filename: "env/dev/app/values.yaml"},
		},
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/app@main": {
				{Name: "values.yaml", Path: "env/dev/app/values.yaml", Type: "file", SHA: "sha1"},
			},
		},
		PullRequestResponse: &gitprovider.PullRequest{
			Number: 42,
			State:  "merged",
			Body:   "",
		},
	}
	details := newDetails(mock, 42)

	err := HandleMergedMR(ctx, details, nil, "main")

	require.NoError(t, err)
	// Dry-run should comment the plan, not create a PR.
	assert.Equal(t, 0, mock.CallCount("CreatePullRequest"), "dry-run should not create PR")
	assert.GreaterOrEqual(t, mock.CallCount("CommentOnPullRequest"), 1, "dry-run should comment the plan")
	assert.Contains(t, mock.LastComment, "Dry Run")
}

func TestHandleMergedMR_ChainedMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Simulate an MR body that contains serialized promotion metadata from a prior hop.
	// The metadata embeds the original author "bob" from the first promotion in the chain.
	// Format: <!--| label |base64json|-->
	metadataBody := `Promotion from dev to staging

<!--| Telefonistka data, do not delete |eyJvcmlnaW5hbFByQXV0aG9yIjoiYm9iIiwib3JpZ2luYWxQck51bWJlciI6MCwicHJvbW90ZWRQYXRocyI6WyJlbnYvc3RhZ2luZy9hcHAiXSwicHJldmlvdXNQcm9tb3Rpb25QYXRocyI6eyIxMCI6eyJzb3VyY2VQYXRoIjoiZW52L2Rldi8iLCJ0YXJnZXRQYXRocyI6WyJlbnYvc3RhZ2luZy8iXX19fQ==|-->`

	mock := promotionMockForMergedMR(basicConfig)
	mock.PullRequestResponse = &gitprovider.PullRequest{
		Number: 42,
		State:  "merged",
		Body:   metadataBody,
		Author: "alice",
	}
	details := newDetails(mock, 42)

	err := HandleMergedMR(ctx, details, nil, "main")

	require.NoError(t, err)
	assert.GreaterOrEqual(t, mock.CallCount("CreatePullRequest"), 1, "chained promotion PR should be created")
	require.NotNil(t, mock.LastCreatePR)
	// The original author "bob" from the metadata chain should be used as assignee
	// (ExecutePromotion sets Assignees to the effective author).
	assert.Contains(t, mock.LastCreatePR.Assignees, "bob", "chained metadata should preserve original author as assignee")
	// The serialized metadata in the body should also carry forward "bob".
	assert.Contains(t, mock.LastCreatePR.Body, "Telefonistka data", "body should contain serialized metadata")
}

// -----------------------------------------------------------------------
// HandlePushPromotion tests
// -----------------------------------------------------------------------

func TestHandlePushPromotion_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main":       []byte(basicConfig),
			"env/dev/app/values.yaml@main": []byte("image: v2\n"),
		},
		Directories: map[string][]*gitprovider.FileNode{
			"env/dev/app@main": {
				{Name: "values.yaml", Path: "env/dev/app/values.yaml", Type: "file", SHA: "sha-new"},
			},
		},
		DiffResponse: &gitprovider.DiffResult{
			FromSHA: "aaa00000",
			ToSHA:   "bbb11111",
			Files: []*gitprovider.CommitFile{
				{Filename: "env/dev/app/values.yaml"},
			},
		},
	}
	details := newDetails(mock, 0)

	err := HandlePushPromotion(ctx, details, "aaa0000000000000", "bbb1111111111111", "main")

	require.NoError(t, err)
	assert.GreaterOrEqual(t, mock.CallCount("CreatePullRequest"), 1, "push promotion should create PR")
}

func TestHandlePushPromotion_NoChangedFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": []byte(basicConfig),
		},
		DiffResponse: &gitprovider.DiffResult{
			FromSHA: "aaa00000",
			ToSHA:   "bbb11111",
			Files:   []*gitprovider.CommitFile{}, // empty diff
		},
	}
	details := newDetails(mock, 0)

	err := HandlePushPromotion(ctx, details, "aaa0000000000000", "bbb1111111111111", "main")

	require.NoError(t, err)
	assert.Equal(t, 0, mock.CallCount("CreatePullRequest"), "no PR for empty diff")
}

func TestHandlePushPromotion_NoPromotions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": []byte(basicConfig),
		},
		DiffResponse: &gitprovider.DiffResult{
			FromSHA: "aaa00000",
			ToSHA:   "bbb11111",
			Files: []*gitprovider.CommitFile{
				{Filename: "docs/README.md"}, // outside promotion paths
			},
		},
	}
	details := newDetails(mock, 0)

	err := HandlePushPromotion(ctx, details, "aaa0000000000000", "bbb1111111111111", "main")

	require.NoError(t, err)
	assert.Equal(t, 0, mock.CallCount("CreatePullRequest"), "no promotions for unrelated files")
}

func TestHandlePushPromotion_ConfigError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &testutils.MockProvider{
		Files: map[string][]byte{
			"telefonistka.yaml@main": []byte(strings.Repeat("{{{invalid", 10)),
		},
	}
	details := newDetails(mock, 0)

	err := HandlePushPromotion(ctx, details, "aaa0000000000000", "bbb1111111111111", "main")

	require.Error(t, err, "invalid YAML config should cause an error")
	assert.Equal(t, 0, mock.CallCount("CreatePullRequest"))
}
