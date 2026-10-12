package commands

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// prTimeout bounds the lookup: a cost comment must not wait on metadata.
const prTimeout = 10 * time.Second

// prMaxBody bounds a pull request response: two fields are read from each.
const prMaxBody = 1 << 20

// fetchPRJSON issues the one GET a title-and-author lookup needs, with the
// redirect and body limits every provider's lookup wants. setAuth differs per
// provider; nothing else does.
func fetchPRJSON(ctx context.Context, apiURL string, tlsConfig *tls.Config, setAuth func(*http.Request), out any) error {
	ctx, cancel := context.WithTimeout(ctx, prTimeout)
	defer cancel()

	// Every caller pins the scheme to https and host-checks a self-managed URL
	// before this, which is what the taint analysis cannot see.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil) //nolint:gosec // G704
	if err != nil {
		return err
	}
	setAuth(req)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	// Do not follow redirects: Go forwards the Authorization header to a
	// same-host target, and a redirect to http would put the token in clear.
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	res, err := client.Do(req) //nolint:gosec // G704
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", apiURL, res.Status)
	}

	return json.NewDecoder(io.LimitReader(res.Body, prMaxBody)).Decode(out)
}
