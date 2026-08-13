package interaction

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

var validSessionHistoryIssueID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// SessionHistoryRecord is one terminal session associated with a work item.
type SessionHistoryRecord struct {
	ID          string     `json:"id"`
	SessionName string     `json:"session_name"`
	IssueID     string     `json:"issue_id"`
	Backend     string     `json:"backend"`
	Status      string     `json:"status"`
	Launcher    string     `json:"launcher"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
}

// SessionHistoryStore is Interaction's outbound persistence port for the
// issue-scoped terminal audit projection.
type SessionHistoryStore interface {
	List(context.Context, string, string) ([]SessionHistoryRecord, error)
	Add(context.Context, string, SessionHistoryRecord) error
	Complete(context.Context, string, string, string) error
}

// ValidateSessionHistoryIssueID validates an issue identifier used by the
// session-history projection.
func ValidateSessionHistoryIssueID(id string) error {
	if id == "" {
		return fmt.Errorf("issue ID is required")
	}
	if !validSessionHistoryIssueID.MatchString(id) {
		return fmt.Errorf("invalid issue ID %q: must match [a-zA-Z0-9._-]+", id)
	}
	return nil
}
