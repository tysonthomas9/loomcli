// Package repo registers the `loom repo` noun-verb commands for
// fleet-db-backed repo CRUD within the active workspace.
package repo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

var (
	repoAddRemote   string
	repoAddBranch   string
	repoAddGroups   []string
	repoAddSourceID string

	repoListJSON bool
	repoShowJSON bool
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage repos within the active workspace",
	Long: `Manage repositories tied to the active workspace.

The active workspace is resolved from the --workspace root flag or
LOOM_WORKSPACE env var. Runtime repo commands do not use
~/.loom/state.json's last_workspace.`,
	GroupID: "workspace",
}

var repoAddCmd = &cobra.Command{
	Use:   "add <NAME> <REMOTE_URL>",
	Short: "Register a repo in the active workspace",
	Args:  cobra.ExactArgs(2),
	RunE:  runRepoAdd,
}

var repoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List repos in the active workspace",
	Args:  cobra.NoArgs,
	RunE:  runRepoList,
}

var repoShowCmd = &cobra.Command{
	Use:   "show <NAME>",
	Short: "Show repo details",
	Args:  cobra.ExactArgs(1),
	RunE:  runRepoShow,
}

var repoRemoveCmd = &cobra.Command{
	Use:   "remove <NAME>",
	Short: "Delete a repo from the active workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runRepoRemove,
}

func init() {
	repoAddCmd.Flags().StringVar(&repoAddRemote, "remote", "", "Git remote name (default: origin)")
	repoAddCmd.Flags().StringVar(&repoAddBranch, "branch", "", "Default branch (default: main)")
	repoAddCmd.Flags().StringSliceVar(&repoAddGroups, "groups", nil, "Logical groups (comma-separated or repeat flag)")
	repoAddCmd.Flags().StringVar(&repoAddSourceID, "source-repo-id", "", "Source repo ID for issue routing (default: NAME)")

	repoListCmd.Flags().BoolVar(&repoListJSON, "json", false, "JSON output")
	repoShowCmd.Flags().BoolVar(&repoShowJSON, "json", false, "JSON output")

	repoCmd.AddCommand(repoAddCmd, repoListCmd, repoShowCmd, repoSetCmd, repoRemoveCmd)
	cli.RegisterCommand(repoCmd)
}

func runRepoAdd(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		localPath, cloned, err := ensureRepoLocalCheckout(ctx, ws, args[0], args[1])
		if err != nil {
			return err
		}
		rollbackClone := func() {
			if cloned && localPath != "" {
				_ = os.RemoveAll(localPath)
			}
		}
		r, err := h.Store.Repos().Create(ctx, store.RepoCreate{
			WorkspaceKey:  ws,
			Name:          args[0],
			RemoteURL:     args[1],
			Remote:        repoAddRemote,
			DefaultBranch: repoAddBranch,
			Groups:        repoAddGroups,
			SourceRepoID:  repoAddSourceID,
		})
		if err != nil {
			rollbackClone()
			return fmt.Errorf("create repo: %w", err)
		}
		if localPath != "" {
			if err := rememberRepoLocalPath(ws, args[0], localPath); err != nil {
				_ = h.Store.Repos().Delete(context.Background(), ws, args[0])
				rollbackClone()
				return err
			}
		}
		fmt.Printf("Created repo %s/%s (remote: %s)\n", r.WorkspaceKey, r.Name, r.RemoteURL)
		return nil
	})
}

func ensureRepoLocalCheckout(ctx context.Context, ws, name, remoteURL string) (path string, cloned bool, err error) {
	if !service.IsCloneURL(remoteURL) {
		return "", false, nil
	}
	if err := service.ValidateCloneURL(remoteURL); err != nil {
		return "", false, err
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		return "", false, fmt.Errorf("load local workspace state: %w", err)
	}
	local := sc.Workspaces[ws]
	if strings.TrimSpace(local.Path) == "" {
		return "", false, nil
	}
	if local.Repos != nil && local.Repos[name] != "" {
		cachedPath := local.Repos[name]
		if _, err := os.Stat(filepath.Join(cachedPath, ".git")); err == nil {
			return cachedPath, false, nil
		}
		if _, err := os.Stat(cachedPath); os.IsNotExist(err) {
			return "", false, fmt.Errorf("cached repo checkout does not exist: %s", cachedPath)
		} else if err != nil {
			return "", false, fmt.Errorf("inspect cached repo checkout: %w", err)
		}
		return "", false, fmt.Errorf("cached repo checkout is not a git repo: %s", cachedPath)
	}
	target, err := localworkspace.RepoCheckoutPath(local.Path, name)
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err == nil {
		return target, false, nil
	}
	if _, err := os.Stat(target); err == nil {
		return "", false, fmt.Errorf("repo checkout path already exists and is not a git repo: %s", target)
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("inspect repo checkout path: %w", err)
	}
	if err := localworkspace.CloneRepoTo(ctx, remoteURL, target); err != nil {
		return "", false, err
	}
	return target, true, nil
}

func rememberRepoLocalPath(ws, name, repoPath string) error {
	return localworkspace.RememberRepoPath(ws, name, repoPath)
}

func runRepoList(_ *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		repos, err := h.Store.Repos().List(ctx, ws)
		if err != nil {
			return fmt.Errorf("list repos: %w", err)
		}
		if repoListJSON {
			return cmdstore.WriteJSON(repos)
		}
		if len(repos) == 0 {
			fmt.Printf("No repos in workspace %s\n", ws)
			return nil
		}
		for _, r := range repos {
			fmt.Printf("%-30s %s\n", r.Name, r.RemoteURL)
		}
		return nil
	})
}

func runRepoShow(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		r, err := h.Store.Repos().Get(ctx, ws, args[0])
		if err != nil {
			return fmt.Errorf("get repo: %w", err)
		}
		if repoShowJSON {
			return cmdstore.WriteJSON(r)
		}
		fmt.Printf("Workspace:    %s\n", r.WorkspaceKey)
		fmt.Printf("Name:         %s\n", r.Name)
		fmt.Printf("Remote URL:   %s\n", r.RemoteURL)
		if r.Remote != "" {
			fmt.Printf("Remote:       %s\n", r.Remote)
		}
		if r.DefaultBranch != "" {
			fmt.Printf("Default branch: %s\n", r.DefaultBranch)
		}
		if len(r.Groups) > 0 {
			fmt.Printf("Groups:       %s\n", strings.Join(r.Groups, ", "))
		}
		// Printed because it is settable: a field that decides issue routing
		// and cannot be seen is the trap the role task_filter fix closed.
		if r.SourceRepoID != "" {
			fmt.Printf("Source repo id: %s\n", r.SourceRepoID)
		}
		return nil
	})
}

func runRepoRemove(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := h.Store.Repos().Delete(ctx, ws, args[0]); err != nil {
			return fmt.Errorf("remove repo: %w", err)
		}
		fmt.Printf("Removed repo %s/%s\n", ws, args[0])
		return nil
	})
}
