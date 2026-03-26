// Package issuetabs provides Redis-backed persistence for issue-scoped tab state.
//
// Each issue gets a Redis key at "ws:{workspaceID}:issue:tabs:{issueId}" storing
// a JSON-encoded IssueTabState blob. The entire tab array is always read/written
// atomically. Keys expire after 24 hours (refreshed on every write).
package issuetabs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"
)

// validIssueID matches issue IDs like "loomcli-fghge.1" — alphanumeric, hyphens, underscores, dots.
var validIssueID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

const (
	keyPrefix = "issue:tabs:"
	ttl       = 24 * time.Hour
)

// IssueTab represents a single tab within an issue's detail view.
type IssueTab struct {
	ID          string `json:"id"`   // "details", "logs", "terminal-{session}"
	Type        string `json:"type"` // "details", "logs", "terminal"
	Label       string `json:"label"`
	SessionName string `json:"session_name,omitempty"` // for terminal tabs only
	SortOrder   int    `json:"sort_order"`
}

// IssueTabState represents the full persisted tab state for an issue.
type IssueTabState struct {
	IssueID     string     `json:"issue_id"`
	Tabs        []IssueTab `json:"tabs"`
	ActiveTabID string     `json:"active_tab_id"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Store provides Redis-backed persistence for issue tab state.
type Store struct {
	client      *redis.Client
	workspaceID string
	logger      *slog.Logger
}

// NewStore creates a new issue tab state store scoped to a workspace.
// workspaceID must be non-empty (programming error if empty).
func NewStore(client *redis.Client, workspaceID string, logger *slog.Logger) *Store {
	if workspaceID == "" {
		panic("issuetabs.NewStore: workspaceID must not be empty")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{client: client, workspaceID: workspaceID, logger: logger}
}

// Close closes the underlying Redis client.
func (s *Store) Close() error {
	return s.client.Close()
}

func (s *Store) issueKey(issueID string) string {
	return "ws:" + s.workspaceID + ":" + keyPrefix + issueID
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

// Get retrieves the tab state for an issue. Returns nil if no saved state.
func (s *Store) Get(ctx context.Context, issueID string) (*IssueTabState, error) {
	if err := ValidateIssueID(issueID); err != nil {
		return nil, err
	}

	data, err := s.client.Get(ctx, s.issueKey(issueID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("get %s: %w", issueID, err)
	}

	var state IssueTabState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", issueID, err)
	}

	return &state, nil
}

// Save writes the full tab state for an issue with a 24-hour TTL.
func (s *Store) Save(ctx context.Context, state *IssueTabState) error {
	if err := ValidateIssueID(state.IssueID); err != nil {
		return err
	}

	state.UpdatedAt = time.Now().UTC()

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", state.IssueID, err)
	}

	if err := s.client.Set(ctx, s.issueKey(state.IssueID), data, ttl).Err(); err != nil {
		return fmt.Errorf("set %s: %w", state.IssueID, err)
	}

	return nil
}

// Delete removes the tab state for an issue.
func (s *Store) Delete(ctx context.Context, issueID string) error {
	if err := ValidateIssueID(issueID); err != nil {
		return err
	}

	if err := s.client.Del(ctx, s.issueKey(issueID)).Err(); err != nil {
		return fmt.Errorf("del %s: %w", issueID, err)
	}

	return nil
}

// ValidateAndFilter removes terminal tabs whose sessions no longer exist.
// Non-terminal tabs (details, logs) are always preserved.
// Returns the filtered state (may be the same pointer if nothing changed).
func ValidateAndFilter(state *IssueTabState, activeSessions []string) *IssueTabState {
	if state == nil {
		return nil
	}

	activeSet := make(map[string]bool, len(activeSessions))
	for _, s := range activeSessions {
		activeSet[s] = true
	}

	var filtered []IssueTab
	for _, tab := range state.Tabs {
		if tab.Type == "terminal" {
			if tab.SessionName == "" || !activeSet[tab.SessionName] {
				continue
			}
		}
		filtered = append(filtered, tab)
	}

	// If active tab was removed, fall back to "details"
	activeTabExists := false
	for _, tab := range filtered {
		if tab.ID == state.ActiveTabID {
			activeTabExists = true
			break
		}
	}

	result := &IssueTabState{
		IssueID:     state.IssueID,
		Tabs:        filtered,
		ActiveTabID: state.ActiveTabID,
		UpdatedAt:   state.UpdatedAt,
	}

	if !activeTabExists {
		result.ActiveTabID = "details"
	}

	return result
}
