// Package epic provides the `loom epic` command tree. The first subcommand -
// `loom epic run` - queues the built-in epic-runner workflow.
package epic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
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
	runDryRun          bool
	runNodeID          string
	runLead            string
	runWorkflow        string
	runDetach          bool
	runProviderProfile string
)

var epicCmd = &cobra.Command{
	Use:     "epic",
	Short:   "Manage epic-scoped work",
	GroupID: "workspace",
	Long: `Group commands for working with an epic and its child tasks.

Today this is a single subcommand:
  loom epic run --parent <epic-id>   drain the epic by running the epic-runner workflow`,
}

var epicRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Drain an epic by running the epic-runner workflow",
	Long: `Queue or run the epic-runner workflow for an epic.

The command records a durable DriverRun for a workflow. Lead assignment,
preflight, and child-task orchestration are handled by the workflow itself. By
default the built-in epic-runner workflow is used; pass --workflow to run
another registered workflow with the same payload shape.`,
	RunE: runEpicRun,
}

func init() {
	epicRunCmd.Flags().StringVar(&runParent, "parent", "", "Epic ID to drain (required)")
	_ = epicRunCmd.MarkFlagRequired("parent")
	epicRunCmd.Flags().IntVar(&runMaxConcurrency, "max-concurrency", 2, "Maximum simultaneous workers spawned by this run")
	epicRunCmd.Flags().StringVar(&runWorkerPrefix, "worker-prefix", "", "Prefix for spawned worker names (default derived from --parent)")
	epicRunCmd.Flags().IntVar(&runIntervalSeconds, "interval-seconds", 5, "Seconds between reconcile passes")
	epicRunCmd.Flags().StringVar(&runNodeID, "node-id", "", "Daemon node ID to run spawned workers on (default: the single active local node)")
	epicRunCmd.Flags().StringVar(&runLead, "lead", "", "Lead agent running this epic (default: $LOOM_AGENT_NAME when set)")
	epicRunCmd.Flags().StringVar(&runWorkflow, "workflow", workflowdefs.BuiltinEpicRunnerWorkflowName, "Workflow name to run")
	epicRunCmd.Flags().StringVar(&runProviderProfile, "provider-profile", "flue-local", "Provider profile requested for child task runs")
	epicRunCmd.Flags().BoolVar(&runDetach, "detach", false, "Queue the workflow run and return without executing it in this process")
	epicRunCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "Print what would be spawned but don't actually create agents")

	epicCmd.AddCommand(epicRunCmd)
	cli.RegisterCommand(epicCmd)
}

func runEpicRun(cmd *cobra.Command, _ []string) error {
	if err := validateEpicRunFlags(); err != nil {
		return err
	}

	ctx, cancel := signalContext(cmd.Context())
	defer cancel()

	handle, err := cmdstore.OpenStore(ctx)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = handle.Close() }()

	ws, err := bootstrap.ResolveActiveWorkspaceKey(ctx, handle.Store.Workspaces())
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}

	payload, err := workflowPayload()
	if err != nil {
		return err
	}
	workflowName := epicRunWorkflow()
	if runDryRun {
		printDryRunPayload(payload)
		if workflowName != workflowdefs.BuiltinEpicRunnerWorkflowName {
			return nil
		}
	}

	if err := ensureWorkflow(ctx, handle.Store, ws, workflowName); err != nil {
		return err
	}
	driverID, err := workflowdefs.ResolveDriverID(ctx, handle.Store, ws, workflowName)
	if err != nil {
		return fmt.Errorf("resolve workflow %q: %w", workflowName, err)
	}
	run, err := driverpkg.CreateDriverRun(ctx, handle.Store, driverpkg.RunOptions{
		WorkspaceKey: ws,
		DriverID:     driverID,
		EpicID:       runParent,
		SourceKind:   "cli",
		SourceRef:    "loom epic run",
		Payload:      payload,
	})
	if err != nil {
		return fmt.Errorf("create epic workflow run: %w", err)
	}
	fmt.Printf("[epic-run] queued workflow %s run %s for epic %s\n", workflowName, run.RunID, runParent)
	if runDetach && !runDryRun {
		return nil
	}
	return executeWorkflowRun(ctx, handle.Store, ws, run.RunID)
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
	return sanitizePrefix(runParent)
}

func workflowPayload() (json.RawMessage, error) {
	payload := map[string]any{
		"epicId":                runParent,
		"leadName":              resolveLeadName(runLead),
		"orchestratorSessionId": strings.TrimSpace(os.Getenv(envOrchestratorSessionID)),
		"maxConcurrency":        runMaxConcurrency,
		"workerPrefix":          epicRunWorkerPrefix(),
		"intervalSeconds":       runIntervalSeconds,
		"providerProfile":       runProviderProfile,
		"requestedBy":           "cli",
		"dryRun":                runDryRun,
	}
	for key, value := range payload {
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			delete(payload, key)
		}
	}
	if runNodeID != "" {
		payload["targetNodeId"] = runNodeID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode workflow payload: %w", err)
	}
	return body, nil
}

func printDryRunPayload(payload json.RawMessage) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, payload, "", "  "); err != nil {
		fmt.Printf("[epic-run] DRY-RUN would queue workflow %s with payload %s\n", epicRunWorkflow(), payload)
		return
	}
	fmt.Printf("[epic-run] DRY-RUN would queue workflow %s with payload:\n%s\n", epicRunWorkflow(), pretty.String())
}

func ensureWorkflow(ctx context.Context, st store.Store, ws, workflowName string) error {
	if _, err := workflowdefs.ResolveDriverID(ctx, st, ws, workflowName); err == nil {
		return nil
	} else if workflowName != workflowdefs.BuiltinEpicRunnerWorkflowName {
		return fmt.Errorf("resolve workflow %q: %w", workflowName, err)
	}
	return workflowdefs.EnsureBuiltinWorkflow(ctx, st, ws, workflowName)
}

func executeWorkflowRun(ctx context.Context, st store.Store, ws, runID string) error {
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve work dir: %w", err)
	}
	result, err := (&driverpkg.Executor{
		Store:             st,
		WorkspaceKey:      ws,
		RunID:             runID,
		WorkDir:           workDir,
		NodeID:            runNodeID,
		HeartbeatInterval: -1,
	}).RunOnce(ctx)
	if err != nil {
		return fmt.Errorf("execute workflow run %s: %w", runID, err)
	}
	if result == nil || result.Final == nil {
		return fmt.Errorf("execute workflow run %s returned no final state", runID)
	}
	fmt.Printf("[epic-run] workflow run %s finished: %s\n", runID, result.Final.Status)
	if result.Final.Summary != "" {
		fmt.Printf("[epic-run] %s\n", result.Final.Summary)
	}
	if result.Final.Status == domain.DriverRunFailed {
		return fmt.Errorf("epic workflow run %s failed: %s", runID, result.Final.Summary)
	}
	return nil
}

func epicRunWorkflow() string {
	if workflow := strings.TrimSpace(runWorkflow); workflow != "" {
		return workflow
	}
	return workflowdefs.BuiltinEpicRunnerWorkflowName
}

func resolveLeadName(flagValue string) string {
	if lead := strings.TrimSpace(flagValue); lead != "" {
		return lead
	}
	return strings.TrimSpace(os.Getenv(envAgentName))
}

func sanitizePrefix(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if b.Len() > 0 && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "epic"
	}
	return out
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
