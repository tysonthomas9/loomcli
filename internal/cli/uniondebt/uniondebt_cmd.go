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
  not in union       file a NEW open, approved ticket the integrator claims

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
	})
	report, err := sweeper.Run(cmdstore.RootContext())
	if err != nil {
		return err
	}

	if err := printReport(cmd.OutOrStdout(), report); err != nil {
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

func printReport(w io.Writer, rep *Report) error {
	if sweepOutput == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}

	if len(rep.Items) == 0 {
		_, err := fmt.Fprintf(w, "No %s items found — the ledger is empty.\n", MarkerLabel)
		return err
	}
	for _, it := range rep.Items {
		line := fmt.Sprintf("%-14s %-14s %-12s %s", it.OriginID, it.Repo, it.Class, it.Action)
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
	verb := "acted on"
	if sweepDryRun {
		verb = "would act on"
	}
	_, err := fmt.Fprintf(w, "\n%s %d item(s), %d error(s).\n", verb, len(rep.Items), rep.Errors)
	return err
}
