package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/infracost/ci/v2/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitHubPRURL(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		owner   string
		repo    string
		apiURL  string
		want    string
		wantErr string
	}{
		{name: "github.com", repoURL: "https://github.com/acme/infra",
			want: "https://api.github.com/repos/acme/infra/pulls/7"},
		{name: "trailing slash", repoURL: "https://github.com/acme/infra/",
			want: "https://api.github.com/repos/acme/infra/pulls/7"},
		{name: "enterprise server takes /api/v3", repoURL: "https://ghe.corp/acme/infra",
			want: "https://ghe.corp/api/v3/repos/acme/infra/pulls/7"},
		{name: "api url override", repoURL: "https://github.com/acme/infra", apiURL: "https://ghe.corp/",
			want: "https://ghe.corp/api/v3/repos/acme/infra/pulls/7"},
		{name: "owner and repo override the path", repoURL: "https://github.com/acme/infra",
			owner: "other", repo: "repo",
			want: "https://api.github.com/repos/other/repo/pulls/7"},
		{name: "http api url", repoURL: "https://github.com/acme/infra", apiURL: "http://ghe.corp",
			wantErr: "is not https"},
		{name: "api url is not a url", repoURL: "https://github.com/acme/infra", apiURL: "::",
			wantErr: "is not a URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gitHubPRURL(tt.repoURL, tt.owner, tt.repo, tt.apiURL, 7)
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

func TestFillGitHubPullRequest(t *testing.T) {
	const body = `{"title":"Raise the IOPS","user":{"login":"owen"}}`

	tests := []struct {
		name       string
		vcsCtx     diffContext
		token      string
		status     int
		inferred   []string
		cfgAuthor  string
		wantCalled bool
		wantTitle  string
		wantAuthor string
	}{
		{
			name:       "fills both",
			vcsCtx:     diffContext{provider: "github", prNumber: 7},
			token:      "gh-token",
			status:     http.StatusOK,
			wantCalled: true,
			wantTitle:  "Raise the IOPS",
			wantAuthor: "owen",
		},
		{
			// An Azure pipeline on a GitHub-backed repo infers the literal
			// "github" from BUILD_REQUESTEDFOR. The real login beats it.
			name:       "an inferred author is replaced",
			vcsCtx:     diffContext{provider: "github", prNumber: 7, prAuthor: "github"},
			token:      "gh-token",
			status:     http.StatusOK,
			inferred:   []string{config.FieldPullRequestAuthor},
			cfgAuthor:  "github",
			wantCalled: true,
			wantTitle:  "Raise the IOPS",
			wantAuthor: "owen",
		},
		{
			name:       "an explicit author is kept",
			vcsCtx:     diffContext{provider: "github", prNumber: 7, prAuthor: "explicit"},
			token:      "gh-token",
			status:     http.StatusOK,
			wantCalled: true,
			wantTitle:  "Raise the IOPS",
			wantAuthor: "explicit",
		},
		{
			name:   "nothing to fill",
			vcsCtx: diffContext{provider: "github", prNumber: 7, prTitle: "Explicit", prAuthor: "explicit"},
			token:  "gh-token", status: http.StatusOK,
			wantTitle: "Explicit", wantAuthor: "explicit",
		},
		{
			name:   "another provider",
			vcsCtx: diffContext{provider: "gitlab", prNumber: 7},
			token:  "gh-token", status: http.StatusOK,
		},
		{
			name:   "no pull request",
			vcsCtx: diffContext{provider: "github"},
			token:  "gh-token", status: http.StatusOK,
		},
		{
			name:   "no token",
			vcsCtx: diffContext{provider: "github", prNumber: 7},
			status: http.StatusOK,
		},
		{
			name:       "api failure leaves the gaps",
			vcsCtx:     diffContext{provider: "github", prNumber: 7},
			token:      "gh-token",
			status:     http.StatusUnauthorized,
			wantCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called bool
			var gotAuth, gotPath string
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				gotAuth = r.Header.Get("Authorization")
				gotPath = r.URL.Path
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			cfg := new(config.Config)
			// The test server's certificate is self-signed, so the lookup only
			// reaches it through the CA path a GHES instance would use.
			cfg.TLSCACertFile = writeServerCA(t, srv)
			cfg.VCSInferences = config.Inferences{Platform: "test", Inferred: tt.inferred}
			cfg.VCS.PullRequestAuthor = tt.cfgAuthor

			vcsCtx := tt.vcsCtx
			vcsCtx.repoURL = srv.URL + "/acme/infra"

			fillGitHubPullRequest(context.Background(), cfg, &diffArgs{githubToken: tt.token}, &vcsCtx)

			assert.Equal(t, tt.wantCalled, called, "API call")
			assert.Equal(t, tt.wantTitle, vcsCtx.prTitle)
			assert.Equal(t, tt.wantAuthor, vcsCtx.prAuthor)
			if tt.wantCalled && tt.status == http.StatusOK {
				assert.Equal(t, "Bearer "+tt.token, gotAuth)
				assert.Equal(t, "/api/v3/repos/acme/infra/pulls/7", gotPath)
			}
		})
	}
}

// Go forwards the Authorization header to a same-host redirect target, so a
// redirect to http would put the token in clear.
func TestFillGitHubPullRequestDoesNotFollowARedirect(t *testing.T) {
	var followed bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			followed = true
			_, _ = w.Write([]byte(`{"title":"Raise the IOPS","user":{"login":"owen"}}`))
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer srv.Close()

	cfg := new(config.Config)
	cfg.TLSCACertFile = writeServerCA(t, srv)
	vcsCtx := diffContext{provider: "github", prNumber: 7, repoURL: srv.URL + "/acme/infra"}

	fillGitHubPullRequest(context.Background(), cfg, &diffArgs{githubToken: "gh-token"}, &vcsCtx)

	assert.False(t, followed, "the redirect must not be followed")
	assert.Empty(t, vcsCtx.prTitle)
}
