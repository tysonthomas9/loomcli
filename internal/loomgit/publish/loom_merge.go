package publish

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/loomgit/stacklock"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type loomMergeForge interface {
	PullByNumber(context.Context, string, string, int) (stackpublish.PR, error)
	PRStatuses(context.Context, string, string, string) (map[string]stackpublish.PRStatus, error)
	QueuedPRNumbers(context.Context, string, string) (map[int]bool, error)
	FailedLoomChecks(context.Context, string, string, string) ([]string, error)
	MergeLoomPull(context.Context, string, string, int, string) (stackpublish.LoomMergeResult, error)
	LoomMergeStatus(context.Context, string, string, int, string) (stackpublish.LoomMergeResult, error)
	DeleteLoomBranch(context.Context, string, string, string) error
}

func beginLoomMerge(ctx context.Context, store Store, request StackRequest, target string) error {
	if err := requireMergeAuthority(ctx, request, target); err != nil {
		return err
	}
	if request.StackID == "" || len(request.Changes) == 0 || target == "" {
		return errors.New("stack, changes and target are required")
	}
	mergeStore, ok := store.(*journal.SQLite)
	if !ok {
		return errors.New("loom merge requires a durable journal")
	}
	return stacklock.With(ctx, request.Workspace, request.StackID, func(lockedCtx context.Context) error {
		return recordLoomMerge(lockedCtx, mergeStore, request, target)
	})
}

func recordLoomMerge(ctx context.Context, store *journal.SQLite, request StackRequest, target string) error {
	mode, err := store.DeliveryMode(ctx, request.Workspace)
	if err != nil {
		return err
	}
	backend, err := store.StackBackend(ctx, request.Workspace, request.StackID)
	if err != nil {
		return err
	}
	if mode != "stack" || backend != "loom" {
		return loomgit.NewError(loomgit.ModeMismatch, "Loom merge requires a recorded Loom stack", nil)
	}
	resumed, err := resumeLoomMerge(ctx, store, request, target)
	if err != nil || resumed {
		return err
	}
	forge, ok := request.forge.(loomMergeForge)
	if !ok {
		return errors.New("forge cannot merge a Loom stack")
	}
	if err := validateLoomMergeOrder(ctx, store, request); err != nil {
		return err
	}
	layers, err := confirmedLoomLayers(ctx, store, forge, request, target)
	if err != nil {
		return err
	}
	merge := journal.LoomMerge{Workspace: request.Workspace, StackID: request.StackID, Target: target,
		RequestID: fmt.Sprintf("loom-merge:%s:%s:%s", request.Workspace, request.StackID, target), Layers: layers}
	if policy, ok := request.MergeAuthority.(whenGreenMerge); ok {
		merge.RequestID = "lead-" + strings.TrimPrefix(merge.RequestID, "loom-")
		merge.Authority, merge.PolicySetBy = leadMergeAuthority, policy.SetBy
	}
	_, err = store.BeginLoomMerge(ctx, merge)
	return err
}

func validateLoomMergeOrder(ctx context.Context, store *journal.SQLite, request StackRequest) error {
	area, err := gitexec.New(request.WorkingArea, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return err
	}
	applied, err := apply.New(store, nil, area).AppliedLog(ctx, request.Workspace, request.Lead)
	if err != nil {
		return err
	}
	return validateStackLayers(ctx, store, request, applied)
}

func resumeLoomMerge(ctx context.Context, store *journal.SQLite, request StackRequest, target string) (bool, error) {
	existing, err := store.LoomMerge(ctx, request.Workspace, request.StackID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if existing.Target != target || len(existing.Layers) != len(request.Changes) {
		if existing.Phase == "done" || existing.Phase == "blocked" {
			return false, nil
		}
		return false, loomgit.NewError(loomgit.Stale, "merge request differs from active intent", nil)
	}
	for index, change := range request.Changes {
		if existing.Layers[index].Change != change {
			if existing.Phase == "done" || existing.Phase == "blocked" {
				return false, nil
			}
			return false, loomgit.NewError(loomgit.Stale, "merge order differs from active intent", nil)
		}
	}
	return true, nil
}

func confirmedLoomLayers(ctx context.Context, store *journal.SQLite, forge loomMergeForge,
	request StackRequest, target string) ([]journal.LoomMergeLayer, error) {
	owner, repo, ok := strings.Cut(request.slug, "/")
	if !ok || owner == "" || repo == "" {
		return nil, errors.New("merge repository slug is invalid")
	}
	layers := make([]journal.LoomMergeLayer, 0, len(request.Changes))
	foundTarget := false
	for _, change := range request.Changes {
		layer, err := confirmedLoomLayer(ctx, store, forge, request, owner, repo, change)
		if err != nil {
			return nil, err
		}
		layers = append(layers, layer)
		foundTarget = foundTarget || change == target
	}
	if !foundTarget {
		return nil, errors.New("merge target is not in the stack")
	}
	return layers, nil
}

func confirmedLoomLayer(ctx context.Context, store *journal.SQLite, forge loomMergeForge,
	request StackRequest, owner, repo, change string) (journal.LoomMergeLayer, error) {
	publication, found, err := store.Publication(ctx, request.Workspace, change)
	if err != nil || !found || publication.StackID != request.StackID || publication.Phase != "done" ||
		publication.PRNumber == 0 || publication.Repo != request.Repo || publication.Slug != request.slug {
		return journal.LoomMergeLayer{}, errors.New("merge stack has an unpublished layer")
	}
	pr, err := forge.PullByNumber(ctx, owner, repo, publication.PRNumber)
	if err != nil {
		return journal.LoomMergeLayer{}, err
	}
	if pr.State != "open" || pr.Merged || pr.Head != publication.Branch || pr.HeadSHA != publication.Head || pr.Base != publication.Trunk {
		return journal.LoomMergeLayer{}, loomgit.NewError(loomgit.Stale, "merge confirmation head differs from provider", nil)
	}
	revision, err := store.RevisionByHead(ctx, request.Workspace, change, publication.Head)
	if err != nil {
		return journal.LoomMergeLayer{}, err
	}
	if err := review.RequireVerdict(ctx, store, request.Workspace, change, revision.Number, publication.Head, "publish", ""); err != nil {
		return journal.LoomMergeLayer{}, err
	}
	return journal.LoomMergeLayer{Change: change, Head: publication.Head, Revision: revision.Number}, nil
}
