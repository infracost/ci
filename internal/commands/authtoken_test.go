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

	tokens := []struct {
		name    string
		token   string
		wantErr string
	}{
		{name: "empty", wantErr: "authentication token is required: set INFRACOST_CLI_AUTHENTICATION_TOKEN"},
		{name: "v0.1 api key", token: v01Key, wantErr: "INFRACOST_CLI_AUTHENTICATION_TOKEN is a v0.1 API key"},
		{name: "unexpanded variable", token: "$(X)", wantErr: "INFRACOST_CLI_AUTHENTICATION_TOKEN is an unexpanded pipeline variable"},
		{name: "leading whitespace", token: " ics_", wantErr: "INFRACOST_CLI_AUTHENTICATION_TOKEN has leading or trailing whitespace"},
	}

	for name, fn := range run {
		for _, tt := range tokens {
			t.Run(name+"/"+tt.name, func(t *testing.T) {
				cfg := &config.Config{Auth: auth.Config{ExternalConfig: auth.ExternalConfig{
					AuthenticationToken: auth.AuthenticationToken(tt.token),
				}}}

				err := fn(cfg)

				require.ErrorContains(t, err, tt.wantErr)
			})
		}
	}
}
