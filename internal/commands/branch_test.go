package commands

import (
	"context"
	"testing"

	"github.com/infracost/ci/v2/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A detached checkout is what Buildkite, Jenkins, TeamCity and Woodpecker leave
// behind, and it is also what `git worktree add <sha>` makes. rev-parse answers
// the literal "HEAD" there, so the run used to upload under a branch of that
// name: no error in the build, and nothing under Visibility → Repos.
func TestResolveDiffContext_RefusesADetachedCheckout(t *testing.T) {
	tests := []struct {
		name          string
		detachHead    bool
		detachBase    bool
		envBranch     string
		envBaseBranch string
		wantErr       string
		wantBranch    string
		wantBase      string
	}{
		{
			name:       "both on a named branch",
			wantBranch: "feature",
			wantBase:   "feature",
		},
		{
			name:       "detached head",
			detachHead: true,
			wantErr:    "cannot determine the branch: set INFRACOST_VCS_BRANCH",
		},
		{
			name:       "detached base",
			detachBase: true,
			wantErr:    "cannot determine the base branch: set INFRACOST_VCS_BASE_BRANCH",
		},
		{
			// What every supported platform does: inference fills both, so a
			// detached worktree is the normal case and must still work.
			name:          "detached but both supplied",
			detachHead:    true,
			detachBase:    true,
			envBranch:     "increase-iops",
			envBaseBranch: "main",
			wantBranch:    "increase-iops",
			wantBase:      "main",
		},
		{
			// An explicit value outranks the checkout, not just fills a gap.
			name:          "supplied values beat the checkout",
			envBranch:     "from-env",
			envBaseBranch: "base-from-env",
			wantBranch:    "from-env",
			wantBase:      "base-from-env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			head := gitRepoWithProject(t, "testdata/basic", "2000-01-01T00:00:00+00:00")
			gitRun(t, head, nil, "checkout", "-q", "-b", "feature")
			base := gitRepoWithProject(t, "testdata/basic", "2000-01-01T00:00:00+00:00")
			gitRun(t, base, nil, "checkout", "-q", "-b", "feature")
			if tt.detachHead {
				gitRun(t, head, nil, "checkout", "-q", "--detach", "HEAD")
			}
			if tt.detachBase {
				gitRun(t, base, nil, "checkout", "-q", "--detach", "HEAD")
			}

			cfg := &config.Config{VCSProvider: "github"}
			cfg.VCS.Branch = tt.envBranch
			cfg.VCS.BaseBranch = tt.envBaseBranch
			args := &diffArgs{
				repoURL:  "https://github.com/acme/infra",
				prNumber: 42,
				basePath: base,
				headPath: head,
			}

			vcsCtx, err := resolveDiffContext(context.Background(), cfg, args)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBranch, vcsCtx.branch)
			assert.Equal(t, tt.wantBase, vcsCtx.baseBranch)
		})
	}
}
