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
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/managementapi"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
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
	runRunner          string
	runRepoURL         string
	runBaseBranch      string
	runOpenPR          bool
	runStackedPRs      bool
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
	Use:               "run",
	Short:             "Drain an epic by running the epic-runner workflow",
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	Long: `Queue or run the epic-runner workflow for an epic.

The command records a durable DriverRun for a workflow. Lead assignment,
preflight, and child-task orchestration are handled by the workflow itself. By
default the built-in epic-runner workflow is used; pass --workflow to run
another registered workflow with the same payload shape.`,
	RunE: runEpicRun,
}

type epicRunManagement interface {
	Workspace() string
	SubmitDriverRun(context.Context, managementapi.SubmitDriverRunRequest) (*domain.DriverRun, error)
	GetDriverRun(context.Context, string) (*domain.DriverRun, error)
}

var newEpicRunManagementClient = func(ctx context.Context) (epicRunManagement, error) {
	return managementapi.New(ctx, "loom epic run")
}

func init() {
	epicRunCmd.Flags().StringVar(&runParent, "parent", "", "Epic ID to drain (required)")
	_ = epicRunCmd.MarkFlagRequired("parent")
	epicRunCmd.Flags().IntVar(&runMaxConcurrency, "max-concurrency", 2, "Maximum simultaneous workers spawned by this run")
	epicRunCmd.Flags().StringVar(&runWorkerPrefix, "worker-prefix", "", "Optional worker profile prefix for spawned workers")
	epicRunCmd.Flags().IntVar(&runIntervalSeconds, "interval-seconds", 5, "Seconds between reconcile passes")
	epicRunCmd.Flags().StringVar(&runNodeID, "node-id", "", "Daemon node ID to run spawned workers on (default: the single active local node)")
	epicRunCmd.Flags().StringVar(&runLead, "lead", "", "Lead agent running this epic (default: $LOOM_AGENT_NAME when set)")
	epicRunCmd.Flags().StringVar(&runWorkflow, "workflow", workflowcatalog.BuiltinEpicRunnerWorkflowName, "Workflow name to run")
	epicRunCmd.Flags().StringVar(&runRunner, "runner", "local-task-runner", "Runner requested for child task runs")
	epicRunCmd.Flags().StringVar(&runRepoURL, "repo-url", "", "Repository URL passed to compatible task runners")
	epicRunCmd.Flags().StringVar(&runBaseBranch, "base-branch", "", "Base branch passed to compatible task runners")
	epicRunCmd.Flags().BoolVar(&runOpenPR, "open-pull-request", false, "Ask compatible task runners to open pull requests")
	epicRunCmd.Flags().BoolVar(&runStackedPRs, "stacked-pull-requests", false, "Ask compatible task runners to stack child pull requests")
	epicRunCmd.Flags().BoolVar(&runDetach, "detach", false, "Queue the workflow run and return without executing it in this process")
	epicRunCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "Print what would be spawned but don't actually create agents")

	epicCmd.AddCommand(epicRunCmd)
	cli.RegisterCommand(epicCmd)
}

//nolint:funlen // The command wires validation, queueing, optional projection, execution, and post-drain publish.
func runEpicRun(cmd *cobra.Command, _ []string) error {
	if err := validateEpicRunFlags(); err != nil {
		return err
	}

	ctx, cancel := signalContext(cmd.Context())
	defer cancel()

	management, err := newEpicRunManagementClient(ctx)
	if err != nil {
		return err
	}
	ws := management.Workspace()

	workflowName := epicRunWorkflow()

	runID := fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())

	// Stacked mode: project the epic's blocks DAG into the per-user stackstore
	// before queueing so the workflow payload can carry the same lineage to
	// sandboxed runners that cannot read the host stack store.
	var stackProj *EpicStackProjection
	if runStackedPRs && !runDryRun {
		handle, openErr := cmdstore.OpenStore(ctx)
		if openErr != nil {
			return fmt.Errorf("open store for stack projection: %w", openErr)
		}
		defer func() { _ = handle.Close() }()
		if proj, perr := projectEpicStackForRun(ctx, handle, ws, runParent, runID, runRepoURL, runBaseBranch); perr != nil {
			fmt.Printf("[epic-run] WARN: stack projection skipped (tasks will base on the repo default branch): %v\n", perr)
		} else {
			stackProj = proj
			fmt.Printf("[epic-run] projected stack %s on %s@%s: %d task(s) — %d chained, %d root(s) (%d fan-in, %d fan-out breaks); %d new\n",
				proj.StackID, proj.RepoName, proj.RootBase, proj.Stats.Tasks, proj.Stats.LinearLinks, proj.Stats.Roots,
				proj.Stats.FanInBreaks, proj.Stats.FanOutBreaks, len(proj.Created))
		}
	}

	payload, err := workflowPayload(stackProj)
	if err != nil {
		return err
	}
	if runDryRun {
		printDryRunPayload(payload)
		if workflowName != workflowcatalog.BuiltinEpicRunnerWorkflowName {
			return nil
		}
	}

	run, err := queueEpicWorkflowRun(ctx, management, workflowName, runID, payload)
	if err != nil {
		return err
	}
	fmt.Printf("[epic-run] queued workflow %s run %s for epic %s\n", workflowName, run.RunID, runParent)

	if runDetach && !runDryRun {
		return nil
	}
	if err := executeWorkflowRun(ctx, management, run.RunID); err != nil {
		return err
	}

	// Stage 4 post-drain reconcile: the epic drained successfully, so every task
	// pushed its canonical branch. Publish the stack as stacked PRs (each PR's
	// base = its predecessor's branch). Fail-open: the branches are on origin, so
	// a reconcile error is a warning — re-runnable via `loom stack publish`.
	if stackProj != nil {
		if rerr := reconcileEpicStack(ctx, ws, stackProj); rerr != nil {
			fmt.Printf("[epic-run] WARN: stack reconcile skipped (branches are pushed; run `loom stack publish %s`): %v\n", stackProj.StackID, rerr)
		}
	}
	return nil
}

func queueEpicWorkflowRun(ctx context.Context, management epicRunManagement, workflowName, runID string, payload json.RawMessage) (*domain.DriverRun, error) {
	run, err := management.SubmitDriverRun(ctx, managementapi.SubmitDriverRunRequest{
		CLICommand: "epic-run", DriverRef: workflowName, RunID: runID,
		Entrypoint: "run", EpicID: runParent, Payload: payload,
	})
	if err != nil {
		return nil, fmt.Errorf("create epic workflow run: %w", err)
	}
	return run, nil
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
	return strings.TrimSpace(runWorkerPrefix)
}

func workflowPayload(stackProj *EpicStackProjection) (json.RawMessage, error) {
	payload := map[string]any{
		"epicId":                runParent,
		"leadName":              resolveLeadName(runLead),
		"orchestratorSessionId": strings.TrimSpace(os.Getenv(envOrchestratorSessionID)),
		"maxConcurrency":        runMaxConcurrency,
		"workerPrefix":          epicRunWorkerPrefix(),
		"intervalSeconds":       runIntervalSeconds,
		"runner":                runRunner,
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
	if strings.TrimSpace(runRepoURL) != "" {
		payload["repoUrl"] = strings.TrimSpace(runRepoURL)
	}
	if strings.TrimSpace(runBaseBranch) != "" {
		payload["baseBranch"] = strings.TrimSpace(runBaseBranch)
	}
	if runOpenPR {
		payload["openPullRequest"] = true
	}
	if runStackedPRs {
		payload["stackedPullRequests"] = true
	}
	if stackProj != nil && len(stackProj.Lineage) > 0 {
		payload["stackLineage"] = stackProj.Lineage
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

func executeWorkflowRun(ctx context.Context, management epicRunManagement, runID string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		run, err := management.GetDriverRun(ctx, runID)
		if err != nil {
			return fmt.Errorf("observe workflow run %s: %w", runID, err)
		}
		if run == nil || strings.TrimSpace(run.RunID) == "" {
			return fmt.Errorf("observe workflow run %s returned no state", runID)
		}
		if run.Status.IsTerminal() {
			fmt.Printf("[epic-run] workflow run %s finished: %s\n", runID, run.Status)
			if run.Summary != "" {
				fmt.Printf("[epic-run] %s\n", run.Summary)
			}
			if run.Status == domain.DriverRunFailed {
				return fmt.Errorf("epic workflow run %s failed: %s", runID, run.Summary)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func epicRunWorkflow() string {
	if workflow := strings.TrimSpace(runWorkflow); workflow != "" {
		return workflow
	}
	return workflowcatalog.BuiltinEpicRunnerWorkflowName
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
