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
	if err != nil {
		return err
	}
	return createDependencyChecks(db)
}

func createDependencyChecks(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS dependency_checks (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, repo TEXT NOT NULL,
		state TEXT NOT NULL, reason TEXT NOT NULL,
		PRIMARY KEY(workspace, change_id)
	);
	CREATE TABLE IF NOT EXISTS dependency_posts (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, sha TEXT NOT NULL,
		state TEXT NOT NULL, reason TEXT NOT NULL,
		PRIMARY KEY(workspace, change_id, sha)
	);
	CREATE TABLE IF NOT EXISTS dependency_apps (
		repo TEXT NOT NULL PRIMARY KEY, app_id INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS dependency_enforcement (
		repo TEXT NOT NULL, branch TEXT NOT NULL, state TEXT NOT NULL, reason TEXT NOT NULL,
		PRIMARY KEY(repo, branch)
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
	observation, found, err := s.ProviderObservation(ctx, workspace, change)
	if err != nil {
		return LandingStatus{}, err
	}
	if found && (observation.State == "diverged" || observation.State == "dependency_abandoned" || observation.State == "closed") {
		return LandingStatus{State: observation.State}, nil
	}
	var rule string
	err = s.db.QueryRowContext(ctx, `SELECT rule FROM landed_changes WHERE workspace=? AND change_id=?`, workspace, change).Scan(&rule)
	if err == nil {
		return LandingStatus{State: "landed", Rule: rule}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return LandingStatus{}, err
	}
	var existing int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM merged_changes WHERE workspace=? AND change_id=?`, workspace, change).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		offers, err := s.RestackOffers(ctx, workspace, change)
		if err != nil {
			return LandingStatus{}, err
		}
		if len(offers) > 0 {
			return LandingStatus{State: "restack_available", Offer: &offers[0]}, nil
		}
		err = s.db.QueryRowContext(ctx, `SELECT 1 FROM change_publications WHERE workspace=? AND change_id=?`, workspace, change).Scan(&existing)
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
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,change_id,repo,branch,trunk,slug,head_sha,feature_flag,phase,pr_number,pr_url,stack_id,prior_sha
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
			&publication.Phase, &publication.PRNumber, &publication.PRURL, &publication.StackID, &publication.Prior); err != nil {
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

// DependencyCheck is the latest loom/dependencies result for a change whose
// task depends on changes in other repositories.
type DependencyCheck struct {
	Workspace, Change, Repo, State, Reason string
}

// DependencyEnforcement records whether a repository requires loom/dependencies.
type DependencyEnforcement struct {
	Repo, Branch, State, Reason string
}

// TaskChanges returns every change a task owns, keyed by repository name.
func (s *SQLite) TaskChanges(ctx context.Context, workspace, task string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT repo,change_id FROM driver_changes WHERE workspace=? AND task_id=?`, workspace, task)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	changes := map[string]string{}
	for rows.Next() {
		var repo, change string
		if err := rows.Scan(&repo, &change); err != nil {
			return nil, err
		}
		changes[repo] = change
	}
	return changes, rows.Err()
}

func (s *SQLite) RecordDependencyCheck(ctx context.Context, check DependencyCheck) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO dependency_checks(workspace,change_id,repo,state,reason) VALUES (?,?,?,?,?)
		ON CONFLICT(workspace,change_id) DO UPDATE SET repo=excluded.repo,state=excluded.state,reason=excluded.reason`,
		check.Workspace, check.Change, check.Repo, check.State, check.Reason)
	return err
}

// DependencyPosted reports whether exactly this result is already on the provider commit.
func (s *SQLite) DependencyPosted(ctx context.Context, workspace, change, sha, state, reason string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM dependency_posts WHERE workspace=? AND change_id=? AND sha=? AND state=? AND reason=?`,
		workspace, change, sha, state, reason).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *SQLite) RecordDependencyPost(ctx context.Context, workspace, change, sha, state, reason string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO dependency_posts(workspace,change_id,sha,state,reason) VALUES (?,?,?,?,?)
		ON CONFLICT(workspace,change_id,sha) DO UPDATE SET state=excluded.state,reason=excluded.reason`,
		workspace, change, sha, state, reason)
	return err
}

// RecordDependencyApp remembers the GitHub App Loom posts loom/dependencies as in a repository.
func (s *SQLite) RecordDependencyApp(ctx context.Context, repo string, app int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO dependency_apps(repo,app_id) VALUES (?,?)
		ON CONFLICT(repo) DO UPDATE SET app_id=excluded.app_id`, repo, app)
	return err
}

// DependencyApp returns the app Loom posts as in a repository, or 0 when unknown.
func (s *SQLite) DependencyApp(ctx context.Context, repo string) (int64, error) {
	var app int64
	err := s.db.QueryRowContext(ctx, `SELECT app_id FROM dependency_apps WHERE repo=?`, repo).Scan(&app)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return app, err
}

func (s *SQLite) RecordDependencyEnforcement(ctx context.Context, enforcement DependencyEnforcement) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO dependency_enforcement(repo,branch,state,reason) VALUES (?,?,?,?)
		ON CONFLICT(repo,branch) DO UPDATE SET state=excluded.state,reason=excluded.reason`,
		enforcement.Repo, enforcement.Branch, enforcement.State, enforcement.Reason)
	return err
}

// DependencyChecks reads every recorded check and enforcement row. A journal
// written before dependency checks existed has none.
func (s *SQLite) DependencyChecks(ctx context.Context) ([]DependencyCheck, []DependencyEnforcement, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='dependency_enforcement'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,change_id,repo,state,reason FROM dependency_checks ORDER BY workspace,change_id`)
	if err != nil {
		return nil, nil, err
	}
	var checks []DependencyCheck
	for rows.Next() {
		var check DependencyCheck
		if err := rows.Scan(&check.Workspace, &check.Change, &check.Repo, &check.State, &check.Reason); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		checks = append(checks, check)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT repo,branch,state,reason FROM dependency_enforcement ORDER BY repo,branch`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var enforcement []DependencyEnforcement
	for rows.Next() {
		var row DependencyEnforcement
		if err := rows.Scan(&row.Repo, &row.Branch, &row.State, &row.Reason); err != nil {
			return nil, nil, err
		}
		enforcement = append(enforcement, row)
	}
	return checks, enforcement, rows.Err()
}
