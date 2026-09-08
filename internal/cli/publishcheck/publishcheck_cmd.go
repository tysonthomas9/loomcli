package publishcheck

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// divergedExitCode is what a caller tests for: the branch cannot be published
// as-is. It is distinct from 1 (a genuine error) on purpose, so a script can
// tell "this needs an escalation" from "the command could not run".
const divergedExitCode = 3

var (
	checkDir    string
	checkBranch string
	checkOutput string
)

var publishCheckCmd = &cobra.Command{
	Use:     "publish-check <TASK-ID>",
	Short:   "State how a local task branch relates to its published head",
	GroupID: "workspace",
	Args:    cobra.ExactArgs(1),
	Long: `Report whether loom/<TASK-ID> can be published, and name the condition when it
cannot.

Verdicts:

  unpublished    no origin/<branch> in this clone: an ordinary first publish
  identical      origin == local: the push is a no-op
  fast-forward   origin is an ancestor of local: publishable
  behind         local is an ancestor of origin: someone published ahead of you
  diverged       neither is an ancestor of the other: the push will be refused

Exit codes: 0 for unpublished/identical/fast-forward, 3 for diverged or behind,
1 for a genuine error (bad ref, git missing, no local branch).

NOTE: ` + "`go run`" + ` collapses exit codes — it reports 1 for any non-zero exit of the
program it runs. Call the BUILT binary when the exit code matters.

This command performs NO network access: it reads the remote-tracking refs this
clone already has, and prints ` + "`fetched: false`" + ` so a stale read is visible rather
than silent. Fetch first if freshness matters.`,
	RunE: runPublishCheck,
}

func init() {
	publishCheckCmd.Flags().StringVar(&checkDir, "dir", ".", "Repository worktree to inspect")
	publishCheckCmd.Flags().StringVar(&checkBranch, "branch", "", "Branch to check (default: loom/<TASK-ID>)")
	publishCheckCmd.Flags().StringVarP(&checkOutput, "output", "o", "text", "Output format: text or json")
	cli.RegisterCommand(publishCheckCmd)
}

func runPublishCheck(cmd *cobra.Command, args []string) error {
	if checkOutput != "text" && checkOutput != "json" {
		return fmt.Errorf("invalid --output %q: want text or json", checkOutput)
	}
	taskID := args[0]

	// branch_pattern is `loom/{task_id}` for every repo in the contract, so this
	// is deliberately not parsed out of integration.yaml. --branch is the escape
	// hatch if a repo ever diverges, and is also how a revision ref that is
	// itself diverged gets checked.
	branch := checkBranch
	if branch == "" {
		branch = "loom/" + taskID
	}

	res, err := NewCheck(checkDir, branch, taskID)
	if err != nil {
		return err
	}
	if err := writeResult(cmd.OutOrStdout(), res); err != nil {
		return err
	}

	if res.Verdict == VerdictDiverged || res.Verdict == VerdictBehind {
		// Cobra's RunE error yields exit 1, so 3 has to be set explicitly. The
		// output is already flushed above.
		os.Exit(divergedExitCode)
	}
	return nil
}

func writeResult(w io.Writer, res Result) error {
	if checkOutput == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	return writeCheckFacts(w, res)
}

// writeCheckFacts prints one aligned label per fact and closes with the sentence
// the integrator pastes into its escalation.
func writeCheckFacts(w io.Writer, res Result) error {
	facts := [][2]string{
		{"task", res.TaskID},
		{"branch", res.Branch},
		{"remote ref", orNone(res.RemoteRef)},
		{"local sha", orNone(res.LocalSHA)},
		{"published sha", orNone(res.PublishedSHA)},
		{"verdict", string(res.Verdict)},
		{"revisions", orNone(joinOrNone(res.Revisions))},
		{"next revision", orNone(res.NextRevision)},
		{"fetched", fmt.Sprintf("%t (no network; refs are whatever this clone already had)", res.Fetched)},
	}
	for _, f := range facts {
		if _, err := fmt.Fprintf(w, "%-14s %s\n", f[0], f[1]); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\n%s\n", res.Detail)
	return err
}

func joinOrNone(refs []string) string {
	out := ""
	for i, r := range refs {
		if i > 0 {
			out += ", "
		}
		out += r
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
