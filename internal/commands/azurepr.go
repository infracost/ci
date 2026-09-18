package commands

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/infracost/ci/internal/config"
	"github.com/infracost/ci/internal/vcsurl"
	"github.com/infracost/cli/pkg/logging"
)

// azurePRTimeout bounds the lookup: a cost comment must not wait on metadata.
const azurePRTimeout = 10 * time.Second

// azurePRMaxBody bounds the response: two fields are read from it.
const azurePRMaxBody = 1 << 20

// fillAzurePullRequest fills the title and author Azure Pipelines has no
// predefined variable for. It is the one lookup outside config.InferVCS: it
// needs a context and the CA bundle, and only a pull request run can use it.
//
// Best effort throughout. Every failure is a debug line: a missing title must
// not stop a comment.
func fillAzurePullRequest(ctx context.Context, cfg *config.Config, token string, vcsCtx *diffContext) {
	if vcsCtx.provider != vcsurl.ProviderAzureRepos || vcsCtx.prNumber <= 0 {
		return
	}
	if vcsCtx.prTitle != "" && vcsCtx.prAuthor != "" {
		return
	}

	// The same token diff posts with: --azure-token, then AZURE_DEVOPS_EXT_PAT
	// or SYSTEM_ACCESSTOKEN. Azure exposes none of them unless the job says so,
	// so the skip is the common path, not the error path.
	if token == "" {
		logging.Debugf("skipping the Azure pull request lookup: set --azure-token, or map System.AccessToken to SYSTEM_ACCESSTOKEN, to fill the pull request title and author")
		return
	}

	apiURL, err := azurePRURL(os.Getenv("SYSTEM_COLLECTIONURI"), vcsCtx.repoURL, os.Getenv("BUILD_REPOSITORY_ID"), vcsCtx.prNumber)
	if err != nil {
		logging.Debugf("skipping the Azure pull request lookup: %s", err)
		return
	}

	tlsConfig, err := cfg.TLSConfig()
	if err != nil {
		logging.Debugf("skipping the Azure pull request lookup: %s", err)
		return
	}

	pr, err := fetchAzurePullRequest(ctx, apiURL, token, tlsConfig)
	if err != nil {
		logging.Debugf("could not fetch the Azure pull request: %s", err)
		return
	}

	if vcsCtx.prTitle == "" {
		vcsCtx.prTitle = pr.Title
	}
	// uniqueName is a handle; BUILD_REQUESTEDFOR, which inference reads, is a
	// display name. The handle beats that, but never an explicit author.
	inferredAuthor := vcsCtx.prAuthor == "" ||
		(cfg.VCSInferences.Filled(config.FieldPullRequestAuthor) && vcsCtx.prAuthor == cfg.VCS.PullRequestAuthor)
	if pr.CreatedBy.UniqueName != "" && inferredAuthor {
		vcsCtx.prAuthor = pr.CreatedBy.UniqueName
	}
}

// azurePRURL builds the pull request API URL, and rejects a collection URI
// that would send the access token somewhere it does not belong.
func azurePRURL(collectionURI, repoURL, repoID string, prNumber int) (string, error) {
	if collectionURI == "" || repoID == "" {
		return "", fmt.Errorf("SYSTEM_COLLECTIONURI and BUILD_REPOSITORY_ID are not both set")
	}

	u, err := url.Parse(collectionURI)
	if err != nil {
		return "", fmt.Errorf("SYSTEM_COLLECTIONURI %q is not a URL", collectionURI)
	}
	// The access token goes in this request, so the transport is not
	// negotiable and the host is checked as the repository URL's is.
	if u.Scheme != "https" {
		return "", fmt.Errorf("SYSTEM_COLLECTIONURI %q is not https", collectionURI)
	}
	if err := vcsurl.CheckProviderHost(vcsurl.ProviderAzureRepos, collectionURI); err != nil {
		return "", err
	}
	// An unrecognised host passes the check above, so the token only goes to
	// the host the repository URL already names.
	if repo, err := url.Parse(repoURL); err != nil || repo.Host == "" || !strings.EqualFold(u.Host, repo.Host) {
		return "", fmt.Errorf("SYSTEM_COLLECTIONURI host %q is not the repository URL host", u.Host)
	}

	return fmt.Sprintf("%s_apis/git/repositories/%s/pullRequests/%d",
		strings.TrimSuffix(collectionURI, "/")+"/", url.PathEscape(repoID), prNumber), nil
}

// azurePullRequest is the slice of the API response we read.
type azurePullRequest struct {
	Title     string `json:"title"`
	CreatedBy struct {
		UniqueName string `json:"uniqueName"`
	} `json:"createdBy"`
}

func fetchAzurePullRequest(ctx context.Context, apiURL, token string, tlsConfig *tls.Config) (azurePullRequest, error) {
	var pr azurePullRequest

	ctx, cancel := context.WithTimeout(ctx, azurePRTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return pr, err
	}
	// azdo is the conventional placeholder user for a pipeline access token.
	req.SetBasicAuth("azdo", token)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	client := &http.Client{Transport: transport}

	// azurePRURL pins the scheme to https and host-checks the collection URI
	// before this, which is what the taint analysis cannot see.
	res, err := client.Do(req) //nolint:gosec // G704
	if err != nil {
		return pr, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return pr, fmt.Errorf("%s returned %s", apiURL, res.Status)
	}

	if err := json.NewDecoder(io.LimitReader(res.Body, azurePRMaxBody)).Decode(&pr); err != nil {
		return pr, err
	}
	return pr, nil
}
