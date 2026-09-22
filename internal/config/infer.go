package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/infracost/ci/internal/api/events"
)

// Inferences records what a platform supplied and what the environment kept,
// for the debug line and the events metadata. Logging happens in Process: this
// runs before logging is configured.
type Inferences struct {
	// Platform is the CIPlatform() string the values came from, empty when
	// nothing was inferred.
	Platform string

	// Inferred and Overridden are contract field names, in struct order.
	Inferred   []string
	Overridden []string
}

// FieldPullRequestAuthor is the one field name a caller needs by name: the
// Azure lookup replaces an inferred author, never an explicit one.
const FieldPullRequestAuthor = "pull_request_author"

// String renders the one debug line Process writes.
func (i Inferences) String() string {
	if i.Platform == "" {
		return "no CI platform detected, VCS metadata comes from the environment and the git checkout"
	}

	line := fmt.Sprintf("inferred from %s: %s", i.Platform, strings.Join(i.Inferred, ", "))
	if len(i.Inferred) == 0 {
		line = fmt.Sprintf("inferred from %s: nothing", i.Platform)
	}
	if len(i.Overridden) > 0 {
		line += fmt.Sprintf("; overridden by env: %s", strings.Join(i.Overridden, ", "))
	}
	return line
}

// Filled reports whether the platform supplied the named field, rather than the
// environment setting it.
func (i Inferences) Filled(name string) bool {
	return slices.Contains(i.Inferred, name)
}

// InferVCS fills the VCS fields the environment left empty from the detected
// CI platform's own predefined variables. An INFRACOST_VCS_* value always wins:
// nothing set here overwrites a field PreProcess already hydrated.
//
// Pure apart from INFRACOST_CI_PLATFORM: no INFRACOST_VCS_* is written back,
// because scan's warnings and the vcsEnv telemetry report what the user set.
func InferVCS(cfg *Config) Inferences {
	labelled := os.Getenv("INFRACOST_CI_PLATFORM") != ""
	platform := events.CIPlatform()

	inferred := platformValues(platform)
	if inferred == nil {
		return Inferences{}
	}

	// Pin the detected name so the run metadata, the events payload and any
	// plugin subprocess agree on one string without re-deriving it.
	if !labelled {
		_ = os.Setenv("INFRACOST_CI_PLATFORM", platform)
	}

	if cfg.VCSProvider == "" {
		cfg.VCSProvider = inferred.provider
	}

	record := Inferences{Platform: platform}
	for _, f := range []struct {
		name  string
		field *string
		value string
	}{
		{"repository_url", &cfg.VCS.RepositoryURL, inferred.repositoryURL},
		{"pull_request_id", nil, ""}, // handled below: the only int field.
		{"pull_request_title", &cfg.VCS.PullRequestTitle, inferred.title},
		{FieldPullRequestAuthor, &cfg.VCS.PullRequestAuthor, inferred.author},
		{"pull_request_labels", &cfg.VCS.PullRequestLabels, inferred.labels},
		{"branch", &cfg.VCS.Branch, inferred.branch},
		{"base_branch", &cfg.VCS.BaseBranch, inferred.baseBranch},
		{"pipeline_run_id", &cfg.VCS.PipelineRunID, inferred.pipelineRunID},
	} {
		if f.field == nil {
			record.add(f.name, cfg.VCS.PullRequestID != 0, inferred.prNumber != 0)
			if cfg.VCS.PullRequestID == 0 {
				cfg.VCS.PullRequestID = inferred.prNumber
			}
			continue
		}

		record.add(f.name, *f.field != "", f.value != "")
		if *f.field == "" {
			*f.field = f.value
		}
	}

	return record
}

// add classifies one field: set by the environment, or filled from the
// platform. A gap on both sides is not worth reporting.
func (i *Inferences) add(name string, fromEnv, fromPlatform bool) {
	switch {
	case fromEnv && fromPlatform:
		i.Overridden = append(i.Overridden, name)
	case !fromEnv && fromPlatform:
		i.Inferred = append(i.Inferred, name)
	}
}

// vcsValues is one platform's answer to the contract. Commit fields are absent
// on purpose: GITHUB_SHA on a pull_request event is the merge commit, and the
// --head-path checkout already has the right one.
type vcsValues struct {
	provider      string
	repositoryURL string
	prNumber      int
	title         string
	author        string
	labels        string
	branch        string
	baseBranch    string
	pipelineRunID string
}

// platformValues dispatches on the platform CIPlatform() names. An explicit
// INFRACOST_CI_PLATFORM therefore pins inference: CIPlatform returns it
// verbatim, and a name not listed here infers nothing.
func platformValues(platform string) *vcsValues {
	switch strings.ToLower(platform) {
	case "github_actions":
		return githubActionsValues()
	case "gitlab_ci":
		return gitlabValues()
	case "bitbucket":
		return bitbucketValues()
	// getCIPlatform interpolates BUILD_REPOSITORY_PROVIDER raw, so the real
	// values are azure_devops_TfsGit and azure_devops_GitHub.
	case "azure_devops_tfsgit":
		return azureValues("azure_repos")
	case "azure_devops_github":
		return azureValues("github")
	// GHES is a GitHub repository to everything downstream; only the API URL
	// differs, and that is derived from the repository URL.
	case "azure_devops_githubenterprise":
		return azureValues("github")
	}
	return nil
}

func gitlabValues() *vcsValues {
	return &vcsValues{
		provider:      "gitlab",
		repositoryURL: os.Getenv("CI_PROJECT_URL"),
		prNumber:      envInt("CI_MERGE_REQUEST_IID"),
		title:         os.Getenv("CI_MERGE_REQUEST_TITLE"),
		// The head commit's author, in "Name <email>" form. Not the MR author,
		// which needs an API call, and closer to it than GITLAB_USER_LOGIN,
		// which is whoever pressed run.
		author: strings.TrimSpace(strings.Split(os.Getenv("CI_COMMIT_AUTHOR"), " <")[0]),
		labels: os.Getenv("CI_MERGE_REQUEST_LABELS"),
		// A branch pipeline sets neither merge request branch, and the
		// detached checkout would otherwise upload "HEAD" as the branch.
		branch:        firstNonEmpty(os.Getenv("CI_MERGE_REQUEST_SOURCE_BRANCH_NAME"), os.Getenv("CI_COMMIT_BRANCH")),
		baseBranch:    os.Getenv("CI_MERGE_REQUEST_TARGET_BRANCH_NAME"),
		pipelineRunID: os.Getenv("CI_PIPELINE_ID"),
	}
}

func bitbucketValues() *vcsValues {
	return &vcsValues{
		provider:      "bitbucket",
		repositoryURL: os.Getenv("BITBUCKET_GIT_HTTP_ORIGIN"),
		// Set only in a pipeline declared under pull-requests:, so a branch
		// pipeline infers the repository and branch but no pull request.
		prNumber:      envInt("BITBUCKET_PR_ID"),
		branch:        os.Getenv("BITBUCKET_BRANCH"),
		baseBranch:    os.Getenv("BITBUCKET_PR_DESTINATION_BRANCH"),
		pipelineRunID: os.Getenv("BITBUCKET_BUILD_NUMBER"),
	}
}

// azureValues covers both backing repository types. Title and author are not
// here: Azure has no predefined variable for the title, and the PR API lookup
// that has it needs a context and a TLS config this function does not have.
func azureValues(provider string) *vcsValues {
	// The ID is Azure-internal; against a GitHub-backed repo the number is the
	// GitHub one, and the ID would key the wrong pull request.
	prNumber := envInt("SYSTEM_PULLREQUEST_PULLREQUESTID")
	if provider == "github" {
		prNumber = envInt("SYSTEM_PULLREQUEST_PULLREQUESTNUMBER")
	}

	return &vcsValues{
		provider: provider,
		// Azure hands out https://org@dev.azure.com/...; the userinfo would
		// give one repository two dashboard keys.
		repositoryURL: stripUserinfo(os.Getenv("BUILD_REPOSITORY_URI")),
		prNumber:      prNumber,
		author:        os.Getenv("BUILD_REQUESTEDFOR"),
		// BUILD_SOURCEBRANCHNAME is "merge" on a pull request build, so it is
		// the fallback for a plain CI build rather than the first choice.
		branch: firstNonEmpty(
			strings.TrimPrefix(os.Getenv("SYSTEM_PULLREQUEST_SOURCEBRANCH"), "refs/heads/"),
			os.Getenv("BUILD_SOURCEBRANCHNAME"),
		),
		baseBranch:    strings.TrimPrefix(os.Getenv("SYSTEM_PULLREQUEST_TARGETBRANCH"), "refs/heads/"),
		pipelineRunID: os.Getenv("BUILD_BUILDID"),
	}
}

// githubActionsValues reads the event payload: Actions has no predefined
// variable for the pull request number, title, author or labels.
func githubActionsValues() *vcsValues {
	// GITHUB_HEAD_REF is set on a pull_request event only, and GITHUB_REF_NAME
	// is "42/merge" there. On a push it is the branch, unless the ref is a tag.
	branch := os.Getenv("GITHUB_HEAD_REF")
	if branch == "" && os.Getenv("GITHUB_REF_TYPE") == "branch" {
		branch = os.Getenv("GITHUB_REF_NAME")
	}

	values := &vcsValues{
		provider:      "github",
		branch:        branch,
		baseBranch:    os.Getenv("GITHUB_BASE_REF"),
		pipelineRunID: os.Getenv("GITHUB_RUN_ID"),
	}

	if server, repo := os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_REPOSITORY"); server != "" && repo != "" {
		values.repositoryURL = strings.TrimSuffix(server, "/") + "/" + repo
	}

	event := readGitHubEvent()
	if event == nil {
		return values
	}

	values.prNumber = event.PullRequest.Number
	values.title = event.PullRequest.Title
	values.author = event.PullRequest.User.Login
	if values.repositoryURL == "" {
		values.repositoryURL = event.Repository.HTMLURL
	}

	labels := make([]string, 0, len(event.PullRequest.Labels))
	for _, label := range event.PullRequest.Labels {
		if label.Name != "" {
			labels = append(labels, label.Name)
		}
	}
	values.labels = strings.Join(labels, ",")

	return values
}

// githubEvent is the slice of the Actions event payload we read. A push event
// has no pull_request key, which decodes to the zero value.
type githubEvent struct {
	PullRequest struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		User   struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"pull_request"`
	Repository struct {
		HTMLURL string `json:"html_url"`
	} `json:"repository"`
}

// readGitHubEvent returns nil for every failure: an unreadable payload leaves
// gaps, it does not stop the run.
func readGitHubEvent() *githubEvent {
	path := os.Getenv("GITHUB_EVENT_PATH")
	if path == "" {
		return nil
	}

	raw, err := os.ReadFile(path) //nolint:gosec // G304: the runner sets GITHUB_EVENT_PATH to its own payload
	if err != nil {
		return nil
	}

	var event githubEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil
	}
	return &event
}

// firstNonEmpty returns the first set value, platform-preferred order first.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func envInt(name string) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < 0 {
		return 0
	}
	return value
}

// stripUserinfo drops the user@ a clone-style URL carries, leaving the web URL
// the dashboard keys on. A URL it cannot parse is returned untouched.
func stripUserinfo(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL
	}
	u.User = nil
	return u.String()
}
