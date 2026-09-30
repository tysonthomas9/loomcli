package journal

import (
	"context"
	"database/sql"
	"time"
)

const outboxDeliveryWindow = 10 * time.Minute

func createOutboxDeliverySchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS event_outbox_delivery (
		event_id INTEGER PRIMARY KEY REFERENCES event_outbox(id) ON DELETE CASCADE,
		created_at INTEGER NOT NULL, jsonl_emitted INTEGER NOT NULL DEFAULT 0,
		expired INTEGER NOT NULL DEFAULT 0
	);
	CREATE TRIGGER IF NOT EXISTS event_outbox_delivery_insert AFTER INSERT ON event_outbox
	BEGIN
		INSERT INTO event_outbox_delivery(event_id,created_at) VALUES (NEW.id,unixepoch());
	END;
	INSERT OR IGNORE INTO event_outbox_delivery(event_id,created_at)
		SELECT id,unixepoch() FROM event_outbox;`)
	return err
}

func (s *SQLite) expirePendingEvents(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE event_outbox_delivery SET expired=1
		WHERE created_at <= ? AND expired=0 AND event_id IN
		(SELECT id FROM event_outbox WHERE delivered=0)`, time.Now().Add(-outboxDeliveryWindow).Unix())
	return err
}

func (s *SQLite) MarkJSONLEmitted(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE event_outbox_delivery SET jsonl_emitted=1 WHERE event_id=?`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
