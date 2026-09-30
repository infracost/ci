package commands

import (
	"testing"

	"github.com/infracost/ci/internal/api/events"
	"github.com/infracost/ci/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveVCSProvider(t *testing.T) {
	tests := []struct {
		name          string
		configured    string
		githubActions string
		expected      string
		expectErr     bool
	}{
		{name: "configured value wins", configured: "gitlab", githubActions: "true", expected: "gitlab"},
		{name: "configured without github actions", configured: "bitbucket", expected: "bitbucket"},
		{name: "case and whitespace folded", configured: " Github ", expected: "github"},
		{name: "typo rejected", configured: "githbu", githubActions: "true", expectErr: true},
		{name: "vcs module package name rejected", configured: "azure", expectErr: true},
		{name: "ci platform name rejected", configured: "gitlab_ci", expectErr: true},
		{name: "unset falls back on github actions", githubActions: "true", expected: "github"},
		{name: "unset elsewhere errors", expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GITHUB_ACTIONS", tt.githubActions)

			provider, err := resolveVCSProvider(&config.Config{VCSProvider: tt.configured})
			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "INFRACOST_VCS_PROVIDER")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, provider)
		})
	}
}

func TestRegisterVCSProvider_Inferred(t *testing.T) {
	t.Setenv("INFRACOST_CI_PLATFORM", "gitlab_ci")
	t.Setenv("GITHUB_ACTIONS", "")
	snapshot := events.Snapshot()
	t.Cleanup(func() { events.Restore(snapshot) })
	cfg := new(config.Config)
	config.InferVCS(cfg)

	provider, err := registerVCSProvider(cfg)
	require.NoError(t, err)
	assert.Equal(t, "gitlab", provider)
	registered, ok := events.GetMetadata[string]("vcsProvider")
	assert.True(t, ok)
	assert.Equal(t, "gitlab", registered)
}

func TestRegisterVCSProvider_Error(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	snapshot := events.Snapshot()
	t.Cleanup(func() { events.Restore(snapshot) })

	provider, err := registerVCSProvider(new(config.Config))
	require.Error(t, err)
	assert.Empty(t, provider)
	registered, ok := events.GetMetadata[string]("vcsProvider")
	assert.True(t, ok)
	assert.Empty(t, registered)
}
