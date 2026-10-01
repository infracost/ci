package commands

import (
	"testing"
	"time"

	"github.com/infracost/ci/internal/vcsurl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffVCSContext_EnvironmentAndFlags(t *testing.T) {
	t.Setenv("INFRACOST_VCS_PROVIDER", "gitlab")
	t.Setenv("INFRACOST_VCS_REPOSITORY_URL", "https://gitlab.com/infracost/actions")
	t.Setenv("INFRACOST_VCS_PULL_REQUEST_URL", "https://gitlab.com/infracost/actions/-/merge_requests/42")
	t.Setenv("INFRACOST_VCS_PULL_REQUEST_TITLE", "Environment title")
	t.Setenv("INFRACOST_VCS_PULL_REQUEST_LABELS", "bug, ci")
	t.Setenv("INFRACOST_VCS_BRANCH", "feature/vcs")
	t.Setenv("INFRACOST_VCS_BASE_BRANCH", "main")
	t.Setenv("INFRACOST_VCS_COMMIT_TIMESTAMP", "1700000000")
	t.Setenv("INFRACOST_VCS_PIPELINE_RUN_ID", "environment-run")

	vcsCtx, err := execDiff(t)
	require.NoError(t, err)
	assert.Equal(t, "gitlab", vcsCtx.provider)
	assert.Equal(t, "https://gitlab.com/infracost/actions", vcsCtx.repoURL)
	assert.Equal(t, "https://gitlab.com/infracost/actions/-/merge_requests/42", vcsCtx.prURL)
	assert.Equal(t, 42, vcsCtx.prNumber)
	assert.Equal(t, "Environment title", vcsCtx.prTitle)
	assert.Equal(t, []string{"bug", "ci"}, vcsCtx.prLabels)
	assert.Equal(t, "feature/vcs", vcsCtx.branch)
	assert.Equal(t, "main", vcsCtx.baseBranch)
	assert.Equal(t, "2023-11-14T22:13:20Z", vcsCtx.commitTimestamp)
	assert.Equal(t, "environment-run", vcsCtx.pipelineRunID)

	vcsCtx, err = execDiff(t, "--pr-title", "Flag title", "--pipeline-run-id", "flag-run")
	require.NoError(t, err)
	assert.Equal(t, "Flag title", vcsCtx.prTitle)
	assert.Equal(t, "flag-run", vcsCtx.pipelineRunID)
}

func TestDiffVCSContext_FlagRepositoryOverridesEnvironment(t *testing.T) {
	t.Setenv("INFRACOST_VCS_PROVIDER", "github")
	t.Setenv("INFRACOST_VCS_REPOSITORY_URL", "https://github.com/infracost/old-actions")
	t.Setenv("INFRACOST_VCS_PULL_REQUEST_ID", "42")

	vcsCtx, err := execDiff(t, "--repo-url", "https://github.com/infracost/actions")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/infracost/actions", vcsCtx.repoURL)
	assert.Equal(t, "https://github.com/infracost/actions/pull/42", vcsCtx.prURL)
}

func TestStatusVCSContext_EnvironmentAndFlag(t *testing.T) {
	t.Setenv("INFRACOST_VCS_PROVIDER", "github")
	t.Setenv("INFRACOST_VCS_REPOSITORY_URL", "https://github.com/infracost/actions")
	t.Setenv("INFRACOST_VCS_PULL_REQUEST_ID", "42")
	t.Setenv("INFRACOST_VCS_PULL_REQUEST_STATUS", "OPEN")

	prURL, err := execStatus(t)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/infracost/actions/pull/42", prURL)

	prURL, err = execStatus(t, "--status", "CLOSED")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/infracost/actions/pull/42", prURL)
}

func TestStatusVCSContext_FlagPullRequestURLOverridesEnvironment(t *testing.T) {
	t.Setenv("INFRACOST_VCS_PROVIDER", "github")
	t.Setenv("INFRACOST_VCS_REPOSITORY_URL", "https://github.com/infracost/actions")
	t.Setenv("INFRACOST_VCS_PULL_REQUEST_URL", "https://github.com/infracost/actions/pull/7")
	t.Setenv("INFRACOST_VCS_PULL_REQUEST_STATUS", "OPEN")

	prURL, err := execStatus(t, "--pr-url", "https://github.com/infracost/actions/pull/8")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/infracost/actions/pull/8", prURL)
}

func TestScanVCSContext_EnvironmentAndFlag(t *testing.T) {
	t.Setenv("INFRACOST_VCS_REPOSITORY_URL", "https://github.com/infracost/actions")
	t.Setenv("INFRACOST_VCS_PIPELINE_RUN_ID", "environment-run")

	_, args, err := execScanArgs(t)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/infracost/actions", args.repoURL)
	assert.Equal(t, "environment-run", args.pipelineRunID)

	_, args, err = execScanArgs(t, "--repo-url", "https://gitlab.com/infracost/actions", "--pipeline-run-id", "flag-run")
	require.NoError(t, err)
	assert.Equal(t, "https://gitlab.com/infracost/actions", args.repoURL)
	assert.Equal(t, "flag-run", args.pipelineRunID)
}

// The host no longer constrains the owner and repo: newVCSClient derives
// APIURL from the same URL, so a GHES token goes to the GHES host.
func TestResolveOwnerRepo_AcceptsEnterpriseHost(t *testing.T) {
	owner, repo, err := resolveOwnerRepo("https://ghes.corp.internal/org/repo", "", "")
	require.NoError(t, err)
	assert.Equal(t, "org", owner)
	assert.Equal(t, "repo", repo)

	apiURL, err := vcsurl.GitHubAPIURL("https://ghes.corp.internal/org/repo")
	require.NoError(t, err)
	assert.Equal(t, "https://ghes.corp.internal", apiURL)
}

func TestNormaliseTimestamp(t *testing.T) {
	got, err := normaliseTimestamp("INFRACOST_VCS_COMMIT_TIMESTAMP", "1700000000")
	require.NoError(t, err)
	assert.Equal(t, "2023-11-14T22:13:20Z", got)

	got, err = normaliseTimestamp("INFRACOST_VCS_COMMIT_TIMESTAMP", "2024-01-02T03:04:05+01:00")
	require.NoError(t, err)
	assert.Equal(t, "2024-01-02T03:04:05+01:00", got)

	_, err = normaliseTimestamp("INFRACOST_VCS_COMMIT_TIMESTAMP", "not-a-timestamp")
	require.EqualError(t, err, `invalid INFRACOST_VCS_COMMIT_TIMESTAMP "not-a-timestamp": expected Unix epoch seconds or an RFC3339 timestamp`)

	// Digits outside epoch seconds are rejected, not read as a far-future date.
	for _, value := range []string{"1700000000000", "20240102"} {
		_, err = normaliseTimestamp("INFRACOST_VCS_COMMIT_TIMESTAMP", value)
		require.Error(t, err, value)
	}
}

func TestDiffVCSContext_FlagPullRequestURLOverridesEnvironment(t *testing.T) {
	t.Setenv("INFRACOST_VCS_PROVIDER", "github")
	t.Setenv("INFRACOST_VCS_REPOSITORY_URL", "https://github.com/infracost/actions")
	t.Setenv("INFRACOST_VCS_PULL_REQUEST_URL", "https://github.com/infracost/actions/pull/7")

	vcsCtx, err := execDiff(t, "--pr-url", "https://github.com/infracost/actions/pull/8", "--pr-number", "8")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/infracost/actions/pull/8", vcsCtx.prURL)
	assert.Equal(t, 8, vcsCtx.prNumber)
}

func TestCommitTimeGenerated(t *testing.T) {
	tests := []struct {
		name          string
		override      string
		committerDate string
		want          string
	}{
		{name: "committer date", committerDate: "2024-01-02T03:04:05Z", want: "2024-01-02T03:04:05Z"},
		{name: "override wins", override: "1700000000", committerDate: "2024-01-02T03:04:05Z", want: "2023-11-14T22:13:20Z"},
		{name: "empty falls back to wall clock", want: ""},
		{
			name:          "a future date falls back to wall clock",
			committerDate: time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
			want:          "",
		},
		{
			name:          "an unparseable committer date falls back to wall clock",
			committerDate: "10000-01-01T00:00:00+00:00",
			want:          "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := commitTimeGenerated(tt.override, tt.committerDate)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	_, err := commitTimeGenerated("not-a-timestamp", "")
	require.Error(t, err)
}
