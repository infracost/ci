package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/infracost/ci/internal/api/events"
	"github.com/infracost/ci/internal/config"
	"github.com/infracost/ci/internal/vcsurl"
)

// resolveVCSProvider falls back to github when GITHUB_ACTIONS is set, so that
// pinned action versions running old YAML still report a provider.
func resolveVCSProvider(cfg *config.Config) (string, error) {
	if cfg.VCSProvider != "" {
		provider := strings.ToLower(strings.TrimSpace(cfg.VCSProvider))
		if !vcsurl.Valid(provider) {
			return "", fmt.Errorf("unrecognised VCS provider %q: set INFRACOST_VCS_PROVIDER to %s", cfg.VCSProvider, vcsurl.ProviderList())
		}
		return provider, nil
	}
	if os.Getenv("GITHUB_ACTIONS") != "" {
		return vcsurl.ProviderGitHub, nil
	}
	return "", fmt.Errorf("cannot determine the VCS provider: set INFRACOST_VCS_PROVIDER to %s", vcsurl.ProviderList())
}

func registerVCSProvider(cfg *config.Config) (string, error) {
	provider, err := resolveVCSProvider(cfg)
	events.RegisterMetadata("vcsProvider", provider)
	return provider, err
}
