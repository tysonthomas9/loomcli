package git

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// WorkItemQueries is the narrow owner query surface consumed by graph and
// blocked-list delivery. It carries no Work Items mutation authority.
type WorkItemQueries interface {
	List(context.Context, workitems.ListQuery) (*workitems.ListResult, error)
	Get(context.Context, workitems.GetQuery) (*workitems.IssueDetail, error)
	Blocked(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error)
}

// BlockedResponse wraps the blocked issues data for JSON response.
type BlockedResponse struct {
	Success bool                     `json:"success"`
	Data    []workitems.IssueSummary `json:"data,omitempty"`
	Error   string                   `json:"error,omitempty"`
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

func HandleBlocked(queries WorkItemQueries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !serveBlocked(w, r, queries) {
			handler.WriteJSON(w, http.StatusServiceUnavailable, BlockedResponse{Success: false, Error: "Work Items service not configured"})
		}
	}
}

func HandleGraph(queries WorkItemQueries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !serveGraph(w, r, queries) {
			handler.WriteJSON(w, http.StatusServiceUnavailable, GraphResponse{Success: false, Error: "Work Items service not configured"})
		}
	}
}

// serveBlocked materializes the owner projection. It returns false only when
// the Work Items query capability is not composed.
func serveBlocked(w http.ResponseWriter, r *http.Request, queries WorkItemQueries) bool {
	if queries == nil {
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
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	issues, err := queries.Blocked(ctx, workitems.AvailabilityQuery{
		ParentID: args.ParentID, Assignee: args.Assignee, Priority: args.Priority,
		IssueType: args.Type, Limit: args.Limit,
	})
	if err != nil {
		slog.Error("Work Items query error in HandleBlocked", "err", err)
		handler.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   "failed to list blocked issues",
		})
		return true
	}
	if issues == nil {
		issues = []workitems.IssueSummary{}
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    issues,
	})
	return true
}

// serveGraph materializes a GraphResponse from Work Items owner projections.
// It returns false only when the query capability is not composed.
//
//nolint:funlen // Handler translates Work Items projections into the established API shape.
func serveGraph(w http.ResponseWriter, r *http.Request, queries WorkItemQueries) bool {
	if queries == nil {
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

	result, err := queries.List(ctx, workitems.ListQuery{})
	if err != nil {
		slog.Error("Work Items query error in HandleGraph", "err", err)
		handler.WriteJSON(w, http.StatusInternalServerError, GraphResponse{
			Success: false,
			Error:   "failed to list issues",
		})
		return true
	}

	list := []workitems.ListItem{}
	if result != nil {
		list = result.Issues
	}
	graphIssues := make([]*GraphIssue, 0, len(list))
	for _, item := range list {
		d := item.IssueSummary
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
		// Fetch detail to pull dependencies from the Work Items owner.
		// If Get fails, leave
		// Dependencies empty so the node still appears without edges.
		if detail, detErr := queries.Get(ctx, workitems.GetQuery{IssueID: d.ID}); detErr == nil && detail != nil {
			deps := make([]*GraphDependency, 0, len(detail.Dependencies))
			for _, dep := range detail.Dependencies {
				deps = append(deps, &GraphDependency{
					DependsOnID: dep.ID,
					Type:        dep.DependencyType,
				})
			}
			gi.Dependencies = deps
		}
		// Synthesize a parent-child edge when the owner projection reports a parent but
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
// Mirrors the established exclude-set logic so the owner projection
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
