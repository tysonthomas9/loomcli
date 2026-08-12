package service

import (
	"fmt"
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
	if params.Status != "" && params.Status != string(types.StatusOpen) && params.Status != string(types.StatusDeferred) {
		return ErrValidation("status must be open or deferred")
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
		ID:                   params.ID,
		Parent:               params.Parent,
		Title:                params.Title,
		Description:          params.Description,
		Status:               params.Status,
		IssueType:            params.IssueType,
		Priority:             params.Priority,
		Design:               params.Design,
		AcceptanceCriteria:   params.AcceptanceCriteria,
		Notes:                params.Notes,
		Assignee:             params.Assignee,
		ExternalRef:          params.ExternalRef,
		EstimatedMinutes:     params.EstimatedMinutes,
		Labels:               params.Labels,
		Dependencies:         params.Dependencies,
		CreatedBy:            params.CreatedBy,
		Owner:                params.Owner,
		DueAt:                params.DueAt,
		DeferUntil:           params.DeferUntil,
		SourceRepo:           params.SourceRepo,
		PrimaryRepository:    params.PrimaryRepository,
		SelectedRepositories: append([]string(nil), params.SelectedRepositories...),
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
