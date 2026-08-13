// Package localredis provides Redis-backed WebUI persistence adapters.
//
// Each issue gets a Redis key at "ws:{workspaceID}:issue:sessions:{issueId}" storing
// a JSON-encoded SessionHistory blob. No TTL — session history persists indefinitely.
package localredis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

const sessionHistoryKeyPrefix = "issue:sessions:"

type sessionHistory struct {
	IssueID  string                             `json:"issue_id"`
	Sessions []interaction.SessionHistoryRecord `json:"sessions"`
}

// SessionHistoryStore provides Redis-backed persistence for session history.
// Workspace identity is passed per-operation (matching the tabmeta pattern),
// not embedded in the struct. One SessionHistoryStore serves all workspaces.
type SessionHistoryStore struct {
	client *redis.Client
	logger *slog.Logger
}

var _ interaction.SessionHistoryStore = (*SessionHistoryStore)(nil)

// NewSessionHistoryStore creates a new session history store.
func NewSessionHistoryStore(client *redis.Client, logger *slog.Logger) *SessionHistoryStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionHistoryStore{client: client, logger: logger}
}

// Close closes the underlying Redis client.
func (s *SessionHistoryStore) Close() error {
	return s.client.Close()
}

func sessionHistoryIssueKey(workspaceID, issueID string) string {
	return "ws:" + workspaceID + ":" + sessionHistoryKeyPrefix + issueID
}

// Add appends a session record to the history for an issue.
func (s *SessionHistoryStore) Add(ctx context.Context, workspaceID string, record interaction.SessionHistoryRecord) error {
	if err := interaction.ValidateSessionHistoryIssueID(record.IssueID); err != nil {
		return err
	}

	key := sessionHistoryIssueKey(workspaceID, record.IssueID)
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
func (s *SessionHistoryStore) List(ctx context.Context, workspaceID, issueID string) ([]interaction.SessionHistoryRecord, error) {
	if err := interaction.ValidateSessionHistoryIssueID(issueID); err != nil {
		return nil, err
	}

	history, err := s.getHistory(ctx, sessionHistoryIssueKey(workspaceID, issueID))
	if err != nil {
		return nil, fmt.Errorf("get history for list: %w", err)
	}

	sessions := history.Sessions
	if sessions == nil {
		sessions = []interaction.SessionHistoryRecord{}
	}

	// Sort by StartedAt descending (most recent first).
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})

	return sessions, nil
}

// Complete marks an active session as completed. Durable scrollback is an
// Artifacts-owned Run Capture facet and is never represented by a local path.
func (s *SessionHistoryStore) Complete(ctx context.Context, workspaceID, issueID, sessionName string) error {
	if err := interaction.ValidateSessionHistoryIssueID(issueID); err != nil {
		return err
	}

	key := sessionHistoryIssueKey(workspaceID, issueID)
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
			found = true
			break
		}
	}

	if !found {
		return nil // No active session with that name — nothing to complete.
	}

	return s.saveHistory(ctx, key, history)
}

func (s *SessionHistoryStore) getHistory(ctx context.Context, key string) (*sessionHistory, error) {
	data, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return &sessionHistory{}, nil
		}
		return nil, err
	}

	var history sessionHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &history, nil
}

func (s *SessionHistoryStore) saveHistory(ctx context.Context, key string, history *sessionHistory) error {
	data, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	// No TTL — session history persists indefinitely.
	return s.client.Set(ctx, key, data, 0).Err()
}
