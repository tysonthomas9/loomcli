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

func RestackOfferWithPublish(ctx context.Context, offer journal.RestackOffer,
	publishChange func(context.Context, string, string, string) error) (int, error) {
	revision, _, err := restackOffer(ctx, offer, publishChange)
	return revision, err
}

func RestackOfferWithPaths(ctx context.Context, offer journal.RestackOffer) (int, []string, error) {
	return restackOffer(ctx, offer, nil)
}

func restackOffer(ctx context.Context, offer journal.RestackOffer,
	publishChange func(context.Context, string, string, string) error) (int, []string, error) {
	store, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = store.Close() }()
	revision, err := store.SourceRevision(ctx, offer.Workspace, offer.Change)
	if err != nil {
		return 0, nil, err
	}
	mode, err := store.DeliveryMode(ctx, offer.Workspace)
	if err != nil {
		return 0, nil, err
	}
	if mode != "trunk" && revision > offer.Revision {
		return revision, nil, nil
	}
	area, err := store.WorkingAreaForAppliedChange(ctx, offer.Workspace, offer.Change, offer.Repo)
	if err != nil {
		return 0, nil, fmt.Errorf("find dependent working area: %w", err)
	}
	if revision <= offer.Revision {
		result, restackErr := RestackLocal(ctx, area.Path, offer.TrunkSHA, nil,
			"landing-restack:"+offer.Workspace+":"+offer.Change+":"+offer.Predecessor)
		if restackErr != nil {
			return 0, result.Paths, restackErr
		}
		revision, err = store.SourceRevision(ctx, offer.Workspace, offer.Change)
		if err != nil {
			return 0, nil, err
		}
	}
	if mode == "trunk" {
		if publishChange == nil {
			return 0, nil, errors.New("trunk restack requires a publisher")
		}
		if err := publishChange(ctx, offer.Workspace, area.Lead, offer.Change); err != nil &&
			!errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) {
			return 0, nil, err
		}
	}
	return revision, nil, nil
}
