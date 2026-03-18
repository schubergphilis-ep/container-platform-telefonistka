package telefonistka

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/alexliesenfeld/health"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/githubapi"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitlabapi"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "server",
	Short: "Runs the web server that listens to GitHub and GitLab webhooks",
	Args:  cobra.ExactArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		serve()
	},
}

// This is still(https://github.com/spf13/cobra/issues/1862) the documented way to use cobra
func init() { //nolint:gochecknoinits
	rootCmd.AddCommand(serveCmd)
}

func handleWebhook(
	githubWebhookSecret []byte,
	gitlabWebhookSecret []byte,
	mainGhClientCache *lru.Cache[string, githubapi.GhClientPair],
	prApproverGhClientCache *lru.Cache[string, githubapi.GhClientPair],
	mainProviderCache *lru.Cache[string, gitprovider.GitProvider],
	approverProviderCache *lru.Cache[string, gitprovider.GitProvider],
) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// Detect provider from webhook headers
		providerType := gitprovider.DetectProviderFromWebhook(r.Header)

		log.Infof("Received webhook from provider: %s", providerType)

		var err error

		switch providerType {
		case gitprovider.ProviderTypeGitLab:
			// Handle GitLab webhook
			err = gitlabapi.ReceiveGitLabWebhook(r, mainProviderCache, approverProviderCache, gitlabWebhookSecret)

		case gitprovider.ProviderTypeGitHub:
			// Handle GitHub webhook
			err = githubapi.ReceiveWebhook(r, mainGhClientCache, prApproverGhClientCache, githubWebhookSecret)

		default:
			log.Warnf("Received webhook with unrecognized provider headers (no X-Github-Event or X-Gitlab-Event), rejecting")
			http.Error(w, "Unrecognized webhook provider", http.StatusBadRequest)
			return
		}

		if err != nil {
			log.Errorf("error handling webhook: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func serve() {
	allowUnsignedWebhooks := os.Getenv("ALLOW_UNSIGNED_WEBHOOKS") == "true"

	githubWebhookSecret := []byte(os.Getenv("GITHUB_WEBHOOK_SECRET"))
	gitlabWebhookSecret := []byte(os.Getenv("GITLAB_WEBHOOK_SECRET"))

	if len(githubWebhookSecret) == 0 && len(gitlabWebhookSecret) == 0 {
		if !allowUnsignedWebhooks {
			log.Fatal("No webhook secrets configured. Set GITHUB_WEBHOOK_SECRET and/or GITLAB_WEBHOOK_SECRET. " +
				"To explicitly run without signature validation (NOT recommended), set ALLOW_UNSIGNED_WEBHOOKS=true")
		}
		log.Warn("ALLOW_UNSIGNED_WEBHOOKS is set: webhook signature validation is disabled. This is NOT recommended for production.")
	} else {
		if len(githubWebhookSecret) == 0 {
			log.Warn("GITHUB_WEBHOOK_SECRET not set, webhook signature validation disabled for GitHub")
		}
		if len(gitlabWebhookSecret) == 0 {
			log.Warn("GITLAB_WEBHOOK_SECRET not set, webhook signature validation disabled for GitLab")
		}
	}

	livenessChecker := health.NewChecker()
	readinessChecker := health.NewChecker(
		health.WithCheck(health.Check{
			Name: "webhook-secrets",
			Check: func(ctx context.Context) error {
				// Verify that webhook processing can succeed: either secrets
				// are configured or unsigned webhooks are explicitly allowed.
				// We intentionally don't call provider APIs here: readiness probes
				// fire every few seconds and adding API calls would consume rate limits.
				// Provider connectivity is validated on the first webhook.
				if len(githubWebhookSecret) == 0 && len(gitlabWebhookSecret) == 0 && !allowUnsignedWebhooks {
					return fmt.Errorf("no webhook secrets configured and unsigned webhooks not allowed")
				}
				return nil
			},
		}),
	)

	// GitHub client caches (for backward compatibility)
	mainGhClientCache, err := lru.New[string, githubapi.GhClientPair](128)
	if err != nil {
		log.Fatalf("Failed to create GitHub client cache: %v", err)
	}
	prApproverGhClientCache, err := lru.New[string, githubapi.GhClientPair](128)
	if err != nil {
		log.Fatalf("Failed to create GitHub approver client cache: %v", err)
	}

	// GitProvider caches (for GitLab and future providers)
	mainProviderCache, err := lru.New[string, gitprovider.GitProvider](128)
	if err != nil {
		log.Fatalf("Failed to create provider cache: %v", err)
	}
	approverProviderCache, err := lru.New[string, gitprovider.GitProvider](128)
	if err != nil {
		log.Fatalf("Failed to create approver provider cache: %v", err)
	}

	go githubapi.MainGhMetricsLoop(mainGhClientCache)

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", handleWebhook(
		githubWebhookSecret,
		gitlabWebhookSecret,
		mainGhClientCache,
		prApproverGhClientCache,
		mainProviderCache,
		approverProviderCache,
	))
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/live", health.NewHandler(livenessChecker))
	mux.Handle("/ready", health.NewHandler(readinessChecker))

	srv := &http.Server{
		Handler:      mux,
		Addr:         ":8080",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Infoln("Server started on :8080")
	log.Infoln("Webhook endpoint: http://localhost:8080/webhook")
	log.Infoln("Supports both GitHub and GitLab webhooks")
	log.Fatal(srv.ListenAndServe())
}
