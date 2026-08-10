package git

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// IssueBackendFn returns the active backend.IssueBackend. Passed through to
// handlers that serve pool-less fleet mode.
//
// ctx carries the per-request workspace ID so cloud-mode wirings can route
// to a per-workspace fleet-db backend.
type IssueBackendFn func(ctx context.Context) backend.IssueBackend

// BlockedResponse wraps the blocked issues data for JSON response.
type BlockedResponse struct {
	Success bool                `json:"success"`
	Data    []backend.IssueData `json:"data,omitempty"`
	Error   string              `json:"error,omitempty"`
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

func HandleBlockedWithBackend(backendFn IssueBackendFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !serveBlockedViaBackend(w, r, backendFn) {
			handler.WriteJSON(w, http.StatusServiceUnavailable, BlockedResponse{Success: false, Error: "issue backend not configured"})
		}
	}
}

func HandleGraphWithBackend(backendFn IssueBackendFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !serveGraphViaBackend(w, r, backendFn) {
			handler.WriteJSON(w, http.StatusServiceUnavailable, GraphResponse{Success: false, Error: "issue backend not configured"})
		}
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
	be := backendFn(r.Context())
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
		slog.Error("backend error in HandleBlocked", "err", err)
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

// serveGraphViaBackend materializes a GraphResponse from the supplied
// IssueBackend and writes it to w. Returns true when it served the request
// (success OR backend error), false when no backend is wired so the caller
// can fall through to the pool-error path.
//
//nolint:funlen // Handler translates backend graph data into the established API shape.
func serveGraphViaBackend(w http.ResponseWriter, r *http.Request, backendFn IssueBackendFn) bool {
	if backendFn == nil {
		return false
	}
	be := backendFn(r.Context())
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
		slog.Error("backend error in HandleGraph", "err", err)
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
		// Fetch detail to pull dependencies; issue backends populate
		// DependencyData from a single Get. If Get fails, leave
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
		// Synthesize a parent-child edge when the backend reports a parent but
		// did not encode it in Dependencies. The FE treats parent-child as a
		// first-class edge type.
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
// Mirrors the exclude-set logic in HandleGraphWithPool so the backend
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

type blockedFilter struct {
	ParentID string
	Assignee string
	Priority *int
	Type     string
	Limit    int
}

// parseBlockedParams parses and validates blocked-list query parameters.
func parseBlockedParams(r *http.Request) (*blockedFilter, error) {
	args := &blockedFilter{}
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
