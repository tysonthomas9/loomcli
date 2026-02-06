package sqlite

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/types"
)

// GetDependenciesForIssues fetches dependencies for multiple issues in a single query.
// Returns a map of issue_id -> []Dependency for O(1) lookup during assignment.
// Issues without dependencies will have an empty slice in the map (not nil).
func (s *SQLiteStorage) GetDependenciesForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Dependency, error) {
	if len(issueIDs) == 0 {
		return make(map[string][]*types.Dependency), nil
	}

	// Hold read lock during database operations to prevent reconnect() from
	// closing the connection mid-query (GH#607 race condition fix)
	s.reconnectMu.RLock()
	defer s.reconnectMu.RUnlock()

	// Build placeholders for IN clause
	placeholders := make([]interface{}, len(issueIDs))
	for i, id := range issueIDs {
		placeholders[i] = id
	}

	query := fmt.Sprintf(`
		SELECT issue_id, depends_on_id, type, created_at, created_by,
		       COALESCE(metadata, '') as metadata, COALESCE(thread_id, '') as thread_id
		FROM dependencies
		WHERE issue_id IN (%s)
		ORDER BY issue_id, created_at
	`, buildPlaceholders(len(issueIDs))) // #nosec G201 -- placeholders are generated internally

	rows, err := s.db.QueryContext(ctx, query, placeholders...)
	if err != nil {
		return nil, fmt.Errorf("failed to batch get dependencies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string][]*types.Dependency)
	for rows.Next() {
		var dep types.Dependency
		var createdAtStr string
		var metadata, threadID string

		if err := rows.Scan(
			&dep.IssueID,
			&dep.DependsOnID,
			&dep.Type,
			&createdAtStr,
			&dep.CreatedBy,
			&metadata,
			&threadID,
		); err != nil {
			return nil, fmt.Errorf("failed to scan dependency: %w", err)
		}

		// Parse timestamp
		dep.CreatedAt = parseTimeString(createdAtStr)
		dep.Metadata = metadata
		dep.ThreadID = threadID

		result[dep.IssueID] = append(result[dep.IssueID], &dep)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating dependency rows: %w", err)
	}

	// Ensure all requested issue IDs have entries (empty slice for no dependencies)
	for _, id := range issueIDs {
		if _, ok := result[id]; !ok {
			result[id] = []*types.Dependency{}
		}
	}

	return result, nil
}
