// Package epic provides the `loom epic` command tree. The first subcommand —
// `loom epic run` — is a foreground reconcile loop that drives an epic to
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
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const envOrchestratorSessionID = "LOOM_ORCHESTRATOR_SESSION_ID"

var (
	runParent          string
	runMaxConcurrency  int
	runWorkerPrefix    string
	runIntervalSeconds int
	runRole            string
	runDryRun          bool
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

This command runs in the foreground. Workers it spawns are independent — if
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
	epicRunCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "Print what would be spawned but don't actually create agents")

	epicCmd.AddCommand(epicRunCmd)
	cli.RegisterCommand(epicCmd)
}

func runEpicRun(cmd *cobra.Command, _ []string) error {
	if strings.TrimSpace(runParent) == "" {
		return errors.New("--parent is required")
	}
	if runMaxConcurrency < 1 {
		return fmt.Errorf("--max-concurrency must be >= 1, got %d", runMaxConcurrency)
	}
	if runIntervalSeconds < 1 {
		return fmt.Errorf("--interval-seconds must be >= 1, got %d", runIntervalSeconds)
	}

	prefix := runWorkerPrefix
	if prefix == "" {
		prefix = sanitizePrefix(runParent)
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

	ib := cli.DefaultIssueBackend()
	if ib == nil {
		return errors.New("no issue backend available")
	}

	orchestratorID := os.Getenv(envOrchestratorSessionID)
	r := &runner{
		store:                 handle.Store,
		ib:                    ib,
		workspace:             ws,
		parent:                runParent,
		role:                  runRole,
		prefix:                prefix,
		maxConcurrency:        runMaxConcurrency,
		interval:              time.Duration(runIntervalSeconds) * time.Second,
		orchestratorSessionID: orchestratorID,
		dryRun:                runDryRun,
		spawned:               make(map[string]string),
	}
	r.printHeader()
	return r.reconcileLoop(ctx)
}

// runner is one foreground epic-run instance.
type runner struct {
	store                 store.Store
	ib                    backend.IssueBackend
	workspace             string
	parent                string
	role                  string
	prefix                string
	maxConcurrency        int
	interval              time.Duration
	orchestratorSessionID string
	dryRun                bool

	// spawned tracks task_id -> worker_name for tasks this runner has already
	// dispatched. Survives only this process; restarting the command rebuilds
	// state from fleet-db's unique-name constraint (idempotent agentdef add).
	spawned map[string]string
}

func (r *runner) printHeader() {
	fmt.Printf("Epic runner\n")
	fmt.Printf("  workspace:        %s\n", r.workspace)
	fmt.Printf("  parent epic:      %s\n", r.parent)
	fmt.Printf("  role:             %s\n", r.role)
	fmt.Printf("  worker prefix:    %s\n", r.prefix)
	fmt.Printf("  max concurrency:  %d\n", r.maxConcurrency)
	fmt.Printf("  interval:         %s\n", r.interval)
	if r.orchestratorSessionID != "" {
		fmt.Printf("  orchestrator:     %s\n", r.orchestratorSessionID)
	} else {
		fmt.Printf("  orchestrator:     (none — workers will be unattached)\n")
	}
	if r.dryRun {
		fmt.Printf("  dry-run:          true\n")
	}
	fmt.Printf("\n")
}

// reconcileLoop is the core loop: query Ready, dispatch any ungraduated tasks,
// sleep, repeat. Exits when no more ready/in-progress/blocked work remains under
// the epic.
func (r *runner) reconcileLoop(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		// Run one reconcile pass immediately, then on each tick.
		done, err := r.reconcileOnce(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[epic-run] reconcile error: %v (continuing)\n", err)
		}
		if done {
			fmt.Printf("[epic-run] epic %s drained, exiting\n", r.parent)
			return nil
		}

		select {
		case <-ctx.Done():
			fmt.Printf("[epic-run] interrupted; %d workers spawned during this run continue independently\n", len(r.spawned))
			return nil
		case <-ticker.C:
			// loop
		}
	}
}

// reconcileOnce performs one pass: fetch ready, fetch blocked, spawn for new
// ready tasks up to the concurrency cap, and report whether the epic is done.
func (r *runner) reconcileOnce(ctx context.Context) (done bool, err error) {
	ready, err := r.ib.Ready(ctx, backend.ReadyOpts{ParentID: r.parent, Limit: 256})
	if err != nil {
		return false, fmt.Errorf("ready query: %w", err)
	}
	blocked, err := r.ib.Blocked(ctx, backend.BlockedOpts{ParentID: r.parent, Limit: 256})
	if err != nil {
		return false, fmt.Errorf("blocked query: %w", err)
	}

	openOrInProgress := 0
	for _, t := range ready {
		if t.Status != "closed" && t.Status != "deferred" {
			openOrInProgress++
		}
	}
	for _, t := range blocked {
		if t.Status != "closed" && t.Status != "deferred" {
			openOrInProgress++
		}
	}

	// Drain when no work remains anywhere AND every task we spawned has settled.
	// We approximate "settled" by checking that the task isn't in ready or blocked.
	if openOrInProgress == 0 {
		return true, nil
	}

	// Spawn for new ready tasks, respecting concurrency.
	activeSpawned := r.countActiveSpawned(ctx)
	slots := r.maxConcurrency - activeSpawned
	if slots <= 0 {
		fmt.Printf("[epic-run] %d ready, %d blocked, %d active workers (at cap)\n", len(ready), len(blocked), activeSpawned)
		return false, nil
	}

	dispatched := 0
	for _, task := range ready {
		if dispatched >= slots {
			break
		}
		if _, alreadySpawned := r.spawned[task.ID]; alreadySpawned {
			continue
		}
		if task.Status == "in_progress" {
			// Someone else is already working on it (could be a peer agent or a
			// re-run of this command). Track but don't spawn.
			r.spawned[task.ID] = ""
			continue
		}
		if err := r.spawnWorker(ctx, task); err != nil {
			fmt.Fprintf(os.Stderr, "[epic-run] spawn for %s failed: %v\n", task.ID, err)
			continue
		}
		dispatched++
	}

	fmt.Printf("[epic-run] %d ready, %d blocked, dispatched %d (active %d/%d)\n",
		len(ready), len(blocked), dispatched, activeSpawned+dispatched, r.maxConcurrency)
	return false, nil
}

// countActiveSpawned returns how many of the workers this runner has spawned
// are still actively running (not stopped). Best-effort — on store error,
// assume all spawned are still active to err on the side of not over-spawning.
func (r *runner) countActiveSpawned(ctx context.Context) int {
	count := 0
	for _, name := range r.spawned {
		if name == "" {
			continue // task was already in_progress when we observed it
		}
		a, err := r.store.Agents().Get(ctx, r.workspace, name)
		if err != nil || a == nil {
			count++ // assume active if we can't tell
			continue
		}
		if a.State != domain.AgentStateStopped {
			count++
		}
	}
	return count
}

// spawnWorker creates an ephemeral worker agent pinned to the given task and
// enqueues a start command. Idempotent: if an agent with the derived name
// already exists, we record the mapping and skip creation.
func (r *runner) spawnWorker(ctx context.Context, task backend.IssueData) error {
	name := workerName(r.prefix, task.ID)

	if r.dryRun {
		fmt.Printf("[epic-run] DRY-RUN would spawn: %s for task %s (%s)\n", name, task.ID, task.Title)
		r.spawned[task.ID] = name
		return nil
	}

	mode := domain.AgentModeEphemeral
	_, err := r.store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:          r.workspace,
		Name:                  name,
		RoleName:              r.role,
		Auto:                  true,
		Parent:                r.parent,
		OrchestratorSessionID: r.orchestratorSessionID,
		Mode:                  mode,
		DesiredState:          domain.AgentDesiredRunning,
	})
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create agent %s: %w", name, err)
	}

	if r.store.AgentCommands() == nil {
		// No command channel available (e.g. memstore in tests) — agent will
		// auto-claim from Ready() on its own polling cycle.
		r.spawned[task.ID] = name
		fmt.Printf("[epic-run] spawned %s for %s (%s) — no command channel, agent will pick task via Ready())\n", name, task.ID, task.Title)
		return nil
	}

	if _, err := r.store.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  r.workspace,
		TargetAgentID: name,
		Type:          "start",
		Payload:       map[string]string{"task_id": task.ID},
	}); err != nil {
		return fmt.Errorf("enqueue start command for %s: %w", name, err)
	}

	r.spawned[task.ID] = name
	fmt.Printf("[epic-run] spawned %s -> %s (%s)\n", name, task.ID, task.Title)
	return nil
}

// workerName derives a deterministic agent name from prefix + task ID. Idempotent
// per task — re-running the command produces the same name, so fleet-db's
// unique-name constraint becomes our restart safety net.
func workerName(prefix, taskID string) string {
	return prefix + "-" + sanitizePrefix(taskID)
}

// sanitizePrefix lowercases and strips characters that aren't valid in an agent
// name (lowercase alphanumeric + dot/underscore/hyphen).
func sanitizePrefix(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		default:
			out = append(out, '-')
		}
	}
	return string(out)
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
