package commands

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/infracost/ci/internal/api/dashboard"
	"github.com/infracost/ci/internal/config"
	testingconfig "github.com/infracost/ci/internal/config/testing"
	"github.com/infracost/proto/gen/go/infracost/parser/event"
	"github.com/infracost/proto/gen/go/infracost/rational"
	"github.com/infracost/vcs/pkg/vcs"
	"github.com/infracost/vcs/pkg/vcs/azure"
	"github.com/infracost/vcs/pkg/vcs/bitbucket"
	"github.com/infracost/vcs/pkg/vcs/comment"
	"github.com/infracost/vcs/pkg/vcs/github"
	"github.com/infracost/vcs/pkg/vcs/gitlab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	testRepoURL  = "https://github.com/infracost/actions"
	testPRNumber = 42
	testRunID    = "test-run-id"
)

func testdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata")
}

// processPlugins initialises the plugin manager configuration.
// Must be called after the config is in its final location (pointer-stable)
// so that the manager captures the correct *plugins.Config.
func processPlugins(cfg *config.Config) {
	cfg.Plugins.Process()
}

// emptyRunParams returns dashboard.RunParameters with minimal required fields.
func emptyRunParams() dashboard.RunParameters {
	return dashboard.RunParameters{
		OrganizationID:   "test-org-id",
		OrganizationSlug: "test-org",
		CloudEnabled:     true,
		RepositoryID:     "test-repo-id",
		RepositoryName:   "test-repo",
	}
}

// setupDashboardAddRun configures the dashboard mock to accept AddRun and return a test URL.
// The posted-comment patch is optional here: only the cases that post assert on it.
func setupDashboardAddRun(m *testingconfig.Mocks) {
	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.Anything).
		Return(dashboard.AddRunResult{
			ID:       testRunID,
			CloudURL: "https://dashboard.infracost.io/org/test-org/repos/test-repo-id/runs/" + testRunID,
		}, nil)
	m.Dashboard.EXPECT().
		SavePostedPrComment(mock.Anything, testRunID, "comment body").
		Return(true, nil).
		Maybe()
}

// setupEventsMocks configures the events mock to accept Push calls and captures
// the infracost-run event metadata for verification.
func setupEventsMocks(m *testingconfig.Mocks) *map[string]interface{} {
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
	// Allow any number of cloud-issue-fixed events.
	m.Events.EXPECT().
		Push(mock.Anything, "cloud-issue-fixed", mock.Anything).
		Return().
		Maybe()
	return &captured
}

// setupVCSMocks configures the VCS mock to capture comment.Data and accept PostComment.
func setupVCSMocks(m *testingconfig.Mocks) *comment.Data {
	var captured comment.Data
	m.VCS.EXPECT().
		GenerateComment(mock.Anything).
		Run(func(data comment.Data) {
			captured = data
		}).
		Return("comment body", nil)
	m.VCS.EXPECT().
		PostComment(mock.Anything, "comment body", vcs.BehaviorUpdate).
		Return(vcs.PostResult{Posted: true}, nil)
	return &captured
}

// mustProtoJSON marshals a proto message to json.RawMessage using protojson.
func mustProtoJSON(t *testing.T, msg proto.Message) json.RawMessage {
	t.Helper()
	b, err := protojson.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal proto to JSON: %v", err)
	}
	return b
}

// ratProto creates a rational.Rat from a numerator integer (denominator = 1).
func ratProto(num int64) *rational.Rat {
	n := big.NewInt(num)
	negative := num < 0
	if negative {
		n = n.Abs(n)
	}
	return &rational.Rat{
		Numerator:   n.Bytes(),
		Denominator: big.NewInt(1).Bytes(),
		Negative:    negative,
	}
}

func runDiff(t *testing.T, cfg *config.Config, m *testingconfig.Mocks, basePath, headPath string) (*ScanResult, error) {
	t.Helper()
	return runDiffWithArgs(t, cfg, m, basePath, headPath, diffArgs{})
}

func runDiffWithArgs(t *testing.T, cfg *config.Config, m *testingconfig.Mocks, basePath, headPath string, extra diffArgs) (*ScanResult, error) {
	t.Helper()
	extra.basePath = basePath
	extra.headPath = headPath
	// diff is always a pull request run, so every case needs an identity.
	if extra.repoURL == "" {
		extra.repoURL = testRepoURL
	}
	if extra.prNumber == 0 {
		extra.prNumber = testPRNumber
	}
	vcsCtx, err := resolveDiffContext(context.Background(), cfg, &extra)
	if err != nil {
		return &ScanResult{}, err
	}
	var results ScanResult
	err = diff(cfg, &extra, vcsCtx, m.VCS, &results)
	return &results, err
}

func TestDiff_BasicCostDiff(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	setupDashboardAddRun(m)
	data := setupVCSMocks(m)
	runEvent := setupEventsMocks(m)

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "basic", "base"), filepath.Join(testdataDir(), "basic", "head"))
	if err != nil {
		t.Fatalf("diff() returned error: %v", err)
	}

	if len(data.Projects) == 0 {
		t.Fatal("expected at least one project in comment data")
	}

	project := data.Projects[0]

	if project.PastTotalMonthlyCost == nil || project.PastTotalMonthlyCost.IsZero() {
		t.Error("expected non-zero past total monthly cost")
	}
	if project.TotalMonthlyCost == nil || project.TotalMonthlyCost.IsZero() {
		t.Error("expected non-zero total monthly cost")
	}

	if project.DiffBreakdown == nil || project.DiffBreakdown.TotalMonthlyCost == nil || project.DiffBreakdown.TotalMonthlyCost.IsZero() {
		t.Error("expected non-zero diff breakdown cost")
	}

	// Verify event tracking was called with diff metadata.
	env := *runEvent
	if env["outputFormat"] != "comment" {
		t.Errorf("expected outputFormat 'comment', got %v", env["outputFormat"])
	}
	if _, ok := env["newResourceCount"]; !ok {
		t.Error("expected newResourceCount in infracost-run event for diff")
	}
}

func TestDiff_NoChanges(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	setupDashboardAddRun(m)
	data := setupVCSMocks(m)
	setupEventsMocks(m)

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "no-changes", "base"), filepath.Join(testdataDir(), "no-changes", "head"))
	if err != nil {
		t.Fatalf("diff() returned error: %v", err)
	}

	if len(data.Projects) == 0 {
		t.Fatal("expected at least one project in comment data")
	}

	project := data.Projects[0]

	if project.DiffBreakdown != nil && project.DiffBreakdown.TotalMonthlyCost != nil && !project.DiffBreakdown.TotalMonthlyCost.IsZero() {
		t.Errorf("expected zero diff, got %s", project.DiffBreakdown.TotalMonthlyCost.String())
	}
}

func TestDiff_DashboardError(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(dashboard.RunParameters{}, errors.New("dashboard unavailable"))

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "basic", "base"), filepath.Join(testdataDir(), "basic", "head"))
	if err == nil {
		t.Fatal("expected error from diff() when dashboard fails")
	}
}

func TestDiff_ScanFailureUploadsErrorRun(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	// When scanning fails, an error run should be uploaded to the dashboard.
	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.MatchedBy(func(input dashboard.RunInput) bool {
			return input.Error != nil && input.Error.Level == "error"
		})).
		Return(dashboard.AddRunResult{}, nil)

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "nonexistent"), filepath.Join(testdataDir(), "basic", "head"))
	if err == nil {
		t.Fatal("expected error from diff() when base path does not exist")
	}
}

func TestDiff_ScanFailureDashboardDisabled(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	cfg.DisableDashboard = true
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	// AddRun should NOT be called when dashboard is disabled, even on error.

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "nonexistent"), filepath.Join(testdataDir(), "basic", "head"))
	if err == nil {
		t.Fatal("expected error from diff() when base path does not exist")
	}
}

// A retried post must not fail the run, and its sleep must stay out of the
// tracked run time.
func TestDiff_RetriedPostIsNotRunTime(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	setupDashboardAddRun(m)
	m.VCS.EXPECT().GenerateComment(mock.Anything).Return("comment body", nil)
	m.VCS.EXPECT().
		PostComment(mock.Anything, "comment body", vcs.BehaviorUpdate).
		Return(vcs.PostResult{}, retryAfterError("1")).Once()
	m.VCS.EXPECT().
		PostComment(mock.Anything, "comment body", vcs.BehaviorUpdate).
		Return(vcs.PostResult{SkipReason: "not updating comment since the latest one is newer"}, nil).Once()
	runEvent := setupEventsMocks(m)

	start := time.Now()
	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "basic", "base"), filepath.Join(testdataDir(), "basic", "head"))
	require.NoError(t, err)
	elapsed := time.Since(start)

	require.GreaterOrEqual(t, elapsed, time.Second, "expected the retry to sleep for the Retry-After")
	runSeconds, ok := (*runEvent)["runSeconds"].(float64)
	require.True(t, ok, "expected runSeconds in infracost-run event")
	assert.LessOrEqual(t, runSeconds, elapsed.Seconds()-1)
}

// An upload failure costs the comment its dashboard link, not its existence.
func TestDiff_UploadFailureStillPostsComment(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)
	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.Anything).
		Return(dashboard.AddRunResult{}, errors.New("dashboard unavailable"))

	data := setupVCSMocks(m)
	setupEventsMocks(m)

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "basic", "base"), filepath.Join(testdataDir(), "basic", "head"))
	require.NoError(t, err, "an upload failure must not stop the comment")

	m.VCS.AssertNumberOfCalls(t, "PostComment", 1)
	assert.False(t, data.CloudEnabled, "no run to link to")
	assert.Empty(t, data.RunID)
	// No run exists, so there is no flag to patch.
	m.Dashboard.AssertNotCalled(t, "SavePostedPrComment", mock.Anything, mock.Anything, mock.Anything)
}

// The flag starts false in the AddRun payload and becomes true only once the
// comment is on the pull request.
func TestDiff_PostedCommentPatchesTheFlag(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)
	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.MatchedBy(func(input dashboard.RunInput) bool {
			return input.ClientPostedComment != nil && !*input.ClientPostedComment
		})).
		Return(dashboard.AddRunResult{ID: testRunID}, nil).
		Once()
	m.Dashboard.EXPECT().
		SavePostedPrComment(mock.Anything, testRunID, "comment body").
		Return(true, nil).
		Once()

	setupVCSMocks(m)
	setupEventsMocks(m)

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "basic", "base"), filepath.Join(testdataDir(), "basic", "head"))
	require.NoError(t, err)
}

// A skipped post means an identical or newer comment is already on the pull
// request, so the flag must still be patched.
func TestDiff_SkippedCommentPatchesTheFlag(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)
	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.Anything).
		Return(dashboard.AddRunResult{ID: testRunID}, nil)
	m.Dashboard.EXPECT().
		SavePostedPrComment(mock.Anything, testRunID, "comment body").
		Return(true, nil).
		Once()

	m.VCS.EXPECT().GenerateComment(mock.Anything).Return("comment body", nil)
	m.VCS.EXPECT().
		PostComment(mock.Anything, "comment body", vcs.BehaviorUpdate).
		Return(vcs.PostResult{SkipReason: "not updating comment since the latest one matches exactly"}, nil)
	setupEventsMocks(m)

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "basic", "base"), filepath.Join(testdataDir(), "basic", "head"))
	require.NoError(t, err)
}

// A post that never succeeded must not be recorded as one.
func TestDiff_FailedPostDoesNotPatchTheFlag(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)
	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.Anything).
		Return(dashboard.AddRunResult{ID: testRunID}, nil)

	m.VCS.EXPECT().GenerateComment(mock.Anything).Return("comment body", nil)
	m.VCS.EXPECT().
		PostComment(mock.Anything, "comment body", vcs.BehaviorUpdate).
		Return(vcs.PostResult{}, errors.New("403 Forbidden"))

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "basic", "base"), filepath.Join(testdataDir(), "basic", "head"))
	require.Error(t, err)

	m.Dashboard.AssertNotCalled(t, "SavePostedPrComment", mock.Anything, mock.Anything, mock.Anything)
}

func TestDiff_GuardrailTriggered(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	guardrail := mustProtoJSON(t, &event.Guardrail{
		Id:                "gr-1",
		Name:              "Cost increase limit",
		Scope:             event.Guardrail_REPO,
		IncreaseThreshold: ratProto(1),
		PrComment:         true,
		BlockPr:           true,
		Message:           "Cost increase exceeds threshold",
	})

	params := emptyRunParams()
	params.Guardrails = []json.RawMessage{guardrail}

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(params, nil)

	setupDashboardAddRun(m)
	data := setupVCSMocks(m)
	setupEventsMocks(m)

	result, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "basic", "base"), filepath.Join(testdataDir(), "basic", "head"))
	if err != nil {
		t.Fatalf("diff() returned error: %v", err)
	}

	triggered := false
	for _, gr := range data.GuardrailResults {
		if gr.Triggered {
			triggered = true
			break
		}
	}
	if !triggered {
		t.Error("expected at least one triggered guardrail result")
	}

	if !result.BlockPR {
		t.Error("expected BlockPR to be true")
	}
	if len(result.Reasons) == 0 {
		t.Error("expected at least one blocking reason")
	}
}

func TestDiff_GuardrailSuppressed(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	guardrail := mustProtoJSON(t, &event.Guardrail{
		Id:             "gr-suppress",
		Name:           "Total cost limit",
		Scope:          event.Guardrail_REPO,
		TotalThreshold: ratProto(0),
		PrComment:      true,
		Message:        "Total cost exceeds threshold",
	})

	params := emptyRunParams()
	params.Guardrails = []json.RawMessage{guardrail}

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(params, nil)

	setupDashboardAddRun(m)
	data := setupVCSMocks(m)
	setupEventsMocks(m)

	result, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "no-changes", "base"), filepath.Join(testdataDir(), "no-changes", "head"))
	if err != nil {
		t.Fatalf("diff() returned error: %v", err)
	}

	if len(data.PreviousGuardrailResults) == 0 {
		t.Error("expected guardrail in PreviousGuardrailResults (suppressed)")
	}

	if result.BlockPR {
		t.Errorf("expected BlockPR to be false for suppressed guardrails, got reasons: %v", result.Reasons)
	}
}

func TestDiff_FinOpsPolicy(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	finopsPolicy := mustProtoJSON(t, &event.FinopsPolicySettings{
		Id:        "fp-1",
		Slug:      "aws-gp2-volumes",
		Name:      "Use gp3 instead of gp2",
		Message:   "gp3 volumes are cheaper",
		PrComment: true,
		Group:     event.FinopsPolicySettings_FINOPS,
	})

	securityPolicy := mustProtoJSON(t, &event.FinopsPolicySettings{
		Id:        "sp-1",
		Slug:      "aws-unencrypted-volumes",
		Name:      "Encrypt EBS volumes",
		Message:   "EBS volumes should be encrypted",
		PrComment: true,
		Group:     event.FinopsPolicySettings_CLOUD_SECURITY,
	})

	params := emptyRunParams()
	params.FinopsPolicies = []json.RawMessage{finopsPolicy, securityPolicy}

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(params, nil)

	setupDashboardAddRun(m)
	data := setupVCSMocks(m)
	setupEventsMocks(m)

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "basic", "base"), filepath.Join(testdataDir(), "basic", "head"))
	if err != nil {
		t.Fatalf("diff() returned error: %v", err)
	}

	if len(data.Projects) == 0 {
		t.Error("expected at least one project")
	}
}

func TestDiff_UsageDefaults(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	usageDefaults := mustProtoJSON(t, &event.UsageDefaults{
		Resources: map[string]*event.UsageResourceMap{
			"aws_instance": {
				Usages: map[string]*event.UsageDefaultList{
					"monthly_hrs": {
						List: []*event.UsageDefault{
							{Quantity: "730"},
						},
					},
				},
			},
		},
	})

	params := emptyRunParams()
	params.UsageDefaults = usageDefaults

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(params, nil)

	setupDashboardAddRun(m)
	data := setupVCSMocks(m)
	setupEventsMocks(m)

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "basic", "base"), filepath.Join(testdataDir(), "basic", "head"))
	if err != nil {
		t.Fatalf("diff() returned error: %v", err)
	}

	if !data.UsageAPIEnabled {
		t.Error("expected UsageAPIEnabled to be true when usage defaults are provided")
	}
}

func TestDiff_SingleProjectFilter(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	setupDashboardAddRun(m)
	data := setupVCSMocks(m)
	setupEventsMocks(m)

	_, err := runDiffWithArgs(t, cfg, m, filepath.Join(testdataDir(), "multi-project", "base"), filepath.Join(testdataDir(), "multi-project", "head"), diffArgs{project: "web"})
	if err != nil {
		t.Fatalf("diff() returned error: %v", err)
	}

	if len(data.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(data.Projects))
	}
	if data.Projects[0].Name != "web" {
		t.Errorf("expected project name 'web', got %q", data.Projects[0].Name)
	}
}

func TestDiff_VCSProviderFromConfig(t *testing.T) {
	cfg, m := testingconfig.Config(t)
	// gitlab proves the value came from cfg rather than the old literal. diff() runs
	// below newVCSClient, so a provider the real command rejects is reachable here.
	cfg.VCSProvider = "gitlab"
	t.Setenv("INFRACOST_CI_PLATFORM", "test_platform")
	processPlugins(cfg)

	m.Dashboard.EXPECT().
		RunParameters(mock.Anything, mock.Anything, mock.Anything).
		Return(emptyRunParams(), nil)

	var metadata map[string]interface{}
	m.Dashboard.EXPECT().
		AddRun(mock.Anything, mock.Anything).
		Run(func(_ context.Context, input dashboard.RunInput) {
			metadata = input.Metadata
		}).
		Return(dashboard.AddRunResult{ID: "test-run-id"}, nil)

	setupVCSMocks(m)
	setupEventsMocks(m)

	_, err := runDiff(t, cfg, m, filepath.Join(testdataDir(), "basic", "base"), filepath.Join(testdataDir(), "basic", "head"))
	if err != nil {
		t.Fatalf("diff() returned error: %v", err)
	}

	if metadata["vcsProvider"] != "gitlab" {
		t.Errorf("expected vcsProvider 'gitlab', got %v", metadata["vcsProvider"])
	}
	if metadata["ciPlatform"] != "test_platform" {
		t.Errorf("expected ciPlatform 'test_platform', got %v", metadata["ciPlatform"])
	}
}

// A github.com repository URL cannot buy another provider's client, however
// the flags are set: the token would go to the wrong host.
func TestNewVCSClient_UnsupportedProvider(t *testing.T) {
	for _, args := range []diffArgs{
		{},
		{githubOwner: "acme", githubRepo: "infra"},
		{gitlabToken: "glpat-x", azureToken: "azure-token"},
	} {
		for _, provider := range []string{"gitlab", "azure_repos", "bitbucket"} {
			t.Run(provider, func(t *testing.T) {
				vcsCtx := diffContext{provider: provider, repoURL: testRepoURL, prNumber: testPRNumber}
				_, err := newVCSClient(context.Background(), new(config.Config), &args, vcsCtx)
				if err == nil {
					t.Fatalf("expected newVCSClient to reject %q", provider)
				}
				if !strings.Contains(err.Error(), "is a github host") {
					t.Errorf("expected a host mismatch error for %q, got: %v", provider, err)
				}
			})
		}
	}
}

func TestNewVCSClient(t *testing.T) {
	const (
		githubToken    = "ghp-token"
		gitlabToken    = "glpat-token"
		azureToken     = "azure-token"
		bitbucketToken = "bb-token"
	)

	tests := []struct {
		name     string
		provider string
		repoURL  string
		args     diffArgs
		wantType any
		// wantServer, when set, is the serverURL that reached gitlab.New.
		wantServer string
		wantErr    string
	}{
		{
			name: "github", provider: "github", repoURL: testRepoURL,
			args: diffArgs{githubToken: githubToken}, wantType: &github.GitHub{},
		},
		{
			name: "github enterprise", provider: "github", repoURL: "https://ghes.corp/infracost/actions",
			args: diffArgs{githubToken: githubToken}, wantType: &github.GitHub{},
		},
		{
			name: "github owner and repo override", provider: "github", repoURL: testRepoURL,
			args: diffArgs{githubToken: githubToken, githubOwner: "acme", githubRepo: "infra"}, wantType: &github.GitHub{},
		},
		{
			name: "gitlab", provider: "gitlab", repoURL: "https://gitlab.com/infracost/actions",
			args: diffArgs{gitlabToken: gitlabToken}, wantType: &gitlab.GitLab{},
		},
		{
			name: "gitlab self-managed subgroup", provider: "gitlab", repoURL: "https://gitlab.corp/group/sub/repo.git",
			args: diffArgs{gitlabToken: gitlabToken}, wantType: &gitlab.GitLab{},
		},
		// A relative-root install derives both values wrong, so the flags
		// stand in for a path the URL cannot yield.
		// gitlab.New builds REST note paths as <serverURL>/api/v4/..., so a
		// trailing slash on the override would give //api/v4/....
		{
			name: "gitlab server url trailing slash", provider: "gitlab", repoURL: "https://gitlab.corp/group/repo",
			args: diffArgs{gitlabToken: gitlabToken, gitlabServer: "https://gitlab.corp/"}, wantServer: "https://gitlab.corp", wantType: &gitlab.GitLab{},
		},
		{
			name: "gitlab relative root overrides", provider: "gitlab", repoURL: "https://host/gitlab/group/repo",
			args: diffArgs{gitlabToken: gitlabToken, gitlabProject: "group/repo", gitlabServer: "https://host/gitlab"}, wantType: &gitlab.GitLab{},
		},
		{
			name: "gitlab single segment path", provider: "gitlab", repoURL: "https://gitlab.com/actions",
			args: diffArgs{gitlabToken: gitlabToken}, wantErr: "the repo URL path must be /<group>/<project>",
		},
		{
			name: "azure", provider: "azure_repos", repoURL: "https://dev.azure.com/org/project/_git/repo",
			args: diffArgs{azureToken: azureToken}, wantType: &azure.Azure{},
		},
		{
			name: "azure with org userinfo", provider: "azure_repos", repoURL: "https://org@dev.azure.com/org/project/_git/repo",
			args: diffArgs{azureToken: azureToken}, wantType: &azure.Azure{},
		},

		{
			name: "bitbucket cloud", provider: "bitbucket", repoURL: "https://bitbucket.org/acme/infra",
			args: diffArgs{bitbucketToken: bitbucketToken}, wantType: &bitbucket.Bitbucket{},
		},
		{
			name: "bitbucket cloud clone suffix", provider: "bitbucket", repoURL: "https://bitbucket.org/acme/infra.git",
			args: diffArgs{bitbucketToken: bitbucketToken}, wantType: &bitbucket.Bitbucket{},
		},
		{
			name: "bitbucket server", provider: "bitbucket", repoURL: "https://bb.corp/projects/PROJ/repos/infra",
			args: diffArgs{bitbucketToken: bitbucketToken}, wantType: &bitbucket.Bitbucket{},
		},
		{
			name: "bitbucket server context path", provider: "bitbucket", repoURL: "https://bb.corp/stash/projects/PROJ/repos/infra",
			args: diffArgs{bitbucketToken: bitbucketToken}, wantType: &bitbucket.Bitbucket{},
		},
		{
			name: "bitbucket cloud single segment path", provider: "bitbucket", repoURL: "https://bitbucket.org/infra",
			args: diffArgs{bitbucketToken: bitbucketToken}, wantErr: "the repo URL path must be /<workspace>/<repo>",
		},
		{
			name: "bitbucket server unrecognised path", provider: "bitbucket", repoURL: "https://bb.corp/acme/infra",
			args: diffArgs{bitbucketToken: bitbucketToken}, wantErr: "the repo URL path must be /projects/<key>/repos/<repo>",
		},
		{
			name: "bitbucket overrides", provider: "bitbucket", repoURL: "https://bb.corp/scm/PROJ/infra",
			args:     diffArgs{bitbucketToken: bitbucketToken, bitbucketRepo: "PROJ/infra", bitbucketSrv: "https://bb.corp"},
			wantType: &bitbucket.Bitbucket{},
		},
		{
			name: "bitbucket token missing", provider: "bitbucket", repoURL: "https://bitbucket.org/acme/infra",
			wantErr: "set --bitbucket-token or BITBUCKET_TOKEN",
		},
		// The server the token is sent to is host-checked, not only the repo URL.
		{
			name: "bitbucket server url on another vendor's host", provider: "bitbucket", repoURL: "https://bb.corp/projects/PROJ/repos/infra",
			args: diffArgs{bitbucketToken: bitbucketToken, bitbucketSrv: "https://gitlab.com"}, wantErr: `is a gitlab host, but INFRACOST_VCS_PROVIDER is "bitbucket"`,
		},

		{
			name: "github token missing", provider: "github", repoURL: testRepoURL,
			wantErr: "set --github-token or GITHUB_TOKEN",
		},
		{
			name: "gitlab token missing", provider: "gitlab", repoURL: "https://gitlab.com/infracost/actions",
			wantErr: "set --gitlab-token or GITLAB_TOKEN",
		},
		{
			name: "azure token missing", provider: "azure_repos", repoURL: "https://dev.azure.com/org/project/_git/repo",
			wantErr: "set --azure-token or AZURE_DEVOPS_EXT_PAT or SYSTEM_ACCESSTOKEN",
		},

		{
			name: "github owner on gitlab", provider: "gitlab", repoURL: "https://gitlab.com/infracost/actions",
			args: diffArgs{gitlabToken: gitlabToken, githubOwner: "acme"}, wantErr: "--github-owner, --github-repo and --github-api-url name a GitHub repository",
		},
		{
			name: "github repo on azure", provider: "azure_repos", repoURL: "https://dev.azure.com/org/project/_git/repo",
			args: diffArgs{azureToken: azureToken, githubRepo: "infra"}, wantErr: "--github-owner, --github-repo and --github-api-url name a GitHub repository",
		},
		{
			name: "github api url on gitlab", provider: "gitlab", repoURL: "https://gitlab.com/infracost/actions",
			args: diffArgs{gitlabToken: gitlabToken, githubAPIURL: "https://ghes.corp"}, wantErr: "--github-owner, --github-repo and --github-api-url name a GitHub repository",
		},
		// The server the token is sent to is host-checked, not only the repo URL.
		{
			name: "gitlab server url on another vendor's host", provider: "gitlab", repoURL: "https://gitlab.corp/group/repo",
			args: diffArgs{gitlabToken: gitlabToken, gitlabServer: "https://github.com"}, wantErr: `is a github host, but INFRACOST_VCS_PROVIDER is "gitlab"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vcsCtx := diffContext{provider: tt.provider, repoURL: tt.repoURL, prNumber: testPRNumber}
			// No constructor makes a call, so every case here is offline.
			client, err := newVCSClient(context.Background(), new(config.Config), &tt.args, vcsCtx)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.IsType(t, tt.wantType, client)
			if tt.wantServer != "" {
				field := reflect.ValueOf(client).Elem().FieldByName("serverURL")
				assert.Equal(t, tt.wantServer, field.String())
			}
		})
	}
}

// azure.buildAPIURL appends the repo segment raw, so an untrimmed .git suffix
// would 404 on every call. Only the built API URL shows it was trimmed.
func TestNewVCSClient_AzureTrimsCloneSuffix(t *testing.T) {
	for _, repoURL := range []string{
		"https://dev.azure.com/org/project/_git/repo.git",
		"https://dev.azure.com/org/project/_git/repo/",
		"https://dev.azure.com/org/project/_git/repo",
	} {
		t.Run(repoURL, func(t *testing.T) {
			vcsCtx := diffContext{provider: "azure_repos", repoURL: repoURL, prNumber: testPRNumber}
			client, err := newVCSClient(context.Background(), new(config.Config), &diffArgs{azureToken: "azure-token"}, vcsCtx)
			require.NoError(t, err)

			field := reflect.ValueOf(client).Elem().FieldByName("repoAPIURL")
			assert.Equal(t, "https://dev.azure.com/org/project/_apis/git/repositories/repo/", field.String())
		})
	}
}

// tlsProviders is every provider a comment client exists for, so a branch that
// drops its TLS wiring cannot pass by being missed.
var tlsProviders = []struct {
	provider string
	repoURL  string
	args     diffArgs
}{
	{"github", testRepoURL, diffArgs{githubToken: "ghp-token"}},
	{"gitlab", "https://gitlab.com/infracost/actions", diffArgs{gitlabToken: "glpat-token"}},
	{"azure_repos", "https://dev.azure.com/org/project/_git/repo", diffArgs{azureToken: "azure-token"}},
	{"bitbucket", "https://bb.corp/projects/PROJ/repos/infra", diffArgs{bitbucketToken: "bb-token"}},
}

// The CA bundle must reach the client, not just parse: a branch that drops
// TLSConfig from its Options would otherwise fail only against a private CA.
func TestNewVCSClient_TLSConfigReachesTheClient(t *testing.T) {
	cfg := new(config.Config)
	cfg.TLSCACertFile = writeTestCA(t)

	for _, tt := range tlsProviders {
		t.Run(tt.provider, func(t *testing.T) {
			vcsCtx := diffContext{provider: tt.provider, repoURL: tt.repoURL, prNumber: testPRNumber}
			client, err := newVCSClient(context.Background(), cfg, &tt.args, vcsCtx)
			require.NoError(t, err)
			assert.True(t, hasTLSConfig(reflect.ValueOf(client), 0), "no *tls.Config reached the %s client", tt.provider)
		})
	}
}

// An unreadable CA file fails the run rather than falling back to the system
// pool: the operator asked for a CA the VCS server is expected to present.
func TestNewVCSClient_TLSConfigError(t *testing.T) {
	cfg := new(config.Config)
	cfg.TLSCACertFile = filepath.Join(t.TempDir(), "missing.pem")

	for _, tt := range tlsProviders {
		t.Run(tt.provider, func(t *testing.T) {
			vcsCtx := diffContext{provider: tt.provider, repoURL: tt.repoURL, prNumber: testPRNumber}
			_, err := newVCSClient(context.Background(), cfg, &tt.args, vcsCtx)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "failed to read INFRACOST_CI_VCS_TLS_CA_CERT_FILE")
		})
	}
}

// hasTLSConfig looks for a non-nil *tls.Config anywhere in the client, so the
// assertion does not depend on each provider's unexported field names.
func hasTLSConfig(v reflect.Value, depth int) bool {
	if depth > 16 || !v.IsValid() {
		return false
	}
	if v.Type() == reflect.TypeOf((*tls.Config)(nil)) {
		return !v.IsNil()
	}

	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return !v.IsNil() && hasTLSConfig(v.Elem(), depth+1)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if hasTLSConfig(v.Field(i), depth+1) {
				return true
			}
		}
	}
	return false
}

// writeTestCA writes a self-signed CA so TLSConfig has something to append.
func writeTestCA(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "infracost-ci test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	return path
}
