package publish

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/landing"
)

func prepareMergeRestack(ctx context.Context, store *journal.SQLite, offer journal.RestackOffer,
	publication journal.Publication, forge landing.Forge, publisher Forge) error {
	merge, err := store.LoomMerge(ctx, offer.Workspace, publication.StackID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if merge.Phase == "done" || merge.Phase == "blocked" || merge.Index >= len(merge.Layers) ||
		merge.Layers[merge.Index].Change != offer.Predecessor {
		return nil
	}
	predecessor, found, err := store.Publication(ctx, offer.Workspace, offer.Predecessor)
	if err != nil {
		return err
	}
	if !found || predecessor.StackID != publication.StackID {
		return loomgit.NewError(loomgit.MergeBlocked, "landed predecessor is outside the merge stack", nil)
	}
	owner, repo, ok := strings.Cut(publication.Slug, "/")
	if !ok || owner == "" || repo == "" {
		return loomgit.NewError(loomgit.MergeBlocked, "merge repository slug is invalid", nil)
	}
	pr, err := forge.PullByNumber(ctx, owner, repo, publication.PRNumber)
	if err != nil {
		return err
	}
	if pr.State != "open" || pr.Merged || pr.Head != publication.Branch {
		return loomgit.NewError(loomgit.MergeBlocked, "dependent PR must be open before restack", nil)
	}
	if pr.HeadSHA != publication.Head {
		return loomgit.NewError(loomgit.Stale, "dependent PR head moved before restack", nil)
	}
	if pr.Base != predecessor.Trunk {
		return publisher.UpdatePRBase(ctx, owner, repo, publication.PRNumber, predecessor.Trunk)
	}
	return nil
}
