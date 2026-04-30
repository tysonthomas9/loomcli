package workspace

import (
	"context"
	"encoding/json"
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
	"github.com/tysonthomas9/loomcli/internal/configlock"
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
The workspace is registered in ~/.loom/config.yaml.

Examples:
  loom workspace create myws --repos /path/to/frontend,/path/to/backend
  loom workspace create myws --repos /path/to/repo --branch feature-x
  loom workspace create myws --repos /path/to/repo --path /custom/path --default`,
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
	workspaceCreateCmd.Flags().BoolVar(&wsCreateDefault, "default", false, "Set as default workspace")
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
	deps := cli.GetDeps(cmd)
	wsName := args[0]
	branch := validateCreateInputs(wsName)
	repoPaths := parseRepoPaths()

	unlock := acquireConfigLock()
	defer unlock()

	cfg := loadOrInitConfig()
	if _, exists := cfg.Workspaces[wsName]; exists {
		fmt.Fprintf(os.Stderr, "Error: workspace %q already exists. Use a different name.\n", wsName)
		os.Exit(1)
	}

	wsDir := wsCreatePath
	if wsDir == "" {
		wsDir = config.GetWorkspaceDir(wsName)
	}

	resolvedRepos := resolveAndValidateRepos(repoPaths)
	repos := createWorkspaceWorktrees(deps, wsDir, resolvedRepos, branch)

	bdResult := deps.Exec.Run(wsDir, "bd", "init")
	if bdResult.Err != nil {
		fmt.Fprintf(os.Stderr, "Warning: bd init failed in workspace (non-fatal): %s\n", bdResult.Stderr)
	}

	saveWorkspaceConfig(cfg, wsName, wsDir, repos)
	fmt.Printf("Workspace %q created at %s with %d repo(s).\n", wsName, wsDir, len(repos))
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

func acquireConfigLock() func() {
	configDir := config.GetConfigDir()
	if configDir == "" {
		fmt.Fprintf(os.Stderr, "Error: cannot determine config directory\n")
		os.Exit(1)
	}
	unlock, err := configlock.ConfigLock(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return unlock
}

func loadOrInitConfig() *config.LoomConfig {
	cfg, err := config.LoadConfigUnlocked()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg == nil {
		cfg = &config.LoomConfig{Workspaces: make(map[string]config.WorkspaceConfig)}
	}
	if cfg.Workspaces == nil {
		cfg.Workspaces = make(map[string]config.WorkspaceConfig)
	}
	return cfg
}

type cliResolvedRepo struct {
	path string
	name string
}

func resolveAndValidateRepos(repoPaths []string) []cliResolvedRepo {
	var resolved []cliResolvedRepo
	seenNames := make(map[string]string)

	for _, rp := range repoPaths {
		rp = strings.TrimSpace(rp)
		if rp == "" {
			continue
		}
		absPath, err := filepath.Abs(rp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot resolve path %q: %v\n", rp, err)
			os.Exit(1)
		}
		validateRepoPath(absPath)
		baseName := filepath.Base(absPath)
		if prev, exists := seenNames[baseName]; exists {
			fmt.Fprintf(os.Stderr, "Error: duplicate repo name %q from paths %s and %s. Repos must have unique directory names.\n", baseName, prev, absPath)
			os.Exit(1)
		}
		seenNames[baseName] = absPath
		resolved = append(resolved, cliResolvedRepo{path: absPath, name: baseName})
	}
	if len(resolved) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no valid repos specified\n")
		os.Exit(1)
	}
	return resolved
}

func validateRepoPath(absPath string) {
	info, err := os.Stat(absPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: repo path does not exist: %s\n", absPath)
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: repo path is not a directory: %s\n", absPath)
		os.Exit(1)
	}
	if _, err := os.Stat(filepath.Join(absPath, ".git")); err != nil {
		fmt.Fprintf(os.Stderr, "Error: not a git repository: %s\n", absPath)
		os.Exit(1)
	}
}

type cliCreatedWorktree struct {
	origRepoPath string
	worktreePath string
}

func createWorkspaceWorktrees(deps *cli.Deps, wsDir string, resolvedRepos []cliResolvedRepo, branch string) []config.RepoConfig {
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot create workspace directory %s: %v\n", wsDir, err)
		os.Exit(1)
	}

	var created []cliCreatedWorktree
	var repos []config.RepoConfig
	for _, repo := range resolvedRepos {
		worktreePath := filepath.Join(wsDir, repo.name)
		if _, err := cli.RunGit(deps, repo.path, "worktree", "add", worktreePath, "-b", branch); err != nil {
			fmt.Fprintf(os.Stderr, "Error: git worktree add failed for %s: %v\n", repo.name, err)
			for _, c := range created {
				_, _ = cli.RunGit(deps, c.origRepoPath, "worktree", "remove", c.worktreePath)
			}
			_ = os.RemoveAll(wsDir)
			os.Exit(1)
		}
		created = append(created, cliCreatedWorktree{origRepoPath: repo.path, worktreePath: worktreePath})
		repos = append(repos, config.RepoConfig{Name: repo.name, Path: worktreePath})
		fmt.Printf("  Created worktree: %s -> %s\n", repo.name, worktreePath)
	}
	return repos
}

func saveWorkspaceConfig(cfg *config.LoomConfig, wsName, wsDir string, repos []config.RepoConfig) {
	cfg.Workspaces[wsName] = config.WorkspaceConfig{ID: config.NewWorkspaceID(), Path: wsDir, Repos: repos}
	if wsCreateDefault || len(cfg.Workspaces) == 1 {
		cfg.DefaultWorkspace = wsName
	}
	if err := config.SaveConfigUnlocked(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runWorkspaceList(cmd *cobra.Command, args []string) {
	if cli.IsFleetDBActive() || cli.IsFleetActive() {
		if err := runFleetWorkspaceList(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg == nil || len(cfg.Workspaces) == 0 {
		fmt.Println("No workspaces configured. Run 'loom workspace create' to create one.")
		return
	}

	if wsListJSON {
		data, err := json.MarshalIndent(cfg.Workspaces, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
		return
	}

	// Sort workspace names for deterministic output
	names := make([]string, 0, len(cfg.Workspaces))
	for name := range cfg.Workspaces {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		ws := cfg.Workspaces[name]
		defaultMarker := ""
		if name == cfg.DefaultWorkspace {
			defaultMarker = " *"
		}

		// Check if directory exists
		dirStatus := "ok"
		if _, err := os.Stat(ws.Path); err != nil {
			dirStatus = "missing"
		}

		fmt.Printf("%-20s %s (%d repos, %s)%s\n", name, ws.Path, len(ws.Repos), dirStatus, defaultMarker)
	}
}

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
		activeKey := ""
		if sc != nil {
			activeKey = sc.LastWorkspace
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
					IsDefault: ws.Key == activeKey,
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
			defaultMarker := ""
			if ws.Key == activeKey {
				defaultMarker = " *"
			}
			path := pathByKey[ws.Key]
			if path == "" {
				path = "(no local checkout)"
			}
			fmt.Printf("%-20s %s (%d repos, %s)%s\n", ws.Key, path, len(repos), state, defaultMarker)
		}
		return nil
	})
}

func runWorkspaceRemove(cmd *cobra.Command, args []string) {
	deps := cli.GetDeps(cmd)
	wsName := args[0]

	unlock := acquireConfigLock()
	defer unlock()

	cfg := loadConfigForRemove()
	ws := findWorkspaceOrExit(cfg, wsName)
	checkRunningAgentsOrExit(ws)

	if !wsRemoveKeepWorktrees {
		removeWorktrees(deps, ws)
	}

	updateConfigAfterRemove(cfg, wsName)
	fmt.Printf("Workspace %q removed.\n", wsName)
}

func loadConfigForRemove() *config.LoomConfig {
	cfg, err := config.LoadConfigUnlocked()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg == nil {
		fmt.Fprintf(os.Stderr, "No config found. Nothing to remove.\n")
		os.Exit(1)
	}
	return cfg
}

func findWorkspaceOrExit(cfg *config.LoomConfig, wsName string) config.WorkspaceConfig {
	ws, ok := cfg.Workspaces[wsName]
	if ok {
		return ws
	}
	available := make([]string, 0, len(cfg.Workspaces))
	for name := range cfg.Workspaces {
		available = append(available, name)
	}
	sort.Strings(available)
	fmt.Fprintf(os.Stderr, "Workspace %q not found. Available: %s\n", wsName, strings.Join(available, ", "))
	os.Exit(1)
	return config.WorkspaceConfig{} // unreachable
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

func updateConfigAfterRemove(cfg *config.LoomConfig, wsName string) {
	delete(cfg.Workspaces, wsName)
	if cfg.DefaultWorkspace == wsName {
		cfg.DefaultWorkspace = ""
		names := make([]string, 0, len(cfg.Workspaces))
		for name := range cfg.Workspaces {
			names = append(names, name)
		}
		if len(names) > 0 {
			sort.Strings(names)
			cfg.DefaultWorkspace = names[0]
		}
	}
	if err := config.SaveConfigUnlocked(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
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
