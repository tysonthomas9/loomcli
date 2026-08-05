package service

import (
	"fmt"
	"strings"
	"time"

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

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
