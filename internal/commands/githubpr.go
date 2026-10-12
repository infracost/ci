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

// gitHubDotComAPIURL is the REST base for github.com. GitHubAPIURL answers ""
// there, because the comment client only needs a base for GHES.
const gitHubDotComAPIURL = "https://api.github.com"

// fillGitHubPullRequest fills the title and author from the pull request
// itself, the way fillAzurePullRequest does for Azure Repos.
//
// Two platforms reach it with the fields empty. GitHub Actions reads them from
// the event payload, which only a pull_request event has. An Azure pipeline on
// a GitHub-backed repository has no payload at all: its BUILD_REQUESTEDFOR is
// the literal "github", and Azure's own title lookup is Azure Repos only.
//
// Best effort throughout. Every failure is a debug line: a missing title must
// not stop a comment.
func fillGitHubPullRequest(ctx context.Context, cfg *config.Config, args *diffArgs, vcsCtx *diffContext) {
	if vcsCtx.provider != vcsurl.ProviderGitHub || vcsCtx.prNumber <= 0 {
		return
	}
	if !needsPullRequestLookup(cfg, vcsCtx) {
		return
	}

	if args.githubToken == "" {
		logging.Debugf("skipping the GitHub pull request lookup: set --github-token, or GITHUB_TOKEN, to fill the pull request title and author")
		return
	}

	apiURL, err := gitHubPRURL(vcsCtx.repoURL, args.githubOwner, args.githubRepo, args.githubAPIURL, vcsCtx.prNumber)
	if err != nil {
		logging.Debugf("skipping the GitHub pull request lookup: %s", err)
		return
	}

	tlsConfig, err := cfg.TLSConfig()
	if err != nil {
		logging.Debugf("skipping the GitHub pull request lookup: %s", err)
		return
	}

	var pr gitHubPullRequest
	err = fetchPRJSON(ctx, apiURL, tlsConfig, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+args.githubToken)
		req.Header.Set("Accept", "application/vnd.github+json")
	}, &pr)
	if err != nil {
		logging.Debugf("could not fetch the GitHub pull request: %s", err)
		return
	}

	if vcsCtx.prTitle == "" {
		vcsCtx.prTitle = pr.Title
	}
	if pr.User.Login != "" && authorIsInferred(cfg, vcsCtx.prAuthor) {
		vcsCtx.prAuthor = pr.User.Login
	}
}

// gitHubPRURL builds the REST pull request URL. The flags override the path
// and the host the same way newVCSClient reads them.
func gitHubPRURL(repoURL, ownerOverride, repoOverride, apiURLOverride string, prNumber int) (string, error) {
	owner, repo, err := resolveOwnerRepo(repoURL, ownerOverride, repoOverride)
	if err != nil {
		return "", err
	}

	base := strings.TrimSuffix(apiURLOverride, "/")
	if base == "" {
		derived, err := vcsurl.GitHubAPIURL(repoURL)
		if err != nil {
			return "", err
		}
		base = derived
	}

	// Empty is github.com, matching GitHubAPIURL's contract.
	if base == "" {
		base = gitHubDotComAPIURL
	} else {
		u, err := url.Parse(base)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("github API URL is not a URL")
		}
		if u.Scheme != "https" {
			return "", fmt.Errorf("github API URL %q is not https", base)
		}
		// GitHub Enterprise Server serves REST under /api/v3. The flag takes
		// the base server URL, as the GraphQL client's /api/graphql does, but
		// a pasted API URL is common enough to be worth not doubling.
		base = strings.TrimSuffix(base, "/api/v3")
		base += "/api/v3"
	}

	return fmt.Sprintf("%s/repos/%s/%s/pulls/%d", base, url.PathEscape(owner), url.PathEscape(repo), prNumber), nil
}

// gitHubPullRequest is the slice of the API response we read.
type gitHubPullRequest struct {
	Title string `json:"title"`
	User  struct {
		Login string `json:"login"`
	} `json:"user"`
}
