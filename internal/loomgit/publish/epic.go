package publish

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/stacklock"
)

func CheckEpicPRDelivery(ctx context.Context, workspace, repoURL string) error {
	if _, err := GitHubSlug(repoURL); err != nil {
		return fmt.Errorf("epic PR delivery requires a GitHub origin: %w", err)
	}
	mode, err := DeliveryModeLocal(ctx, workspace)
	if err != nil {
		return err
	}
	if mode != "stack" {
		return loomgit.NewError(loomgit.ModeMismatch, "epic PR delivery requires stack mode", nil)
	}
	if githubtoken.GitHub(ctx) == "" {
		return errors.New("GitHub host credential unavailable")
	}
	return nil
}

func ReconcileEpicLead(ctx context.Context, workspace, lead string) error {
	forge, token, slug := localPublishProvider()
	return reconcileEpicPublicationsAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"),
		workspace, lead, forge, token, slug)
}

func ReconcileEpicPublicationsAt(ctx context.Context, path string) error {
	forge, token, slug := localPublishProvider()
	return reconcileEpicPublicationsAt(ctx, path, "", "", forge, token, slug)
}

func ReconcileEpicPublicationsWithProvider(ctx context.Context, forge Forge, token, slug string) error {
	return reconcileEpicPublicationsAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"),
		"", "", forge, token, slug)
}

func ReconcileEpicLeadWithProvider(ctx context.Context, workspace, lead string, forge Forge, token, slug string) error {
	return reconcileEpicPublicationsAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"),
		workspace, lead, forge, token, slug)
}

func reconcileEpicPublicationsAt(ctx context.Context, path, workspace, lead string, forge Forge, token, slug string) error {
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	intents, err := store.PendingEpicPublications(ctx)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		if (workspace != "" && intent.Workspace != workspace) || (lead != "" && intent.Lead != lead) {
			continue
		}
		if err := reconcileEpicPublication(ctx, store, intent, forge, token, slug); err != nil {
			return err
		}
	}
	return nil
}

func reconcileEpicPublication(ctx context.Context, store *journal.SQLite, intent journal.EpicPublication,
	forge Forge, token, slug string) error {
	applied, err := store.AppliedLog(ctx, intent.Workspace, intent.Lead)
	if err != nil {
		return err
	}
	for _, change := range intent.Changes {
		found := false
		for _, layer := range applied {
			if layer.Change == change {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	ordered := make([]string, 0, len(intent.Changes))
	for _, layer := range applied {
		if slices.Contains(intent.Changes, layer.Change) && !slices.Contains(ordered, layer.Change) {
			ordered = append(ordered, layer.Change)
		}
	}
	if _, err := PublishStackWithProvider(stacklock.ForEpicReconcile(ctx), intent.Workspace, LeadStackID(intent.Lead),
		intent.Lead, ordered, forge, token, slug); err != nil {
		return err
	}
	return store.CompleteEpicPublication(ctx, intent.Workspace, intent.RunID)
}
