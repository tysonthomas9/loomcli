package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func validateCreateParams(params *CreateIssueParams) *ServiceError {
	if strings.TrimSpace(params.Title) == "" {
		return ErrValidation("title is required")
	}
	if params.IssueType == "" {
		return ErrValidation("issue_type is required")
	}
	if !validIssueTypes[params.IssueType] {
		return ErrValidation(fmt.Sprintf("invalid issue_type: %s (must be bug, feature, task, epic, or chore)", params.IssueType))
	}
	if params.Priority < 0 || params.Priority > 4 {
		return ErrValidation(fmt.Sprintf("priority must be between 0 and 4 (got %d)", params.Priority))
	}
	if len(params.Labels) > maxLabels {
		return ErrValidation(fmt.Sprintf("too many labels (max %d, got %d)", maxLabels, len(params.Labels)))
	}
	if len(params.Dependencies) > maxDependencies {
		return ErrValidation(fmt.Sprintf("too many dependencies (max %d, got %d)", maxDependencies, len(params.Dependencies)))
	}
	return nil
}

func toCreateArgs(params *CreateIssueParams) *rpc.CreateArgs {
	return &rpc.CreateArgs{
		ID:                 params.ID,
		Parent:             params.Parent,
		Title:              params.Title,
		Description:        params.Description,
		IssueType:          params.IssueType,
		Priority:           params.Priority,
		Design:             params.Design,
		AcceptanceCriteria: params.AcceptanceCriteria,
		Notes:              params.Notes,
		Assignee:           params.Assignee,
		ExternalRef:        params.ExternalRef,
		EstimatedMinutes:   params.EstimatedMinutes,
		Labels:             params.Labels,
		Dependencies:       params.Dependencies,
		CreatedBy:          params.CreatedBy,
		Owner:              params.Owner,
		DueAt:              params.DueAt,
		DeferUntil:         params.DeferUntil,
	}
}

func patchParamsToUpdateArgs(params *PatchIssueParams) *rpc.UpdateArgs {
	return &rpc.UpdateArgs{
		ID:                 params.IssueID,
		Title:              params.Title,
		Description:        params.Description,
		Status:             params.Status,
		Priority:           params.Priority,
		Assignee:           params.Assignee,
		Owner:              params.Owner,
		Design:             params.Design,
		AcceptanceCriteria: params.AcceptanceCriteria,
		Notes:              params.Notes,
		ExternalRef:        params.ExternalRef,
		EstimatedMinutes:   params.EstimatedMinutes,
		IssueType:          params.IssueType,
		AddLabels:          params.AddLabels,
		RemoveLabels:       params.RemoveLabels,
		SetLabels:          params.SetLabels,
		Pinned:             params.Pinned,
		Parent:             params.Parent,
		DueAt:              params.DueAt,
		DeferUntil:         params.DeferUntil,
		AgentState:         params.AgentState,
	}
}

func buildMoveCreateArgs(source *types.Issue, issueID string) *rpc.CreateArgs {
	description := source.Description
	if description != "" {
		description += "\n\n"
	}
	description += fmt.Sprintf("(Moved from %s)", issueID)

	externalRef := ""
	if source.ExternalRef != nil {
		externalRef = *source.ExternalRef
	}

	return &rpc.CreateArgs{
		Title:              source.Title,
		Description:        description,
		IssueType:          string(source.IssueType),
		Priority:           source.Priority,
		Design:             source.Design,
		AcceptanceCriteria: source.AcceptanceCriteria,
		Notes:              source.Notes,
		Assignee:           source.Assignee,
		ExternalRef:        externalRef,
		EstimatedMinutes:   source.EstimatedMinutes,
		Labels:             source.Labels,
		Owner:              source.Owner,
		CreatedBy:          "web-ui",
		DueAt:              formatTimePtr(source.DueAt),
		DeferUntil:         formatTimePtr(source.DeferUntil),
	}
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// fetchUnclosedIDSetAndMap fetches all issues via client.List and returns:
//   - unclosedIDs: set of issue IDs with status != closed
//   - issueMap: lookup map for populating blocker details (title, priority)
//
// Returns nil, nil on error (non-fatal — caller falls back to daemon data).
func fetchUnclosedIDSetAndMap(client *rpc.Client) (map[string]bool, map[string]*types.IssueWithCounts) {
	resp, err := client.List(&rpc.ListArgs{Limit: maxListLimit})
	if err != nil {
		slog.Error("failed to fetch issues for blocker detection", "err", err)
		return nil, nil
	}
	if !resp.Success {
		slog.Error("list RPC failed for blocker detection", "err", resp.Error)
		return nil, nil
	}

	var allIssues []*types.IssueWithCounts
	if err := json.Unmarshal(resp.Data, &allIssues); err != nil {
		slog.Error("failed to parse issues for blocker detection", "err", err)
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

// getUnclosedBlockerRefs returns BlockerRef entries for each blocking dependency
// that points to an unclosed issue.
func getUnclosedBlockerRefs(deps []*types.Dependency, unclosedIDs map[string]bool, issueMap map[string]*types.IssueWithCounts) []types.BlockerRef {
	var refs []types.BlockerRef
	for _, dep := range deps {
		if dep.Type.IsDirectBlocker() && unclosedIDs[dep.DependsOnID] {
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
