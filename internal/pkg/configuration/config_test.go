package configuration

import (
	"os"
	"testing"

	"github.com/go-test/deep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigurationParse(t *testing.T) {
	t.Parallel()

	configurationFileContent, _ := os.ReadFile("tests/testConfigurationParsing.yaml")

	config, err := ParseConfigFromYaml(string(configurationFileContent))
	if err != nil {
		t.Fatalf("config parsing failed: err=%s", err)
	}

	if config.PromotionPaths == nil {
		t.Fatalf("config is missing PromotionPaths, %v", config.PromotionPaths)
	}

	expectedConfig := &Config{
		PromotionPaths: []PromotionPath{
			{
				SourcePath: "workspace/",
				Conditions: Condition{
					PrHasLabels: []string{
						"some-label",
					},
					AutoMerge: true,
				},
				PromotionPrs: []PromotionPr{
					{
						TargetPaths: []string{
							"env/staging/us-east4/c1/",
						},
					},
					{
						TargetPaths: []string{
							"env/staging/europe-west4/c1/",
						},
					},
				},
			},
			{
				SourcePath: "env/staging/us-east4/c1/",
				Conditions: Condition{
					AutoMerge: false,
				},
				PromotionPrs: []PromotionPr{
					{
						TargetPaths: []string{
							"env/prod/us-central1/c2/",
						},
					},
				},
			},
			{
				SourcePath: "env/prod/us-central1/c2/",
				Conditions: Condition{
					AutoMerge: false,
				},
				PromotionPrs: []PromotionPr{
					{
						TargetPaths: []string{
							"env/prod/us-west1/c2/",
							"env/prod/us-central1/c3/",
						},
					},
				},
			},
		},
	}

	if diff := deep.Equal(expectedConfig, config); diff != nil {
		t.Error(diff)
	}
}

func TestConfigurationParseBlockList(t *testing.T) {
	t.Parallel()

	configurationFileContent, _ := os.ReadFile("tests/testConfigurationParsingBlockList.yaml")

	config, err := ParseConfigFromYaml(string(configurationFileContent))
	if err != nil {
		t.Fatalf("config parsing failed: err=%s", err)
	}

	if config.PromotionPaths == nil {
		t.Fatalf("config is missing PromotionPaths, %v", config.PromotionPaths)
	}

	expectedConfig := &Config{
		PromotionPaths: []PromotionPath{
			{
				SourcePath: "local/",
				PromotionPrs: []PromotionPr{
					{
						TargetDescription: "Dev",
						TargetPaths: []string{
							"dev/",
						},
						BlockList: []string{
							"**/application.yaml",
							"**/values-env.yaml",
						},
					},
				},
			},
			{
				SourcePath: "dev/",
				PromotionPrs: []PromotionPr{
					{
						TargetDescription: "Staging",
						TargetPaths: []string{
							"staging/",
						},
						BlockList: []string{
							"**/application.yaml",
						},
					},
				},
			},
		},
	}

	if diff := deep.Equal(expectedConfig, config); diff != nil {
		t.Error(diff)
	}
}

// --- Validate() tests ---

func TestValidate_ValidConfig(t *testing.T) {
	t.Parallel()
	config := &Config{PromotionPaths: []PromotionPath{{
		SourcePath: "dev/", PromotionPrs: []PromotionPr{{TargetPaths: []string{"staging/"}}},
	}}}
	assert.NoError(t, config.Validate())
}

func TestValidate_EmptyPromotionPaths(t *testing.T) {
	t.Parallel()
	require.Error(t, (&Config{}).Validate())
}

func TestValidate_EmptySourcePath(t *testing.T) {
	t.Parallel()
	config := &Config{PromotionPaths: []PromotionPath{{
		SourcePath: "", PromotionPrs: []PromotionPr{{TargetPaths: []string{"staging/"}}},
	}}}
	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sourcePath must not be empty")
}

func TestValidate_EmptyPromotionPrs(t *testing.T) {
	t.Parallel()
	config := &Config{PromotionPaths: []PromotionPath{{
		SourcePath: "dev/", PromotionPrs: []PromotionPr{},
	}}}
	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "promotionPrs must not be empty")
}

func TestValidate_EmptyTargetPaths(t *testing.T) {
	t.Parallel()
	config := &Config{PromotionPaths: []PromotionPath{{
		SourcePath: "dev/", PromotionPrs: []PromotionPr{{TargetPaths: []string{}}},
	}}}
	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "targetPaths must not be empty")
}

func TestValidate_SecondPathInvalid(t *testing.T) {
	t.Parallel()
	config := &Config{PromotionPaths: []PromotionPath{
		{SourcePath: "dev/", PromotionPrs: []PromotionPr{{TargetPaths: []string{"staging/"}}}},
		{SourcePath: "", PromotionPrs: []PromotionPr{}},
	}}
	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "promotionPaths[1]")
}

func TestValidate_SecondPromotionPrInvalid(t *testing.T) {
	t.Parallel()
	config := &Config{PromotionPaths: []PromotionPath{{
		SourcePath: "dev/",
		PromotionPrs: []PromotionPr{
			{TargetPaths: []string{"staging/"}},
			{TargetPaths: []string{}},
		},
	}}}
	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "promotionPrs[1]")
}

func TestGetDefaultBranch_Configured(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "master", (&Config{DefaultBranch: "master"}).GetDefaultBranch())
}

func TestGetDefaultBranch_FallbackToMain(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "main", (&Config{}).GetDefaultBranch())
}

func TestParseConfigFromYaml_EmptyYAML(t *testing.T) {
	t.Parallel()
	config, err := ParseConfigFromYaml("")
	require.NoError(t, err)
	assert.Empty(t, config.PromotionPaths)
}

func TestParseConfigFromYaml_MalformedYAML(t *testing.T) {
	t.Parallel()
	_, err := ParseConfigFromYaml("{{{{not yaml")
	assert.Error(t, err)
}

func TestParseConfigFromYaml_UnknownFieldsIgnored(t *testing.T) {
	t.Parallel()
	config, err := ParseConfigFromYaml("unknownField: true\npromotionPaths:\n  - sourcePath: dev/\n    promotionPrs:\n      - targetPaths: [staging/]\n")
	require.NoError(t, err)
	assert.Len(t, config.PromotionPaths, 1)
}

func TestParseConfigFromYaml_BackwardCompatLegacyKey(t *testing.T) {
	t.Parallel()
	config, err := ParseConfigFromYaml("promtionPRlables:\n  - promotion\n  - automated\npromotionPaths:\n  - sourcePath: dev/\n    promotionPrs:\n      - targetPaths: [staging/]\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"promotion", "automated"}, config.PromotionPRLabels)
}

func TestParseConfigFromYaml_NewKeyTakesPrecedence(t *testing.T) {
	t.Parallel()
	config, err := ParseConfigFromYaml("promotionPRLabels:\n  - new-label\npromtionPRlables:\n  - old-label\npromotionPaths:\n  - sourcePath: dev/\n    promotionPrs:\n      - targetPaths: [staging/]\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"new-label"}, config.PromotionPRLabels)
}

func TestParseConfigFromYaml_FullConfig(t *testing.T) {
	t.Parallel()
	y := "defaultBranch: master\ndryRunMode: true\nautoApprovePromotionPrs: true\npromotionPRLabels: [promotion]\npromotionPaths:\n  - sourcePath: dev/\n    conditions:\n      autoMerge: true\n      prHasLabels: [deploy]\n    promotionPrs:\n      - targetPaths: [staging/]\n        targetDescription: All staging\n        blockList: ['**/secrets.yaml']\nargocd:\n  commentDiffonPR: true\n"
	config, err := ParseConfigFromYaml(y)
	require.NoError(t, err)
	assert.Equal(t, "master", config.DefaultBranch)
	assert.True(t, config.DryRunMode)
	assert.True(t, config.AutoApprovePromotionPrs)
	assert.Equal(t, []string{"promotion"}, config.PromotionPRLabels)
	assert.True(t, config.PromotionPaths[0].Conditions.AutoMerge)
	assert.Equal(t, []string{"deploy"}, config.PromotionPaths[0].Conditions.PrHasLabels)
	assert.Equal(t, "All staging", config.PromotionPaths[0].PromotionPrs[0].TargetDescription)
	assert.Equal(t, []string{"**/secrets.yaml"}, config.PromotionPaths[0].PromotionPrs[0].BlockList)
	assert.True(t, config.Argocd.CommentDiffonPR)
}
