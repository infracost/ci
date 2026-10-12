package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/infracost/ci/v2/internal/config"
	"github.com/infracost/ci/v2/internal/vcsurl"
	"github.com/infracost/cli/pkg/logging"
)

// fillGitLabPullRequest fills the merge request author, and the title on a
// branch pipeline that has no CI_MERGE_REQUEST_TITLE.
//
// GitLab has no predefined variable for the merge request author, so inference
// falls back to CI_COMMIT_AUTHOR — whoever wrote the last commit, which is
// usually but not always the person who opened it.
//
// Best effort throughout. Every failure is a debug line: a missing author must
// not stop a comment.
func fillGitLabPullRequest(ctx context.Context, cfg *config.Config, args *diffArgs, vcsCtx *diffContext) {
	if vcsCtx.provider != vcsurl.ProviderGitLab || vcsCtx.prNumber <= 0 {
		return
	}
	if !needsPullRequestLookup(cfg, vcsCtx) {
		return
	}

	if args.gitlabToken == "" {
		logging.Debugf("skipping the GitLab merge request lookup: set --gitlab-token, or GITLAB_TOKEN, to fill the merge request title and author")
		return
	}

	apiURL, err := gitLabMRURL(vcsCtx.repoURL, args.gitlabProject, args.gitlabServer, vcsCtx.prNumber)
	if err != nil {
		logging.Debugf("skipping the GitLab merge request lookup: %s", err)
		return
	}

	tlsConfig, err := cfg.TLSConfig()
	if err != nil {
		logging.Debugf("skipping the GitLab merge request lookup: %s", err)
		return
	}

	var mr gitLabMergeRequest
	err = fetchPRJSON(ctx, apiURL, tlsConfig, func(req *http.Request) {
		// PRIVATE-TOKEN takes personal, project and group access tokens, which
		// is every token the GitLab recipes tell you to make.
		req.Header.Set("PRIVATE-TOKEN", args.gitlabToken)
	}, &mr)
	if err != nil {
		logging.Debugf("could not fetch the GitLab merge request: %s", err)
		return
	}

	if vcsCtx.prTitle == "" {
		vcsCtx.prTitle = mr.Title
	}
	if mr.Author.Username != "" && authorIsInferred(cfg, vcsCtx.prAuthor) {
		vcsCtx.prAuthor = mr.Author.Username
	}
}

// gitLabMRURL builds the REST merge request URL. The token goes in the
// request, so a self-managed server URL is pinned to https and host-checked.
func gitLabMRURL(repoURL, projectOverride, serverOverride string, prNumber int) (string, error) {
	serverURL, project, err := vcsurl.GitLabProject(repoURL)
	if err != nil && (projectOverride == "" || serverOverride == "") {
		return "", err
	}
	serverURL = strings.TrimSuffix(firstNonEmpty(serverOverride, serverURL), "/")
	project = strings.Trim(firstNonEmpty(projectOverride, project), "/")

	u, err := url.Parse(serverURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("gitlab server URL is not a URL")
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("gitlab server URL %q is not https", serverURL)
	}
	if err := vcsurl.CheckProviderHost(vcsurl.ProviderGitLab, serverURL); err != nil {
		return "", err
	}
	if project == "" {
		return "", fmt.Errorf("cannot determine the GitLab project: set --gitlab-project")
	}

	// The project path is one path segment, so every "/" in it is escaped.
	// Not QueryEscape: that renders a space as "+", which a path does not mean.
	escaped := strings.ReplaceAll(url.PathEscape(project), "/", "%2F")
	return fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d", serverURL, escaped, prNumber), nil
}

// gitLabMergeRequest is the slice of the API response we read.
type gitLabMergeRequest struct {
	Title  string `json:"title"`
	Author struct {
		Username string `json:"username"`
	} `json:"author"`
}
