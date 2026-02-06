package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var planCmd = &cobra.Command{
	Use:               "plan [worktree]",
	Short:             "Run a Claude planning agent",
	GroupID:           "agents",
	ValidArgsFunction: worktreeCompletion,
	Long: `Run a Claude planning agent in the specified worktree.

The planning agent will:
  1. Pick the highest priority task (skipping [Need Review] tasks)
  2. Research the codebase and create a detailed plan
  3. Save the plan to the task's --design field
  4. Mark the task as [Need Review] for human approval
  5. Exit after completing ONE task

Arguments:
  worktree    Worktree name (e.g., falcon) or path
              If omitted, runs in current directory

Examples:
  vibecli plan falcon              # Run in falcon worktree
  vibecli plan                     # Run in current directory
  vibecli plan /path/to/worktree   # Run in specific path`,
	Args: cobra.MaximumNArgs(1),
	Run:  runPlan,
}

func init() {
	rootCmd.AddCommand(planCmd)
}

func runPlan(_ *cobra.Command, args []string) {
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
	if err := AcquireLock(worktreePath, "plan", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = ReleaseLock(worktreePath) }()

	fmt.Println("=========================================")
	fmt.Printf("Running PLANNING agent in: %s\n", worktreePath)
	fmt.Printf("Agent name: %s\n", agentName)
	fmt.Println("=========================================")
	fmt.Println("")

	// Generate and run the planning prompt
	prompt := GeneratePlanningPrompt(agentName)
	if err := InvokeClaude(worktreePath, prompt); err != nil {
		fmt.Fprintf(os.Stderr, "Error running Claude: %v\n", err)
		os.Exit(1)
	}
}
