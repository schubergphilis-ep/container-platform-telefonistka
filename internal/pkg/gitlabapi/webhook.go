package gitlabapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	_ "github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider/gitlab"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/prometheus"
	prom "github.com/schubergphilis/container-platform-telefonistka/internal/pkg/promotion"
	log "github.com/sirupsen/logrus"
)

// ProviderClientDetails is a type alias for gitprovider.ProviderClientDetails,
// keeping all GitLab webhook/promotion code provider-agnostic.
type ProviderClientDetails = gitprovider.ProviderClientDetails

// ReceiveGitLabWebhook parses and validates GitLab webhook, then handles the event
func ReceiveGitLabWebhook(
	r *http.Request,
	mainProviderCache *lru.Cache[string, gitprovider.GitProvider],
	approverProviderCache *lru.Cache[string, gitprovider.GitProvider],
	webhookSecret []byte,
) error {
	// Create provider factory
	factory, err := gitprovider.NewDefaultProviderFactory(10)
	if err != nil {
		log.Errorf("Failed to create provider factory: %v", err)
		prometheus.InstrumentWebhookHit("factory_creation_failed")
		return err
	}

	// Get or create GitLab provider
	gitlabURL := os.Getenv("GITLAB_URL")
	if gitlabURL == "" {
		gitlabURL = "https://gitlab.com"
	}

	gitlabToken := os.Getenv("GITLAB_TOKEN")
	if gitlabToken == "" {
		log.Error("GITLAB_TOKEN environment variable is required")
		prometheus.InstrumentWebhookHit("missing_token")
		return fmt.Errorf("GITLAB_TOKEN is required")
	}

	config := &gitprovider.ProviderConfig{
		Type:    gitprovider.ProviderTypeGitLab,
		Token:   gitlabToken,
		BaseURL: gitlabURL,
	}

	// Get provider from cache or create new
	cacheKey := fmt.Sprintf("gitlab:%s", gitlabURL)
	var provider gitprovider.GitProvider

	if cached, ok := mainProviderCache.Get(cacheKey); ok {
		provider = cached
		log.Debug("Using cached GitLab provider")
	} else {
		provider, err = factory.Create(config)
		if err != nil {
			log.Errorf("Failed to create GitLab provider: %v", err)
			prometheus.InstrumentWebhookHit("provider_creation_failed")
			return err
		}
		mainProviderCache.Add(cacheKey, provider)
		log.Info("Created new GitLab provider")
	}

	// Validate webhook signature — always required unless the server was started
	// with ALLOW_UNSIGNED_WEBHOOKS=true (in which case webhookSecret is empty).
	if len(webhookSecret) > 0 {
		err = provider.ValidateWebhookSignature(r, webhookSecret)
		if err != nil {
			log.Errorf("Webhook signature validation failed: %v", err)
			prometheus.InstrumentWebhookHit("validation_failed")
			return err
		}
	} else {
		log.Warn("Skipping webhook signature validation: no secret configured (ALLOW_UNSIGNED_WEBHOOKS mode)")
	}

	// Parse webhook event
	event, err := provider.ParseWebhook(r, webhookSecret)
	if err != nil {
		log.Errorf("Failed to parse webhook: %v", err)
		prometheus.InstrumentWebhookHit("parsing_failed")
		return err
	}

	prometheus.InstrumentWebhookHit("successful")

	// Create approver provider if GITLAB_APPROVER_TOKEN is set
	var approverProvider gitprovider.GitProvider
	if approverToken := os.Getenv("GITLAB_APPROVER_TOKEN"); approverToken != "" {
		approverCacheKey := fmt.Sprintf("gitlab-approver:%s", gitlabURL)
		if cached, ok := approverProviderCache.Get(approverCacheKey); ok {
			approverProvider = cached
		} else {
			approverConfig := &gitprovider.ProviderConfig{
				Type:    gitprovider.ProviderTypeGitLab,
				Token:   approverToken,
				BaseURL: gitlabURL,
			}
			approverProvider, err = factory.Create(approverConfig)
			if err != nil {
				log.Warnf("Failed to create approver provider: %v (auto-approve will be disabled)", err)
			} else {
				approverProviderCache.Add(approverCacheKey, approverProvider)
				log.Info("Created approver GitLab provider")
			}
		}
	}

	// Handle event asynchronously, capturing errors via a goroutine.
	go func() {
		if err := HandleGitLabEvent(event, provider, approverProvider); err != nil {
			log.Errorf("GitLab event handler failed: %v", err)
		}
	}()

	return nil
}

// HandleGitLabEvent processes different types of GitLab events.
// It returns an error so callers (including the async goroutine) can log failures.
func HandleGitLabEvent(
	event gitprovider.Event,
	mainProvider gitprovider.GitProvider,
	approverProvider gitprovider.GitProvider,
) (retErr error) {
	eventType := string(event.Type())
	start := time.Now()

	defer func() {
		duration := time.Since(start).Seconds()
		prometheus.ObserveEventProcessingDuration("gitlab", eventType, duration)
		if r := recover(); r != nil {
			log.Errorf("Recovered from panic in HandleGitLabEvent: %v\n%s", r, debug.Stack())
			prometheus.InstrumentWebhookEvent("gitlab", eventType, "panic")
			retErr = fmt.Errorf("panic in HandleGitLabEvent: %v", r)
		}
	}()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	requestID := uuid.New().String()[:8]
	logger := log.WithField("request_id", requestID)
	logger.Infof("Handling GitLab event type: %s", event.Type())

	var handlerErr error

	switch e := event.(type) {
	case gitprovider.PullRequestEvent:
		handlerErr = handleMergeRequestEvent(ctx, e, mainProvider, approverProvider, requestID)
	case gitprovider.PushEvent:
		handlerErr = handlePushEvent(ctx, e, mainProvider, requestID)
	case gitprovider.IssueCommentEvent:
		handlerErr = handleCommentEvent(ctx, e, mainProvider, requestID)
	default:
		logger.Warnf("Unhandled event type: %T", event)
		prometheus.InstrumentWebhookEvent("gitlab", eventType, "unhandled")
		return nil
	}

	if handlerErr != nil {
		prometheus.InstrumentWebhookEvent("gitlab", eventType, "error")
		return handlerErr
	}
	prometheus.InstrumentWebhookEvent("gitlab", eventType, "processed")
	return nil
}

// handleMergeRequestEvent handles GitLab merge request events
func handleMergeRequestEvent(
	ctx context.Context,
	event gitprovider.PullRequestEvent,
	mainProvider gitprovider.GitProvider,
	approverProvider gitprovider.GitProvider,
	requestID string,
) error {
	pr := event.PullRequest()
	repo := event.Repository()
	action := event.Action()

	if pr == nil || repo == nil {
		return fmt.Errorf("merge request event has nil PullRequest or Repository")
	}

	prLogger := log.WithFields(log.Fields{
		"request_id": requestID,
		"repo":       repo.FullName,
		"number":     pr.Number,
		"event_type": "merge_request",
		"action":     action,
	})

	prLogger.Infof("Handling MR event: action=%s, MR#%d, repo=%s", action, pr.Number, repo.FullName)

	// Extract owner and repo from FullName
	owner, repoName, err := parseRepoFullName(repo.FullName)
	if err != nil {
		prLogger.Errorf("Invalid repo name: %v", err)
		return err
	}

	clientDetails := ProviderClientDetails{
		Ctx:      ctx,
		Provider: mainProvider,
		Owner:    owner,
		Repo:     repoName,
		PrNumber: pr.Number,
		PrSHA:    pr.HeadSHA,
		Ref:      pr.HeadRef,
		RepoURL:  repo.HTMLURL,
		PrAuthor: pr.Author,
		PrLogger: prLogger,
		Labels:   pr.Labels,
	}

	// Set commit status to pending at start
	prom.SetCommitStatus(ctx, mainProvider, owner, repoName, pr.HeadSHA, "pending", prLogger)

	var processingErr error
	defer func() {
		if processingErr != nil {
			prom.SetCommitStatus(ctx, mainProvider, owner, repoName, pr.HeadSHA, "error", prLogger)
			return
		}
		prom.SetCommitStatus(ctx, mainProvider, owner, repoName, pr.HeadSHA, "success", prLogger)
	}()

	switch action {
	case "opened", "synchronize", "update":
		// Check for drift, add warnings
		prLogger.Info("MR opened or updated - checking for drift")
		processingErr = handleMROpenedOrUpdated(ctx, clientDetails)
		if processingErr != nil {
			prLogger.Errorf("Error handling MR opened/updated: %v", processingErr)
		}

	case "merged", "closed":
		if pr.Merged {
			// Handle promotion workflow
			prLogger.Info("MR merged - starting promotion workflow")
			processingErr = handleMRMerged(ctx, clientDetails, approverProvider, "")
			if processingErr != nil {
				prLogger.Errorf("Error handling MR merged: %v", processingErr)
			}
		} else {
			prLogger.Info("MR closed without merge - no action needed")
		}

	default:
		prLogger.Debugf("Ignoring MR action: %s", action)
	}
	return processingErr
}

// handlePushEvent handles GitLab push events.
// NOTE: Push-triggered promotion and webhook proxy forwarding are not yet implemented
// for GitLab. Only logging is performed. See GitHub's handlePushEvent for reference.
func handlePushEvent(
	ctx context.Context,
	event gitprovider.PushEvent,
	provider gitprovider.GitProvider,
	requestID string,
) error {
	repo := event.Repository()
	if repo == nil {
		return fmt.Errorf("push event has nil Repository")
	}
	pushLogger := log.WithFields(log.Fields{
		"request_id": requestID,
		"repo":       repo.FullName,
		"ref":        event.Ref(),
		"event_type": "push",
	})
	pushLogger.Warn("Push event handling is not yet implemented for GitLab — event logged but no promotion or webhook forwarding will occur")
	pushLogger.Debugf("Push event: %d commits", len(event.Commits()))
	return nil
}

// handleCommentEvent handles GitLab comment/note events
func handleCommentEvent(
	ctx context.Context,
	event gitprovider.IssueCommentEvent,
	provider gitprovider.GitProvider,
	requestID string,
) error {
	pr := event.Issue()
	comment := event.Comment()
	repo := event.Repository()

	if pr == nil || comment == nil || repo == nil {
		return fmt.Errorf("comment event has nil Issue, Comment, or Repository")
	}

	prLogger := log.WithFields(log.Fields{
		"request_id": requestID,
		"repo":       repo.FullName,
		"number":     pr.Number,
		"event_type": "comment",
	})

	commentPreview := comment.Body
	if len(commentPreview) > 50 {
		commentPreview = commentPreview[:50]
	}
	prLogger.Infof("Handling comment event: comment=%s", commentPreview)

	// Extract owner and repo
	owner, repoName, err := parseRepoFullName(repo.FullName)
	if err != nil {
		prLogger.Errorf("Invalid repo name in comment event: %v", err)
		return err
	}

	// Skip comments from the bot itself to prevent infinite loops
	if provider != nil {
		botUser, botErr := provider.GetBotIdentity(ctx)
		if botErr != nil {
			prLogger.Debugf("Could not check bot identity: %v", botErr)
		} else if botUser != nil && botUser.Login == event.Sender() {
			prLogger.Debug("Ignoring comment from bot user")
			return nil
		}
	}

	clientDetails := ProviderClientDetails{
		Ctx:      ctx,
		Provider: provider,
		Owner:    owner,
		Repo:     repoName,
		PrNumber: pr.Number,
		PrLogger: prLogger,
	}

	// Handle bot commands in comments (like /promote, /sync, etc.)
	err = handleBotCommands(ctx, clientDetails, comment.Body, event.Sender())
	if err != nil {
		prLogger.Errorf("Error handling bot command: %v", err)
		return err
	}
	return nil
}

// handleMROpenedOrUpdated checks for drift between source and target directories and warns on the MR
func handleMROpenedOrUpdated(ctx context.Context, details ProviderClientDetails) error {
	details.PrLogger.Info("Running drift detection")
	return DetectDrift(ctx, details)
}

// HandleMergedMR is the exported entry point for the promotion workflow after an MR is merged.
// It can be called from both the webhook handler and the CI push handler.
// If defaultBranch is empty, it will be fetched from the API.
func HandleMergedMR(
	ctx context.Context,
	details ProviderClientDetails,
	approverProvider gitprovider.GitProvider,
	defaultBranch string,
) error {
	return handleMRMerged(ctx, details, approverProvider, defaultBranch)
}

// handleMRMerged handles the promotion workflow when an MR is merged
func handleMRMerged(
	ctx context.Context,
	details ProviderClientDetails,
	approverProvider gitprovider.GitProvider,
	defaultBranch string,
) error {
	details.PrLogger.Info("Starting promotion workflow for merged MR")

	// Get default branch if not provided
	if defaultBranch == "" {
		var err error
		defaultBranch, err = details.Provider.GetDefaultBranch(ctx, details.Owner, details.Repo)
		if err != nil {
			details.PrLogger.Errorf("Failed to get default branch: %v", err)
			return err
		}
	}

	// Load telefonistka.yaml from repo root
	config, err := prom.GetRepoConfig(ctx, details.Provider, details.Owner, details.Repo, defaultBranch, details.PrLogger)
	if err != nil {
		_, _ = details.Provider.CommentOnPullRequest(ctx, details.Owner, details.Repo, details.PrNumber,
			fmt.Sprintf("Failed to get configuration\n```\n%s\n```\n", err))
		return err
	}

	// Fetch the MR to get its body for metadata chain parsing
	mr, err := details.Provider.GetPullRequest(ctx, details.Owner, details.Repo, details.PrNumber)
	if err != nil {
		details.PrLogger.Warnf("Failed to fetch MR body for metadata parsing: %v", err)
	}

	// Parse existing metadata from the MR body (enables chained promotions)
	var existingMetadata *prom.PrMetadata
	if mr != nil && mr.Body != "" {
		existingMetadata = prom.ParsePrMetadata(mr.Body)
		if existingMetadata != nil {
			details.PrLogger.Infof("Found promotion metadata chain in MR body (original author: %s)", existingMetadata.OriginalPrAuthor)
		}
	}

	// List changed files in the merged MR
	mrFiles, err := details.Provider.ListPullRequestFiles(ctx, details.Owner, details.Repo, details.PrNumber)
	if errors.Is(err, gitprovider.ErrNoDiffRefs) {
		details.PrLogger.Info("MR has no diff refs (empty MR or new repo), skipping promotion")
		return nil
	}
	if err != nil {
		details.PrLogger.Errorf("Failed to list MR files: %v", err)
		return err
	}

	changedFiles := make([]string, 0, len(mrFiles))
	for _, f := range mrFiles {
		changedFiles = append(changedFiles, f.Filename)
	}

	// Generate promotion plan
	promotions, err := prom.GeneratePromotionPlan(ctx, details.Provider, details.Owner, details.Repo, changedFiles, details.Labels, config, defaultBranch, details.PrLogger)
	if err != nil {
		details.PrLogger.Errorf("Failed to generate promotion plan: %v", err)
		return err
	}

	if len(promotions) == 0 {
		details.PrLogger.Info("No promotions needed for this MR")
		prometheus.InstrumentPromotionOutcome("gitlab", "skipped")
		return nil
	}

	// Dry-run mode: comment the plan instead of executing
	if config.DryRunMode {
		prom.CommentPromotionPlan(ctx, details.Provider, details.Owner, details.Repo, details.PrNumber, promotions, details.PrLogger)
		return nil
	}

	// Determine effective author (from metadata chain or current MR)
	effectiveAuthor := details.PrAuthor
	if existingMetadata != nil && existingMetadata.OriginalPrAuthor != "" {
		effectiveAuthor = existingMetadata.OriginalPrAuthor
	}

	// Execute each promotion
	for _, promotion := range promotions {
		err := prom.ExecutePromotion(ctx, details.Provider, approverProvider, details.Owner, details.Repo, defaultBranch,
			details.PrNumber, details.Ref, effectiveAuthor, details.RepoURL, config, promotion, existingMetadata, "!", details.PrLogger)
		if err != nil {
			details.PrLogger.Errorf("Promotion failed for %s: %v", promotion.Metadata.SourcePath, err)
			_, _ = details.Provider.CommentOnPullRequest(ctx, details.Owner, details.Repo, details.PrNumber,
				fmt.Sprintf("Promotion failed for `%s`: %v", promotion.Metadata.SourcePath, err))
			prometheus.InstrumentPromotionOutcome("gitlab", "failure")
		} else {
			prometheus.InstrumentPromotionOutcome("gitlab", "success")
		}
	}

	details.PrLogger.Info("Promotion workflow completed")
	return nil
}

// handleBotCommands processes bot commands from MR comments
func handleBotCommands(
	ctx context.Context,
	details ProviderClientDetails,
	commentBody string,
	sender string,
) error {
	trimmed := strings.TrimSpace(commentBody)

	if trimmed == "/retrigger" {
		details.PrLogger.Infof("Retrigger requested by %s", sender)
		return handleRetrigger(ctx, details)
	}

	return nil
}

// handleRetrigger re-runs drift detection on an MR, as if it was just opened/updated
func handleRetrigger(ctx context.Context, details ProviderClientDetails) error {
	// Fetch MR to get full context (HeadSHA, branch, labels, author)
	mr, err := details.Provider.GetPullRequest(ctx, details.Owner, details.Repo, details.PrNumber)
	if err != nil {
		details.PrLogger.Errorf("Failed to fetch MR for retrigger: %v", err)
		return err
	}

	details.PrSHA = mr.HeadSHA
	details.Ref = mr.HeadRef
	details.PrAuthor = mr.Author
	details.Labels = mr.Labels

	// Set commit status to pending
	prom.SetCommitStatus(ctx, details.Provider, details.Owner, details.Repo, mr.HeadSHA, "pending", details.PrLogger)

	err = DetectDrift(ctx, details)
	if err != nil {
		prom.SetCommitStatus(ctx, details.Provider, details.Owner, details.Repo, mr.HeadSHA, "error", details.PrLogger)
		return err
	}

	prom.SetCommitStatus(ctx, details.Provider, details.Owner, details.Repo, mr.HeadSHA, "success", details.PrLogger)
	return nil
}

// parseRepoFullName splits "group/subgroup/repo" into (owner="group/subgroup", repo="repo").
// Returns an error if the name doesn't contain at least one slash.
func parseRepoFullName(fullName string) (owner, repo string, err error) {
	for i := len(fullName) - 1; i >= 0; i-- {
		if fullName[i] == '/' {
			return fullName[:i], fullName[i+1:], nil
		}
	}
	return "", fullName, fmt.Errorf("invalid repository full name %q: expected owner/repo format", fullName)
}
