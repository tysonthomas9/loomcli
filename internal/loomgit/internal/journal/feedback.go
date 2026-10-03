package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
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
	);
	CREATE TABLE IF NOT EXISTS feedback_updates (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, revision INTEGER NOT NULL,
		lead TEXT NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
		verdict_id INTEGER NOT NULL DEFAULT 0, merge_cancelled INTEGER NOT NULL DEFAULT 0,
		held_notice TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(workspace, change_id, revision)
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

// Feedback update states (D29 (6)): a fix-up revision of a change whose PR is
// open is either on its way to the PR (pushing: its feedback verdict, follow
// and publish intent are recorded) or was never pushed, for the reason given.
const (
	FeedbackUpdatePushing   = "pushing"
	FeedbackUpdateNotPushed = "not_pushed"
)

// FeedbackUpdate is Loom's automatic handling of one fix-up revision.
type FeedbackUpdate struct {
	Workspace, Change, Lead, Status, Reason string
	Revision                                int
	VerdictID                               int64
	MergeCancelled                          bool
}

// FixupCandidate is the newest source revision of a change whose PR is open
// that nobody has decided on yet.
type FixupCandidate struct {
	Workspace, Change, HeadSHA, BaseSHA string
	Revision                            int
	Incomplete                          bool
}

// FixupCandidates lists, for every change with an open PR, its newest source
// revision when that revision has no verdict and no feedback update yet. The
// revision the PR was opened from always has a verdict, so the first version
// of a task is never a candidate.
func (s *SQLite) FixupCandidates(ctx context.Context) ([]FixupCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.workspace, r.change_id, r.number, r.head_sha, r.base_sha, r.incomplete
		FROM change_publications p JOIN change_revisions r ON r.workspace=p.workspace AND r.change_id=p.change_id
		WHERE p.phase='done' AND p.pr_number>0 AND r.kind='source' AND r.ready=1
		AND r.number=(SELECT MAX(n.number) FROM change_revisions n WHERE n.workspace=r.workspace
			AND n.change_id=r.change_id AND n.kind='source' AND n.ready=1)
		AND NOT EXISTS (SELECT 1 FROM review_verdicts v WHERE v.workspace=r.workspace
			AND v.change_id=r.change_id AND v.number=r.number)
		AND NOT EXISTS (SELECT 1 FROM feedback_updates f WHERE f.workspace=r.workspace
			AND f.change_id=r.change_id AND f.revision=r.number)
		AND NOT EXISTS (SELECT 1 FROM landed_changes l WHERE l.workspace=r.workspace AND l.change_id=r.change_id)
		ORDER BY r.workspace, r.change_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FixupCandidate
	for rows.Next() {
		var c FixupCandidate
		if err := rows.Scan(&c.Workspace, &c.Change, &c.Revision, &c.HeadSHA, &c.BaseSHA, &c.Incomplete); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PublishedApproval returns the approval a change's open PR was published
// under, and the lead it was applied to: the newest applied follow of an
// approving verdict. A feedback verdict resolves to the approval it carries.
func (s *SQLite) PublishedApproval(ctx context.Context, workspace, change string) (int64, string, bool, error) {
	var id, source int64
	var kind, lead string
	err := s.db.QueryRowContext(ctx, `SELECT v.id, v.kind, v.source_verdict_id, f.lead FROM approval_follow f
		JOIN review_verdicts v ON v.id=f.verdict_id
		WHERE f.workspace=? AND f.change_id=? AND f.status='applied'
		AND v.kind IN ('approve','override','policy','feedback')
		ORDER BY f.verdict_id DESC LIMIT 1`, workspace, change).Scan(&id, &kind, &source, &lead)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	if kind == "feedback" {
		id = source
	}
	return id, lead, true, nil
}

// RecordFeedbackUpdate records, in one transaction, Loom's system "feedback"
// verdict on a fix-up revision (chained to the approval the PR was published
// under), the follow that applies it into lead's working area, the intent to
// push it to the open PR, and the update itself. It returns ErrStale when the
// revision is no longer the newest complete source revision or someone has
// decided on it in the meantime.
func (s *SQLite) RecordFeedbackUpdate(ctx context.Context, update FeedbackUpdate, headSHA string) (FeedbackUpdate, error) {
	if update.Workspace == "" || update.Change == "" || update.Lead == "" || update.Revision < 1 || update.VerdictID < 1 {
		return update, errors.New("workspace, change, lead, revision and approval are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return update, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO review_verdicts
		(workspace,change_id,number,head_sha,kind,actor_kind,actor_id,reason,source_verdict_id)
		SELECT ?,?,?,?,'feedback','system','loom','review_fixup',? WHERE EXISTS (
			SELECT 1 FROM change_revisions r WHERE r.workspace=? AND r.change_id=? AND r.number=?
			AND r.head_sha=? AND r.kind='source' AND r.ready=1 AND r.incomplete=0
			AND NOT EXISTS (SELECT 1 FROM change_revisions newer WHERE newer.workspace=r.workspace
				AND newer.change_id=r.change_id AND newer.kind='source' AND newer.ready=1 AND newer.number>r.number))
		AND NOT EXISTS (SELECT 1 FROM review_verdicts v WHERE v.workspace=? AND v.change_id=? AND v.number=?)
		AND EXISTS (SELECT 1 FROM review_verdicts a WHERE a.id=? AND a.workspace=? AND a.change_id=?
			AND a.kind IN ('approve','override','policy'))`,
		update.Workspace, update.Change, update.Revision, headSHA, update.VerdictID,
		update.Workspace, update.Change, update.Revision, headSHA,
		update.Workspace, update.Change, update.Revision,
		update.VerdictID, update.Workspace, update.Change)
	if err != nil {
		return update, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return update, errors.Join(err, ErrStale)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return update, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO approval_follow(workspace,lead,change_id,revision,verdict_id)
		VALUES (?,?,?,?,?) ON CONFLICT(workspace,lead,change_id,revision) DO NOTHING`,
		update.Workspace, update.Lead, update.Change, update.Revision, id); err != nil {
		return update, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO approval_publications(workspace,lead,change_id,revision,verdict_id)
		VALUES (?,?,?,?,?) ON CONFLICT(workspace,lead,change_id,revision) DO NOTHING`,
		update.Workspace, update.Lead, update.Change, update.Revision, id); err != nil {
		return update, err
	}
	update.Status, update.Reason, update.VerdictID = FeedbackUpdatePushing, "", id
	if _, err := tx.ExecContext(ctx, `INSERT INTO feedback_updates
		(workspace,change_id,revision,lead,status,reason,verdict_id,merge_cancelled) VALUES (?,?,?,?,?,?,?,?)`,
		update.Workspace, update.Change, update.Revision, update.Lead, update.Status, update.Reason,
		id, update.MergeCancelled); err != nil {
		return update, err
	}
	verdict := loomgit.Verdict{ID: id, Workspace: update.Workspace, Change: update.Change, Number: update.Revision,
		HeadSHA: headSHA, Kind: "feedback", ActorKind: "system", ActorID: "loom"}
	if err := queueVerdictEvent(ctx, tx, verdict); err != nil {
		return update, err
	}
	return update, tx.Commit()
}

// RecordFeedbackNotPushed records that a fix-up revision is not pushed to its
// PR, and why, and tells the lead with an attention event. Recording it again
// keeps the first record.
func (s *SQLite) RecordFeedbackNotPushed(ctx context.Context, update FeedbackUpdate) error {
	if update.Workspace == "" || update.Change == "" || update.Revision < 1 || update.Reason == "" {
		return errors.New("workspace, change, revision and reason are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO feedback_updates
		(workspace,change_id,revision,lead,status,reason,verdict_id,merge_cancelled) VALUES (?,?,?,?,?,?,0,0)
		ON CONFLICT(workspace,change_id,revision) DO NOTHING`,
		update.Workspace, update.Change, update.Revision, update.Lead, FeedbackUpdateNotPushed, update.Reason)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return err
	}
	key := fmt.Sprintf("feedback-not-pushed:%s:%s:%d", update.Workspace, update.Change, update.Revision)
	if err := queueFeedbackAttention(ctx, tx, key, update, FeedbackUpdateNotPushed, nil,
		fmt.Sprintf("Revision %d of %s was not pushed to its PR: %s.", update.Revision, update.Change, update.Reason)); err != nil {
		return err
	}
	return tx.Commit()
}

// NoteFeedbackHeld tells the lead, once per held state, that a fix-up could
// not be applied in place (a conflict, or edits in its working area) and so
// was not pushed. It reports whether a new notice was queued.
func (s *SQLite) NoteFeedbackHeld(ctx context.Context, update FeedbackUpdate, status string, paths []string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE feedback_updates SET held_notice=?
		WHERE workspace=? AND change_id=? AND revision=? AND status=? AND held_notice<>?`,
		status, update.Workspace, update.Change, update.Revision, FeedbackUpdatePushing, status)
	if err != nil {
		return false, err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	message := fmt.Sprintf("Revision %d of %s conflicts with the stack, so it was not pushed to its PR. Create a fix-up task to resolve it.", update.Revision, update.Change)
	if status == "apply_pending" {
		message = fmt.Sprintf("Revision %d of %s is held: the lead's working area has edits to the same files, so it was not pushed to its PR. Commit or move them, and it will be pushed.", update.Revision, update.Change)
	}
	if len(paths) > 0 {
		message += " Paths: " + strings.Join(paths, ", ")
	}
	key := fmt.Sprintf("feedback-held:%s:%s:%d:%s", update.Workspace, update.Change, update.Revision, status)
	if err := queueFeedbackAttention(ctx, tx, key, update, status, paths, message); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func queueFeedbackAttention(ctx context.Context, tx *sql.Tx, key string, update FeedbackUpdate, status string, paths []string, message string) error {
	if paths == nil {
		paths = []string{}
	}
	payload, err := json.Marshal(map[string]any{"workspace": update.Workspace, "lead": update.Lead,
		"change_id": update.Change, "revision": update.Revision, "status": status, "paths": paths, "message": message})
	if err != nil {
		return err
	}
	return queueEvent(ctx, tx, key, "git.attention_required", payload)
}

// PushingFeedbackUpdates lists the fix-ups on their way to their PRs.
func (s *SQLite) PushingFeedbackUpdates(ctx context.Context) ([]FeedbackUpdate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+feedbackUpdateColumns+` FROM feedback_updates
		WHERE status=? ORDER BY verdict_id`, FeedbackUpdatePushing)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FeedbackUpdate
	for rows.Next() {
		update, err := scanFeedbackUpdate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, update)
	}
	return out, rows.Err()
}

// FeedbackUpdateState is a fix-up's update together with how its follow and
// publish intent went, for the task view.
type FeedbackUpdateState struct {
	FeedbackUpdate
	FollowStatus, PublishStatus, PublishReason string
	Paths                                      []string
}

// FeedbackUpdateFor returns the feedback update of one revision, if any.
func (s *SQLite) FeedbackUpdateFor(ctx context.Context, workspace, change string, revision int) (FeedbackUpdateState, bool, error) {
	var state FeedbackUpdateState
	var paths []byte
	err := s.db.QueryRowContext(ctx, `SELECT `+feedbackUpdateColumnsOf("u")+`,
		COALESCE(f.status,''), COALESCE(f.paths,'[]'), COALESCE(p.status,''), COALESCE(p.reason,'')
		FROM feedback_updates u
		LEFT JOIN approval_follow f ON f.workspace=u.workspace AND f.lead=u.lead AND f.change_id=u.change_id AND f.revision=u.revision
		LEFT JOIN approval_publications p ON p.workspace=u.workspace AND p.lead=u.lead AND p.change_id=u.change_id AND p.revision=u.revision
		WHERE u.workspace=? AND u.change_id=? AND u.revision=?`, workspace, change, revision).Scan(
		&state.Workspace, &state.Change, &state.Revision, &state.Lead, &state.Status, &state.Reason,
		&state.VerdictID, &state.MergeCancelled, &state.FollowStatus, &paths, &state.PublishStatus, &state.PublishReason)
	if errors.Is(err, sql.ErrNoRows) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	if err := json.Unmarshal(paths, &state.Paths); err != nil {
		return state, false, err
	}
	return state, true, nil
}

const feedbackUpdateColumns = `workspace,change_id,revision,lead,status,reason,verdict_id,merge_cancelled`

func feedbackUpdateColumnsOf(alias string) string {
	columns := strings.Split(feedbackUpdateColumns, ",")
	for index := range columns {
		columns[index] = alias + "." + columns[index]
	}
	return strings.Join(columns, ",")
}

func scanFeedbackUpdate(row interface{ Scan(...any) error }) (FeedbackUpdate, error) {
	var update FeedbackUpdate
	err := row.Scan(&update.Workspace, &update.Change, &update.Revision, &update.Lead, &update.Status,
		&update.Reason, &update.VerdictID, &update.MergeCancelled)
	return update, err
}

// FinishFeedbackUpdate settles a pushing update: pushed once its PR has the
// fix-up, superseded by a newer fix-up, or not_pushed with the reason.
func (s *SQLite) FinishFeedbackUpdate(ctx context.Context, update FeedbackUpdate, status, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE feedback_updates SET status=?, reason=?
		WHERE workspace=? AND change_id=? AND revision=? AND status=?`,
		status, reason, update.Workspace, update.Change, update.Revision, FeedbackUpdatePushing)
	return err
}
