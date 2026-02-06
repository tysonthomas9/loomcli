package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version information (set at build time)
var (
	Version = "dev"
	Build   = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "vibecli",
	Short: "Agent management CLI for parallel Claude Code workflows",
	Long: `vibecli - Agent Management CLI

A unified command-line tool for managing Claude Code agents in parallel
worktree workflows. Provides commands for planning, implementation,
merging, syncing, and resetting worktrees.

Commands:
  plan     Run a planning agent (creates designs, marks for review)
  task     Run an implementation agent (implements approved tasks)
  merge    Merge worktree branches with AI conflict resolution
  sync     Sync worktrees with integration branch
  reset    Hard reset worktrees to a specific branch

Environment Variables:
  VIBECLI_DEFAULT_BRANCH    Default integration branch (default: feature/web-ui)
  VIBECLI_WORKTREES_DIR     Worktrees directory (default: ./worktrees)

Examples:
  vibecli plan falcon              # Run planning agent in falcon worktree
  vibecli task falcon              # Run implementation agent in falcon
  vibecli merge --all              # Merge all worktrees to integration branch
  vibecli sync --all               # Sync all worktrees from integration branch
  vibecli reset falcon --force     # Reset falcon worktree`,
	Run: func(cmd *cobra.Command, _ []string) {
		if v, _ := cmd.Flags().GetBool("version"); v {
			fmt.Printf("vibecli version %s (%s)\n", Version, Build)
			return
		}
		_ = cmd.Help()
	},
}

func init() {
	rootCmd.Flags().BoolP("version", "v", false, "Print version information")

	// Add command groups for organized help
	rootCmd.AddGroup(&cobra.Group{ID: "agents", Title: "Agent Commands:"})
	rootCmd.AddGroup(&cobra.Group{ID: "git", Title: "Git Operations:"})
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
