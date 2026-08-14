// Package localredis provides Redis-backed local runtime adapters.
//
// Each tmux session gets a Redis hash at key "terminal:meta:{workspace}:{session_name}"
// storing label, notes, sort_order, created_at, and updated_at fields.
// Metadata is scoped per workspace — each workspace has its own independent tab set.
// Metadata survives session death — tabs remember their custom labels/notes
// even after tmux sessions are destroyed.
package localredis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

const tabMetadataKeyPrefix = "terminal:meta:"

// TabMetadataStore provides Redis-backed persistence for terminal tab metadata.
type TabMetadataStore struct {
	client *redis.Client
	logger *slog.Logger
}

var _ interaction.TabMetadataStore = (*TabMetadataStore)(nil)

// NewTabMetadataStore creates a new tab metadata store.
func NewTabMetadataStore(client *redis.Client, logger *slog.Logger) *TabMetadataStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &TabMetadataStore{client: client, logger: logger}
}

// Close closes the underlying Redis client.
func (s *TabMetadataStore) Close() error {
	return s.client.Close()
}

// RedisClient returns the underlying Redis client for direct operations.
func (s *TabMetadataStore) RedisClient() *redis.Client {
	return s.client
}

func metaKey(workspace, session string) string {
	return tabMetadataKeyPrefix + workspace + ":" + session
}

func validateTabMetadataWorkspaceName(name string) error {
	if err := workspacemodule.ValidateName(name); err != nil {
		kind, ok := workspacemodule.NameValidationKindOf(err)
		if !ok {
			return err
		}
		switch kind {
		case workspacemodule.NameRequired:
			return fmt.Errorf("workspace name is required")
		case workspacemodule.NameTooLong:
			return fmt.Errorf("workspace name is too long (max %d characters)", workspacemodule.MaxNameLength)
		case workspacemodule.NameInvalidCharacters:
			return fmt.Errorf("invalid workspace name %q: must match [a-zA-Z0-9_-]+", name)
		default:
			return err
		}
	}
	return nil
}

// Get retrieves metadata for a single session within a workspace. Returns nil if not found.
func (s *TabMetadataStore) Get(ctx context.Context, workspace, sessionName string) (*interaction.TabMetadata, error) {
	if err := validateTabMetadataWorkspaceName(workspace); err != nil {
		return nil, err
	}
	if err := interaction.ValidateTerminalSessionName(sessionName); err != nil {
		return nil, err
	}

	vals, err := s.client.HGetAll(ctx, metaKey(workspace, sessionName)).Result()
	if err != nil {
		return nil, fmt.Errorf("hgetall %s: %w", sessionName, err)
	}
	if len(vals) == 0 {
		return nil, nil
	}

	return parseMetadata(workspace, sessionName, vals)
}

// List retrieves metadata for all sessions within a workspace.
func (s *TabMetadataStore) List(ctx context.Context, workspace string) ([]interaction.TabMetadata, error) {
	if err := validateTabMetadataWorkspaceName(workspace); err != nil {
		return nil, err
	}

	var result []interaction.TabMetadata
	var cursor uint64
	prefix := tabMetadataKeyPrefix + workspace + ":"

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		for _, key := range keys {
			sessionName := key[len(prefix):]
			vals, err := s.client.HGetAll(ctx, key).Result()
			if err != nil {
				s.logger.Warn("failed to get tab metadata", "key", key, "error", err)
				continue
			}
			if len(vals) == 0 {
				continue
			}
			meta, err := parseMetadata(workspace, sessionName, vals)
			if err != nil {
				s.logger.Warn("failed to parse tab metadata", "key", key, "error", err)
				continue
			}
			result = append(result, *meta)
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Pinned != result[j].Pinned {
			return result[i].Pinned
		}
		return result[i].SortOrder < result[j].SortOrder
	})

	return result, nil
}

// ListAll retrieves metadata for all sessions across all workspaces.
func (s *TabMetadataStore) ListAll(ctx context.Context) ([]interaction.TabMetadata, error) {
	var result []interaction.TabMetadata
	var cursor uint64

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, tabMetadataKeyPrefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		for _, key := range keys {
			remainder := key[len(tabMetadataKeyPrefix):]
			// Key format: {workspace}:{session}
			idx := strings.Index(remainder, ":")
			if idx < 0 {
				s.logger.Warn("skipping key with invalid format", "key", key)
				continue
			}
			ws := remainder[:idx]
			sessionName := remainder[idx+1:]

			vals, err := s.client.HGetAll(ctx, key).Result()
			if err != nil {
				s.logger.Warn("failed to get tab metadata", "key", key, "error", err)
				continue
			}
			if len(vals) == 0 {
				continue
			}
			meta, err := parseMetadata(ws, sessionName, vals)
			if err != nil {
				s.logger.Warn("failed to parse tab metadata", "key", key, "error", err)
				continue
			}
			result = append(result, *meta)
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Pinned != result[j].Pinned {
			return result[i].Pinned
		}
		return result[i].SortOrder < result[j].SortOrder
	})

	return result, nil
}

// Set writes full metadata for a session. meta.Workspace must be set.
func (s *TabMetadataStore) Set(ctx context.Context, meta *interaction.TabMetadata) error {
	if err := validateTabMetadataWorkspaceName(meta.Workspace); err != nil {
		return err
	}
	if err := interaction.ValidateTerminalSessionName(meta.SessionName); err != nil {
		return err
	}

	pinnedStr := "false"
	if meta.Pinned {
		pinnedStr = "true"
	}
	fields := map[string]interface{}{
		"label":                           meta.Label,
		"notes":                           meta.Notes,
		"sort_order":                      strconv.Itoa(meta.SortOrder),
		"pinned":                          pinnedStr,
		"issue_id":                        meta.IssueID,
		"kind":                            meta.Kind,
		"agent_id":                        meta.AgentID,
		"role":                            meta.Role,
		"backend":                         meta.Backend,
		"interaction_session_id":          meta.InteractionSessionID,
		"interaction_terminal_id":         meta.InteractionTerminalID,
		"interaction_lease_id":            meta.InteractionLeaseID,
		"interaction_lease_fencing_token": strconv.FormatInt(meta.InteractionLeaseFencingToken, 10),
		"writable":                        strconv.FormatBool(meta.Writable),
		"created_at":                      meta.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":                      meta.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if meta.Launch != nil {
		raw, err := json.Marshal(meta.Launch)
		if err != nil {
			return fmt.Errorf("marshal launch spec %s: %w", meta.SessionName, err)
		}
		fields["launch"] = string(raw)
	} else {
		fields["launch"] = ""
	}

	if err := s.client.HSet(ctx, metaKey(meta.Workspace, meta.SessionName), fields).Err(); err != nil {
		return fmt.Errorf("hset %s: %w", meta.SessionName, err)
	}
	return nil
}

// Patch partially updates metadata fields for a session within a workspace.
// Only non-empty fields in the map are updated. Returns the full metadata after update.
func (s *TabMetadataStore) Patch(ctx context.Context, workspace, sessionName string, fields map[string]string) (*interaction.TabMetadata, error) {
	if err := validateTabMetadataWorkspaceName(workspace); err != nil {
		return nil, err
	}
	if err := interaction.ValidateTerminalSessionName(sessionName); err != nil {
		return nil, err
	}

	key := metaKey(workspace, sessionName)

	// Check if the key exists
	exists, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("exists %s: %w", sessionName, err)
	}
	if exists == 0 {
		return nil, fmt.Errorf("session %q not found", sessionName)
	}

	// Add updated_at to fields
	fields["updated_at"] = time.Now().UTC().Format(time.RFC3339)

	// Convert to interface map for HSet
	ifields := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		ifields[k] = v
	}

	if err := s.client.HSet(ctx, key, ifields).Err(); err != nil {
		return nil, fmt.Errorf("hset %s: %w", sessionName, err)
	}

	return s.Get(ctx, workspace, sessionName)
}

// Delete removes metadata for a session within a workspace.
func (s *TabMetadataStore) Delete(ctx context.Context, workspace, sessionName string) error {
	if err := validateTabMetadataWorkspaceName(workspace); err != nil {
		return err
	}
	if err := interaction.ValidateTerminalSessionName(sessionName); err != nil {
		return err
	}

	if err := s.client.Del(ctx, metaKey(workspace, sessionName)).Err(); err != nil {
		return fmt.Errorf("del %s: %w", sessionName, err)
	}
	return nil
}

// EnsureDefaults cross-references active sessions with stored metadata for a workspace
// and creates default records for any sessions that don't have metadata yet.
// Returns the complete sorted list of metadata for all sessions (active + stored).
func (s *TabMetadataStore) EnsureDefaults(ctx context.Context, workspace string, activeSessions []string) ([]interaction.TabMetadata, error) {
	existing, err := s.List(ctx, workspace)
	if err != nil {
		return nil, err
	}

	// Build lookup of sessions that already have metadata
	existingSet := make(map[string]bool, len(existing))
	for _, m := range existing {
		existingSet[m.SessionName] = true
	}

	// Find the max sort_order
	maxOrder := 0
	for _, m := range existing {
		if m.SortOrder > maxOrder {
			maxOrder = m.SortOrder
		}
	}

	// Create defaults for missing sessions
	now := time.Now().UTC()
	for _, session := range activeSessions {
		if existingSet[session] {
			continue
		}
		maxOrder++
		meta := &interaction.TabMetadata{
			SessionName: session,
			Workspace:   workspace,
			Label:       session,
			Notes:       "",
			SortOrder:   maxOrder,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.Set(ctx, meta); err != nil {
			s.logger.Warn("failed to create default metadata", "session", session, "error", err)
			continue
		}
		existing = append(existing, *meta)
	}

	sort.Slice(existing, func(i, j int) bool {
		if existing[i].Pinned != existing[j].Pinned {
			return existing[i].Pinned
		}
		return existing[i].SortOrder < existing[j].SortOrder
	})

	return existing, nil
}

// ListByIssue returns all tab metadata associated with the given issue ID (across all workspaces).
func (s *TabMetadataStore) ListByIssue(ctx context.Context, issueID string) ([]interaction.TabMetadata, error) {
	all, err := s.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	var result []interaction.TabMetadata
	for _, m := range all {
		if m.IssueID == issueID {
			result = append(result, m)
		}
	}
	return result, nil
}

// ListIssueSessionMap returns a map of issue_id → []session_name for all sessions
// that have an issue_id set (across all workspaces).
func (s *TabMetadataStore) ListIssueSessionMap(ctx context.Context) (map[string][]string, error) {
	all, err := s.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string)
	for _, m := range all {
		if m.IssueID != "" {
			result[m.IssueID] = append(result[m.IssueID], m.SessionName)
		}
	}
	return result, nil
}

// parseMetadata converts a Redis hash map to a TabMetadata struct.
func parseMetadata(workspace, sessionName string, vals map[string]string) (*interaction.TabMetadata, error) {
	meta := &interaction.TabMetadata{
		SessionName:           sessionName,
		Workspace:             workspace,
		Label:                 vals["label"],
		Notes:                 vals["notes"],
		IssueID:               vals["issue_id"],
		Kind:                  vals["kind"],
		AgentID:               vals["agent_id"],
		Role:                  vals["role"],
		Backend:               vals["backend"],
		InteractionSessionID:  vals["interaction_session_id"],
		InteractionTerminalID: vals["interaction_terminal_id"],
		InteractionLeaseID:    vals["interaction_lease_id"],
	}

	if so, ok := vals["sort_order"]; ok {
		n, err := strconv.Atoi(so)
		if err == nil {
			meta.SortOrder = n
		}
	}

	if p, ok := vals["pinned"]; ok {
		meta.Pinned = p == "true"
	}
	if writable, ok := vals["writable"]; ok {
		meta.Writable = writable == "true"
	}
	if raw, ok := vals["interaction_lease_fencing_token"]; ok {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err == nil {
			meta.InteractionLeaseFencingToken = value
		}
	}
	if raw, ok := vals["launch"]; ok && strings.TrimSpace(raw) != "" {
		var spec interaction.LaunchSpec
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			return nil, fmt.Errorf("parse launch spec for %s: %w", sessionName, err)
		}
		meta.Launch = &spec
	}

	parseMetadataTimestamps(meta, vals)
	return meta, nil
}

func parseMetadataTimestamps(meta *interaction.TabMetadata, vals map[string]string) {
	if ca, ok := vals["created_at"]; ok {
		t, err := time.Parse(time.RFC3339, ca)
		if err == nil {
			meta.CreatedAt = t
		}
	}

	if ua, ok := vals["updated_at"]; ok {
		t, err := time.Parse(time.RFC3339, ua)
		if err == nil {
			meta.UpdatedAt = t
		}
	}
}
