package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func (s *issueServiceImpl) ListIssues(ctx context.Context, params ListIssuesParams) (*ListIssuesResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	be, svcErr := s.resolveBackend(ctx)
	if svcErr != nil {
		return nil, svcErr
	}
	return s.listIssuesViaBackend(ctx, be, params)
}

// listIssuesViaBackend serves a list request through the IssueBackend port.
//
// This port covers the standard (non-kanban) list shape and enriches kanban
// blocked state from IssueBackend.Blocked. Parent-title enrichment requires
// extra Get calls per issue and is skipped — the FE shows the parent ID
// without a title, which the kanban handles as a graceful fallback (see
// kanbanIssue.go in the FE).
//
// The composite ListKanban RPC's per-issue parent-title and full
// blocked-by-details lookups are intentionally not reproduced: matching them
// would mean N extra Gets. The blocked summary uses one backend Blocked call
// instead, which preserves kanban correctness without the p95 hit.
func (s *issueServiceImpl) listIssuesViaBackend(
	ctx context.Context, be backend.IssueBackend, params ListIssuesParams,
) (*ListIssuesResult, error) {
	opts := listArgsToBackendOpts(params.Args)
	issues, err := be.List(ctx, opts)
	if err != nil {
		return nil, translateBackendError(err)
	}
	issues = excludeBackendIssuesByStatus(issues, params.ExcludeStatus)

	if !params.IncludeBlocked {
		return &ListIssuesResult{Issues: backendIssuesWithParent(issues)}, nil
	}

	blockedByID, err := s.blockedIssueMap(ctx, be, params.Args)
	if err != nil {
		return nil, err
	}
	readyByID, err := s.readyIssueIDMap(ctx, be, params.Args)
	if err != nil {
		return nil, err
	}
	deferredByID, err := s.deferredIssueMap(ctx, be, params.Args)
	if err != nil {
		return nil, err
	}
	issues = appendMissingBlockedIssues(issues, blockedByID)
	issues = appendMissingDeferredIssues(issues, deferredByID)

	return &ListIssuesResult{KanbanIssues: backendKanbanIssues(issues, blockedByID, readyByID, deferredByID)}, nil
}

// Apply ExcludeStatus client-side. fleet-db's ListOpts has a single Status
// filter, not an exclude set; doing it here keeps the contract uniform with the
// pool-based path.
func excludeBackendIssuesByStatus(issues []backend.IssueData, excludeStatus []string) []backend.IssueData {
	if len(excludeStatus) == 0 {
		return issues
	}
	excluded := make(map[string]bool, len(excludeStatus))
	for _, status := range excludeStatus {
		excluded[status] = true
	}
	filtered := issues[:0]
	for _, issue := range issues {
		if !excluded[issue.Status] {
			filtered = append(filtered, issue)
		}
	}
	return filtered
}

func backendIssuesWithParent(issues []backend.IssueData) []IssueWithParent {
	out := make([]IssueWithParent, len(issues))
	for i, d := range issues {
		out[i] = backendIssueWithParent(d)
	}
	return out
}

func backendIssueWithParent(d backend.IssueData) IssueWithParent {
	iwc := backendIssueDataToWithCounts(&d)
	iwp := IssueWithParent{IssueWithCounts: iwc}
	if d.Parent != "" {
		p := d.Parent
		iwp.Parent = &p
	}
	if d.SourceRepo != "" {
		r := d.SourceRepo
		iwp.Repo = &r
	}
	return iwp
}

func appendMissingBlockedIssues(
	issues []backend.IssueData,
	blockedByID map[string]backend.IssueData,
) []backend.IssueData {
	return appendMissingIssueData(issues, blockedByID)
}

func appendMissingDeferredIssues(
	issues []backend.IssueData,
	deferredByID map[string]backend.IssueData,
) []backend.IssueData {
	return appendMissingIssueData(issues, deferredByID)
}

func appendMissingIssueData(
	issues []backend.IssueData,
	byID map[string]backend.IssueData,
) []backend.IssueData {
	if len(byID) == 0 {
		return issues
	}
	seen := make(map[string]bool, len(issues))
	for _, d := range issues {
		seen[d.ID] = true
	}
	for _, d := range byID {
		if !seen[d.ID] {
			issues = append(issues, d)
		}
	}
	return issues
}

func backendKanbanIssues(
	issues []backend.IssueData,
	blockedByID map[string]backend.IssueData,
	readyByID map[string]bool,
	deferredByID map[string]backend.IssueData,
) []KanbanIssue {
	out := make([]KanbanIssue, len(issues))
	for i, d := range issues {
		_, deferred := deferredByID[d.ID]
		out[i] = backendKanbanIssue(d, blockedByID[d.ID], readyByID[d.ID], deferred)
	}
	return out
}

func backendKanbanIssue(d backend.IssueData, blocked backend.IssueData, ready bool, deferred bool) KanbanIssue {
	iwc := backendIssueDataToWithCounts(&d)
	ki := KanbanIssue{IssueWithCounts: iwc}
	if d.Parent != "" {
		p := d.Parent
		ki.Parent = &p
	}
	if d.SourceRepo != "" {
		r := d.SourceRepo
		ki.Repo = &r
	}
	if blocked.ID != "" {
		applyBlockedSummary(&ki, blocked)
	}
	ki.IsDeferred = deferred || d.Status == string(types.StatusDeferred)
	ki.IsReady = ready && !ki.IsBlocked && !ki.IsDeferred
	return ki
}

func applyBlockedSummary(ki *KanbanIssue, blocked backend.IssueData) {
	ki.IsBlocked = true
	ki.BlockedByCount = blocked.BlockedByCount
	ki.BlockedBy = append([]string(nil), blocked.BlockedBy...)
	if ki.BlockedByCount == 0 {
		ki.BlockedByCount = len(ki.BlockedBy)
	}
}

func (s *issueServiceImpl) blockedIssueMap(
	ctx context.Context,
	be backend.IssueBackend,
	args *ListFilter,
) (map[string]backend.IssueData, error) {
	blocked, err := be.Blocked(ctx, blockedOptsFromListArgs(args))
	if err != nil {
		slog.Error("backend error in ListIssues.Blocked", "err", err)
		return nil, translateBackendError(err)
	}
	out := make(map[string]backend.IssueData, len(blocked))
	for _, d := range blocked {
		out[d.ID] = d
	}
	return out, nil
}

func (s *issueServiceImpl) readyIssueIDMap(
	ctx context.Context,
	be backend.IssueBackend,
	args *ListFilter,
) (map[string]bool, error) {
	ready, err := be.Ready(ctx, readyOptsFromListArgs(args))
	if err != nil {
		slog.Error("backend error in ListIssues.Ready", "err", err)
		return nil, translateBackendError(err)
	}
	return issueIDSet(ready), nil
}

func (s *issueServiceImpl) deferredIssueMap(
	ctx context.Context,
	be backend.IssueBackend,
	args *ListFilter,
) (map[string]backend.IssueData, error) {
	deferredBackend, ok := be.(backend.DeferredIssueBackend)
	if !ok {
		return map[string]backend.IssueData{}, nil
	}
	deferred, err := deferredBackend.Deferred(ctx, deferredOptsFromListArgs(args))
	if err != nil {
		slog.Error("backend error in ListIssues.Deferred", "err", err)
		return nil, translateBackendError(err)
	}
	return issueDataMap(deferred), nil
}

func issueIDSet(issues []backend.IssueData) map[string]bool {
	out := make(map[string]bool, len(issues))
	for _, d := range issues {
		out[d.ID] = true
	}
	return out
}

func issueDataMap(issues []backend.IssueData) map[string]backend.IssueData {
	out := make(map[string]backend.IssueData, len(issues))
	for _, d := range issues {
		out[d.ID] = d
	}
	return out
}

func readyOptsFromListArgs(args *ListFilter) backend.ReadyOpts {
	if args == nil {
		return backend.ReadyOpts{}
	}
	return backend.ReadyOpts{
		ParentID:    args.ParentID,
		Assignee:    args.Assignee,
		Priority:    args.Priority,
		Type:        args.IssueType,
		Labels:      args.Labels,
		SourceRepos: args.SourceRepos,
	}
}

func deferredOptsFromListArgs(args *ListFilter) backend.DeferredOpts {
	if args == nil {
		return backend.DeferredOpts{}
	}
	return backend.DeferredOpts{
		ParentID:    args.ParentID,
		Assignee:    args.Assignee,
		Priority:    args.Priority,
		Type:        args.IssueType,
		Labels:      args.Labels,
		SourceRepos: args.SourceRepos,
	}
}

func blockedOptsFromListArgs(args *ListFilter) backend.BlockedOpts {
	if args == nil {
		return backend.BlockedOpts{}
	}
	return backend.BlockedOpts{
		ParentID:    args.ParentID,
		Assignee:    args.Assignee,
		Priority:    args.Priority,
		Type:        args.IssueType,
		Labels:      args.Labels,
		SourceRepos: args.SourceRepos,
	}
}

// backendIssueDataToWithCounts adapts the slim list projection
// (backend.IssueData) into the FE-facing types.IssueWithCounts shape.
// Counts come straight off IssueData; the embedded Issue is populated
// from the same fields. Fields the slim projection doesn't carry
// (description, dependencies, comments, etc.) stay zero-valued.
func backendIssueDataToWithCounts(d *backend.IssueData) *types.IssueWithCounts {
	if d == nil {
		return nil
	}
	issue := &types.Issue{
		ID:               d.ID,
		Title:            d.Title,
		Status:           types.Status(d.Status),
		Priority:         d.Priority,
		IssueType:        types.IssueType(d.IssueType),
		Assignee:         d.Assignee,
		Owner:            d.Owner,
		Labels:           d.Labels,
		Design:           d.Design,
		DesignArtifactID: d.DesignArtifactID,
		DesignFormat:     d.DesignFormat,
		HasDesign:        d.HasDesign || d.Design != "",
		Notes:            d.Notes,
		CreatedAt:        d.CreatedAt,
		UpdatedAt:        d.UpdatedAt,
		DueAt:            d.DueAt,
		DeferUntil:       d.DeferUntil,
		CreatedBy:        d.CreatedBy,
		ClosedAt:         d.ClosedAt,
		CloseReason:      d.CloseReason,
	}
	if d.SourceRepo != "" {
		issue.SourceRepo = d.SourceRepo
	}
	if d.ExternalRef != "" {
		ref := d.ExternalRef
		issue.ExternalRef = &ref
	}
	return &types.IssueWithCounts{
		Issue:           issue,
		DependencyCount: d.DependencyCount,
		DependentCount:  d.DependentCount,
	}
}

// (note: backendIssueDataToWithCounts must carry Notes — see
// TestBackendIssueDataToWithCounts_CarriesNotes — so the kanban board can
// compute the "blocked with notes" needs-attention state.)

// listArgsToBackendOpts converts the Web UI query contract into the
// backend.ListOpts shape. We map
// only fields that backend.IssueBackend.List accepts uniformly across
// drivers — fleet-db rejects several optional filters (see ListOpts
// "fleet-db: unsupported" comments), so passing them through would
// surface as runtime errors instead of being silently ignored.
func listArgsToBackendOpts(a *ListFilter) backend.ListOpts {
	if a == nil {
		return backend.ListOpts{}
	}
	return backend.ListOpts{
		Status:      a.Status,
		IssueType:   a.IssueType,
		Assignee:    a.Assignee,
		Labels:      a.Labels,
		ParentID:    a.ParentID,
		Limit:       a.Limit,
		SourceRepos: a.SourceRepos,
	}
}
