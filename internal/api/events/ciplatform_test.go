package events

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ciEnvVars is every exact variable name getCIPlatform looks up.
var ciEnvVars = append(
	[]string{"INFRACOST_CI_PLATFORM", "SYSTEM_COLLECTIONURI", "BUILD_REPOSITORY_PROVIDER", "CI"},
	exactCIEnvNames()...,
)

func exactCIEnvNames() []string {
	names := make([]string, 0, len(ciEnvPlatforms))
	for k := range ciEnvPlatforms {
		names = append(names, k)
	}
	return names
}

// clearCIEnv unsets every CI variable for the duration of the test, including
// anything matching the prefix scan. getCIPlatform ranges over a Go map, so a
// variable left set by the CI running these tests would otherwise win at
// random. t.Setenv cannot unset, but it does register the restore, so pair it
// with os.Unsetenv.
func clearCIEnv(t *testing.T) {
	t.Helper()

	unset := func(k string) {
		if v, ok := os.LookupEnv(k); ok {
			t.Setenv(k, v)
			require.NoError(t, os.Unsetenv(k))
		}
	}

	for _, k := range ciEnvVars {
		unset(k)
	}
	for _, entry := range os.Environ() {
		if key, _, _ := strings.Cut(entry, "="); hasCIEnvPrefix(key) {
			unset(key)
		}
	}
}

func hasCIEnvPrefix(key string) bool {
	for prefix := range ciEnvPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func TestCIPlatform(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		expected string
	}{
		{name: "nothing recognised", expected: ""},
		{name: "explicit override", env: map[string]string{"INFRACOST_CI_PLATFORM": "custom"}, expected: "custom"},
		{name: "github actions", env: map[string]string{"GITHUB_ACTIONS": "true"}, expected: "github_actions"},
		{name: "gitlab ci", env: map[string]string{"GITLAB_CI": "true"}, expected: "gitlab_ci"},
		{name: "bitbucket by prefix", env: map[string]string{"BITBUCKET_BUILD_NUMBER": "12"}, expected: "bitbucket"},
		{
			name:     "azure devops without repository provider",
			env:      map[string]string{"SYSTEM_COLLECTIONURI": "https://dev.azure.com/acme/"},
			expected: "azure_devops_",
		},
		{
			name:     "azure devops with repository provider",
			env:      map[string]string{"SYSTEM_COLLECTIONURI": "https://dev.azure.com/acme/", "BUILD_REPOSITORY_PROVIDER": "GitHub"},
			expected: "azure_devops_GitHub",
		},
		{name: "jenkins url", env: map[string]string{"JENKINS_URL": "https://jenkins.acme.com/"}, expected: "jenkins"},
		{name: "jenkins node cookie", env: map[string]string{"JENKINS_NODE_COOKIE": "abc123"}, expected: "jenkins"},
		{name: "jenkins home on the controller", env: map[string]string{"JENKINS_HOME": "/var/jenkins_home"}, expected: "jenkins"},
		{
			// Jenkins core sets no CI variable, but a job or image may; the
			// fallthrough would then return it verbatim.
			name:     "jenkins outranks the CI fallthrough",
			env:      map[string]string{"CI": "true", "JENKINS_URL": "https://jenkins.acme.com/"},
			expected: "jenkins",
		},
		{name: "CI passes through a boolean", env: map[string]string{"CI": "true"}, expected: "true"},
		{name: "CI passes through a platform name", env: map[string]string{"CI": "woodpecker"}, expected: "woodpecker"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCIEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			assert.Equal(t, tt.expected, CIPlatform())
		})
	}
}

func TestNormalizeCIPlatform(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{name: "nothing detected", expected: ""},
		{name: "azure devops without repository provider", raw: "azure_devops_", expected: "unknown"},
		{name: "CI=true", raw: "true", expected: "unknown"},
		{name: "CI=1", raw: "1", expected: "unknown"},
		{name: "CI=yes", raw: "yes", expected: "unknown"},
		{name: "CI=false", raw: "false", expected: "unknown"},
		{name: "CI=TRUE", raw: "TRUE", expected: "unknown"},
		{name: "CI=on", raw: "on", expected: "unknown"},
		{name: "github actions", raw: "github_actions", expected: "github_actions"},
		{name: "gitlab ci", raw: "gitlab_ci", expected: "gitlab_ci"},
		{name: "CI names a platform", raw: "woodpecker", expected: "woodpecker"},
		{name: "azure suffix is lowercased", raw: "azure_devops_GitHub", expected: "azure_devops_github"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeCIPlatform(tt.raw))
		})
	}
}

func TestLookupCIPlatformOverride(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		set      bool
		expected string
		ok       bool
	}{
		{name: "present", value: "on", set: true, expected: "on", ok: true},
		{name: "empty", set: true},
		{name: "unset"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCIEnv(t)
			if tt.set {
				t.Setenv("INFRACOST_CI_PLATFORM", tt.value)
			}
			platform, ok := lookupCIPlatformOverride()
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.expected, platform)
		})
	}
}

func TestNormalizedCIPlatform(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		expected    string
		expectedRun string
	}{
		{name: "override present", env: map[string]string{"INFRACOST_CI_PLATFORM": "Azure_DevOps_GitHub"}, expected: "azure_devops_github", expectedRun: "azure_devops_github"},
		{name: "boolean-like override kept", env: map[string]string{"INFRACOST_CI_PLATFORM": "TrUe"}, expected: "true", expectedRun: "true"},
		// An explicitly empty override suppresses detection entirely.
		{name: "override empty", env: map[string]string{"INFRACOST_CI_PLATFORM": "", "CI": "true"}, expected: "", expectedRun: "unknown"},
		{name: "CI=true is unknown", env: map[string]string{"CI": "true"}, expected: "unknown", expectedRun: "unknown"},
		{name: "nothing detected", expected: "", expectedRun: "unknown"},
		{name: "github actions", env: map[string]string{"GITHUB_ACTIONS": "true"}, expected: "github_actions", expectedRun: "github_actions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCIEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			assert.Equal(t, tt.expected, NormalizedCIPlatform())
			assert.Equal(t, tt.expectedRun, RunMetadataCIPlatform())
		})
	}
}
