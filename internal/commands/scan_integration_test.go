package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/infracost/ci/internal/api/dashboard"
	"github.com/infracost/ci/internal/config"
	testingconfig "github.com/infracost/ci/internal/config/testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// setupScanEventsMocks configures the events mock to expect a single
// infracost-run push and captures the metadata for verification.
func setupScanEventsMocks(m *testingconfig.Mocks) *map[string]interface{} {
	captured := make(map[string]interface{})
	m.Events.EXPECT().
		Push(mock.Anything, "infracost-run", mock.Anything).
		Run(func(_ context.Context, _ string, extra ...interface{}) {
			for i := 0; i < len(extra); i += 2 {
				if key, ok := extra[i].(string); ok {
					captured[key] = extra[i+1]
				}
			}
		}).
		Return().
		Once()
	return &captured
}

func runScan(t *testing.T, cfg *config.Config, path string) error {
	t.Helper()
	// scan reads the repo URL from config only, so the fixture must supply it.
	return scan(cfg, &scanArgs{path: path, repoURL: testRepoURL})
}

func TestScan_BasicUpload(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	runEvent := setupScanEventsMocks(m)
	_ = runEvent // used below after scan

	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.Anything).
		Run(func(_ context.Context, input dashboard.RunInput) {
			// Verify this is an upload (not a comment) run.
			metadata, ok := input.Metadata["command"]
			if !ok || metadata != "upload" {
				t.Errorf("expected command metadata to be 'upload', got %v", metadata)
			}

			// Should have project results but no past breakdowns.
			if len(input.ProjectResults) == 0 {
				t.Error("expected at least one project result")
			}
			for _, pr := range input.ProjectResults {
				if pr.PastBreakdown != nil {
					t.Errorf("expected no past breakdown for scan upload, got one for project %q", pr.ProjectName)
				}
				if pr.Diff != nil {
					t.Errorf("expected no diff for scan upload, got one for project %q", pr.ProjectName)
				}
			}
		}).
		Return(dashboard.AddRunResult{
			ID:       "test-run-id",
			CloudURL: "https://dashboard.infracost.io/org/test-org/runs/test-run-id",
		}, nil)

	err := runScan(t, cfg, filepath.Join(testdataDir(), "basic", "head"))
	if err != nil {
		t.Fatalf("scan() returned error: %v", err)
	}

	env := *runEvent
	if env["outputFormat"] != "upload" {
		t.Errorf("expected outputFormat 'upload', got %v", env["outputFormat"])
	}
	if env["totalResources"] == nil || env["totalResources"].(int) == 0 {
		t.Error("expected non-zero totalResources in infracost-run event")
	}
}

func TestScan_DashboardDisabled(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	cfg.DisableDashboard = true
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	// AddRun should NOT be called when dashboard is disabled.
	setupScanEventsMocks(m)

	err := runScan(t, cfg, filepath.Join(testdataDir(), "basic", "head"))
	if err != nil {
		t.Fatalf("scan() returned error: %v", err)
	}
}

func TestScan_ScanFailureUploadsErrorRun(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.MatchedBy(func(input dashboard.RunInput) bool {
			return input.Error != nil && input.Error.Level == "error"
		})).
		Return(dashboard.AddRunResult{}, nil)

	err := runScan(t, cfg, filepath.Join(testdataDir(), "nonexistent"))
	if err == nil {
		t.Fatal("expected error from scan() when path does not exist")
	}
}

func TestScan_ScanFailureDashboardDisabled(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	cfg.DisableDashboard = true
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	// AddRun should NOT be called when dashboard is disabled, even on error.

	err := runScan(t, cfg, filepath.Join(testdataDir(), "nonexistent"))
	if err == nil {
		t.Fatal("expected error from scan() when path does not exist")
	}
}

func TestScan_DashboardError(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.Anything).
		Return(dashboard.AddRunResult{}, fmt.Errorf("dashboard unavailable"))

	err := runScan(t, cfg, filepath.Join(testdataDir(), "basic", "head"))
	if err == nil {
		t.Fatal("expected error from scan() when dashboard fails")
	}
}

func TestScan_VCSProviderFromConfig(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	cfg.VCSProvider = "azure_repos"
	t.Setenv("INFRACOST_CI_PLATFORM", "test_platform")
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	setupScanEventsMocks(m)

	var metadata map[string]interface{}
	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.Anything).
		Run(func(_ context.Context, input dashboard.RunInput) {
			metadata = input.Metadata
		}).
		Return(dashboard.AddRunResult{ID: "test-run-id"}, nil)

	if err := runScan(t, cfg, filepath.Join(testdataDir(), "basic", "head")); err != nil {
		t.Fatalf("scan() returned error: %v", err)
	}

	if metadata["vcsProvider"] != "azure_repos" {
		t.Errorf("expected vcsProvider 'azure_repos', got %v", metadata["vcsProvider"])
	}
	if metadata["ciPlatform"] != "test_platform" {
		t.Errorf("expected ciPlatform 'test_platform', got %v", metadata["ciPlatform"])
	}
}

// gitRepoWithProject copies a testdata project into a git repository committed
// at committerDate, so scan reads a real commit rather than an empty SHA.
func gitRepoWithProject(t *testing.T, src, committerDate string) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS(src)))

	gitRun(t, dir, nil, "init", "-q")
	gitRun(t, dir, nil, "config", "user.email", "test@example.com")
	gitRun(t, dir, nil, "config", "user.name", "test")
	gitRun(t, dir, nil, "add", ".")
	gitRun(t, dir, []string{
		"GIT_COMMITTER_DATE=" + committerDate,
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00+00:00",
	}, "commit", "-qm", "seed")

	return dir
}

func gitRun(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) // #nosec G204 -- test-controlled args
	cmd.Dir = dir
	// Isolated from the developer's gitconfig: commit.gpgsign or core.hooksPath
	// there would fail the seed commit for reasons unrelated to the test.
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

// The dashboard orders runs on generated_at. Wall clock would stamp commit +
// CI queue + scan minutes, which the diff baseline upload cannot match.
func TestScan_TimeGeneratedIsTheCommitterDate(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)
	setupScanEventsMocks(m)

	var input dashboard.RunInput
	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.Anything).
		Run(func(_ context.Context, in dashboard.RunInput) { input = in }).
		Return(dashboard.AddRunResult{ID: "test-run-id"}, nil)

	path := gitRepoWithProject(t, filepath.Join(testdataDir(), "basic", "head"), "2024-05-06T07:08:09+00:00")
	require.NoError(t, runScan(t, cfg, path))

	// normaliseTimestamp reformats through RFC3339, so git's "+00:00" becomes "Z".
	assert.Equal(t, "2024-05-06T07:08:09Z", input.TimeGenerated)
	// The author date still feeds vcsCommitTimestamp, which keeps its meaning.
	assert.Equal(t, "2000-01-01T00:00:00Z", input.Metadata["vcsCommitTimestamp"])
}

// The override is the escape hatch for a checkout scan cannot read a commit
// from, so it still outranks the committer date.
func TestScan_TimeGeneratedHonoursTheCommitTimestampOverride(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	cfg.VCS.CommitTimestamp = "1700000000"
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)
	setupScanEventsMocks(m)

	var input dashboard.RunInput
	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.Anything).
		Run(func(_ context.Context, in dashboard.RunInput) { input = in }).
		Return(dashboard.AddRunResult{ID: "test-run-id"}, nil)

	path := gitRepoWithProject(t, filepath.Join(testdataDir(), "basic", "head"), "2024-05-06T07:08:09+00:00")
	require.NoError(t, runScan(t, cfg, path))

	assert.Equal(t, "2023-11-14T22:13:20Z", input.TimeGenerated)
}
