package agent

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/git"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all agents (worktrees)",
	GroupID: "agents",
	Long: `List all available agents (worktrees) and their status.

Shows:
  - Worktree name
  - Current branch
  - Status: running agent, dirty working tree, or clean

Examples:
  loom list                     # List all agents
  loom ls                       # Short alias`,
	Args: cobra.NoArgs,
	Run:  runList,
}

func init() {
	cli.RegisterCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) {
	worktrees, err := cli.DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(worktrees) == 0 {
		fmt.Println("No agents (worktrees) found.")
		fmt.Printf("\nWorktrees directory: %s\n", cli.GetWorktreesDir())
		return
	}

	renderListWorkspace(worktrees)
}

func renderListWorkspace(worktrees []cli.WorktreeInfo) {
	// Group worktrees by workspace
	groups := make(map[string][]cli.WorktreeInfo)
	for _, wt := range worktrees {
		ws := wt.Workspace
		if ws == "" {
			ws = "unassigned"
		}
		groups[ws] = append(groups[ws], wt)
	}

	// Sort workspace names
	var wsNames []string
	for name := range groups {
		wsNames = append(wsNames, name)
	}
	sort.Strings(wsNames)

	fmt.Println("Agents by Workspace:")
	fmt.Println("====================")

	for _, ws := range wsNames {
		fmt.Printf("\n[%s]\n", ws)
		for _, wt := range groups[ws] {
			status := getWorktreeListStatus(wt)
			fmt.Printf("  %-12s  %-20s  %s\n", wt.Name, wt.Branch, status)
		}
	}

	fmt.Println("")
	fmt.Printf("Total: %d agents across %d workspaces\n", len(worktrees), len(wsNames))
	fmt.Printf("Default branch: %s\n", cli.GetDefaultBranchForWorktrees(worktrees))
}

func getWorktreeListStatusDeps(deps *cli.Deps, wt cli.WorktreeInfo) string {
	// Check for running agent first (highest priority)
	lockStatus := cli.GetLockStatus(wt.Path)
	if lockStatus != "" {
		return fmt.Sprintf("● %s", lockStatus)
	}

	// Check if working tree is clean
	clean, _ := git.IsCleanWorkingTreeDeps(deps, wt.Path)
	status := "✓ ready"
	if !clean {
		status = "● dirty"
	}

	// Check for uncommitted changes count
	changes := GetUncommittedChangesCountDeps(deps, wt.Path)
	if changes > 0 {
		status = fmt.Sprintf("● %d changes", changes)
	}

	return status
}

func getWorktreeListStatus(wt cli.WorktreeInfo) string {
	return getWorktreeListStatusDeps(cli.GetDeps(nil), wt)
}

func GetUncommittedChangesCountDeps(deps *cli.Deps, path string) int {
	output, err := cli.RunGit(deps, path, "status", "--porcelain")
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0
	}
	return len(lines)
}

func GetUncommittedChangesCount(path string) int {
	return GetUncommittedChangesCountDeps(cli.GetDeps(nil), path)
}
