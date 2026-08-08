package interaction

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

var validIssueTabIssueID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// IssueTab is Interaction's canonical issue-scoped UI tab projection.
type IssueTab struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Label       string `json:"label"`
	SessionName string `json:"session_name,omitempty"`
	Backend     string `json:"backend,omitempty"`
	SortOrder   int    `json:"sort_order"`
}

// IssueTabState is the complete replace-on-write tab projection for one issue.
type IssueTabState struct {
	IssueID     string     `json:"issue_id"`
	Tabs        []IssueTab `json:"tabs"`
	ActiveTabID string     `json:"active_tab_id"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// IssueTabStateAPI is the narrow Interaction-owned boundary used by the UI.
// Callers can replace or clear one complete issue-scoped projection; they do
// not receive the Redis client or its generic key/value surface.
type IssueTabStateAPI interface {
	GetIssueTabs(context.Context, string, string) (*IssueTabState, error)
	ReplaceIssueTabs(context.Context, string, *IssueTabState) error
	ClearIssueTabs(context.Context, string, string) error
}

// ValidateIssueTabIssueID validates the transport-independent issue identity
// used by the Interaction tab projection.
func ValidateIssueTabIssueID(id string) error {
	if id == "" {
		return fmt.Errorf("issue ID is required")
	}
	if !validIssueTabIssueID.MatchString(id) {
		return fmt.Errorf("invalid issue ID %q: must match [a-zA-Z0-9._-]+", id)
	}
	return nil
}
