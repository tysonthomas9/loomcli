package journal

import (
	"context"
	"database/sql"
	"errors"
)

type LandingStatus struct {
	State string
	Rule  string
	Offer *RestackOffer
}

type RestackOffer struct {
	Workspace, Change, Predecessor, TrunkSHA string
	Task, Repo                               string
	Revision, DerivedRevision                int
}

func ensureLandedRule(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(landed_changes)`)
	if err != nil {
		return err
	}
	var hasRule bool
	for rows.Next() {
		var index, required, primary int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&index, &name, &columnType, &required, &defaultValue, &primary); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "rule" {
			hasRule = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasRule {
		if _, err := db.Exec(`ALTER TABLE landed_changes ADD COLUMN rule TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS merged_changes (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL,
		PRIMARY KEY(workspace, change_id)
	);
	CREATE TABLE IF NOT EXISTS restack_offers (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, predecessor TEXT NOT NULL,
		task_id TEXT NOT NULL, repo TEXT NOT NULL,
		revision INTEGER NOT NULL, trunk_sha TEXT NOT NULL, derived_revision INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(workspace, change_id, predecessor)
	)`)
	return err
}

func (s *SQLite) MarkMerged(ctx context.Context, workspace, change string) error {
	if workspace == "" || change == "" {
		return errors.New("workspace and change are required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO merged_changes(workspace,change_id)
		SELECT ?,? WHERE NOT EXISTS (SELECT 1 FROM landed_changes WHERE workspace=? AND change_id=?)
		ON CONFLICT(workspace,change_id) DO NOTHING`, workspace, change, workspace, change)
	return err
}

func (s *SQLite) LandingStatus(ctx context.Context, workspace, change string) (LandingStatus, error) {
	var rule string
	err := s.db.QueryRowContext(ctx, `SELECT rule FROM landed_changes WHERE workspace=? AND change_id=?`, workspace, change).Scan(&rule)
	if err == nil {
		return LandingStatus{State: "landed", Rule: rule}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return LandingStatus{}, err
	}
	var found int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM merged_changes WHERE workspace=? AND change_id=?`, workspace, change).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		offers, err := s.RestackOffers(ctx, workspace, change)
		if err != nil {
			return LandingStatus{}, err
		}
		if len(offers) > 0 {
			return LandingStatus{State: "restack_available", Offer: &offers[0]}, nil
		}
		err = s.db.QueryRowContext(ctx, `SELECT 1 FROM change_publications WHERE workspace=? AND change_id=?`, workspace, change).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return LandingStatus{State: "unknown"}, nil
		}
		if err != nil {
			return LandingStatus{}, err
		}
		return LandingStatus{State: "published"}, nil
	}
	if err != nil {
		return LandingStatus{}, err
	}
	return LandingStatus{State: "merged"}, nil
}

func (s *SQLite) PublishedChanges(ctx context.Context) ([]Publication, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,change_id,repo,branch,trunk,slug,head_sha,feature_flag,phase,pr_number,pr_url
		FROM change_publications WHERE phase='done' AND pr_number>0 ORDER BY workspace,change_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var publications []Publication
	for rows.Next() {
		var publication Publication
		if err := rows.Scan(&publication.Workspace, &publication.Change, &publication.Repo,
			&publication.Branch, &publication.Trunk, &publication.Slug, &publication.Head, &publication.FeatureFlag,
			&publication.Phase, &publication.PRNumber, &publication.PRURL); err != nil {
			return nil, err
		}
		publications = append(publications, publication)
	}
	return publications, rows.Err()
}

func (s *SQLite) ChangeForTask(ctx context.Context, workspace, task, repo string) (string, error) {
	var change string
	err := s.db.QueryRowContext(ctx, `SELECT change_id FROM driver_changes
		WHERE workspace=? AND task_id=? AND repo=?`, workspace, task, repo).Scan(&change)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return change, err
}

func (s *SQLite) SourceRevision(ctx context.Context, workspace, change string) (int, error) {
	var number int
	err := s.db.QueryRowContext(ctx, `SELECT number FROM change_revisions
		WHERE workspace=? AND change_id=? AND ready=1 AND incomplete=0 ORDER BY number DESC LIMIT 1`, workspace, change).Scan(&number)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return number, err
}

func (s *SQLite) OfferRestack(ctx context.Context, offer RestackOffer) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO restack_offers
		(workspace,change_id,predecessor,task_id,repo,revision,trunk_sha)
		SELECT ?,?,?,?,?,?,? WHERE NOT EXISTS
		(SELECT 1 FROM landed_changes WHERE workspace=? AND change_id=?)
		ON CONFLICT(workspace,change_id,predecessor) DO NOTHING`, offer.Workspace, offer.Change,
		offer.Predecessor, offer.Task, offer.Repo, offer.Revision, offer.TrunkSHA,
		offer.Workspace, offer.Change)
	return err
}

func (s *SQLite) OpenRestackOffers(ctx context.Context) ([]RestackOffer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.workspace,o.change_id,o.predecessor,o.task_id,o.repo,o.revision,o.trunk_sha,o.derived_revision
		FROM restack_offers o WHERE o.derived_revision=0 AND NOT EXISTS
		(SELECT 1 FROM landed_changes l WHERE l.workspace=o.workspace AND l.change_id=o.change_id)
		ORDER BY o.workspace,o.change_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var offers []RestackOffer
	for rows.Next() {
		var offer RestackOffer
		if err := rows.Scan(&offer.Workspace, &offer.Change, &offer.Predecessor, &offer.Task,
			&offer.Repo, &offer.Revision, &offer.TrunkSHA, &offer.DerivedRevision); err != nil {
			return nil, err
		}
		offers = append(offers, offer)
	}
	return offers, rows.Err()
}

func (s *SQLite) CompleteRestackOffer(ctx context.Context, offer RestackOffer, derivedRevision int) error {
	if derivedRevision < 1 {
		return errors.New("derived revision is required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE restack_offers SET derived_revision=?
		WHERE workspace=? AND change_id=? AND predecessor=? AND trunk_sha=? AND derived_revision=0`,
		derivedRevision, offer.Workspace, offer.Change, offer.Predecessor, offer.TrunkSHA)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrStale
	}
	return nil
}

func (s *SQLite) RestackOffers(ctx context.Context, workspace, change string) ([]RestackOffer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,change_id,predecessor,task_id,repo,revision,trunk_sha,derived_revision
		FROM restack_offers WHERE workspace=? AND change_id=? ORDER BY predecessor`, workspace, change)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var offers []RestackOffer
	for rows.Next() {
		var offer RestackOffer
		if err := rows.Scan(&offer.Workspace, &offer.Change, &offer.Predecessor, &offer.Task,
			&offer.Repo, &offer.Revision, &offer.TrunkSHA, &offer.DerivedRevision); err != nil {
			return nil, err
		}
		offers = append(offers, offer)
	}
	return offers, rows.Err()
}
