package commands

import (
	"testing"

	"github.com/infracost/proto/gen/go/infracost/provider"
	"github.com/infracost/vcs/pkg/vcs/comment"
	"github.com/stretchr/testify/assert"
)

// A new blocking AI policy failure blocks the PR, as FinOps and security ones do.
// One that was already failing on the base branch doesn't.
func TestCheckBlockingViolations_AIPolicy(t *testing.T) {
	failing := func(slug, name string) *provider.FinopsPolicyResult {
		return &provider.FinopsPolicyResult{
			PolicySlug:       slug,
			PolicyName:       name,
			BlockPullRequest: true,
			FailingResources: []*provider.FinopsPolicyFailingResource{{Id: "app/chat.py:12"}},
		}
	}

	results := &ScanResult{}
	checkBlockingViolations(comment.Data{
		AIPolicyResults: []*provider.FinopsPolicyResult{failing("ai-deprecated-models", "Deprecated models")},
	}, nil, results)
	assert.True(t, results.BlockPR)
	assert.Equal(t, []string{`policy "Deprecated models" has failing resources`}, results.Reasons)

	results = &ScanResult{}
	checkBlockingViolations(comment.Data{
		AIPolicyResults:         []*provider.FinopsPolicyResult{failing("ai-deprecated-models", "Deprecated models")},
		PreviousAIPolicyResults: []*provider.FinopsPolicyResult{failing("ai-deprecated-models", "Deprecated models")},
	}, nil, results)
	assert.False(t, results.BlockPR)
}
