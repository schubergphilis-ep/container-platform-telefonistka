package githubapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shurcooL/githubv4"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestGraphQLClient creates a githubv4.Client pointed at the given test server URL.
func newTestGraphQLClient(url string) *githubv4.Client {
	return githubv4.NewEnterpriseClient(url+"/graphql", http.DefaultClient)
}

func TestGetBotGhIdentity_HappyPath(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"viewer": map[string]interface{}{
					"login": "my-bot[bot]",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := newTestGraphQLClient(srv.URL)
	login, err := GetBotGhIdentity(client, context.Background())

	require.NoError(t, err)
	assert.Equal(t, "my-bot[bot]", login)
}

func TestGetBotGhIdentity_Error(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"errors": []map[string]interface{}{
				{"message": "Bad credentials"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := newTestGraphQLClient(srv.URL)
	login, err := GetBotGhIdentity(client, context.Background())

	require.Error(t, err)
	assert.Empty(t, login)
}

func TestMimizeStalePrComments_NoComments(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"repository": map[string]interface{}{
					"pullRequest": map[string]interface{}{
						"title": "Test PR",
						"comments": map[string]interface{}{
							"edges": []interface{}{},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := newTestGraphQLClient(srv.URL)
	details := GhPrClientDetails{
		Ctx:      context.Background(),
		Owner:    "test-owner",
		Repo:     "test-repo",
		PrNumber: 1,
		PrLogger: log.WithField("test", true),
	}

	err := MimizeStalePrComments(details, client, "my-bot[bot]")
	assert.NoError(t, err)
}

func TestMimizeStalePrComments_MinimizesOldComments(t *testing.T) {
	t.Parallel()

	// Track whether the mutation was called.
	mutationCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodyStr := string(body)

		// The shurcooL client sends JSON with a "query" field for both queries and mutations.
		// Mutations use the "mutation" keyword; queries use "query" or are bare.
		if strings.Contains(bodyStr, "minimizeComment") {
			mutationCalled = true
			resp := map[string]interface{}{
				"data": map[string]interface{}{
					"minimizeComment": map[string]interface{}{
						"clientMutationId": "test-id",
						"minimizedComment": map[string]interface{}{
							"isMinimized": true,
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// Query response with a non-minimized comment authored by the bot, containing the tag.
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"repository": map[string]interface{}{
					"pullRequest": map[string]interface{}{
						"title": "Test PR",
						"comments": map[string]interface{}{
							"edges": []interface{}{
								map[string]interface{}{
									"node": map[string]interface{}{
										"id":          "MDEyOklzc3VlQ29tbWVudDE=",
										"isMinimized": false,
										"body":        "Some old promotion info\n<!-- telefonistka_tag -->",
										"author": map[string]interface{}{
											"login": "my-bot",
										},
									},
								},
							},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := newTestGraphQLClient(srv.URL)
	details := GhPrClientDetails{
		Ctx:      context.Background(),
		Owner:    "test-owner",
		Repo:     "test-repo",
		PrNumber: 42,
		PrLogger: log.WithField("test", true),
	}

	err := MimizeStalePrComments(details, client, "my-bot[bot]")
	assert.NoError(t, err)
	assert.True(t, mutationCalled, "expected minimizeComment mutation to be called for a matching stale comment")
}
