package gitlabapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	prom "github.com/schubergphilis/container-platform-telefonistka/internal/pkg/promotion"
)

type (
	PromotionInstance         = prom.PromotionInstance
	PromotionInstanceMetaData = prom.PromotionInstanceMetaData
)

// DetectDrift compares source and target directories for each promotion path
// and returns a comment body describing any drift found.
func DetectDrift(ctx context.Context, details ProviderClientDetails) error {
	details.PrLogger.Debug("Checking for drift")
	if ctx.Err() != nil {
		return ctx.Err()
	}

	defaultBranch, err := details.Provider.GetDefaultBranch(ctx, details.Owner, details.Repo)
	if err != nil {
		return fmt.Errorf("failed to get default branch: %w", err)
	}

	config, err := prom.GetRepoConfig(ctx, details.Provider, details.Owner, details.Repo, defaultBranch, details.PrLogger)
	if err != nil {
		_, _ = details.Provider.CommentOnPullRequest(ctx, details.Owner, details.Repo, details.PrNumber,
			fmt.Sprintf("Failed to get configuration\n```\n%s\n```\n", err))
		return err
	}

	mrFiles, err := details.Provider.ListPullRequestFiles(ctx, details.Owner, details.Repo, details.PrNumber)
	if errors.Is(err, gitprovider.ErrNoDiffRefs) {
		details.PrLogger.Info("MR has no diff refs, skipping drift detection")
		return nil
	}
	if err != nil {
		details.PrLogger.Errorf("Failed to list MR files for drift detection: %v", err)
		return err
	}
	changedFiles := make([]string, 0, len(mrFiles))
	for _, f := range mrFiles {
		changedFiles = append(changedFiles, f.Filename)
	}

	promotions, err := prom.GeneratePromotionPlan(ctx, details.Provider, details.Owner, details.Repo, changedFiles, details.Labels, config, defaultBranch, details.PrLogger)
	if err != nil {
		return err
	}

	diffOutputMap := make(map[string]string)
	for _, promotion := range promotions {
		details.PrLogger.Debugf("Checking drift for %s", promotion.Metadata.SourcePath)
		for target, source := range promotion.ComputedSyncPaths {
			blamePrefix := fmt.Sprintf("%s/-/blame/HEAD", details.RepoURL)
			hasDiff, diffOutput, err := prom.CompareRepoDirectories(ctx, details.Provider, details.Owner, details.Repo, source, target, defaultBranch, blamePrefix, promotion.Metadata.BlockList, details.PrLogger)
			if err != nil {
				details.PrLogger.Warnf("Error comparing %s vs %s: %v", source, target, err)
				continue
			}
			if hasDiff {
				mapKey := fmt.Sprintf("`%s` ↔️  `%s`", source, target)
				diffOutputMap[mapKey] = diffOutput
				details.PrLogger.Debugf("Found diff @ %s", mapKey)
			}
		}
	}

	if len(diffOutputMap) > 0 {
		comment := prom.GenerateDriftComment(diffOutputMap)
		_, err = details.Provider.CommentOnPullRequest(ctx, details.Owner, details.Repo, details.PrNumber, comment)
		if err != nil {
			details.PrLogger.Errorf("Failed to comment drift warning: %v", err)
			return err
		}
	} else {
		details.PrLogger.Info("No drift found")
	}

	return nil
}

// HandlePushPromotion handles the promotion workflow triggered by a push to the default branch.
func HandlePushPromotion(ctx context.Context, details ProviderClientDetails, beforeSHA, afterSHA, defaultBranch string) error {
	prLogger := details.PrLogger

	config, err := prom.GetRepoConfig(ctx, details.Provider, details.Owner, details.Repo, defaultBranch, prLogger)
	if err != nil {
		prLogger.Errorf("Failed to get repo config: %v", err)
		return err
	}

	diff, err := details.Provider.CompareCommits(ctx, details.Owner, details.Repo, beforeSHA, afterSHA)
	if err != nil {
		prLogger.Errorf("Failed to compare commits %s..%s: %v", beforeSHA[:8], afterSHA[:8], err)
		return err
	}

	changedFiles := make([]string, 0, len(diff.Files))
	for _, f := range diff.Files {
		changedFiles = append(changedFiles, f.Filename)
	}

	if len(changedFiles) == 0 {
		prLogger.Info("No changed files in push, skipping promotion")
		return nil
	}

	prLogger.Infof("Found %d changed files in push", len(changedFiles))

	promotions, err := prom.GeneratePromotionPlan(ctx, details.Provider, details.Owner, details.Repo, changedFiles, nil, config, defaultBranch, prLogger)
	if err != nil {
		prLogger.Errorf("Failed to generate promotion plan: %v", err)
		return err
	}

	if len(promotions) == 0 {
		prLogger.Info("No promotions needed for this push")
		return nil
	}

	for _, promotion := range promotions {
		err := prom.ExecutePromotion(ctx, details.Provider, nil, details.Owner, details.Repo, defaultBranch,
			0, details.Ref, details.PrAuthor, details.RepoURL, config, promotion, nil, "!", prLogger)
		if err != nil {
			prLogger.Errorf("Promotion failed for %s: %v", promotion.Metadata.SourcePath, err)
		}
	}

	prLogger.Info("Push promotion workflow completed")
	return nil
}
