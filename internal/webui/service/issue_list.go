package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func (s *issueServiceImpl) ListIssues(ctx context.Context, params ListIssuesParams) (*ListIssuesResult, error) {
	// Pool-less fleet mode: there is no daemon to connect to, so go
	// directly through the IssueBackend. The pool path below only runs
	// when multiPool/pool is wired.
	if s.pool == nil {
		if be, _ := s.resolveBackend(ctx); be != nil {
			return s.listIssuesViaBackend(ctx, be, params)
		}
		return nil, ErrUnavailable("connection pool not initialized")
	}

	// Mixed deployment: a pool exists for the local workspace, but other
	// workspaces (fleet-db-only) won't have one. When the request's
	// workspace isn't in the pool registry, route to the IssueBackend
	// instead of opening a daemon connection that would return the wrong
	// workspace's data.
	if wsID := middleware.WorkspaceFromContext(ctx); wsID != "" && s.multiPool != nil {
		if s.multiPool.PoolForWorkspace(wsID) == nil {
			if be, _ := s.resolveBackend(ctx); be != nil {
				return s.listIssuesViaBackend(ctx, be, params)
			}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.pool.Get(ctx)
	if err != nil {
		// Pool acquisition failed. In fleet mode the pool is intentionally
		// non-functional (no local issue daemon at all), so before bubbling
		// "issue backend unavailable" up to the FE, see whether a backend is
		// wired and serve the list from there.
		if be, _ := s.resolveBackend(ctx); be != nil {
			return s.listIssuesViaBackend(ctx, be, params)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout("timeout connecting to issue backend")
		}
		slog.Error("connection pool error", "err", err)
		return nil, ErrUnavailable("issue backend unavailable")
	}
	rpcOK := false
	defer s.releaseClient(client, &rpcOK)

	// Try the composite ListKanban RPC first — 1 round-trip instead of 3.
	kanbanArgs := &rpc.ListKanbanArgs{
		ListArgs:       *params.Args,
		IncludeBlocked: params.IncludeBlocked,
		ExcludeStatus:  params.ExcludeStatus,
	}
	kanbanResp, kanbanErr := client.ListKanban(kanbanArgs)
	if kanbanErr == nil {
		rpcOK = true
		return buildResultFromKanbanRPC(kanbanResp, params.IncludeBlocked), nil
	}
	slog.Error("ListKanban RPC error", "err", kanbanErr)
	return nil, ErrInternal("failed to list issues", kanbanErr)
}

// buildResultFromKanbanRPC converts the composite RPC response into the
// service-layer result shape. Kanban state flags are passed through from the
// RPC response; this layer does not rebuild computed queues from raw issue data.
func buildResultFromKanbanRPC(resp *rpc.ListKanbanResponse, includeBlocked bool) *ListIssuesResult {
	if !includeBlocked {
		issuesWithParent := make([]IssueWithParent, len(resp.Issues))
		for i, ki := range resp.Issues {
			issuesWithParent[i] = kanbanRPCToIssueWithParent(ki)
		}
		return &ListIssuesResult{Issues: issuesWithParent}
	}

	kanbanIssues := make([]KanbanIssue, len(resp.Issues))
	for i, ki := range resp.Issues {
		kanbanIssues[i] = kanbanRPCToKanbanIssue(ki)
	}
	return &ListIssuesResult{KanbanIssues: kanbanIssues}
}

func kanbanRPCToIssueWithParent(ki *rpc.KanbanIssueRPC) IssueWithParent {
	iwp := IssueWithParent{IssueWithCounts: ki.IssueWithCounts}
	if ki.ParentID != "" {
		pid, pt := ki.ParentID, ki.ParentTitle
		iwp.Parent = &pid
		iwp.ParentTitle = &pt
	}
	if ki.Repo != "" {
		repo := ki.Repo
		iwp.Repo = &repo
	}
	return iwp
}

func kanbanRPCToKanbanIssue(ki *rpc.KanbanIssueRPC) KanbanIssue {
	out := KanbanIssue{IssueWithCounts: ki.IssueWithCounts}
	if ki.ParentID != "" {
		pid, pt := ki.ParentID, ki.ParentTitle
		out.Parent = &pid
		out.ParentTitle = &pt
	}
	if ki.Repo != "" {
		repo := ki.Repo
		out.Repo = &repo
	}
	out.IsReady = ki.IsReady
	out.IsDeferred = ki.IsDeferred
	if ki.IsBlocked || ki.BlockedByCount > 0 || len(ki.BlockedBy) > 0 || len(ki.BlockedByDetails) > 0 {
		out.IsBlocked = true
		out.BlockedByCount = ki.BlockedByCount
		out.BlockedBy = ki.BlockedBy
		out.BlockedByDetails = ki.BlockedByDetails
		if out.BlockedByCount == 0 {
			out.BlockedByCount = len(out.BlockedBy)
		}
	}
	return out
}

// listIssuesViaBackend serves a list request through the IssueBackend
// abstraction. Used when there is no daemon connection pool to talk to —
// most importantly, in fleet mode where the local issue daemon is intentionally
// absent and every list query has to hit the fleet HTTP API instead.
//
// This port covers the standard (non-kanban) list shape and enriches kanban
// blocked state from IssueBackend.Blocked.
//
// Parent titles are enriched without an N+1: the board fetches essentially the
// whole workspace, so nearly every parent is already present in the same
// result set. resolveParentTitles builds an id → title index and fills
// ParentTitle from it — O(n), zero extra round-trips.
//
// The index is built over the *pre-filter* list, not the rows being returned.
// That distinction is the whole point: the board sends exclude_status=tombstone,
// so a tombstoned epic is dropped from the result while its children remain,
// and indexing the filtered slice would make every one of those children an
// unresolved parent needing its own request. The excluded issues are already in
// hand — indexing them costs nothing and resolves their children for free.
//
// Only parents absent from the pre-filter list altogether (filtered out
// server-side, or past the limit) need a fetch, and that backfill is bounded:
// at most parentTitleBackfillMax distinct IDs, fetched concurrently under a
// short sub-context, one request each via IssueSummaryBackend where the backend
// offers it, every failure logged at debug and skipped. Past the cap nothing is
// fetched at all and those issues carry no ParentTitle — the FE then renders
// the bare parent ID, which is a graceful fallback rather than an error.
//
// The composite ListKanban RPC's full blocked-by-details lookups are still not
// reproduced: matching them would mean N extra Gets. The blocked summary uses
// one backend Blocked call instead, which preserves kanban correctness without
// the p95 hit.
func (s *issueServiceImpl) listIssuesViaBackend(
	ctx context.Context, be backend.IssueBackend, params ListIssuesParams,
) (*ListIssuesResult, error) {
	opts := listArgsToBackendOpts(params.Args)
	issues, err := be.List(ctx, opts)
	if err != nil {
		return nil, translateBackendError(err)
	}
	// Keep the pre-filter list: it is the parent-title index source. See the
	// doc comment above — indexing only the returned rows turns every child of
	// an excluded epic into a per-issue request.
	all := issues
	issues = excludeBackendIssuesByStatus(issues, params.ExcludeStatus)

	if !params.IncludeBlocked {
		titles := s.resolveParentTitles(ctx, be, all, issues)
		return &ListIssuesResult{Issues: backendIssuesWithParent(issues, titles)}, nil
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

	// After the appends, so issues pulled in by the blocked/deferred merge both
	// contribute to the index and benefit from it. The index source is the
	// pre-filter list plus those same appends; the rows needing a title are the
	// ones actually being returned.
	// Copied first: with no ExcludeStatus, `all` and `issues` are the same
	// slice, and appending to both would have them fight over one backing
	// array — same elements, but in whichever order the map ranged.
	indexSource := make([]backend.IssueData, len(all), len(all)+len(blockedByID)+len(deferredByID))
	copy(indexSource, all)
	indexSource = appendMissingBlockedIssues(indexSource, blockedByID)
	indexSource = appendMissingDeferredIssues(indexSource, deferredByID)
	titles := s.resolveParentTitles(ctx, be, indexSource, issues)

	return &ListIssuesResult{
		KanbanIssues: backendKanbanIssues(issues, titles, blockedByID, readyByID, deferredByID),
	}, nil
}

// Parent-title backfill bounds. The in-result index resolves the overwhelming
// majority of parents; these cap what the residual is allowed to cost.
const (
	// parentTitleBackfillMax is the largest number of distinct unresolved
	// parent IDs worth fetching. Past it the backfill is skipped entirely
	// rather than degrading list latency.
	parentTitleBackfillMax = 16
	// parentTitleBackfillConcurrency bounds in-flight backfill Gets.
	parentTitleBackfillConcurrency = 4
	// parentTitleBackfillTimeout caps the whole backfill; a list request must
	// never wait long on a cosmetic lookup.
	parentTitleBackfillTimeout = time.Second
)

// parentTitleIndex maps issue ID → title over the issues already in hand.
// Issues with an empty ID or an empty title are skipped: an empty title is
// indistinguishable from "unresolved" and must not surface as parent_title "".
// On duplicate IDs the first entry wins.
func parentTitleIndex(issues []backend.IssueData) map[string]string {
	index := make(map[string]string, len(issues))
	for _, d := range issues {
		if d.ID == "" || d.Title == "" {
			continue
		}
		if _, exists := index[d.ID]; exists {
			continue
		}
		index[d.ID] = d.Title
	}
	return index
}

// unresolvedParentIDs returns the distinct parent IDs referenced by issues that
// the index cannot already resolve.
func unresolvedParentIDs(issues []backend.IssueData, index map[string]string) []string {
	var missing []string
	seen := make(map[string]bool)
	for _, d := range issues {
		if d.Parent == "" || seen[d.Parent] {
			continue
		}
		if _, ok := index[d.Parent]; ok {
			continue
		}
		seen[d.Parent] = true
		missing = append(missing, d.Parent)
	}
	return missing
}

// resolveParentTitles builds the parent-title lookup for a backend list result:
// the in-hand index first, then a bounded best-effort backfill for parents that
// index cannot resolve. Never returns an error — a failed title lookup degrades
// to a missing title, never to a failed list.
//
// indexed is the superset of issues already fetched (the pre-filter list plus
// any blocked/deferred merges); needing is the rows actually being returned,
// and only their parents can trigger a backfill. Passing a superset as indexed
// is always safe: it only makes more parents resolvable for free.
func (s *issueServiceImpl) resolveParentTitles(
	ctx context.Context, be backend.IssueBackend, indexed, needing []backend.IssueData,
) map[string]string {
	titles := parentTitleIndex(indexed)
	missing := unresolvedParentIDs(needing, titles)
	if len(missing) == 0 {
		return titles
	}
	if len(missing) > parentTitleBackfillMax {
		slog.Debug("parent-title backfill skipped: over cap",
			"missing", len(missing), "cap", parentTitleBackfillMax)
		return titles
	}
	for id, title := range backfillParentTitles(ctx, be, missing) {
		titles[id] = title
	}
	return titles
}

// backfillParentTitles fetches the given parent IDs concurrently, returning
// whatever resolved. Misses are logged at debug and dropped.
//
// A title is a scalar field, so the fetch goes through IssueSummaryBackend when
// the backend implements it: on fleet-db that is one request instead of the
// three Get costs (issue + dependencies + comments). Backends without the
// extension fall back to Get, which is a single call for them anyway.
func backfillParentTitles(
	ctx context.Context, be backend.IssueBackend, ids []string,
) map[string]string {
	fetchCtx, cancel := context.WithTimeout(ctx, parentTitleBackfillTimeout)
	defer cancel()

	lookup := parentTitleLookup(be)

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		out    = make(map[string]string, len(ids))
		tokens = make(chan struct{}, parentTitleBackfillConcurrency)
	)
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			tokens <- struct{}{}
			defer func() { <-tokens }()

			title, err := lookup(fetchCtx, id)
			if err != nil {
				slog.Debug("parent-title backfill: get failed", "issue_id", id, "err", err)
				return
			}
			// An empty title counts as unresolved, same as the in-hand index.
			if title == "" {
				return
			}
			mu.Lock()
			out[id] = title
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return out
}

// parentTitleLookup returns a single-ID title reader for the backend: the
// one-round-trip GetSummary when the backend offers it, Get otherwise. Both
// treat a nil record with a nil error — a legal "not found" from some
// backends — as an empty title.
func parentTitleLookup(be backend.IssueBackend) func(context.Context, string) (string, error) {
	if summaryBackend, ok := be.(backend.IssueSummaryBackend); ok {
		return func(ctx context.Context, id string) (string, error) {
			data, err := summaryBackend.GetSummary(ctx, id)
			if err != nil || data == nil {
				return "", err
			}
			return data.Title, nil
		}
	}
	return func(ctx context.Context, id string) (string, error) {
		detail, err := be.Get(ctx, id)
		if err != nil || detail == nil {
			return "", err
		}
		return detail.Title, nil
	}
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
	// A fresh slice, deliberately: the caller keeps the unfiltered list as the
	// parent-title index source, and the in-place `issues[:0]` form this used to
	// take would overwrite it through the shared backing array.
	filtered := make([]backend.IssueData, 0, len(issues))
	for _, issue := range issues {
		if !excluded[issue.Status] {
			filtered = append(filtered, issue)
		}
	}
	return filtered
}

func backendIssuesWithParent(issues []backend.IssueData, titles map[string]string) []IssueWithParent {
	out := make([]IssueWithParent, len(issues))
	for i, d := range issues {
		out[i] = backendIssueWithParent(d, titles)
	}
	return out
}

func backendIssueWithParent(d backend.IssueData, titles map[string]string) IssueWithParent {
	iwc := backendIssueDataToWithCounts(&d)
	iwp := IssueWithParent{IssueWithCounts: iwc}
	if d.Parent != "" {
		p := d.Parent
		iwp.Parent = &p
		if title := titles[d.Parent]; title != "" {
			iwp.ParentTitle = &title
		}
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
	titles map[string]string,
	blockedByID map[string]backend.IssueData,
	readyByID map[string]bool,
	deferredByID map[string]backend.IssueData,
) []KanbanIssue {
	out := make([]KanbanIssue, len(issues))
	for i, d := range issues {
		_, deferred := deferredByID[d.ID]
		out[i] = backendKanbanIssue(d, titles, blockedByID[d.ID], readyByID[d.ID], deferred)
	}
	return out
}

func backendKanbanIssue(
	d backend.IssueData,
	titles map[string]string,
	blocked backend.IssueData,
	ready bool,
	deferred bool,
) KanbanIssue {
	iwc := backendIssueDataToWithCounts(&d)
	ki := KanbanIssue{IssueWithCounts: iwc}
	if d.Parent != "" {
		p := d.Parent
		ki.Parent = &p
		if title := titles[d.Parent]; title != "" {
			ki.ParentTitle = &title
		}
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
	args *rpc.ListArgs,
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
	args *rpc.ListArgs,
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
	args *rpc.ListArgs,
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

func readyOptsFromListArgs(args *rpc.ListArgs) backend.ReadyOpts {
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

func deferredOptsFromListArgs(args *rpc.ListArgs) backend.DeferredOpts {
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

func blockedOptsFromListArgs(args *rpc.ListArgs) backend.BlockedOpts {
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

// listArgsToBackendOpts converts the rpc.ListArgs the webui handler
// composes for the daemon path into the backend.ListOpts shape. We map
// only fields that backend.IssueBackend.List accepts uniformly across
// drivers — fleet-db rejects several optional filters (see ListOpts
// "fleet-db: unsupported" comments), so passing them through would
// surface as runtime errors instead of being silently ignored.
func listArgsToBackendOpts(a *rpc.ListArgs) backend.ListOpts {
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
