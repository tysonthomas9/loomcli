package pull

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
)

func PullLocal(ctx context.Context, path, remote, sourceBranch, requestID string) (PullResult, error) {
	return withLocalService(ctx, path, func(service *Service, area journal.WorkingArea) (PullResult, error) {
		return service.Pull(ctx, PullRequest{Workspace: area.Workspace, Lead: area.Lead,
			Repo: area.Repo, Remote: remote, SourceBranch: sourceBranch, RequestID: requestID})
	})
}

func RestackLocal(ctx context.Context, path, baseSHA string, order []string, requestID string) (PullResult, error) {
	return withLocalService(ctx, path, func(service *Service, area journal.WorkingArea) (PullResult, error) {
		return service.Restack(ctx, RestackRequest{Workspace: area.Workspace, Lead: area.Lead,
			Repo: area.Repo, BaseSHA: baseSHA, Order: order, RequestID: requestID})
	})
}

func withLocalService(ctx context.Context, path string, action func(*Service, journal.WorkingArea) (PullResult, error)) (PullResult, error) {
	journalPath := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(journalPath); err != nil {
		return PullResult{}, loomgit.NewError(loomgit.WorkspaceUnsupported, "revision journal is unavailable", err)
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		return PullResult{}, err
	}
	defer func() { _ = store.Close() }()
	area, err := store.WorkingAreaByPath(ctx, filepath.Clean(path))
	if err != nil {
		return PullResult{}, loomgit.NewError(loomgit.WorkspaceUnsupported, "registered working area is unavailable", err)
	}
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
	repo, err := pool.New(store, options).Admit(ctx, area.Path)
	if err != nil {
		return PullResult{}, err
	}
	runner, err := gitexec.New(area.Path, options)
	if err != nil {
		return PullResult{}, err
	}
	return action(New(store, repo, runner), area)
}
