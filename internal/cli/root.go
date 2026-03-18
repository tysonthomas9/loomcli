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
  1. Install beads CLI (issue tracker) from vendored source:
     make install-bd

  2. Initialize beads in your project:
     bd init

  3. Create worktrees for parallel agent work:
     mkdir -p worktrees
     git worktree add ./worktrees/falcon -b falcon
     git worktree add ./worktrees/nova -b nova

  4. Create tasks for agents to work on:
     bd create --title="Add login feature" --type=feature --priority=2

  5. Run agents:
     loom plan falcon    # Creates design, sets status=review
     loom lead           # Review and approve plans
     loom task falcon    # Implements approved design

KEY CONCEPTS
  Worktrees    Isolated git directories (./worktrees/<name>) where agents
               work independently. Each has its own branch.

  Agents       Claude processes that work on tasks:
               - 'plan' agent: researches and creates designs
               - 'task' agent: implements approved designs

  Beads        Issue tracker (bd CLI). Tasks flow through states:
               open → in_progress → review → open → closed

  Auto Mode    --auto flag runs agents continuously, processing multiple
               tasks until stopped (Ctrl+C) or idle timeout.

COMMANDS
  plan         Run a planning agent (creates designs, marks for review)
  task         Run an implementation agent (implements approved tasks)
  lead         Interactive mode for reviewing plans and managing backlog
  monitor      Dashboard showing agent status and task progress
  recover      Recover agent from error state (clear stale locks, reset tasks)
  push         Push worktree branches to target with AI conflict resolution
  pull         Pull integration branch into worktrees with AI conflict resolution
  sync         Full sync: push all completed work, then pull into all worktrees
  reset        Hard reset worktrees to a specific branch
  list         List all worktrees and their status

GLOBAL FLAGS
  -w, --worktrees        Override worktrees directory (takes precedence over env)
      --backend          AI backend CLI (claude, codex, opencode). Env: LOOM_BACKEND

ENVIRONMENT VARIABLES
  LOOM_DEFAULT_BRANCH    Default integration branch (default: main)
  LOOM_WORKTREES_DIR     Worktrees directory (default: ./worktrees)
  LOOM_BACKEND           AI backend CLI to use (default: claude)

EXAMPLES
  loom plan falcon              # Run planning agent in falcon worktree
  loom task falcon --auto       # Continuous implementation mode
  loom lead                     # Interactive backlog management
  loom monitor                  # Watch agent progress
  loom push --all               # Push all worktrees to main
  loom pull --all               # Pull main into all worktrees
  loom sync                     # Full sync: push all + pull all`,
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
	rootCmd.PersistentFlags().StringVar(&backendFlag, "backend", "", "AI backend CLI to use (claude, codex, opencode). Env: LOOM_BACKEND")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "Log format (text|json)")
	rootCmd.PersistentFlags().StringVar(&logOutput, "log-output", "stderr", "Log output destination (stderr|<filepath>)")

	// Resolve and set active backend before any subcommand runs,
	// then inject the Deps container into the command context.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := InitLogger(logFormat, logOutput); err != nil {
			return err
		}
		if err := ResolveAndSetBackend(); err != nil {
			return err
		}
		deps := DefaultDeps()
		cmd.SetContext(WithDeps(cmd.Context(), deps))
		return nil
	}

	// Add command groups for organized help
	rootCmd.AddGroup(&cobra.Group{ID: "agents", Title: "Agent Commands:"})
	rootCmd.AddGroup(&cobra.Group{ID: "git", Title: "Git Operations:"})
	rootCmd.AddGroup(&cobra.Group{ID: "config", Title: "Configuration:"})
	rootCmd.AddGroup(&cobra.Group{ID: "workspace", Title: "Workspace Commands:"})
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}
