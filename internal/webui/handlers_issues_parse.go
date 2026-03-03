package webui

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// parseListParams extracts ListArgs from HTTP query parameters.
func parseListParams(r *http.Request) (*rpc.ListArgs, error) {
	query := r.URL.Query()
	args := &rpc.ListArgs{}

	// Basic filters
	if v := query.Get("status"); v != "" {
		args.Status = v
	}
	if v := query.Get("type"); v != "" {
		args.IssueType = v
	}
	if v := query.Get("assignee"); v != "" {
		args.Assignee = v
	}
	if v := query.Get("q"); v != "" {
		args.Query = v
	}

	// Priority (integer)
	if v := query.Get("priority"); v != "" {
		if priority, err := strconv.Atoi(v); err == nil {
			args.Priority = &priority
		}
	}

	// Labels (comma-separated)
	if v := query.Get("labels"); v != "" {
		args.Labels = splitAndTrim(v)
	}

	// Limit (capped at MaxListLimit to prevent DoS)
	if v := query.Get("limit"); v != "" {
		if limit, err := strconv.Atoi(v); err == nil && limit > 0 {
			if limit > MaxListLimit {
				limit = MaxListLimit
			}
			args.Limit = limit
		}
	}

	// Pattern matching
	if v := query.Get("title_contains"); v != "" {
		args.TitleContains = v
	}
	if v := query.Get("description_contains"); v != "" {
		args.DescriptionContains = v
	}
	if v := query.Get("notes_contains"); v != "" {
		args.NotesContains = v
	}

	// Date ranges (validated as RFC3339 or date-only)
	dateParams := []struct {
		param string
		dest  *string
	}{
		{"created_after", &args.CreatedAfter},
		{"created_before", &args.CreatedBefore},
		{"updated_after", &args.UpdatedAfter},
		{"updated_before", &args.UpdatedBefore},
	}
	for _, dp := range dateParams {
		if v := query.Get(dp.param); v != "" {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				if _, err2 := time.Parse("2006-01-02", v); err2 != nil {
					return nil, fmt.Errorf("invalid %s: expected RFC3339 format (e.g., 2024-01-15T00:00:00Z) or date (2024-01-15)", dp.param)
				}
			}
			*dp.dest = v
		}
	}

	// Empty/null checks
	if v := query.Get("empty_description"); v == "true" {
		args.EmptyDescription = true
	}
	if v := query.Get("no_assignee"); v == "true" {
		args.NoAssignee = true
	}
	if v := query.Get("no_labels"); v == "true" {
		args.NoLabels = true
	}

	// Pinned filtering
	if v := query.Get("pinned"); v != "" {
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

// fetchUnclosedIDSetAndMap fetches all issues via client.List and returns:
//   - unclosedIDs: set of issue IDs with status != closed
//   - issueMap: lookup map for populating blocker details (title, priority)
//
// Returns nil, nil on error (non-fatal — caller falls back to daemon data).
func fetchUnclosedIDSetAndMap(client *rpc.Client) (map[string]bool, map[string]*types.IssueWithCounts) {
	resp, err := client.List(&rpc.ListArgs{Limit: MaxListLimit})
	if err != nil {
		log.Printf("Failed to fetch issues for blocker detection: %v", err)
		return nil, nil
	}
	if !resp.Success {
		log.Printf("List RPC failed for blocker detection: %s", resp.Error)
		return nil, nil
	}

	var allIssues []*types.IssueWithCounts
	if err := json.Unmarshal(resp.Data, &allIssues); err != nil {
		log.Printf("Failed to parse issues for blocker detection: %v", err)
		return nil, nil
	}

	unclosedIDs := make(map[string]bool, len(allIssues))
	issueMap := make(map[string]*types.IssueWithCounts, len(allIssues))
	for _, iwc := range allIssues {
		issueMap[iwc.Issue.ID] = iwc
		if iwc.Issue.Status != types.StatusClosed {
			unclosedIDs[iwc.Issue.ID] = true
		}
	}
	return unclosedIDs, issueMap
}

// getUnclosedBlockerRefs returns BlockerRef entries for each "blocks" dependency
// that points to an unclosed issue. Populates title/priority from issueMap.
func getUnclosedBlockerRefs(deps []*types.Dependency, unclosedIDs map[string]bool, issueMap map[string]*types.IssueWithCounts) []types.BlockerRef {
	var refs []types.BlockerRef
	for _, dep := range deps {
		if dep.Type == types.DepBlocks && unclosedIDs[dep.DependsOnID] {
			ref := types.BlockerRef{ID: dep.DependsOnID}
			if blocker, ok := issueMap[dep.DependsOnID]; ok {
				ref.Title = blocker.Issue.Title
				ref.Priority = blocker.Issue.Priority
			}
			refs = append(refs, ref)
		}
	}
	return refs
}

// extractBlockerIDs extracts issue IDs from a slice of BlockerRefs.
func extractBlockerIDs(refs []types.BlockerRef) []string {
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.ID
	}
	return ids
}
