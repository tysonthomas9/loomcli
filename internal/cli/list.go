package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
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
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) {
	worktrees, err := DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(worktrees) == 0 {
		fmt.Println("No agents (worktrees) found.")
		fmt.Printf("\nWorktrees directory: %s\n", GetWorktreesDir())
		return
	}

	// Detect workspace mode: if any worktree has a non-empty Workspace, use grouped display
	isWorkspaceMode := false
	for _, wt := range worktrees {
		if wt.Workspace != "" {
			isWorkspaceMode = true
			break
		}
	}

	if isWorkspaceMode {
		renderListWorkspace(worktrees)
	} else {
		renderListLegacy(worktrees)
	}
}

func renderListLegacy(worktrees []WorktreeInfo) {
	fmt.Println("Agents (Worktrees):")
	fmt.Println("-------------------")

	for _, wt := range worktrees {
		status := getWorktreeListStatus(wt)
		fmt.Printf("  %-12s  %-20s  %s\n", wt.Name, wt.Branch, status)
	}

	fmt.Println("")
	fmt.Printf("Total: %d agents\n", len(worktrees))
	fmt.Printf("Default branch: %s\n", GetDefaultBranchForWorktrees(worktrees))
}

func renderListWorkspace(worktrees []WorktreeInfo) {
	// Group worktrees by workspace
	groups := make(map[string][]WorktreeInfo)
	for _, wt := range worktrees {
		ws := wt.Workspace
		if ws == "" {
			ws = "(legacy)"
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
	fmt.Printf("Default branch: %s\n", GetDefaultBranchForWorktrees(worktrees))
}

func getWorktreeListStatusDeps(deps *Deps, wt WorktreeInfo) string {
	// Check for running agent first (highest priority)
	lockStatus := GetLockStatus(wt.Path)
	if lockStatus != "" {
		return fmt.Sprintf("● %s", lockStatus)
	}

	// Check if working tree is clean
	clean, _ := isCleanWorkingTreeDeps(deps, wt.Path)
	status := "✓ ready"
	if !clean {
		status = "● dirty"
	}

	// Check for uncommitted changes count
	changes := getUncommittedChangesCountDeps(deps, wt.Path)
	if changes > 0 {
		status = fmt.Sprintf("● %d changes", changes)
	}

	return status
}

func getWorktreeListStatus(wt WorktreeInfo) string {
	return getWorktreeListStatusDeps(defaultDeps, wt)
}

func getUncommittedChangesCountDeps(deps *Deps, path string) int {
	output, err := runGit(deps, path, "status", "--porcelain")
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0
	}
	return len(lines)
}

func getUncommittedChangesCount(path string) int {
	return getUncommittedChangesCountDeps(defaultDeps, path)
}
