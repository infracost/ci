package config

import (
	"testing"

	"github.com/infracost/ci/v2/internal/api/dashboard/graphql"
	"github.com/infracost/cli/pkg/auth"
	"github.com/infracost/cli/pkg/config/process"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestConfig_Process(t *testing.T) {
	var cfg Config

	flags := pflag.NewFlagSet("", pflag.ContinueOnError)

	// first, make sure that preprocess doesn't error or panic when no values provided.
	if diags := process.PreProcess(&cfg, flags); diags.Len() != 0 {
		t.Fatal(diags)
	}
	require.NoError(t, flags.Parse(nil)) // we have no required flags yet, so will provide nothing
	process.Process(&cfg)                // make sure doesn't panic
}

// The action's status step passes the provider by environment variable only,
// so the env tag is the whole contract.
func TestConfig_VCSProviderFromEnv(t *testing.T) {
	t.Setenv("INFRACOST_VCS_PROVIDER", "gitlab")

	var cfg Config
	flags := pflag.NewFlagSet("", pflag.ContinueOnError)
	if diags := process.PreProcess(&cfg, flags); diags.Len() != 0 {
		t.Fatal(diags)
	}
	require.NoError(t, flags.Parse(nil))
	process.Process(&cfg)

	require.Equal(t, "gitlab", cfg.VCSProvider)
}

func TestConfig_VCSProviderFlagOverridesEnv(t *testing.T) {
	t.Setenv("INFRACOST_VCS_PROVIDER", "gitlab")

	var cfg Config
	flags := pflag.NewFlagSet("", pflag.ContinueOnError)
	if diags := process.PreProcess(&cfg, flags); diags.Len() != 0 {
		t.Fatal(diags)
	}
	require.NoError(t, flags.Parse([]string{"--vcs-provider", "bitbucket"}))
	process.Process(&cfg)

	require.Equal(t, "bitbucket", cfg.VCSProvider)
}

// The prefix is what lets the message say "this is a v0.1 API key" as a fact.
// Everything else has to reach the dashboard before anything can be said.
func TestConfig_RequireAuthToken(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		wantErrMsg string
	}{
		{
			name:       "empty",
			wantErrMsg: "authentication token is required: set INFRACOST_CLI_AUTHENTICATION_TOKEN",
		},
		{
			name:       "v0.1 api key",
			token:      "ico-abcdefghijklmnopqrstuvwxyz012345",
			wantErrMsg: "INFRACOST_CLI_AUTHENTICATION_TOKEN is a v0.1 API key (ico-…), which CI v2 does not accept: " + graphql.CLITokenHint,
		},
		{
			// Azure substitutes the literal when the pipeline variable is absent.
			name:       "unexpanded azure variable",
			token:      "$(INFRACOST_CLI_TOKEN)",
			wantErrMsg: `INFRACOST_CLI_AUTHENTICATION_TOKEN is an unexpanded pipeline variable ("$(INFRACOST_CLI_TOKEN)"): define it in the pipeline, or set the token directly`,
		},
		{
			name:       "trailing newline",
			token:      "ics_v1_abcdefghijklmnop_abcdefghijklmnopqrstuvwxyz012345abcdef\n",
			wantErrMsg: "INFRACOST_CLI_AUTHENTICATION_TOKEN has leading or trailing whitespace, which the Authorization header rejects",
		},
		{
			name:  "cli v2 token",
			token: "ics_v1_abcdefghijklmnop_abcdefghijklmnopqrstuvwxyz012345abcdef",
		},
		{
			// The dashboard still accepts a JWT on this header, so the check must
			// not reject what it cannot recognise.
			name:  "jwt",
			token: "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjMifQ.c2ln",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Auth: auth.Config{ExternalConfig: auth.ExternalConfig{AuthenticationToken: auth.AuthenticationToken(tt.token)}}}

			err := cfg.RequireAuthToken()

			if tt.wantErrMsg == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErrMsg)
		})
	}
}
