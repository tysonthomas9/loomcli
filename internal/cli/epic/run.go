// Package epic provides the `loom epic` command tree. The first subcommand -
// `loom epic run` - is a foreground reconcile loop that drives an epic to
// completion by spawning one ephemeral worker per ready task. Lead/orchestrator
// AIs invoke this from chat ("take the auth epic") to fan out work; humans can
// also run it directly. Workers spawned by this command inherit the lead's
// LOOM_ORCHESTRATOR_SESSION_ID env var so the UI groups them under their lead.
package epic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	envAgentName             = "LOOM_AGENT_NAME"
	envOrchestratorSessionID = "LOOM_ORCHESTRATOR_SESSION_ID"
)

var (
	runParent          string
	runMaxConcurrency  int
	runWorkerPrefix    string
	runIntervalSeconds int
	runRole            string
	runDryRun          bool
	runNodeID          string
	runLead            string

	epicSignalContextFn       = signalContext
	epicOpenStoreFn           = cmdstore.OpenStore
	epicResolveWorkspaceKeyFn = bootstrap.ResolveActiveWorkspaceKey
	epicDefaultIssueBackendFn = cli.DefaultIssueBackend
	epicNewRunnerFromFlagsFn  = newRunnerFromFlags
	epicRunConfiguredRunnerFn = runConfiguredRunner
)

var epicCmd = &cobra.Command{
	Use:     "epic",
	Short:   "Manage epic-scoped work",
	GroupID: "workspace",
	Long: `Group commands for working with an epic and its child tasks.

Today this is a single subcommand:
  loom epic run --parent <epic-id>   drain the epic by fanning out ephemeral workers`,
}

var epicRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Drain an epic by spawning one ephemeral worker per ready task",
	Long: `Reconcile loop that drives an epic to completion.

Every interval the runner queries the issue backend for tasks under the epic
that are ready (have no open blockers). For each ready task that does not
already have a worker, the runner creates an ephemeral agent pinned to that
task. As tasks close, downstream tasks become ready and the runner spawns
workers for them on the next tick. The loop exits when no ready, in-progress,
or blocked work remains under the epic.

This command runs in the foreground. Workers it spawns are independent - if
this process exits early they keep running and the daemon supervises them
normally. Re-running the command with the same --parent picks up where it
left off (worker names are deterministic per task ID, so duplicates are no-ops).

Worker attribution: if LOOM_ORCHESTRATOR_SESSION_ID is set in the environment
(loom lead injects it), spawned workers are attributed to that orchestrator
session. The UI uses this to group "the workers Nova is coordinating".`,
	RunE: runEpicRun,
}

func init() {
	epicRunCmd.Flags().StringVar(&runParent, "parent", "", "Epic ID to drain (required)")
	_ = epicRunCmd.MarkFlagRequired("parent")
	epicRunCmd.Flags().IntVar(&runMaxConcurrency, "max-concurrency", 2, "Maximum simultaneous workers spawned by this run")
	epicRunCmd.Flags().StringVar(&runWorkerPrefix, "worker-prefix", "", "Prefix for spawned worker names (default derived from --parent)")
	epicRunCmd.Flags().IntVar(&runIntervalSeconds, "interval-seconds", 5, "Seconds between reconcile passes")
	epicRunCmd.Flags().StringVar(&runRole, "role", "task", "Role to spawn workers under")
	epicRunCmd.Flags().StringVar(&runNodeID, "node-id", "", "Daemon node ID to run spawned workers on (default: the single active local node)")
	epicRunCmd.Flags().StringVar(&runLead, "lead", "", "Lead agent running this epic (default: $LOOM_AGENT_NAME when set)")
	epicRunCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "Print what would be spawned but don't actually create agents")

	epicCmd.AddCommand(epicRunCmd)
	cli.RegisterCommand(epicCmd)
}

func runEpicRun(cmd *cobra.Command, _ []string) error {
	if err := validateEpicRunFlags(); err != nil {
		return err
	}

	ctx, cancel := epicSignalContextFn(cmd.Context())
	defer cancel()

	handle, err := epicOpenStoreFn(ctx)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = handle.Close() }()

	ws, err := epicResolveWorkspaceKeyFn(ctx, handle.Store.Workspaces())
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}

	ib := epicDefaultIssueBackendFn()
	if ib == nil {
		return errors.New("no issue backend available")
	}

	r, err := epicNewRunnerFromFlagsFn(ctx, handle.Store, ib, ws)
	if err != nil {
		return err
	}
	return epicRunConfiguredRunnerFn(ctx, r)
}

func runConfiguredRunner(ctx context.Context, r *epicrunner.Runner) error {
	r.PrintHeader()
	return r.RunLoop(ctx)
}

func validateEpicRunFlags() error {
	if strings.TrimSpace(runParent) == "" {
		return errors.New("--parent is required")
	}
	if runMaxConcurrency < 1 {
		return fmt.Errorf("--max-concurrency must be >= 1, got %d", runMaxConcurrency)
	}
	if runIntervalSeconds < 1 {
		return fmt.Errorf("--interval-seconds must be >= 1, got %d", runIntervalSeconds)
	}
	return nil
}

func epicRunWorkerPrefix() string {
	if runWorkerPrefix != "" {
		return runWorkerPrefix
	}
	return epicrunner.SanitizePrefix(runParent)
}

func newRunnerFromFlags(ctx context.Context, st store.Store, ib backend.IssueBackend, ws string) (*epicrunner.Runner, error) {
	orchestratorID := strings.TrimSpace(os.Getenv(envOrchestratorSessionID))
	r, result, err := epicrunner.NewRunner(ctx, epicrunner.RunnerConfig{
		Store:                 st,
		IssueBackend:          ib,
		WorkspaceKey:          ws,
		EpicID:                runParent,
		LeadName:              resolveLeadName(runLead),
		Role:                  runRole,
		Backend:               strings.TrimSpace(cli.GetBackendName()),
		WorkerPrefix:          epicRunWorkerPrefix(),
		MaxConcurrency:        runMaxConcurrency,
		Interval:              time.Duration(runIntervalSeconds) * time.Second,
		OrchestratorSessionID: orchestratorID,
		TargetNodeID:          runNodeID,
		DryRun:                runDryRun,
		MutateLead:            !runDryRun,
		PrepareWorktrees:      true,
		Out:                   os.Stdout,
		ErrOut:                os.Stderr,
	})
	if err != nil {
		return nil, err
	}
	if runDryRun && result != nil && result.Lead != nil && result.Lead.Parent == "" {
		fmt.Printf("[epic-run] DRY-RUN would assign lead %s to epic %s\n", result.LeadName, runParent)
	}
	return r, nil
}

func resolveLeadName(flagValue string) string {
	if lead := strings.TrimSpace(flagValue); lead != "" {
		return lead
	}
	return strings.TrimSpace(os.Getenv(envAgentName))
}

// signalContext returns a context cancelled by Ctrl-C / SIGTERM so the loop
// exits cleanly on user interrupt without abandoning workers.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sig)
	}()
	return ctx, cancel
}
