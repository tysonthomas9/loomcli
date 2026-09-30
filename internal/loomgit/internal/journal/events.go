package journal

import (
	"context"
	"database/sql"
)

func queueEvent(ctx context.Context, tx *sql.Tx, key, kind string, payload []byte) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO journal_entries(id,request_id,operation,phase,version,fence)
		VALUES (?,?,'event','done',1,1) ON CONFLICT(id) DO NOTHING`, key, key)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO event_outbox(entry_id,kind,payload) VALUES (?,?,?)`, key, kind, payload)
	return err
}
