package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var claimCmd = &cobra.Command{
	Use:   "claim <task-id>",
	Short: "Update the lock file with the task being worked on",
	Long: `Update the agent lock file to record which task is being worked on.

This command is called by Claude after picking a task with 'bd update <id> --status in_progress'.
It updates the lock file so that 'vibecli monitor' can show which task each agent is working on.

Arguments:
  task-id    The beads task ID (e.g., bd-487)

Examples:
  vibecli claim bd-487              # Record that we're working on bd-487`,
	Args: cobra.ExactArgs(1),
	Run:  runClaim,
}

func init() {
	rootCmd.AddCommand(claimCmd)
}

// bdShowOutput represents the JSON output from 'bd show --json'
type bdShowOutput struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func runClaim(cmd *cobra.Command, args []string) {
	taskID := args[0]

	// Get the current working directory (should be in a worktree)
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting current directory: %v\n", err)
		os.Exit(1)
	}

	// Get task title from bd show
	taskTitle := getTaskTitle(taskID)

	// Update the lock file
	if err := UpdateLockTask(cwd, taskID, taskTitle); err != nil {
		fmt.Fprintf(os.Stderr, "Error updating lock: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Claimed task: %s\n", taskID)
	if taskTitle != "" {
		fmt.Printf("Title: %s\n", taskTitle)
	}
}

func getTaskTitle(taskID string) string {
	cmd := exec.Command("bd", "show", taskID, "--json")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	// bd show --json returns an array
	var results []bdShowOutput
	if err := json.Unmarshal(output, &results); err != nil {
		return ""
	}
	if len(results) == 0 {
		return ""
	}

	return results[0].Title
}
