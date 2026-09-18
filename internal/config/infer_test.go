package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ciMarkers is every variable that steers detection or inference. The test
// process inherits the CI it runs in, so each test starts from none of them.
var ciMarkers = []string{
	"INFRACOST_CI_PLATFORM",
	"GITHUB_ACTIONS", "GITHUB_SERVER_URL", "GITHUB_REPOSITORY", "GITHUB_HEAD_REF",
	"GITHUB_BASE_REF", "GITHUB_RUN_ID", "GITHUB_EVENT_PATH", "GITHUB_REF_NAME", "GITHUB_REF_TYPE",
	"GITLAB_CI", "CI_PROJECT_URL", "CI_MERGE_REQUEST_IID", "CI_MERGE_REQUEST_TITLE",
	"CI_COMMIT_AUTHOR", "CI_MERGE_REQUEST_LABELS", "CI_MERGE_REQUEST_SOURCE_BRANCH_NAME",
	"CI_MERGE_REQUEST_TARGET_BRANCH_NAME", "CI_PIPELINE_ID", "CI_COMMIT_BRANCH",
	"SYSTEM_COLLECTIONURI", "BUILD_REPOSITORY_PROVIDER", "BUILD_REPOSITORY_URI",
	"BUILD_REQUESTEDFOR", "BUILD_BUILDID", "SYSTEM_PULLREQUEST_PULLREQUESTID",
	"SYSTEM_PULLREQUEST_PULLREQUESTNUMBER", "SYSTEM_PULLREQUEST_SOURCEBRANCH",
	"SYSTEM_PULLREQUEST_TARGETBRANCH", "BUILD_SOURCEBRANCHNAME",
	"BITBUCKET_BUILD_NUMBER", "BITBUCKET_GIT_HTTP_ORIGIN", "BITBUCKET_PR_ID",
	"BITBUCKET_BRANCH", "BITBUCKET_PR_DESTINATION_BRANCH",
	"CI", "CIRCLECI", "JENKINS_HOME", "BUILDKITE", "TRAVIS", "CODEBUILD_CI",
}

// clearCIEnv unsets every marker for the test's duration. t.Setenv cannot
// unset, but it registers the restore, so pair it with os.Unsetenv.
func clearCIEnv(t *testing.T) {
	t.Helper()
	for _, name := range ciMarkers {
		if value, ok := os.LookupEnv(name); ok {
			t.Setenv(name, value)
			require.NoError(t, os.Unsetenv(name))
		}
	}
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for name, value := range env {
		t.Setenv(name, value)
	}
}

func TestInferVCS(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		wantPlatform string
		wantProvider string
		want         VCS
	}{
		{
			name: "gitlab merge request",
			env: map[string]string{
				"GITLAB_CI":                           "true",
				"CI_PROJECT_URL":                      "https://gitlab.com/acme/infra",
				"CI_MERGE_REQUEST_IID":                "42",
				"CI_MERGE_REQUEST_TITLE":              "Add a bucket",
				"CI_COMMIT_AUTHOR":                    "Owen Rumney <owen@infracost.io>",
				"CI_MERGE_REQUEST_LABELS":             "cost,infra",
				"CI_MERGE_REQUEST_SOURCE_BRANCH_NAME": "feature/bucket",
				"CI_MERGE_REQUEST_TARGET_BRANCH_NAME": "main",
				"CI_PIPELINE_ID":                      "9876",
			},
			wantPlatform: "gitlab_ci",
			wantProvider: "gitlab",
			want: VCS{
				RepositoryURL:     "https://gitlab.com/acme/infra",
				PullRequestID:     42,
				PullRequestTitle:  "Add a bucket",
				PullRequestAuthor: "Owen Rumney",
				PullRequestLabels: "cost,infra",
				Branch:            "feature/bucket",
				BaseBranch:        "main",
				PipelineRunID:     "9876",
			},
		},
		{
			name: "gitlab branch pipeline takes the commit branch",
			env: map[string]string{
				"GITLAB_CI":        "true",
				"CI_PROJECT_URL":   "https://gitlab.com/acme/infra",
				"CI_COMMIT_BRANCH": "main",
				"CI_PIPELINE_ID":   "9876",
			},
			wantPlatform: "gitlab_ci",
			wantProvider: "gitlab",
			want: VCS{
				RepositoryURL: "https://gitlab.com/acme/infra",
				Branch:        "main",
				PipelineRunID: "9876",
			},
		},
		{
			// CI_COMMIT_BRANCH is unset in a merge request pipeline, but a
			// stale one must not outrank the source branch.
			name: "gitlab merge request branch outranks the commit branch",
			env: map[string]string{
				"GITLAB_CI":                           "true",
				"CI_PROJECT_URL":                      "https://gitlab.com/acme/infra",
				"CI_MERGE_REQUEST_IID":                "42",
				"CI_MERGE_REQUEST_SOURCE_BRANCH_NAME": "feature/bucket",
				"CI_COMMIT_BRANCH":                    "main",
			},
			wantPlatform: "gitlab_ci",
			wantProvider: "gitlab",
			want: VCS{
				RepositoryURL: "https://gitlab.com/acme/infra",
				PullRequestID: 42,
				Branch:        "feature/bucket",
			},
		},
		{
			name: "azure ci build takes the source branch name",
			env: map[string]string{
				"SYSTEM_COLLECTIONURI":      "https://dev.azure.com/acme/",
				"BUILD_REPOSITORY_PROVIDER": "TfsGit",
				"BUILD_REPOSITORY_URI":      "https://dev.azure.com/acme/infra/_git/infra",
				"BUILD_SOURCEBRANCHNAME":    "main",
				"BUILD_BUILDID":             "5150",
			},
			wantPlatform: "azure_devops_TfsGit",
			wantProvider: "azure_repos",
			want: VCS{
				RepositoryURL: "https://dev.azure.com/acme/infra/_git/infra",
				Branch:        "main",
				PipelineRunID: "5150",
			},
		},
		{
			name: "azure repos pull request",
			env: map[string]string{
				"SYSTEM_COLLECTIONURI":             "https://dev.azure.com/acme/",
				"BUILD_REPOSITORY_PROVIDER":        "TfsGit",
				"BUILD_REPOSITORY_URI":             "https://acme@dev.azure.com/acme/infra/_git/infra",
				"BUILD_REQUESTEDFOR":               "Owen Rumney",
				"BUILD_BUILDID":                    "5150",
				"SYSTEM_PULLREQUEST_PULLREQUESTID": "7",
				"SYSTEM_PULLREQUEST_SOURCEBRANCH":  "refs/heads/feature/bucket",
				"SYSTEM_PULLREQUEST_TARGETBRANCH":  "refs/heads/main",
			},
			wantPlatform: "azure_devops_TfsGit",
			wantProvider: "azure_repos",
			want: VCS{
				// The org@ userinfo is stripped: it would give one repository
				// two dashboard keys.
				RepositoryURL:     "https://dev.azure.com/acme/infra/_git/infra",
				PullRequestID:     7,
				PullRequestAuthor: "Owen Rumney",
				Branch:            "feature/bucket",
				BaseBranch:        "main",
				PipelineRunID:     "5150",
			},
		},
		{
			name: "azure pipeline against a github repo takes the github number",
			env: map[string]string{
				"SYSTEM_COLLECTIONURI":                 "https://dev.azure.com/acme/",
				"BUILD_REPOSITORY_PROVIDER":            "GitHub",
				"BUILD_REPOSITORY_URI":                 "https://github.com/acme/infra",
				"BUILD_BUILDID":                        "5150",
				"SYSTEM_PULLREQUEST_PULLREQUESTID":     "900123",
				"SYSTEM_PULLREQUEST_PULLREQUESTNUMBER": "42",
			},
			wantPlatform: "azure_devops_GitHub",
			wantProvider: "github",
			want: VCS{
				RepositoryURL: "https://github.com/acme/infra",
				PullRequestID: 42,
				PipelineRunID: "5150",
			},
		},
		{
			name: "bitbucket pull request pipeline",
			env: map[string]string{
				"BITBUCKET_BUILD_NUMBER":          "88",
				"BITBUCKET_GIT_HTTP_ORIGIN":       "https://bitbucket.org/acme/infra",
				"BITBUCKET_PR_ID":                 "17",
				"BITBUCKET_BRANCH":                "feature/bucket",
				"BITBUCKET_PR_DESTINATION_BRANCH": "main",
			},
			wantPlatform: "bitbucket",
			wantProvider: "bitbucket",
			want: VCS{
				RepositoryURL: "https://bitbucket.org/acme/infra",
				PullRequestID: 17,
				Branch:        "feature/bucket",
				BaseBranch:    "main",
				PipelineRunID: "88",
			},
		},
		{
			name: "bitbucket branch pipeline has no pull request",
			env: map[string]string{
				"BITBUCKET_BUILD_NUMBER":    "88",
				"BITBUCKET_GIT_HTTP_ORIGIN": "https://bitbucket.org/acme/infra",
				"BITBUCKET_BRANCH":          "main",
			},
			wantPlatform: "bitbucket",
			wantProvider: "bitbucket",
			want: VCS{
				RepositoryURL: "https://bitbucket.org/acme/infra",
				Branch:        "main",
				PipelineRunID: "88",
			},
		},
		{
			name:         "unmapped platform infers nothing",
			env:          map[string]string{"CIRCLECI": "true"},
			wantPlatform: "",
			wantProvider: "",
			want:         VCS{},
		},
		{
			name:         "no platform infers nothing",
			env:          map[string]string{},
			wantPlatform: "",
			wantProvider: "",
			want:         VCS{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCIEnv(t)
			setEnv(t, tt.env)

			cfg := new(Config)
			record := InferVCS(cfg)

			assert.Equal(t, tt.want, cfg.VCS)
			assert.Equal(t, tt.wantProvider, cfg.VCSProvider)
			assert.Equal(t, tt.wantPlatform, record.Platform)
		})
	}
}

func TestInferVCSGitHubActions(t *testing.T) {
	clearCIEnv(t)
	setEnv(t, map[string]string{
		"GITHUB_ACTIONS":    "true",
		"GITHUB_SERVER_URL": "https://github.com",
		"GITHUB_REPOSITORY": "acme/infra",
		"GITHUB_HEAD_REF":   "feature/bucket",
		"GITHUB_BASE_REF":   "main",
		"GITHUB_RUN_ID":     "112233",
		"GITHUB_EVENT_PATH": writeEvent(t, `{
			"pull_request": {
				"number": 42,
				"title": "Add a bucket",
				"user": {"login": "owenrumney"},
				"labels": [{"name": "cost"}, {"name": "infra"}]
			},
			"repository": {"html_url": "https://github.com/acme/infra"}
		}`),
	})

	cfg := new(Config)
	InferVCS(cfg)

	assert.Equal(t, VCS{
		RepositoryURL:     "https://github.com/acme/infra",
		PullRequestID:     42,
		PullRequestTitle:  "Add a bucket",
		PullRequestAuthor: "owenrumney",
		// Names, not the label objects v1 stringified.
		PullRequestLabels: "cost,infra",
		Branch:            "feature/bucket",
		BaseBranch:        "main",
		PipelineRunID:     "112233",
	}, cfg.VCS)
	assert.Equal(t, "github", cfg.VCSProvider)
}

// A push event has no pull_request key, and an unreadable payload must leave
// gaps rather than stop the run.
func TestInferVCSGitHubActionsWithoutPullRequest(t *testing.T) {
	for name, path := range map[string]string{
		"push event":     writeEvent(t, `{"repository": {"html_url": "https://github.com/acme/infra"}}`),
		"missing file":   filepath.Join(t.TempDir(), "absent.json"),
		"malformed json": writeEvent(t, `{`),
		"no event path":  "",
	} {
		t.Run(name, func(t *testing.T) {
			clearCIEnv(t)
			setEnv(t, map[string]string{
				"GITHUB_ACTIONS":    "true",
				"GITHUB_SERVER_URL": "https://github.com",
				"GITHUB_REPOSITORY": "acme/infra",
				"GITHUB_RUN_ID":     "112233",
				"GITHUB_EVENT_PATH": path,
			})

			cfg := new(Config)
			InferVCS(cfg)

			assert.Equal(t, VCS{
				RepositoryURL: "https://github.com/acme/infra",
				PipelineRunID: "112233",
			}, cfg.VCS)
		})
	}
}

// The contract is an override: a hydrated INFRACOST_VCS_* is never replaced.
func TestInferVCSDoesNotOverrideTheEnvironment(t *testing.T) {
	clearCIEnv(t)
	setEnv(t, map[string]string{
		"GITLAB_CI":                           "true",
		"CI_PROJECT_URL":                      "https://gitlab.com/acme/inferred",
		"CI_MERGE_REQUEST_IID":                "42",
		"CI_MERGE_REQUEST_TITLE":              "Inferred title",
		"CI_MERGE_REQUEST_SOURCE_BRANCH_NAME": "inferred-branch",
		"CI_PIPELINE_ID":                      "9876",
	})

	cfg := &Config{VCSProvider: "github"}
	cfg.VCS = VCS{
		RepositoryURL:    "https://gitlab.com/acme/explicit",
		PullRequestID:    7,
		PullRequestTitle: "Explicit title",
	}

	record := InferVCS(cfg)

	assert.Equal(t, "https://gitlab.com/acme/explicit", cfg.VCS.RepositoryURL)
	assert.Equal(t, 7, cfg.VCS.PullRequestID)
	assert.Equal(t, "Explicit title", cfg.VCS.PullRequestTitle)
	assert.Equal(t, "github", cfg.VCSProvider)

	// The gaps are still filled.
	assert.Equal(t, "inferred-branch", cfg.VCS.Branch)
	assert.Equal(t, "9876", cfg.VCS.PipelineRunID)

	assert.Equal(t, []string{"repository_url", "pull_request_id", "pull_request_title"}, record.Overridden)
	assert.Equal(t, []string{"branch", "pipeline_run_id"}, record.Inferred)
}

// A detected platform is pinned so every later reader agrees on one string.
// A labelled one is left alone, and pins inference to whatever it names.
func TestInferVCSPinsTheDetectedPlatform(t *testing.T) {
	t.Run("detected", func(t *testing.T) {
		clearCIEnv(t)
		setEnv(t, map[string]string{"GITLAB_CI": "true", "CI_PROJECT_URL": "https://gitlab.com/acme/infra"})

		InferVCS(new(Config))
		assert.Equal(t, "gitlab_ci", os.Getenv("INFRACOST_CI_PLATFORM"))
	})

	t.Run("labelled with an unmapped name", func(t *testing.T) {
		clearCIEnv(t)
		setEnv(t, map[string]string{
			"INFRACOST_CI_PLATFORM": "jenkins",
			"GITLAB_CI":             "true",
			"CI_PROJECT_URL":        "https://gitlab.com/acme/infra",
		})

		cfg := new(Config)
		InferVCS(cfg)

		assert.Equal(t, "jenkins", os.Getenv("INFRACOST_CI_PLATFORM"))
		assert.Equal(t, VCS{}, cfg.VCS)
	})
}

// Inference sets struct fields only: scan's warnings and the vcsEnv telemetry
// report what the user set, and would be wrong if it wrote them back.
func TestInferVCSWritesNoContractVariables(t *testing.T) {
	clearCIEnv(t)
	setEnv(t, map[string]string{
		"GITLAB_CI":            "true",
		"CI_PROJECT_URL":       "https://gitlab.com/acme/infra",
		"CI_MERGE_REQUEST_IID": "42",
	})

	InferVCS(new(Config))

	for _, name := range append([]string{"INFRACOST_VCS_PROVIDER"}, PullRequestEnvNames...) {
		_, ok := os.LookupEnv(name)
		assert.False(t, ok, "%s was written to the environment", name)
	}
	_, ok := os.LookupEnv("INFRACOST_VCS_REPOSITORY_URL")
	assert.False(t, ok, "INFRACOST_VCS_REPOSITORY_URL was written to the environment")
}

func TestInferencesString(t *testing.T) {
	tests := []struct {
		name string
		in   Inferences
		want string
	}{
		{
			name: "inferred and overridden",
			in:   Inferences{Platform: "gitlab_ci", Inferred: []string{"branch"}, Overridden: []string{"repository_url"}},
			want: "inferred from gitlab_ci: branch; overridden by env: repository_url",
		},
		{
			name: "nothing inferred",
			in:   Inferences{Platform: "bitbucket"},
			want: "inferred from bitbucket: nothing",
		},
		{
			name: "no platform",
			in:   Inferences{},
			want: "no CI platform detected, VCS metadata comes from the environment and the git checkout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.in.String())
		})
	}
}

func writeEvent(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "event.json")
	require.NoError(t, os.WriteFile(path, []byte(payload), 0o600))
	return path
}

// A push build has no GITHUB_HEAD_REF, and its GITHUB_REF_NAME is the branch —
// but only when the ref is one. On a pull_request event it is "42/merge".
func TestInferVCSGitHubActionsBranchFallback(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "pull request", want: "feature/bucket", env: map[string]string{
			"GITHUB_HEAD_REF": "feature/bucket", "GITHUB_REF_NAME": "42/merge", "GITHUB_REF_TYPE": "branch"}},
		{name: "push", want: "main", env: map[string]string{
			"GITHUB_REF_NAME": "main", "GITHUB_REF_TYPE": "branch"}},
		{name: "tag", want: "", env: map[string]string{
			"GITHUB_REF_NAME": "v1.2.3", "GITHUB_REF_TYPE": "tag"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCIEnv(t)
			setEnv(t, tt.env)
			t.Setenv("GITHUB_ACTIONS", "true")

			cfg := new(Config)
			InferVCS(cfg)

			assert.Equal(t, tt.want, cfg.VCS.Branch)
		})
	}
}
