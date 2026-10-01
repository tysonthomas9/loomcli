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
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
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
		return pull.RestackOfferWithPublish(ctx, offer, func(ctx context.Context, workspace, lead, change string) error {
			_, err := PublishLocal(ctx, workspace, lead, change)
			return err
		})
	}
	if publication.Phase != "done" || publication.Repo == "" {
		return 0, loomgit.NewError(loomgit.AttentionRequired, "stack publication is incomplete", nil)
	}
	backend, err := store.StackBackend(ctx, offer.Workspace, publication.StackID)
	if err != nil || (backend != "loom" && backend != "native") {
		return 0, loomgit.NewError(loomgit.AttentionRequired, "recorded stack backend cannot restack this offer", err)
	}
	publisher, ok := forge.(Forge)
	if !ok {
		return 0, errors.New("landing forge cannot publish a restacked stack")
	}
	var revision int
	err = stacklock.With(ctx, offer.Workspace, publication.StackID, func(lockedCtx context.Context) error {
		if backend == "native" {
			return adoptNativeRestack(lockedCtx, store, offer, publication, forge, &revision)
		}
		var paths []string
		var restackErr error
		revision, paths, restackErr = pull.RestackOfferWithPaths(lockedCtx, offer)
		if restackErr != nil {
			return recordRestackError(lockedCtx, store, offer, publication.StackID, paths, fmt.Errorf("restack published offer: %w", restackErr))
		}
		if err := store.ClearStackAttention(lockedCtx, offer.Workspace, publication.StackID); err != nil {
			return err
		}
		if err := prepareMergeRestack(lockedCtx, store, offer, publication, forge, publisher); err != nil {
			return err
		}
		return publishRestackedOffer(lockedCtx, store, offer, publication, publisher)
	})
	return revision, err
}

func recordRestackError(ctx context.Context, store *journal.SQLite, offer journal.RestackOffer,
	stackID string, paths []string, cause error) error {
	var coded *loomgit.Error
	if !errors.As(cause, &coded) {
		return cause
	}
	status := ""
	switch coded.Code() {
	case string(loomgit.Conflict):
		status = "restack_conflict"
	case string(loomgit.SwapHeld):
		status = "swap_held"
	default:
		return cause
	}
	if err := store.RecordStackAttention(ctx, offer, stackID, status, paths); err != nil {
		return errors.Join(cause, err)
	}
	if status == "restack_conflict" {
		return loomgit.NewError(loomgit.RestackConflict, cause.Error(), cause)
	}
	return cause
}

func publishRestackedOffer(ctx context.Context, store *journal.SQLite, offer journal.RestackOffer,
	publication journal.Publication, forge Forge) error {
	area, err := store.WorkingAreaForAppliedChange(ctx, offer.Workspace, offer.Change, offer.Repo)
	if err != nil {
		return fmt.Errorf("find restacked working area: %w", err)
	}
	runner, err := gitexec.New(area.Path, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
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
	if errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) {
		return errors.Join(err, recordRestackReviews(ctx, store, offer, publication.StackID, area.Lead, layers))
	}
	return err
}

func recordRestackReviews(ctx context.Context, store *journal.SQLite, offer journal.RestackOffer,
	stackID, lead string, layers []loomgit.AppliedLayer) error {
	for _, layer := range layers {
		revision, err := store.RevisionByHead(ctx, offer.Workspace, layer.Change, layer.NewTip)
		if err != nil {
			return err
		}
		err = review.RequireVerdict(ctx, store, offer.Workspace, layer.Change, revision.Number, layer.NewTip, "publish", "")
		if err == nil {
			continue
		}
		if !errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) {
			return err
		}
		if err := store.RecordRestackReviewRequired(ctx, offer, stackID, lead, layer.Change, revision.Number); err != nil {
			return err
		}
	}
	return nil
}
