package github

import (
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
)

// Register the GitHub provider with the factory
func init() { //nolint:gochecknoinits
	gitprovider.RegisterProvider(gitprovider.ProviderTypeGitHub, func(config *gitprovider.ProviderConfig) (gitprovider.GitProvider, error) {
		return NewGitHubProvider(config)
	})
}
