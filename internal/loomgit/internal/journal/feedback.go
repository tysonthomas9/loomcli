package journal

import (
	"context"
	"database/sql"
	"errors"
)

type Feedback struct {
	Workspace   string `json:"workspace"`
	Change      string `json:"change"`
	DeliveryID  string `json:"delivery_id"`
	Kind        string `json:"kind"`
	Actor       string `json:"actor"`
	Association string `json:"association"`
	Body        string `json:"body"`
	HeadSHA     string `json:"head_sha"`
	Status      string `json:"status"`
	PRNumber    int    `json:"pr_number"`
	Revision    int    `json:"revision"`
	RequestID   string `json:"request_id"`
	Target      string `json:"target"`
	Attempt     string `json:"attempt"`
	BaseSHA     string `json:"base_sha"`
	Prompt      string `json:"prompt"`
}

type FeedbackRequest struct {
	Workspace  string `json:"workspace"`
	DeliveryID string `json:"delivery_id"`
	Change     string `json:"change"`
	PRNumber   int    `json:"pr_number"`
	RequestID  string `json:"request_id"`
	Target     string `json:"target"`
	Attempt    string `json:"attempt"`
	BaseSHA    string `json:"base_sha"`
	Prompt     string `json:"prompt"`
	Revision   int    `json:"revision"`
}

func createFeedbackSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS change_feedback (
		workspace TEXT NOT NULL, delivery_id TEXT NOT NULL, change_id TEXT NOT NULL,
		pr_number INTEGER NOT NULL, kind TEXT NOT NULL, actor TEXT NOT NULL,
		association TEXT NOT NULL, body TEXT NOT NULL, head_sha TEXT NOT NULL,
		status TEXT NOT NULL, PRIMARY KEY(workspace, delivery_id)
	); CREATE INDEX IF NOT EXISTS change_feedback_status
		ON change_feedback(workspace, change_id, status);
	CREATE TABLE IF NOT EXISTS feedback_requests (
		workspace TEXT NOT NULL, delivery_id TEXT NOT NULL, change_id TEXT NOT NULL,
		pr_number INTEGER NOT NULL,
		request_id TEXT NOT NULL UNIQUE, target TEXT NOT NULL, attempt TEXT NOT NULL,
		base_sha TEXT NOT NULL, prompt TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(workspace, delivery_id)
	)`)
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
	err := s.db.QueryRowContext(ctx, `SELECT f.workspace,f.delivery_id,f.change_id,f.pr_number,f.kind,
		f.actor,f.association,f.body,f.head_sha,f.status,COALESCE(r.revision,0),
		COALESCE(r.request_id,''),COALESCE(r.target,''),COALESCE(r.attempt,''),COALESCE(r.base_sha,''),COALESCE(r.prompt,'')
		FROM change_feedback f LEFT JOIN feedback_requests r
		ON r.workspace=f.workspace AND r.delivery_id=f.delivery_id
		WHERE f.workspace=? AND f.delivery_id=?`,
		workspace, deliveryID).Scan(&item.Workspace, &item.DeliveryID, &item.Change, &item.PRNumber,
		&item.Kind, &item.Actor, &item.Association, &item.Body, &item.HeadSHA, &item.Status, &item.Revision,
		&item.RequestID, &item.Target, &item.Attempt, &item.BaseSHA, &item.Prompt)
	if errors.Is(err, sql.ErrNoRows) {
		return Feedback{}, ErrNotFound
	}
	return item, err
}

func (s *SQLite) FeedbackStatus(ctx context.Context, workspace, change string) ([]Feedback, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.workspace,f.delivery_id,f.change_id,f.pr_number,f.kind,
		f.actor,f.association,f.body,f.head_sha,f.status,COALESCE(r.revision,0),
		COALESCE(r.request_id,''),COALESCE(r.target,''),COALESCE(r.attempt,''),COALESCE(r.base_sha,''),COALESCE(r.prompt,'')
		FROM change_feedback f LEFT JOIN feedback_requests r
		ON r.workspace=f.workspace AND r.delivery_id=f.delivery_id
		WHERE f.workspace=? AND f.change_id=? ORDER BY f.rowid`, workspace, change)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var items []Feedback
	for rows.Next() {
		var item Feedback
		if err := rows.Scan(&item.Workspace, &item.DeliveryID, &item.Change, &item.PRNumber,
			&item.Kind, &item.Actor, &item.Association, &item.Body, &item.HeadSHA, &item.Status, &item.Revision,
			&item.RequestID, &item.Target, &item.Attempt, &item.BaseSHA, &item.Prompt); err != nil {
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

func (s *SQLite) FeedbackRequest(ctx context.Context, workspace, deliveryID string) (FeedbackRequest, bool, error) {
	var request FeedbackRequest
	err := s.db.QueryRowContext(ctx, `SELECT workspace,delivery_id,change_id,pr_number,request_id,target,attempt,base_sha,prompt,revision
		FROM feedback_requests WHERE workspace=? AND delivery_id=?`, workspace, deliveryID).Scan(
		&request.Workspace, &request.DeliveryID, &request.Change, &request.PRNumber, &request.RequestID, &request.Target,
		&request.Attempt, &request.BaseSHA, &request.Prompt, &request.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return FeedbackRequest{}, false, nil
	}
	return request, err == nil, err
}

func (s *SQLite) RecordFeedbackRequest(ctx context.Context, request FeedbackRequest) (FeedbackRequest, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO feedback_requests
		(workspace,delivery_id,change_id,pr_number,request_id,target,attempt,base_sha,prompt)
		VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(workspace,delivery_id) DO NOTHING`, request.Workspace,
		request.DeliveryID, request.Change, request.PRNumber, request.RequestID, request.Target, request.Attempt, request.BaseSHA, request.Prompt)
	if err != nil {
		return FeedbackRequest{}, err
	}
	stored, _, err := s.FeedbackRequest(ctx, request.Workspace, request.DeliveryID)
	if err != nil {
		return FeedbackRequest{}, err
	}
	if stored.Change != request.Change || stored.PRNumber != request.PRNumber ||
		stored.Target != request.Target || stored.Attempt != request.Attempt || stored.BaseSHA != request.BaseSHA {
		return FeedbackRequest{}, ErrStale
	}
	return stored, nil
}
