package config

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/infracost/ci/v2/internal/api/dashboard"
	"github.com/infracost/ci/v2/internal/vcsurl"
	"github.com/infracost/ci/v2/internal/version"
	pkgscanner "github.com/infracost/cli/pkg/scanner"
	"github.com/infracost/go-proto/pkg/address"
	"github.com/infracost/go-proto/pkg/diagnostic"
	goprotoevent "github.com/infracost/go-proto/pkg/event"
	"github.com/infracost/go-proto/pkg/rat"
	"github.com/infracost/proto/gen/go/infracost/provider"
	"github.com/infracost/vcs/pkg/vcs/comment"
)

// RunInputOptions contains the parameters needed to build an addRun mutation input.
type RunInputOptions struct {
	BaseResult       *DirectoryResult
	HeadResult       *DirectoryResult
	GuardrailResults []goprotoevent.GuardrailResult
	BudgetResults    []goprotoevent.BudgetResult
	CommentPosted    bool
	Currency         string

	// TimeGenerated orders runs in the dashboard, which keeps the newest and
	// drops the rest. Empty means wall clock; a branch run sets the commit's
	// committer date so a re-run of an older commit loses.
	TimeGenerated string

	// Command identifies the type of run: "comment" for diff/PR runs,
	// "upload" for baseline scans.
	Command string

	// VCS metadata — used by the dashboard to create repo, branch, and PR records.
	CIPlatform        string
	VCSProvider       string
	RepoURL           string
	RepoID            string
	RepoName          string
	PRURL             string
	PRNumber          int
	PRTitle           string
	PRAuthor          string
	PRLabels          []string
	CommitSHA         string
	CommitMessage     string
	CommitAuthorName  string
	CommitAuthorEmail string
	CommitTimestamp   string
	BaseCommitSHA     string
	Branch            string
	BaseBranch        string
	PipelineRunID     string

	// Scan metadata — surfaced in the addRun mutation metadata.
	UsageAPIEnabled        bool
	UsageFilePath          string
	HasConfigFile          bool
	ConfigFileHasUsageFile bool
}

// runMetadata is the top-level metadata sent with the addRun mutation.
// The dashboard reads specific fields from this to create VCS records,
// store commit info, and control run behavior.
type runMetadata struct {
	// Run context
	Command    string `json:"command"`
	Version    string `json:"version"`
	CIPlatform string `json:"ciPlatform"`

	// VCS provider
	VCSProvider string `json:"vcsProvider"`

	// Repository
	VCSRepositoryURL string `json:"vcsRepositoryUrl"`
	RepoID           string `json:"repoId,omitempty"`
	RepoName         string `json:"repoName,omitempty"`

	// Branch
	VCSBranch     string `json:"vcsBranch"`
	VCSBaseBranch string `json:"vcsBaseBranch,omitempty"`

	// Commit
	VCSCommitSHA         string `json:"vcsCommitSha"`
	VCSCommitMessage     string `json:"vcsCommitMessage,omitempty"`
	VCSCommitAuthorName  string `json:"vcsCommitAuthorName,omitempty"`
	VCSCommitAuthorEmail string `json:"vcsCommitAuthorEmail,omitempty"`
	VCSCommitTimestamp   string `json:"vcsCommitTimestamp,omitempty"`
	VCSBaseCommitSHA     string `json:"vcsBaseCommitSha,omitempty"`

	// Pull request (only for diff/comment runs)
	VCSPullRequestURL    string   `json:"vcsPullRequestUrl,omitempty"`
	VCSPullRequestID     string   `json:"vcsPullRequestId,omitempty"`
	VCSPullRequestTitle  string   `json:"vcsPullRequestTitle,omitempty"`
	VCSPullRequestAuthor string   `json:"vcsPullRequestAuthor,omitempty"`
	VCSPullRequestLabels []string `json:"vcsPullRequestLabels,omitempty"`

	// CI pipeline
	VCSPipelineRunID string `json:"vcsPipelineRunId,omitempty"`

	// Unused — these are internal to the runner's webhook/replay system and
	// not applicable to GitHub Actions.
	// VCSWebhookDeliveryID string `json:"vcsWebhookDeliveryId,omitempty"`
	// ReplayID             string `json:"replayId,omitempty"`

	// Dashboard
	DashboardEnabled bool `json:"dashboardEnabled"`

	// Usage / config metadata
	UsageAPIEnabled        bool   `json:"usageApiEnabled,omitempty"`
	UsageFilePath          string `json:"usageFilePath,omitempty"`
	HasConfigFile          bool   `json:"hasConfigFile,omitempty"`
	ConfigFileHasUsageFile bool   `json:"configFileHasUsageFile,omitempty"`

	// Nested repo metadata for backwards compatibility — the dashboard
	// merges this into combinedMetadata alongside top-level fields.
	RepoMetadata repoMetadata `json:"repoMetadata"`
}

// repoMetadata is the nested metadata the dashboard merges with top-level fields.
type repoMetadata struct {
	VCSRepositoryURL  string `json:"vcsRepositoryUrl"`
	VCSBranch         string `json:"vcsBranch"`
	VCSBaseBranch     string `json:"vcsBaseBranch,omitempty"`
	VCSCommitSHA      string `json:"vcsCommitSha"`
	VCSPullRequestURL string `json:"vcsPullRequestUrl,omitempty"`
}

// BuildErrorRunInput creates a run input that reports a top-level error to the
// dashboard. This should be called when the scanner cannot complete a run (e.g.
// scan failure) so the dashboard records the attempt.
func BuildErrorRunInput(opts RunInputOptions, code diagnostic.ErrorCode, title, description string) dashboard.RunInput {
	opts.Command = "error"
	input := BuildRunInput(opts)
	input.Error = &dashboard.RunError{
		Code:        fmt.Sprintf("%d", code),
		Level:       "error",
		Title:       title,
		Description: description,
	}
	return input
}

// BuildRunInput converts scan results into the dashboard addRun mutation input.
func BuildRunInput(opts RunInputOptions) dashboard.RunInput {
	if opts.HeadResult == nil {
		return buildRunInputFromMetadata(opts, nil)
	}

	projectResults := make([]dashboard.ProjectResultInput, 0, len(opts.HeadResult.Projects))

	var baseByName map[string]*pkgscanner.ProjectResult
	if opts.BaseResult != nil {
		baseByName = make(map[string]*pkgscanner.ProjectResult, len(opts.BaseResult.Projects))
		for i := range opts.BaseResult.Projects {
			baseByName[opts.BaseResult.Projects[i].Name] = &opts.BaseResult.Projects[i]
		}
	}

	for i := range opts.HeadResult.Projects {
		head := &opts.HeadResult.Projects[i]
		base := baseByName[head.Name]

		pr := dashboard.ProjectResultInput{
			ProjectName: head.Name,
		}

		headQuotaFailed := opts.HeadResult.QuotaFailedProjects[head.Name]
		baseQuotaFailed := opts.BaseResult != nil && opts.BaseResult.QuotaFailedProjects[head.Name]

		// A quota-failed project has no cost, so uploading a breakdown would
		// record it at $0 and a diff would report the other side's whole cost.
		if !headQuotaFailed {
			breakdown := buildBreakdownInput(head)
			pr.Breakdown = &breakdown
		}

		if base != nil && !headQuotaFailed && !baseQuotaFailed {
			pastBreakdown := buildBreakdownInput(base)
			pr.PastBreakdown = &pastBreakdown
			diff := buildDiffBreakdownInput(base, head)
			pr.Diff = &diff
		}

		var workspace string
		var projectType string
		var projectPath string
		var isTerraform bool
		if head.Config != nil {
			workspace = head.Config.Terraform.Workspace
			projectType = string(head.Config.Type)
			projectPath = head.Config.Path
			isTerraform = head.Config.Type == "terraform" || head.Config.Type == "terragrunt"
			pr.BreakdownSha = head.Config.ConfigSHA()
		}

		var quotaMessage string
		if headQuotaFailed {
			quotaMessage = truncateRunQuotaMessage(opts.HeadResult.RunQuotaMessage)
		} else if baseQuotaFailed {
			quotaMessage = truncateRunQuotaMessage(opts.BaseResult.RunQuotaMessage)
		}

		pr.Metadata = buildProjectMetadata(head, workspace, projectType, projectPath, opts.Command == "comment", quotaMessage)
		pr.FinopsPolicyResults = buildFinopsPolicyResults(head.FinopsResults, head.Resources, head.Name, workspace, isTerraform)
		pr.TagPolicyResults = buildTagPolicyResults(head.TagPolicyResults)

		projectResults = append(projectResults, pr)
	}

	return buildRunInputFromMetadata(opts, projectResults)
}

func buildRunInputFromMetadata(opts RunInputOptions, projectResults []dashboard.ProjectResultInput) dashboard.RunInput {
	// diff resolves the URL before scanning, so this only builds one for a
	// caller that supplied a number alone. Dropped to keep BuildRunInput
	// pure: scan never sets either, and diff has already rejected a bad one.
	prURL := opts.PRURL
	if prURL == "" {
		prURL, _ = vcsurl.PullRequest(opts.VCSProvider, opts.RepoURL, opts.PRNumber)
	}
	var prID string
	if prURL != "" && opts.PRNumber > 0 {
		prID = fmt.Sprintf("%d", opts.PRNumber)
	}

	metadata := runMetadata{
		Command:                opts.Command,
		Version:                version.Version,
		CIPlatform:             opts.CIPlatform,
		VCSProvider:            opts.VCSProvider,
		VCSRepositoryURL:       opts.RepoURL,
		RepoID:                 opts.RepoID,
		RepoName:               opts.RepoName,
		VCSBranch:              opts.Branch,
		VCSBaseBranch:          opts.BaseBranch,
		VCSCommitSHA:           opts.CommitSHA,
		VCSCommitMessage:       opts.CommitMessage,
		VCSCommitAuthorName:    opts.CommitAuthorName,
		VCSCommitAuthorEmail:   opts.CommitAuthorEmail,
		VCSCommitTimestamp:     opts.CommitTimestamp,
		VCSBaseCommitSHA:       opts.BaseCommitSHA,
		VCSPullRequestURL:      prURL,
		VCSPullRequestID:       prID,
		VCSPullRequestTitle:    opts.PRTitle,
		VCSPullRequestAuthor:   opts.PRAuthor,
		VCSPullRequestLabels:   opts.PRLabels,
		VCSPipelineRunID:       opts.PipelineRunID,
		DashboardEnabled:       true,
		UsageAPIEnabled:        opts.UsageAPIEnabled,
		UsageFilePath:          opts.UsageFilePath,
		HasConfigFile:          opts.HasConfigFile,
		ConfigFileHasUsageFile: opts.ConfigFileHasUsageFile,
		RepoMetadata: repoMetadata{
			VCSRepositoryURL:  opts.RepoURL,
			VCSBranch:         opts.Branch,
			VCSBaseBranch:     opts.BaseBranch,
			VCSCommitSHA:      opts.CommitSHA,
			VCSPullRequestURL: prURL,
		},
	}

	timeGenerated := opts.TimeGenerated
	if timeGenerated == "" {
		timeGenerated = time.Now().UTC().Format(time.RFC3339)
	}

	posted := opts.CommentPosted
	input := dashboard.RunInput{
		ProjectResults:           projectResults,
		Currency:                 opts.Currency,
		TimeGenerated:            timeGenerated,
		PoliciesAlreadyEvaluated: true,
		ClientPostedComment:      &posted,
		Metadata:                 structToJSON(metadata),
	}

	if len(opts.GuardrailResults) > 0 {
		input.GuardrailResults = buildGuardrailResults(opts.GuardrailResults)
	}

	if len(opts.BudgetResults) > 0 {
		input.BudgetResults = buildBudgetResults(opts.BudgetResults)
	}

	return input
}

func buildBreakdownInput(result *pkgscanner.ProjectResult) dashboard.BreakdownInput {
	monthlyCost := orZero(result.TotalMonthlyCost)
	resources, freeResources := convertBreakdownResources(result.Resources)

	return dashboard.BreakdownInput{
		TotalHourlyCost:  monthlyCost.Div(rat.New(730)).String(),
		TotalMonthlyCost: monthlyCost.String(),
		Resources:        resources,
		FreeResources:    freeResources,
	}
}

func buildDiffBreakdownInput(base, head *pkgscanner.ProjectResult) dashboard.BreakdownInput {
	headCost := orZero(head.TotalMonthlyCost)
	baseCost := orZero(base.TotalMonthlyCost)
	diffCost := headCost.Sub(baseCost)

	// The same comparison the PR comment uses, over the same filtered set the
	// breakdown gets, so the uploaded diff and the comment cannot disagree.
	pastBreakdown := buildCostBreakdown(supportedResources(base.Resources))
	currentBreakdown := buildCostBreakdown(supportedResources(head.Resources))

	var resources []dashboard.BreakdownResourceInput
	if diff := diffCostBreakdown(pastBreakdown, currentBreakdown); diff != nil {
		// A diff resource carries only a name and costs, so type and tags come from
		// the source resource: head first, then base for a removed one.
		resources = convertDiffResources(diff.Resources, indexResources(head.Resources, base.Resources))
	}

	return dashboard.BreakdownInput{
		TotalHourlyCost:  diffCost.Div(rat.New(730)).String(),
		TotalMonthlyCost: diffCost.String(),
		Resources:        resources,
	}
}

// convertBreakdownResources splits resources the way the runner's outputBreakdown
// does: free-and-unsupported to freeResources, unsupported dropped with its costs.
func convertBreakdownResources(resources []*provider.Resource) (supported, free []dashboard.BreakdownResourceInput) {
	for _, r := range resources {
		if !r.IsSupported && !r.IsFree {
			continue
		}

		res, _, _ := convertBreakdownResource(r)
		if r.IsSupported {
			supported = append(supported, res)
		} else {
			free = append(free, res)
		}
	}
	return supported, free
}

// supportedResources keeps the set that reaches breakdown.resources, so the diff
// describes the same one. A diff has no equivalent of freeResources.
func supportedResources(resources []*provider.Resource) []*provider.Resource {
	out := make([]*provider.Resource, 0, len(resources))
	for _, r := range resources {
		if r.IsSupported {
			out = append(out, r)
		}
	}
	return out
}

// indexChildResources maps a resource's children by name, so a diff
// sub-resource can be matched to the resource it came from.
func indexChildResources(r *provider.Resource) map[string]*provider.Resource {
	if r == nil {
		return nil
	}
	return indexResources(r.ChildResources)
}

// indexResources maps resources by name, preferring the first list. A diff
// resource names a resource that may exist only on one side of the comparison.
func indexResources(lists ...[]*provider.Resource) map[string]*provider.Resource {
	index := map[string]*provider.Resource{}
	for _, list := range lists {
		for _, r := range list {
			if _, ok := index[r.Name]; !ok {
				index[r.Name] = r
			}
		}
	}
	return index
}

// convertBreakdownResource returns the resource alongside its rolled-up hourly
// and monthly costs, so a parent can sum its children without reparsing them.
func convertBreakdownResource(r *provider.Resource) (dashboard.BreakdownResourceInput, *rat.Rat, *rat.Rat) {
	out := dashboard.BreakdownResourceInput{
		Name:         r.Name,
		ResourceType: r.Type,
	}

	if r.Metadata != nil {
		out.Metadata = &dashboard.BreakdownResourceMetadataInput{
			Checksum:            r.Metadata.DeepChecksum,
			DefaultTagsChecksum: r.Metadata.DefaultTagsChecksum,
		}
	}

	out.Tags, out.DefaultTags = splitTags(r.Tagging)

	hourlyCost := rat.Zero
	monthlyCost := rat.Zero

	if r.Costs != nil {
		for _, c := range r.Costs.Components {
			cc, hourly, monthly := convertBreakdownCostComponent(c)
			out.CostComponents = append(out.CostComponents, cc)
			hourlyCost = hourlyCost.Add(hourly)
			monthlyCost = monthlyCost.Add(monthly)
		}
	}

	for _, child := range r.ChildResources {
		sub, hourly, monthly := convertBreakdownResource(child)
		out.SubResources = append(out.SubResources, sub)
		hourlyCost = hourlyCost.Add(hourly)
		monthlyCost = monthlyCost.Add(monthly)
	}

	out.HourlyCost = hourlyCost.String()
	out.MonthlyCost = monthlyCost.String()

	return out, hourlyCost, monthlyCost
}

func convertBreakdownCostComponent(c *provider.CostComponent) (dashboard.BreakdownCostComponentInput, *rat.Rat, *rat.Rat) {
	hourlyCost := rat.Zero
	monthlyCost := rat.Zero

	out := dashboard.BreakdownCostComponentInput{
		Name:            c.Name,
		Unit:            c.Unit,
		UsageBased:      c.UsageBased,
		Price:           "0",
		HourlyCost:      "0",
		MonthlyCost:     "0",
		HourlyQuantity:  "0",
		MonthlyQuantity: "0",
	}

	if c.PeriodPrice == nil || c.Quantity == nil {
		return out, hourlyCost, monthlyCost
	}

	hourlyQty, monthlyQty := pkgscanner.ConvertQuantityByPeriod(rat.FromProto(c.Quantity), c.PeriodPrice.Period)
	out.HourlyQuantity = hourlyQty.String()
	out.MonthlyQuantity = monthlyQty.String()

	if c.PeriodPrice.Price != nil {
		price := pkgscanner.ApplyDiscount(rat.FromProto(c.PeriodPrice.Price), rat.FromProto(c.DiscountRate))
		hourlyCost = hourlyQty.Mul(price)
		monthlyCost = monthlyQty.Mul(price)
		out.Price = price.String()
		out.HourlyCost = hourlyCost.String()
		out.MonthlyCost = monthlyCost.String()
	}

	return out, hourlyCost, monthlyCost
}

// splitTags separates user tags from provider default tags, which the dashboard
// reads as two fields.
func splitTags(tagging *provider.Tagging) (tags, defaultTags map[string]string) {
	if tagging == nil {
		return nil, nil
	}

	for _, t := range tagging.Tags {
		if t.IsDefault {
			if defaultTags == nil {
				defaultTags = map[string]string{}
			}
			defaultTags[t.Key] = t.Value
			continue
		}
		if tags == nil {
			tags = map[string]string{}
		}
		tags[t.Key] = t.Value
	}

	return tags, defaultTags
}

// convertDiffResources maps diff resources onto the upload shape. Their costs are
// deltas, so hourly derives from monthly; byName supplies the type and tags.
func convertDiffResources(resources []comment.BreakdownResource, byName map[string]*provider.Resource) []dashboard.BreakdownResourceInput {
	if len(resources) == 0 {
		return nil
	}

	out := make([]dashboard.BreakdownResourceInput, 0, len(resources))
	for _, r := range resources {
		// The comment lists any added or removed resource as changed. Uploading a
		// zero-dollar one would set the dashboard's has_diff; the runner's does not.
		monthlyCost := orZero(r.MonthlyCost)
		if monthlyCost.IsZero() && len(r.CostComponents) == 0 && len(r.SubResources) == 0 {
			continue
		}

		source := byName[r.Name]
		dr := dashboard.BreakdownResourceInput{
			Name:         r.Name,
			HourlyCost:   monthlyCost.Div(rat.New(730)).String(),
			MonthlyCost:  monthlyCost.String(),
			SubResources: convertDiffResources(r.SubResources, indexChildResources(source)),
		}

		if source != nil {
			dr.ResourceType = source.Type
			dr.Tags, dr.DefaultTags = splitTags(source.Tagging)
		}

		for _, c := range r.CostComponents {
			ccMonthlyCost := orZero(c.MonthlyCost)
			ccMonthlyQty := orZero(c.MonthlyQuantity)
			dr.CostComponents = append(dr.CostComponents, dashboard.BreakdownCostComponentInput{
				Name:            c.Name,
				Unit:            c.Unit,
				UsageBased:      c.UsageBased,
				Price:           orZero(c.Price).String(),
				HourlyCost:      ccMonthlyCost.Div(rat.New(730)).String(),
				MonthlyCost:     ccMonthlyCost.String(),
				HourlyQuantity:  ccMonthlyQty.Div(rat.New(730)).String(),
				MonthlyQuantity: ccMonthlyQty.String(),
			})
		}

		out = append(out, dr)
	}

	return out
}

func buildBudgetResults(results []goprotoevent.BudgetResult) []dashboard.BudgetResultInput {
	out := make([]dashboard.BudgetResultInput, 0, len(results))
	for _, br := range results {
		tags := make([]dashboard.BudgetTagInput, 0, len(br.Tags))
		for _, t := range br.Tags {
			tags = append(tags, dashboard.BudgetTagInput{Key: t.Key, Value: t.Value})
		}
		out = append(out, dashboard.BudgetResultInput{
			BudgetID:             br.BudgetID,
			Tags:                 tags,
			StartDate:            br.StartDate.Format("2006-01-02T15:04:05Z"),
			EndDate:              br.EndDate.Format("2006-01-02T15:04:05Z"),
			Amount:               ratString(br.Amount),
			CurrentCost:          ratString(br.CurrentCost),
			CustomOverrunMessage: br.CustomOverrunMessage,
		})
	}
	return out
}

func buildGuardrailResults(results []goprotoevent.GuardrailResult) []dashboard.GuardrailResultInput {
	out := make([]dashboard.GuardrailResultInput, 0, len(results))
	for _, gr := range results {
		out = append(out, dashboard.GuardrailResultInput{
			GuardrailID:            gr.GuardrailID,
			Triggered:              gr.Triggered,
			PRComment:              gr.PRComment,
			BlockPR:                gr.BlockPR,
			TriggeringProjectNames: gr.TriggeringProjectNames,
			Increase:               ratString(gr.Increase),
			PercentIncrease:        ratString(gr.PercentIncrease),
			TotalMonthlyCost:       ratString(gr.TotalMonthlyCost),
		})
	}
	return out
}

func buildFinopsPolicyResults(results []*provider.FinopsPolicyResult, resources []*provider.Resource, projectName, workspace string, isTerraform bool) []dashboard.FinopsPolicyResultInput {
	if len(results) == 0 {
		return nil
	}

	resourceMap := make(map[string]*provider.Resource, len(resources))
	for _, r := range resources {
		resourceMap[r.Id] = r
	}

	out := make([]dashboard.FinopsPolicyResultInput, 0, len(results))
	for _, r := range results {
		out = append(out, buildFinopsPolicyResult(r, resourceMap, projectName, workspace, isTerraform))
	}
	return out
}

func buildFinopsPolicyResult(result *provider.FinopsPolicyResult, resourceMap map[string]*provider.Resource, projectName, workspace string, isTerraform bool) dashboard.FinopsPolicyResultInput {
	allCurrentResources := make([]string, 0, len(result.PassingResourceIds)+len(result.FailingResources))
	failingResources := make([]dashboard.FinopsPolicyResourceInput, 0, len(result.FailingResources))
	passingResources := make([]dashboard.FinopsPolicyPassingResourceInput, 0, len(result.PassingResourceIds))

	for _, failing := range result.FailingResources {
		resource, ok := resourceMap[failing.Id]
		if !ok {
			continue
		}

		var modulePath, moduleCallPath string
		var moduleCallLine int
		stack := resource.CallStack
		if stack != nil && len(stack.Frames) > 1 {
			modulePath = stack.Frames[1].Source
			moduleCallPath = address.FromProto(stack.Frames[0].Address).String()
			if stack.Frames[0].SourceRange != nil {
				moduleCallLine = int(stack.Frames[0].SourceRange.StartLine)
			}
		}

		var tfModule string
		if isTerraform {
			tfModule = modulePath
		}

		var startLine, endLine int
		var path, checksum, parserChecksum string
		if resource.Metadata != nil {
			startLine = int(resource.Metadata.StartLine)
			endLine = int(resource.Metadata.EndLine)
			path = resource.Metadata.Filename
			checksum = resource.Metadata.BasicChecksum
			parserChecksum = resource.Metadata.DeepChecksum
		}

		failingResources = append(failingResources, dashboard.FinopsPolicyResourceInput{
			NewResource:          resource.Action == provider.ResourceAction_CREATE,
			Address:              failing.CauseAddress,
			ResourceType:         resource.Type,
			Path:                 path,
			ProjectName:          projectName,
			ModulePath:           modulePath,
			ModuleCallPath:       moduleCallPath,
			ModuleCallStartLine:  moduleCallLine,
			Issues:               buildFinopsIssues(failing.Issues),
			StartLine:            startLine,
			EndLine:              endLine,
			UnlabeledProjectName: projectName,
			TerraformModulePath:  tfModule,
			TerraformWorkspace:   workspace,
			Checksum:             checksum,
			ParserChecksum:       parserChecksum,
		})
		allCurrentResources = append(allCurrentResources, resource.Name)
	}

	for _, resourceID := range result.PassingResourceIds {
		resource, ok := resourceMap[resourceID]
		if !ok {
			continue
		}
		passingResources = append(passingResources, dashboard.FinopsPolicyPassingResourceInput{
			Address:     resource.Name,
			ProjectName: projectName,
		})
		allCurrentResources = append(allCurrentResources, resource.Name)
	}

	return dashboard.FinopsPolicyResultInput{
		Name:                     result.PolicyName,
		PolicyID:                 result.PolicyId,
		Message:                  result.PolicyMessage,
		BlockPR:                  result.BlockPullRequest,
		PRComment:                result.IncludeInPullRequestComment,
		OnlyNewResources:         result.OnlyAppliesToNewResources,
		AllCurrentResources:      allCurrentResources,
		Resources:                failingResources,
		PastResources:            []dashboard.FinopsPolicyResourceInput{},
		PassingResources:         passingResources,
		TotalApplicableResources: len(allCurrentResources),
	}
}

func buildFinopsIssues(issues []*provider.FinopsResourceIssue) []dashboard.FinopsPolicyIssueInput {
	out := make([]dashboard.FinopsPolicyIssueInput, 0, len(issues))
	for _, issue := range issues {
		m := dashboard.FinopsPolicyIssueInput{
			FromAddress: issue.Address,
			Attribute:   issue.Attribute,
			Description: issue.Description,
		}
		if issue.SavingsDetails != nil {
			m.SavingsDetails = issue.SavingsDetails
		}
		if issue.MonthlySavings != nil {
			s := rat.FromProto(issue.MonthlySavings).String()
			m.MonthlySavings = &s
		}
		if issue.MonthlyCarbonSavingsGramsCo2E != nil {
			s := rat.FromProto(issue.MonthlyCarbonSavingsGramsCo2E).String()
			m.MonthlyCarbonSavingsGramsCo2e = &s
		}
		if issue.MonthlyWaterSavingsLiters != nil {
			s := rat.FromProto(issue.MonthlyWaterSavingsLiters).String()
			m.MonthlyWaterSavingsLitres = &s
		}
		if len(issue.BeforeFixBreakdowns) > 0 {
			m.CurrentCostBreakdowns = buildIssueBreakdowns(issue.BeforeFixBreakdowns)
		}
		if len(issue.AfterFixBreakdowns) > 0 {
			m.NewCostBreakdowns = buildIssueBreakdowns(issue.AfterFixBreakdowns)
		}
		out = append(out, m)
	}
	return out
}

func buildIssueBreakdowns(breakdowns []*provider.IssueBreakdown) []dashboard.FinopsPolicyBreakdownInput {
	out := make([]dashboard.FinopsPolicyBreakdownInput, 0, len(breakdowns))
	for _, b := range breakdowns {
		totalMonthlyCost := rat.Zero
		totalHourlyCost := rat.Zero

		costComponents := make([]dashboard.FinopsPolicyCostComponentInput, 0, len(b.CostComponents))
		for _, cc := range b.CostComponents {
			converted := buildIssueCostComponent(cc)
			costComponents = append(costComponents, converted)
			if mcRat, err := rat.NewFromString(converted.MonthlyCost); err == nil {
				totalMonthlyCost = totalMonthlyCost.Add(mcRat)
			}
			if hcRat, err := rat.NewFromString(converted.HourlyCost); err == nil {
				totalHourlyCost = totalHourlyCost.Add(hcRat)
			}
		}

		m := dashboard.FinopsPolicyBreakdownInput{
			Name:           b.ResourceName,
			HourlyCost:     totalHourlyCost.String(),
			MonthlyCost:    totalMonthlyCost.String(),
			Metadata:       dashboard.FinopsPolicyBreakdownMetadataInput{Region: b.Region},
			CostComponents: costComponents,
			SubResources:   buildIssueBreakdowns(b.Subresources),
		}
		out = append(out, m)
	}
	return out
}

func buildIssueCostComponent(cc *provider.IssueCostComponent) dashboard.FinopsPolicyCostComponentInput {
	monthlyQty := rat.Zero
	hourlyQty := rat.Zero
	monthlyCost := rat.Zero
	hourlyCost := rat.Zero
	price := rat.Zero

	if cc.PeriodPrice != nil {
		qty := rat.FromProto(cc.Quantity)
		if qty == nil {
			qty = rat.Zero
		}
		hourlyQty, monthlyQty = convertQuantityByPeriod(qty, cc.PeriodPrice.Period)

		if cc.PeriodPrice.Price != nil {
			p := rat.FromProto(cc.PeriodPrice.Price)
			if p == nil {
				p = rat.Zero
			}
			discount := rat.FromProto(cc.DiscountRate)
			if discount == nil {
				discount = rat.Zero
			}
			price = applyDiscount(p, discount)
			monthlyCost = monthlyQty.Mul(price)
			hourlyCost = hourlyQty.Mul(price)
		}
	}

	return dashboard.FinopsPolicyCostComponentInput{
		Name:            cc.Name,
		Unit:            cc.Unit,
		Price:           price.String(),
		HourlyCost:      hourlyCost.String(),
		HourlyQuantity:  hourlyQty.String(),
		MonthlyCost:     monthlyCost.String(),
		MonthlyQuantity: monthlyQty.String(),
		UsageBased:      cc.UsageBased,
	}
}

func convertQuantityByPeriod(qty *rat.Rat, period provider.Period) (hourly, monthly *rat.Rat) {
	if qty == nil {
		return rat.Zero, rat.Zero
	}
	hoursPerMonth := rat.New(730)
	switch period {
	case provider.Period_HOUR:
		return qty, qty.Mul(hoursPerMonth)
	case provider.Period_MONTH:
		return qty.Div(hoursPerMonth), qty
	default:
		return rat.Zero, rat.Zero
	}
}

func applyDiscount(price, discountRate *rat.Rat) *rat.Rat {
	if discountRate == nil || discountRate.IsZero() {
		return price
	}
	return price.Sub(price.Mul(discountRate))
}

func buildTagPolicyResults(results []goprotoevent.TaggingPolicyResult) []map[string]any {
	if len(results) == 0 {
		return nil
	}
	var out []map[string]any
	for _, r := range results {
		out = append(out, structToJSON(r))
	}
	return out
}

// quotaMessage, when non-empty, records the org's run limit as a project error
// so the dashboard shows code 401 rather than a generic breakdown failure.
func buildProjectMetadata(project *pkgscanner.ProjectResult, workspace, projectType, projectPath string, isDiff bool, quotaMessage string) *dashboard.ProjectMetadataInput {
	meta := &dashboard.ProjectMetadataInput{
		Path:               projectPath,
		Type:               projectType,
		TerraformWorkspace: workspace,
		VCSSubPath:         projectPath,
	}

	if project.Config != nil {
		configSha := project.Config.ConfigSHA()
		meta.ConfigSha = configSha
	}

	if isDiff {
		codeChanged := true
		meta.VCSCodeChanged = &codeChanged
	}

	meta.RemoteModuleCalls = project.RemoteModuleCalls

	var errors []dashboard.ProjectDiagnostic
	var warnings []dashboard.ProjectDiagnostic
	for _, d := range project.Diagnostics {
		if !d.Critical && !d.Warning {
			continue
		}
		pd := dashboard.ProjectDiagnostic{
			Code:    diagnostic.DashboardCode(d.Type),
			Message: diagnostic.MessagePrefix(d.Type) + ": " + d.Error,
			IsError: d.Critical,
		}
		if d.Critical {
			errors = append(errors, pd)
		} else {
			warnings = append(warnings, pd)
		}
	}

	if quotaMessage != "" {
		errors = append(errors, dashboard.ProjectDiagnostic{
			Code:    diagnostic.CodeRunQuotaExceeded,
			Message: quotaMessage,
			IsError: true,
		})
	}

	meta.Errors = errors
	meta.Warnings = warnings

	return meta
}

func structToJSON(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func ratString(r *rat.Rat) string {
	if r == nil {
		return "0"
	}
	return r.String()
}
</merged><merged>
package config

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/infracost/ci/v2/internal/api/dashboard"
	"github.com/infracost/ci/v2/internal/vcsurl"
	"github.com/infracost/ci/v2/internal/version"
	pkgscanner "github.com/infracost/cli/pkg/scanner"
	"github.com/infracost/go-proto/pkg/address"
	"github.com/infracost/go-proto/pkg/diagnostic"
	goprotoevent "github.com/infracost/go-proto/pkg/event"
	"github.com/infracost/go-proto/pkg/rat"
	"github.com/infracost/proto/gen/go/infracost/provider"
	"github.com/infracost/vcs/pkg/vcs/comment"
)

// RunInputOptions contains the parameters needed to build an addRun mutation input.
type RunInputOptions struct {
	BaseResult       *DirectoryResult
	HeadResult       *DirectoryResult
	GuardrailResults []goprotoevent.GuardrailResult
	BudgetResults    []goprotoevent.BudgetResult
	CommentPosted    bool
	Currency         string

	// TimeGenerated orders runs in the dashboard, which keeps the newest and
	// drops the rest. Empty means wall clock; a branch run sets the commit's
	// committer date so a re-run of an older commit loses.
	TimeGenerated string

	// Command identifies the type of run: "comment" for diff/PR runs,
	// "upload" for baseline scans.
	Command string

	// VCS metadata — used by the dashboard to create repo, branch, and PR records.
	CIPlatform        string
	VCSProvider       string
	RepoURL           string
	RepoID            string
	RepoName          string
	PRURL             string
	PRNumber          int
	PRTitle           string
	PRAuthor          string
	PRLabels          []string
	CommitSHA         string
	CommitMessage     string
	CommitAuthorName  string
	CommitAuthorEmail string
	CommitTimestamp   string
	BaseCommitSHA     string
	Branch            string
	BaseBranch        string
	PipelineRunID     string

	// Scan metadata — surfaced in the addRun mutation metadata.
	UsageAPIEnabled        bool
	UsageFilePath          string
	HasConfigFile          bool
	ConfigFileHasUsageFile bool
}

// runMetadata is the top-level metadata sent with the addRun mutation.
// The dashboard reads specific fields from this to create VCS records,
// store commit info, and control run behavior.
type runMetadata struct {
	// Run context
	Command    string `json:"command"`
	Version    string `json:"version"`
	CIPlatform string `json:"ciPlatform"`

	// VCS provider
	VCSProvider string `json:"vcsProvider"`

	// Repository
	VCSRepositoryURL string `json:"vcsRepositoryUrl"`
	RepoID           string `json:"repoId,omitempty"`
	RepoName         string `json:"repoName,omitempty"`

	// Branch
	VCSBranch     string `json:"vcsBranch"`
	VCSBaseBranch string `json:"vcsBaseBranch,omitempty"`

	// Commit
	VCSCommitSHA         string `json:"vcsCommitSha"`
	VCSCommitMessage     string `json:"vcsCommitMessage,omitempty"`
	VCSCommitAuthorName  string `json:"vcsCommitAuthorName,omitempty"`
	VCSCommitAuthorEmail string `json:"vcsCommitAuthorEmail,omitempty"`
	VCSCommitTimestamp   string `json:"vcsCommitTimestamp,omitempty"`
	VCSBaseCommitSHA     string `json:"vcsBaseCommitSha,omitempty"`

	// Pull request (only for diff/comment runs)
	VCSPullRequestURL    string   `json:"vcsPullRequestUrl,omitempty"`
	VCSPullRequestID     string   `json:"vcsPullRequestId,omitempty"`
	VCSPullRequestTitle  string   `json:"vcsPullRequestTitle,omitempty"`
	VCSPullRequestAuthor string   `json:"vcsPullRequestAuthor,omitempty"`
	VCSPullRequestLabels []string `json:"vcsPullRequestLabels,omitempty"`

	// CI pipeline
	VCSPipelineRunID string `json:"vcsPipelineRunId,omitempty"`

	// Unused — these are internal to the runner's webhook/replay system and
	// not applicable to GitHub Actions.
	// VCSWebhookDeliveryID string `json:"vcsWebhookDeliveryId,omitempty"`
	// ReplayID             string `json:"replayId,omitempty"`

	// Dashboard
	DashboardEnabled bool `json:"dashboardEnabled"`

	// Usage / config metadata
	UsageAPIEnabled        bool   `json:"usageApiEnabled,omitempty"`
	UsageFilePath          string `json:"usageFilePath,omitempty"`
	HasConfigFile          bool   `json:"hasConfigFile,omitempty"`
	ConfigFileHasUsageFile bool   `json:"configFileHasUsageFile,omitempty"`

	// Nested repo metadata for backwards compatibility — the dashboard
	// merges this into combinedMetadata alongside top-level fields.
	RepoMetadata repoMetadata `json:"repoMetadata"`
}

// repoMetadata is the nested metadata the dashboard merges with top-level fields.
type repoMetadata struct {
	VCSRepositoryURL  string `json:"vcsRepositoryUrl"`
	VCSBranch         string `json:"vcsBranch"`
	VCSBaseBranch     string `json:"vcsBaseBranch,omitempty"`
	VCSCommitSHA      string `json:"vcsCommitSha"`
	VCSPullRequestURL string `json:"vcsPullRequestUrl,omitempty"`
}

// BuildErrorRunInput creates a run input that reports a top-level error to the
// dashboard. This should be called when the scanner cannot complete a run (e.g.
// scan failure) so the dashboard records the attempt.
func BuildErrorRunInput(opts RunInputOptions, code diagnostic.ErrorCode, title, description string) dashboard.RunInput {
	opts.Command = "error"
	input := BuildRunInput(opts)
	input.Error = &dashboard.RunError{
		Code:        fmt.Sprintf("%d", code),
		Level:       "error",
		Title:       title,
		Description: description,
	}
	return input
}

// BuildRunInput converts scan results into the dashboard addRun mutation input.
func BuildRunInput(opts RunInputOptions) dashboard.RunInput {
	if opts.HeadResult == nil {
		return buildRunInputFromMetadata(opts, nil)
	}

	projectResults := make([]dashboard.ProjectResultInput, 0, len(opts.HeadResult.Projects))

	var baseByName map[string]*pkgscanner.ProjectResult
	if opts.BaseResult != nil {
		baseByName = make(map[string]*pkgscanner.ProjectResult, len(opts.BaseResult.Projects))
		for i := range opts.BaseResult.Projects {
			baseByName[opts.BaseResult.Projects[i].Name] = &opts.BaseResult.Projects[i]
		}
	}

	for i := range opts.HeadResult.Projects {
		head := &opts.HeadResult.Projects[i]
		base := baseByName[head.Name]

		pr := dashboard.ProjectResultInput{
			ProjectName: head.Name,
		}

		headQuotaFailed := opts.HeadResult.QuotaFailedProjects[head.Name]
		baseQuotaFailed := opts.BaseResult != nil && opts.BaseResult.QuotaFailedProjects[head.Name]

		// A quota-failed project has no cost, so uploading a breakdown would
		// record it at $0 and a diff would report the other side's whole cost.
		if !headQuotaFailed {
			breakdown := buildBreakdownInput(head)
			pr.Breakdown = &breakdown
		}

		if base != nil && !headQuotaFailed && !baseQuotaFailed {
			pastBreakdown := buildBreakdownInput(base)
			pr.PastBreakdown = &pastBreakdown
			diff := buildDiffBreakdownInput(base, head)
			pr.Diff = &diff
		}

		var workspace string
		var projectType string
		var projectPath string
		var isTerraform bool
		if head.Config != nil {
			workspace = head.Config.Terraform.Workspace
			projectType = string(head.Config.Type)
			projectPath = head.Config.Path
			isTerraform = head.Config.Type == "terraform" || head.Config.Type == "terragrunt"
			pr.BreakdownSha = head.Config.ConfigSHA()
		}

		var quotaMessage string
		if headQuotaFailed {
			quotaMessage = truncateRunQuotaMessage(opts.HeadResult.RunQuotaMessage)
		} else if baseQuotaFailed {
			quotaMessage = truncateRunQuotaMessage(opts.BaseResult.RunQuotaMessage)
		}

		pr.Metadata = buildProjectMetadata(head, workspace, projectType, projectPath, opts.Command == "comment", quotaMessage)
		pr.FinopsPolicyResults = buildFinopsPolicyResults(head.FinopsResults, head.Resources, head.Name, workspace, isTerraform)
		pr.TagPolicyResults = buildTagPolicyResults(head.TagPolicyResults)

		projectResults = append(projectResults, pr)
	}

	return buildRunInputFromMetadata(opts, projectResults)
}

func buildRunInputFromMetadata(opts RunInputOptions, projectResults []dashboard.ProjectResultInput) dashboard.RunInput {
	// diff resolves the URL before scanning, so this only builds one for a
	// caller that supplied a number alone. Dropped to keep BuildRunInput
	// pure: scan never sets either, and diff has already rejected a bad one.
	prURL := opts.PRURL
	if prURL == "" {
		prURL, _ = vcsurl.PullRequest(opts.VCSProvider, opts.RepoURL, opts.PRNumber)
	}
	var prID string
	if prURL != "" && opts.PRNumber > 0 {
		prID = fmt.Sprintf("%d", opts.PRNumber)
	}

	metadata := runMetadata{
		Command:                opts.Command,
		Version:                version.Version,
		CIPlatform:             opts.CIPlatform,
		VCSProvider:            opts.VCSProvider,
		VCSRepositoryURL:       opts.RepoURL,
		RepoID:                 opts.RepoID,
		RepoName:               opts.RepoName,
		VCSBranch:              opts.Branch,
		VCSBaseBranch:          opts.BaseBranch,
		VCSCommitSHA:           opts.CommitSHA,
		VCSCommitMessage:       opts.CommitMessage,
		VCSCommitAuthorName:    opts.CommitAuthorName,
		VCSCommitAuthorEmail:   opts.CommitAuthorEmail,
		VCSCommitTimestamp:     opts.CommitTimestamp,
		VCSBaseCommitSHA:       opts.BaseCommitSHA,
		VCSPullRequestURL:      prURL,
		VCSPullRequestID:       prID,
		VCSPullRequestTitle:    opts.PRTitle,
		VCSPullRequestAuthor:   opts.PRAuthor,
		VCSPullRequestLabels:   opts.PRLabels,
		VCSPipelineRunID:       opts.PipelineRunID,
		DashboardEnabled:       true,
		UsageAPIEnabled:        opts.UsageAPIEnabled,
		UsageFilePath:          opts.UsageFilePath,
		HasConfigFile:          opts.HasConfigFile,
		ConfigFileHasUsageFile: opts.ConfigFileHasUsageFile,
		RepoMetadata: repoMetadata{
			VCSRepositoryURL:  opts.RepoURL,
			VCSBranch:         opts.Branch,
			VCSBaseBranch:     opts.BaseBranch,
			VCSCommitSHA:      opts.CommitSHA,
			VCSPullRequestURL: prURL,
		},
	}

	timeGenerated := opts.TimeGenerated
	if timeGenerated == "" {
		timeGenerated = time.Now().UTC().Format(time.RFC3339)
	}

	posted := opts.CommentPosted
	input := dashboard.RunInput{
		ProjectResults:           projectResults,
		Currency:                 opts.Currency,
		TimeGenerated:            timeGenerated,
		PoliciesAlreadyEvaluated: true,
		ClientPostedComment:      &posted,
		Metadata:                 structToJSON(metadata),
	}

	if len(opts.GuardrailResults) > 0 {
		input.GuardrailResults = buildGuardrailResults(opts.GuardrailResults)
	}

	if len(opts.BudgetResults) > 0 {
		input.BudgetResults = buildBudgetResults(opts.BudgetResults)
	}

	return input
}

func buildBreakdownInput(result *pkgscanner.ProjectResult) dashboard.BreakdownInput {
	monthlyCost := orZero(result.TotalMonthlyCost)
	resources, freeResources := convertBreakdownResources(result.Resources)

	return dashboard.BreakdownInput{
		TotalHourlyCost:  monthlyCost.Div(rat.New(730)).String(),
		TotalMonthlyCost: monthlyCost.String(),
		Resources:        resources,
		FreeResources:    freeResources,
	}
}

func buildDiffBreakdownInput(base, head *pkgscanner.ProjectResult) dashboard.BreakdownInput {
	headCost := orZero(head.TotalMonthlyCost)
	baseCost := orZero(base.TotalMonthlyCost)
	diffCost := headCost.Sub(baseCost)

	// The same comparison the PR comment uses, over the same filtered set the
	// breakdown gets, so the uploaded diff and the comment cannot disagree.
	pastBreakdown := buildCostBreakdown(supportedResources(base.Resources))
	currentBreakdown := buildCostBreakdown(supportedResources(head.Resources))

	var resources []dashboard.BreakdownResourceInput
	if diff := diffCostBreakdown(pastBreakdown, currentBreakdown); diff != nil {
		// A diff resource carries only a name and costs, so type and tags come from
		// the source resource: head first, then base for a removed one.
		resources = convertDiffResources(diff.Resources, indexResources(head.Resources, base.Resources))
	}

	return dashboard.BreakdownInput{
		TotalHourlyCost:  diffCost.Div(rat.New(730)).String(),
		TotalMonthlyCost: diffCost.String(),
		Resources:        resources,
	}
}

// convertBreakdownResources splits resources the way the runner's outputBreakdown
// does: free-and-unsupported to freeResources, unsupported dropped with its costs.
func convertBreakdownResources(resources []*provider.Resource) (supported, free []dashboard.BreakdownResourceInput) {
	for _, r := range resources {
		if !r.IsSupported && !r.IsFree {
			continue
		}

		res, _, _ := convertBreakdownResource(r)
		if r.IsSupported {
			supported = append(supported, res)
		} else {
			free = append(free, res)
		}
	}
	return supported, free
}

// supportedResources keeps the set that reaches breakdown.resources, so the diff
// describes the same one. A diff has no equivalent of freeResources.
func supportedResources(resources []*provider.Resource) []*provider.Resource {
	out := make([]*provider.Resource, 0, len(resources))
	for _, r := range resources {
		if r.IsSupported {
			out = append(out, r)
		}
	}
	return out
}

// indexChildResources maps a resource's children by name, so a diff
// sub-resource can be matched to the resource it came from.
func indexChildResources(r *provider.Resource) map[string]*provider.Resource {
	if r == nil {
		return nil
	}
	return indexResources(r.ChildResources)
}

// indexResources maps resources by name, preferring the first list. A diff
// resource names a resource that may exist only on one side of the comparison.
func indexResources(lists ...[]*provider.Resource) map[string]*provider.Resource {
	index := map[string]*provider.Resource{}
	for _, list := range lists {
		for _, r := range list {
			if _, ok := index[r.Name]; !ok {
				index[r.Name] = r
			}
		}
	}
	return index
}

// convertBreakdownResource returns the resource alongside its rolled-up hourly
// and monthly costs, so a parent can sum its children without reparsing them.
func convertBreakdownResource(r *provider.Resource) (dashboard.BreakdownResourceInput, *rat.Rat, *rat.Rat) {
	out := dashboard.BreakdownResourceInput{
		Name:         r.Name,
		ResourceType: r.Type,
	}

	if r.Metadata != nil {
		out.Metadata = &dashboard.BreakdownResourceMetadataInput{
			Checksum:            r.Metadata.DeepChecksum,
			DefaultTagsChecksum: r.Metadata.DefaultTagsChecksum,
		}
	}

	out.Tags, out.DefaultTags = splitTags(r.Tagging)

	hourlyCost := rat.Zero
	monthlyCost := rat.Zero

	if r.Costs != nil {
		for _, c := range r.Costs.Components {
			cc, hourly, monthly := convertBreakdownCostComponent(c)
			out.CostComponents = append(out.CostComponents, cc)
			hourlyCost = hourlyCost.Add(hourly)
			monthlyCost = monthlyCost.Add(monthly)
		}
	}

	for _, child := range r.ChildResources {
		sub, hourly, monthly := convertBreakdownResource(child)
		out.SubResources = append(out.SubResources, sub)
		hourlyCost = hourlyCost.Add(hourly)
		monthlyCost = monthlyCost.Add(monthly)
	}

	out.HourlyCost = hourlyCost.String()
	out.MonthlyCost = monthlyCost.String()

	return out, hourlyCost, monthlyCost
}

func convertBreakdownCostComponent(c *provider.CostComponent) (dashboard.BreakdownCostComponentInput, *rat.Rat, *rat.Rat) {
	hourlyCost := rat.Zero
	monthlyCost := rat.Zero

	out := dashboard.BreakdownCostComponentInput{
		Name:            c.Name,
		Unit:            c.Unit,
		UsageBased:      c.UsageBased,
		Price:           "0",
		HourlyCost:      "0",
		MonthlyCost:     "0",
		HourlyQuantity:  "0",
		MonthlyQuantity: "0",
	}

	if c.PeriodPrice == nil || c.Quantity == nil {
		return out, hourlyCost, monthlyCost
	}

	hourlyQty, monthlyQty := pkgscanner.ConvertQuantityByPeriod(rat.FromProto(c.Quantity), c.PeriodPrice.Period)
	out.HourlyQuantity = hourlyQty.String()
	out.MonthlyQuantity = monthlyQty.String()

	if c.PeriodPrice.Price != nil {
		price := pkgscanner.ApplyDiscount(rat.FromProto(c.PeriodPrice.Price), rat.FromProto(c.DiscountRate))
		hourlyCost = hourlyQty.Mul(price)
		monthlyCost = monthlyQty.Mul(price)
		out.Price = price.String()
		out.HourlyCost = hourlyCost.String()
		out.MonthlyCost = monthlyCost.String()
	}

	return out, hourlyCost, monthlyCost
}

// splitTags separates user tags from provider default tags, which the dashboard
// reads as two fields.
func splitTags(tagging *provider.Tagging) (tags, defaultTags map[string]string) {
	if tagging == nil {
		return nil, nil
	}

	for _, t := range tagging.Tags {
		if t.IsDefault {
			if defaultTags == nil {
				defaultTags = map[string]string{}
			}
			defaultTags[t.Key] = t.Value
			continue
		}
		if tags == nil {
			tags = map[string]string{}
		}
		tags[t.Key] = t.Value
	}

	return tags, defaultTags
}

// convertDiffResources maps diff resources onto the upload shape. Their costs are
// deltas, so hourly derives from monthly; byName supplies the type and tags.
func convertDiffResources(resources []comment.BreakdownResource, byName map[string]*provider.Resource) []dashboard.BreakdownResourceInput {
	if len(resources) == 0 {
		return nil
	}

	out := make([]dashboard.BreakdownResourceInput, 0, len(resources))
	for _, r := range resources {
		// The comment lists any added or removed resource as changed. Uploading a
		// zero-dollar one would set the dashboard's has_diff; the runner's does not.
		monthlyCost := orZero(r.MonthlyCost)
		if monthlyCost.IsZero() && len(r.CostComponents) == 0 && len(r.SubResources) == 0 {
			continue
		}

		source := byName[r.Name]
		dr := dashboard.BreakdownResourceInput{
			Name:         r.Name,
			HourlyCost:   monthlyCost.Div(rat.New(730)).String(),
			MonthlyCost:  monthlyCost.String(),
			SubResources: convertDiffResources(r.SubResources, indexChildResources(source)),
		}

		if source != nil {
			dr.ResourceType = source.Type
			dr.Tags, dr.DefaultTags = splitTags(source.Tagging)
		}

		for _, c := range r.CostComponents {
			ccMonthlyCost := orZero(c.MonthlyCost)
			ccMonthlyQty := orZero(c.MonthlyQuantity)
			dr.CostComponents = append(dr.CostComponents, dashboard.BreakdownCostComponentInput{
				Name:            c.Name,
				Unit:            c.Unit,
				UsageBased:      c.UsageBased,
				Price:           orZero(c.Price).String(),
				HourlyCost:      ccMonthlyCost.Div(rat.New(730)).String(),
				MonthlyCost:     ccMonthlyCost.String(),
				HourlyQuantity:  ccMonthlyQty.Div(rat.New(730)).String(),
				MonthlyQuantity: ccMonthlyQty.String(),
			})
		}

		out = append(out, dr)
	}

	return out
}

func buildBudgetResults(results []goprotoevent.BudgetResult) []dashboard.BudgetResultInput {
	out := make([]dashboard.BudgetResultInput, 0, len(results))
	for _, br := range results {
		tags := make([]dashboard.BudgetTagInput, 0, len(br.Tags))
		for _, t := range br.Tags {
			tags = append(tags, dashboard.BudgetTagInput{Key: t.Key, Value: t.Value})
		}
		out = append(out, dashboard.BudgetResultInput{
			BudgetID:             br.BudgetID,
			Tags:                 tags,
			StartDate:            br.StartDate.Format("2006-01-02T15:04:05Z"),
			EndDate:              br.EndDate.Format("2006-01-02T15:04:05Z"),
			Amount:               ratString(br.Amount),
			CurrentCost:          ratString(br.CurrentCost),
			CustomOverrunMessage: br.CustomOverrunMessage,
		})
	}
	return out
}

func buildGuardrailResults(results []goprotoevent.GuardrailResult) []dashboard.GuardrailResultInput {
	out := make([]dashboard.GuardrailResultInput, 0, len(results))
	for _, gr := range results {
		out = append(out, dashboard.GuardrailResultInput{
			GuardrailID:            gr.GuardrailID,
			Triggered:              gr.Triggered,
			PRComment:              gr.PRComment,
			BlockPR:                gr.BlockPR,
			TriggeringProjectNames: gr.TriggeringProjectNames,
			Increase:               ratString(gr.Increase),
			PercentIncrease:        ratString(gr.PercentIncrease),
			TotalMonthlyCost:       ratString(gr.TotalMonthlyCost),
		})
	}
	return out
}

func buildFinopsPolicyResults(results []*provider.FinopsPolicyResult, resources []*provider.Resource, projectName, workspace string, isTerraform bool) []dashboard.FinopsPolicyResultInput {
	if len(results) == 0 {
		return nil
	}

	resourceMap := make(map[string]*provider.Resource, len(resources))
	for _, r := range resources {
		resourceMap[r.Id] = r
	}

	out := make([]dashboard.FinopsPolicyResultInput, 0, len(results))
	for _, r := range results {
		out = append(out, buildFinopsPolicyResult(r, resourceMap, projectName, workspace, isTerraform))
	}
	return out
}

func buildFinopsPolicyResult(result *provider.FinopsPolicyResult, resourceMap map[string]*provider.Resource, projectName, workspace string, isTerraform bool) dashboard.FinopsPolicyResultInput {
	allCurrentResources := make([]string, 0, len(result.PassingResourceIds)+len(result.FailingResources))
	failingResources := make([]dashboard.FinopsPolicyResourceInput, 0, len(result.FailingResources))
	passingResources := make([]dashboard.FinopsPolicyPassingResourceInput, 0, len(result.PassingResourceIds))

	for _, failing := range result.FailingResources {
		resource, ok := resourceMap[failing.Id]
		if !ok {
			continue
		}

		var modulePath, moduleCallPath string
		var moduleCallLine int
		stack := resource.CallStack
		if stack != nil && len(stack.Frames) > 1 {
			modulePath = stack.Frames[1].Source
			moduleCallPath = address.FromProto(stack.Frames[0].Address).String()
			if stack.Frames[0].SourceRange != nil {
				moduleCallLine = int(stack.Frames[0].SourceRange.StartLine)
			}
		}

		var tfModule string
		if isTerraform {
			tfModule = modulePath
		}

		var startLine, endLine int
		var path, checksum, parserChecksum string
		if resource.Metadata != nil {
			startLine = int(resource.Metadata.StartLine)
			endLine = int(resource.Metadata.EndLine)
			path = resource.Metadata.Filename
			checksum = resource.Metadata.BasicChecksum
			parserChecksum = resource.Metadata.DeepChecksum
		}

		failingResources = append(failingResources, dashboard.FinopsPolicyResourceInput{
			NewResource:          resource.Action == provider.ResourceAction_CREATE,
			Address:              failing.CauseAddress,
			ResourceType:         resource.Type,
			Path:                 path,
			ProjectName:          projectName,
			ModulePath:           modulePath,
			ModuleCallPath:       moduleCallPath,
			ModuleCallStartLine:  moduleCallLine,
			Issues:               buildFinopsIssues(failing.Issues),
			StartLine:            startLine,
			EndLine:              endLine,
			UnlabeledProjectName: projectName,
			TerraformModulePath:  tfModule,
			TerraformWorkspace:   workspace,
			Checksum:             checksum,
			ParserChecksum:       parserChecksum,
		})
		allCurrentResources = append(allCurrentResources, resource.Name)
	}

	for _, resourceID := range result.PassingResourceIds {
		resource, ok := resourceMap[resourceID]
		if !ok {
			continue
		}
		passingResources = append(passingResources, dashboard.FinopsPolicyPassingResourceInput{
			Address:     resource.Name,
			ProjectName: projectName,
		})
		allCurrentResources = append(allCurrentResources, resource.Name)
	}

	return dashboard.FinopsPolicyResultInput{
		Name:                     result.PolicyName,
		PolicyID:                 result.PolicyId,
		Message:                  result.PolicyMessage,
		BlockPR:                  result.BlockPullRequest,
		PRComment:                result.IncludeInPullRequestComment,
		OnlyNewResources:         result.OnlyAppliesToNewResources,
		AllCurrentResources:      allCurrentResources,
		Resources:                failingResources,
		PastResources:            []dashboard.FinopsPolicyResourceInput{},
		PassingResources:         passingResources,
		TotalApplicableResources: len(allCurrentResources),
	}
}

func buildFinopsIssues(issues []*provider.FinopsResourceIssue) []dashboard.FinopsPolicyIssueInput {
	out := make([]dashboard.FinopsPolicyIssueInput, 0, len(issues))
	for _, issue := range issues {
		m := dashboard.FinopsPolicyIssueInput{
			FromAddress: issue.Address,
			Attribute:   issue.Attribute,
			Description: issue.Description,
		}
		if issue.SavingsDetails != nil {
			m.SavingsDetails = issue.SavingsDetails
		}
		if issue.MonthlySavings != nil {
			s := rat.FromProto(issue.MonthlySavings).String()
			m.MonthlySavings = &s
		}
		if issue.MonthlyCarbonSavingsGramsCo2E != nil {
			s := rat.FromProto(issue.MonthlyCarbonSavingsGramsCo2E).String()
			m.MonthlyCarbonSavingsGramsCo2e = &s
		}
		if issue.MonthlyWaterSavingsLiters != nil {
			s := rat.FromProto(issue.MonthlyWaterSavingsLiters).String()
			m.MonthlyWaterSavingsLitres = &s
		}
		if len(issue.BeforeFixBreakdowns) > 0 {
			m.CurrentCostBreakdowns = buildIssueBreakdowns(issue.BeforeFixBreakdowns)
		}
		if len(issue.AfterFixBreakdowns) > 0 {
			m.NewCostBreakdowns = buildIssueBreakdowns(issue.AfterFixBreakdowns)
		}
		out = append(out, m)
	}
	return out
}

func buildIssueBreakdowns(breakdowns []*provider.IssueBreakdown) []dashboard.FinopsPolicyBreakdownInput {
	out := make([]dashboard.FinopsPolicyBreakdownInput, 0, len(breakdowns))
	for _, b := range breakdowns {
		totalMonthlyCost := rat.Zero
		totalHourlyCost := rat.Zero

		costComponents := make([]dashboard.FinopsPolicyCostComponentInput, 0, len(b.CostComponents))
		for _, cc := range b.CostComponents {
			converted := buildIssueCostComponent(cc)
			costComponents = append(costComponents, converted)
			if mcRat, err := rat.NewFromString(converted.MonthlyCost); err == nil {
				totalMonthlyCost = totalMonthlyCost.Add(mcRat)
			}
			if hcRat, err := rat.NewFromString(converted.HourlyCost); err == nil {
				totalHourlyCost = totalHourlyCost.Add(hcRat)
			}
		}

		m := dashboard.FinopsPolicyBreakdownInput{
			Name:           b.ResourceName,
			HourlyCost:     totalHourlyCost.String(),
			MonthlyCost:    totalMonthlyCost.String(),
			Metadata:       dashboard.FinopsPolicyBreakdownMetadataInput{Region: b.Region},
			CostComponents: costComponents,
			SubResources:   buildIssueBreakdowns(b.Subresources),
		}
		out = append(out, m)
	}
	return out
}

func buildIssueCostComponent(cc *provider.IssueCostComponent) dashboard.FinopsPolicyCostComponentInput {
	monthlyQty := rat.Zero
	hourlyQty := rat.Zero
	monthlyCost := rat.Zero
	hourlyCost := rat.Zero
	price := rat.Zero

	if cc.PeriodPrice != nil {
		qty := rat.FromProto(cc.Quantity)
		if qty == nil {
			qty = rat.Zero
		}
		hourlyQty, monthlyQty = convertQuantityByPeriod(qty, cc.PeriodPrice.Period)

		if cc.PeriodPrice.Price != nil {
			p := rat.FromProto(cc.PeriodPrice.Price)
			if p == nil {
				p = rat.Zero
			}
			discount := rat.FromProto(cc.DiscountRate)
			if discount == nil {
				discount = rat.Zero
			}
			price = applyDiscount(p, discount)
			monthlyCost = monthlyQty.Mul(price)
			hourlyCost = hourlyQty.Mul(price)
		}
	}

	return dashboard.FinopsPolicyCostComponentInput{
		Name:            cc.Name,
		Unit:            cc.Unit,
		Price:           price.String(),
		HourlyCost:      hourlyCost.String(),
		HourlyQuantity:  hourlyQty.String(),
		MonthlyCost:     monthlyCost.String(),
		MonthlyQuantity: monthlyQty.String(),
		UsageBased:      cc.UsageBased,
	}
}

func convertQuantityByPeriod(qty *rat.Rat, period provider.Period) (hourly, monthly *rat.Rat) {
	if qty == nil {
		return rat.Zero, rat.Zero
	}
	hoursPerMonth := rat.New(730)
	switch period {
	case provider.Period_HOUR:
		return qty, qty.Mul(hoursPerMonth)
	case provider.Period_MONTH:
		return qty.Div(hoursPerMonth), qty
	default:
		return rat.Zero, rat.Zero
	}
}

func applyDiscount(price, discountRate *rat.Rat) *rat.Rat {
	if discountRate == nil || discountRate.IsZero() {
		return price
	}
	return price.Sub(price.Mul(discountRate))
}

func buildTagPolicyResults(results []goprotoevent.TaggingPolicyResult) []map[string]any {
	if len(results) == 0 {
		return nil
	}
	var out []map[string]any
	for _, r := range results {
		out = append(out, structToJSON(r))
	}
	return out
}

// quotaMessage, when non-empty, records the org's run limit as a project error
// so the dashboard shows code 401 rather than a generic breakdown failure.
func buildProjectMetadata(project *pkgscanner.ProjectResult, workspace, projectType, projectPath string, isDiff bool, quotaMessage string) *dashboard.ProjectMetadataInput {
	meta := &dashboard.ProjectMetadataInput{
		Path:               projectPath,
		Type:               projectType,
		TerraformWorkspace: workspace,
		VCSSubPath:         projectPath,
	}

	if project.Config != nil {
		configSha := project.Config.ConfigSHA()
		meta.ConfigSha = configSha
	}

	if isDiff {
		codeChanged := true
		meta.VCSCodeChanged = &codeChanged
	}

	meta.RemoteModuleCalls = project.RemoteModuleCalls

	var errors []dashboard.ProjectDiagnostic
	var warnings []dashboard.ProjectDiagnostic
	for _, d := range project.Diagnostics {
		if !d.Critical && !d.Warning {
			continue
		}
		pd := dashboard.ProjectDiagnostic{
			Code:    diagnostic.DashboardCode(d.Type),
			Message: diagnostic.MessagePrefix(d.Type) + ": " + d.Error,
			IsError: d.Critical,
		}
		if d.Critical {
			errors = append(errors, pd)
		} else {
			warnings = append(warnings, pd)
		}
	}

	if quotaMessage != "" {
		errors = append(errors, dashboard.ProjectDiagnostic{
			Code:    diagnostic.CodeRunQuotaExceeded,
			Message: quotaMessage,
			IsError: true,
		})
	}

	meta.Errors = errors
	meta.Warnings = warnings

	return meta
}

func structToJSON(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func ratString(r *rat.Rat) string {
	if r == nil {
		return "0"
	}
	return r.String()
}
