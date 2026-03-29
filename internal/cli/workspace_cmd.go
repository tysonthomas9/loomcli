package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
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

	rootCmd.AddCommand(workspaceCmd)
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

	if !isValidWorkspaceName(wsName) {
		fmt.Fprintf(os.Stderr, "Error: workspace name %q contains invalid characters. Use only alphanumeric, hyphens, and underscores.\n", wsName)
		os.Exit(1)
	}

	branch := wsCreateBranch
	if branch == "" {
		branch = wsName
	}

	// Validate branch name (wsName is already validated above, but --branch flag is not)
	if !isValidWorkspaceName(branch) || strings.HasPrefix(branch, "-") {
		fmt.Fprintf(os.Stderr, "Error: branch name %q is invalid. Must contain only alphanumeric, hyphens, and underscores, and must not start with a dash.\n", branch)
		os.Exit(1)
	}

	// Parse repos
	repoPaths := strings.Split(wsCreateRepos, ",")
	if len(repoPaths) == 0 || (len(repoPaths) == 1 && repoPaths[0] == "") {
		fmt.Fprintf(os.Stderr, "Error: --repos is required and must not be empty\n")
		os.Exit(1)
	}

	// Load or create config
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg == nil {
		cfg = &LoomConfig{
			Workspaces: make(map[string]WorkspaceConfig),
		}
	}
	if cfg.Workspaces == nil {
		cfg.Workspaces = make(map[string]WorkspaceConfig)
	}

	// Check if workspace already exists
	if _, exists := cfg.Workspaces[wsName]; exists {
		fmt.Fprintf(os.Stderr, "Error: workspace %q already exists. Use a different name.\n", wsName)
		os.Exit(1)
	}

	// Determine workspace directory
	wsDir := wsCreatePath
	if wsDir == "" {
		wsDir = GetWorkspaceDir(wsName)
	}

	// Validate all repo paths before creating anything
	type resolvedRepo struct {
		path string
		name string
	}
	var resolvedRepos []resolvedRepo
	seenNames := make(map[string]string) // basename -> original path (for duplicate detection)

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

		// Validate path exists
		info, err := os.Stat(absPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: repo path does not exist: %s\n", absPath)
			os.Exit(1)
		}
		if !info.IsDir() {
			fmt.Fprintf(os.Stderr, "Error: repo path is not a directory: %s\n", absPath)
			os.Exit(1)
		}

		// Validate it's a git repository
		gitDir := filepath.Join(absPath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: not a git repository: %s\n", absPath)
			os.Exit(1)
		}

		baseName := filepath.Base(absPath)
		if prev, exists := seenNames[baseName]; exists {
			fmt.Fprintf(os.Stderr, "Error: duplicate repo name %q from paths %s and %s. Repos must have unique directory names.\n", baseName, prev, absPath)
			os.Exit(1)
		}
		seenNames[baseName] = absPath

		resolvedRepos = append(resolvedRepos, resolvedRepo{path: absPath, name: baseName})
	}

	if len(resolvedRepos) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no valid repos specified\n")
		os.Exit(1)
	}

	// Create workspace directory
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot create workspace directory %s: %v\n", wsDir, err)
		os.Exit(1)
	}

	// Track created worktrees with their original repo paths for cleanup
	type createdWorktree struct {
		origRepoPath string
		worktreePath string
	}
	var created []createdWorktree
	var repos []RepoConfig

	for _, repo := range resolvedRepos {
		worktreePath := filepath.Join(wsDir, repo.name)

		// Run git worktree add from the repo root
		_, err := RunGitCommand(repo.path, "worktree", "add", worktreePath, "-b", branch)
		if err != nil {
			// Clean up workspace directory on failure
			fmt.Fprintf(os.Stderr, "Error: git worktree add failed for %s: %v\n", repo.name, err)
			// Attempt cleanup of already-created worktrees using original repo paths
			for _, c := range created {
				_, _ = RunGitCommand(c.origRepoPath, "worktree", "remove", c.worktreePath)
			}
			_ = os.RemoveAll(wsDir)
			os.Exit(1)
		}

		created = append(created, createdWorktree{origRepoPath: repo.path, worktreePath: worktreePath})
		repos = append(repos, RepoConfig{
			Name: repo.name,
			Path: worktreePath,
		})

		fmt.Printf("  Created worktree: %s -> %s\n", repo.name, worktreePath)
	}

	// Run bd init in workspace directory (best-effort)
	bdResult := execCommand(wsDir, "bd", "init")
	if bdResult.Err != nil {
		fmt.Fprintf(os.Stderr, "Warning: bd init failed in workspace (non-fatal): %s\n", bdResult.Stderr)
	}

	// Save config
	ws := WorkspaceConfig{
		ID:    NewWorkspaceID(),
		Path:  wsDir,
		Repos: repos,
	}
	cfg.Workspaces[wsName] = ws

	if wsCreateDefault || len(cfg.Workspaces) == 1 {
		cfg.DefaultWorkspace = wsName
	}

	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Workspace %q created at %s with %d repo(s).\n", wsName, wsDir, len(repos))
}

func runWorkspaceList(cmd *cobra.Command, args []string) {
	cfg, err := LoadConfig()
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

func runWorkspaceRemove(cmd *cobra.Command, args []string) {
	wsName := args[0]

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg == nil {
		fmt.Fprintf(os.Stderr, "No config found. Nothing to remove.\n")
		os.Exit(1)
	}

	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		available := make([]string, 0, len(cfg.Workspaces))
		for name := range cfg.Workspaces {
			available = append(available, name)
		}
		sort.Strings(available)
		fmt.Fprintf(os.Stderr, "Workspace %q not found. Available: %s\n", wsName, strings.Join(available, ", "))
		os.Exit(1)
	}

	// Check for lock files (running agents) unless --force
	if !wsRemoveForce {
		for _, repo := range ws.Repos {
			lockPath := filepath.Join(repo.Path, ".agent.lock")
			if _, err := os.Stat(lockPath); err == nil {
				fmt.Fprintf(os.Stderr, "Error: repo %q has a running agent (lock file exists). Use --force to override.\n", repo.Name)
				os.Exit(1)
			}
		}
	}

	// Remove git worktrees unless --keep-worktrees
	if !wsRemoveKeepWorktrees {
		var errors []string
		for _, repo := range ws.Repos {
			repoPath := repo.Path
			if !filepath.IsAbs(repoPath) {
				repoPath = filepath.Join(ws.Path, repoPath)
			}

			// Find the original repo root by reading the .git file in the worktree
			gitFile := filepath.Join(repoPath, ".git")
			if _, err := os.Stat(gitFile); err != nil {
				// Worktree directory doesn't exist or isn't a git worktree
				continue
			}

			// Try to remove worktree using git
			// We need the main repo path. Read .git file to find it.
			mainRepoPath := findMainRepoPath(repoPath)
			if mainRepoPath != "" {
				_, err := RunGitCommand(mainRepoPath, "worktree", "remove", repoPath)
				if err != nil {
					if wsRemoveForce {
						// Force remove
						_, err = RunGitCommand(mainRepoPath, "worktree", "remove", "--force", repoPath)
						if err != nil {
							errors = append(errors, fmt.Sprintf("  %s: %v", repo.Name, err))
						}
					} else {
						errors = append(errors, fmt.Sprintf("  %s: %v", repo.Name, err))
					}
				}
			}
		}

		if len(errors) > 0 && !wsRemoveForce {
			fmt.Fprintf(os.Stderr, "Error removing worktrees:\n%s\nUse --force to override.\n", strings.Join(errors, "\n"))
			os.Exit(1)
		}

		// Remove workspace directory
		if err := os.RemoveAll(ws.Path); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not remove workspace directory %s: %v\n", ws.Path, err)
		}
	}

	// Update config
	delete(cfg.Workspaces, wsName)

	// Update default workspace if needed
	if cfg.DefaultWorkspace == wsName {
		cfg.DefaultWorkspace = ""
		// Set to first remaining workspace
		names := make([]string, 0, len(cfg.Workspaces))
		for name := range cfg.Workspaces {
			names = append(names, name)
		}
		if len(names) > 0 {
			sort.Strings(names)
			cfg.DefaultWorkspace = names[0]
		}
	}

	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Workspace %q removed.\n", wsName)
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
