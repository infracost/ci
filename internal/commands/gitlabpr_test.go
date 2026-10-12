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

func TestGitLabMRURL(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		project string
		server  string
		want    string
		wantErr string
	}{
		{name: "gitlab.com", repoURL: "https://gitlab.com/acme/infra",
			want: "https://gitlab.com/api/v4/projects/acme%2Finfra/merge_requests/7"},
		{name: "trailing slash", repoURL: "https://gitlab.com/acme/infra/",
			want: "https://gitlab.com/api/v4/projects/acme%2Finfra/merge_requests/7"},
		// The whole path, not the last two segments: a truncated subgroup path
		// 404s silently.
		{name: "subgroup", repoURL: "https://gitlab.com/acme/platform/infra",
			want: "https://gitlab.com/api/v4/projects/acme%2Fplatform%2Finfra/merge_requests/7"},
		{name: "self-managed", repoURL: "https://gitlab.corp/acme/infra",
			want: "https://gitlab.corp/api/v4/projects/acme%2Finfra/merge_requests/7"},
		// A GitLab served from a relative root cannot be derived from the URL.
		{name: "overrides", repoURL: "https://gitlab.corp/gitlab/acme/infra",
			project: "acme/infra", server: "https://gitlab.corp/gitlab",
			want: "https://gitlab.corp/gitlab/api/v4/projects/acme%2Finfra/merge_requests/7"},
		{name: "http server", repoURL: "https://gitlab.com/acme/infra", server: "http://gitlab.corp",
			project: "acme/infra", wantErr: "is not https"},
		{name: "one path segment", repoURL: "https://gitlab.com/infra",
			wantErr: "cannot derive the GitLab project"},
		// A path segment, not a query parameter: a space is %20, never "+".
		{name: "space in the project override", repoURL: "https://gitlab.com/acme/infra",
			project: "acme/my project", server: "https://gitlab.com",
			want: "https://gitlab.com/api/v4/projects/acme%2Fmy%20project/merge_requests/7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gitLabMRURL(tt.repoURL, tt.project, tt.server, 7)
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

func TestFillGitLabPullRequest(t *testing.T) {
	const body = `{"title":"Raise the IOPS","author":{"username":"owen"}}`

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
			vcsCtx:     diffContext{provider: "gitlab", prNumber: 7},
			token:      "glpat-token",
			status:     http.StatusOK,
			wantCalled: true,
			wantTitle:  "Raise the IOPS",
			wantAuthor: "owen",
		},
		{
			// Inference fills the author from CI_COMMIT_AUTHOR, which is the
			// commit's author, not the merge request's. The API wins.
			name:       "an inferred commit author is replaced",
			vcsCtx:     diffContext{provider: "gitlab", prNumber: 7, prTitle: "Raise the IOPS", prAuthor: "Someone Else"},
			token:      "glpat-token",
			status:     http.StatusOK,
			inferred:   []string{config.FieldPullRequestAuthor},
			cfgAuthor:  "Someone Else",
			wantCalled: true,
			wantTitle:  "Raise the IOPS",
			wantAuthor: "owen",
		},
		{
			name:       "an explicit author is kept",
			vcsCtx:     diffContext{provider: "gitlab", prNumber: 7, prAuthor: "explicit"},
			token:      "glpat-token",
			status:     http.StatusOK,
			wantCalled: true,
			wantTitle:  "Raise the IOPS",
			wantAuthor: "explicit",
		},
		{
			name:   "nothing to fill",
			vcsCtx: diffContext{provider: "gitlab", prNumber: 7, prTitle: "Explicit", prAuthor: "explicit"},
			token:  "glpat-token", status: http.StatusOK,
			wantTitle: "Explicit", wantAuthor: "explicit",
		},
		{
			name:   "another provider",
			vcsCtx: diffContext{provider: "github", prNumber: 7},
			token:  "glpat-token", status: http.StatusOK,
		},
		{
			// A branch pipeline sets no CI_MERGE_REQUEST_IID.
			name:   "no merge request",
			vcsCtx: diffContext{provider: "gitlab"},
			token:  "glpat-token", status: http.StatusOK,
		},
		{
			name:   "no token",
			vcsCtx: diffContext{provider: "gitlab", prNumber: 7},
			status: http.StatusOK,
		},
		{
			name:       "api failure leaves the gaps",
			vcsCtx:     diffContext{provider: "gitlab", prNumber: 7},
			token:      "glpat-token",
			status:     http.StatusUnauthorized,
			wantCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called bool
			var gotToken, gotPath string
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				gotToken = r.Header.Get("PRIVATE-TOKEN")
				// EscapedPath, not Path: Path decodes %2F back to "/", so it
				// would pass with the project path unescaped.
				gotPath = r.URL.EscapedPath()
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			cfg := new(config.Config)
			// The test server's certificate is self-signed, so the lookup only
			// reaches it through the CA path a self-managed GitLab would use.
			cfg.TLSCACertFile = writeServerCA(t, srv)
			cfg.VCSInferences = config.Inferences{Platform: "test", Inferred: tt.inferred}
			cfg.VCS.PullRequestAuthor = tt.cfgAuthor

			vcsCtx := tt.vcsCtx
			vcsCtx.repoURL = srv.URL + "/acme/infra"

			fillGitLabPullRequest(context.Background(), cfg, &diffArgs{gitlabToken: tt.token}, &vcsCtx)

			assert.Equal(t, tt.wantCalled, called, "API call")
			assert.Equal(t, tt.wantTitle, vcsCtx.prTitle)
			assert.Equal(t, tt.wantAuthor, vcsCtx.prAuthor)
			if tt.wantCalled && tt.status == http.StatusOK {
				assert.Equal(t, tt.token, gotToken)
				assert.Equal(t, "/api/v4/projects/acme%2Finfra/merge_requests/7", gotPath)
			}
		})
	}
}

// Go forwards the token header to a same-host redirect target, so a redirect
// to http would put it in clear.
func TestFillGitLabPullRequestDoesNotFollowARedirect(t *testing.T) {
	var followed bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			followed = true
			_, _ = w.Write([]byte(`{"title":"Raise the IOPS","author":{"username":"owen"}}`))
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer srv.Close()

	cfg := new(config.Config)
	cfg.TLSCACertFile = writeServerCA(t, srv)
	vcsCtx := diffContext{provider: "gitlab", prNumber: 7, repoURL: srv.URL + "/acme/infra"}

	fillGitLabPullRequest(context.Background(), cfg, &diffArgs{gitlabToken: "glpat-token"}, &vcsCtx)

	assert.False(t, followed, "the redirect must not be followed")
	assert.Empty(t, vcsCtx.prTitle)
}
