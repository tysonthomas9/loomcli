package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	epicRunWorktree string
	epicRunResume   bool
)

var epicRunCmd = &cobra.Command{
	Use:     "epic-run [epic-id]",
	Short:   "Run an epic end-to-end with an AI orchestrator",
	GroupID: "agents",
	Long: `Launch an interactive orchestrator that runs an epic end-to-end.

The orchestrator is an AI agent that manages the epic run:
  1. Understands the epic and its tasks
  2. Generates an agentflow pipeline from the task DAG
  3. Starts the agentflow engine (serve mode) and monitors progress
  4. Validates results after the pipeline completes
  5. Creates fix tasks and re-runs if validation fails
  6. Closes the epic when everything passes

The orchestrator is interactive — you can talk to it between phases,
ask for status, pause or resume the pipeline, and make decisions.

If no epic-id is given, the orchestrator will help you create one.

Examples:
  loom epic-run loomcli-abc --worktree falcon
  loom epic-run loomcli-abc --worktree falcon --resume
  loom epic-run --worktree falcon`,
	Args: cobra.MaximumNArgs(1),
	Run:  runEpicRun,
}

func init() {
	epicRunCmd.Flags().StringVar(&epicRunWorktree, "worktree", "", "Worktree for agent execution (required)")
	epicRunCmd.Flags().BoolVar(&epicRunResume, "resume", false, "Resume an in-progress epic run from checkpoint")
	_ = epicRunCmd.MarkFlagRequired("worktree")
	rootCmd.AddCommand(epicRunCmd)
}

func runEpicRun(cmd *cobra.Command, args []string) {
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}

	// Check backend health
	backendName := GetBackendName()
	if hs, ok := CheckBackendHealth(backendName); ok && !hs.Installed {
		fmt.Fprintf(os.Stderr, "Error: %s backend is not installed (%s)\n\n", backendName, hs.Message)
		fmt.Fprintf(os.Stderr, "Install it and try again. Dropping into a shell so you can fix this.\n\n")
		execShell(workDir)
		return
	}

	epicID := ""
	if len(args) > 0 {
		epicID = args[0]
	}

	// Use absolute path so commands work regardless of cwd changes during the session
	runDir := filepath.Join(workDir, ".loom", "epic-runs")
	if epicID != "" {
		runDir = filepath.Join(workDir, ".loom", "epic-runs", epicID)
	}

	fmt.Println("=========================================")
	fmt.Println("Starting EPIC RUN ORCHESTRATOR")
	if epicID != "" {
		fmt.Printf("Epic: %s\n", epicID)
	} else {
		fmt.Println("Mode: new epic (will create one)")
	}
	fmt.Printf("Worktree: %s\n", epicRunWorktree)
	if epicRunResume {
		fmt.Println("Resuming from checkpoint")
	}
	fmt.Println("=========================================")
	fmt.Println()

	prompt := GenerateEpicRunPrompt(epicID, epicRunWorktree, runDir, epicRunResume)

	if err := InvokeAgent(workDir, prompt, "epic-run"); err != nil {
		fmt.Fprintf(os.Stderr, "Error running epic-run orchestrator: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nDropping into a shell. Fix the issue and run 'loom epic-run' to retry.\n\n")
		execShell(workDir)
	}
}
