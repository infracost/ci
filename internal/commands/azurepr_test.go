package commands

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infracost/ci/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAzurePRURL(t *testing.T) {
	const azureRepo = "https://dev.azure.com/acme/proj/_git/repo"

	tests := []struct {
		name          string
		collectionURI string
		repoURL       string
		repoID        string
		want          string
		wantErr       string
	}{
		{name: "trailing slash", collectionURI: "https://dev.azure.com/acme/", repoURL: azureRepo, repoID: "repo-guid",
			want: "https://dev.azure.com/acme/_apis/git/repositories/repo-guid/pullRequests/7?api-version=6.0"},
		{name: "no trailing slash", collectionURI: "https://dev.azure.com/acme", repoURL: azureRepo, repoID: "repo-guid",
			want: "https://dev.azure.com/acme/_apis/git/repositories/repo-guid/pullRequests/7?api-version=6.0"},
		{name: "on-premise server", collectionURI: "https://tfs.corp/tfs/DefaultCollection/",
			repoURL: "https://tfs.corp/tfs/DefaultCollection/proj/_git/repo", repoID: "repo-guid",
			want: "https://tfs.corp/tfs/DefaultCollection/_apis/git/repositories/repo-guid/pullRequests/7?api-version=6.0"},
		// The org@ userinfo is the web URL Azure hands out.
		{name: "repository URL userinfo", collectionURI: "https://dev.azure.com/acme/",
			repoURL: "https://acme@dev.azure.com/acme/proj/_git/repo", repoID: "repo-guid",
			want: "https://dev.azure.com/acme/_apis/git/repositories/repo-guid/pullRequests/7?api-version=6.0"},
		{name: "repo id is escaped", collectionURI: "https://dev.azure.com/acme/", repoURL: azureRepo, repoID: "a b?c",
			want: "https://dev.azure.com/acme/_apis/git/repositories/a%20b%3Fc/pullRequests/7?api-version=6.0"},
		// A query on the collection URI must not swallow the path or api-version.
		{name: "collection URI query", collectionURI: "https://dev.azure.com/acme/?traceparent=x", repoURL: azureRepo,
			repoID: "repo-guid",
			want:   "https://dev.azure.com/acme/_apis/git/repositories/repo-guid/pullRequests/7?api-version=6.0"},
		{name: "missing repo id", collectionURI: "https://dev.azure.com/acme/", repoURL: azureRepo,
			wantErr: "not both set"},
		{name: "missing collection", repoURL: azureRepo, repoID: "repo-guid", wantErr: "not both set"},
		// The access token goes in this request.
		{name: "plain http", collectionURI: "http://tfs.corp/tfs/", repoURL: "https://tfs.corp/tfs/proj/_git/repo",
			repoID: "repo-guid", wantErr: "is not https"},
		{name: "another vendor's host", collectionURI: "https://github.com/acme/", repoURL: "https://github.com/acme/repo",
			repoID: "repo-guid", wantErr: "is a github host"},
		// An unrecognised host passes CheckProviderHost, so the repository URL
		// is what keeps the token off it.
		{name: "host the repository URL does not name", collectionURI: "https://dev.azure.com.evil.example/acme/",
			repoURL: azureRepo, repoID: "repo-guid", wantErr: "is not the repository URL host"},
		{name: "no repository URL", collectionURI: "https://dev.azure.com/acme/", repoID: "repo-guid",
			wantErr: "is not the repository URL host"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := azurePRURL(tt.collectionURI, tt.repoURL, tt.repoID, 7)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFillAzurePullRequest(t *testing.T) {
	const body = `{"title":"Add a bucket","createdBy":{"uniqueName":"owen@infracost.io"}}`

	tests := []struct {
		name string
		// inferredAuthor makes vcsCtx.prAuthor the value InferVCS filled from
		// BUILD_REQUESTEDFOR, rather than one the user set.
		inferredAuthor bool
		vcsCtx         diffContext
		token          string
		status         int
		wantCalled     bool
		wantAuth       string
		wantTitle      string
		wantAuthor     string
	}{
		{
			name:       "fills both",
			vcsCtx:     diffContext{provider: "azure_repos", prNumber: 7},
			token:      "system-access-token",
			status:     http.StatusOK,
			wantCalled: true,
			// System.AccessToken is an OAuth token, which Azure rejects under Basic.
			wantAuth:   "Bearer system-access-token",
			wantTitle:  "Add a bucket",
			wantAuthor: "owen@infracost.io",
		},
		{
			// A PAT reaches the server as Basic, the other half of setAzureAuth.
			name:       "personal access token",
			vcsCtx:     diffContext{provider: "azure_repos", prNumber: 7},
			token:      strings.Repeat("p", 52),
			status:     http.StatusOK,
			wantCalled: true,
			wantAuth:   "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+strings.Repeat("p", 52))),
			wantTitle:  "Add a bucket",
			wantAuthor: "owen@infracost.io",
		},
		{
			// The handle beats the display name BUILD_REQUESTEDFOR inferred.
			name:           "handle replaces the inferred display name",
			inferredAuthor: true,
			vcsCtx:         diffContext{provider: "azure_repos", prNumber: 7, prAuthor: "Owen Rumney"},
			token:          "system-access-token",
			status:         http.StatusOK,
			wantCalled:     true,
			wantTitle:      "Add a bucket",
			wantAuthor:     "owen@infracost.io",
		},
		{
			// An INFRACOST_VCS_PULL_REQUEST_AUTHOR or --pr-author wins over both.
			name:       "explicit author is kept",
			vcsCtx:     diffContext{provider: "azure_repos", prNumber: 7, prAuthor: "explicit-author"},
			token:      "system-access-token",
			status:     http.StatusOK,
			wantCalled: true,
			wantTitle:  "Add a bucket",
			wantAuthor: "explicit-author",
		},
		{
			name:       "explicit title is kept",
			vcsCtx:     diffContext{provider: "azure_repos", prNumber: 7, prTitle: "Explicit"},
			token:      "system-access-token",
			status:     http.StatusOK,
			wantCalled: true,
			wantTitle:  "Explicit",
			wantAuthor: "owen@infracost.io",
		},
		{
			name:   "nothing to fill",
			vcsCtx: diffContext{provider: "azure_repos", prNumber: 7, prTitle: "Explicit", prAuthor: "owen"},
			token:  "system-access-token", status: http.StatusOK,
			wantTitle: "Explicit", wantAuthor: "owen",
		},
		{
			name:   "another provider",
			vcsCtx: diffContext{provider: "github", prNumber: 7},
			token:  "system-access-token", status: http.StatusOK,
		},
		{
			name:   "no pull request",
			vcsCtx: diffContext{provider: "azure_repos"},
			token:  "system-access-token", status: http.StatusOK,
		},
		{
			// The common path: the job mapped no Azure token at all.
			name:   "no token",
			vcsCtx: diffContext{provider: "azure_repos", prNumber: 7},
			status: http.StatusOK,
		},
		{
			name:           "api failure leaves the gaps",
			inferredAuthor: true,
			vcsCtx:         diffContext{provider: "azure_repos", prNumber: 7, prAuthor: "Owen Rumney"},
			token:          "system-access-token",
			status:         http.StatusUnauthorized,
			wantCalled:     true,
			wantAuthor:     "Owen Rumney",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called bool
			var gotAuth string
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			t.Setenv("SYSTEM_COLLECTIONURI", srv.URL+"/")
			t.Setenv("BUILD_REPOSITORY_ID", "repo-guid")

			cfg := new(config.Config)
			// The test server's certificate is self-signed, so the lookup only
			// reaches it through the CA path a private Azure server would use.
			cfg.TLSCACertFile = writeServerCA(t, srv)
			if tt.inferredAuthor {
				cfg.VCS.PullRequestAuthor = tt.vcsCtx.prAuthor
				cfg.VCSInferences = config.Inferences{Inferred: []string{config.FieldPullRequestAuthor}}
			}

			vcsCtx := tt.vcsCtx
			// The collection URI and the repository URL must name one host.
			vcsCtx.repoURL = srv.URL
			fillAzurePullRequest(context.Background(), cfg, tt.token, &vcsCtx)

			assert.Equal(t, tt.wantCalled, called, "API call")
			assert.Equal(t, tt.wantTitle, vcsCtx.prTitle)
			assert.Equal(t, tt.wantAuthor, vcsCtx.prAuthor)
			if tt.wantAuth != "" {
				assert.Equal(t, tt.wantAuth, gotAuth)
			}
		})
	}
}

// A lookup that cannot reach the server must not fail the run.
func TestFillAzurePullRequestSurvivesATransportFailure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	t.Setenv("SYSTEM_COLLECTIONURI", srv.URL+"/")
	t.Setenv("BUILD_REPOSITORY_ID", "repo-guid")

	vcsCtx := diffContext{provider: "azure_repos", prNumber: 7, prAuthor: "Owen Rumney", repoURL: srv.URL}
	fillAzurePullRequest(context.Background(), new(config.Config), "system-access-token", &vcsCtx)

	assert.Equal(t, "Owen Rumney", vcsCtx.prAuthor)
	assert.Empty(t, vcsCtx.prTitle)
}

// writeServerCA writes a test server's own certificate as a CA bundle, so the
// lookup trusts it the way a private Azure DevOps Server's CA is trusted.
func writeServerCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	require.NoError(t, os.WriteFile(path, pemBytes, 0o600))
	return path
}

// A PAT goes in Basic and an OAuth token in Bearer, the rule pkg/vcs/azure
// applies to the same credentials.
func TestSetAzureAuth(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{name: "personal access token", token: strings.Repeat("p", 52),
			want: "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+strings.Repeat("p", 52)))},
		{name: "system access token", token: "oauth-token", want: "Bearer oauth-token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "https://dev.azure.com/acme/", nil)
			require.NoError(t, err)

			setAzureAuth(req, tt.token)
			assert.Equal(t, tt.want, req.Header.Get("Authorization"))
		})
	}
}
