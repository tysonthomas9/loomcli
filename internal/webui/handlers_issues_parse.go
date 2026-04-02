package webui

import (
	"fmt"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// parseListParams extracts ListArgs from HTTP query parameters.
func parseListParams(r *http.Request) (*rpc.ListArgs, error) {
	q := r.URL.Query()
	args := &rpc.ListArgs{}

	// Basic string filters
	args.Status = parseStringParam(q, "status")
	args.IssueType = parseStringParam(q, "type")
	args.Assignee = parseStringParam(q, "assignee")
	args.Query = parseStringParam(q, "q")

	// Priority (integer, silently ignore invalid values)
	args.Priority, _ = parseIntParam(q, "priority")

	// Labels (comma-separated)
	args.Labels = parseArrayParam(q, "labels")
	args.SourceRepos = parseArrayParam(q, "source_repos")

	// Limit (capped at MaxListLimit to prevent DoS, silently ignore invalid)
	limitPtr, _ := parseIntParam(q, "limit")
	if limitPtr != nil && *limitPtr > 0 {
		limit := *limitPtr
		if limit > MaxListLimit {
			limit = MaxListLimit
		}
		args.Limit = limit
	}

	// Pattern matching
	args.TitleContains = parseStringParam(q, "title_contains")
	args.DescriptionContains = parseStringParam(q, "description_contains")
	args.NotesContains = parseStringParam(q, "notes_contains")

	// Date ranges (validated as RFC3339 or date-only)
	dateKeys := []string{"created_after", "created_before", "updated_after", "updated_before"}
	dates, err := parseDateParams(q, dateKeys)
	if err != nil {
		return nil, err
	}
	args.CreatedAfter = dates["created_after"]
	args.CreatedBefore = dates["created_before"]
	args.UpdatedAfter = dates["updated_after"]
	args.UpdatedBefore = dates["updated_before"]

	// Empty/null checks
	if parseStringParam(q, "empty_description") == "true" {
		args.EmptyDescription = true
	}
	if parseStringParam(q, "no_assignee") == "true" {
		args.NoAssignee = true
	}
	if parseStringParam(q, "no_labels") == "true" {
		args.NoLabels = true
	}

	// Pinned filtering
	if v := parseStringParam(q, "pinned"); v != "" {
		pinned := v == "true"
		args.Pinned = &pinned
	}

	return args, nil
}

// kanbanParams holds the additional query parameters for Kanban-enriched responses.
type kanbanParams struct {
	ExcludeStatus  []string // Statuses to exclude from results
	IncludeBlocked bool     // Whether to include blocked dependency info
}

// MaxExcludeStatuses caps the number of exclude_status values to prevent abuse.
const MaxExcludeStatuses = 1000

func parseKanbanParams(r *http.Request) (*kanbanParams, error) {
	params := &kanbanParams{}
	q := r.URL.Query()

	if v := q.Get("exclude_status"); v != "" {
		statuses := splitAndTrim(v)
		if len(statuses) > MaxExcludeStatuses {
			return nil, fmt.Errorf("too many exclude_status values (max %d)", MaxExcludeStatuses)
		}
		params.ExcludeStatus = statuses
	}

	if v := q.Get("include_blocked"); v == "true" {
		params.IncludeBlocked = true
	}

	return params, nil
}
