package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Response[T any] struct {
	Data   T       `json:"data"`
	Errors []Error `json:"errors,omitempty"`
}

type Error struct {
	Message string `json:"message"`
	// Extensions carries the GraphQL error code. A rejected token is a 200 with
	// an UNAUTHENTICATED entry, so this is the only place the status is.
	Extensions struct {
		Code string `json:"code"`
	} `json:"extensions"`
}

// StatusError reports a 5xx. Only 5xx: a 4xx body is a GraphQL response worth
// decoding, and retrying it would not change the answer.
type StatusError struct {
	Status int
}

// AuthError reports a rejected token. Named separately from StatusError because
// the only useful response is to issue a different one, and a v0.1 API key
// reaches here rather than failing at config time.
type AuthError struct {
	// Status is 0 when the dashboard answered 200 with an UNAUTHENTICATED error,
	// which is what a rejected token actually produces.
	Status int
	// Message is the dashboard's own text, when there was one.
	Message string
}

type Request struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

const (
	unauthenticatedCode = "UNAUTHENTICATED"

	// maxErrorBody caps what is quoted back from a 401/403 body, which comes
	// from whatever is in front of the dashboard and is not bounded.
	maxErrorBody = 1024

	// CLITokenHint names the screen that issues a CI v2 token. Exported so the
	// config-time check and this package give the same instruction. No host: the
	// dashboard endpoint is per-environment, and a prod URL misdirects a dev run.
	CLITokenHint = "in your Infracost dashboard, Organization settings → CLI tokens " +
		"→ Create CLI v2 tokens. Set it as INFRACOST_CLI_AUTHENTICATION_TOKEN"
)

func (e *StatusError) Error() string {
	// The code as well as the name: a proxy in front of the dashboard can
	// return a status http.StatusText does not know, such as 520.
	return strings.TrimSpace(fmt.Sprintf("dashboard returned %d %s", e.Status, http.StatusText(e.Status)))
}

func (e *AuthError) Error() string {
	cause := "the dashboard rejected the authentication token"
	if e.Status != 0 {
		cause = strings.TrimSpace(fmt.Sprintf("%s (%d %s)", cause, e.Status, http.StatusText(e.Status)))
	}

	// Only a bare "Unauthorized" leaves the cause open. Anything else — a missing
	// scope, a suspended org, a proxy's own refusal — names it better than the hint.
	if e.Message != "" && !bareRejection(e.Message) {
		return fmt.Sprintf("%s: %s", cause, e.Message)
	}

	return cause + ". If you are migrating from CI v0.1, CI v2 needs a CLI v2 token, not a " +
		"v0.1 API key: " + CLITokenHint
}

// bareRejection reports a message that says only that the token was rejected.
func bareRejection(msg string) bool {
	m := strings.ToLower(strings.TrimSpace(msg))
	m = strings.TrimSuffix(m, ".")
	return m == "" || m == "unauthorized" || m == "forbidden"
}

func Query[T any](ctx context.Context, client *http.Client, endpoint string, query string, variables map[string]interface{}) (Response[T], error) {
	request := Request{
		Query:     query,
		Variables: variables,
	}

	bytes := new(bytes.Buffer)
	if err := json.NewEncoder(bytes).Encode(request); err != nil {
		return Response[T]{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes)
	if err != nil {
		return Response[T]{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	r, err := client.Do(req) // #nosec G704 -- request target originates from config file
	if err != nil {
		return Response[T]{}, err
	}
	defer func() {
		_ = r.Body.Close()
	}()

	// The dashboard answers a rejected token with 200, so a status here comes from
	// a proxy in front of it, or from the dashboard for a token of the wrong type.
	// Either way its body is the only thing naming the cause, so it is carried.
	if r.StatusCode == http.StatusUnauthorized || r.StatusCode == http.StatusForbidden {
		return Response[T]{}, &AuthError{Status: r.StatusCode, Message: bodyMessage(r.Body)}
	}

	if r.StatusCode >= http.StatusInternalServerError {
		return Response[T]{}, &StatusError{Status: r.StatusCode}
	}

	var response Response[T]
	if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
		return Response[T]{}, err
	}

	if authErr := authError(response.Errors); authErr != nil {
		return Response[T]{}, authErr
	}

	return response, nil
}

// authError reports the UNAUTHENTICATED entry in a GraphQL error list, which is
// what every rejected token produces regardless of why it was rejected. The
// first entry that names a cause wins over an earlier bare "Unauthorized".
func authError(errs []Error) *AuthError {
	var first *AuthError
	for _, e := range errs {
		if e.Extensions.Code != unauthenticatedCode {
			continue
		}
		if !bareRejection(e.Message) {
			return &AuthError{Message: e.Message}
		}
		if first == nil {
			first = &AuthError{Message: e.Message}
		}
	}
	return first
}

// bodyMessage is the responder's own text for a status rejection: a proxy or
// gateway, which names a cause the dashboard's own error codes cannot.
func bodyMessage(body io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(body, maxErrorBody))
	if err != nil {
		return ""
	}

	// A JSON body is quoted only through its own message fields: the raw object
	// reads as noise, and may carry more than the reason.
	var decoded struct {
		Error   string  `json:"error"`
		Message string  `json:"message"`
		Errors  []Error `json:"errors"`
	}
	if json.Unmarshal(b, &decoded) == nil {
		switch {
		case decoded.Error != "":
			return decoded.Error
		case decoded.Message != "":
			return decoded.Message
		case len(decoded.Errors) > 0:
			return decoded.Errors[0].Message
		}
		return ""
	}

	return strings.TrimSpace(string(b))
}
