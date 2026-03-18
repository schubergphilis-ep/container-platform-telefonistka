package githubapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/go-github/v62/github"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/configuration"
	prom "github.com/schubergphilis/container-platform-telefonistka/internal/pkg/prometheus"
	log "github.com/sirupsen/logrus"
	"golang.org/x/exp/maps"
)

func generateListOfChangedFiles(eventPayload *github.PushEvent) []string {
	fileList := map[string]bool{} // using map for uniqueness

	for _, commit := range eventPayload.Commits {
		for _, file := range commit.Added {
			fileList[file] = true
		}
		for _, file := range commit.Modified {
			fileList[file] = true
		}
		for _, file := range commit.Removed {
			fileList[file] = true
		}
	}

	return maps.Keys(fileList)
}

func generateListOfEndpoints(listOfChangedFiles []string, config *configuration.Config) []string {
	// Pre-compile regexes once to avoid recompiling per file (prevents ReDoS amplification).
	type compiledEndpointRegex struct {
		regex        *regexp.Regexp
		replacements []string
	}
	var compiled []compiledEndpointRegex
	for _, r := range config.WebhookEndpointRegexs {
		re, err := regexp.Compile(r.Expression)
		if err != nil {
			log.Errorf("Invalid webhook endpoint regex %q: %v, skipping", r.Expression, err)
			continue
		}
		compiled = append(compiled, compiledEndpointRegex{regex: re, replacements: r.Replacements})
	}

	endpoints := map[string]bool{} // using map for uniqueness
	for _, file := range listOfChangedFiles {
		for _, cr := range compiled {
			if cr.regex.MatchString(file) {
				for _, replacement := range cr.replacements {
					endpoints[cr.regex.ReplaceAllString(file, replacement)] = true
				}
				break
			}
		}
	}

	return maps.Keys(endpoints)
}

func proxyRequest(ctx context.Context, skipTLSVerify bool, originalHttpRequest *http.Request, body []byte, endpoint string, responses chan<- string) {
	tr := &http.Transport{}
	if skipTLSVerify {
		tr = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 - letting the user decide if they want to skip TLS verification, for some in-cluster scenarios its a reasonable compromise
		}
	}
	client := &http.Client{Transport: tr}
	req, err := http.NewRequestWithContext(ctx, originalHttpRequest.Method, endpoint, bytes.NewBuffer(body))
	if err != nil {
		log.Errorf("Error creating request to %s: %v", endpoint, err)
		responses <- fmt.Sprintf("Failed to create request to %s", endpoint)
		return
	}
	req.Header = originalHttpRequest.Header.Clone()
	// Because payload and headers are passed as-is, I'm hoping webhook signature validation will "just work"

	resp, err := client.Do(req)
	if err != nil {
		log.Errorf("Error proxying request to %s: %v", endpoint, err)
		responses <- fmt.Sprintf("Failed to proxy request to %s", endpoint)
		return
	} else {
		log.Debugf("Webhook successfully forwarded to %s", endpoint)
	}
	defer resp.Body.Close()

	_ = prom.InstrumentProxyUpstreamRequest(resp)

	respBody, err := io.ReadAll(resp.Body)

	if !strings.HasPrefix(resp.Status, "2") {
		log.Errorf("Got non 2XX HTTP status from %s: status=%s responseLength=%d", endpoint, resp.Status, len(respBody))
	}

	if err != nil {
		log.Errorf("Error reading response body from %s: %v", endpoint, err)
		responses <- fmt.Sprintf("Failed to read response from %s", endpoint)
		return
	}

	responses <- string(respBody)
}

func handlePushEvent(ctx context.Context, eventPayload *github.PushEvent, httpRequest *http.Request, payload []byte, ghPrClientDetails GhPrClientDetails) {
	listOfChangedFiles := generateListOfChangedFiles(eventPayload)
	log.Debugf("Changed files in push event: %v", listOfChangedFiles)

	defaultBranch := eventPayload.Repo.DefaultBranch

	if *eventPayload.Ref == "refs/heads/"+*defaultBranch {
		// TODO this need to be cached with TTL + invalidate if configfile in listOfChangedFiles?
		// This is possible because these webhooks are defined as "best effort" for the designed use case:
		// Speeding up ArgoCD reconcile loops
		config, _ := GetInRepoConfig(ghPrClientDetails, *defaultBranch)
		endpoints := generateListOfEndpoints(listOfChangedFiles, config)

		// Buffered channel so goroutines never block even if this function returns early.
		responses := make(chan string, len(endpoints))

		// Start a goroutine for each endpoint
		for _, endpoint := range endpoints {
			go proxyRequest(ctx, config.WhProxtSkipTLSVerifyUpstream, httpRequest, payload, endpoint, responses)
		}

		// Collect responses with a timeout to avoid goroutine leaks on slow endpoints.
		timeout := time.After(30 * time.Second)
		for i := 0; i < len(endpoints); i++ {
			select {
			case <-responses:
				// response collected
			case <-timeout:
				log.Warnf("Timed out waiting for %d/%d webhook proxy responses", len(endpoints)-i, len(endpoints))
				return
			}
		}
	}
}
