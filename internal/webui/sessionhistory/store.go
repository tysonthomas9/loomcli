// Package sessionhistory provides Redis-backed persistence for terminal session audit records.
//
// Each issue gets a Redis key at "ws:{workspaceID}:issue:sessions:{issueId}" storing
// a JSON-encoded SessionHistory blob. No TTL — session history persists indefinitely.
package sessionhistory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

// validIssueID matches issue IDs like "loomcli-fghge.1".
var validIssueID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

const keyPrefix = "issue:sessions:"

// SessionRecord represents a single terminal session associated with an issue.
type SessionRecord struct {
	ID             string     `json:"id"`
	SessionName    string     `json:"session_name"`
	IssueID        string     `json:"issue_id"`
	Backend        string     `json:"backend"`
	Status         string     `json:"status"`   // "active" | "completed"
	Launcher       string     `json:"launcher"` // "user" | "start-work"
	StartedAt      time.Time  `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	ScrollbackPath string     `json:"scrollback_path,omitempty"`
}

// SessionHistory holds all session records for an issue.
type SessionHistory struct {
	IssueID  string          `json:"issue_id"`
	Sessions []SessionRecord `json:"sessions"`
}

// ScrollbackResult is the read projection for one completed session's
// captured terminal output. It belongs with session-history persistence rather
// than the retired global Web UI service bucket.
type ScrollbackResult struct {
	Content string
	Lines   int
}

// Store provides Redis-backed persistence for session history.
// Workspace identity is passed per-operation (matching the tabmeta pattern),
// not embedded in the struct. One Store instance serves all workspaces.
type Store struct {
	client *redis.Client
	logger *slog.Logger
}

// NewStore creates a new session history store.
func NewStore(client *redis.Client, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{client: client, logger: logger}
}

// Close closes the underlying Redis client.
func (s *Store) Close() error {
	return s.client.Close()
}

func issueKey(workspaceID, issueID string) string {
	return "ws:" + workspaceID + ":" + keyPrefix + issueID
}

// ValidateIssueID returns an error if the issue ID is invalid.
func ValidateIssueID(id string) error {
	if id == "" {
		return fmt.Errorf("issue ID is required")
	}
	if !validIssueID.MatchString(id) {
		return fmt.Errorf("invalid issue ID %q: must match [a-zA-Z0-9._-]+", id)
	}
	return nil
}

// Add appends a session record to the history for an issue.
func (s *Store) Add(ctx context.Context, workspaceID string, record SessionRecord) error {
	if err := ValidateIssueID(record.IssueID); err != nil {
		return err
	}

	key := issueKey(workspaceID, record.IssueID)
	history, err := s.getHistory(ctx, key)
	if err != nil {
		return fmt.Errorf("get history for add: %w", err)
	}

	history.IssueID = record.IssueID
	history.Sessions = append(history.Sessions, record)

	return s.saveHistory(ctx, key, history)
}

// List returns all session records for an issue, sorted by StartedAt descending.
// Returns an empty slice (not nil) for unknown issues.
func (s *Store) List(ctx context.Context, workspaceID, issueID string) ([]SessionRecord, error) {
	if err := ValidateIssueID(issueID); err != nil {
		return nil, err
	}

	history, err := s.getHistory(ctx, issueKey(workspaceID, issueID))
	if err != nil {
		return nil, fmt.Errorf("get history for list: %w", err)
	}

	sessions := history.Sessions
	if sessions == nil {
		sessions = []SessionRecord{}
	}

	// Sort by StartedAt descending (most recent first).
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})

	return sessions, nil
}

// Complete marks an active session as completed, setting EndedAt and ScrollbackPath.
func (s *Store) Complete(ctx context.Context, workspaceID, issueID, sessionName, scrollbackPath string) error {
	if err := ValidateIssueID(issueID); err != nil {
		return err
	}

	key := issueKey(workspaceID, issueID)
	history, err := s.getHistory(ctx, key)
	if err != nil {
		return fmt.Errorf("get history for complete: %w", err)
	}

	now := time.Now().UTC()
	found := false
	for i := range history.Sessions {
		if history.Sessions[i].SessionName == sessionName && history.Sessions[i].Status == "active" {
			history.Sessions[i].Status = "completed"
			history.Sessions[i].EndedAt = &now
			history.Sessions[i].ScrollbackPath = scrollbackPath
			found = true
			break
		}
	}

	if !found {
		return nil // No active session with that name — nothing to complete.
	}

	return s.saveHistory(ctx, key, history)
}

func (s *Store) getHistory(ctx context.Context, key string) (*SessionHistory, error) {
	data, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return &SessionHistory{}, nil
		}
		return nil, err
	}

	var history SessionHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &history, nil
}

func (s *Store) saveHistory(ctx context.Context, key string, history *SessionHistory) error {
	data, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	// No TTL — session history persists indefinitely.
	return s.client.Set(ctx, key, data, 0).Err()
}
