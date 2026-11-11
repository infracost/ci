package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvSnippet(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		names    []string
		want     string
	}{
		{
			name:  "no variables produces nothing",
			names: nil,
			want:  "",
		},
		{
			name:     "an unknown platform falls back to export",
			platform: "spacelift",
			names:    []string{"INFRACOST_VCS_PULL_REQUEST_URL"},
			want:     "\n\nSet it before running Infracost:\n  export INFRACOST_VCS_PULL_REQUEST_URL=<value>",
		},
		{
			name:     "GitHub Actions names its own expression",
			platform: "github_actions",
			names:    []string{"INFRACOST_VCS_PULL_REQUEST_URL"},
			want:     "\n\nIn GitHub Actions, add this to the job's env: block:\n      INFRACOST_VCS_PULL_REQUEST_URL: ${{ github.event.pull_request.html_url }}",
		},
		{
			name:     "a variable the platform has no expression for is a placeholder",
			platform: "circleci",
			names:    []string{"INFRACOST_VCS_REPOSITORY_URL"},
			want:     "\n\nIn CircleCI, add this to the job's environment: block:\n      INFRACOST_VCS_REPOSITORY_URL: <value>",
		},
		{
			name:     "every named variable is listed",
			platform: "gitlab_ci",
			names:    []string{"INFRACOST_VCS_BRANCH", "INFRACOST_VCS_BASE_BRANCH"},
			want: "\n\nIn GitLab CI, add this to the job's variables: block:" +
				"\n    INFRACOST_VCS_BRANCH: $CI_MERGE_REQUEST_SOURCE_BRANCH_NAME" +
				"\n    INFRACOST_VCS_BASE_BRANCH: $CI_MERGE_REQUEST_TARGET_BRANCH_NAME",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("INFRACOST_CI_PLATFORM", tt.platform)
			assert.Equal(t, tt.want, envSnippet(tt.names...))
		})
	}
}

// TestMissingPullRequestHint pins the split the hint turns on: every platform
// InferVCS handles reads the pull request number itself, so pasting that
// platform's own variable there sets nothing.
func TestMissingPullRequestHint(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		want     []string
		wantNot  []string
	}{
		{
			name:     "an inferring platform is told to run on pull requests",
			platform: "gitlab_ci",
			want:     []string{"GitLab CI names the pull request itself", "$CI_MERGE_REQUEST_IID", "infracost-ci scan"},
			wantNot:  []string{"INFRACOST_VCS_PULL_REQUEST_URL"},
		},
		{
			name:     "Azure is named in prose and given its trigger",
			platform: "azure_devops_TfsGit",
			want:     []string{"Azure Pipelines names the pull request itself", "`pr:` trigger"},
			wantNot:  []string{"INFRACOST_VCS_PULL_REQUEST_URL"},
		},
		{
			name:     "a platform we infer nothing from still gets the variable",
			platform: "spacelift",
			want:     []string{"export INFRACOST_VCS_PULL_REQUEST_URL=<value>"},
			wantNot:  []string{"names the pull request itself"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("INFRACOST_CI_PLATFORM", tt.platform)
			got := missingPullRequestHint()

			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}
			for _, wantNot := range tt.wantNot {
				assert.NotContains(t, got, wantNot)
			}
		})
	}
}
