package config

import (
	"encoding/json"
	"testing"

	"github.com/infracost/ci/v2/internal/api/dashboard"
)

func TestParseRunParametersAIEnabled(t *testing.T) {
	for name, tc := range map[string]struct {
		featureFlags string
		want         bool
	}{
		"no feature flags": {"", false},
		"AI plugin off":    {`{"enableAiPlugin":false}`, false},
		"AI plugin on":     {`{"enableAiPlugin":true}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			parsed, err := ParseRunParameters(dashboard.RunParameters{FeatureFlags: json.RawMessage(tc.featureFlags)})
			if err != nil {
				t.Fatal(err)
			}
			if parsed.AIEnabled != tc.want {
				t.Errorf("AIEnabled = %v, want %v", parsed.AIEnabled, tc.want)
			}
		})
	}
}
