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

// ValidateAndFilterIssueTabs removes terminal tabs whose sessions no longer
// exist. Non-terminal tabs are always preserved. If the active tab is
// removed, the projection falls back to the permanent details tab.
func ValidateAndFilterIssueTabs(state *IssueTabState, activeSessions []string) *IssueTabState {
	if state == nil {
		return nil
	}

	activeSet := make(map[string]bool, len(activeSessions))
	for _, session := range activeSessions {
		activeSet[session] = true
	}

	filtered := make([]IssueTab, 0, len(state.Tabs))
	for _, tab := range state.Tabs {
		if tab.Type == "terminal" && (tab.SessionName == "" || !activeSet[tab.SessionName]) {
			continue
		}
		filtered = append(filtered, tab)
	}

	activeTabID := state.ActiveTabID
	activeTabExists := false
	for _, tab := range filtered {
		if tab.ID == activeTabID {
			activeTabExists = true
			break
		}
	}
	if !activeTabExists {
		activeTabID = "details"
	}

	return &IssueTabState{
		IssueID:     state.IssueID,
		Tabs:        filtered,
		ActiveTabID: activeTabID,
		UpdatedAt:   state.UpdatedAt,
	}
}
