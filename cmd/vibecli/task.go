package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var taskCmd = &cobra.Command{
	Use:               "task [worktree]",
	Short:             "Run a Claude implementation agent",
	GroupID:           "agents",
	ValidArgsFunction: worktreeCompletion,
	Long: `Run a Claude implementation agent in the specified worktree.

The implementation agent will:
  1. Pick the highest priority ready task (skipping [Need Review] tasks)
  2. Follow the --design plan if present, otherwise create a local plan
  3. Implement, test, and review the code
  4. Commit and push changes
  5. Close the task and exit after completing ONE task

Arguments:
  worktree    Worktree name (e.g., falcon) or path
              If omitted, runs in current directory

Examples:
  vibecli task falcon              # Run in falcon worktree
  vibecli task                     # Run in current directory
  vibecli task /path/to/worktree   # Run in specific path`,
	Args: cobra.MaximumNArgs(1),
	Run:  runTask,
}

func init() {
	rootCmd.AddCommand(taskCmd)
}

func runTask(cmd *cobra.Command, args []string) {
	// Resolve worktree path
	var worktreeName string
	if len(args) > 0 {
		worktreeName = args[0]
	}

	worktreePath, err := ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	agentName := GetWorktreeName(worktreePath)

	// Acquire lock to prevent concurrent agents
	if err := AcquireLock(worktreePath, "task", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = ReleaseLock(worktreePath) }()

	fmt.Println("=========================================")
	fmt.Printf("Running IMPLEMENTATION agent in: %s\n", worktreePath)
	fmt.Printf("Agent name: %s\n", agentName)
	fmt.Println("=========================================")
	fmt.Println("")

	// Generate and run the task prompt
	prompt := GenerateTaskPrompt(agentName)
	if err := InvokeClaude(worktreePath, prompt); err != nil {
		fmt.Fprintf(os.Stderr, "Error running Claude: %v\n", err)
		os.Exit(1)
	}
}
