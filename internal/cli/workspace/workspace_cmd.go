package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

var (
	wsCreateRepos   string
	wsCreatePath    string
	wsCreateDefault bool
	wsCreateBranch  string

	wsListJSON bool

	wsRemoveForce         bool
	wsRemoveKeepWorktrees bool
)

var workspaceCmd = &cobra.Command{
	Use:     "workspace",
	Short:   "Manage multi-repo workspaces",
	GroupID: "workspace",
	Long: `Manage loom workspaces that group multiple repositories together.

Subcommands:
  create       Create a new workspace with git worktrees
  list         List all configured workspaces
  remove       Remove a workspace and its worktrees

Examples:
  loom workspace create myws --repos /path/to/repo1,/path/to/repo2
  loom workspace list
  loom workspace remove myws`,
}

var workspaceCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new workspace with git worktrees",
	Long: `Create a new workspace that groups multiple repositories together.

For each repo, a git worktree is created under the workspace directory.
The workspace is registered in FleetDB and local checkout paths are cached in
~/.loom/state.json.

Examples:
  loom workspace create myws --repos /path/to/frontend,/path/to/backend
  loom workspace create myws --repos /path/to/repo --branch feature-x
  loom workspace create myws --repos /path/to/repo --path /custom/path`,
	Args: cobra.ExactArgs(1),
	Run:  runWorkspaceCreate,
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured workspaces",
	Args:  cobra.NoArgs,
	Run:   runWorkspaceList,
}

var workspaceRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a workspace and its worktrees",
	Long: `Remove a workspace and optionally clean up its git worktrees.

By default, removes the workspace directory and runs git worktree remove
for each repo. Use --keep-worktrees to only remove from config.

Examples:
  loom workspace remove myws
  loom workspace remove myws --force
  loom workspace remove myws --keep-worktrees`,
	Args: cobra.ExactArgs(1),
	Run:  runWorkspaceRemove,
}

func init() {
	workspaceCreateCmd.Flags().StringVar(&wsCreateRepos, "repos", "", "Comma-separated list of repo paths (required)")
	_ = workspaceCreateCmd.MarkFlagRequired("repos")
	workspaceCreateCmd.Flags().StringVar(&wsCreatePath, "path", "", "Workspace directory path (default: ~/.loom/workspaces/<name>)")
	workspaceCreateCmd.Flags().BoolVar(&wsCreateDefault, "default", false, "Deprecated no-op; default workspace selection has been removed")
	workspaceCreateCmd.Flags().StringVar(&wsCreateBranch, "branch", "", "Branch name for worktrees (default: workspace name)")

	workspaceListCmd.Flags().BoolVar(&wsListJSON, "json", false, "Output as JSON")

	workspaceRemoveCmd.Flags().BoolVar(&wsRemoveForce, "force", false, "Remove even if worktrees are dirty")
	workspaceRemoveCmd.Flags().BoolVar(&wsRemoveKeepWorktrees, "keep-worktrees", false, "Remove from config but don't delete git worktrees")

	workspaceCmd.AddCommand(workspaceCreateCmd)
	workspaceCmd.AddCommand(workspaceListCmd)
	workspaceCmd.AddCommand(workspaceRemoveCmd)

	cli.RegisterCommand(workspaceCmd)
}

// isValidWorkspaceName checks that a workspace name contains only safe characters.
func isValidWorkspaceName(name string) bool {
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return len(name) > 0
}

func runWorkspaceCreate(cmd *cobra.Command, args []string) {
	wsName := args[0]
	branch := validateCreateInputs(wsName)
	repoPaths := parseRepoPaths()

	if err := cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		create := workspacemgr.BuildStoreBackedCreateWorkspace(h.Store)
		result, err := create(ctx, service.WorkspaceCreateRequest{
			Name:   wsName,
			Type:   "empty",
			Repos:  repoPaths,
			Path:   wsCreatePath,
			Branch: branch,
		})
		if err != nil {
			return err
		}
		fmt.Printf("Workspace %q created at %s.\n", result.WorkspaceID, result.WorkspacePath)
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func validateCreateInputs(wsName string) string {
	if !isValidWorkspaceName(wsName) {
		fmt.Fprintf(os.Stderr, "Error: workspace name %q contains invalid characters. Use only alphanumeric, hyphens, and underscores.\n", wsName)
		os.Exit(1)
	}
	branch := wsCreateBranch
	if branch == "" {
		branch = wsName
	}
	if !isValidWorkspaceName(branch) || strings.HasPrefix(branch, "-") {
		fmt.Fprintf(os.Stderr, "Error: branch name %q is invalid. Must contain only alphanumeric, hyphens, and underscores, and must not start with a dash.\n", branch)
		os.Exit(1)
	}
	return branch
}

func parseRepoPaths() []string {
	repoPaths := strings.Split(wsCreateRepos, ",")
	if len(repoPaths) == 0 || (len(repoPaths) == 1 && repoPaths[0] == "") {
		fmt.Fprintf(os.Stderr, "Error: --repos is required and must not be empty\n")
		os.Exit(1)
	}
	return repoPaths
}

func runWorkspaceList(cmd *cobra.Command, args []string) {
	if err := runFleetWorkspaceList(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

//nolint:gocognit,funlen // CLI table/JSON output branches share one store read path.
func runFleetWorkspaceList() error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		workspaces, err := h.Store.Workspaces().List(ctx)
		if err != nil {
			return fmt.Errorf("list workspaces: %w", err)
		}
		if len(workspaces) == 0 {
			fmt.Println("No workspaces configured. Run 'loom workspace add <KEY>' to create one.")
			return nil
		}

		sc, _ := bootstrap.LoadStateCache()
		pathByKey := map[string]string{}
		if sc != nil {
			for key, local := range sc.Workspaces {
				pathByKey[key] = local.Path
			}
		}

		sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].Name < workspaces[j].Name })
		if wsListJSON {
			type fleetWorkspaceListItem struct {
				Key       string `json:"key"`
				Name      string `json:"name"`
				Path      string `json:"path,omitempty"`
				Repos     int    `json:"repos"`
				State     string `json:"state"`
				IsDefault bool   `json:"is_default"`
			}
			items := make([]fleetWorkspaceListItem, 0, len(workspaces))
			for _, ws := range workspaces {
				repos, err := h.Store.Repos().List(ctx, ws.Key)
				if err != nil {
					return fmt.Errorf("list repos for %s: %w", ws.Key, err)
				}
				state := string(ws.State)
				if state == "" {
					state = "ready"
				}
				items = append(items, fleetWorkspaceListItem{
					Key:       ws.Key,
					Name:      ws.Name,
					Path:      pathByKey[ws.Key],
					Repos:     len(repos),
					State:     state,
					IsDefault: false,
				})
			}
			return cmdstore.WriteJSON(items)
		}

		for _, ws := range workspaces {
			repos, err := h.Store.Repos().List(ctx, ws.Key)
			if err != nil {
				return fmt.Errorf("list repos for %s: %w", ws.Key, err)
			}
			state := string(ws.State)
			if state == "" {
				state = "ready"
			}
			path := pathByKey[ws.Key]
			if path == "" {
				path = "(no local checkout)"
			}
			fmt.Printf("%-20s %s (%d repos, %s)\n", ws.Key, path, len(repos), state)
		}
		return nil
	})
}

func runWorkspaceRemove(cmd *cobra.Command, args []string) {
	deps := cli.GetDeps(cmd)
	wsName := args[0]
	if err := cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, err := h.Store.Workspaces().Get(ctx, wsName)
		if err != nil {
			if byName, byNameErr := h.Store.Workspaces().GetByName(ctx, wsName); byNameErr == nil {
				ws = byName
			} else {
				return fmt.Errorf("workspace %q not found: %w", wsName, err)
			}
		}
		local, err := workspaceLocalConfig(ctx, h, ws.Key)
		if err != nil {
			return err
		}
		if !wsRemoveKeepWorktrees {
			checkRunningAgentsOrExit(local)
			removeWorktrees(deps, local)
		}
		if err := h.Store.Workspaces().Delete(ctx, ws.Key); err != nil && !cmdstore.IsNotFound(err) {
			return fmt.Errorf("delete workspace from fleet-db: %w", err)
		}
		if err := deleteWorkspaceLocalState(ws.Key); err != nil {
			return err
		}
		fmt.Printf("Workspace %q removed.\n", ws.Key)
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func workspaceLocalConfig(ctx context.Context, h *bootstrap.StoreHandle, key string) (config.WorkspaceConfig, error) {
	sc, _ := bootstrap.LoadStateCache()
	local := bootstrap.WorkspaceLocalState{}
	if sc != nil {
		local = sc.Workspaces[key]
	}
	repoRows, err := h.Store.Repos().List(ctx, key)
	if err != nil {
		return config.WorkspaceConfig{}, fmt.Errorf("list repos for %s: %w", key, err)
	}
	repos := make([]config.RepoConfig, 0, len(repoRows))
	for _, repo := range repoRows {
		if repo == nil {
			continue
		}
		path := local.Repos[repo.Name]
		if path == "" && local.Path != "" {
			path = filepath.Join(local.Path, repo.Name)
		}
		repos = append(repos, config.RepoConfig{
			Name:          repo.Name,
			Path:          path,
			DefaultBranch: repo.DefaultBranch,
			Remote:        repo.Remote,
			Groups:        append([]string(nil), repo.Groups...),
			SourceRepoID:  repo.SourceRepoID,
		})
	}
	return config.WorkspaceConfig{ID: key, Path: local.Path, Repos: repos}, nil
}

func deleteWorkspaceLocalState(key string) error {
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		delete(sc.Workspaces, key)
		if sc.LastWorkspace == key {
			sc.LastWorkspace = ""
		}
		return nil
	}); err != nil {
		return fmt.Errorf("update local workspace state: %w", err)
	}
	return nil
}

func checkRunningAgentsOrExit(ws config.WorkspaceConfig) {
	if wsRemoveForce {
		return
	}
	for _, repo := range ws.Repos {
		lockPath := filepath.Join(repo.Path, ".agent.lock")
		if _, err := os.Stat(lockPath); err == nil {
			fmt.Fprintf(os.Stderr, "Error: repo %q has a running agent (lock file exists). Use --force to override.\n", repo.Name)
			os.Exit(1)
		}
	}
}

func removeWorktrees(deps *cli.Deps, ws config.WorkspaceConfig) {
	var errs []string
	for _, repo := range ws.Repos {
		repoPath := repo.Path
		if !filepath.IsAbs(repoPath) {
			repoPath = filepath.Join(ws.Path, repoPath)
		}
		if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
			continue
		}
		if err := removeOneWorktree(deps, repoPath, repo.Name); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 && !wsRemoveForce {
		fmt.Fprintf(os.Stderr, "Error removing worktrees:\n%s\nUse --force to override.\n", strings.Join(errs, "\n"))
		os.Exit(1)
	}
	if err := os.RemoveAll(ws.Path); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not remove workspace directory %s: %v\n", ws.Path, err)
	}
}

func removeOneWorktree(deps *cli.Deps, repoPath, repoName string) error {
	mainRepoPath := findMainRepoPath(repoPath)
	if mainRepoPath == "" {
		return nil
	}
	if _, err := cli.RunGit(deps, mainRepoPath, "worktree", "remove", repoPath); err != nil {
		if wsRemoveForce {
			if _, err = cli.RunGit(deps, mainRepoPath, "worktree", "remove", "--force", repoPath); err != nil {
				return fmt.Errorf("  %s: %v", repoName, err)
			}
			return nil
		}
		return fmt.Errorf("  %s: %v", repoName, err)
	}
	return nil
}

// findMainRepoPath reads the .git file in a worktree to find the main repo's path.
func findMainRepoPath(worktreePath string) string {
	gitFile := filepath.Join(worktreePath, ".git")
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}

	// .git file in worktree contains: "gitdir: /path/to/main/.git/worktrees/<name>"
	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(content, "gitdir: ") {
		return ""
	}

	gitDir := strings.TrimPrefix(content, "gitdir: ")
	// Resolve relative paths relative to the worktree directory
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktreePath, gitDir)
	}
	gitDir = filepath.Clean(gitDir)
	// Navigate up from .git/worktrees/<name> to the main repo
	// gitDir looks like: /path/to/main/.git/worktrees/<name>
	parts := strings.Split(filepath.ToSlash(gitDir), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == ".git" {
			return filepath.FromSlash(strings.Join(parts[:i], "/"))
		}
	}

	return ""
}
