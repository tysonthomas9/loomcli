package issues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"net/url"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// IssueBackendFn returns the active backend.IssueBackend. Consumed by
// HandleReadyWithBackend when no daemon pool is available (fleet mode).
// Defined locally to mirror the git package's identical-shape type
// without creating a package cross-import in production code.
//
// ctx carries the per-request workspace ID so cloud-mode wirings can route
// to a per-workspace fleet-db backend.
type IssueBackendFn func(ctx context.Context) backend.IssueBackend

// ReadyIssueWithParent extends Issue with parent info for /api/ready.
// This enables epic swimlane grouping in the Kanban view.
type ReadyIssueWithParent struct {
	*types.Issue
	Parent      *string `json:"parent,omitempty"`       // Parent issue ID (null for root-level issues)
	ParentTitle *string `json:"parent_title,omitempty"` // Parent issue title for display
	Repo        *string `json:"repo,omitempty"`         // Repository that owns this issue
}

// ReadyResponse wraps the ready issues data for JSON response.
type ReadyResponse struct {
	Success bool                    `json:"success"`
	Data    []*ReadyIssueWithParent `json:"data,omitempty"`
	Error   string                  `json:"error,omitempty"`
}

// readyClient is an internal interface for testing ready issue operations.
// The production code uses *rpc.Client which implements this interface.
type readyClient interface {
	Ready(args *rpc.ReadyArgs) (*rpc.Response, error)
	GetParentIDs(args *rpc.GetParentIDsArgs) (*rpc.GetParentIDsResponse, error)
	List(args *rpc.ListArgs) (*rpc.Response, error)
}

// readyConnectionGetter is an internal interface for testing ready handler pool operations.
type readyConnectionGetter interface {
	Get(ctx context.Context) (readyClient, error)
	Put(client readyClient)
	Discard(client readyClient)
}

// readyPoolAdapter wraps daemon.Pool to implement readyConnectionGetter.
type readyPoolAdapter struct {
	pool daemon.Pool
}

func (p *readyPoolAdapter) Get(ctx context.Context) (readyClient, error) {
	return p.pool.Get(ctx)
}

func (p *readyPoolAdapter) Put(client readyClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

func (p *readyPoolAdapter) Discard(client readyClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Discard(c)
	}
}

// HandleReady returns issues ready to work on (open/in_progress with no blockers).
func HandleReady(pool daemon.Pool) http.HandlerFunc {
	return HandleReadyWithBackend(pool, nil)
}

// HandleReadyWithBackend returns a handler that serves the ready endpoint
// from exactly one configured source: the daemon pool when present, otherwise
// the supplied backend.IssueBackend for pool-less fleet mode.
//
// backendFn may be nil — in that case the behavior is identical to the
// pool-only path, returning a 503 when the pool is unusable.
func HandleReadyWithBackend(pool daemon.Pool, backendFn IssueBackendFn) http.HandlerFunc {
	var poolAdapter readyConnectionGetter
	if pool != nil {
		poolAdapter = &readyPoolAdapter{pool: pool}
	}
	poolHandler := handleReadyWithPool(poolAdapter)
	if pool != nil || backendFn == nil {
		return poolHandler
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if serveReadyViaBackend(w, r, backendFn) {
			return
		}
		poolHandler(w, r)
	}
}

// readyInterceptor captures the pool-handler's response so the wrapper can
// decide whether to forward or fall through to the backend path without
// double-writing to the real ResponseWriter. Mirrors graphInterceptor in
// the git package.
type readyInterceptor struct {
	header     http.Header
	body       []byte
	statusCode int
}

func (g *readyInterceptor) Header() http.Header { return g.header }

func (g *readyInterceptor) WriteHeader(code int) {
	if g.statusCode == 0 {
		g.statusCode = code
	}
}

func (g *readyInterceptor) Write(b []byte) (int, error) {
	if g.statusCode == 0 {
		g.statusCode = http.StatusOK
	}
	g.body = append(g.body, b...)
	return len(b), nil
}

func (g *readyInterceptor) flushTo(w http.ResponseWriter) {
	for k, vs := range g.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if g.statusCode == 0 {
		g.statusCode = http.StatusOK
	}
	w.WriteHeader(g.statusCode)
	_, _ = w.Write(g.body)
}

// serveReadyViaBackend materializes a ready-style response from the supplied
// IssueBackend and writes it to w. Returns true when it served the request
// (including backend errors), false when no backend is wired so the caller
// can fall through to the pool-error path.
//
//nolint:funlen // Handler keeps the ready response shaping in one place.
func serveReadyViaBackend(w http.ResponseWriter, r *http.Request, backendFn IssueBackendFn) bool {
	if backendFn == nil {
		return false
	}
	be := backendFn(r.Context())
	if be == nil {
		return false
	}
	args, err := parseReadyParams(r)
	if err != nil {
		handler.WriteJSON(w, http.StatusBadRequest, ReadyResponse{
			Success: false,
			Error:   err.Error(),
		})
		return true
	}
	opts := backend.ReadyOpts{
		Assignee:    args.Assignee,
		Unassigned:  args.Unassigned,
		Priority:    args.Priority,
		Type:        args.Type,
		ParentID:    args.ParentID,
		Limit:       args.Limit,
		SortPolicy:  args.SortPolicy,
		Labels:      args.Labels,
		LabelsAny:   args.LabelsAny,
		MolType:     args.MolType,
		SourceRepos: args.SourceRepos,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	issues, err := be.Ready(ctx, opts)
	if err != nil {
		slog.Error("backend error in HandleReady", "err", err)
		handler.WriteJSON(w, http.StatusInternalServerError, ReadyResponse{
			Success: false,
			Error:   "failed to list ready issues",
		})
		return true
	}
	// Project each IssueData onto the pool-path wire shape. Parent/Repo are
	// surfaced from IssueData directly and ParentTitle is left nil.
	out := make([]*ReadyIssueWithParent, 0, len(issues))
	for i := range issues {
		d := &issues[i]
		iwp := &ReadyIssueWithParent{Issue: issueDataToTypesIssue(d)}
		if d.Parent != "" {
			p := d.Parent
			iwp.Parent = &p
		}
		if d.SourceRepo != "" {
			r := d.SourceRepo
			iwp.Repo = &r
		}
		out = append(out, iwp)
	}
	handler.WriteJSON(w, http.StatusOK, ReadyResponse{
		Success: true,
		Data:    out,
	})
	return true
}

// issueDataToTypesIssue projects a backend.IssueData into the slim
// *types.Issue shape that ReadyIssueWithParent embeds. Only the fields the
// backend slim projection populates are carried; unknown fields stay at
// their zero values (the FE already tolerates missing optional fields on a
// ready list item).
//
// Design must be carried: agents reading the ready queue through the API
// backend (LOOM_SERVER_URL set) apply the task router's has_design filter
// (ReadyToImplement = HasDesign && !needs-revision). Dropping it here made
// every ready task look design-less, so implementation agents could never
// claim work (perpetual NoWork) while planners — gated on !HasDesign — were
// unaffected. It is omitempty, so empty designs add nothing to the wire.
func issueDataToTypesIssue(d *backend.IssueData) *types.Issue {
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
		SourceRepo:       d.SourceRepo,
		Design:           d.Design,
		DesignArtifactID: d.DesignArtifactID,
		DesignFormat:     d.DesignFormat,
		HasDesign:        d.HasDesign || d.Design != "",
		CreatedAt:        d.CreatedAt,
		UpdatedAt:        d.UpdatedAt,
		DueAt:            d.DueAt,
		DeferUntil:       d.DeferUntil,
	}
	return issue
}

// executeReadyRPC acquires a connection, calls Ready, and returns filtered issues.
func executeReadyRPC(ctx context.Context, pool readyConnectionGetter, args *rpc.ReadyArgs) (readyClient, []*types.Issue, int, error) {
	client, err := pool.Get(ctx)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, daemon.ErrDaemonStarting) {
			return nil, nil, http.StatusServiceUnavailable, daemon.ErrDaemonStarting
		}
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		return nil, nil, status, err
	}

	resp, err := client.Ready(args)
	if err != nil {
		pool.Discard(client)
		return nil, nil, http.StatusInternalServerError, fmt.Errorf("RPC error: %w", err)
	}

	if !resp.Success {
		pool.Put(client)
		return nil, nil, http.StatusInternalServerError, fmt.Errorf("%s", resp.Error)
	}

	var issues []*types.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		pool.Put(client)
		return nil, nil, http.StatusInternalServerError, fmt.Errorf("failed to parse ready issues: %w", err)
	}

	issues = filterUnclosedBlockers(client, issues)
	return client, issues, 0, nil
}

// buildReadyResponse enriches issues with parent info for the response.
func buildReadyResponse(client readyClient, issues []*types.Issue) []*ReadyIssueWithParent {
	issueIDs := make([]string, len(issues))
	for i, issue := range issues {
		issueIDs[i] = issue.ID
	}

	parentResp, err := client.GetParentIDs(&rpc.GetParentIDsArgs{IssueIDs: issueIDs})
	if err != nil {
		slog.Error("failed to get parent IDs for ready issues", "err", err)
		parentResp = &rpc.GetParentIDsResponse{Parents: make(map[string]*rpc.ParentInfo)}
	}

	result := make([]*ReadyIssueWithParent, len(issues))
	for i, issue := range issues {
		iwp := &ReadyIssueWithParent{Issue: issue}
		if parentInfo, ok := parentResp.Parents[issue.ID]; ok {
			iwp.Parent = &parentInfo.ParentID
			iwp.ParentTitle = &parentInfo.ParentTitle
		}
		if issue.SourceRepo != "" {
			iwp.Repo = &issue.SourceRepo
		}
		result[i] = iwp
	}
	return result
}

// handleReadyWithPool is the internal implementation that accepts an interface for testing.
//
// Put/Discard closure) adds ~7 lines of ceremony; extraction would obscure the
// happy path. The transport-corruption fix (LOOM-SERVER-3) is the reason.
//
//nolint:funlen // Conditional pool.Discard defer pattern (rpcOK flag + deferred
func handleReadyWithPool(pool readyConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, ReadyResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		args, err := parseReadyParams(r)
		if err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, ReadyResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, issues, status, err := executeReadyRPC(ctx, pool, args)
		if err != nil {
			slog.Error("handleReady error", "err", err)
			if errors.Is(err, daemon.ErrDaemonStarting) {
				w.Header().Set("Retry-After", "5")
			}
			handler.WriteJSON(w, status, ReadyResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}
		// executeReadyRPC already discards on RPC error; if we reach here,
		// the Ready RPC succeeded. filterUnclosedBlockers and buildReadyResponse
		// make additional non-fatal RPCs; we optimistically assume they succeeded.
		// A failure there may corrupt the connection, but returning partial data
		// is acceptable and a deeper refactor would be needed to track those.
		rpcOK := false
		defer func() {
			if rpcOK {
				pool.Put(client)
			} else {
				pool.Discard(client)
			}
		}()

		rpcOK = true

		if len(issues) == 0 {
			handler.WriteJSON(w, http.StatusOK, ReadyResponse{
				Success: true,
				Data:    []*ReadyIssueWithParent{},
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, ReadyResponse{
			Success: true,
			Data:    buildReadyResponse(client, issues),
		})
	}
}

// filterUnclosedBlockers removes issues whose blocking dependencies are not yet closed.
// It extracts dependency target IDs from the ready result and fetches only those
// specific issues via client.List(IDs: ...) instead of a full table scan.
// On error, returns the original list unfiltered (non-fatal).
func filterUnclosedBlockers(client readyClient, issues []*types.Issue) []*types.Issue {
	// Extract unique dependency target IDs that affect ready work
	depIDSet := make(map[string]struct{})
	for _, issue := range issues {
		for _, dep := range issue.Dependencies {
			if dep.Type.AffectsReadyWork() {
				depIDSet[dep.DependsOnID] = struct{}{}
			}
		}
	}

	// Fast path: no blocking dependencies means nothing to filter
	if len(depIDSet) == 0 {
		return issues
	}

	depIDs := make([]string, 0, len(depIDSet))
	for id := range depIDSet {
		depIDs = append(depIDs, id)
	}

	listResp, err := client.List(&rpc.ListArgs{IDs: depIDs})
	if err != nil {
		slog.Error("failed to fetch blocker issues for filtering", "err", err)
		return issues
	}
	if !listResp.Success {
		slog.Error("list RPC failed for blocker filtering", "err", listResp.Error)
		return issues
	}

	var blockerIssues []*types.Issue
	if err := json.Unmarshal(listResp.Data, &blockerIssues); err != nil {
		slog.Error("failed to parse blocker issues for filtering", "err", err)
		return issues
	}

	unclosedIDs := make(map[string]bool, len(blockerIssues))
	for _, issue := range blockerIssues {
		if issue.Status != types.StatusClosed {
			unclosedIDs[issue.ID] = true
		}
	}

	filtered := make([]*types.Issue, 0, len(issues))
	for _, issue := range issues {
		if !hasUnclosedBlockersTyped(issue.Dependencies, unclosedIDs) {
			filtered = append(filtered, issue)
		}
	}
	return filtered
}

// hasUnclosedBlockersTyped returns true if any blocking dependency points to
// an issue that is still unclosed. Uses types.Dependency (pointer slice).
func hasUnclosedBlockersTyped(deps []*types.Dependency, unclosedIDs map[string]bool) bool {
	for _, dep := range deps {
		if dep.Type.IsDirectBlocker() && unclosedIDs[dep.DependsOnID] {
			return true
		}
	}
	return false
}

// parseReadyParams parses query parameters into rpc.ReadyArgs.
func parseReadyParams(r *http.Request) (*rpc.ReadyArgs, error) {
	args := &rpc.ReadyArgs{}
	q := r.URL.Query()

	args.Assignee = handler.ParseStringParam(q, "assignee")
	args.Type = handler.ParseStringParam(q, "type")
	args.ParentID = handler.ParseStringParam(q, "parent_id")

	if err := parseReadyValidatedStrings(q, args); err != nil {
		return nil, err
	}

	var err error
	if args.Unassigned, err = handler.ParseBoolParam(q, "unassigned"); err != nil {
		return nil, err
	}
	if err := parseReadyIntParams(q, args); err != nil {
		return nil, err
	}

	args.Labels = handler.ParseArrayParam(q, "labels")
	args.LabelsAny = handler.ParseArrayParam(q, "labels_any")
	args.SourceRepos = handler.ParseArrayParam(q, "source_repos")

	return args, nil
}

// parseReadyValidatedStrings parses and validates mol_type and sort parameters.
func parseReadyValidatedStrings(q url.Values, args *rpc.ReadyArgs) error {
	if v := handler.ParseStringParam(q, "mol_type"); v != "" {
		if !types.MolType(v).IsValid() {
			return fmt.Errorf("invalid mol_type: %s (must be swarm, patrol, or work)", v)
		}
		args.MolType = v
	}
	if v := handler.ParseStringParam(q, "sort"); v != "" {
		if !types.SortPolicy(v).IsValid() {
			return fmt.Errorf("invalid sort policy: %s (must be hybrid, priority, or oldest)", v)
		}
		args.SortPolicy = v
	}
	return nil
}

// parseReadyIntParams parses priority and limit integer parameters.
func parseReadyIntParams(q url.Values, args *rpc.ReadyArgs) error {
	var err error
	if args.Priority, err = handler.ParseIntParam(q, "priority"); err != nil {
		return err
	}
	if args.Priority != nil && (*args.Priority < 0 || *args.Priority > 4) {
		return fmt.Errorf("priority must be between 0 and 4 (got %d)", *args.Priority)
	}
	limitPtr, err := handler.ParseIntParam(q, "limit")
	if err != nil {
		return err
	}
	if limitPtr != nil {
		if *limitPtr < 0 {
			return fmt.Errorf("limit must be non-negative (got %d)", *limitPtr)
		}
		args.Limit = *limitPtr
	}
	return nil
}
