package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/infracost/ci/v2/internal/api/dashboard/graphql"
)

func TestSavePostedPrComment(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		response      string
		wantSaved     bool
		wantErrMsg    string
		wantRetryable bool
	}{
		{
			name:      "saved",
			status:    http.StatusOK,
			response:  `{"data":{"savePostedPrComment":true}}`,
			wantSaved: true,
		},
		{
			name:     "not saved",
			status:   http.StatusOK,
			response: `{"data":{"savePostedPrComment":false}}`,
		},
		{
			name:       "graphql errors",
			status:     http.StatusOK,
			response:   `{"errors":[{"message":"run not found"},{"message":"forbidden"}]}`,
			wantErrMsg: "run not found; forbidden",
		},
		{
			name:       "missing field",
			status:     http.StatusOK,
			response:   `{"data":{}}`,
			wantErrMsg: "savePostedPrComment missing from response",
		},
		{
			name:       "null data",
			status:     http.StatusOK,
			response:   `{"data":null}`,
			wantErrMsg: "savePostedPrComment missing from response",
		},
		{
			name:          "gateway error",
			status:        http.StatusBadGateway,
			response:      `{"message":"Internal server error"}`,
			wantErrMsg:    "dashboard returned 502 Bad Gateway",
			wantRetryable: true,
		},
		{
			// A proxy in front of the dashboard, so http.StatusText knows nothing.
			name:          "unknown 5xx",
			status:        520,
			response:      `{"message":"unknown error"}`,
			wantErrMsg:    "dashboard returned 520",
			wantRetryable: true,
		},
		{
			// A 403 is a rejected token; the body only names a cause when a
			// proxy supplied one, and "forbidden" repeats the status.
			name:       "forbidden",
			status:     http.StatusForbidden,
			response:   `{"message":"forbidden"}`,
			wantErrMsg: "the dashboard rejected the authentication token (403 Forbidden). If you are migrating from CI v0.1, CI v2 needs a CLI v2 token, not a v0.1 API key: " + graphql.CLITokenHint,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/graphql", r.URL.Path)

				body, err := io.ReadAll(r.Body)
				if !assert.NoError(t, err) {
					return
				}

				var request struct {
					Query     string            `json:"query"`
					Variables map[string]string `json:"variables"`
				}
				if !assert.NoError(t, json.Unmarshal(body, &request)) {
					return
				}

				assert.Contains(t, request.Query, "mutation SavePostedPrComment($runId: String!, $comment: String!)")
				assert.Contains(t, request.Query, "savePostedPrComment(runId: $runId, comment: $comment)")
				assert.Equal(t, map[string]string{"runId": "test-run-id", "comment": "comment body"}, request.Variables)

				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()

			c := newTestClient(server.URL)

			saved, err := c.SavePostedPrComment(context.Background(), "test-run-id", "comment body")

			if tt.wantErrMsg != "" {
				require.EqualError(t, err, tt.wantErrMsg)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantRetryable, Retryable(err))
			assert.Equal(t, tt.wantSaved, saved)
		})
	}
}

func TestSavePostedPrCommentValidation(t *testing.T) {
	tests := []struct {
		name       string
		runID      string
		comment    string
		wantErrMsg string
	}{
		{
			name:       "empty run id",
			comment:    "comment body",
			wantErrMsg: "runID is required",
		},
		{
			name:       "empty comment",
			runID:      "test-run-id",
			wantErrMsg: "comment is required",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		assert.Fail(t, "unexpected request to the dashboard")
	}))
	defer server.Close()

	c := newTestClient(server.URL)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saved, err := c.SavePostedPrComment(context.Background(), tt.runID, tt.comment)
			require.EqualError(t, err, tt.wantErrMsg)
			assert.False(t, saved)
		})
	}
}

func TestSavePostedPrCommentTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if !assert.NoError(t, err) {
			return
		}
		_ = conn.Close()
	}))
	defer server.Close()

	c := newTestClient(server.URL)

	saved, err := c.SavePostedPrComment(context.Background(), "test-run-id", "comment body")
	require.Error(t, err)
	assert.False(t, saved)
	// A dropped connection is what a restart or a failover in flight looks like.
	assert.True(t, Retryable(err))
}

// The response body is cut off mid-JSON, which the decoder reports as an EOF
// rather than a net.Error.
func TestSavePostedPrCommentTruncatedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"savePostedPrComment"`))

		conn, _, err := w.(http.Hijacker).Hijack()
		if !assert.NoError(t, err) {
			return
		}
		_ = conn.Close()
	}))
	defer server.Close()

	c := newTestClient(server.URL)

	saved, err := c.SavePostedPrComment(context.Background(), "test-run-id", "comment body")
	require.Error(t, err)
	assert.False(t, saved)
	assert.True(t, Retryable(err))
}

// A rejected token is the migrating v0.1 user's first run. The dashboard answers
// 200 with an UNAUTHENTICATED error, not a 401, so the status is not the signal.
func TestRunParametersRejectedToken(t *testing.T) {
	const unauthenticated = `{"errors":[{"message":"Unauthorized","extensions":{"code":"UNAUTHENTICATED","shouldReport":false}}],"data":null}`

	tests := []struct {
		name       string
		status     int
		response   string
		wantErrMsg string
	}{
		{
			name:       "200 with UNAUTHENTICATED names the CLI v2 token",
			status:     http.StatusOK,
			response:   unauthenticated,
			wantErrMsg: "the dashboard rejected the authentication token. If you are migrating from CI v0.1, CI v2 needs a CLI v2 token, not a v0.1 API key: " + graphql.CLITokenHint,
		},
		{
			// A real CLI v2 token, under-scoped. The migration hint would send its
			// owner round a loop they have already been round.
			name:       "missing scopes passes the dashboard's own text through",
			status:     http.StatusOK,
			response:   `{"errors":[{"message":"Unauthorized. Missing required scopes: runs:write","extensions":{"code":"UNAUTHENTICATED"}}]}`,
			wantErrMsg: "the dashboard rejected the authentication token: Unauthorized. Missing required scopes: runs:write",
		},
		{
			// The bare entry comes first; the one naming the scope must still win.
			name:       "a later entry naming the cause beats a bare Unauthorized",
			status:     http.StatusOK,
			response:   `{"errors":[{"message":"Unauthorized","extensions":{"code":"UNAUTHENTICATED"}},{"message":"Unauthorized. Missing required scopes: runs:write","extensions":{"code":"UNAUTHENTICATED"}}]}`,
			wantErrMsg: "the dashboard rejected the authentication token: Unauthorized. Missing required scopes: runs:write",
		},
		{
			// Not every UNAUTHENTICATED is a migration: the dashboard's own text
			// is the only thing that distinguishes revoked from never-valid.
			name:       "a revoked token keeps the dashboard's wording",
			status:     http.StatusOK,
			response:   `{"errors":[{"message":"This token has been revoked","extensions":{"code":"UNAUTHENTICATED"}}]}`,
			wantErrMsg: "the dashboard rejected the authentication token: This token has been revoked",
		},
		{
			name:       "an ordinary graphql error is unchanged",
			status:     http.StatusOK,
			response:   `{"errors":[{"message":"User has no associated organization"}]}`,
			wantErrMsg: "User has no associated organization",
		},
		{
			// A proxy in front of the dashboard, or a token of the wrong type.
			name:       "401 carries the status",
			status:     http.StatusUnauthorized,
			response:   `{"error":"Unauthorized"}`,
			wantErrMsg: "the dashboard rejected the authentication token (401 Unauthorized). If you are migrating from CI v0.1, CI v2 needs a CLI v2 token, not a v0.1 API key: " + graphql.CLITokenHint,
		},
		{
			name:       "403 carries the status",
			status:     http.StatusForbidden,
			response:   `{"error":"Forbidden"}`,
			wantErrMsg: "the dashboard rejected the authentication token (403 Forbidden). If you are migrating from CI v0.1, CI v2 needs a CLI v2 token, not a v0.1 API key: " + graphql.CLITokenHint,
		},
		{
			// Rotating the Infracost token would not fix this, so the hint must go.
			name:       "a proxy's own reason replaces the migration hint",
			status:     http.StatusForbidden,
			response:   `{"error":"proxy credentials expired"}`,
			wantErrMsg: "the dashboard rejected the authentication token (403 Forbidden): proxy credentials expired",
		},
		{
			name:       "a non-json proxy body is carried through",
			status:     http.StatusForbidden,
			response:   "blocked by egress policy\n",
			wantErrMsg: "the dashboard rejected the authentication token (403 Forbidden): blocked by egress policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()

			_, err := newTestClient(server.URL).RunParameters(context.Background(), "https://github.com/org/repo", "main")

			require.EqualError(t, err, tt.wantErrMsg)
			// Reissuing the token is the only thing that clears this.
			assert.False(t, Retryable(err))
		})
	}
}

// Every call the client makes goes through the same Query, so the mutations get
// the hint too — addRun is where an under-scoped token actually fails.
func TestAddRunRejectedToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"Unauthorized","extensions":{"code":"UNAUTHENTICATED"}}],"data":null}`))
	}))
	defer server.Close()

	_, err := newTestClient(server.URL).AddRun(context.Background(), RunInput{})

	var authErr *graphql.AuthError
	require.ErrorAs(t, err, &authErr)
	assert.Contains(t, err.Error(), "CI v2 needs a CLI v2 token")
}

func newTestClient(endpoint string) Client {
	cfg := &Config{Endpoint: endpoint}
	cfg.Process()
	return cfg.Client(http.DefaultClient)
}
