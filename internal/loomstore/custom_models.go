package loomstore

import "context"

// CustomModels returns the model ids workspace added to harness's catalog
// (MCS3), in the order they were set; none is an empty list.
func (s *Store) CustomModels(ctx context.Context, workspace, harness string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT model FROM custom_models WHERE workspace_id = ? AND harness = ? ORDER BY pos`, workspace, harness)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetCustomModels replaces workspace's custom model ids for harness with ids,
// which must not repeat.
func (s *Store) SetCustomModels(ctx context.Context, workspace, harness string, ids []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM custom_models WHERE workspace_id = ? AND harness = ?`, workspace, harness); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO custom_models (workspace_id, harness, model, pos) VALUES (?, ?, ?, ?)`,
			workspace, harness, id, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}
