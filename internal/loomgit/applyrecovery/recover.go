package applyrecovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
	"github.com/tysonthomas9/loomcli/internal/loomgit/outbox"
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
	targets, err := store.OpenAppliedTargets(ctx)
	if err != nil {
		return err
	}
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
	for _, target := range targets {
		areas, err := store.WorkingAreas(ctx, target.Workspace, target.Lead)
		if err != nil {
			return err
		}
		if len(areas) != 1 || areas[0].Path == "" {
			return loomgit.NewError(loomgit.AttentionRequired,
				fmt.Sprintf("interrupted apply for %s/%s has no unique working area", target.Workspace, target.Lead), nil)
		}
		area := areas[0]
		repo, err := pool.New(store, options).Admit(ctx, area.Path)
		if err != nil {
			return loomgit.NewError(loomgit.AttentionRequired, "open interrupted apply working area", err)
		}
		runner, err := gitexec.New(area.Path, options)
		if err != nil {
			return loomgit.NewError(loomgit.AttentionRequired, "open interrupted apply Git runner", err)
		}
		if err := apply.New(store, repo, runner).Reconcile(ctx, target.Workspace, target.Lead); err != nil {
			return err
		}
	}
	if err := apply.RecoverPending(ctx, store); err != nil {
		return err
	}
	dir := os.Getenv("LOOM_EVENTS_DIR")
	if dir == "" {
		dir = filepath.Join(config.GetConfigDir(), "events")
	}
	bus := events.NewBus(dir)
	defer func() { _ = bus.Close() }()
	return outbox.EmitPending(ctx, store, bus)
}
