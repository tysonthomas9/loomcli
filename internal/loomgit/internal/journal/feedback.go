package journal

import (
	"context"
	"database/sql"
	"errors"
)

type Feedback struct {
	Workspace, Change, DeliveryID, Kind, Actor, Association, Body, HeadSHA, Status string
	PRNumber                                                                       int
}

func createFeedbackSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS change_feedback (
		workspace TEXT NOT NULL, delivery_id TEXT NOT NULL, change_id TEXT NOT NULL,
		pr_number INTEGER NOT NULL, kind TEXT NOT NULL, actor TEXT NOT NULL,
		association TEXT NOT NULL, body TEXT NOT NULL, head_sha TEXT NOT NULL,
		status TEXT NOT NULL, PRIMARY KEY(workspace, delivery_id)
	); CREATE INDEX IF NOT EXISTS change_feedback_status
		ON change_feedback(workspace, change_id, status)`)
	return err
}

func (s *SQLite) PublishedPR(ctx context.Context, workspace, slug string, number int) (Publication, bool, error) {
	var change string
	err := s.db.QueryRowContext(ctx, `SELECT change_id FROM change_publications
		WHERE workspace=? AND slug=? AND pr_number=? AND phase='done'`, workspace, slug, number).Scan(&change)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, false, nil
	}
	if err != nil {
		return Publication{}, false, err
	}
	return s.Publication(ctx, workspace, change)
}

func (s *SQLite) RecordFeedback(ctx context.Context, item Feedback) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO change_feedback
		(workspace,delivery_id,change_id,pr_number,kind,actor,association,body,head_sha,status)
		VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(workspace,delivery_id) DO NOTHING`,
		item.Workspace, item.DeliveryID, item.Change, item.PRNumber, item.Kind,
		item.Actor, item.Association, item.Body, item.HeadSHA, item.Status)
	return err
}

func (s *SQLite) Feedback(ctx context.Context, workspace, deliveryID string) (Feedback, error) {
	var item Feedback
	err := s.db.QueryRowContext(ctx, `SELECT workspace,delivery_id,change_id,pr_number,kind,
		actor,association,body,head_sha,status FROM change_feedback WHERE workspace=? AND delivery_id=?`,
		workspace, deliveryID).Scan(&item.Workspace, &item.DeliveryID, &item.Change, &item.PRNumber,
		&item.Kind, &item.Actor, &item.Association, &item.Body, &item.HeadSHA, &item.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return Feedback{}, ErrNotFound
	}
	return item, err
}

func (s *SQLite) FeedbackStatus(ctx context.Context, workspace, change string) ([]Feedback, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,delivery_id,change_id,pr_number,kind,
		actor,association,body,head_sha,status FROM change_feedback
		WHERE workspace=? AND change_id=? ORDER BY rowid`, workspace, change)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var items []Feedback
	for rows.Next() {
		var item Feedback
		if err := rows.Scan(&item.Workspace, &item.DeliveryID, &item.Change, &item.PRNumber,
			&item.Kind, &item.Actor, &item.Association, &item.Body, &item.HeadSHA, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLite) MarkFeedbackAddressed(ctx context.Context, workspace, deliveryID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE change_feedback SET status='addressed'
		WHERE workspace=? AND delivery_id=? AND status='pending'`, workspace, deliveryID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrStale
	}
	return nil
}
