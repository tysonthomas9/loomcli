package pull

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func RestackOffer(ctx context.Context, offer journal.RestackOffer) (int, error) {
	store, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = store.Close() }()
	revision, err := store.SourceRevision(ctx, offer.Workspace, offer.Change)
	if err != nil {
		return 0, err
	}
	if revision > offer.Revision {
		return revision, nil
	}
	area, err := store.WorkingAreaForAppliedChange(ctx, offer.Workspace, offer.Change, offer.Repo)
	if err != nil {
		return 0, fmt.Errorf("find dependent working area: %w", err)
	}
	if _, err := RestackLocal(ctx, area.Path, offer.TrunkSHA, nil,
		"landing-restack:"+offer.Workspace+":"+offer.Change+":"+offer.Predecessor); err != nil {
		return 0, err
	}
	return store.SourceRevision(ctx, offer.Workspace, offer.Change)
}
