package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version information (set at build time via ldflags)
var (
	Version = "dev"
	Build   = "unknown"
)

// worktreesFlag stores the --worktrees flag value for override
var worktreesFlag string

var rootCmd = &cobra.Command{
	Use:   "loom",
	Short: "Agent management CLI for parallel Claude Code workflows",
	Long: `loom - Agent Management CLI

Manage Claude Code agents working in parallel across git worktrees.

GETTING STARTED
  1. Install beads CLI (issue tracker):
     go install github.com/bounteous/beads/cmd/bd@latest

  2. Initialize beads in your project:
     bd init

  3. Create worktrees for parallel agent work:
     mkdir -p worktrees
     git worktree add ./worktrees/falcon -b falcon
     git worktree add ./worktrees/nova -b nova

  4. Create tasks for agents to work on:
     bd create --title="Add login feature" --type=feature --priority=2

  5. Run agents:
     loom plan falcon    # Creates design, marks [Need Review]
     loom lead           # Review and approve plans
     loom task falcon    # Implements approved design

KEY CONCEPTS
  Worktrees    Isolated git directories (./worktrees/<name>) where agents
               work independently. Each has its own branch.

  Agents       Claude processes that work on tasks:
               - 'plan' agent: researches and creates designs
               - 'task' agent: implements approved designs

  Beads        Issue tracker (bd CLI). Tasks flow through states:
               open → in_progress → [Need Review] → open → closed

  Auto Mode    --auto flag runs agents continuously, processing multiple
               tasks until stopped (Ctrl+C) or idle timeout.

COMMANDS
  plan         Run a planning agent (creates designs, marks for review)
  task         Run an implementation agent (implements approved tasks)
  lead         Interactive mode for reviewing plans and managing backlog
  monitor      Dashboard showing agent status and task progress
  recover      Recover agent from error state (clear stale locks, reset tasks)
  merge        Merge worktree branches with AI conflict resolution
  sync         Sync worktrees with integration branch
  reset        Hard reset worktrees to a specific branch
  list         List all worktrees and their status

GLOBAL FLAGS
  -w, --worktrees        Override worktrees directory (takes precedence over env)

ENVIRONMENT VARIABLES
  LOOM_DEFAULT_BRANCH    Default integration branch (default: main)
  LOOM_WORKTREES_DIR     Worktrees directory (default: ./worktrees)

EXAMPLES
  loom plan falcon              # Run planning agent in falcon worktree
  loom task falcon --auto       # Continuous implementation mode
  loom lead                     # Interactive backlog management
  loom monitor                  # Watch agent progress
  loom merge --all              # Merge all worktrees to main
  loom sync --all               # Sync all worktrees from main`,
	Run: func(cmd *cobra.Command, args []string) {
		if v, _ := cmd.Flags().GetBool("version"); v {
			fmt.Printf("loom version %s (%s)\n", Version, Build)
			return
		}
		_ = cmd.Help()
	},
}

func init() {
	rootCmd.Flags().BoolP("version", "v", false, "Print version information")
	rootCmd.PersistentFlags().StringVarP(&worktreesFlag, "worktrees", "w", "", "Override worktrees directory (takes precedence over LOOM_WORKTREES_DIR)")

	// Add command groups for organized help
	rootCmd.AddGroup(&cobra.Group{ID: "agents", Title: "Agent Commands:"})
	rootCmd.AddGroup(&cobra.Group{ID: "git", Title: "Git Operations:"})
	rootCmd.AddGroup(&cobra.Group{ID: "config", Title: "Configuration:"})
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}
