package tabmeta

import (
	"context"
	"fmt"
	"strings"
)

// MigrateLegacyKeys scans for old-format keys (terminal:meta:{session} without workspace)
// and renames them to workspace-scoped format (terminal:meta:{workspace}:{session}).
// This is idempotent — already-migrated keys are skipped.
func (s *Store) MigrateLegacyKeys(ctx context.Context, defaultWorkspace string) error {
	var cursor uint64
	var migratedCount int

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, keyPrefix+"*", 100).Result()
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		for _, key := range keys {
			remainder := key[len(keyPrefix):]
			// Legacy keys have no ":" in the remainder (just session name).
			// New keys have format {workspace}:{session}.
			if strings.Contains(remainder, ":") {
				continue // Already workspace-scoped
			}

			newKey := metaKey(defaultWorkspace, remainder)
			if err := s.client.Rename(ctx, key, newKey).Err(); err != nil {
				s.logger.Warn("failed to migrate legacy key", "key", key, "error", err)
				continue
			}
			migratedCount++
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	if migratedCount > 0 {
		s.logger.Info("migrated legacy tab metadata keys", "count", migratedCount, "workspace", defaultWorkspace)
	}

	return nil
}

// MigrateNamedKeys renames Redis keys that use workspace names to use workspace UUIDs.
// Keys with workspace segments matching a key in nameToID are renamed from
// terminal:meta:{name}:{session} to terminal:meta:{uuid}:{session}.
// Keys already using UUIDs (not in nameToID) are left unchanged.
// This is idempotent — running it multiple times produces the same result.
func (s *Store) MigrateNamedKeys(ctx context.Context, nameToID map[string]string) error {
	if len(nameToID) == 0 {
		return nil
	}

	var cursor uint64
	var migratedCount int

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, keyPrefix+"*", 100).Result()
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		for _, key := range keys {
			remainder := key[len(keyPrefix):]
			idx := strings.Index(remainder, ":")
			if idx < 0 {
				continue // Legacy key without workspace — handled by MigrateLegacyKeys
			}
			ws := remainder[:idx]
			session := remainder[idx+1:]

			uuid, ok := nameToID[ws]
			if !ok {
				continue // Already a UUID or unknown workspace name — skip
			}

			newKey := metaKey(uuid, session)
			// Use RenameNX to avoid overwriting an existing destination key.
			moved, err := s.client.RenameNX(ctx, key, newKey).Result()
			if err != nil {
				s.logger.Warn("failed to migrate named key", "key", key, "error", err)
				continue
			}
			if moved {
				migratedCount++
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	if migratedCount > 0 {
		s.logger.Info("migrated workspace-name-keyed tab metadata to UUIDs", "count", migratedCount)
	}

	return nil
}
