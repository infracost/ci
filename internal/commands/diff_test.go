package commands

import (
	"context"
	"testing"

	"github.com/infracost/ci/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// diff is always a pull request run, so it must refuse to scan rather than
// upload a run the dashboard would file as a branch build.
func TestResolveDiffContext_RequiresBuildablePullRequestURL(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		repoURL  string
		prURL    string
		prNumber int
		wantErr  string
	}{
		{
			name:     "missing repo url",
			provider: "github",
			prNumber: 42,
			wantErr:  "cannot determine the repository URL: set INFRACOST_VCS_REPOSITORY_URL",
		},
		{
			name:     "no pull request at all",
			provider: "github",
			repoURL:  "https://github.com/infracost/actions",
			wantErr:  "cannot determine the pull request: set INFRACOST_VCS_PULL_REQUEST_ID or INFRACOST_VCS_PULL_REQUEST_URL",
		},
		{
			name:     "ssh clone url",
			provider: "gitlab",
			repoURL:  "git@gitlab.com:infracost/actions.git",
			prNumber: 42,
			wantErr:  `repo URL "git@gitlab.com:infracost/actions.git" must be an http(s) web URL of the repository, not a clone URL`,
		},
		{
			name:     "azure project url without _git",
			provider: "azure_repos",
			repoURL:  "https://dev.azure.com/infracost/actions",
			prNumber: 42,
			wantErr:  `repo URL "https://dev.azure.com/infracost/actions" must be an Azure Repos repository URL containing /_git/`,
		},
		{
			name:     "url and number disagree",
			provider: "github",
			repoURL:  "https://github.com/infracost/actions",
			prURL:    "https://github.com/infracost/actions/pull/7",
			prNumber: 42,
			wantErr:  `pull request URL "https://github.com/infracost/actions/pull/7" does not match INFRACOST_VCS_REPOSITORY_URL and INFRACOST_VCS_PULL_REQUEST_ID, which give "https://github.com/infracost/actions/pull/42"`,
		},
		{
			name:     "url names another repository",
			provider: "github",
			repoURL:  "https://github.com/infracost/actions",
			prURL:    "https://github.com/attacker/actions/pull/42",
			wantErr:  `pull request URL "https://github.com/attacker/actions/pull/42" does not match INFRACOST_VCS_REPOSITORY_URL and INFRACOST_VCS_PULL_REQUEST_ID, which give "https://github.com/infracost/actions/pull/42"`,
		},
		{
			name:     "url is not a pull request url",
			provider: "github",
			repoURL:  "https://github.com/infracost/actions",
			prURL:    "https://github.com/infracost/actions/issues/42",
			wantErr:  `pull request URL "https://github.com/infracost/actions/issues/42" is not a github pull request URL: expected /pull/<number>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{VCSProvider: tt.provider}
			// prURL is a flag bound to INFRACOST_VCS_PULL_REQUEST_URL.
			args := &diffArgs{repoURL: tt.repoURL, prURL: tt.prURL, prNumber: tt.prNumber}

			_, err := resolveDiffContext(context.Background(), cfg, args)

			require.Error(t, err)
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

// The client flags hydrate from the environment and lose to an explicit flag,
// the same ordering as the INFRACOST_VCS_* flags.
func TestDiffFlags_EnvThenFlag(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		argv  []string
		value func(*diffArgs) string
		want  string
	}{
		{name: "gitlab token from env", env: map[string]string{"GITLAB_TOKEN": "env-token"},
			value: func(a *diffArgs) string { return a.gitlabToken }, want: "env-token"},
		{name: "gitlab token flag wins", env: map[string]string{"GITLAB_TOKEN": "env-token"},
			argv: []string{"--gitlab-token", "flag-token"}, value: func(a *diffArgs) string { return a.gitlabToken }, want: "flag-token"},
		{name: "azure token from the CLI variable", env: map[string]string{"AZURE_DEVOPS_EXT_PAT": "ext-pat"},
			value: func(a *diffArgs) string { return a.azureToken }, want: "ext-pat"},
		{name: "azure token falls back to the pipeline variable", env: map[string]string{"SYSTEM_ACCESSTOKEN": "system-token"},
			value: func(a *diffArgs) string { return a.azureToken }, want: "system-token"},
		{name: "azure PAT beats the pipeline variable", env: map[string]string{"AZURE_DEVOPS_EXT_PAT": "ext-pat", "SYSTEM_ACCESSTOKEN": "system-token"},
			value: func(a *diffArgs) string { return a.azureToken }, want: "ext-pat"},
		{name: "azure token flag wins", env: map[string]string{"SYSTEM_ACCESSTOKEN": "system-token"},
			argv: []string{"--azure-token", "flag-token"}, value: func(a *diffArgs) string { return a.azureToken }, want: "flag-token"},
		// GITHUB_SERVER_URL is not a default: GitHub Actions sets it to
		// https://github.com on github.com, which is not an APIURL.
		{name: "github api url ignores the server variable", env: map[string]string{"GITHUB_SERVER_URL": "https://github.com"},
			value: func(a *diffArgs) string { return a.githubAPIURL }, want: ""},
		{name: "github api url from the flag", argv: []string{"--github-api-url", "https://ghes.corp"},
			value: func(a *diffArgs) string { return a.githubAPIURL }, want: "https://ghes.corp"},
		{name: "gitlab project flag", argv: []string{"--gitlab-project", "group/sub/repo"},
			value: func(a *diffArgs) string { return a.gitlabProject }, want: "group/sub/repo"},
		{name: "gitlab server url flag", argv: []string{"--gitlab-server-url", "https://host/gitlab"},
			value: func(a *diffArgs) string { return a.gitlabServer }, want: "https://host/gitlab"},
		{name: "tag flag", argv: []string{"--tag", "infracost-prod"},
			value: func(a *diffArgs) string { return a.tag }, want: "infracost-prod"},
		{name: "tag defaults to empty, so the library default stands",
			value: func(a *diffArgs) string { return a.tag }, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name, value := range tt.env {
				t.Setenv(name, value)
			}
			_, args, err := execDiffArgs(t, tt.argv...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, tt.value(args))
		})
	}
}

// The APIURL github.New is given must be empty for github.com, however the
// value arrived: newClient keys the non-enterprise path on "".
func TestResolveGitHubAPIURL(t *testing.T) {
	tests := []struct {
		name     string
		override string
		repoURL  string
		want     string
		wantErr  string
	}{
		{name: "github.com repo", repoURL: "https://github.com/infracost/actions"},
		// What GITHUB_SERVER_URL holds on github.com.
		{name: "github.com override", override: "https://github.com", repoURL: "https://github.com/infracost/actions"},
		{name: "ghes repo", repoURL: "https://ghes.corp/infracost/actions", want: "https://ghes.corp"},
		{name: "ghes override", override: "https://ghes.corp", repoURL: "https://github.com/infracost/actions", want: "https://ghes.corp"},
		{name: "override wins", override: "https://ghes.corp", repoURL: "https://other.corp/infracost/actions", want: "https://ghes.corp"},
		{name: "override on another vendor's host", override: "https://gitlab.com", repoURL: "https://github.com/infracost/actions",
			wantErr: `is a gitlab host, but INFRACOST_VCS_PROVIDER is "github"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveGitHubAPIURL(tt.override, tt.repoURL)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// The TLS variables are env: tags on config.Config, so PreProcess hydrates
// them and --help sees them.
func TestDiffFlags_TLSConfig(t *testing.T) {
	t.Setenv("INFRACOST_CI_VCS_TLS_INSECURE_SKIP_VERIFY", "true")

	cfg, _, err := execDiffArgs(t)
	require.NoError(t, err)
	assert.True(t, cfg.TLSInsecureSkipVerify)

	tlsConfig, err := cfg.TLSConfig()
	require.NoError(t, err)
	require.NotNil(t, tlsConfig)
	assert.True(t, tlsConfig.InsecureSkipVerify)
}

func TestConfig_TLSConfig_NilWhenUnset(t *testing.T) {
	tlsConfig, err := new(config.Config).TLSConfig()
	require.NoError(t, err)
	assert.Nil(t, tlsConfig)
}
