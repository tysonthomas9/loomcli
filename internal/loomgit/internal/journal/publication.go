package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

type Publication struct {
	Workspace, Change, Repo, Branch, Trunk, Slug, Head, StackID, Prior, DriftSHA string
	FeatureFlag                                                                  string
	Phase                                                                        string
	PRNumber                                                                     int
	PRURL                                                                        string
}

type NativeMerge struct {
	Workspace, StackID, Target, Phase, Head, UUID, Reason string
	Authority                                             string
	Changes                                               []string
}

func createNativeMergeSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS native_stack_merges (
		workspace TEXT NOT NULL, stack_id TEXT NOT NULL, target TEXT NOT NULL,
		changes TEXT NOT NULL,
		phase TEXT NOT NULL, head_sha TEXT NOT NULL DEFAULT '', request_uuid TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(workspace, stack_id))`)
	if err != nil {
		return err
	}
	for _, column := range []string{"request_uuid", "authority"} {
		_, err = db.Exec(`ALTER TABLE native_stack_merges ADD COLUMN ` + column + ` TEXT NOT NULL DEFAULT ''`)
		if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}

func (s *SQLite) BeginNativeMerge(ctx context.Context, merge NativeMerge) error {
	encoded, err := json.Marshal(merge.Changes)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO native_stack_merges
		(workspace,stack_id,target,changes,phase,authority) VALUES (?,?,?,?, 'ready',?)`,
		merge.Workspace, merge.StackID, merge.Target, string(encoded), merge.Authority)
	if err != nil {
		return err
	}
	var target, changes string
	err = s.db.QueryRowContext(ctx, `SELECT target,changes FROM native_stack_merges
		WHERE workspace=? AND stack_id=?`, merge.Workspace, merge.StackID).Scan(&target, &changes)
	if err != nil {
		return err
	}
	if target != merge.Target || changes != string(encoded) {
		return errors.New("native merge intent differs from recorded request")
	}
	return nil
}

func (s *SQLite) OpenNativeMerges(ctx context.Context) ([]NativeMerge, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,stack_id,target,changes,phase,head_sha,request_uuid,authority
		FROM native_stack_merges WHERE phase != 'done' AND phase != 'blocked'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var merges []NativeMerge
	for rows.Next() {
		var merge NativeMerge
		var changes string
		if err := rows.Scan(&merge.Workspace, &merge.StackID, &merge.Target, &changes, &merge.Phase, &merge.Head, &merge.UUID, &merge.Authority); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(changes), &merge.Changes); err != nil {
			return nil, err
		}
		merges = append(merges, merge)
	}
	return merges, rows.Err()
}

func (s *SQLite) NativeMerge(ctx context.Context, workspace, stackID string) (NativeMerge, error) {
	var merge NativeMerge
	var changes string
	err := s.db.QueryRowContext(ctx, `SELECT workspace,stack_id,target,changes,phase,head_sha,request_uuid,reason,authority
		FROM native_stack_merges WHERE workspace=? AND stack_id=?`, workspace, stackID).Scan(
		&merge.Workspace, &merge.StackID, &merge.Target, &changes, &merge.Phase, &merge.Head, &merge.UUID, &merge.Reason, &merge.Authority)
	if err != nil {
		return NativeMerge{}, err
	}
	if err := json.Unmarshal([]byte(changes), &merge.Changes); err != nil {
		return NativeMerge{}, err
	}
	return merge, nil
}

func (s *SQLite) RecordNativeMergeRequest(ctx context.Context, merge NativeMerge, uuid string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_stack_merges SET phase='sent',request_uuid=?
		WHERE workspace=? AND stack_id=? AND phase='dispatching' AND head_sha=?`,
		uuid, merge.Workspace, merge.StackID, merge.Head)
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

func (s *SQLite) AdvanceNativeMerge(ctx context.Context, merge NativeMerge, phase, head string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_stack_merges SET phase=?,head_sha=?
		WHERE workspace=? AND stack_id=? AND phase=? AND head_sha=?`,
		phase, head, merge.Workspace, merge.StackID, merge.Phase, merge.Head)
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

func (s *SQLite) BlockNativeMerge(ctx context.Context, merge NativeMerge, reason string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_stack_merges SET phase='blocked',reason=?
		WHERE workspace=? AND stack_id=? AND phase=?`,
		reason, merge.Workspace, merge.StackID, merge.Phase)
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

func createPublicationSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS change_publications (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, repo TEXT NOT NULL,
		branch TEXT NOT NULL, trunk TEXT NOT NULL, slug TEXT NOT NULL,
		head_sha TEXT NOT NULL, phase TEXT NOT NULL,
		feature_flag TEXT NOT NULL DEFAULT '',
		pr_number INTEGER NOT NULL DEFAULT 0, pr_url TEXT NOT NULL DEFAULT '',
		stack_id TEXT NOT NULL DEFAULT '', prior_sha TEXT NOT NULL DEFAULT '',
		drift_sha TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(workspace, change_id)
	)`)
	if err != nil {
		return err
	}
	rows, err := db.Query(`PRAGMA table_info(change_publications)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, column := range []string{"stack_id", "prior_sha", "drift_sha", "feature_flag"} {
		if columns[column] {
			continue
		}
		_, err = db.Exec(`ALTER TABLE change_publications ADD COLUMN ` + column + ` TEXT NOT NULL DEFAULT ''`)
		if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}

func createStackBackendSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS stack_backends (
		workspace TEXT NOT NULL, stack_id TEXT NOT NULL, backend TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT '', paths TEXT NOT NULL DEFAULT '[]',
		PRIMARY KEY(workspace, stack_id)
	)`)
	if err != nil {
		return err
	}
	for _, column := range []string{"status TEXT NOT NULL DEFAULT ''", "paths TEXT NOT NULL DEFAULT '[]'"} {
		_, err = db.Exec(`ALTER TABLE stack_backends ADD COLUMN ` + column)
		if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return createNativeMergeSchema(db)
}

func (s *SQLite) RecordStackBackend(ctx context.Context, workspace, stackID, backend string) error {
	if workspace == "" || stackID == "" || backend == "" {
		return errors.New("workspace, stack ID and backend are required")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO stack_backends(workspace,stack_id,backend) VALUES (?,?,?)`, workspace, stackID, backend); err != nil {
		return err
	}
	var recorded string
	if err := s.db.QueryRowContext(ctx, `SELECT backend FROM stack_backends WHERE workspace=? AND stack_id=?`, workspace, stackID).Scan(&recorded); err != nil {
		return err
	}
	if recorded != backend {
		return errors.New("stack backend differs from its recorded selection")
	}
	return nil
}

func (s *SQLite) StackBackend(ctx context.Context, workspace, stackID string) (string, error) {
	var backend string
	err := s.db.QueryRowContext(ctx, `SELECT backend FROM stack_backends WHERE workspace=? AND stack_id=?`, workspace, stackID).Scan(&backend)
	return backend, err
}

type StackState struct {
	Backend, Status string
	Paths           []string
}

func (s *SQLite) StackState(ctx context.Context, workspace, stackID string) (StackState, error) {
	var state StackState
	var paths string
	err := s.db.QueryRowContext(ctx, `SELECT backend,status,paths FROM stack_backends
		WHERE workspace=? AND stack_id=?`, workspace, stackID).Scan(&state.Backend, &state.Status, &paths)
	if err != nil {
		return StackState{}, err
	}
	err = json.Unmarshal([]byte(paths), &state.Paths)
	return state, err
}

func (s *SQLite) RecordStackAttention(ctx context.Context, offer RestackOffer, stackID, status string, paths []string) error {
	if stackID == "" || (status != "restack_conflict" && status != "swap_held") || len(paths) == 0 {
		return errors.New("stack, attention status and paths are required")
	}
	encodedPaths, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	message := "Resolve the listed conflicts, then retry restack."
	if status == "swap_held" {
		message = "Save or move the listed working-area edits, then retry restack."
	}
	payload, err := json.Marshal(map[string]any{"workspace": offer.Workspace, "stack_id": stackID,
		"change_id": offer.Change, "status": status, "paths": paths, "message": message})
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE stack_backends SET status=?,paths=? WHERE workspace=? AND stack_id=?`,
		status, string(encodedPaths), offer.Workspace, stackID)
	if err != nil {
		return err
	}
	if updated, err := result.RowsAffected(); err != nil || updated != 1 {
		return errors.Join(err, ErrStale)
	}
	key := fmt.Sprintf("stack-attention:%s:%s:%s:%s:%s:%s", offer.Workspace, stackID,
		offer.Change, offer.Predecessor, offer.TrunkSHA, status)
	if err := queueEvent(ctx, tx, key, "git.attention_required", payload); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) ClearStackAttention(ctx context.Context, workspace, stackID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE stack_backends SET status='',paths='[]'
		WHERE workspace=? AND stack_id=?`, workspace, stackID)
	return err
}

func (s *SQLite) RecordRestackReviewRequired(ctx context.Context, offer RestackOffer,
	stackID, lead, change string, revision int) error {
	if stackID == "" || lead == "" || change == "" || revision < 1 {
		return errors.New("stack, lead, change and derived revision are required")
	}
	payload, err := json.Marshal(map[string]any{"workspace": offer.Workspace, "stack_id": stackID,
		"lead": lead, "change_id": change, "revision": revision, "status": "review_required",
		"message": fmt.Sprintf("Review %s revision %d before publishing the restacked stack.", change, revision)})
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE stack_backends SET status='review_required',paths='[]'
		WHERE workspace=? AND stack_id=?`, offer.Workspace, stackID)
	if err != nil {
		return err
	}
	if updated, err := result.RowsAffected(); err != nil || updated != 1 {
		return errors.Join(err, ErrStale)
	}
	key := fmt.Sprintf("restack-review:%s:%s:%s:%d", offer.Workspace, stackID, change, revision)
	if err := queueEvent(ctx, tx, key, "git.attention_required", payload); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) AdoptStackPublications(ctx context.Context, publications []Publication, heads map[string]string) error {
	if len(publications) == 0 || len(publications) != len(heads) {
		return errors.New("native publication heads must cover the stack")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, publication := range publications {
		head := heads[publication.Change]
		if head == "" {
			return errors.New("native publication head is missing")
		}
		result, err := tx.ExecContext(ctx, `UPDATE change_publications SET head_sha=?,trunk=?
			WHERE workspace=? AND change_id=? AND head_sha=? AND stack_id=? AND phase='done'`,
			head, publication.Trunk, publication.Workspace, publication.Change, publication.Head, publication.StackID)
		if err != nil {
			return err
		}
		if updated, err := result.RowsAffected(); err != nil || updated != 1 {
			return errors.Join(err, ErrStale)
		}
	}
	return tx.Commit()
}

func (s *SQLite) Publication(ctx context.Context, workspace, change string) (Publication, bool, error) {
	var p Publication
	err := s.db.QueryRowContext(ctx, `SELECT workspace,change_id,repo,branch,trunk,slug,head_sha,feature_flag,phase,pr_number,pr_url,stack_id,prior_sha,drift_sha
		FROM change_publications WHERE workspace=? AND change_id=?`, workspace, change).Scan(
		&p.Workspace, &p.Change, &p.Repo, &p.Branch, &p.Trunk, &p.Slug, &p.Head, &p.FeatureFlag, &p.Phase, &p.PRNumber, &p.PRURL, &p.StackID, &p.Prior, &p.DriftSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, false, nil
	}
	return p, err == nil, err
}

func (s *SQLite) StackPublications(ctx context.Context, workspace, stackID string) ([]Publication, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,change_id,repo,branch,trunk,slug,head_sha,feature_flag,phase,pr_number,pr_url,stack_id,prior_sha,drift_sha
		FROM change_publications WHERE workspace=? AND stack_id=? ORDER BY pr_number`, workspace, stackID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var publications []Publication
	for rows.Next() {
		var publication Publication
		if err := rows.Scan(&publication.Workspace, &publication.Change, &publication.Repo, &publication.Branch,
			&publication.Trunk, &publication.Slug, &publication.Head, &publication.FeatureFlag, &publication.Phase,
			&publication.PRNumber, &publication.PRURL, &publication.StackID, &publication.Prior, &publication.DriftSHA); err != nil {
			return nil, err
		}
		publications = append(publications, publication)
	}
	return publications, rows.Err()
}

func (s *SQLite) BeginPublication(ctx context.Context, p Publication) error {
	return beginPublication(ctx, s.db, p)
}

type publicationExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func beginPublication(ctx context.Context, database publicationExecer, p Publication) error {
	_, err := database.ExecContext(ctx, `INSERT INTO change_publications
		(workspace,change_id,repo,branch,trunk,slug,head_sha,stack_id,prior_sha,feature_flag,phase) VALUES (?,?,?,?,?,?,?,?,?,?,'started')
		ON CONFLICT(workspace,change_id) DO UPDATE SET repo=excluded.repo,branch=excluded.branch,
		trunk=excluded.trunk,slug=excluded.slug,head_sha=excluded.head_sha,stack_id=excluded.stack_id,prior_sha=excluded.prior_sha,feature_flag=excluded.feature_flag,
		phase='started',drift_sha=''
		WHERE change_publications.head_sha <> excluded.head_sha OR change_publications.trunk <> excluded.trunk
		OR change_publications.stack_id <> excluded.stack_id OR change_publications.feature_flag <> excluded.feature_flag OR change_publications.phase='drift'`,
		p.Workspace, p.Change, p.Repo, p.Branch, p.Trunk, p.Slug, p.Head, p.StackID, p.Prior, p.FeatureFlag)
	return err
}

func (s *SQLite) BeginStackPublications(ctx context.Context, publications []Publication) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, publication := range publications {
		if err := beginPublication(ctx, tx, publication); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) RecordPublicationDrift(ctx context.Context, publication Publication, observed string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE change_publications SET phase='drift',drift_sha=?
		WHERE workspace=? AND change_id=? AND head_sha=? AND stack_id=?`,
		observed, publication.Workspace, publication.Change, publication.Head, publication.StackID)
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

func (s *SQLite) AdvancePublication(ctx context.Context, p Publication) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE change_publications SET phase=?,pr_number=?,pr_url=?,drift_sha=''
		WHERE workspace=? AND change_id=? AND head_sha=?`,
		p.Phase, p.PRNumber, p.PRURL, p.Workspace, p.Change, p.Head)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStale
	}
	if p.Phase == "done" {
		payload, err := json.Marshal(struct {
			Workspace string `json:"workspace"`
			ChangeID  string `json:"change_id"`
			HeadSHA   string `json:"head_sha"`
			PRNumber  int    `json:"pr_number"`
			PRURL     string `json:"pr_url"`
		}{p.Workspace, p.Change, p.Head, p.PRNumber, p.PRURL})
		if err != nil {
			return err
		}
		key := "publication-event:" + p.Workspace + ":" + p.Change + ":" + p.Head
		if err := queueEvent(ctx, tx, key, "git.published", payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) OpenPublications(ctx context.Context) ([]Publication, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,change_id,repo,branch,trunk,slug,head_sha,feature_flag,phase,pr_number,pr_url,stack_id,prior_sha,drift_sha
		FROM change_publications WHERE phase <> 'done' ORDER BY workspace,change_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Publication
	for rows.Next() {
		var p Publication
		if err := rows.Scan(&p.Workspace, &p.Change, &p.Repo, &p.Branch, &p.Trunk, &p.Slug,
			&p.Head, &p.FeatureFlag, &p.Phase, &p.PRNumber, &p.PRURL, &p.StackID, &p.Prior, &p.DriftSHA); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type ProviderObservation struct {
	Workspace, Change, Base, HeadSHA, State string
}

func createProviderSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS provider_observations (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, base_ref TEXT NOT NULL,
		head_sha TEXT NOT NULL, state TEXT NOT NULL, PRIMARY KEY(workspace,change_id)
	)`)
	return err
}

func (s *SQLite) ProviderObservation(ctx context.Context, workspace, change string) (ProviderObservation, bool, error) {
	var observation ProviderObservation
	err := s.db.QueryRowContext(ctx, `SELECT workspace,change_id,base_ref,head_sha,state
		FROM provider_observations WHERE workspace=? AND change_id=?`, workspace, change).Scan(
		&observation.Workspace, &observation.Change, &observation.Base, &observation.HeadSHA, &observation.State)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderObservation{}, false, nil
	}
	return observation, err == nil, err
}

func (s *SQLite) RecordProviderObservation(ctx context.Context, observation ProviderObservation) error {
	if observation.Workspace == "" || observation.Change == "" || observation.Base == "" || observation.State == "" {
		return errors.New("incomplete provider observation")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO provider_observations(workspace,change_id,base_ref,head_sha,state)
		VALUES (?,?,?,?,?) ON CONFLICT(workspace,change_id) DO UPDATE SET
		base_ref=excluded.base_ref,head_sha=excluded.head_sha,state=excluded.state
		WHERE base_ref<>excluded.base_ref OR head_sha<>excluded.head_sha OR state<>excluded.state`,
		observation.Workspace, observation.Change, observation.Base, observation.HeadSHA, observation.State)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		payload, err := json.Marshal(observation)
		if err != nil {
			return err
		}
		key := "provider:" + observation.Workspace + ":" + observation.Change + ":" + observation.Base + ":" + observation.HeadSHA + ":" + observation.State
		if err := queueEvent(ctx, tx, key, "git.provider_stack_changed", payload); err != nil {
			return err
		}
		if observation.State == "diverged" {
			if err := queueEvent(ctx, tx, key+":feedback", "git.feedback_recorded", payload); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

type LoomMergeLayer struct {
	Change   string `json:"change"`
	Head     string `json:"head"`
	Revision int    `json:"revision"`
	MergedBy string `json:"merged_by,omitempty"`
}

type LoomMerge struct {
	Workspace         string           `json:"workspace"`
	StackID           string           `json:"stack_id"`
	Target            string           `json:"target"`
	RequestID         string           `json:"request_id"`
	Layers            []LoomMergeLayer `json:"layers"`
	Index             int              `json:"index"`
	Phase             string           `json:"phase"`
	Reason            string           `json:"reason,omitempty"`
	PRNumber          int              `json:"pr_number,omitempty"`
	DispatchHead      string           `json:"dispatch_head,omitempty"`
	ProviderRequestID string           `json:"provider_request_id,omitempty"`
	DispatchAttempts  int              `json:"dispatch_attempts,omitempty"`
	Authority         string           `json:"authority,omitempty"`
	PolicySetBy       string           `json:"policy_set_by,omitempty"`
	Version           int              `json:"-"`
}

func (s *SQLite) ensureLoomMergeSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS loom_stack_merges (
		workspace TEXT NOT NULL, stack_id TEXT NOT NULL, request_id TEXT NOT NULL,
		state BLOB NOT NULL, version INTEGER NOT NULL DEFAULT 1,
		PRIMARY KEY(workspace,stack_id), UNIQUE(request_id))`)
	return err
}

func (s *SQLite) BeginLoomMerge(ctx context.Context, merge LoomMerge) (LoomMerge, error) {
	if merge.Workspace == "" || merge.StackID == "" || merge.Target == "" || merge.RequestID == "" || len(merge.Layers) == 0 {
		return LoomMerge{}, errors.New("merge intent is incomplete")
	}
	if err := s.ensureLoomMergeSchema(ctx); err != nil {
		return LoomMerge{}, err
	}
	merge.Phase = "ready"
	data, err := json.Marshal(merge)
	if err != nil {
		return LoomMerge{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO loom_stack_merges(workspace,stack_id,request_id,state)
		VALUES(?,?,?,?) ON CONFLICT(workspace,stack_id) DO UPDATE SET request_id=excluded.request_id,
		state=excluded.state,version=loom_stack_merges.version+1
		WHERE loom_stack_merges.request_id != excluded.request_id
		AND json_extract(loom_stack_merges.state,'$.phase') IN ('done','blocked')`,
		merge.Workspace, merge.StackID, merge.RequestID, data)
	if err != nil {
		return LoomMerge{}, err
	}
	recorded, err := s.LoomMerge(ctx, merge.Workspace, merge.StackID)
	if err != nil {
		return LoomMerge{}, err
	}
	if recorded.RequestID != merge.RequestID || recorded.Target != merge.Target || !reflect.DeepEqual(recorded.Layers, merge.Layers) {
		return LoomMerge{}, errors.New("merge intent differs from recorded request")
	}
	return recorded, nil
}

func (s *SQLite) LoomMerge(ctx context.Context, workspace, stackID string) (LoomMerge, error) {
	if err := s.ensureLoomMergeSchema(ctx); err != nil {
		return LoomMerge{}, err
	}
	var data []byte
	var version int
	err := s.db.QueryRowContext(ctx, `SELECT state,version FROM loom_stack_merges WHERE workspace=? AND stack_id=?`, workspace, stackID).Scan(&data, &version)
	if err != nil {
		return LoomMerge{}, err
	}
	var merge LoomMerge
	if err := json.Unmarshal(data, &merge); err != nil {
		return LoomMerge{}, err
	}
	merge.Version = version
	return merge, nil
}

func (s *SQLite) OpenLoomMerges(ctx context.Context) ([]LoomMerge, error) {
	if err := s.ensureLoomMergeSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT state,version FROM loom_stack_merges`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var merges []LoomMerge
	for rows.Next() {
		var data []byte
		var merge LoomMerge
		if err := rows.Scan(&data, &merge.Version); err != nil {
			return nil, err
		}
		version := merge.Version
		if err := json.Unmarshal(data, &merge); err != nil {
			return nil, err
		}
		merge.Version = version
		if merge.Phase != "done" && merge.Phase != "blocked" {
			merges = append(merges, merge)
		}
	}
	return merges, rows.Err()
}

func (s *SQLite) AdvanceLoomMerge(ctx context.Context, before, after LoomMerge) error {
	data, err := json.Marshal(after)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE loom_stack_merges SET state=?,version=version+1
		WHERE workspace=? AND stack_id=? AND version=?`, data, before.Workspace, before.StackID, before.Version)
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
