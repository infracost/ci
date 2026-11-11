package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode"

	"github.com/infracost/ci/v2/internal/api/dashboard"
	"github.com/infracost/ci/v2/internal/api/dashboard/graphql"
	"github.com/infracost/ci/v2/internal/api/events"
	"github.com/infracost/cli/pkg/auth"
	"github.com/infracost/cli/pkg/config/process"
	"github.com/infracost/cli/pkg/environment"
	"github.com/infracost/cli/pkg/logging"
	"github.com/infracost/cli/pkg/plugins"
)

// v01APIKeyPrefix is the prefix every v0.1 dashboard API key carries.
const v01APIKeyPrefix = "ico-"

// privateSourcesWarned makes warnPrivateSources say its piece once per run.
var privateSourcesWarned sync.Once

// Config holds the shared configuration used by all subcommands.
type Config struct {
	// Environment is the environment to target for operations / authentication (development or production). Defaults to
	// production.
	Environment environment.Environment `flag:"environment;hidden" usage:"The environment to use for authentication" default:"prod"`

	// PricingEndpoint is the endpoint to use for prices. Defaults to https://pricing.api.infracost.io.
	PricingEndpoint string `env:"INFRACOST_CI_PRICING_ENDPOINT" flag:"pricing-endpoint;hidden" usage:"The pricing endpoint to use for prices" default:"https://pricing.api.infracost.io"`

	// OrgID is the organization ID to use for authentication. Defaults to the value of the INFRACOST_ORG_ID environment variable.
	OrgID string `env:"INFRACOST_CI_ORG_ID" flag:"org-id;hidden" usage:"The organization ID to use for authentication"`

	// DisableDashboard disables uploading scan results to the Infracost dashboard.
	DisableDashboard bool `env:"INFRACOST_CI_DISABLE_DASHBOARD"`

	// TLSCACertFile is a PEM bundle trusted in addition to the system pool,
	// for a self-managed VCS server behind a private CA. VCS-scoped in its
	// name: the dashboard, pricing and events clients keep the system pool.
	TLSCACertFile string `env:"INFRACOST_CI_VCS_TLS_CA_CERT_FILE" flag:"vcs-tls-ca-cert-file" usage:"PEM CA bundle to trust when talking to the VCS server"`

	// TLSInsecureSkipVerify disables VCS certificate verification. A
	// man-in-the-middle switch on a request carrying a VCS token: TLSConfig
	// warns when it is on, and TLSCACertFile covers the legitimate case.
	TLSInsecureSkipVerify bool `env:"INFRACOST_CI_VCS_TLS_INSECURE_SKIP_VERIFY" flag:"vcs-tls-insecure-skip-verify" usage:"Skip VCS TLS certificate verification (prefer --vcs-tls-ca-cert-file)"`

	// PrivateSourceAllowlist is a comma- or whitespace-separated list of hosts, IP addresses and
	// CIDR ranges that module sources may be fetched from even though they are on
	// a private network (a self-hosted git server, say). The parser's SSRF guard
	// stays on for everything else, and loopback, link-local (the cloud metadata
	// address), multicast and the unspecified address are never allowed. It is read
	// only from the job's environment: never from infracost.yml, a config template
	// or the dashboard. Do not set it on a job that scans a fork's pull request
	// (pull_request_target included): the fork's module sources could then reach any
	// path on an allowlisted host with the job's git credentials.
	PrivateSourceAllowlist string `env:"INFRACOST_CI_PRIVATE_SOURCE_ALLOWLIST"`

	// VCSProvider is the VCS hosting the repository — github, gitlab, azure_repos
	// or bitbucket. Unprefixed on purpose: INFRACOST_VCS_PROVIDER is the v0.1
	// contract name, unlike the INFRACOST_CI_* fields above.
	VCSProvider string `env:"INFRACOST_VCS_PROVIDER" flag:"vcs-provider" usage:"VCS provider hosting the repository"`

	// VCS is the rest of the INFRACOST_VCS_* contract, hydrated from the
	// environment and then from the CI platform. See vcs.go for why none of it
	// carries a flag tag.
	VCS VCS

	// VCSInferences is what InferVCS filled and what the environment kept.
	// Held rather than logged at the time: inference runs before PreRun
	// configures logging.
	VCSInferences Inferences

	// JSON toggles JSON output for logs. Registered here so sub-configs that
	// bind via `flagvalue:"json"` (e.g. logging) have a flag to reference.
	// Must stay above Logging so it is registered before logging binds to it.
	JSON process.BoolFlag `env:"INFRACOST_CLI_LOG_JSON" flag:"json;hidden" usage:"Output logs as JSON"`

	// Debug enables debug logging. Shared with logging via `flagvalue:"debug"`.
	// Must stay above Logging so it is registered before logging binds to it.
	Debug process.BoolFlag `flag:"debug;hidden" usage:"Enable debug logging"`

	// Logging contains the configuration for logging.
	// keep logging above other structs, so it gets processed first and others can log in their process functions.
	Logging logging.Config

	// Dashboard contains the configuration for the dashboard API.
	Dashboard dashboard.Config

	// Events contains the configuration for the events API.
	Events events.Config

	// Auth contains the configuration for authenticating with Infracost.
	Auth auth.Config

	// Plugins contains the configuration for plugins.
	Plugins plugins.Config
}

func (config *Config) Process() {
	events.RegisterMetadata("cloudEnabled", os.Getenv("INFRACOST_ENABLE_CLOUD") == "true")
	events.RegisterMetadata("dashboardEnabled", !config.DisableDashboard)
	events.RegisterMetadata("environment", config.Environment.String())
	events.RegisterMetadata("isDefaultPricingApiEndpoint", config.PricingEndpoint == "https://pricing.api.infracost.io")
	events.RegisterMetadata("vcsInferred", config.VCSInferences.Inferred)
	events.RegisterMetadata("privateSourceAllowlistSet", len(config.PrivateSources()) > 0)
	logging.Debugf("%s", config.VCSInferences)
}

// warnPrivateSources says, once per run and where the list is used, that the
// SSRF guard is relaxed for sources.
func warnPrivateSources(sources []string) {
	privateSourcesWarned.Do(func() {
		logging.Warnf("INFRACOST_CI_PRIVATE_SOURCE_ALLOWLIST is set: modules may be fetched from %s even though they are on a private network", strings.Join(sources, ", "))
	})
}

// PrivateSources splits PrivateSourceAllowlist on commas and whitespace. Blank
// entries are dropped so a trailing comma or a multi-line CI variable does not
// become an entry.
func (config *Config) PrivateSources() []string {
	return strings.FieldsFunc(config.PrivateSourceAllowlist, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
}

// TLSConfig builds the TLS configuration the VCS clients use, or nil when
// neither variable is set — which is what their Options already expect.
func (config *Config) TLSConfig() (*tls.Config, error) {
	if config.TLSCACertFile == "" && !config.TLSInsecureSkipVerify {
		return nil, nil
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if config.TLSInsecureSkipVerify {
		logging.Warnf("INFRACOST_CI_VCS_TLS_INSECURE_SKIP_VERIFY is set: TLS certificates are not verified, and the VCS token is sent over an unauthenticated connection")
		cfg.InsecureSkipVerify = true //nolint:gosec // opt-in, and warned about above
	}

	if config.TLSCACertFile != "" {
		pem, err := os.ReadFile(config.TLSCACertFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read INFRACOST_CI_VCS_TLS_CA_CERT_FILE: %w", err)
		}
		// Appended to the system pool, not replacing it: a private CA is
		// usually additional to the public ones. Failing to load the system
		// pool is an error, not a silent substitution of the private CA.
		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("failed to load the system certificate pool: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in INFRACOST_CI_VCS_TLS_CA_CERT_FILE %q", config.TLSCACertFile)
		}
		cfg.RootCAs = pool
	}

	return cfg, nil
}

// RequireAuthToken rejects a missing token, and a v0.1 API key by its prefix.
// The prefix is certain where the API's "Unauthorized" is not — that is equally
// consistent with a revoked or expired CLI v2 token — so the message can name
// the problem outright.
//
// Negative check only: a positive one would reject an Auth0 JWT, which the
// dashboard still accepts on this header. Legacy keys predating the prefix fall
// through to the API, where graphql.AuthError handles them.
func (config *Config) RequireAuthToken() error {
	token := string(config.Auth.AuthenticationToken)

	if len(token) == 0 {
		return fmt.Errorf("authentication token is required: set INFRACOST_CLI_AUTHENTICATION_TOKEN")
	}

	// Azure passes $(NAME) through verbatim when no such pipeline variable
	// exists; without this the literal reaches the dashboard and reads as a
	// rejected token. Same guard as requireToken in diff.go.
	if strings.HasPrefix(token, "$(") {
		return fmt.Errorf("INFRACOST_CLI_AUTHENTICATION_TOKEN is an unexpanded pipeline variable (%q): define it in the pipeline, or set the token directly", token)
	}

	if token != strings.TrimSpace(token) {
		return fmt.Errorf("INFRACOST_CLI_AUTHENTICATION_TOKEN has leading or trailing whitespace, which the Authorization header rejects")
	}

	if strings.HasPrefix(token, v01APIKeyPrefix) {
		return fmt.Errorf("INFRACOST_CLI_AUTHENTICATION_TOKEN is a v0.1 API key (%s…), which CI v2 does not accept: %s",
			v01APIKeyPrefix, graphql.CLITokenHint)
	}

	return nil
}
