package promotion

import (
	"strings"
	"testing"

	cfg "github.com/schubergphilis/container-platform-telefonistka/internal/pkg/configuration"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func testLogger() *log.Entry {
	return log.WithField("test", true)
}

func TestIdentifyRelevantComponents_LiteralPath(t *testing.T) {
	t.Parallel()
	config := &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath: "prod/us-east-4/",
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"prod/eu-west-1/"}},
				},
			},
		},
	}

	changedFiles := []string{
		"prod/us-east-4/componentA/file.yaml",
		"prod/us-east-4/componentA/file2.yaml",
		".ci-config/random-file.json",
	}

	result := IdentifyRelevantComponents(changedFiles, config, testLogger())

	assert.Len(t, result, 1)
	for rc := range result {
		assert.Equal(t, "prod/us-east-4/", rc.SourcePath)
		assert.Equal(t, "componentA", rc.ComponentName)
	}
}

func TestIdentifyRelevantComponents_RegexPath(t *testing.T) {
	t.Parallel()
	config := &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath: "dev/[^/]*/",
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"prod/eu-west-1/"}},
				},
			},
		},
	}

	changedFiles := []string{
		"dev/us-east-4/componentA/file.yaml",
		"dev/us-east-5/componentA/file.yaml",
	}

	result := IdentifyRelevantComponents(changedFiles, config, testLogger())

	// Should find components from both matching source paths
	assert.Len(t, result, 2)

	var paths []string
	for rc := range result {
		paths = append(paths, rc.SourcePath)
		assert.Equal(t, "componentA", rc.ComponentName)
	}
	assert.Contains(t, paths, "dev/us-east-4/")
	assert.Contains(t, paths, "dev/us-east-5/")
}

func TestIdentifyRelevantComponents_NoMatch(t *testing.T) {
	t.Parallel()
	config := &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath: "prod/us-east-4/",
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"prod/eu-west-1/"}},
				},
			},
		},
	}

	changedFiles := []string{
		".ci-config/random-file.json",
		"docs/README.md",
	}

	result := IdentifyRelevantComponents(changedFiles, config, testLogger())
	assert.Len(t, result, 0)
}

func TestIdentifyRelevantComponents_MalformedRegex(t *testing.T) {
	t.Parallel()
	// Malformed regex in SourcePath should not panic — it should be logged and skipped
	config := &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				// This is valid for initial match (regexp.MatchString handles it)
				// but would panic in MustCompile when building component regex
				// since SourcePath is used as-is. We test that it doesn't panic.
				SourcePath: "prod/[invalid/",
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"staging/"}},
				},
			},
		},
	}

	changedFiles := []string{
		"prod/[invalid/componentA/file.yaml",
	}

	// This must not panic — the old code would have panicked on regexp.MustCompile
	assert.NotPanics(t, func() {
		IdentifyRelevantComponents(changedFiles, config, testLogger())
	})
}

func TestIdentifyRelevantComponents_ExtraDepth(t *testing.T) {
	t.Parallel()
	config := &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath:              "prod/us-east-4/",
				ComponentPathExtraDepth: 2,
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"prod/eu-west-1/"}},
				},
			},
		},
	}

	changedFiles := []string{
		"prod/us-east-4/teamA/namespaceB/componentA/file.yaml",
	}

	result := IdentifyRelevantComponents(changedFiles, config, testLogger())

	assert.Len(t, result, 1)
	for rc := range result {
		assert.Equal(t, "prod/us-east-4/", rc.SourcePath)
		assert.Equal(t, "teamA/namespaceB/componentA", rc.ComponentName)
	}
}

func TestIdentifyRelevantComponents_MultiplePromotionPaths(t *testing.T) {
	t.Parallel()
	config := &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath: "dev/",
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"staging/"}},
				},
			},
			{
				SourcePath: "staging/",
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"prod/"}},
				},
			},
		},
	}

	changedFiles := []string{
		"dev/componentA/file.yaml",
		"staging/componentB/file.yaml",
	}

	result := IdentifyRelevantComponents(changedFiles, config, testLogger())

	assert.Len(t, result, 2)

	names := make(map[string]string)
	for rc := range result {
		names[rc.ComponentName] = rc.SourcePath
	}
	assert.Equal(t, "dev/", names["componentA"])
	assert.Equal(t, "staging/", names["componentB"])
}

func TestIdentifyRelevantComponents_FileMatchesFirstPathOnly(t *testing.T) {
	t.Parallel()
	// A file can only be a single "source dir" — first match wins
	config := &cfg.Config{
		PromotionPaths: []cfg.PromotionPath{
			{
				SourcePath: "env/",
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"target1/"}},
				},
			},
			{
				SourcePath: "env/",
				PromotionPrs: []cfg.PromotionPr{
					{TargetPaths: []string{"target2/"}},
				},
			},
		},
	}

	changedFiles := []string{"env/comp/file.yaml"}

	result := IdentifyRelevantComponents(changedFiles, config, testLogger())

	// Should only match first PromotionPath (break after first match)
	assert.Len(t, result, 1)
}

// ---------------------------------------------------------------------------
// ContainsString tests
// ---------------------------------------------------------------------------

func TestContainsString_Found(t *testing.T) {
	t.Parallel()
	assert.True(t, ContainsString([]string{"alpha", "beta", "gamma"}, "beta"))
}

func TestContainsString_NotFound(t *testing.T) {
	t.Parallel()
	assert.False(t, ContainsString([]string{"alpha", "beta", "gamma"}, "delta"))
}

func TestContainsString_EmptySlice(t *testing.T) {
	t.Parallel()
	assert.False(t, ContainsString([]string{}, "anything"))
	assert.False(t, ContainsString(nil, "anything"))
}

// ---------------------------------------------------------------------------
// ContainMatchingRegex tests
// ---------------------------------------------------------------------------

func TestContainMatchingRegex_Match(t *testing.T) {
	t.Parallel()
	assert.True(t, ContainMatchingRegex([]string{`^prod/.*`}, "prod/us-east-1/app"))
}

func TestContainMatchingRegex_NoMatch(t *testing.T) {
	t.Parallel()
	assert.False(t, ContainMatchingRegex([]string{`^prod/.*`}, "dev/us-east-1/app"))
}

func TestContainMatchingRegex_InvalidRegex(t *testing.T) {
	t.Parallel()
	// An invalid regex should not match and should not panic.
	assert.False(t, ContainMatchingRegex([]string{`[invalid`}, "anything"))
}

// ---------------------------------------------------------------------------
// IsFileBlocked tests
// ---------------------------------------------------------------------------

func TestIsFileBlocked_Match(t *testing.T) {
	t.Parallel()
	assert.True(t, IsFileBlocked("manifests/application.yaml", []string{"manifests/application.yaml"}))
}

func TestIsFileBlocked_NoMatch(t *testing.T) {
	t.Parallel()
	assert.False(t, IsFileBlocked("manifests/deployment.yaml", []string{"manifests/application.yaml"}))
}

func TestIsFileBlocked_GlobPattern(t *testing.T) {
	t.Parallel()
	assert.True(t, IsFileBlocked("deep/nested/application.yaml", []string{"**/application.yaml"}))
	assert.True(t, IsFileBlocked("manifests/config.yaml", []string{"manifests/*.yaml"}))
	assert.False(t, IsFileBlocked("other/config.yaml", []string{"manifests/*.yaml"}))
}

func TestIsFileBlocked_EmptyBlockList(t *testing.T) {
	t.Parallel()
	assert.False(t, IsFileBlocked("anything.yaml", []string{}))
	assert.False(t, IsFileBlocked("anything.yaml", nil))
}

func TestIsFileBlocked_InvalidPattern(t *testing.T) {
	t.Parallel()
	// doublestar treats most patterns gracefully; a truly invalid pattern
	// (e.g. unclosed bracket) should not panic and should not block the file.
	assert.False(t, IsFileBlocked("file.yaml", []string{"[invalid"}))
}

// ---------------------------------------------------------------------------
// FirstN tests
// ---------------------------------------------------------------------------

func TestFirstN_Short(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hi", FirstN("hi", 10))
}

func TestFirstN_Long(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hel", FirstN("hello", 3))
}

func TestFirstN_Empty(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", FirstN("", 5))
}

func TestFirstN_Unicode(t *testing.T) {
	t.Parallel()
	// Each character is a single rune, even though multi-byte in UTF-8.
	assert.Equal(t, "ab", FirstN("abcdef", 2))
	assert.Equal(t, "日本", FirstN("日本語テスト", 2))
}

// ---------------------------------------------------------------------------
// GenerateSafePromotionBranchName tests
// ---------------------------------------------------------------------------

func TestGenerateSafePromotionBranchName_Basic(t *testing.T) {
	t.Parallel()
	name := GenerateSafePromotionBranchName(42, "my-feature", []string{"prod/eu-west-1/"})
	assert.True(t, len(name) > 0)
	assert.True(t, strings.HasPrefix(name, "promotions/42-my-feature-"))
}

func TestGenerateSafePromotionBranchName_SlashesReplaced(t *testing.T) {
	t.Parallel()
	name := GenerateSafePromotionBranchName(1, "feat/sub/thing", []string{"target/"})
	// Slashes in the original branch name should be replaced with dashes.
	assert.NotContains(t, strings.TrimPrefix(name, "promotions/"), "/")
}

func TestGenerateSafePromotionBranchName_LongBranch(t *testing.T) {
	t.Parallel()
	longBranch := strings.Repeat("a", 300)
	name := GenerateSafePromotionBranchName(99, longBranch, []string{"target/"})
	// Total length must not exceed 250 characters (the function caps the original name at 200).
	assert.LessOrEqual(t, len(name), 250)
}

func TestGenerateSafePromotionBranchName_Deterministic(t *testing.T) {
	t.Parallel()
	a := GenerateSafePromotionBranchName(7, "branch", []string{"path-a/", "path-b/"})
	b := GenerateSafePromotionBranchName(7, "branch", []string{"path-a/", "path-b/"})
	assert.Equal(t, a, b, "same inputs must produce the same branch name")
}
