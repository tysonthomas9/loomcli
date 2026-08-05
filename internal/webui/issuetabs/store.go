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
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

const (
	keyPrefix = "issue:tabs:"
	ttl       = 24 * time.Hour
)

// IssueTab keeps existing WebUI callers source-compatible while Interaction
// owns the canonical tab vocabulary.
type IssueTab = interaction.IssueTab

// IssueTabState keeps existing WebUI callers source-compatible while
// Interaction owns the canonical tab-state vocabulary.
type IssueTabState = interaction.IssueTabState

// Store provides Redis-backed persistence for issue tab state.
// Workspace identity is passed per-operation (matching the tabmeta pattern),
// not embedded in the struct. One Store instance serves all workspaces.
type Store struct {
	client *redis.Client
	logger *slog.Logger
}

var _ interaction.IssueTabStateAPI = (*Store)(nil)

// NewStore creates a new issue tab state store.
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
	return interaction.ValidateIssueTabIssueID(id)
}

// Get retrieves the tab state for an issue. Returns nil if no saved state.
func (s *Store) Get(ctx context.Context, workspaceID, issueID string) (*IssueTabState, error) {
	if err := ValidateIssueID(issueID); err != nil {
		return nil, err
	}

	data, err := s.client.Get(ctx, issueKey(workspaceID, issueID)).Bytes()
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

func (s *Store) GetIssueTabs(ctx context.Context, workspaceID, issueID string) (*interaction.IssueTabState, error) {
	return s.Get(ctx, workspaceID, issueID)
}

// ReplaceIssueTabs writes the full tab state for an issue with a 24-hour TTL.
func (s *Store) ReplaceIssueTabs(ctx context.Context, workspaceID string, state *interaction.IssueTabState) error {
	if err := ValidateIssueID(state.IssueID); err != nil {
		return err
	}

	state.UpdatedAt = time.Now().UTC()

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", state.IssueID, err)
	}

	if err := s.client.Set(ctx, issueKey(workspaceID, state.IssueID), data, ttl).Err(); err != nil {
		return fmt.Errorf("set %s: %w", state.IssueID, err)
	}

	return nil
}

// ClearIssueTabs removes the tab state for an issue.
func (s *Store) ClearIssueTabs(ctx context.Context, workspaceID, issueID string) error {
	if err := ValidateIssueID(issueID); err != nil {
		return err
	}

	if err := s.client.Del(ctx, issueKey(workspaceID, issueID)).Err(); err != nil {
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
