package publish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type nativeMergeStore interface {
	Store
	BeginNativeMerge(context.Context, journal.NativeMerge) error
	OpenNativeMerges(context.Context) ([]journal.NativeMerge, error)
	AdvanceNativeMerge(context.Context, journal.NativeMerge, string, string, int) error
	BlockNativeMerge(context.Context, journal.NativeMerge, string) error
	LandingStatus(context.Context, string, string) (journal.LandingStatus, error)
	StackBackend(context.Context, string, string) (string, error)
}

type nativeMergeForge interface {
	PullByNumber(context.Context, string, string, int) (stackpublish.PR, error)
	MergeNativePull(context.Context, string, string, int, string) error
}

func beginNativeMerge(ctx context.Context, store Store, request StackRequest, target string) error {
	mergeStore, ok := store.(nativeMergeStore)
	if !ok {
		return errors.New("store cannot journal native merges")
	}
	if request.Workspace == "" || request.StackID == "" || len(request.Changes) == 0 {
		return errors.New("workspace, stack and changes are required")
	}
	mode, err := store.DeliveryMode(ctx, request.Workspace)
	if err != nil {
		return err
	}
	if mode != "stack" {
		return loomgit.NewError(loomgit.ModeMismatch, "merge requires stack delivery mode", nil)
	}
	backend, err := mergeStore.StackBackend(ctx, request.Workspace, request.StackID)
	if err != nil {
		return err
	}
	if backend != "native" {
		return loomgit.NewError(loomgit.ModeMismatch, "stack is not native", nil)
	}
	index := -1
	for current, change := range request.Changes {
		if change == target {
			index = current
		}
		publication, found, err := store.Publication(ctx, request.Workspace, change)
		if err != nil {
			return err
		}
		if !found || publication.StackID != request.StackID || publication.Phase != "done" {
			return loomgit.NewError(loomgit.MergeBlocked, "stack publication is incomplete", nil)
		}
		if current > index && index >= 0 {
			continue
		}
		if err := requireNativeVerdict(ctx, store, publication); err != nil {
			return err
		}
	}
	if index < 0 {
		return loomgit.NewError(loomgit.Stale, "merge target is not in stack", nil)
	}
	return mergeStore.BeginNativeMerge(ctx, journal.NativeMerge{
		Workspace: request.Workspace, StackID: request.StackID, Target: target, Changes: request.Changes[:index+1],
	})
}

func requireNativeVerdict(ctx context.Context, store Store, publication journal.Publication) error {
	revision, err := store.RevisionByHead(ctx, publication.Workspace, publication.Change, publication.Head)
	if err != nil {
		return loomgit.NewError(loomgit.ReviewRequired, "native layer has no reviewed revision", err)
	}
	return review.RequireVerdict(ctx, store, publication.Workspace, publication.Change,
		revision.Number, publication.Head, "publish", "")
}

func ReconcileNativeMerges(ctx context.Context, store nativeMergeStore, forge nativeMergeForge) error {
	merges, err := store.OpenNativeMerges(ctx)
	if err != nil {
		return err
	}
	for _, merge := range merges {
		if err := reconcileNativeMerge(ctx, store, forge, merge); err != nil {
			var coded *loomgit.Error
			if errors.As(err, &coded) && (coded.Kind == loomgit.Stale || coded.Kind == loomgit.MergeBlocked || coded.Kind == loomgit.ReviewRequired) {
				if blockErr := store.BlockNativeMerge(ctx, merge, err.Error()); blockErr != nil {
					return blockErr
				}
			}
			return err
		}
	}
	return nil
}

func ReconcileNativeAt(ctx context.Context) error {
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
	merges, err := store.OpenNativeMerges(ctx)
	if err != nil || len(merges) == 0 {
		return err
	}
	token := githubtoken.GitHub(ctx)
	if token == "" {
		return errors.New("GitHub host credential unavailable")
	}
	return ReconcileNativeMerges(ctx, store, stackpublish.NewConfiguredGitHubForge(token))
}

func reconcileNativeMerge(ctx context.Context, store nativeMergeStore, forge nativeMergeForge, merge journal.NativeMerge) error {
	if merge.Index < 0 || merge.Index >= len(merge.Changes) {
		return errors.New("native merge cursor is invalid")
	}
	change := merge.Changes[merge.Index]
	publication, found, err := store.Publication(ctx, merge.Workspace, change)
	if err != nil {
		return err
	}
	if !found || publication.StackID != merge.StackID || publication.PRNumber == 0 {
		return loomgit.NewError(loomgit.MergeBlocked, "native layer publication is missing", nil)
	}
	parts := strings.Split(publication.Slug, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("native repository slug is invalid")
	}
	pr, err := forge.PullByNumber(ctx, parts[0], parts[1], publication.PRNumber)
	if err != nil {
		return err
	}
	if merge.Phase == "dispatching" {
		if !pr.Merged {
			return loomgit.NewError(loomgit.AttentionRequired, "native merge submission outcome is unknown", nil)
		}
		if err := store.AdvanceNativeMerge(ctx, merge, "sent", merge.Head, merge.Index); err != nil {
			return err
		}
		merge.Phase = "sent"
	}
	if merge.Phase == "sent" {
		return reconcileNativeSent(ctx, store, merge, publication, pr)
	}
	if merge.Phase != "ready" {
		return fmt.Errorf("native merge phase %q is invalid", merge.Phase)
	}
	return submitNativeMerge(ctx, store, forge, merge, publication, pr, parts)
}

func submitNativeMerge(ctx context.Context, store nativeMergeStore, forge nativeMergeForge,
	merge journal.NativeMerge, publication journal.Publication, pr stackpublish.PR, parts []string) error {
	if pr.Merged || pr.State != "open" || pr.HeadSHA != publication.Head || pr.Head != publication.Branch {
		return loomgit.NewError(loomgit.Stale, "native PR moved before merge", nil)
	}
	if err := requireNativeVerdict(ctx, store, publication); err != nil {
		return err
	}
	if err := store.AdvanceNativeMerge(ctx, merge, "dispatching", pr.HeadSHA, merge.Index); err != nil {
		return err
	}
	if err := forge.MergeNativePull(ctx, parts[0], parts[1], publication.PRNumber, pr.HeadSHA); err != nil {
		return err
	}
	merge.Phase, merge.Head = "dispatching", pr.HeadSHA
	return store.AdvanceNativeMerge(ctx, merge, "sent", pr.HeadSHA, merge.Index)
}

func reconcileNativeSent(ctx context.Context, store nativeMergeStore, merge journal.NativeMerge,
	publication journal.Publication, pr stackpublish.PR) error {
	if pr.HeadSHA != merge.Head {
		return loomgit.NewError(loomgit.Stale, "submitted native PR head changed", nil)
	}
	if !pr.Merged {
		if pr.State != "open" {
			return loomgit.NewError(loomgit.MergeBlocked, "submitted native merge changed or closed", nil)
		}
		return nil
	}
	status, err := store.LandingStatus(ctx, merge.Workspace, publication.Change)
	if err != nil {
		return err
	}
	if status.State != "landed" {
		return nil
	}
	index := merge.Index + 1
	phase := "ready"
	if index == len(merge.Changes) {
		phase = "done"
	}
	return store.AdvanceNativeMerge(ctx, merge, phase, "", index)
}
