package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

var (
	wsAddRepoName   string
	wsAddRepoBranch string
	wsAddRepoRemote string
	wsAddRepoGroups string
)

var workspaceAddRepoCmd = &cobra.Command{
	Use:   "add-repo <workspace> <repo-path>",
	Short: "Add a repo entry to an existing workspace",
	Long: `Add a repo entry to an existing workspace.

The repo path must be an existing directory containing a .git entry.
This command only updates ~/.loom/config.yaml — it does NOT create a
git worktree. The repo path is stored as an absolute path.

Use --name to override the default name (otherwise the directory's
basename is used).

Examples:
  loom workspace add-repo myws /path/to/repo
  loom workspace add-repo myws /path/to/repo --name custom
  loom workspace add-repo myws /path/to/repo --branch develop --remote origin
  loom workspace add-repo myws /path/to/repo --groups backend,api`,
	Args: cobra.ExactArgs(2),
	Run:  runWorkspaceAddRepo,
}

// isValidRepoNameCLI mirrors the service-side rule (service.isValidRepoName).
// Allows alphanumeric, hyphens, underscores, dots — safe for YAML and paths.
func isValidRepoNameCLI(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// validateRepoPathErr is a non-exiting form of validateRepoPath.
func validateRepoPathErr(absPath string) error {
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("repo path does not exist: %s", absPath)
	}
	if !info.IsDir() {
		return fmt.Errorf("repo path is not a directory: %s", absPath)
	}
	if _, err := os.Stat(filepath.Join(absPath, ".git")); err != nil {
		return fmt.Errorf("not a git repository: %s", absPath)
	}
	return nil
}

// resolveAddRepoInputs validates and resolves the path/name/groups inputs.
// Exits the process on validation failure (matching existing CLI conventions).
func resolveAddRepoInputs(repoPathArg string) (absPath, repoName string, groups []string) {
	if strings.TrimSpace(repoPathArg) == "" {
		fmt.Fprintf(os.Stderr, "Error: repo path is required\n")
		os.Exit(1)
	}
	absPath, err := filepath.Abs(repoPathArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot resolve repo path %q: %v\n", repoPathArg, err)
		os.Exit(1)
	}
	if err := validateRepoPathErr(absPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	repoName = strings.TrimSpace(wsAddRepoName)
	if repoName == "" {
		repoName = filepath.Base(absPath)
	}
	if !isValidRepoNameCLI(repoName) {
		fmt.Fprintf(os.Stderr, "Error: invalid repo name %q; use only alphanumeric, hyphens, underscores, and dots.\n", repoName)
		os.Exit(1)
	}
	if g := strings.TrimSpace(wsAddRepoGroups); g != "" {
		for _, part := range strings.Split(g, ",") {
			if p := strings.TrimSpace(part); p != "" {
				groups = append(groups, p)
			}
		}
	}
	return absPath, repoName, groups
}

func runWorkspaceAddRepo(cmd *cobra.Command, args []string) {
	wsName := args[0]
	if !isValidWorkspaceName(wsName) {
		fmt.Fprintf(os.Stderr, "Error: workspace name %q contains invalid characters.\n", wsName)
		os.Exit(1)
	}
	absPath, repoName, groups := resolveAddRepoInputs(args[1])

	unlock := acquireConfigLock()
	defer unlock()

	cfg, err := config.LoadConfigUnlocked()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg == nil {
		fmt.Fprintf(os.Stderr, "Error: no config found. Run 'loom workspace create' first.\n")
		os.Exit(1)
	}
	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: workspace %q not found.\n", wsName)
		os.Exit(1)
	}
	for _, r := range ws.Repos {
		if r.Name == repoName {
			fmt.Fprintf(os.Stderr, "Error: repo %q already exists in workspace %q. Use --name to override.\n", repoName, wsName)
			os.Exit(1)
		}
	}
	ws.Repos = append(ws.Repos, config.RepoConfig{
		Name:          repoName,
		Path:          absPath,
		DefaultBranch: wsAddRepoBranch,
		Remote:        wsAddRepoRemote,
		Groups:        groups,
		SourceRepoID:  repoName,
	})
	cfg.Workspaces[wsName] = ws
	if err := config.SaveConfigUnlocked(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Repo %q added to workspace %q.\n", repoName, wsName)
}
