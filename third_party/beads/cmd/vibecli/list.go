package main

import (
	"fmt"
	"os"
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
  vibecli list                     # List all agents
  vibecli ls                       # Short alias`,
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

	fmt.Println("Agents (Worktrees):")
	fmt.Println("-------------------")

	for _, wt := range worktrees {
		// Check for running agent first (highest priority)
		lockStatus := GetLockStatus(wt.Path)
		if lockStatus != "" {
			fmt.Printf("  %-12s  %-20s  ● %s\n", wt.Name, wt.Branch, lockStatus)
			continue
		}

		// Check if working tree is clean
		clean, _ := IsCleanWorkingTree(wt.Path)
		status := "✓ ready"
		if !clean {
			status = "● dirty"
		}

		// Check for uncommitted changes count
		changes := getUncommittedChangesCount(wt.Path)
		if changes > 0 {
			status = fmt.Sprintf("● %d changes", changes)
		}

		fmt.Printf("  %-12s  %-20s  %s\n", wt.Name, wt.Branch, status)
	}

	fmt.Println("")
	fmt.Printf("Total: %d agents\n", len(worktrees))
	fmt.Printf("Default branch: %s\n", GetDefaultBranch())
}

func getUncommittedChangesCount(path string) int {
	output, err := RunGitCommand(path, "status", "--porcelain")
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0
	}
	return len(lines)
}
