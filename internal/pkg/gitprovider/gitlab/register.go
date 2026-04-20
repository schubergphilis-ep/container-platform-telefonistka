package gitlab

import (
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
)

// Register the GitLab provider with the factory
func init() { //nolint:gochecknoinits
	gitprovider.RegisterProvider(gitprovider.ProviderTypeGitLab, func(config *gitprovider.ProviderConfig) (gitprovider.GitProvider, error) {
		return NewGitLabProvider(config)
	})
}
