package uniondebt

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
)

var (
	sweepContract string
	sweepRepos    []string
	sweepDryRun   bool
	sweepLimit    int
	sweepPRDrift  bool
	sweepPRDerive bool
	sweepPRLimit  int
	sweepOutput   string
)

var unionDebtCmd = &cobra.Command{
	Use:     "union-debt",
	Short:   "Inspect and drain the union-pending debt ledger",
	GroupID: "workspace",
	Long: `Work with the union-pending ledger.

A union-pending marker means code sits on a branch that is NOT in the local
union branch, and so is absent from the build this machine runs. The marker
outlives its ticket, and a closed ticket is unreachable by every agent, so the
debt needs materializing as work somebody can actually claim.`,
}

var sweepCmd = &cobra.Command{
	Use:   "sweep",
	Short: "Probe the union-pending ledger and file claimable debt tickets",
	Long: `Read the union-pending ledger, probe each ticket's branch against its repo's
union branch locally, and act on what it finds:

  already in union   remove the marker, comment; file nothing
  no branch found    swap the marker for union-unreachable, comment
  superseded         the branch was rebuilt, or its work already arrived by
                     another route: swap the marker for union-superseded,
                     comment, and warn any debt ticket already filed
  not in union       file a NEW open, approved ticket the integrator claims

It also runs the pr-pending clear test over every ticket carrying that marker.
The marker is cleared ONLY when a pull request exists for the ticket's branch,
GitHub reports it MERGEABLE, and its base chain reaches the repo's trunk
through open, mergeable PRs. GitHub computes mergeability lazily, so UNKNOWN is
re-polled with bounded backoff and is never read as mergeable; CONFLICTING, a
missing PR and a chain that stops short all keep the marker.

It also DERIVES that ledger rather than waiting for a human to maintain it.
The apply pass enumerates the tasks the local union branch carries that the
trunk does not, and stamps the marker on every one whose work is not on the
trunk and whose pull request cannot land today. That rule is the exact
complement of the clear test above — the same PR check, read once per ticket,
in the opposite direction — so one run can never clear a marker and re-apply it
to the same ticket. A task whose PR CAN land is reported, not labeled, and the
report's census prints both counts so the difference is readable without
post-processing.

The sweep never merges, never claims and never reopens: the closed original is
touched only through its labels and comments. It also never runs git fetch —
it reads the refs the clone already has and records the probe time and tip SHA
in what it writes, so a stale read is visible rather than silent.`,
	RunE: runSweep,
}

func init() {
	sweepCmd.Flags().StringVar(&sweepContract, "contract", "", "Path to integration.yaml (default: <workspace>/integration.yaml, then ./integration.yaml)")
	sweepCmd.Flags().StringSliceVar(&sweepRepos, "repo", nil, "Restrict the sweep to these source repos (repeatable)")
	sweepCmd.Flags().BoolVar(&sweepDryRun, "dry-run", false, "Classify and print without writing anything")
	sweepCmd.Flags().IntVar(&sweepLimit, "limit", 10, "Maximum debt tickets to file per run (0 = unlimited)")
	sweepCmd.Flags().BoolVar(&sweepPRDrift, "pr-drift", true, "Also report tickets that fail the pr-pending test while carrying no marker (read-only)")
	sweepCmd.Flags().BoolVar(&sweepPRDerive, "pr-derive", true, "Derive the pr-pending ledger from the union branch: apply the marker to union-merged work that cannot land")
	sweepCmd.Flags().IntVar(&sweepPRLimit, "pr-limit", 200, "Maximum pr-pending markers to apply per run (0 = unlimited)")
	sweepCmd.Flags().StringVarP(&sweepOutput, "output", "o", "text", "Output format: text or json")
	unionDebtCmd.AddCommand(sweepCmd)
	cli.RegisterCommand(unionDebtCmd)
}

func runSweep(cmd *cobra.Command, _ []string) error {
	if sweepOutput != "text" && sweepOutput != "json" {
		return fmt.Errorf("invalid --output %q: want text or json", sweepOutput)
	}

	if err := preflightBackend(); err != nil {
		return err
	}

	path, err := resolveContractPath(sweepContract)
	if err != nil {
		return err
	}
	contract, err := LoadContract(path)
	if err != nil {
		return err
	}

	sweeper := NewSweeper(cli.DefaultIssueBackend(), nil, Options{
		Contract: contract,
		Repos:    sweepRepos,
		DryRun:   sweepDryRun,
		Limit:    sweepLimit,
		PRDrift:  sweepPRDrift,
		PRDerive: sweepPRDerive,
		PRLimit:  sweepPRLimit,
	})
	report, err := sweeper.Run(cmdstore.RootContext())
	if err != nil {
		return err
	}

	if err := printReport(cmd.OutOrStdout(), report, contract.Labels()); err != nil {
		return err
	}
	if report.Errors > 0 {
		return fmt.Errorf("%d of %d ledger items failed", report.Errors, len(report.Items))
	}
	return nil
}

// preflightBackend refuses to run a writing sweep against the HTTP API backend.
//
// The API backend's create schema (api/openapi.yaml CreateIssueRequest, and the
// client generated from it) carries no repo/source_repo field at all, so
// CreateParams.SourceRepo is silently dropped and every create fails against a
// repo-scoped workspace with "repo is required in this workspace" — an error
// that reads like a flag mistake rather than a missing schema field. Verified
// live against the running fleet on 2026-09-02. Failing here names the actual
// cause once, instead of once per ledger item.
//
// The apply pass added by PUPPET-673 writes only labels and comments
// (AddLabel/AddComment, never Create), so it is unaffected by the missing
// schema field — do not loosen this check on the strength of that. It is the
// union-debt FILING path that needs Create, and it shares this command.
//
// A dry run writes nothing, so it is allowed against any backend.
func preflightBackend() error {
	if sweepDryRun || !cli.IsAPIActive() {
		return nil
	}
	return fmt.Errorf("union-debt sweep cannot write through the HTTP API backend: " +
		"its create schema has no repo field, so source_repo is dropped and every create " +
		"fails with \"repo is required in this workspace\". " +
		"Unset LOOM_SERVER_URL (drop --server), or set LOOM_ISSUE_BACKEND=fleetdb, and retry. " +
		"Use --dry-run to classify without writing")
}

// resolveContractPath honors an explicit --contract, then the workspace root,
// then the working directory.
func resolveContractPath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	candidates := []string{
		filepath.Join(cli.GetWorkspaceRuntimeDir(), "integration.yaml"),
		"integration.yaml",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("integration.yaml not found (looked in %v); pass --contract", candidates)
}

func printReport(w io.Writer, rep *Report, lbl LabelSet) error {
	if sweepOutput == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}

	if len(rep.Items) == 0 {
		if _, err := fmt.Fprintf(w, "No %s items found — the ledger is empty.\n", lbl.Marker); err != nil {
			return err
		}
		// The census still prints: "nothing to act on" and "nothing in the
		// union" are different answers, and only the census distinguishes them.
		return printCensus(w, rep)
	}
	for _, it := range rep.Items {
		line := fmt.Sprintf("%-14s %-14s %-12s %s", it.OriginID, it.Repo, it.Class, it.Action)
		if it.PR != 0 {
			line += fmt.Sprintf(" #%d", it.PR)
		}
		if it.MergeSHA != "" {
			line += " @" + abbrev(it.MergeSHA)
		}
		if it.DerivedID != "" {
			line += " -> " + it.DerivedID
		}
		if it.ErrMessage != "" {
			line += ": " + it.ErrMessage
		} else if it.Detail != "" {
			line += ": " + it.Detail
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if err := printCensus(w, rep); err != nil {
		return err
	}
	verb := "acted on"
	if sweepDryRun {
		verb = "would act on"
	}
	_, err := fmt.Fprintf(w, "\n%s %d item(s), %d error(s).\n", verb, len(rep.Items), rep.Errors)
	return err
}

// censusRow is one line of the derived census. The letters are the ticket's
// own A-F buckets, kept verbatim so a reader can check the set differences
// against the ticket without post-processing the JSON.
type censusRow struct {
	letter string
	label  string
	count  func(Census) int
	note   string
}

var censusRows = []censusRow{
	{"A", "union-merged, no landable PR, unlabeled", func(c Census) int { return c.A }, "applied"},
	{"B", "labeled, no open PR", func(c Census) int { return c.B }, "(clear pass owns)"},
	{"C", "union-merged, unlabeled, PR can land", func(c Census) int { return c.C }, "reported only"},
	{"D", "labeled, no union merge", func(c Census) int { return c.D }, ""},
	{"E", "labeled, open PR", func(c Census) int { return c.E }, "(clear pass owns)"},
	{"F", "union-merged and already on the trunk", func(c Census) int { return c.F }, "reported only"},
	{"-", "union-merged, no branch resolves", func(c Census) int { return c.NoBranch }, "reported only"},
	{"-", "union-merged, excluded by label", func(c Census) int { return c.Excluded }, "skipped"},
}

// printCensus renders the derived census. The union tip SHA is printed with
// the counts and not as a footnote: the union branch is rebuilt periodically,
// and a count without the tip it came from means nothing a week later.
func printCensus(w io.Writer, rep *Report) error {
	for _, c := range rep.Census {
		if _, err := fmt.Fprintf(w, "\nderived census (%s, union %s @ %s, trunk %s, %d union-only task(s)):\n",
			c.Repo, c.Union, abbrev(c.UnionSHA), c.Trunk, c.Merges); err != nil {
			return err
		}
		for _, row := range censusRows {
			if _, err := fmt.Fprintf(w, "  %s  %-42s %4d  %s\n",
				row.letter, row.label, row.count(c), row.note); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "     applied %d, skipped by --pr-limit %d, errors %d\n",
			c.Applied, c.Skipped, c.Errors); err != nil {
			return err
		}
	}
	return nil
}
