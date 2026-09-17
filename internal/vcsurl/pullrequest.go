// Package vcsurl builds VCS web URLs from a repository's web URL.
package vcsurl

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Provider names. Fixed by the v0.1 INFRACOST_VCS_PROVIDER contract, and
// deliberately not the vcs module's package names.
const (
	ProviderGitHub     = "github"
	ProviderGitLab     = "gitlab"
	ProviderAzureRepos = "azure_repos"
	ProviderBitbucket  = "bitbucket"
)

// providers is the accepted value set, in the order error messages list them.
// Unexported so no caller can widen the contract at runtime.
var providers = []string{ProviderGitHub, ProviderGitLab, ProviderAzureRepos, ProviderBitbucket}

// prPaths is the segment between the repository URL and the pull request
// number. Shared by PullRequest and ParsePullRequest so they cannot drift.
var prPaths = map[string]string{
	ProviderGitHub: "/pull/",
	// The number must be the project-scoped iid, not the global merge request
	// id. A global id is still positive, so it 404s silently.
	ProviderGitLab:     "/-/merge_requests/",
	ProviderAzureRepos: "/pullrequest/",
	ProviderBitbucket:  "/pull-requests/",
}

// gitHubHosts are the github.com hosts. A host outside this set is GHES, which
// GitHubAPIURL turns into an APIURL rather than rejecting.
var gitHubHosts = []string{"github.com", "www.github.com"}

// providerHosts pins a vendor-hosted host to the only provider that can serve
// it. Self-managed installs have no fixed hostname, so an absent host is
// unconstrained rather than invalid.
var providerHosts = map[string]string{
	"github.com":     ProviderGitHub,
	"www.github.com": ProviderGitHub,
	"gitlab.com":     ProviderGitLab,
	"dev.azure.com":  ProviderAzureRepos,
	"bitbucket.org":  ProviderBitbucket,
}

// visualStudioSuffix matches the older org-scoped Azure Repos hosts,
// e.g. myorg.visualstudio.com.
const visualStudioSuffix = ".visualstudio.com"

// Valid reports whether PullRequest can build a URL for provider.
func Valid(provider string) bool {
	return slices.Contains(providers, provider)
}

// ProviderList renders the accepted providers for use in an error message.
func ProviderList() string {
	return strings.Join(providers, ", ")
}

// PullRequest returns the web URL of a pull request for the given provider.
// repoURL is the repository's web URL. An empty repoURL or a number <= 0 yields
// "" and no error, so a caller needing a PR URL must reject "" itself.
func PullRequest(provider, repoURL string, number int) (string, error) {
	if repoURL == "" || number <= 0 {
		return "", nil
	}

	if err := CheckRepoURL(provider, repoURL); err != nil {
		return "", err
	}
	base := TrimRepoURL(repoURL)

	path, ok := prPaths[provider]
	if !ok {
		return "", fmt.Errorf("cannot build a pull request URL for VCS provider %q: must be one of %s", provider, ProviderList())
	}

	// Azure PR URLs hang off the repository, not the project, and a project
	// URL is otherwise indistinguishable from a repository one.
	if provider == ProviderAzureRepos && !strings.Contains(base, "/_git/") {
		return "", fmt.Errorf("repo URL %q must be an Azure Repos repository URL containing /_git/", repoURL)
	}

	return fmt.Sprintf("%s%s%d", base, path, number), nil
}

// ParsePullRequest is the inverse of PullRequest: it recovers the repository
// web URL and the pull request number from a pull request URL.
func ParsePullRequest(provider, prURL string) (string, int, error) {
	if prURL == "" {
		return "", 0, nil
	}

	if err := CheckRepoURL(provider, prURL); err != nil {
		return "", 0, err
	}

	path, ok := prPaths[provider]
	if !ok {
		return "", 0, fmt.Errorf("cannot parse a pull request URL for VCS provider %q: must be one of %s", provider, ProviderList())
	}

	trimmed := strings.TrimSuffix(prURL, "/")
	i := strings.LastIndex(trimmed, path)
	if i < 0 {
		return "", 0, fmt.Errorf("pull request URL %q is not a %s pull request URL: expected %s<number>", prURL, provider, path)
	}

	repoURL := trimmed[:i]
	number, err := strconv.Atoi(trimmed[i+len(path):])
	if err != nil || number <= 0 {
		return "", 0, fmt.Errorf("pull request URL %q must end in a positive pull request number", prURL)
	}

	if repoURL == "" {
		return "", 0, fmt.Errorf("pull request URL %q has no repository part before %s", prURL, path)
	}

	return repoURL, number, nil
}

// CheckProviderHost rejects a repository URL whose host belongs to a different
// provider, so a GitLab token cannot be sent to github.com. An unrecognised
// host is allowed: it may be GHES, self-managed GitLab or Azure DevOps Server.
func CheckProviderHost(provider, repoURL string) error {
	// Never echoes the URL: a credentialed clone URL would put its token in
	// the error.
	if err := CheckRepoURL(provider, repoURL); err != nil {
		return err
	}

	host := hostname(repoURL)
	want, known := providerHosts[host]
	if !known && strings.HasSuffix(host, visualStudioSuffix) {
		want, known = ProviderAzureRepos, true
	}
	if known && want != provider {
		return fmt.Errorf("repository URL host %q is a %s host, but INFRACOST_VCS_PROVIDER is %q", host, want, provider)
	}

	return nil
}

// GitHubAPIURL returns the GHES base URL to talk to, or "" for github.com.
// Empty is not cosmetic: github.newClient takes the non-enterprise path only
// when APIURL is empty, and appends /api/graphql to anything else.
func GitHubAPIURL(repoURL string) (string, error) {
	if err := CheckRepoURL(ProviderGitHub, repoURL); err != nil {
		return "", err
	}

	if slices.Contains(gitHubHosts, hostname(repoURL)) {
		return "", nil
	}

	u, _ := url.Parse(repoURL) // CheckRepoURL parsed it already.
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host), nil
}

// GitLabProject splits a GitLab repository web URL into the server to talk to
// and the full project path the API keys on.
func GitLabProject(repoURL string) (string, string, error) {
	if err := CheckRepoURL(ProviderGitLab, repoURL); err != nil {
		return "", "", err
	}

	u, _ := url.Parse(repoURL) // CheckRepoURL parsed it already.
	serverURL := fmt.Sprintf("%s://%s", u.Scheme, u.Host)

	// The whole path, not the last two segments: gitlab.New binds
	// project(fullPath:), which 404s silently on a truncated subgroup path.
	project := strings.Trim(TrimRepoURL(u.Path), "/")
	parts := strings.Split(project, "/")
	if len(parts) < 2 || slices.Contains(parts, "") {
		return "", "", fmt.Errorf("cannot derive the GitLab project: the repo URL path must be /<group>/<project>, or set --gitlab-project and --gitlab-server-url")
	}

	return serverURL, project, nil
}

// OwnerRepo extracts the owner and repository name from a GitHub web URL. The
// host is not constrained here: GitHubAPIURL sends the token to whichever host
// this URL names, so a GHES URL is derived from, not rejected.
func OwnerRepo(repoURL string) (string, string, error) {
	if err := CheckRepoURL(ProviderGitHub, repoURL); err != nil {
		return "", "", err
	}

	u, _ := url.Parse(repoURL) // CheckRepoURL parsed it already.

	// The slash goes first: a path of /owner/repo.git/ keeps its suffix if the
	// two run the other way round.
	parts := strings.Split(strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("cannot derive the GitHub owner and repository: the repo URL path must be /<owner>/<repo>, or set --github-owner and --github-repo")
	}

	return parts[0], parts[1], nil
}

// TrimRepoURL drops the trailing slash and .git suffix a clone URL carries.
// Exported because azure.buildAPIURL appends the repo segment untrimmed, so a
// .git suffix would become a 404ing API path.
func TrimRepoURL(repoURL string) string {
	return strings.TrimSuffix(strings.TrimSuffix(repoURL, "/"), ".git")
}

// hostname lowercases the host: hosts are case-insensitive and url.Parse does
// not normalise them. Hostname() also strips the port and IPv6 brackets. The
// FQDN trailing dot goes too, or "github.com." would miss providerHosts.
func hostname(repoURL string) string {
	u, err := url.Parse(repoURL)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

// CheckRepoURL constrains the metadata URL, not the transport: nothing in the
// binary clones, so an SSH remote here only yields an unopenable link.
func CheckRepoURL(provider, repoURL string) error {
	u, err := url.Parse(repoURL)

	// Any userinfo, not just a password: a token-only clone URL would otherwise
	// leak its token into the error, the metadata and the PR key. Azure Repos
	// is exempt: org@dev.azure.com is the web URL it hands out.
	if err == nil && u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword || provider != ProviderAzureRepos {
			return fmt.Errorf("repo URL must not contain credentials: pass the repository's web URL, not a credentialed clone URL")
		}
	}

	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("repo URL %q must be an http(s) web URL of the repository, not a clone URL", repoURL)
	}

	// The PR path is appended to the raw URL, so anything after it corrupts.
	// Not echoed: a query or fragment is a common place for a stray token.
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("repo URL must not contain a query or fragment: pass the repository's web URL")
	}

	return nil
}
