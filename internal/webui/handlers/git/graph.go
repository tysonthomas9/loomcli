package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// IssueBackendFn returns the active backend.IssueBackend. Passed through to
// HandleGraph so the handler can fall back to the backend when no daemon
// pool is available (fleet mode) or pool acquisition fails.
type IssueBackendFn func() backend.IssueBackend

// BlockedResponse wraps the blocked issues data for JSON response.
type BlockedResponse struct {
	Success bool                  `json:"success"`
	Data    []*types.BlockedIssue `json:"data,omitempty"`
	Error   string                `json:"error,omitempty"`
}

// BlockedClient is an interface for testing blocked operations.
// The production code uses *rpc.Client which implements this interface.
type BlockedClient interface {
	Blocked(args *rpc.BlockedArgs) (*rpc.Response, error)
}

// BlockedConnectionGetter is an interface for testing blocked handler pool operations.
type BlockedConnectionGetter interface {
	Get(ctx context.Context) (BlockedClient, error)
	Put(client BlockedClient)
	Discard(client BlockedClient)
}

// blockedPoolAdapter wraps daemon.Pool to implement BlockedConnectionGetter.
type blockedPoolAdapter struct {
	pool daemon.Pool
}

func (p *blockedPoolAdapter) Get(ctx context.Context) (BlockedClient, error) {
	return p.pool.Get(ctx)
}

func (p *blockedPoolAdapter) Put(client BlockedClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

func (p *blockedPoolAdapter) Discard(client BlockedClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Discard(c)
	}
}

// GraphDependency represents a dependency relationship for graph visualization.
type GraphDependency struct {
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
}

// GraphIssue represents a slim issue with dependency data for graph visualization.
type GraphIssue struct {
	ID           string             `json:"id"`
	Title        string             `json:"title"`
	Status       string             `json:"status"`
	Priority     int                `json:"priority"`
	IssueType    string             `json:"issue_type"`
	Labels       []string           `json:"labels,omitempty"`
	Dependencies []*GraphDependency `json:"dependencies,omitempty"`
	DeferUntil   string             `json:"defer_until,omitempty"`
	DueAt        string             `json:"due_at,omitempty"`
}

// GraphResponse wraps the graph data for JSON response.
type GraphResponse struct {
	Success bool          `json:"success"`
	Data    []*GraphIssue `json:"data,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// GraphClient is an interface for testing graph operations.
// The production code uses *rpc.Client which implements this interface.
type GraphClient interface {
	GetGraphData(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error)
}

// GraphConnectionGetter is an interface for testing graph handler pool operations.
type GraphConnectionGetter interface {
	Get(ctx context.Context) (GraphClient, error)
	Put(client GraphClient)
	Discard(client GraphClient)
}

// graphPoolAdapter wraps daemon.Pool to implement GraphConnectionGetter.
type graphPoolAdapter struct {
	pool daemon.Pool
}

func (p *graphPoolAdapter) Get(ctx context.Context) (GraphClient, error) {
	return p.pool.Get(ctx)
}

func (p *graphPoolAdapter) Put(client GraphClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

func (p *graphPoolAdapter) Discard(client GraphClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Discard(c)
	}
}

// HandleBlocked returns issues that have blocking dependencies (waiting on other issues).
func HandleBlocked(pool daemon.Pool) http.HandlerFunc {
	return HandleBlockedWithBackendFallback(pool, nil)
}

// HandleBlockedWithBackendFallback returns a handler that serves the blocked
// endpoint through the daemon pool when available and transparently falls
// back to the supplied backend.IssueBackend when the pool is nil or the
// pool-backed RPC path produces a 5xx (fleet mode has no daemon). The
// fallback emits the {success:true, data:[...]} envelope mirrored from the
// pool path; each item is serialized directly from backend.IssueData, which
// overlaps the FE's Issue/BlockedIssue shape on id/title/status/priority/
// issue_type/assignee/labels/parent/source_repo/created_at/updated_at. Fields
// only the daemon populates (blocked_by*, blocked_by_details) are absent
// from the backend fallback — the FE degrades to "unknown blocker count"
// rather than crashing.
//
// backendFn may be nil — in that case the behaviour is identical to the
// legacy HandleBlocked(pool) path, returning a 503 when the pool is unusable.
func HandleBlockedWithBackendFallback(pool daemon.Pool, backendFn IssueBackendFn) http.HandlerFunc {
	var poolAdapter BlockedConnectionGetter
	if pool != nil {
		poolAdapter = &blockedPoolAdapter{pool: pool}
	}
	poolHandler := HandleBlockedWithPool(poolAdapter)
	if backendFn == nil {
		return poolHandler
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			if serveBlockedViaBackend(w, r, backendFn) {
				return
			}
			poolHandler(w, r)
			return
		}
		rec := &graphInterceptor{header: http.Header{}}
		poolHandler(rec, r)
		if rec.statusCode >= 200 && rec.statusCode < 500 {
			rec.flushTo(w)
			return
		}
		if serveBlockedViaBackend(w, r, backendFn) {
			return
		}
		rec.flushTo(w)
	}
}

// serveBlockedViaBackend materializes a BlockedResponse-style envelope from
// the supplied IssueBackend and writes it to w. Returns true when it served
// the request (including backend errors), false when no backend is wired so
// the caller can fall through to the pool-error path.
func serveBlockedViaBackend(w http.ResponseWriter, r *http.Request, backendFn IssueBackendFn) bool {
	if backendFn == nil {
		return false
	}
	be := backendFn()
	if be == nil {
		return false
	}
	args, err := parseBlockedParams(r)
	if err != nil {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return true
	}
	opts := backend.BlockedOpts{
		ParentID: args.ParentID,
		Assignee: args.Assignee,
		Priority: args.Priority,
		Type:     args.Type,
		Limit:    args.Limit,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	issues, err := be.Blocked(ctx, opts)
	if err != nil {
		slog.Error("backend error in HandleBlocked fallback", "err", err)
		handler.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   "failed to list blocked issues",
		})
		return true
	}
	if issues == nil {
		issues = []backend.IssueData{}
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    issues,
	})
	return true
}

// HandleBlockedWithPool is the internal implementation that accepts an interface for testing.
func HandleBlockedWithPool(pool BlockedConnectionGetter) http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, BlockedResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		// Parse query parameters into BlockedArgs
		args, err := parseBlockedParams(r)
		if err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, BlockedResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		// Acquire connection with 5-second timeout
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			slog.Error("pool error in HandleBlocked", "err", err)
			handler.WriteJSON(w, status, BlockedResponse{
				Success: false,
				Error:   "daemon not available",
			})
			return
		}
		rpcOK := false
		defer func() {
			if rpcOK {
				pool.Put(client)
			} else {
				pool.Discard(client)
			}
		}()

		// Execute Blocked RPC call
		resp, err := client.Blocked(args)
		if err != nil {
			slog.Error("RPC error in HandleBlocked", "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, BlockedResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}
		rpcOK = true

		if !resp.Success {
			handler.WriteJSON(w, http.StatusInternalServerError, BlockedResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		// Parse the blocked issues from RPC response
		var issues []*types.BlockedIssue
		if err := json.Unmarshal(resp.Data, &issues); err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, BlockedResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse blocked issues: %v", err),
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, BlockedResponse{
			Success: true,
			Data:    issues,
		})
	}
}

// HandleGraph returns issues with full dependency data for graph visualization.
func HandleGraph(pool daemon.Pool) http.HandlerFunc {
	return HandleGraphWithBackendFallback(pool, nil)
}

// HandleGraphWithBackendFallback returns a handler that serves the graph
// endpoint through the daemon pool when available and transparently falls
// back to the supplied backend.IssueBackend when the pool is nil or the
// pool-backed RPC path can't be reached (fleet mode has no daemon). The
// backend fallback serves a reduced projection: it covers the top-level
// fields plus Dependencies sourced from backend.Get, which is enough for
// the SPA GraphView to lay out parent-child + blocker edges.
//
// backendFn may be nil — in that case the behaviour is identical to the
// legacy HandleGraph(pool) path, returning a 503 when the pool is unusable.
func HandleGraphWithBackendFallback(pool daemon.Pool, backendFn IssueBackendFn) http.HandlerFunc {
	var poolAdapter GraphConnectionGetter
	if pool != nil {
		poolAdapter = &graphPoolAdapter{pool: pool}
	}
	poolHandler := HandleGraphWithPool(poolAdapter)
	if backendFn == nil {
		return poolHandler
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// Shortcut fleet mode: no pool at all, serve exclusively from the
		// backend. Otherwise try the pool path first via a response
		// recorder so a 503/504 from pool acquisition transparently
		// degrades to the backend fallback without double-writing.
		if pool == nil {
			if serveGraphViaBackend(w, r, backendFn) {
				return
			}
			poolHandler(w, r) // 503 path for nil backend + nil pool
			return
		}
		rec := &graphInterceptor{header: http.Header{}}
		poolHandler(rec, r)
		// Pool path succeeded (2xx) or produced a client-authored error
		// (4xx) — forward as-is, don't second-guess.
		if rec.statusCode >= 200 && rec.statusCode < 500 {
			rec.flushTo(w)
			return
		}
		// 5xx from the pool layer — try the backend. If the backend can't
		// serve either, fall through and return the original pool-layer
		// error so the FE still sees a coherent response.
		if serveGraphViaBackend(w, r, backendFn) {
			return
		}
		rec.flushTo(w)
	}
}

// graphInterceptor captures the pool-handler's response so the wrapper can
// decide whether to forward or fall through to the backend path without
// double-writing to the real ResponseWriter.
type graphInterceptor struct {
	header     http.Header
	body       []byte
	statusCode int
}

func (g *graphInterceptor) Header() http.Header { return g.header }

func (g *graphInterceptor) WriteHeader(code int) {
	if g.statusCode == 0 {
		g.statusCode = code
	}
}

func (g *graphInterceptor) Write(b []byte) (int, error) {
	if g.statusCode == 0 {
		g.statusCode = http.StatusOK
	}
	g.body = append(g.body, b...)
	return len(b), nil
}

func (g *graphInterceptor) flushTo(w http.ResponseWriter) {
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

// serveGraphViaBackend materializes a GraphResponse from the supplied
// IssueBackend and writes it to w. Returns true when it served the request
// (success OR backend error), false when no backend is wired so the caller
// can fall through to the pool-error path.
func serveGraphViaBackend(w http.ResponseWriter, r *http.Request, backendFn IssueBackendFn) bool {
	if backendFn == nil {
		return false
	}
	be := backendFn()
	if be == nil {
		return false
	}
	status, includeClosed, _ := parseGraphParams(r)
	validStatuses := map[string]bool{"all": true, "open": true, "closed": true}
	if !validStatuses[status] {
		handler.WriteJSON(w, http.StatusBadRequest, GraphResponse{
			Success: false,
			Error:   fmt.Sprintf("invalid status: %s (must be all, open, or closed)", status),
		})
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	list, err := be.List(ctx, backend.ListOpts{})
	if err != nil {
		slog.Error("backend error in HandleGraph fallback", "err", err)
		handler.WriteJSON(w, http.StatusInternalServerError, GraphResponse{
			Success: false,
			Error:   "failed to list issues",
		})
		return true
	}

	graphIssues := make([]*GraphIssue, 0, len(list))
	for _, d := range list {
		if !includeGraphIssue(d.Status, status, includeClosed) {
			continue
		}
		gi := &GraphIssue{
			ID:        d.ID,
			Title:     d.Title,
			Status:    d.Status,
			Priority:  d.Priority,
			IssueType: d.IssueType,
			Labels:    d.Labels,
		}
		// Fetch detail to pull dependencies; fleet and beads backends both
		// populate DependencyData from a single Get. If Get fails, leave
		// Dependencies empty so the node still appears without edges.
		if detail, detErr := be.Get(ctx, d.ID); detErr == nil && detail != nil {
			deps := make([]*GraphDependency, 0, len(detail.Dependencies))
			for _, dep := range detail.Dependencies {
				deps = append(deps, &GraphDependency{
					DependsOnID: dep.DependsOnID,
					Type:        dep.Type,
				})
			}
			gi.Dependencies = deps
		}
		// Synthesize a parent-child edge when the backend reports a
		// parent but didn't encode it in Dependencies (beads slim list
		// path). The FE treats parent-child as a first-class edge type.
		if d.Parent != "" {
			hasParent := false
			for _, dep := range gi.Dependencies {
				if dep.DependsOnID == d.Parent && dep.Type == "parent-child" {
					hasParent = true
					break
				}
			}
			if !hasParent {
				gi.Dependencies = append(gi.Dependencies, &GraphDependency{
					DependsOnID: d.Parent,
					Type:        "parent-child",
				})
			}
		}
		if d.DeferUntil != nil {
			gi.DeferUntil = d.DeferUntil.Format(time.RFC3339)
		}
		if d.DueAt != nil {
			gi.DueAt = d.DueAt.Format(time.RFC3339)
		}
		graphIssues = append(graphIssues, gi)
	}

	handler.WriteJSON(w, http.StatusOK, GraphResponse{
		Success: true,
		Data:    graphIssues,
	})
	return true
}

// includeGraphIssue applies the status filter used by parseGraphParams.
// Mirrors the exclude-set logic in HandleGraphWithPool so the fallback
// preserves filter semantics.
func includeGraphIssue(issueStatus, filter string, includeClosed bool) bool {
	if issueStatus == "tombstone" {
		return false
	}
	switch filter {
	case "open":
		return issueStatus != "closed" && issueStatus != "tombstone"
	case "closed":
		return issueStatus == "closed"
	default: // "all"
		if !includeClosed && issueStatus == "closed" {
			return false
		}
		return true
	}
}

// HandleGraphWithPool is the internal implementation that accepts an interface for testing.
func HandleGraphWithPool(pool GraphConnectionGetter) http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, GraphResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		// Parse query parameters
		status, includeClosed, sourceRepos := parseGraphParams(r)

		// Validate status parameter
		validStatuses := map[string]bool{"all": true, "open": true, "closed": true}
		if !validStatuses[status] {
			handler.WriteJSON(w, http.StatusBadRequest, GraphResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid status: %s (must be all, open, or closed)", status),
			})
			return
		}

		// Acquire connection with timeout
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			httpStatus := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				httpStatus = http.StatusGatewayTimeout
			}
			slog.Error("pool error in HandleGraph", "err", err)
			handler.WriteJSON(w, httpStatus, GraphResponse{
				Success: false,
				Error:   "daemon not available",
			})
			return
		}
		rpcOK := false
		defer func() {
			if rpcOK {
				pool.Put(client)
			} else {
				pool.Discard(client)
			}
		}()

		// Build GetGraphData args based on status filter
		graphArgs := &rpc.GetGraphDataArgs{
			SourceRepos: sourceRepos,
		}
		if status == "open" {
			graphArgs.ExcludeStatus = []string{"closed", "tombstone"}
		} else if status == "closed" {
			graphArgs.Status = "closed"
		} else {
			// "all" - exclude only tombstones
			graphArgs.ExcludeStatus = []string{"tombstone"}
		}
		// Don't include closed if explicitly disabled
		if !includeClosed && status == "all" {
			graphArgs.ExcludeStatus = append(graphArgs.ExcludeStatus, "closed")
		}
		// Single RPC call replaces List + N×Show
		result, err := client.GetGraphData(graphArgs)
		if err != nil {
			slog.Error("RPC error in HandleGraph", "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, GraphResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}
		rpcOK = true

		// Convert RPC response to HTTP response format
		graphIssues := make([]*GraphIssue, 0, len(result.Issues))
		for _, summary := range result.Issues {
			var graphDeps []*GraphDependency
			for _, dep := range summary.Dependencies {
				graphDeps = append(graphDeps, &GraphDependency{
					DependsOnID: dep.DependsOnID,
					Type:        dep.Type,
				})
			}
			graphIssues = append(graphIssues, &GraphIssue{
				ID:           summary.ID,
				Title:        summary.Title,
				Status:       summary.Status,
				Priority:     summary.Priority,
				IssueType:    summary.IssueType,
				Labels:       summary.Labels,
				Dependencies: graphDeps,
				DeferUntil:   summary.DeferUntil,
				DueAt:        summary.DueAt,
			})
		}

		handler.WriteJSON(w, http.StatusOK, GraphResponse{
			Success: true,
			Data:    graphIssues,
		})
	}
}

// parseBlockedParams parses query parameters into rpc.BlockedArgs.
func parseBlockedParams(r *http.Request) (*rpc.BlockedArgs, error) {
	args := &rpc.BlockedArgs{}
	q := r.URL.Query()

	// String parameters
	if v := q.Get("parent_id"); v != "" {
		args.ParentID = v
	}
	if v := q.Get("assignee"); v != "" {
		args.Assignee = v
	}
	if v := q.Get("type"); v != "" {
		args.Type = v
	}

	// Integer parameters
	if v := q.Get("priority"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid priority value: %s (must be an integer 0-4)", v)
		}
		if p < 0 || p > 4 {
			return nil, fmt.Errorf("priority must be between 0 and 4 (got %d)", p)
		}
		args.Priority = &p
	}
	if v := q.Get("limit"); v != "" {
		l, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid limit value: %s (must be a non-negative integer)", v)
		}
		if l < 0 {
			return nil, fmt.Errorf("limit must be non-negative (got %d)", l)
		}
		if l > handler.MaxListLimit {
			l = handler.MaxListLimit
		}
		args.Limit = l
	}

	return args, nil
}

// parseGraphParams parses query parameters for the graph endpoint.
func parseGraphParams(r *http.Request) (status string, includeClosed bool, sourceRepos []string) {
	q := r.URL.Query()
	status = q.Get("status")
	if status == "" {
		status = "all"
	}
	includeClosedStr := q.Get("include_closed")
	includeClosed = includeClosedStr != "false" // Default true
	sourceRepos = parseArrayParam(q, "source_repos")
	return status, includeClosed, sourceRepos
}

// parseArrayParam splits a comma-separated query parameter into trimmed strings.
// Returns nil if the key is absent.
func parseArrayParam(q interface{ Get(string) string }, key string) []string {
	v := q.Get(key)
	if v == "" {
		return nil
	}
	return splitAndTrim(v)
}

// splitAndTrim splits a comma-separated string and trims whitespace from each element.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// parseIntParam parses an optional integer query parameter.
// Returns (nil, nil) if the key is absent.
func parseIntParam(q interface{ Get(string) string }, key string) (*int, error) {
	v := q.Get(key)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, fmt.Errorf("invalid %s value: %s (must be an integer)", key, v)
	}
	return &n, nil
}
