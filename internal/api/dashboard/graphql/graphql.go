package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Response[T any] struct {
	Data   T       `json:"data"`
	Errors []Error `json:"errors,omitempty"`
}

// StatusError reports a 5xx. Only 5xx: a 4xx body is a GraphQL response worth
// decoding, and retrying it would not change the answer.
type StatusError struct {
	Status int
}

func (e *StatusError) Error() string {
	// The code as well as the name: a proxy in front of the dashboard can
	// return a status http.StatusText does not know, such as 520.
	return strings.TrimSpace(fmt.Sprintf("dashboard returned %d %s", e.Status, http.StatusText(e.Status)))
}

type Error struct {
	Message string `json:"message"`
}

type Request struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
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

	if r.StatusCode >= http.StatusInternalServerError {
		return Response[T]{}, &StatusError{Status: r.StatusCode}
	}

	var response Response[T]
	if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
		return Response[T]{}, err
	}

	return response, nil
}
