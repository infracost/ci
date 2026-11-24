package config

import (
	"testing"

	"github.com/infracost/proto/gen/go/infracost/parser/event"
	"github.com/infracost/proto/gen/go/infracost/provider"
)

func TestSplitPolicyResults(t *testing.T) {
	results := []*provider.FinopsPolicyResult{
		{PolicySlug: "finops"},
		{PolicySlug: "security"},
		{PolicySlug: "ai"},
		{PolicySlug: "unknown"},
	}
	groups := map[string]event.FinopsPolicySettings_Group{
		"finops":   event.FinopsPolicySettings_FINOPS,
		"security": event.FinopsPolicySettings_CLOUD_SECURITY,
		"ai":       event.FinopsPolicySettings_AI,
	}

	finops, security, ai := splitPolicyResults(results, groups)

	slugs := func(rs []*provider.FinopsPolicyResult) []string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = r.PolicySlug
		}
		return out
	}
	if got := slugs(finops); len(got) != 2 || got[0] != "finops" || got[1] != "unknown" {
		t.Errorf("finops = %v, want [finops unknown]", got)
	}
	if got := slugs(security); len(got) != 1 || got[0] != "security" {
		t.Errorf("security = %v, want [security]", got)
	}
	if got := slugs(ai); len(got) != 1 || got[0] != "ai" {
		t.Errorf("ai = %v, want [ai]", got)
	}
}
