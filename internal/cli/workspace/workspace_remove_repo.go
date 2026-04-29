package workspace

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

var workspaceRemoveRepoCmd = &cobra.Command{
	Use:   "remove-repo <workspace> <repo-name>",
	Short: "Remove a repo entry from an existing workspace",
	Long: `Remove a repo entry from an existing workspace.

This command only updates ~/.loom/config.yaml — it does NOT delete the
repo directory or run git worktree remove. Use 'loom workspace remove'
to remove an entire workspace and its worktrees.

Examples:
  loom workspace remove-repo myws repo-name`,
	Args: cobra.ExactArgs(2),
	Run:  runWorkspaceRemoveRepo,
}

func runWorkspaceRemoveRepo(cmd *cobra.Command, args []string) {
	wsName := args[0]
	repoName := args[1]

	if !isValidWorkspaceName(wsName) {
		fmt.Fprintf(os.Stderr, "Error: workspace name %q contains invalid characters.\n", wsName)
		os.Exit(1)
	}
	if repoName == "" {
		fmt.Fprintf(os.Stderr, "Error: repo name is required\n")
		os.Exit(1)
	}

	unlock := acquireConfigLock()
	defer unlock()

	cfg, err := config.LoadConfigUnlocked()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg == nil {
		fmt.Fprintf(os.Stderr, "Error: no config found.\n")
		os.Exit(1)
	}
	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: workspace %q not found.\n", wsName)
		os.Exit(1)
	}

	idx := -1
	for i, r := range ws.Repos {
		if r.Name == repoName {
			idx = i
			break
		}
	}
	if idx == -1 {
		fmt.Fprintf(os.Stderr, "Error: repo %q not found in workspace %q.\n", repoName, wsName)
		os.Exit(1)
	}

	ws.Repos = append(ws.Repos[:idx], ws.Repos[idx+1:]...)
	cfg.Workspaces[wsName] = ws

	if err := config.SaveConfigUnlocked(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Repo %q removed from workspace %q.\n", repoName, wsName)
}
