package agent

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var leadCmd = &cobra.Command{
	Use:     "lead",
	Short:   "Interactive project management with AI agent",
	GroupID: "agents",
	Long: `Launch an interactive AI agent session for project management.

Unlike 'plan' and 'task' (which are autonomous agents), 'lead' is a
human-collaborative mode where the AI agent helps you:
  - Review and approve/reject plans from planning agents
  - Create new tickets (tasks, bugs, features, epics)
  - Triage and prioritize the backlog
  - Manage dependencies between tickets

This command does not require a worktree - it can run from the main
repository or any worktree.`,
	Args: cobra.NoArgs,
	Run:  runLead,
}

func init() {
	cli.RegisterCommand(leadCmd)
}

func runLead(cmd *cobra.Command, args []string) {
	// Get current working directory
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=========================================")
	fmt.Println("Starting LEAD mode (Interactive)")
	fmt.Println("=========================================")
	fmt.Println()

	// Generate the lead prompt
	prompt := GenerateLeadPrompt()

	// Invoke agent interactively (no agent name needed - lead mode doesn't claim tasks)
	if err := cli.InvokeAgent(workDir, prompt, ""); err != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", err)
		os.Exit(1)
	}
}
