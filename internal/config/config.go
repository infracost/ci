package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/infracost/ci/internal/api/dashboard"
	"github.com/infracost/ci/internal/api/events"
	"github.com/infracost/cli/pkg/auth"
	"github.com/infracost/cli/pkg/config/process"
	"github.com/infracost/cli/pkg/environment"
	"github.com/infracost/cli/pkg/logging"
	"github.com/infracost/cli/pkg/plugins"
)

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
	logging.Debugf("%s", config.VCSInferences)
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
