package issues

import (
	"fmt"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// parseListParams extracts a service-owned issue filter from HTTP query parameters.
func parseListParams(r *http.Request) (*issueListFilter, error) { //nolint:funlen
	q := r.URL.Query()
	args := &issueListFilter{}

	// Basic string filters
	args.Status = handler.ParseStringParam(q, "status")
	args.IssueType = handler.ParseStringParam(q, "type")
	args.Assignee = handler.ParseStringParam(q, "assignee")
	args.Query = handler.ParseStringParam(q, "q")

	// Priority (integer, silently ignore invalid values)
	args.Priority, _ = handler.ParseIntParam(q, "priority")

	// Labels (comma-separated)
	args.Labels = handler.ParseArrayParam(q, "labels")
	args.SourceRepos = handler.ParseArrayParam(q, "source_repos")

	// Limit (capped at MaxListLimit to prevent DoS, silently ignore invalid)
	limitPtr, _ := handler.ParseIntParam(q, "limit")
	if limitPtr != nil && *limitPtr > 0 {
		limit := *limitPtr
		if limit > handler.MaxListLimit {
			limit = handler.MaxListLimit
		}
		args.Limit = limit
	}

	// Pattern matching
	args.TitleContains = handler.ParseStringParam(q, "title_contains")
	args.DescriptionContains = handler.ParseStringParam(q, "description_contains")
	args.NotesContains = handler.ParseStringParam(q, "notes_contains")

	// Date ranges (validated as RFC3339 or date-only)
	dateKeys := []string{"created_after", "created_before", "updated_after", "updated_before"}
	dates, err := handler.ParseDateParams(q, dateKeys)
	if err != nil {
		return nil, err
	}
	args.CreatedAfter = dates["created_after"]
	args.CreatedBefore = dates["created_before"]
	args.UpdatedAfter = dates["updated_after"]
	args.UpdatedBefore = dates["updated_before"]

	// Empty/null checks
	if handler.ParseStringParam(q, "empty_description") == "true" {
		args.EmptyDescription = true
	}
	if handler.ParseStringParam(q, "no_assignee") == "true" {
		args.NoAssignee = true
	}
	if handler.ParseStringParam(q, "no_labels") == "true" {
		args.NoLabels = true
	}

	// Pinned filtering
	if v := handler.ParseStringParam(q, "pinned"); v != "" {
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
		statuses := handler.SplitAndTrim(v)
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
