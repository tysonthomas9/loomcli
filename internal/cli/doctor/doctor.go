// Package doctor implements `loom doctor`, the environment and health preflight.
// It runs the individual checks — git, tmux, redis, the configured agent-backend
// CLI, fleet-db reachability and the resolved issue backend, plus stale locks,
// stuck daemons, orphaned tmux sessions and transcripts — and reports them as
// pass/warn/fail text or JSON. Registered on the root command from init(); only
// cmd/loom/main.go imports it, for that side effect.
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// CheckStatus represents the outcome of a doctor check.
type CheckStatus int

const (
	StatusPass CheckStatus = iota
	StatusWarn
	StatusFail
)

func (s CheckStatus) String() string {
	switch s {
	case StatusPass:
		return "pass"
	case StatusWarn:
		return "warn"
	case StatusFail:
		return "fail"
	default:
		return "unknown"
	}
}

func (s CheckStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// CheckResult holds the outcome of a single doctor check.
type CheckResult struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Summary string      `json:"summary"`
	Detail  string      `json:"detail,omitempty"`
}

// DoctorOutput is the top-level JSON output for loom doctor.
type DoctorOutput struct {
	Checks  []CheckResult `json:"checks"`
	Summary DoctorSummary `json:"summary"`
}

// DoctorSummary tallies check results.
type DoctorSummary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

var doctorJSON bool
var doctorFix bool

var doctorCmd = &cobra.Command{
	Use:     "doctor",
	Short:   "Check loom installation and configuration health",
	GroupID: "workspace",
	Long: `Diagnose the health of your loom installation, configuration, and runtime.

Runs a series of checks covering prerequisite tools, daemon status,
configuration validity, worktree state, and connectivity. Reports
actionable pass/warn/fail results.

Examples:
  loom doctor              # Human-readable health report
  loom doctor --json       # Machine-readable JSON output`,
	Args: cobra.NoArgs,
	RunE: runDoctor,
	// Override PersistentPreRunE: doctor must run even when the backend
	// binary is missing (that is one of the things it diagnoses).
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		lf, _ := cmd.Flags().GetString("log-format")
		lo, _ := cmd.Flags().GetString("log-output")
		return cli.InitLogger(lf, lo)
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Output in JSON format")
	doctorCmd.Flags().BoolVar(&doctorFix, "fix", false, "Automatically fix issues where possible")
	cli.RegisterCommand(doctorCmd)
}

// checkFunc is the signature for individual doctor checks.
type checkFunc func() CheckResult

func runDoctor(cmd *cobra.Command, args []string) error {
	checks := collectDoctorChecks(cmd)

	var results []CheckResult
	for _, check := range checks {
		result := check()
		if result.Name == "" {
			continue
		}
		results = append(results, result)
	}

	summary := tallyResults(results)
	output := DoctorOutput{Checks: results, Summary: summary}

	if doctorJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(output); err != nil {
			return err
		}
	} else {
		renderDoctorHuman(output)
	}

	if summary.Fail > 0 {
		cmd.SilenceErrors = true
		return fmt.Errorf("doctor found %d failure(s)", summary.Fail)
	}
	return nil
}

// collectDoctorChecks assembles the list of checks based on active backends.
func collectDoctorChecks(cmd *cobra.Command) []checkFunc {
	deps := cli.GetDeps(cmd)
	checks := []checkFunc{
		func() CheckResult { return checkGit(deps) },
		func() CheckResult { return checkGitRepo(deps) },
		func() CheckResult { return checkTmux(deps) },
		checkIssueBackend,
	}
	if cli.IsFleetActive() {
		checks = append(checks, checkFleet)
	} else {
		checks = append(checks, checkFleetDB)
	}
	checks = append(checks, checkBackendCLI, checkProjectConfig, checkGlobalConfig,
		checkWorktrees, checkStaleLocks, checkStaleSignalFiles, checkStaleSessionRecords,
		checkOrphanedTranscripts, checkOrphanedTmuxSessions, checkLoomDaemon, checkDaemonStuck, checkRedis,
		func() CheckResult { return checkOrphanedFleetLocks(deps) })
	return checks
}

// tallyResults counts pass/warn/fail across all check results.
func tallyResults(results []CheckResult) DoctorSummary {
	var summary DoctorSummary
	for _, r := range results {
		switch r.Status {
		case StatusPass:
			summary.Pass++
		case StatusWarn:
			summary.Warn++
		case StatusFail:
			summary.Fail++
		}
	}
	return summary
}

func renderDoctorHuman(output DoctorOutput) {
	fmt.Println("Loom Doctor")
	fmt.Println("===========")
	fmt.Println()

	for _, r := range output.Checks {
		var icon string
		switch r.Status {
		case StatusPass:
			icon = "\u2713" // check mark
		case StatusWarn:
			icon = "\u26a0" // warning
		case StatusFail:
			icon = "\u2717" // x mark
		}
		fmt.Printf("%s %s\n", icon, r.Summary)
		if r.Detail != "" {
			for _, line := range strings.Split(r.Detail, "\n") {
				fmt.Printf("  \u2192 %s\n", line)
			}
		}
	}

	fmt.Printf("\n%d checks passed, %d warnings, %d failures\n",
		output.Summary.Pass, output.Summary.Warn, output.Summary.Fail)
}
