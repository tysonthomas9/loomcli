package pull

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
)

func Recover(ctx context.Context) error {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	plans, err := store.OpenPullPlans(ctx)
	if err != nil {
		return err
	}
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
	// A failed plan holds back only its own lead; later plans for that lead wait.
	var failures []error
	blocked := map[string]bool{}
	for _, plan := range plans {
		key := plan.Workspace + "\x00" + plan.Lead
		if blocked[key] {
			continue
		}
		if err := recoverPlan(ctx, store, options, plan); err != nil {
			blocked[key] = true
			failures = append(failures, &PlanError{Workspace: plan.Workspace, Lead: plan.Lead, Err: err})
		}
	}
	return errors.Join(failures...)
}

// PlanError is a pull plan recovery could not finish. Apply recovery for the
// same lead waits for it; other leads continue.
type PlanError struct {
	Workspace, Lead string
	Err             error
}

func (e *PlanError) Error() string {
	return fmt.Sprintf("recover pull for %s/%s: %v", e.Workspace, e.Lead, e.Err)
}

func (e *PlanError) Unwrap() error { return e.Err }

func recoverPlan(ctx context.Context, store *journal.SQLite, options gitexec.Options, plan journal.PullPlan) error {
	path, err := pullAreaPath(ctx, store, plan)
	if err != nil {
		return err
	}
	repo, err := pool.New(store, options).Admit(ctx, path)
	if err != nil {
		return fmt.Errorf("open pull working area: %w", err)
	}
	runner, err := gitexec.New(path, options)
	if err != nil {
		return err
	}
	if plan.Phase != "" && plan.Phase != "not_applied" && plan.Phase != "done" {
		if err := apply.New(store, repo, runner).ReconcileRequest(ctx, plan.Workspace, plan.Lead, plan.RequestID); err != nil {
			return err
		}
		pending, err := store.PendingPullPlans(ctx, plan.Workspace, plan.Lead)
		if err != nil {
			return err
		}
		found := false
		for _, current := range pending {
			if current.RequestID == plan.RequestID {
				plan = current
				found = true
				break
			}
		}
		if !found {
			return loomgit.NewError(loomgit.AttentionRequired, "pull plan disappeared during recovery", nil)
		}
	}
	return repo.WithLock(ctx, func(ctx context.Context) error {
		switch plan.Phase {
		case "done":
			return store.CompletePull(ctx, plan.RequestID, plan.Workspace, plan.Lead, plan.Repo, plan.BaseSHA, plan.Layers)
		case "", "not_applied":
			return store.DiscardPullPlan(ctx, plan.RequestID)
		default:
			return loomgit.NewError(loomgit.AttentionRequired, "pull swap remains incomplete", nil)
		}
	})
}

func pullAreaPath(ctx context.Context, store *journal.SQLite, plan journal.PullPlan) (string, error) {
	areas, err := store.WorkingAreas(ctx, plan.Workspace, plan.Lead)
	if err != nil {
		return "", err
	}
	path := ""
	for _, area := range areas {
		if area.Repo != plan.Repo {
			continue
		}
		if path != "" {
			return "", loomgit.NewError(loomgit.AttentionRequired, "pull has multiple working areas for repo", nil)
		}
		path = area.Path
	}
	if path == "" {
		return "", loomgit.NewError(loomgit.AttentionRequired, "pull working area is unavailable", nil)
	}
	return path, nil
}
