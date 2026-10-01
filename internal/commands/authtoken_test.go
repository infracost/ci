package commands

import (
	"testing"

	"github.com/infracost/ci/internal/api/dashboard"
	"github.com/infracost/ci/internal/config"
	"github.com/infracost/cli/pkg/auth"
	"github.com/stretchr/testify/require"
)

// The token check is the first thing each command does, and the only tests that
// otherwise reach these functions skip without a token in the environment.
func TestCommandsRequireAuthToken(t *testing.T) {
	const v01Key = "ico-abcdefghijklmnopqrstuvwxyz012345"

	run := map[string]func(cfg *config.Config) error{
		"diff": func(cfg *config.Config) error {
			return diff(cfg, &diffArgs{}, diffContext{}, nil, nil)
		},
		"scan": func(cfg *config.Config) error {
			return scan(cfg, &scanArgs{repoURL: "https://github.com/infracost/actions"})
		},
		"status": func(cfg *config.Config) error {
			return updatePullRequestStatus(cfg, "https://github.com/infracost/actions/pull/1",
				"https://github.com/infracost/actions", dashboard.PullRequestStatusOpen)
		},
	}

	tokens := map[string]string{
		"":      "authentication token is required: set INFRACOST_CLI_AUTHENTICATION_TOKEN",
		v01Key:  "INFRACOST_CLI_AUTHENTICATION_TOKEN is a v0.1 API key",
		"$(X)":  "INFRACOST_CLI_AUTHENTICATION_TOKEN is an unexpanded pipeline variable",
		" ics_": "INFRACOST_CLI_AUTHENTICATION_TOKEN has leading or trailing whitespace",
	}

	for name, fn := range run {
		for token, wantErr := range tokens {
			t.Run(name+"/"+token, func(t *testing.T) {
				cfg := &config.Config{Auth: auth.Config{ExternalConfig: auth.ExternalConfig{
					AuthenticationToken: auth.AuthenticationToken(token),
				}}}

				err := fn(cfg)

				require.ErrorContains(t, err, wantErr)
			})
		}
	}
}
