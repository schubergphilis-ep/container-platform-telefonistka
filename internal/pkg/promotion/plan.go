package promotion

import (
	"context"
	"regexp"
	"sort"
	"strings"

	cfg "github.com/schubergphilis/container-platform-telefonistka/internal/pkg/configuration"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	log "github.com/sirupsen/logrus"
)

// GeneratePromotionPlan generates a map of promotions based on changed files and config.
// changedFiles is a list of file paths that changed (from MR files or commit comparison).
func GeneratePromotionPlan(
	ctx context.Context,
	provider gitprovider.GitProvider,
	owner, repo string,
	changedFiles []string,
	labels []string,
	config *cfg.Config,
	defaultBranch string,
	prLogger *log.Entry,
) (map[string]PromotionInstance, error) {
	relevantComponents := IdentifyRelevantComponents(changedFiles, config, prLogger)
	return GeneratePlanFromComponents(ctx, provider, owner, repo, config, relevantComponents, labels, defaultBranch, prLogger)
}

// IdentifyRelevantComponents extracts component names from changed files based on config PromotionPaths.
func IdentifyRelevantComponents(changedFiles []string, config *cfg.Config, prLogger *log.Entry) map[RelevantComponent]struct{} {
	relevantComponents := make(map[RelevantComponent]struct{})

	// Pre-compile source path match regexes to avoid recompiling per file.
	type compiledPath struct {
		config       cfg.PromotionPath
		matchRegex   *regexp.Regexp
		componentFmt string // format string for component regex, filled per-depth
	}
	var compiledPaths []compiledPath
	for _, ppc := range config.PromotionPaths {
		re, err := regexp.Compile("^" + ppc.SourcePath + ".*")
		if err != nil {
			prLogger.Errorf("Invalid sourcePath regex %q: %v, skipping", ppc.SourcePath, err)
			continue
		}
		componentPathRegexSubStrings := make([]string, 0, ppc.ComponentPathExtraDepth+1)
		for i := 0; i <= ppc.ComponentPathExtraDepth; i++ {
			componentPathRegexSubStrings = append(componentPathRegexSubStrings, "[^/]*")
		}
		compiledPaths = append(compiledPaths, compiledPath{
			config:       ppc,
			matchRegex:   re,
			componentFmt: strings.Join(componentPathRegexSubStrings, "/"),
		})
	}

	for _, filename := range changedFiles {
		for _, cp := range compiledPaths {
			if cp.matchRegex.MatchString(filename) {
				getComponentRegex, err := regexp.Compile("^" + cp.config.SourcePath + "(" + cp.componentFmt + ")/.*")
				if err != nil {
					prLogger.Errorf("Invalid sourcePath regex %q: %v, skipping", cp.config.SourcePath, err)
					break
				}
				componentName := getComponentRegex.ReplaceAllString(filename, "${1}")

				getSourcePathRegex, err := regexp.Compile("^(" + cp.config.SourcePath + ")" + regexp.QuoteMeta(componentName) + "/.*")
				if err != nil {
					prLogger.Errorf("Invalid sourcePath regex %q: %v, skipping", cp.config.SourcePath, err)
					break
				}
				compiledSourcePath := getSourcePathRegex.ReplaceAllString(filename, "${1}")

				rc := RelevantComponent{
					SourcePath:    compiledSourcePath,
					ComponentName: componentName,
					AutoMerge:     cp.config.Conditions.AutoMerge,
				}
				relevantComponents[rc] = struct{}{}
				break // a file can only be a single "source dir"
			}
		}
	}
	return relevantComponents
}

// GeneratePlanFromComponents creates PromotionInstances by matching components against config.
func GeneratePlanFromComponents(
	ctx context.Context,
	provider gitprovider.GitProvider,
	owner, repo string,
	config *cfg.Config,
	relevantComponents map[RelevantComponent]struct{},
	labels []string,
	configBranch string,
	prLogger *log.Entry,
) (map[string]PromotionInstance, error) {
	promotions := make(map[string]PromotionInstance)

	// Pre-compile promotion path regexes to avoid recompiling per component.
	type compiledPromotionPath struct {
		config cfg.PromotionPath
		regex  *regexp.Regexp
	}
	var compiledPromotionPaths []compiledPromotionPath
	for _, cpp := range config.PromotionPaths {
		re, err := regexp.Compile(cpp.SourcePath)
		if err != nil {
			prLogger.Errorf("Invalid promotion path regex %q: %v, skipping", cpp.SourcePath, err)
			continue
		}
		compiledPromotionPaths = append(compiledPromotionPaths, compiledPromotionPath{config: cpp, regex: re})
	}

	for componentToPromote := range relevantComponents {
		componentConfig, err := GetComponentConfig(ctx, provider, owner, repo, componentToPromote.SourcePath+componentToPromote.ComponentName, configBranch, prLogger)
		if err != nil {
			prLogger.Errorf("Failed to get in component configuration, err=%s, skipping %s", err, componentToPromote.SourcePath+componentToPromote.ComponentName)
		}

		for _, cpp := range compiledPromotionPaths {
			configPromotionPath := cpp.config
			if cpp.regex.MatchString(componentToPromote.SourcePath) {
				if configPromotionPath.Conditions.PrHasLabels != nil {
					hasRightLabel := false
					for _, l := range labels {
						if ContainsString(configPromotionPath.Conditions.PrHasLabels, l) {
							hasRightLabel = true
							break
						}
					}
					if !hasRightLabel {
						continue
					}
				}

				for _, ppr := range configPromotionPath.PromotionPrs {
					sort.Strings(ppr.TargetPaths)

					mapKey := configPromotionPath.SourcePath + ">" + strings.Join(ppr.TargetPaths, "|")
					if entry, ok := promotions[mapKey]; !ok {
						if ppr.TargetDescription == "" {
							ppr.TargetDescription = strings.Join(ppr.TargetPaths, " ")
						}
						promotions[mapKey] = PromotionInstance{
							Metadata: PromotionInstanceMetaData{
								TargetPaths:                    ppr.TargetPaths,
								TargetDescription:              ppr.TargetDescription,
								SourcePath:                     componentToPromote.SourcePath,
								ComponentNames:                 []string{componentToPromote.ComponentName},
								PerComponentSkippedTargetPaths: map[string][]string{},
								AutoMerge:                      componentToPromote.AutoMerge,
								BlockList:                      ppr.BlockList,
							},
							ComputedSyncPaths: map[string]string{},
						}
					} else if !ContainsString(entry.Metadata.ComponentNames, componentToPromote.ComponentName) {
						entry.Metadata.ComponentNames = append(entry.Metadata.ComponentNames, componentToPromote.ComponentName)
						promotions[mapKey] = entry
					}

					for _, individualPath := range ppr.TargetPaths {
						if componentConfig != nil {
							if componentConfig.PromotionTargetBlockList != nil {
								if ContainMatchingRegex(componentConfig.PromotionTargetBlockList, individualPath) {
									promotions[mapKey].Metadata.PerComponentSkippedTargetPaths[componentToPromote.ComponentName] = append(
										promotions[mapKey].Metadata.PerComponentSkippedTargetPaths[componentToPromote.ComponentName], individualPath)
									continue
								}
							}
							if componentConfig.PromotionTargetAllowList != nil {
								if !ContainMatchingRegex(componentConfig.PromotionTargetAllowList, individualPath) {
									promotions[mapKey].Metadata.PerComponentSkippedTargetPaths[componentToPromote.ComponentName] = append(
										promotions[mapKey].Metadata.PerComponentSkippedTargetPaths[componentToPromote.ComponentName], individualPath)
									continue
								}
							}
						}
						promotions[mapKey].ComputedSyncPaths[individualPath+componentToPromote.ComponentName] = componentToPromote.SourcePath + componentToPromote.ComponentName
					}
				}
				break
			}
		}
	}
	return promotions, nil
}
