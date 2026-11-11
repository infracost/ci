package commands

import (
	"fmt"
	"strings"

	"github.com/infracost/ci/v2/internal/api/events"
	"github.com/infracost/ci/v2/internal/config"
)

// envSnippet renders the named variables in the syntax of the detected CI
// platform, so a run that cannot determine a value is answered with something
// to paste rather than a variable name to go and look up.
//
// Values are the platform's own expressions where the platform has one, and
// a placeholder where it does not.
func envSnippet(names ...string) string {
	if len(names) == 0 {
		return ""
	}

	platform := strings.ToLower(events.CIPlatform())
	format, ok := envSnippetFormats[platform]
	if !ok {
		format = envSnippetFormats[""]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n\n%s\n", format.heading)
	for _, name := range names {
		value := envSnippetValues[platform][name]
		if value == "" {
			value = "<value>"
		}
		fmt.Fprintf(&b, format.line+"\n", name, value)
	}
	return strings.TrimRight(b.String(), "\n")
}

// envSnippetFormat is how one platform writes an environment variable, the
// line that introduces the block, and the platform's own name for prose.
type envSnippetFormat struct {
	name    string
	heading string
	line    string
}

// platformName is what to call the detected platform in a sentence, or "" for
// one we do not recognise. CIPlatform() is raw — it falls back to the value of
// $CI and renders Azure as azure_devops_TfsGit — so it is never shown as-is.
func platformName() string {
	return envSnippetFormats[strings.ToLower(events.CIPlatform())].name
}

// envSnippetFormats is keyed on the platform CIPlatform() names, with "" as
// the fallback for a platform with no entry.
var envSnippetFormats = map[string]envSnippetFormat{
	"": {
		heading: "Set it before running Infracost:",
		line:    "  export %s=%s",
	},
	"github_actions": {
		name:    "GitHub Actions",
		heading: "In GitHub Actions, add this to the job's env: block:",
		line:    "      %s: %s",
	},
	"gitlab_ci": {
		name:    "GitLab CI",
		heading: "In GitLab CI, add this to the job's variables: block:",
		line:    "    %s: %s",
	},
	"bitbucket": {
		name:    "Bitbucket Pipelines",
		heading: "In Bitbucket Pipelines, add this to the step's script:",
		line:    "  - export %s=%s",
	},
	"circleci": {
		name:    "CircleCI",
		heading: "In CircleCI, add this to the job's environment: block:",
		line:    "      %s: %s",
	},
	"buildkite": {
		name:    "Buildkite",
		heading: "In Buildkite, add this to the step's env: block:",
		line:    "    %s: %s",
	},
	"jenkins": {
		name:    "Jenkins",
		heading: "In Jenkins, add this to the stage's environment block:",
		line:    "    %s = %s",
	},
	"azure_devops_tfsgit": {
		name:    "Azure Pipelines",
		heading: "In Azure Pipelines, add this to the job's variables: block:",
		line:    "    %s: %s",
	},
	"azure_devops_github": {
		name:    "Azure Pipelines",
		heading: "In Azure Pipelines, add this to the job's variables: block:",
		line:    "    %s: %s",
	},
}

// envSnippetValues is the platform expression that produces each value, where
// the platform has one. A variable absent here is written as a placeholder,
// which is still the right shape to paste and fill in.
var envSnippetValues = map[string]map[string]string{
	"github_actions": {
		"INFRACOST_VCS_PULL_REQUEST_URL": "${{ github.event.pull_request.html_url }}",
		"INFRACOST_VCS_PULL_REQUEST_ID":  "${{ github.event.pull_request.number }}",
		"INFRACOST_VCS_REPOSITORY_URL":   "${{ github.event.repository.html_url }}",
		"INFRACOST_VCS_BRANCH":           "${{ github.event.pull_request.head.ref }}",
		"INFRACOST_VCS_BASE_BRANCH":      "${{ github.event.pull_request.base.ref }}",
	},
	"gitlab_ci": {
		"INFRACOST_VCS_PULL_REQUEST_URL": "$CI_MERGE_REQUEST_PROJECT_URL/-/merge_requests/$CI_MERGE_REQUEST_IID",
		"INFRACOST_VCS_PULL_REQUEST_ID":  "$CI_MERGE_REQUEST_IID",
		"INFRACOST_VCS_REPOSITORY_URL":   "$CI_PROJECT_URL",
		"INFRACOST_VCS_BRANCH":           "$CI_MERGE_REQUEST_SOURCE_BRANCH_NAME",
		"INFRACOST_VCS_BASE_BRANCH":      "$CI_MERGE_REQUEST_TARGET_BRANCH_NAME",
	},
	"bitbucket": {
		"INFRACOST_VCS_PULL_REQUEST_ID": "$BITBUCKET_PR_ID",
		"INFRACOST_VCS_REPOSITORY_URL":  "$BITBUCKET_GIT_HTTP_ORIGIN",
		"INFRACOST_VCS_BRANCH":          "$BITBUCKET_BRANCH",
		"INFRACOST_VCS_BASE_BRANCH":     "$BITBUCKET_PR_DESTINATION_BRANCH",
	},
	"circleci": {
		"INFRACOST_VCS_PULL_REQUEST_URL": "$CIRCLE_PULL_REQUEST",
		"INFRACOST_VCS_BRANCH":           "$CIRCLE_BRANCH",
	},
	"buildkite": {
		"INFRACOST_VCS_PULL_REQUEST_ID": "$BUILDKITE_PULL_REQUEST",
		"INFRACOST_VCS_BRANCH":          "$BUILDKITE_BRANCH",
		"INFRACOST_VCS_BASE_BRANCH":     "$BUILDKITE_PULL_REQUEST_BASE_BRANCH",
	},
	"jenkins": {
		"INFRACOST_VCS_PULL_REQUEST_ID": "env.CHANGE_ID",
		"INFRACOST_VCS_BRANCH":          "env.CHANGE_BRANCH",
		"INFRACOST_VCS_BASE_BRANCH":     "env.CHANGE_TARGET",
	},
}

// pullRequestTriggers is how each platform is asked to run a build on a pull
// request. Keyed as envSnippetFormats is, and covering exactly the platforms
// config.InfersPullRequest reports on.
var pullRequestTriggers = map[string]string{
	"github_actions":      "run the workflow on `on: pull_request`",
	"gitlab_ci":           "run the job on merge request pipelines, with `rules: - if: $CI_MERGE_REQUEST_IID`",
	"bitbucket":           "declare the step under `pull-requests:` rather than `branches:`",
	"circleci":            "run the workflow on pull requests, so CircleCI sets CIRCLE_PULL_REQUEST",
	"buildkite":           "run the pipeline on pull requests, so Buildkite sets BUILDKITE_PULL_REQUEST",
	"jenkins":             "use a multibranch pipeline, which builds a change request as PR-<n> and sets CHANGE_ID",
	"azure_devops_tfsgit": "add a `pr:` trigger, so the pipeline runs on pull requests as well as branches",
	"azure_devops_github": "add a `pr:` trigger, so the pipeline runs on pull requests as well as branches",
}

// missingPullRequestHint answers a run that reached diff with no pull request.
//
// On a platform Infracost infers from, the platform's own pull request
// variable was empty, so pasting that variable sets nothing: the pipeline ran
// on a branch or a push. Elsewhere nothing was inferred, and the variable
// really is the fix.
func missingPullRequestHint() string {
	if !config.InfersPullRequest() {
		return envSnippet("INFRACOST_VCS_PULL_REQUEST_URL")
	}

	platform := platformName()
	if platform == "" {
		platform = "this CI platform"
	}

	hint := fmt.Sprintf("\n\n%s names the pull request itself, and did not here, so this build is not running on one.", platform)
	if trigger := pullRequestTriggers[strings.ToLower(events.CIPlatform())]; trigger != "" {
		hint += "\nTo comment on pull requests, " + trigger + "."
	}
	return hint + "\nA branch or push build has no pull request to comment on: run `infracost-ci scan` there instead. See " + ciDocsURL
}
