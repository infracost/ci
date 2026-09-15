package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/infracost/cli/pkg/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An empty cache directory keeps List() offline: it builds its manager with
// SkipInstall, so nothing here reaches the release host. Returns the directory
// so a test can prove the command read it rather than the user's real cache,
// which is also empty on a fresh machine and would pass either way.
func emptyPluginCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("INFRACOST_CLI_PLUGIN_CACHE_DIRECTORY", dir)
	return dir
}

func TestPluginsList_JSONReportsEveryRequiredPlugin(t *testing.T) {
	dir := emptyPluginCache(t)

	out, err := execPlugins(t, "list", "--format", "json")
	require.Error(t, err, "an uninstalled required plugin must fail the command")
	assert.Contains(t, err.Error(), "not installed")

	var items []pluginItem
	require.NoError(t, json.Unmarshal([]byte(out), &items))
	require.NotEmpty(t, items)

	for _, item := range items {
		assert.True(t, item.Required, "an empty cache directory holds no extra plugins")
		assert.False(t, item.Installed)
		assert.NotEmpty(t, item.Key)
		// The env var is hydrated by PreProcess, not by Config.Process, and
		// nothing else in the test asserts it took effect.
		assert.Equal(t, dir, filepath.Dir(item.Path))
	}
}

func TestPluginsList_InvalidFormat(t *testing.T) {
	emptyPluginCache(t)

	_, err := execPlugins(t, "list", "--format", "yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid format "yaml"`)
}

func TestCheckRequiredPlugins(t *testing.T) {
	item := func(name string, required, installed bool, version string) plugins.ListItem {
		return plugins.ListItem{
			Path:      filepath.Join("/opt/infracost/plugins", name),
			Required:  required,
			Installed: installed,
			Version:   version,
		}
	}

	tests := []struct {
		name    string
		items   []plugins.ListItem
		wantErr string
	}{
		{
			name:  "all required plugins healthy",
			items: []plugins.ListItem{item("infracost-parser-terraform", true, true, "1.2.3")},
		},
		{
			name:    "required plugin missing",
			items:   []plugins.ListItem{item("infracost-parser-terraform", true, false, "")},
			wantErr: "infracost-parser-terraform (not installed)",
		},
		{
			name:    "required plugin will not answer over gRPC",
			items:   []plugins.ListItem{item("infracost-provider-aws", true, true, "unknown")},
			wantErr: "infracost-provider-aws (did not report a version)",
		},
		{
			name:  "third-party plugin is not required to work",
			items: []plugins.ListItem{item("custom-parser", false, true, "unknown")},
		},
		{
			name: "every broken plugin is named",
			items: []plugins.ListItem{
				item("infracost-parser-terraform", true, false, ""),
				item("infracost-provider-aws", true, true, "unknown"),
				item("infracost-parser-arm", true, true, "1.0.0"),
			},
			wantErr: "infracost-parser-terraform (not installed), infracost-provider-aws (did not report a version)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkRequiredPlugins(tt.items)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Without parsers nothing identifies a project, and a zero-project directory
// has to fail rather than print an empty table.
//
// INFRACOST_CLI_PLUGIN_DIR, not the cache directory: detect calls EnsurePlugins,
// and only the developer-override directory sets SkipInstall. Pointed at the
// cache instead, this test would download the whole required set and then find
// the AWS fixture, passing for the wrong reason and needing a network.
func TestPluginsDetect_NoProjectsIsAFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("INFRACOST_CLI_PLUGIN_DIR", dir)

	_, err := execPlugins(t, "detect", "--path", filepath.Join(testdataDir(), "basic", "head"))
	require.Error(t, err)

	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	require.Empty(t, entries, "nothing may be downloaded into the override directory")
}

func TestPluginsDetect_PathIsRequired(t *testing.T) {
	emptyPluginCache(t)

	_, err := execPlugins(t, "detect")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}
