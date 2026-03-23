// Package tabmeta provides Redis-backed persistence for terminal tab metadata.
//
// Each tmux session gets a Redis hash at key "terminal:meta:{session_name}"
// storing label, notes, sort_order, created_at, and updated_at fields.
// Metadata survives session death — tabs remember their custom labels/notes
// even after tmux sessions are destroyed.
package tabmeta

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const keyPrefix = "terminal:meta:"

// validSessionName matches alphanumeric characters, hyphens, and underscores.
var validSessionName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// TabMetadata represents persisted metadata for a single terminal tab.
type TabMetadata struct {
	SessionName string    `json:"session_name"`
	Workspace   string    `json:"workspace,omitempty"`
	Label       string    `json:"label"`
	Notes       string    `json:"notes"`
	IssueID     string    `json:"issue_id,omitempty"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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

func metaKey(session string) string {
	return keyPrefix + session
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

// Get retrieves metadata for a single session. Returns nil if not found.
func (s *Store) Get(ctx context.Context, sessionName string) (*TabMetadata, error) {
	if err := ValidateSessionName(sessionName); err != nil {
		return nil, err
	}

	vals, err := s.client.HGetAll(ctx, metaKey(sessionName)).Result()
	if err != nil {
		return nil, fmt.Errorf("hgetall %s: %w", sessionName, err)
	}
	if len(vals) == 0 {
		return nil, nil
	}

	return parseMetadata(sessionName, vals)
}

// GetInWorkspace retrieves metadata for a session within a specific workspace.
// The workspace is used to construct a workspace-scoped key. Returns nil if not found.
func (s *Store) GetInWorkspace(ctx context.Context, workspace, sessionName string) (*TabMetadata, error) {
	if err := ValidateSessionName(sessionName); err != nil {
		return nil, err
	}

	key := metaKey(sessionName)
	if workspace != "" {
		key = keyPrefix + workspace + ":" + sessionName
	}

	vals, err := s.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("hgetall %s: %w", key, err)
	}
	if len(vals) == 0 {
		return nil, nil
	}

	return parseMetadata(sessionName, vals)
}

// List retrieves metadata for all stored sessions.
func (s *Store) List(ctx context.Context) ([]TabMetadata, error) {
	var result []TabMetadata
	var cursor uint64

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, keyPrefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		for _, key := range keys {
			sessionName := key[len(keyPrefix):]
			vals, err := s.client.HGetAll(ctx, key).Result()
			if err != nil {
				s.logger.Warn("failed to get tab metadata", "key", key, "error", err)
				continue
			}
			if len(vals) == 0 {
				continue
			}
			meta, err := parseMetadata(sessionName, vals)
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
		return result[i].SortOrder < result[j].SortOrder
	})

	return result, nil
}

// Set writes full metadata for a session.
func (s *Store) Set(ctx context.Context, meta *TabMetadata) error {
	if err := ValidateSessionName(meta.SessionName); err != nil {
		return err
	}

	fields := map[string]interface{}{
		"label":      meta.Label,
		"notes":      meta.Notes,
		"sort_order": strconv.Itoa(meta.SortOrder),
		"created_at": meta.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": meta.UpdatedAt.UTC().Format(time.RFC3339),
	}

	if err := s.client.HSet(ctx, metaKey(meta.SessionName), fields).Err(); err != nil {
		return fmt.Errorf("hset %s: %w", meta.SessionName, err)
	}
	return nil
}

// Patch partially updates metadata fields for a session.
// Only non-empty fields in the map are updated. Returns the full metadata after update.
func (s *Store) Patch(ctx context.Context, sessionName string, fields map[string]string) (*TabMetadata, error) {
	if err := ValidateSessionName(sessionName); err != nil {
		return nil, err
	}

	key := metaKey(sessionName)

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

	return s.Get(ctx, sessionName)
}

// Delete removes metadata for a session.
func (s *Store) Delete(ctx context.Context, sessionName string) error {
	if err := ValidateSessionName(sessionName); err != nil {
		return err
	}

	if err := s.client.Del(ctx, metaKey(sessionName)).Err(); err != nil {
		return fmt.Errorf("del %s: %w", sessionName, err)
	}
	return nil
}

// EnsureDefaults cross-references active sessions with stored metadata and creates
// default records for any sessions that don't have metadata yet.
// Returns the complete sorted list of metadata for all sessions (active + stored).
func (s *Store) EnsureDefaults(ctx context.Context, activeSessions []string) ([]TabMetadata, error) {
	existing, err := s.List(ctx)
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
		return existing[i].SortOrder < existing[j].SortOrder
	})

	return existing, nil
}

// parseMetadata converts a Redis hash map to a TabMetadata struct.
func parseMetadata(sessionName string, vals map[string]string) (*TabMetadata, error) {
	meta := &TabMetadata{
		SessionName: sessionName,
		Workspace:   vals["workspace"],
		Label:       vals["label"],
		Notes:       vals["notes"],
		IssueID:     vals["issue_id"],
	}

	if so, ok := vals["sort_order"]; ok {
		n, err := strconv.Atoi(so)
		if err == nil {
			meta.SortOrder = n
		}
	}

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

	return meta, nil
}
