package git

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	gh "github.com/tysonthomas9/loomcli/internal/github"
)

var prNumberOrURL = regexp.MustCompile(`^(?:[0-9]+|https://github\.com/[^/]+/[^/]+/pull/[0-9]+)$`)

// mergeCmd deliberately delegates the merge decision to GitHub. In particular,
// it must not merge a local checkout or fall back to a git merge.
var mergeCmd = &cobra.Command{
	Use:     "merge <pr-number-or-url>",
	Short:   "Merge a GitHub pull request",
	GroupID: "git",
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) != 1 || !prNumberOrURL.MatchString(args[0]) {
			return fmt.Errorf("requires one GitHub pull request number or URL")
		}
		return nil
	},
	RunE: runMerge,
}

var mergeSquash, mergeCommit, mergeRebase bool

func init() {
	mergeCmd.Flags().BoolVar(&mergeSquash, "squash", false, "squash merge")
	mergeCmd.Flags().BoolVar(&mergeCommit, "merge", false, "merge commit")
	mergeCmd.Flags().BoolVar(&mergeRebase, "rebase", false, "rebase merge")
	cli.RegisterCommand(mergeCmd)
}

func runMerge(cmd *cobra.Command, args []string) error {
	selected := 0
	for _, enabled := range []bool{mergeSquash, mergeCommit, mergeRebase} {
		if enabled {
			selected++
		}
	}
	if selected != 1 {
		return fmt.Errorf("exactly one of --squash, --merge, or --rebase is required")
	}
	deps := cli.GetDeps(cmd)
	if err := checkGhInstalled(deps); err != nil {
		return err
	}
	if err := gh.PRProtection(cmd.Context(), func(_ context.Context, name string, args ...string) (string, string, error) {
		result := deps.Exec.Run(".", name, args...)
		return result.Stdout, result.Stderr, result.Err
	}, args[0]); err != nil {
		return err
	}
	method := "--squash"
	if mergeCommit {
		method = "--merge"
	} else if mergeRebase {
		method = "--rebase"
	}
	result := deps.Exec.Run(".", "gh", "pr", "merge", args[0], method)
	if result.Err != nil {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = result.Err.Error()
		}
		return fmt.Errorf("gh pr merge failed: %s", msg)
	}
	fmt.Printf("Merged pull request %s via %s\n", args[0], strings.TrimPrefix(method, "--"))
	return nil
}
