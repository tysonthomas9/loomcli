package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var pushAll bool
var pushWorkspace string

var pushCmd = &cobra.Command{
	Use:               "push [worktree] [target]",
	Short:             "Push worktree branch to target branch",
	Aliases:           []string{"merge"},
	GroupID:           "git",
	ValidArgsFunction: branchCompletion,
	Long: `Push completed work from worktree branches to target branch.

Merges worktree branch INTO the target branch (e.g., main), pushing
completed work. If conflicts occur, Claude is launched to resolve them.

Arguments:
  worktree    Source worktree to push from (e.g., falcon)
  target      Target branch to push into (default: main or per-repo default)

Flags:
  -a, --all          Push all worktree branches to target
  -W, --workspace    Workspace to operate on (workspace mode only)

Examples:
  loom push falcon                        # Push falcon to main (or per-repo default)
  loom push falcon main                   # Push falcon to main explicitly
  loom push --all                         # Push all worktrees to their default targets
  loom push --all main                    # Push all worktrees to main
  loom push -W myworkspace falcon         # Push in specific workspace`,
	Args: func(cmd *cobra.Command, args []string) error {
		if IsWorkspaceMode() {
			if pushAll {
				if len(args) > 1 {
					return fmt.Errorf("--all flag accepts at most 1 argument (target branch)")
				}
				return nil
			}
			if len(args) < 1 || len(args) > 2 {
				return fmt.Errorf("requires 1-2 arguments: <worktree> [target]")
			}
			return nil
		}
		// Legacy mode
		if pushAll {
			if len(args) != 1 {
				return fmt.Errorf("--all flag requires exactly 1 argument (target branch)")
			}
			return nil
		}
		if len(args) != 2 {
			return fmt.Errorf("requires exactly 2 arguments: <source> <target>")
		}
		return nil
	},
	RunE: runPush,
}

func init() {
	pushCmd.Flags().BoolVarP(&pushAll, "all", "a", false, "Push all worktree branches to target")
	pushCmd.Flags().StringVarP(&pushWorkspace, "workspace", "W", "", "Workspace to operate on")
	rootCmd.AddCommand(pushCmd)
}

func runPush(cmd *cobra.Command, args []string) error {
	deps := GetDeps(cmd)
	all, _ := cmd.Flags().GetBool("all")
	ws, _ := cmd.Flags().GetString("workspace")

	if IsWorkspaceMode() {
		if all && ws != "" {
			fmt.Fprintln(os.Stderr, "Error: --all and --workspace are mutually exclusive")
			os.Exit(1)
		}

		targetBranch := ""
		sourceBranch := ""

		if all {
			if len(args) == 1 {
				targetBranch = args[0]
			}
			pushAllWorkspaces(deps, targetBranch)
		} else {
			sourceBranch = args[0]
			if len(args) == 2 {
				targetBranch = args[1]
			}

			resolver, err := NewResolver()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error creating resolver: %v\n", err)
				os.Exit(1)
			}

			wsName := ws
			if wsName != "" {
				if err := resolver.SetWorkspace(wsName); err != nil {
					available := resolver.WorkspaceNames()
					fmt.Fprintf(os.Stderr, "Error: workspace %q not found. Available: %v\n", wsName, available)
					os.Exit(1)
				}
			}

			pushWorkspaceRepos(deps, resolver, sourceBranch, targetBranch)
		}
		return nil
	}

	// Legacy mode
	if all {
		pushAllWorktrees(deps, args[0])
	} else {
		pushBranch(deps, args[0], args[1])
	}
	return nil
}
