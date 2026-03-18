package telefonistka

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/githubapi"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestCaches(t *testing.T) (
	*lru.Cache[string, githubapi.GhClientPair],
	*lru.Cache[string, githubapi.GhClientPair],
	*lru.Cache[string, gitprovider.GitProvider],
	*lru.Cache[string, gitprovider.GitProvider],
) {
	t.Helper()
	ghMain, err := lru.New[string, githubapi.GhClientPair](4)
	require.NoError(t, err)
	ghApprover, err := lru.New[string, githubapi.GhClientPair](4)
	require.NoError(t, err)
	providerMain, err := lru.New[string, gitprovider.GitProvider](4)
	require.NoError(t, err)
	providerApprover, err := lru.New[string, gitprovider.GitProvider](4)
	require.NoError(t, err)
	return ghMain, ghApprover, providerMain, providerApprover
}

func TestHandleWebhook_UnrecognizedProvider(t *testing.T) {
	t.Parallel()
	ghMain, ghApprover, providerMain, providerApprover := newTestCaches(t)
	handler := handleWebhook(
		[]byte("gh-secret"),
		[]byte("gl-secret"),
		ghMain, ghApprover,
		providerMain, providerApprover,
	)

	// Request with no provider headers → 400
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader("{}"))
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Unrecognized webhook provider")
}

func TestHandleWebhook_GitHubProviderDetected(t *testing.T) {
	t.Parallel()
	ghMain, ghApprover, providerMain, providerApprover := newTestCaches(t)
	handler := handleWebhook(
		[]byte("gh-secret"),
		[]byte("gl-secret"),
		ghMain, ghApprover,
		providerMain, providerApprover,
	)

	// GitHub header present but invalid payload → should return 500 (validation fails)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader("{}"))
	req.Header.Set("X-Github-Event", "push")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	// Expect 500 because the HMAC signature is missing/invalid
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleWebhook_GitLabProviderDetected_MissingToken(t *testing.T) {
	t.Parallel()
	ghMain, ghApprover, providerMain, providerApprover := newTestCaches(t)
	handler := handleWebhook(
		[]byte("gh-secret"),
		[]byte("gl-secret"),
		ghMain, ghApprover,
		providerMain, providerApprover,
	)

	// GitLab header present but missing GITLAB_TOKEN env → should return 500
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader("{}"))
	req.Header.Set("X-Gitlab-Event", "Merge Request Hook")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	// Expect 500 because GITLAB_TOKEN env var is not set in test
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
