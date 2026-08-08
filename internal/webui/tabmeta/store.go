// Package tabmeta provides Redis-backed persistence for terminal tab metadata.
//
// Each tmux session gets a Redis hash at key "terminal:meta:{workspace}:{session_name}"
// storing label, notes, sort_order, created_at, and updated_at fields.
// Metadata is scoped per workspace — each workspace has its own independent tab set.
// Metadata survives session death — tabs remember their custom labels/notes
// even after tmux sessions are destroyed.
package tabmeta

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

const keyPrefix = "terminal:meta:"

// validSessionName matches alphanumeric characters, hyphens, and underscores.
var validSessionName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// TabMetadata represents persisted metadata for a single terminal tab.
//
// PTYAlive and AttachedClients are NOT persisted to Redis — they are
// populated by the service layer at read time from the in-process
// PTYManager. PTYAlive=false means the tab survived a server restart but
// its backing shell did not. AttachedClients>1 means the same session is
// being viewed by multiple WebSocket clients concurrently.
type TabMetadata struct {
	SessionName                  string      `json:"session_name"`
	Workspace                    string      `json:"workspace,omitempty"`
	Label                        string      `json:"label"`
	Notes                        string      `json:"notes"`
	SortOrder                    int         `json:"sort_order"`
	Pinned                       bool        `json:"pinned"`
	IssueID                      string      `json:"issue_id,omitempty"`
	Kind                         string      `json:"kind,omitempty"`
	AgentID                      string      `json:"agent_id,omitempty"`
	Role                         string      `json:"role,omitempty"`
	Backend                      string      `json:"backend,omitempty"`
	InteractionSessionID         string      `json:"interaction_session_id,omitempty"`
	InteractionTerminalID        string      `json:"interaction_terminal_id,omitempty"`
	InteractionLeaseID           string      `json:"interaction_lease_id,omitempty"`
	InteractionLeaseFencingToken int64       `json:"interaction_lease_fencing_token,omitempty"`
	Writable                     bool        `json:"writable,omitempty"`
	Launch                       *LaunchSpec `json:"launch,omitempty"`
	CreatedAt                    time.Time   `json:"created_at"`
	UpdatedAt                    time.Time   `json:"updated_at"`
	PTYAlive                     bool        `json:"pty_alive"`
	AttachedClients              int         `json:"attached_clients"`
}

// LaunchSpec is the explicit command contract for a terminal session. Agent
// tabs persist this instead of deriving behavior from the human-facing tab
// name.
type LaunchSpec struct {
	Argv []string          `json:"argv,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	Cwd  string            `json:"cwd,omitempty"`
}

// Store provides Redis-backed persistence for terminal tab metadata.
type Store struct {
	client *redis.Client
	logger *slog.Logger
}

// NewStore creates a new tab metadata store.
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

// RedisClient returns the underlying Redis client for direct operations.
func (s *Store) RedisClient() *redis.Client {
	return s.client
}

func metaKey(workspace, session string) string {
	return keyPrefix + workspace + ":" + session
}

// ValidateSessionName returns an error if the session name is invalid.
func ValidateSessionName(name string) error {
	if name == "" {
		return fmt.Errorf("session name is required")
	}
	if !validSessionName.MatchString(name) {
		return fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}
	return nil
}

// ValidateWorkspaceName returns an error if the workspace name is invalid.
func ValidateWorkspaceName(name string) error {
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
func (s *Store) Get(ctx context.Context, workspace, sessionName string) (*TabMetadata, error) {
	if err := ValidateWorkspaceName(workspace); err != nil {
		return nil, err
	}
	if err := ValidateSessionName(sessionName); err != nil {
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
func (s *Store) List(ctx context.Context, workspace string) ([]TabMetadata, error) {
	if err := ValidateWorkspaceName(workspace); err != nil {
		return nil, err
	}

	var result []TabMetadata
	var cursor uint64
	prefix := keyPrefix + workspace + ":"

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
func (s *Store) ListAll(ctx context.Context) ([]TabMetadata, error) {
	var result []TabMetadata
	var cursor uint64

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, keyPrefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		for _, key := range keys {
			remainder := key[len(keyPrefix):]
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
func (s *Store) Set(ctx context.Context, meta *TabMetadata) error {
	if err := ValidateWorkspaceName(meta.Workspace); err != nil {
		return err
	}
	if err := ValidateSessionName(meta.SessionName); err != nil {
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
func (s *Store) Patch(ctx context.Context, workspace, sessionName string, fields map[string]string) (*TabMetadata, error) {
	if err := ValidateWorkspaceName(workspace); err != nil {
		return nil, err
	}
	if err := ValidateSessionName(sessionName); err != nil {
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
func (s *Store) Delete(ctx context.Context, workspace, sessionName string) error {
	if err := ValidateWorkspaceName(workspace); err != nil {
		return err
	}
	if err := ValidateSessionName(sessionName); err != nil {
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
func (s *Store) EnsureDefaults(ctx context.Context, workspace string, activeSessions []string) ([]TabMetadata, error) {
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
		meta := &TabMetadata{
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
func (s *Store) ListByIssue(ctx context.Context, issueID string) ([]TabMetadata, error) {
	all, err := s.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	var result []TabMetadata
	for _, m := range all {
		if m.IssueID == issueID {
			result = append(result, m)
		}
	}
	return result, nil
}

// ListIssueSessionMap returns a map of issue_id → []session_name for all sessions
// that have an issue_id set (across all workspaces).
func (s *Store) ListIssueSessionMap(ctx context.Context) (map[string][]string, error) {
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
func parseMetadata(workspace, sessionName string, vals map[string]string) (*TabMetadata, error) {
	meta := &TabMetadata{
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
		var spec LaunchSpec
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			return nil, fmt.Errorf("parse launch spec for %s: %w", sessionName, err)
		}
		meta.Launch = &spec
	}

	parseMetadataTimestamps(meta, vals)
	return meta, nil
}

func parseMetadataTimestamps(meta *TabMetadata, vals map[string]string) {
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
