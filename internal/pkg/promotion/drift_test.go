package promotion

import (
	"context"
	"testing"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareRepoDirectories_NoDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "a.yaml", Path: "src/a.yaml", Type: "file", SHA: "sha1"},
				{Name: "b.yaml", Path: "src/b.yaml", Type: "file", SHA: "sha2"},
			},
			"tgt@main": {
				{Name: "a.yaml", Path: "tgt/a.yaml", Type: "file", SHA: "sha1"},
				{Name: "b.yaml", Path: "tgt/b.yaml", Type: "file", SHA: "sha2"},
			},
		},
	}

	hasDiff, output, err := CompareRepoDirectories(ctx, provider, "o", "r", "src", "tgt", "main", "", nil, testutils.TestLogger())
	require.NoError(t, err)
	assert.False(t, hasDiff)
	assert.Empty(t, output)
}

func TestCompareRepoDirectories_ContentDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "values.yaml", Path: "src/values.yaml", Type: "file", SHA: "sha_src"},
			},
			"tgt@main": {
				{Name: "values.yaml", Path: "tgt/values.yaml", Type: "file", SHA: "sha_tgt"},
			},
		},
		Files: map[string][]byte{
			"src/values.yaml@main": []byte("image: app:v2\n"),
			"tgt/values.yaml@main": []byte("image: app:v1\n"),
		},
	}

	hasDiff, output, err := CompareRepoDirectories(ctx, provider, "o", "r", "src", "tgt", "main", "", nil, testutils.TestLogger())
	require.NoError(t, err)
	assert.True(t, hasDiff)
	assert.Contains(t, output, "app:v2")
	assert.Contains(t, output, "app:v1")
}

func TestCompareRepoDirectories_MissingSourceFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "a.yaml", Path: "src/a.yaml", Type: "file", SHA: "sha1"},
			},
			"tgt@main": {
				{Name: "a.yaml", Path: "tgt/a.yaml", Type: "file", SHA: "sha1"},
				{Name: "extra.yaml", Path: "tgt/extra.yaml", Type: "file", SHA: "sha_extra"},
			},
		},
	}

	hasDiff, output, err := CompareRepoDirectories(ctx, provider, "o", "r", "src", "tgt", "main", "", nil, testutils.TestLogger())
	require.NoError(t, err)
	assert.True(t, hasDiff)
	assert.Contains(t, output, "extra.yaml")
	assert.Contains(t, output, "missing from source")
}

func TestCompareRepoDirectories_MissingTargetFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "a.yaml", Path: "src/a.yaml", Type: "file", SHA: "sha1"},
				{Name: "new.yaml", Path: "src/new.yaml", Type: "file", SHA: "sha_new"},
			},
			"tgt@main": {
				{Name: "a.yaml", Path: "tgt/a.yaml", Type: "file", SHA: "sha1"},
			},
		},
	}

	hasDiff, output, err := CompareRepoDirectories(ctx, provider, "o", "r", "src", "tgt", "main", "", nil, testutils.TestLogger())
	require.NoError(t, err)
	assert.True(t, hasDiff)
	assert.Contains(t, output, "new.yaml")
	assert.Contains(t, output, "missing from target")
}

func TestCompareRepoDirectories_SourceDirMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"tgt@main": {
				{Name: "a.yaml", Path: "tgt/a.yaml", Type: "file", SHA: "sha1"},
			},
		},
	}

	hasDiff, output, err := CompareRepoDirectories(ctx, provider, "o", "r", "src", "tgt", "main", "", nil, testutils.TestLogger())
	require.NoError(t, err)
	assert.True(t, hasDiff)
	assert.Contains(t, output, "Source directory")
	assert.Contains(t, output, "not found")
}

func TestCompareRepoDirectories_TargetDirMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "a.yaml", Path: "src/a.yaml", Type: "file", SHA: "sha1"},
			},
		},
	}

	hasDiff, output, err := CompareRepoDirectories(ctx, provider, "o", "r", "src", "tgt", "main", "", nil, testutils.TestLogger())
	require.NoError(t, err)
	assert.True(t, hasDiff)
	assert.Contains(t, output, "Target directory")
	assert.Contains(t, output, "not found")
}

func TestCompareRepoDirectories_BlockListExcludes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "values.yaml", Path: "src/values.yaml", Type: "file", SHA: "sha1"},
				{Name: "secret.yaml", Path: "src/secret.yaml", Type: "file", SHA: "sha_secret_new"},
			},
			"tgt@main": {
				{Name: "values.yaml", Path: "tgt/values.yaml", Type: "file", SHA: "sha1"},
				{Name: "secret.yaml", Path: "tgt/secret.yaml", Type: "file", SHA: "sha_secret_old"},
			},
		},
	}

	blockList := []string{"secret.yaml"}
	hasDiff, _, err := CompareRepoDirectories(ctx, provider, "o", "r", "src", "tgt", "main", "", blockList, testutils.TestLogger())
	require.NoError(t, err)
	// Only secret.yaml differs but it's blocked → no drift
	assert.False(t, hasDiff)
}

func TestCompareRepoDirectories_BlameLinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "v.yaml", Path: "src/v.yaml", Type: "file", SHA: "sha_new"},
			},
			"tgt@main": {
				{Name: "v.yaml", Path: "tgt/v.yaml", Type: "file", SHA: "sha_old"},
			},
		},
		Files: map[string][]byte{
			"src/v.yaml@main": []byte("a: 1\n"),
			"tgt/v.yaml@main": []byte("a: 2\n"),
		},
	}

	hasDiff, output, err := CompareRepoDirectories(ctx, provider, "o", "r", "src", "tgt", "main", "https://example.com/-/blame/HEAD", nil, testutils.TestLogger())
	require.NoError(t, err)
	assert.True(t, hasDiff)
	assert.Contains(t, output, "Blame Links")
	assert.Contains(t, output, "https://example.com/-/blame/HEAD/src/v.yaml")
}

func TestGenerateDriftComment_Structure(t *testing.T) {
	t.Parallel()
	diffMap := map[string]string{
		"`env/dev/app` ↔️  `env/staging/app`": "some diff output",
	}
	comment := GenerateDriftComment(diffMap)
	assert.Contains(t, comment, "Found drift")
	assert.Contains(t, comment, "env/dev/app")
	assert.Contains(t, comment, "some diff output")
	assert.Contains(t, comment, "<details>")
}

func TestGenerateDriftComment_Empty(t *testing.T) {
	t.Parallel()
	comment := GenerateDriftComment(map[string]string{})
	assert.Contains(t, comment, "Found drift")
	// No diff sections
	assert.NotContains(t, comment, "<details>")
}
