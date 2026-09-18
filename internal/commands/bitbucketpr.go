package commands

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/infracost/ci/internal/config"
	"github.com/infracost/ci/internal/vcsurl"
	"github.com/infracost/cli/pkg/logging"
)

// bitbucketCloudAPIURL is the Cloud REST base, as pkg/vcs/bitbucket uses. The
// web URL is bitbucket.org; the API lives on its own host.
const bitbucketCloudAPIURL = "https://api.bitbucket.org/2.0"

// bitbucketPRMaxBody bounds the response: three fields are read from it.
const bitbucketPRMaxBody = 1 << 20

// fillBitbucketPullRequest fills the title and author Bitbucket Pipelines has
// no predefined variable for, the way fillAzurePullRequest does for Azure.
//
// Best effort throughout. Every failure is a debug line: a missing title must
// not stop a comment.
func fillBitbucketPullRequest(ctx context.Context, cfg *config.Config, args *diffArgs, vcsCtx *diffContext) {
	if vcsCtx.provider != vcsurl.ProviderBitbucket || vcsCtx.prNumber <= 0 {
		return
	}
	if vcsCtx.prTitle != "" && vcsCtx.prAuthor != "" {
		return
	}

	if args.bitbucketToken == "" {
		logging.Debugf("skipping the Bitbucket pull request lookup: set --bitbucket-token, or BITBUCKET_TOKEN, to fill the pull request title and author")
		return
	}

	apiURL, err := bitbucketPRURL(vcsCtx.repoURL, args.bitbucketRepo, args.bitbucketSrv, vcsCtx.prNumber)
	if err != nil {
		logging.Debugf("skipping the Bitbucket pull request lookup: %s", err)
		return
	}

	tlsConfig, err := cfg.TLSConfig()
	if err != nil {
		logging.Debugf("skipping the Bitbucket pull request lookup: %s", err)
		return
	}

	pr, err := fetchBitbucketPullRequest(ctx, apiURL, args.bitbucketToken, tlsConfig)
	if err != nil {
		logging.Debugf("could not fetch the Bitbucket pull request: %s", err)
		return
	}

	if vcsCtx.prTitle == "" {
		vcsCtx.prTitle = pr.Title
	}
	if vcsCtx.prAuthor == "" {
		vcsCtx.prAuthor = pr.authorName()
	}
}

// bitbucketPRURL builds the pull request API URL for whichever flavour the
// repository URL names. The token goes in the request, so a Server URL is
// host-checked and pinned to https first.
func bitbucketPRURL(repoURL, repoOverride, serverOverride string, prNumber int) (string, error) {
	serverURL, repo, err := vcsurl.BitbucketProject(repoURL)
	if err != nil && (repoOverride == "" || serverOverride == "") {
		return "", err
	}
	serverURL = strings.TrimSuffix(firstNonEmpty(serverOverride, serverURL), "/")
	repo = firstNonEmpty(repoOverride, repo)
	// An override that trims away to nothing must not fall through to the Cloud
	// branch below: the private instance's token would go to Atlassian.
	if serverOverride != "" && serverURL == "" {
		return "", fmt.Errorf("bitbucket server URL is not a URL")
	}

	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("repo %q must have the format workspace/repo", repo)
	}
	owner, name := url.PathEscape(parts[0]), url.PathEscape(parts[1])

	// Empty is Cloud, matching newVCSClient's reading of the same two values.
	if serverURL == "" {
		return fmt.Sprintf("%s/repositories/%s/%s/pullrequests/%d", bitbucketCloudAPIURL, owner, name, prNumber), nil
	}

	u, err := url.Parse(serverURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("bitbucket server URL is not a URL")
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("bitbucket server URL %q is not https", serverURL)
	}
	if err := vcsurl.CheckProviderHost(vcsurl.ProviderBitbucket, serverURL); err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/pull-requests/%d", serverURL, owner, name, prNumber), nil
}

// bitbucketPullRequest is the slice of the API response we read. Cloud and
// Server disagree on the author's shape, so both are decoded and authorName
// picks whichever answered.
type bitbucketPullRequest struct {
	Title string `json:"title"`
	// Cloud: author is the account. Server: author wraps a user.
	Author struct {
		DisplayName string `json:"display_name"`
		Nickname    string `json:"nickname"`
		User        struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"user"`
	} `json:"author"`
}

// authorName prefers a handle over a display name: it is stable, and the
// dashboard keys authors on it.
func (pr bitbucketPullRequest) authorName() string {
	return firstNonEmpty(pr.Author.Nickname, pr.Author.User.Name, pr.Author.DisplayName, pr.Author.User.DisplayName)
}

func fetchBitbucketPullRequest(ctx context.Context, apiURL, token string, tlsConfig *tls.Config) (bitbucketPullRequest, error) {
	var pr bitbucketPullRequest

	ctx, cancel := context.WithTimeout(ctx, azurePRTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return pr, err
	}
	setBitbucketAuth(req, token)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	// Do not follow redirects: Go forwards the Authorization header to a
	// same-host target, and a redirect to http would put the token in clear.
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// bitbucketPRURL pins the scheme to https and host-checks a Server URL
	// before this, which is what the taint analysis cannot see.
	res, err := client.Do(req) //nolint:gosec // G704
	if err != nil {
		return pr, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return pr, fmt.Errorf("%s returned %s", apiURL, res.Status)
	}

	if err := json.NewDecoder(io.LimitReader(res.Body, bitbucketPRMaxBody)).Decode(&pr); err != nil {
		return pr, err
	}
	return pr, nil
}

// setBitbucketAuth mirrors pkg/vcs/bitbucket's newClient: a token containing
// ":" is user:app-password and goes in Basic, anything else in Bearer.
func setBitbucketAuth(req *http.Request, token string) {
	if strings.Contains(token, ":") {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(token)))
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
}
