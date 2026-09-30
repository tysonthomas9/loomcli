package publish

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/landing"
	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
	"github.com/tysonthomas9/loomcli/internal/loomgit/stacklock"
)

func RestackOffer(ctx context.Context, offer journal.RestackOffer, forge landing.Forge) (int, error) {
	store, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = store.Close() }()
	publication, found, err := store.Publication(ctx, offer.Workspace, offer.Change)
	if err != nil {
		return 0, err
	}
	if !found || publication.StackID == "" {
		return pull.RestackOffer(ctx, offer)
	}
	if publication.Phase != "done" || publication.Repo == "" {
		return 0, loomgit.NewError(loomgit.AttentionRequired, "stack publication is incomplete", nil)
	}
	backend, err := store.StackBackend(ctx, offer.Workspace, publication.StackID)
	if err != nil || backend != "loom" {
		return 0, loomgit.NewError(loomgit.AttentionRequired, "recorded stack backend cannot restack this offer", err)
	}
	publisher, ok := forge.(Forge)
	if !ok {
		return 0, errors.New("landing forge cannot publish a restacked stack")
	}
	var revision int
	err = stacklock.With(ctx, offer.Workspace, publication.StackID, func(lockedCtx context.Context) error {
		var restackErr error
		revision, restackErr = pull.RestackOffer(lockedCtx, offer)
		if restackErr != nil {
			var coded *loomgit.Error
			if errors.As(restackErr, &coded) && coded.Code() == string(loomgit.Conflict) {
				return loomgit.NewError(loomgit.RestackConflict, restackErr.Error(), restackErr)
			}
			return restackErr
		}
		return publishRestackedOffer(lockedCtx, store, offer, publication, publisher)
	})
	return revision, err
}

func publishRestackedOffer(ctx context.Context, store *journal.SQLite, offer journal.RestackOffer,
	publication journal.Publication, forge Forge) error {
	area, err := store.WorkingAreaForAppliedChange(ctx, offer.Workspace, offer.Change, offer.Repo)
	if err != nil {
		return fmt.Errorf("find restacked working area: %w", err)
	}
	runner, err := gitexec.New(area.Path, gitexec.Options{})
	if err != nil {
		return err
	}
	layers, err := apply.New(store, nil, runner).AppliedLog(ctx, offer.Workspace, area.Lead)
	if err != nil {
		return err
	}
	changes := make([]string, 0, len(layers))
	for _, layer := range layers {
		prior, found, err := store.Publication(ctx, offer.Workspace, layer.Change)
		if err != nil {
			return err
		}
		if !found || prior.StackID != publication.StackID {
			return loomgit.NewError(loomgit.StackNotLinear, "restacked layer is outside the published stack", nil)
		}
		changes = append(changes, layer.Change)
	}
	if len(changes) == 0 {
		return errors.New("restacked stack has no remaining layers")
	}
	_, err = publishStack(ctx, store, StackRequest{Request: Request{
		Workspace: offer.Workspace, Lead: area.Lead, Repo: publication.Repo,
		WorkingArea: area.Path, BaseSHA: area.BaseSHA, RepoName: area.Repo,
		forge: forge, token: githubtoken.GitHub(ctx), slug: publication.Slug,
	}, StackID: publication.StackID, Changes: changes})
	return err
}
