package events

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentCallerEnvNames are the agent variables getCaller prefers over the
// default. A developer machine running these tests inside an agent would
// otherwise see that agent's name instead of ci-v2.
var agentCallerEnvNames = []string{
	"INFRACOST_CI_CALLER",
	"CLAUDE_CODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDECODE",
	"CODEX_CLI_INVOKED_BY", "GEMINI_CLI", "AIDER",
	"CONTINUE_GLOBAL_DIR", "CLINE_TASK_ID", "CURSOR_TRACE_ID",
	"TERM_PROGRAM",
}

func clearCallerEnv(t *testing.T) {
	t.Helper()

	for _, k := range agentCallerEnvNames {
		if v, ok := os.LookupEnv(k); ok {
			t.Setenv(k, v)
			require.NoError(t, os.Unsetenv(k))
		}
	}
}

// TestGetCaller_DefaultsToCIV2 pins the default. Until 9efbf3f (Oct 2026) this
// returned "", which is why a month of scanner runs reached Mixpanel carrying
// vcsProvider but no caller and were mistaken for an untagged third product.
// Every event this binary sends must name itself.
func TestGetCaller_DefaultsToCIV2(t *testing.T) {
	clearCallerEnv(t)

	assert.Equal(t, "ci-v2", getCaller())
}

func TestGetCaller_PrefersExplicitAndAgents(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "explicit caller wins", env: map[string]string{"INFRACOST_CI_CALLER": "atlantis"}, want: "atlantis"},
		{name: "explicit empty caller is honoured", env: map[string]string{"INFRACOST_CI_CALLER": ""}, want: ""},
		{name: "agent is named", env: map[string]string{"CLAUDECODE": "1"}, want: "claude-code"},
		{name: "ide terminal is named", env: map[string]string{"TERM_PROGRAM": "cursor"}, want: "cursor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCallerEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			assert.Equal(t, tt.want, getCaller())
		})
	}
}

// TestMetadataAlwaysNamesTheProduct guards the three properties the activation
// funnels group by. vcsProvider is deliberately present and empty until a
// command resolves it.
func TestMetadataAlwaysNamesTheProduct(t *testing.T) {
	snapshot := Snapshot()

	assert.Contains(t, snapshot, "caller")
	assert.Contains(t, snapshot, "version")
	assert.Contains(t, snapshot, "vcsProvider")
}
