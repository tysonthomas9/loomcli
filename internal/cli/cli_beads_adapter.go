package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// cliBeadsAdapter implements backend.IssueBackend by shelling out to the bd CLI
// via BDRunner. This is the replacement for bdBackend during the transition where
// the daemon may not be running.
type cliBeadsAdapter struct {
	runner BDRunner
	dir    string
}

// Compile-time interface check.
var _ backend.IssueBackend = (*cliBeadsAdapter)(nil)

func newCliBeadsAdapter(runner BDRunner, dir string) *cliBeadsAdapter {
	return &cliBeadsAdapter{runner: runner, dir: dir}
}

// --- Query methods ---

func (a *cliBeadsAdapter) Ready(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	args := []string{"ready", "--json"}
	if opts.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(opts.Limit))
	}
	if opts.ParentID != "" {
		args = append(args, "--parent", opts.ParentID)
	}
	for _, label := range opts.Labels {
		args = append(args, "--label", label)
	}
	if len(opts.SourceRepos) > 0 {
		args = append(args, "--source-repos="+strings.Join(opts.SourceRepos, ","))
	}
	return a.queryIssues("Ready", args)
}

func (a *cliBeadsAdapter) List(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	args := []string{"list", "--json"}
	if opts.Status != "" {
		args = append(args, "--status", opts.Status)
	}
	if opts.Assignee != "" {
		args = append(args, "--assignee", opts.Assignee)
	}
	if opts.IssueType != "" {
		args = append(args, "--type", opts.IssueType)
	}
	if opts.ParentID != "" {
		args = append(args, "--parent", opts.ParentID)
	}
	if opts.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(opts.Limit))
	}
	return a.queryIssues("List", args)
}

func (a *cliBeadsAdapter) Blocked(_ context.Context, _ backend.BlockedOpts) ([]backend.IssueData, error) {
	return a.queryIssues("Blocked", []string{"blocked", "--json"})
}

func (a *cliBeadsAdapter) Stats(_ context.Context) (*backend.StatsData, error) {
	result := a.runner.Run(a.dir, "stats", "--json")
	if result.Err != nil {
		return nil, a.classifyError("Stats", result)
	}
	// bd stats --json returns a nested structure with "summary" key
	var raw struct {
		Summary struct {
			TotalIssues      int `json:"total_issues"`
			OpenIssues       int `json:"open_issues"`
			ClosedIssues     int `json:"closed_issues"`
			InProgressIssues int `json:"in_progress_issues"`
			BlockedIssues    int `json:"blocked_issues"`
			DeferredIssues   int `json:"deferred_issues"`
			TombstoneIssues  int `json:"tombstone_issues"`
			PinnedIssues     int `json:"pinned_issues"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return nil, fmt.Errorf("cliBeadsAdapter.Stats: parse: %w", err)
	}
	return &backend.StatsData{
		TotalIssues:      raw.Summary.TotalIssues,
		OpenIssues:       raw.Summary.OpenIssues,
		ClosedIssues:     raw.Summary.ClosedIssues,
		InProgressIssues: raw.Summary.InProgressIssues,
		BlockedIssues:    raw.Summary.BlockedIssues,
		DeferredIssues:   raw.Summary.DeferredIssues,
		TombstoneIssues:  raw.Summary.TombstoneIssues,
		PinnedIssues:     raw.Summary.PinnedIssues,
	}, nil
}

func (a *cliBeadsAdapter) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	result := a.runner.Run(a.dir, "show", id, "--json")
	if result.Err != nil {
		return nil, a.classifyError("Get", result)
	}
	// bd show --json returns a single-element array
	var issues []cliIssueJSON
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("cliBeadsAdapter.Get(%s): parse: %w", id, err)
	}
	if len(issues) == 0 {
		return nil, backend.ErrNotFound("Get", fmt.Sprintf("issue %s not found", id))
	}
	return issues[0].toDetailData(), nil
}

func (a *cliBeadsAdapter) Count(_ context.Context, _ backend.CountOpts) (int, error) {
	return 0, backend.ErrNotImplemented("Count", "not supported via CLI adapter")
}

// --- Mutation methods ---

func (a *cliBeadsAdapter) Create(_ context.Context, _ backend.CreateParams) (*backend.IssueData, error) {
	return nil, backend.ErrNotImplemented("Create", "not supported via CLI adapter")
}

func (a *cliBeadsAdapter) Update(_ context.Context, id string, params backend.UpdateParams) error {
	args := []string{"update", id}
	if params.Status != nil {
		args = append(args, "--status", *params.Status)
	}
	if params.Assignee != nil {
		args = append(args, "--assignee", *params.Assignee)
	}
	if params.Design != nil {
		args = append(args, "--design", *params.Design)
	}
	if params.Claim {
		args = append(args, "--claim")
	}
	if params.ExternalRef != nil {
		args = append(args, "--external-ref", *params.ExternalRef)
	}
	return a.runMutation("Update", args...)
}

// ClaimIssue atomically claims an issue by shelling out to bd update --claim.
// The lockTTL parameter is accepted but ignored (beads SQLite claims don't support TTL).
func (a *cliBeadsAdapter) ClaimIssue(_ context.Context, id string, lockTTL time.Duration) error {
	if id == "" {
		return backend.ErrValidation("ClaimIssue", "id must not be empty")
	}
	if lockTTL < 0 {
		return backend.ErrValidation("ClaimIssue", "lockTTL must not be negative")
	}
	return a.runMutation("ClaimIssue", "update", id, "--claim")
}

func (a *cliBeadsAdapter) Close(_ context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	args := []string{"close", id}
	if params.Reason != "" {
		args = append(args, "--reason", params.Reason)
	}
	if err := a.runMutation("Close", args...); err != nil {
		return nil, err
	}
	return &backend.CloseResult{}, nil
}

func (a *cliBeadsAdapter) Delete(_ context.Context, _ backend.DeleteParams) error {
	return backend.ErrNotImplemented("Delete", "not supported via CLI adapter")
}

// --- Dependency methods ---

func (a *cliBeadsAdapter) AddDependency(_ context.Context, _ backend.DepAddParams) error {
	return backend.ErrNotImplemented("AddDependency", "not supported via CLI adapter")
}

func (a *cliBeadsAdapter) RemoveDependency(_ context.Context, _ backend.DepRemoveParams) error {
	return backend.ErrNotImplemented("RemoveDependency", "not supported via CLI adapter")
}

// --- Label methods ---

func (a *cliBeadsAdapter) AddLabel(_ context.Context, _, _ string) error {
	return backend.ErrNotImplemented("AddLabel", "not supported via CLI adapter")
}

func (a *cliBeadsAdapter) RemoveLabel(_ context.Context, _, _ string) error {
	return backend.ErrNotImplemented("RemoveLabel", "not supported via CLI adapter")
}

// --- Comment methods ---

func (a *cliBeadsAdapter) ListComments(_ context.Context, _ string) ([]backend.CommentData, error) {
	return nil, backend.ErrNotImplemented("ListComments", "not supported via CLI adapter")
}

func (a *cliBeadsAdapter) AddComment(_ context.Context, _ backend.CommentAddParams) (*backend.CommentData, error) {
	return nil, backend.ErrNotImplemented("AddComment", "not supported via CLI adapter")
}

// --- Event methods ---

func (a *cliBeadsAdapter) ListEvents(_ context.Context, _ string, _ int) ([]backend.EventData, error) {
	return nil, backend.ErrNotImplemented("ListEvents", "not supported via CLI adapter")
}

// --- Batch methods ---

func (a *cliBeadsAdapter) Batch(_ context.Context, _ []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, backend.ErrNotImplemented("Batch", "not supported via CLI adapter")
}

// --- Mutation polling ---

func (a *cliBeadsAdapter) GetMutations(_ context.Context, _ int64) ([]backend.MutationData, error) {
	return nil, backend.ErrNotImplemented("GetMutations", "not supported via CLI adapter")
}

func (a *cliBeadsAdapter) WaitForMutations(_ context.Context, _ int64, _ int64) ([]backend.MutationData, error) {
	return nil, backend.ErrNotImplemented("WaitForMutations", "not supported via CLI adapter")
}

// --- Metadata ---

func (a *cliBeadsAdapter) BackendName() string {
	return "beads"
}

// --- internal helpers ---

// cliIssueJSON maps the bd CLI JSON output for issue parsing.
type cliIssueJSON struct {
	ID           string              `json:"id"`
	Title        string              `json:"title"`
	Status       string              `json:"status"`
	Priority     int                 `json:"priority"`
	IssueType    string              `json:"issue_type"`
	Design       string              `json:"design"`
	Assignee     string              `json:"assignee"`
	Owner        string              `json:"owner"`
	Labels       []string            `json:"labels"`
	SourceRepo   string              `json:"source_repo,omitempty"`
	Parent       string              `json:"parent,omitempty"`
	Description  string              `json:"description,omitempty"`
	Notes        string              `json:"notes,omitempty"`
	AcceptCrit   string              `json:"acceptance_criteria,omitempty"`
	ExternalRef  string              `json:"external_ref,omitempty"`
	Dependencies []cliDependencyJSON `json:"dependencies,omitempty"`
}

type cliDependencyJSON struct {
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
	Title       string `json:"title,omitempty"`
	Status      string `json:"status,omitempty"`
	Priority    int    `json:"priority,omitempty"`
	IssueType   string `json:"issue_type,omitempty"`
}

func (c *cliIssueJSON) toIssueData() backend.IssueData {
	labels := make([]string, 0)
	if len(c.Labels) > 0 {
		labels = c.Labels
	}
	return backend.IssueData{
		ID:         c.ID,
		Title:      c.Title,
		Status:     c.Status,
		Priority:   c.Priority,
		IssueType:  c.IssueType,
		Assignee:   c.Assignee,
		Owner:      c.Owner,
		Labels:     labels,
		SourceRepo: c.SourceRepo,
		Parent:     c.Parent,
		Design:     c.Design,
	}
}

func (c *cliIssueJSON) toDetailData() *backend.IssueDetailData {
	d := &backend.IssueDetailData{
		IssueData:          c.toIssueData(),
		Description:        c.Description,
		Design:             c.Design,
		AcceptanceCriteria: c.AcceptCrit,
		Notes:              c.Notes,
		ExternalRef:        c.ExternalRef,
	}
	deps := make([]backend.DependencyData, 0, len(c.Dependencies))
	for _, dep := range c.Dependencies {
		deps = append(deps, backend.DependencyData{
			IssueID:     c.ID,
			DependsOnID: dep.DependsOnID,
			Type:        dep.Type,
			Title:       dep.Title,
			Status:      dep.Status,
			Priority:    dep.Priority,
			IssueType:   dep.IssueType,
		})
	}
	d.Dependencies = deps
	return d
}

func (a *cliBeadsAdapter) queryIssues(method string, args []string) ([]backend.IssueData, error) {
	result := a.runner.Run(a.dir, args...)
	if result.Err != nil {
		return nil, a.classifyError(method, result)
	}
	var issues []cliIssueJSON
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("cliBeadsAdapter.%s: parse: %w", method, err)
	}
	out := make([]backend.IssueData, 0, len(issues))
	for i := range issues {
		out = append(out, issues[i].toIssueData())
	}
	return out, nil
}

func (a *cliBeadsAdapter) runMutation(method string, args ...string) error {
	result := a.runner.Run(a.dir, args...)
	if result.Err != nil {
		return a.classifyError(method, result)
	}
	return nil
}

func (a *cliBeadsAdapter) classifyError(method string, result CommandResult) error {
	stderr := strings.TrimSpace(result.Stderr)
	if strings.Contains(stderr, "not found") {
		return backend.ErrNotFound(method, stderr)
	}
	return backend.ErrUnavailable(method, fmt.Sprintf("%v: %s", result.Err, stderr), result.Err)
}
