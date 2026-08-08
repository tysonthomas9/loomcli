// Package localredis provides Redis-backed WebUI persistence adapters.
//
// Each issue gets a Redis key at "ws:{workspaceID}:issue:tabs:{issueId}" storing
// a JSON-encoded IssueTabState blob. The entire tab array is always read/written
// atomically. Keys expire after 24 hours (refreshed on every write).
package localredis

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
	issueTabKeyPrefix = "issue:tabs:"
	issueTabTTL       = 24 * time.Hour
)

// IssueTabStore provides Redis-backed persistence for issue tab state.
// Workspace identity is passed per-operation (matching the tabmeta pattern),
// not embedded in the struct. One IssueTabStore instance serves all workspaces.
type IssueTabStore struct {
	client *redis.Client
	logger *slog.Logger
}

var _ interaction.IssueTabStateAPI = (*IssueTabStore)(nil)

// NewIssueTabStore creates a new issue tab state store.
func NewIssueTabStore(client *redis.Client, logger *slog.Logger) *IssueTabStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &IssueTabStore{client: client, logger: logger}
}

// Close closes the underlying Redis client.
func (s *IssueTabStore) Close() error {
	return s.client.Close()
}

func issueTabKey(workspaceID, issueID string) string {
	return "ws:" + workspaceID + ":" + issueTabKeyPrefix + issueID
}

// Get retrieves the tab state for an issue. Returns nil if no saved state.
func (s *IssueTabStore) Get(ctx context.Context, workspaceID, issueID string) (*interaction.IssueTabState, error) {
	if err := interaction.ValidateIssueTabIssueID(issueID); err != nil {
		return nil, err
	}

	data, err := s.client.Get(ctx, issueTabKey(workspaceID, issueID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("get %s: %w", issueID, err)
	}

	var state interaction.IssueTabState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", issueID, err)
	}

	return &state, nil
}

func (s *IssueTabStore) GetIssueTabs(ctx context.Context, workspaceID, issueID string) (*interaction.IssueTabState, error) {
	return s.Get(ctx, workspaceID, issueID)
}

// ReplaceIssueTabs writes the full tab state for an issue with a 24-hour TTL.
func (s *IssueTabStore) ReplaceIssueTabs(ctx context.Context, workspaceID string, state *interaction.IssueTabState) error {
	if err := interaction.ValidateIssueTabIssueID(state.IssueID); err != nil {
		return err
	}

	state.UpdatedAt = time.Now().UTC()

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", state.IssueID, err)
	}

	if err := s.client.Set(ctx, issueTabKey(workspaceID, state.IssueID), data, issueTabTTL).Err(); err != nil {
		return fmt.Errorf("set %s: %w", state.IssueID, err)
	}

	return nil
}

// ClearIssueTabs removes the tab state for an issue.
func (s *IssueTabStore) ClearIssueTabs(ctx context.Context, workspaceID, issueID string) error {
	if err := interaction.ValidateIssueTabIssueID(issueID); err != nil {
		return err
	}

	if err := s.client.Del(ctx, issueTabKey(workspaceID, issueID)).Err(); err != nil {
		return fmt.Errorf("del %s: %w", issueID, err)
	}

	return nil
}
