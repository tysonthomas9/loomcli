package pull

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func RestackOffer(ctx context.Context, offer journal.RestackOffer) (int, error) {
	return RestackOfferWithPublish(ctx, offer, nil)
}

func RestackOfferWithPublish(ctx context.Context, offer journal.RestackOffer, publishChange func(context.Context, string, string, string) error) (int, error) {
	store, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = store.Close() }()
	revision, err := store.SourceRevision(ctx, offer.Workspace, offer.Change)
	if err != nil {
		return 0, err
	}
	mode, err := store.DeliveryMode(ctx, offer.Workspace)
	if err != nil {
		return 0, err
	}
	if mode != "trunk" && revision > offer.Revision {
		return revision, nil
	}
	area, err := store.WorkingAreaForAppliedChange(ctx, offer.Workspace, offer.Change, offer.Repo)
	if err != nil {
		return 0, fmt.Errorf("find dependent working area: %w", err)
	}
	if revision <= offer.Revision {
		if _, err := RestackLocal(ctx, area.Path, offer.TrunkSHA, nil,
			"landing-restack:"+offer.Workspace+":"+offer.Change+":"+offer.Predecessor); err != nil {
			return 0, err
		}
		revision, err = store.SourceRevision(ctx, offer.Workspace, offer.Change)
		if err != nil {
			return 0, err
		}
	}
	if mode == "trunk" {
		if publishChange == nil {
			return 0, errors.New("trunk restack requires a publisher")
		}
		if err := publishChange(ctx, offer.Workspace, area.Lead, offer.Change); err != nil &&
			!errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) {
			return 0, err
		}
	}
	return revision, nil
}
