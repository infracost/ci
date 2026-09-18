package commands

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/infracost/ci/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBitbucketPRURL(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		repo    string
		server  string
		want    string
		wantErr string
	}{
		{name: "cloud", repoURL: "https://bitbucket.org/acme/infra",
			want: "https://api.bitbucket.org/2.0/repositories/acme/infra/pullrequests/7"},
		{name: "cloud trailing slash", repoURL: "https://bitbucket.org/acme/infra/",
			want: "https://api.bitbucket.org/2.0/repositories/acme/infra/pullrequests/7"},
		{name: "data center", repoURL: "https://bb.corp/projects/ACME/repos/infra",
			want: "https://bb.corp/rest/api/1.0/projects/ACME/repos/infra/pull-requests/7"},
		{name: "data center under a context path", repoURL: "https://bb.corp/stash/projects/ACME/repos/infra",
			want: "https://bb.corp/stash/rest/api/1.0/projects/ACME/repos/infra/pull-requests/7"},
		{name: "overrides win", repoURL: "https://bitbucket.org/acme/infra",
			repo: "OTHER/repo", server: "https://bb.corp",
			want: "https://bb.corp/rest/api/1.0/projects/OTHER/repos/repo/pull-requests/7"},
		// A "?" in a segment would otherwise re-target the request.
		{name: "segments are escaped", repoURL: "https://bitbucket.org/acme/infra", repo: "a b/c?d", server: "https://bb.corp",
			want: "https://bb.corp/rest/api/1.0/projects/a%20b/repos/c%3Fd/pull-requests/7"},
		{name: "unparseable repo URL", repoURL: "https://bitbucket.org/acme",
			wantErr: "cannot derive the Bitbucket repository"},
		// The token goes in this request.
		{name: "plain http server", repoURL: "https://bb.corp/projects/ACME/repos/infra",
			repo: "ACME/infra", server: "http://bb.corp", wantErr: "is not https"},
		{name: "another vendor's host", repoURL: "https://bb.corp/projects/ACME/repos/infra",
			repo: "ACME/infra", server: "https://github.com", wantErr: "is a github host"},
		// Trimming "/" to "" would otherwise send the Data Center token to Cloud.
		{name: "server override of a bare slash", repoURL: "https://bb.corp/projects/ACME/repos/infra",
			repo: "ACME/infra", server: "/", wantErr: "is not a URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := bitbucketPRURL(tt.repoURL, tt.repo, tt.server, 7)
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

func TestFillBitbucketPullRequest(t *testing.T) {
	// Server's shape. Cloud's is covered by TestBitbucketAuthorName: its API
	// host is fixed, so no test server can stand in for it.
	const body = `{"title":"Add a bucket","author":{"user":{"name":"owen","displayName":"Owen Rumney"}}}`

	tests := []struct {
		name       string
		vcsCtx     diffContext
		token      string
		status     int
		wantCalled bool
		wantAuth   string
		wantTitle  string
		wantAuthor string
	}{
		{
			name:       "fills both",
			vcsCtx:     diffContext{provider: "bitbucket", prNumber: 7},
			token:      "repo-access-token",
			status:     http.StatusOK,
			wantCalled: true,
			wantAuth:   "Bearer repo-access-token",
			wantTitle:  "Add a bucket",
			wantAuthor: "owen",
		},
		{
			// user:app-password reaches the server as Basic.
			name:       "app password",
			vcsCtx:     diffContext{provider: "bitbucket", prNumber: 7},
			token:      "owen:app-password",
			status:     http.StatusOK,
			wantCalled: true,
			wantAuth:   "Basic " + base64.StdEncoding.EncodeToString([]byte("owen:app-password")),
			wantTitle:  "Add a bucket",
			wantAuthor: "owen",
		},
		{
			name:       "explicit title is kept",
			vcsCtx:     diffContext{provider: "bitbucket", prNumber: 7, prTitle: "Explicit"},
			token:      "repo-access-token",
			status:     http.StatusOK,
			wantCalled: true,
			wantTitle:  "Explicit",
			wantAuthor: "owen",
		},
		{
			name:       "explicit author is kept",
			vcsCtx:     diffContext{provider: "bitbucket", prNumber: 7, prAuthor: "explicit-author"},
			token:      "repo-access-token",
			status:     http.StatusOK,
			wantCalled: true,
			wantTitle:  "Add a bucket",
			wantAuthor: "explicit-author",
		},
		{
			name:   "nothing to fill",
			vcsCtx: diffContext{provider: "bitbucket", prNumber: 7, prTitle: "Explicit", prAuthor: "owen"},
			token:  "repo-access-token", status: http.StatusOK,
			wantTitle: "Explicit", wantAuthor: "owen",
		},
		{
			name:   "another provider",
			vcsCtx: diffContext{provider: "github", prNumber: 7},
			token:  "repo-access-token", status: http.StatusOK,
		},
		{
			// A branches: pipeline sets no BITBUCKET_PR_ID.
			name:   "no pull request",
			vcsCtx: diffContext{provider: "bitbucket"},
			token:  "repo-access-token", status: http.StatusOK,
		},
		{
			name:   "no token",
			vcsCtx: diffContext{provider: "bitbucket", prNumber: 7},
			status: http.StatusOK,
		},
		{
			name:       "api failure leaves the gaps",
			vcsCtx:     diffContext{provider: "bitbucket", prNumber: 7},
			token:      "repo-access-token",
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
			// reaches it through the CA path a private Bitbucket would use.
			cfg.TLSCACertFile = writeServerCA(t, srv)

			vcsCtx := tt.vcsCtx
			vcsCtx.repoURL = srv.URL + "/projects/ACME/repos/infra"

			args := &diffArgs{bitbucketToken: tt.token}
			fillBitbucketPullRequest(context.Background(), cfg, args, &vcsCtx)

			assert.Equal(t, tt.wantCalled, called, "API call")
			assert.Equal(t, tt.wantTitle, vcsCtx.prTitle)
			assert.Equal(t, tt.wantAuthor, vcsCtx.prAuthor)
			if tt.wantAuth != "" {
				assert.Equal(t, tt.wantAuth, gotAuth)
				assert.Equal(t, "/rest/api/1.0/projects/ACME/repos/infra/pull-requests/7", gotPath)
			}
		})
	}
}

// The two flavours disagree on where the author's handle lives.
func TestBitbucketAuthorName(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "cloud prefers the nickname", want: "owen",
			body: `{"author":{"nickname":"owen","display_name":"Owen Rumney"}}`},
		{name: "cloud display name only", want: "Owen Rumney",
			body: `{"author":{"display_name":"Owen Rumney"}}`},
		{name: "server name", want: "owen",
			body: `{"author":{"user":{"name":"owen","displayName":"Owen Rumney"}}}`},
		{name: "server display name only", want: "Owen Rumney",
			body: `{"author":{"user":{"displayName":"Owen Rumney"}}}`},
		{name: "no author", want: "", body: `{"title":"Add a bucket"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pr bitbucketPullRequest
			require.NoError(t, json.Unmarshal([]byte(tt.body), &pr))
			assert.Equal(t, tt.want, pr.authorName())
		})
	}
}

// Go forwards the Authorization header to a same-host redirect target, so a
// redirect to http would put the token in clear.
func TestFillBitbucketPullRequestDoesNotFollowARedirect(t *testing.T) {
	var followed bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			followed = true
			_, _ = w.Write([]byte(`{"title":"Add a bucket","author":{"user":{"name":"owen"}}}`))
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer srv.Close()

	cfg := new(config.Config)
	cfg.TLSCACertFile = writeServerCA(t, srv)

	vcsCtx := diffContext{provider: "bitbucket", prNumber: 7, repoURL: srv.URL + "/projects/ACME/repos/infra"}
	args := &diffArgs{bitbucketToken: "repo-access-token"}
	fillBitbucketPullRequest(context.Background(), cfg, args, &vcsCtx)

	assert.False(t, followed, "redirect followed")
	assert.Empty(t, vcsCtx.prTitle)
	assert.Empty(t, vcsCtx.prAuthor)
}

// A lookup that cannot reach the server must not fail the run.
func TestFillBitbucketPullRequestSurvivesATransportFailure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	vcsCtx := diffContext{provider: "bitbucket", prNumber: 7, repoURL: srv.URL + "/projects/ACME/repos/infra"}
	args := &diffArgs{bitbucketToken: "repo-access-token"}
	fillBitbucketPullRequest(context.Background(), new(config.Config), args, &vcsCtx)

	assert.Empty(t, vcsCtx.prTitle)
	assert.Empty(t, vcsCtx.prAuthor)
}
