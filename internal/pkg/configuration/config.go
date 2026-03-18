package configuration

import (
	"fmt"

	yaml "gopkg.in/yaml.v2"
)

type WebhookEndpointRegex struct {
	Expression   string   `yaml:"expression"`
	Replacements []string `yaml:"replacements"`
}

type ComponentConfig struct {
	PromotionTargetAllowList []string `yaml:"promotionTargetAllowList"`
	PromotionTargetBlockList []string `yaml:"promotionTargetBlockList"`
	DisableArgoCDDiff        bool     `yaml:"disableArgoCDDiff"`
}

type Condition struct {
	PrHasLabels []string `yaml:"prHasLabels"`
	AutoMerge   bool     `yaml:"autoMerge"`
}

type PromotionPr struct {
	TargetDescription string   `yaml:"targetDescription"`
	TargetPaths       []string `yaml:"targetPaths"`
	BlockList         []string `yaml:"blockList"`
}

type PromotionPath struct {
	Conditions              Condition     `yaml:"conditions"`
	ComponentPathExtraDepth int           `yaml:"componentPathExtraDepth"`
	SourcePath              string        `yaml:"sourcePath"`
	PromotionPrs            []PromotionPr `yaml:"promotionPrs"`
}

type Config struct {
	// What paths trigger promotion to which paths
	PromotionPaths []PromotionPath `yaml:"promotionPaths"`

	// Generic configuration
	DefaultBranch                string                 `yaml:"defaultBranch"`
	PromotionPRLabels            []string               `yaml:"promotionPRLabels"`
	DryRunMode                   bool                   `yaml:"dryRunMode"`
	AutoApprovePromotionPrs      bool                   `yaml:"autoApprovePromotionPrs"`
	ToggleCommitStatus           map[string]string      `yaml:"toggleCommitStatus"`
	WebhookEndpointRegexs        []WebhookEndpointRegex `yaml:"webhookEndpointRegexs"`
	WhProxtSkipTLSVerifyUpstream bool                   `yaml:"whProxtSkipTLSVerifyUpstream"`
	Argocd                       ArgocdConfig           `yaml:"argocd"`

	// Git provider configuration
	GitProvider ProviderConfig `yaml:"gitProvider"`
}

// GetDefaultBranch returns the configured default branch, falling back to "main".
func (c *Config) GetDefaultBranch() string {
	if c.DefaultBranch != "" {
		return c.DefaultBranch
	}
	return "main"
}

type ArgocdConfig struct {
	CommentDiffonPR               bool   `yaml:"commentDiffonPR"`
	AutoMergeNoDiffPRs            bool   `yaml:"autoMergeNoDiffPRs"`
	AllowSyncfromBranchPathRegex  string `yaml:"allowSyncfromBranchPathRegex"`
	UseSHALabelForAppDiscovery    bool   `yaml:"useSHALabelForAppDiscovery"`
	CreateTempAppObjectFroNewApps bool   `yaml:"createTempAppObjectFromNewApps"`
}

// ProviderConfig contains Git provider configuration
type ProviderConfig struct {
	Type   string            `yaml:"type"`   // "github" or "gitlab"
	URL    string            `yaml:"url"`    // For self-hosted instances
	Config map[string]string `yaml:"config"` // Provider-specific config
}

// Validate checks for the most common misconfigurations.
func (c *Config) Validate() error {
	if len(c.PromotionPaths) == 0 {
		return fmt.Errorf("telefonistka.yaml: promotionPaths must not be empty")
	}
	for i, pp := range c.PromotionPaths {
		if pp.SourcePath == "" {
			return fmt.Errorf("telefonistka.yaml: promotionPaths[%d].sourcePath must not be empty", i)
		}
		if len(pp.PromotionPrs) == 0 {
			return fmt.Errorf("telefonistka.yaml: promotionPaths[%d].promotionPrs must not be empty", i)
		}
		for j, pr := range pp.PromotionPrs {
			if len(pr.TargetPaths) == 0 {
				return fmt.Errorf("telefonistka.yaml: promotionPaths[%d].promotionPrs[%d].targetPaths must not be empty", i, j)
			}
		}
	}
	return nil
}

func ParseConfigFromYaml(y string) (*Config, error) {
	config := &Config{}

	err := yaml.Unmarshal([]byte(y), config)
	if err != nil {
		return config, err
	}

	// Backward compatibility: support the old misspelled YAML key "promtionPRlables"
	var raw map[string]interface{}
	if yamlErr := yaml.Unmarshal([]byte(y), &raw); yamlErr == nil {
		if legacyLabels, ok := raw["promtionPRlables"]; ok && len(config.PromotionPRLabels) == 0 {
			if labels, ok := legacyLabels.([]interface{}); ok {
				for _, l := range labels {
					if s, ok := l.(string); ok {
						config.PromotionPRLabels = append(config.PromotionPRLabels, s)
					}
				}
			}
		}
	}

	return config, err
}
