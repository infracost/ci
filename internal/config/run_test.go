package config

import (
	"testing"
	"time"

	pkgscanner "github.com/infracost/cli/pkg/scanner"
	repoconfig "github.com/infracost/config"
	"github.com/infracost/go-proto/pkg/diagnostic"
	"github.com/infracost/go-proto/pkg/rat"
	"github.com/infracost/proto/gen/go/infracost/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRunInput_Metadata_CIPlatformAndVCSProvider(t *testing.T) {
	tests := []struct {
		name        string
		opts        RunInputOptions
		ciPlatform  string
		vcsProvider string
	}{
		{
			name:        "github actions",
			opts:        RunInputOptions{CIPlatform: "github_actions", VCSProvider: "github"},
			ciPlatform:  "github_actions",
			vcsProvider: "github",
		},
		{
			name:        "namespaces are independent",
			opts:        RunInputOptions{CIPlatform: "gitlab_ci", VCSProvider: "gitlab"},
			ciPlatform:  "gitlab_ci",
			vcsProvider: "gitlab",
		},
		{
			name:        "unrecognised platform",
			opts:        RunInputOptions{CIPlatform: "unknown", VCSProvider: "bitbucket"},
			ciPlatform:  "unknown",
			vcsProvider: "bitbucket",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := BuildRunInput(tt.opts)

			assert.Equal(t, tt.ciPlatform, input.Metadata["ciPlatform"])
			assert.Equal(t, tt.vcsProvider, input.Metadata["vcsProvider"])
		})
	}
}

func TestBuildErrorRunInput_Metadata_CIPlatformAndVCSProvider(t *testing.T) {
	input := BuildErrorRunInput(
		RunInputOptions{CIPlatform: "gitlab_ci", VCSProvider: "gitlab"},
		0, "Failed to scan", "boom",
	)

	assert.Equal(t, "gitlab_ci", input.Metadata["ciPlatform"])
	assert.Equal(t, "gitlab", input.Metadata["vcsProvider"])
}

func TestBuildRunInput_PullRequestURL(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		repoURL  string
		prNumber int
		wantURL  string
		wantID   string
	}{
		{
			name:     "github",
			provider: "github",
			repoURL:  "https://github.com/infracost/actions",
			prNumber: 42,
			wantURL:  "https://github.com/infracost/actions/pull/42",
			wantID:   "42",
		},
		{
			name:     "gitlab",
			provider: "gitlab",
			repoURL:  "https://gitlab.com/infracost/actions",
			prNumber: 42,
			wantURL:  "https://gitlab.com/infracost/actions/-/merge_requests/42",
			wantID:   "42",
		},
		{
			name:     "azure repos",
			provider: "azure_repos",
			repoURL:  "https://dev.azure.com/infracost/actions/_git/actions",
			prNumber: 42,
			wantURL:  "https://dev.azure.com/infracost/actions/_git/actions/pullrequest/42",
			wantID:   "42",
		},
		{
			name:     "bitbucket",
			provider: "bitbucket",
			repoURL:  "https://bitbucket.org/infracost/actions",
			prNumber: 42,
			wantURL:  "https://bitbucket.org/infracost/actions/pull-requests/42",
			wantID:   "42",
		},
		{
			name:     "branch run has no pull request",
			provider: "gitlab",
			repoURL:  "https://gitlab.com/infracost/actions",
			prNumber: 0,
		},
		{
			name:     "unbuildable url yields no pull request",
			provider: "gitlab",
			repoURL:  "git@gitlab.com:infracost/actions.git",
			prNumber: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := BuildRunInput(RunInputOptions{
				Command:     "comment",
				VCSProvider: tt.provider,
				RepoURL:     tt.repoURL,
				PRNumber:    tt.prNumber,
			})

			assertMetadata(t, input.Metadata, "vcsPullRequestUrl", tt.wantURL)
			assertMetadata(t, input.Metadata, "vcsPullRequestId", tt.wantID)

			repoMeta, ok := input.Metadata["repoMetadata"].(map[string]any)
			require.True(t, ok, "repoMetadata should be an object")
			assertMetadata(t, repoMeta, "vcsPullRequestUrl", tt.wantURL)
		})
	}
}

// assertMetadata treats an absent key as empty: the PR fields are omitempty.
func assertMetadata(t *testing.T, metadata map[string]any, key, want string) {
	t.Helper()

	if want == "" {
		assert.Empty(t, metadata[key], key)
		return
	}
	assert.Equal(t, want, metadata[key], key)
}

func TestBuildRunInput_TimeGenerated(t *testing.T) {
	t.Run("honoured when set", func(t *testing.T) {
		input := BuildRunInput(RunInputOptions{TimeGenerated: "2020-01-02T03:04:05Z"})
		assert.Equal(t, "2020-01-02T03:04:05Z", input.TimeGenerated)
	})

	t.Run("defaults to now", func(t *testing.T) {
		before := time.Now().UTC().Add(-time.Second)
		input := BuildRunInput(RunInputOptions{})

		got, err := time.Parse(time.RFC3339, input.TimeGenerated)
		require.NoError(t, err)
		assert.False(t, got.Before(before))
	})
}

func TestBuildRunInput_BreakdownSha(t *testing.T) {
	project := &repoconfig.Project{Name: "project-a", Path: "a"}
	input := BuildRunInput(RunInputOptions{
		HeadResult: &DirectoryResult{
			Projects: []pkgscanner.ProjectResult{{Name: "project-a", Config: project}},
		},
	})

	require.Len(t, input.ProjectResults, 1)
	assert.Equal(t, project.ConfigSHA(), input.ProjectResults[0].BreakdownSha)
	assert.Equal(t, input.ProjectResults[0].Metadata.ConfigSha, input.ProjectResults[0].BreakdownSha)
}

// A project with no config has nothing stable to dedup on, so the field is
// omitted rather than sent empty.
func TestBuildRunInput_BreakdownShaOmittedWithoutConfig(t *testing.T) {
	input := BuildRunInput(RunInputOptions{
		HeadResult: &DirectoryResult{Projects: []pkgscanner.ProjectResult{{Name: "project-a"}}},
	})

	require.Len(t, input.ProjectResults, 1)
	assert.Empty(t, input.ProjectResults[0].BreakdownSha)
}

// A quota-failed project carries the dashboard's run-quota code (401) rather
// than the generic breakdown error the run used to report.
func TestBuildRunInput_RunQuotaDiagnostic(t *testing.T) {
	input := BuildRunInput(RunInputOptions{
		BaseResult: &DirectoryResult{
			Projects: []pkgscanner.ProjectResult{
				{Name: "project-a", TotalMonthlyCost: rat.New(500)},
				{Name: "project-b", TotalMonthlyCost: rat.New(500)},
			},
		},
		HeadResult: &DirectoryResult{
			Projects:            []pkgscanner.ProjectResult{{Name: "project-a"}, {Name: "project-b"}},
			RunQuotaMessage:     "Infracost free monthly run limit exceeded. Please contact hello@infracost.io to purchase usage pack.",
			QuotaFailedProjects: map[string]bool{"project-a": true},
		},
	})

	require.Len(t, input.ProjectResults, 2)
	require.Len(t, input.ProjectResults[0].Metadata.Errors, 1)
	assert.Equal(t, diagnostic.CodeRunQuotaExceeded, input.ProjectResults[0].Metadata.Errors[0].Code)
	assert.Contains(t, input.ProjectResults[0].Metadata.Errors[0].Message, "hello@infracost.io")
	assert.True(t, input.ProjectResults[0].Metadata.Errors[0].IsError)

	// No breakdown or diff for the quota-failed project: its zero cost is
	// missing data, not a $500 saving.
	assert.Nil(t, input.ProjectResults[0].Breakdown)
	assert.Nil(t, input.ProjectResults[0].Diff)
	assert.Nil(t, input.ProjectResults[0].PastBreakdown)

	assert.Empty(t, input.ProjectResults[1].Metadata.Errors)
	require.NotNil(t, input.ProjectResults[1].Breakdown)
	require.NotNil(t, input.ProjectResults[1].Diff)
	assert.Equal(t, "-500", input.ProjectResults[1].Diff.TotalMonthlyCost)
}

// A base-branch quota failure carries the same diagnostic: the head scan
// succeeded, but the project's past cost is missing for the same reason.
func TestBuildRunInput_RunQuotaDiagnosticFromBase(t *testing.T) {
	input := BuildRunInput(RunInputOptions{
		BaseResult: &DirectoryResult{
			Projects:            []pkgscanner.ProjectResult{{Name: "project-a"}},
			RunQuotaMessage:     "Infracost free monthly run limit exceeded. Please contact hello@infracost.io to purchase usage pack.",
			QuotaFailedProjects: map[string]bool{"project-a": true},
		},
		HeadResult: &DirectoryResult{
			Projects: []pkgscanner.ProjectResult{{Name: "project-a", TotalMonthlyCost: rat.New(200)}},
		},
	})

	require.Len(t, input.ProjectResults, 1)
	require.Len(t, input.ProjectResults[0].Metadata.Errors, 1)
	assert.Equal(t, diagnostic.CodeRunQuotaExceeded, input.ProjectResults[0].Metadata.Errors[0].Code)
	assert.Nil(t, input.ProjectResults[0].Diff)
	assert.Nil(t, input.ProjectResults[0].PastBreakdown)

	// The head scan priced this project, so its own breakdown still uploads.
	require.NotNil(t, input.ProjectResults[0].Breakdown)
	assert.Equal(t, "200", input.ProjectResults[0].Breakdown.TotalMonthlyCost)
}

// priced returns a resource with a single monthly cost component, which is the
// shape most of the breakdown assertions below need.
func priced(name string, monthlyCost string) *provider.Resource {
	return &provider.Resource{
		Name:        name,
		Type:        "aws_instance",
		IsSupported: true,
		Costs: &provider.ResourceCosts{
			Components: []*provider.CostComponent{
				{
					Name:        "Compute",
					Unit:        "hours",
					PeriodPrice: &provider.PeriodPrice{Price: ratProto(monthlyCost), Period: provider.Period_MONTH},
					Quantity:    ratProto("1"),
				},
			},
		},
	}
}

// The dashboard reads the Infracost output format, not the provider proto's
// wire shape, so the uploaded resource must carry costComponents, subresources
// and the deep checksum. See TRK-1542.
func TestBuildRunInput_BreakdownResourceShape(t *testing.T) {
	resource := priced("aws_instance.web", "10")
	resource.Metadata = &provider.ResourceMetadata{
		DeepChecksum:        "deep-123",
		BasicChecksum:       "basic-123",
		DefaultTagsChecksum: "tags-123",
	}
	resource.Tagging = &provider.Tagging{
		Tags: []*provider.Tag{
			{Key: "env", Value: "prod"},
			{Key: "owner", Value: "platform", IsDefault: true},
		},
	}
	resource.ChildResources = []*provider.Resource{priced("root_block_device", "5")}

	input := BuildRunInput(RunInputOptions{
		HeadResult: &DirectoryResult{
			Projects: []pkgscanner.ProjectResult{
				{Name: "project-a", TotalMonthlyCost: rat.New(15), Resources: []*provider.Resource{resource}},
			},
		},
	})

	require.Len(t, input.ProjectResults, 1)
	require.NotNil(t, input.ProjectResults[0].Breakdown)
	require.Len(t, input.ProjectResults[0].Breakdown.Resources, 1)

	got := input.ProjectResults[0].Breakdown.Resources[0]
	assert.Equal(t, "aws_instance.web", got.Name)
	assert.Equal(t, "aws_instance", got.ResourceType)
	assert.Equal(t, "15", got.MonthlyCost)

	// The dashboard stores this as the finops policy resource's parserChecksum.
	require.NotNil(t, got.Metadata)
	assert.Equal(t, "deep-123", got.Metadata.Checksum)
	assert.Equal(t, "tags-123", got.Metadata.DefaultTagsChecksum)

	assert.Equal(t, map[string]string{"env": "prod"}, got.Tags)
	assert.Equal(t, map[string]string{"owner": "platform"}, got.DefaultTags)

	require.Len(t, got.CostComponents, 1)
	assert.Equal(t, "Compute", got.CostComponents[0].Name)
	assert.Equal(t, "hours", got.CostComponents[0].Unit)
	assert.Equal(t, "10", got.CostComponents[0].MonthlyCost)

	require.Len(t, got.SubResources, 1)
	assert.Equal(t, "root_block_device", got.SubResources[0].Name)
	assert.Equal(t, "5", got.SubResources[0].MonthlyCost)
}

// An unsupported resource with no cost belongs in freeResources, and an
// unsupported resource with costs is dropped, matching the runner's
// outputBreakdown.
func TestBuildRunInput_BreakdownFreeAndUnsupportedResources(t *testing.T) {
	supportedFree := &provider.Resource{Name: "aws_iam_role.a", IsSupported: true, IsFree: true}
	unsupportedFree := &provider.Resource{Name: "aws_unknown.b", IsFree: true}
	unsupported := &provider.Resource{Name: "aws_unknown.c"}

	input := BuildRunInput(RunInputOptions{
		HeadResult: &DirectoryResult{
			Projects: []pkgscanner.ProjectResult{
				{
					Name:      "project-a",
					Resources: []*provider.Resource{priced("aws_instance.web", "10"), supportedFree, unsupportedFree, unsupported},
				},
			},
		},
	})

	breakdown := input.ProjectResults[0].Breakdown
	require.NotNil(t, breakdown)

	names := make([]string, 0, len(breakdown.Resources))
	for _, r := range breakdown.Resources {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"aws_instance.web", "aws_iam_role.a"}, names)

	require.Len(t, breakdown.FreeResources, 1)
	assert.Equal(t, "aws_unknown.b", breakdown.FreeResources[0].Name)
}

// has_diff in the dashboard is diff.resources.length > 0, so a run with a cost
// change must upload one diff resource per changed resource.
func TestBuildRunInput_DiffResources(t *testing.T) {
	tests := []struct {
		name          string
		base          []*provider.Resource
		head          []*provider.Resource
		wantResources []string
		wantCosts     []string
	}{
		{
			name:          "resource added",
			base:          []*provider.Resource{priced("aws_instance.web", "10")},
			head:          []*provider.Resource{priced("aws_instance.web", "10"), priced("aws_instance.api", "5")},
			wantResources: []string{"aws_instance.api"},
			wantCosts:     []string{"5"},
		},
		{
			name:          "resource removed",
			base:          []*provider.Resource{priced("aws_instance.web", "10"), priced("aws_instance.api", "5")},
			head:          []*provider.Resource{priced("aws_instance.web", "10")},
			wantResources: []string{"aws_instance.api"},
			wantCosts:     []string{"-5"},
		},
		{
			name:          "cost changed",
			base:          []*provider.Resource{priced("aws_instance.web", "10")},
			head:          []*provider.Resource{priced("aws_instance.web", "25")},
			wantResources: []string{"aws_instance.web"},
			wantCosts:     []string{"15"},
		},
		{
			name:          "unchanged resource is omitted",
			base:          []*provider.Resource{priced("aws_instance.web", "10")},
			head:          []*provider.Resource{priced("aws_instance.web", "10")},
			wantResources: nil,
			wantCosts:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := BuildRunInput(RunInputOptions{
				BaseResult: &DirectoryResult{
					Projects: []pkgscanner.ProjectResult{{Name: "project-a", Resources: tt.base}},
				},
				HeadResult: &DirectoryResult{
					Projects: []pkgscanner.ProjectResult{{Name: "project-a", Resources: tt.head}},
				},
			})

			require.Len(t, input.ProjectResults, 1)
			require.NotNil(t, input.ProjectResults[0].Diff)

			names := make([]string, 0, len(input.ProjectResults[0].Diff.Resources))
			costs := make([]string, 0, len(input.ProjectResults[0].Diff.Resources))
			for _, r := range input.ProjectResults[0].Diff.Resources {
				names = append(names, r.Name)
				costs = append(costs, r.MonthlyCost)
			}

			if tt.wantResources == nil {
				assert.Empty(t, names)
				return
			}
			assert.Equal(t, tt.wantResources, names)
			assert.Equal(t, tt.wantCosts, costs)
		})
	}
}

// A resource whose total is unchanged but whose components shifted still counts
// as a diff, matching the dashboard's hasDiff criterion.
func TestBuildRunInput_DiffResourcesComponentsShiftedTotalUnchanged(t *testing.T) {
	base := priced("aws_instance.web", "10")
	head := priced("aws_instance.web", "4")
	head.Costs.Components = append(head.Costs.Components, &provider.CostComponent{
		Name:        "Storage",
		Unit:        "GB",
		PeriodPrice: &provider.PeriodPrice{Price: ratProto("6"), Period: provider.Period_MONTH},
		Quantity:    ratProto("1"),
	})

	input := BuildRunInput(RunInputOptions{
		BaseResult: &DirectoryResult{
			Projects: []pkgscanner.ProjectResult{{Name: "project-a", Resources: []*provider.Resource{base}}},
		},
		HeadResult: &DirectoryResult{
			Projects: []pkgscanner.ProjectResult{{Name: "project-a", Resources: []*provider.Resource{head}}},
		},
	})

	diff := input.ProjectResults[0].Diff
	require.NotNil(t, diff)
	require.Len(t, diff.Resources, 1)
	assert.Equal(t, "aws_instance.web", diff.Resources[0].Name)
	assert.Equal(t, "0", diff.Resources[0].MonthlyCost)

	require.Len(t, diff.Resources[0].CostComponents, 2)
	assert.Equal(t, "Compute", diff.Resources[0].CostComponents[0].Name)
	assert.Equal(t, "-6", diff.Resources[0].CostComponents[0].MonthlyCost)
	assert.Equal(t, "Storage", diff.Resources[0].CostComponents[1].Name)
	assert.Equal(t, "6", diff.Resources[0].CostComponents[1].MonthlyCost)
}
