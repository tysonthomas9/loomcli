package apply

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
)

func ApplyLocal(ctx context.Context, request Request) (Result, error) {
	if request.Lead == "" {
		request.Lead = defaultLead(request.Workspace)
	}
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); err != nil {
		return Result{}, loomgit.NewError(loomgit.WorkspaceUnsupported, "revision journal is unavailable", err)
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = store.Close() }()
	repoName, err := store.RepoForChange(ctx, request.Workspace, request.Change)
	if err != nil {
		return Result{}, fmt.Errorf("find repo for change: %w", err)
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return Result{}, err
	}
	_, workspace, found := config.WorkspaceByID(cfg, request.Workspace)
	if !found {
		return Result{}, loomgit.NewError(loomgit.WorkspaceUnsupported, "workspace is unavailable", nil)
	}
	for _, candidate := range workspace.Repos {
		if candidate.Name != repoName {
			continue
		}
		path := candidate.ResolveAbsPath(workspace.Path)
		options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
		repo, err := pool.New(store, options).Admit(ctx, path)
		if err != nil {
			return Result{}, err
		}
		runner, err := gitexec.New(path, options)
		if err != nil {
			return Result{}, err
		}
		return New(store, repo, runner).Apply(ctx, request)
	}
	return Result{}, loomgit.NewError(loomgit.RepoSelectionRequired, "change repo is not in the workspace", nil)
}

func defaultLead(string) string { return "lead" }
