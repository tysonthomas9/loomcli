package agent

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
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

	// Check backend health before invoking. If the binary isn't installed,
	// show a helpful error and drop into a shell so the user can fix it.
	backendName := cli.GetBackendName()
	if hs, ok := backends.CheckBackendHealth(backendName); ok && !hs.Installed {
		fmt.Fprintf(os.Stderr, "Error: %s backend is not installed (%s)\n\n", backendName, hs.Message)
		fmt.Fprintf(os.Stderr, "Install it and try again. Dropping into a shell so you can fix this.\n\n")
		execShell(workDir)
		return
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
		fmt.Fprintf(os.Stderr, "\nDropping into a shell. Fix the issue and run 'loom lead' to retry.\n\n")
		execShell(workDir)
	}
}

// execShell replaces the current process with an interactive shell.
// Falls back to running the shell as a subprocess if exec fails.
func execShell(workDir string) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	// Try to replace the process entirely so the terminal stays alive.
	// The shell path is read from the user's own $SHELL env var (trusted),
	// with a safe fallback to /bin/bash. This is an interactive drop-in,
	// not a user-supplied command string.
	// #nosec G204 -- shell path is from $SHELL/static fallback, not user input
	if err := syscall.Exec(shell, []string{shell}, os.Environ()); err != nil {
		// Fallback: run as a child process.
		// #nosec G204 -- shell path is from $SHELL/static fallback, not user input
		cmd := exec.Command(shell)
		cmd.Dir = workDir
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}
}
