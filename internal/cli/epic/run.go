// Package epic provides the `loom epic` command tree. The first subcommand —
// `loom epic run` — is a foreground reconcile loop that drives an epic to
// completion by spawning one ephemeral worker per ready task. Lead/orchestrator
// AIs invoke this from chat ("take the auth epic") to fan out work; humans can
// also run it directly. Workers spawned by this command inherit the lead's
// LOOM_ORCHESTRATOR_SESSION_ID env var so the UI groups them under their lead.
package epic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
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
)

var errStalledWorker = errors.New("task worker stopped before completing its task")

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
	epicRunCmd.Flags().StringVar(&runNodeID, "node-id", "", "Daemon node ID to run spawned workers on (default: the single active local node)")
	epicRunCmd.Flags().StringVar(&runLead, "lead", "", "Lead agent running this epic (default: $LOOM_AGENT_NAME when set)")
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

	orchestratorID := strings.TrimSpace(os.Getenv(envOrchestratorSessionID))
	leadName, orchestratorID, err := bindLeadAgent(ctx, handle.Store, ws, resolveLeadName(runLead), runParent, orchestratorID, !runDryRun)
	if err != nil {
		return err
	}

	nodeID := runNodeID
	if nodeID == "" && handle.Store.AgentCommands() != nil && !runDryRun {
		nodeID, err = selectTargetNodeID(ctx, handle.Store, ws)
		if err != nil {
			return err
		}
	}

	r := &runner{
		store:                 handle.Store,
		ib:                    ib,
		workspace:             ws,
		parent:                runParent,
		leadName:              leadName,
		role:                  runRole,
		prefix:                prefix,
		maxConcurrency:        runMaxConcurrency,
		interval:              time.Duration(runIntervalSeconds) * time.Second,
		orchestratorSessionID: orchestratorID,
		targetNodeID:          nodeID,
		dryRun:                runDryRun,
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
	leadName              string
	role                  string
	prefix                string
	maxConcurrency        int
	interval              time.Duration
	orchestratorSessionID string
	targetNodeID          string
	dryRun                bool
}

func (r *runner) printHeader() {
	fmt.Printf("Epic runner\n")
	fmt.Printf("  workspace:        %s\n", r.workspace)
	fmt.Printf("  parent epic:      %s\n", r.parent)
	if r.leadName != "" {
		fmt.Printf("  lead agent:       %s\n", r.leadName)
	}
	fmt.Printf("  role:             %s\n", r.role)
	fmt.Printf("  worker prefix:    %s\n", r.prefix)
	fmt.Printf("  max concurrency:  %d\n", r.maxConcurrency)
	fmt.Printf("  interval:         %s\n", r.interval)
	if r.orchestratorSessionID != "" {
		fmt.Printf("  orchestrator:     %s\n", r.orchestratorSessionID)
	} else {
		fmt.Printf("  orchestrator:     (none — workers will be unattached)\n")
	}
	if r.targetNodeID != "" {
		fmt.Printf("  target node:      %s\n", r.targetNodeID)
	}
	if r.dryRun {
		fmt.Printf("  dry-run:          true\n")
	}
	fmt.Printf("\n")
}

func resolveLeadName(flagValue string) string {
	if lead := strings.TrimSpace(flagValue); lead != "" {
		return lead
	}
	return strings.TrimSpace(os.Getenv(envAgentName))
}

func bindLeadAgent(ctx context.Context, st store.Store, workspace, leadName, parent, orchestratorID string, mutate bool) (string, string, error) {
	leadName = strings.TrimSpace(leadName)
	if leadName == "" {
		return "", orchestratorID, nil
	}

	if mutate {
		unlock, err := acquireLeadBindLock(workspace, leadName)
		if err != nil {
			return "", "", err
		}
		defer unlock()
	}

	lead, err := st.Agents().Get(ctx, workspace, leadName)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", "", fmt.Errorf("lead agent %q was not found in workspace %s; create it with `loom agentdef add %s --role lead` or rerun without --lead: %w", leadName, workspace, leadName, domain.ErrNotFound)
		}
		return "", "", fmt.Errorf("load lead agent %q: %w", leadName, err)
	}
	if lead == nil {
		return "", "", fmt.Errorf("lead agent %q lookup returned nil", leadName)
	}
	if !isLeadRole(lead.RoleName) {
		return "", "", fmt.Errorf("agent %q has role %q; `loom epic run` requires a lead agent when --lead or %s is set", leadName, lead.RoleName, envAgentName)
	}
	if lead.Parent != "" && lead.Parent != parent {
		return "", "", fmt.Errorf("lead agent %q is already running epic %s; ask the lead to clear or finish that epic before running %s", leadName, lead.Parent, parent)
	}

	effectiveOrchestratorID := strings.TrimSpace(orchestratorID)
	if effectiveOrchestratorID == "" {
		effectiveOrchestratorID = strings.TrimSpace(lead.OrchestratorSessionID)
	}

	if !mutate {
		if lead.Parent == "" {
			fmt.Printf("[epic-run] DRY-RUN would assign lead %s to epic %s\n", leadName, parent)
		}
		return leadName, effectiveOrchestratorID, nil
	}

	patch := store.AgentUpdate{}
	needsUpdate := false
	if lead.Parent == "" {
		patch.Parent = &parent
		needsUpdate = true
	}
	if effectiveOrchestratorID != "" && lead.OrchestratorSessionID != effectiveOrchestratorID {
		patch.OrchestratorSessionID = &effectiveOrchestratorID
		needsUpdate = true
	}
	if !needsUpdate {
		return leadName, effectiveOrchestratorID, nil
	}

	updated, err := st.Agents().Update(ctx, workspace, leadName, patch)
	if err != nil {
		return "", "", fmt.Errorf("bind lead agent %q to epic %s: %w", leadName, parent, err)
	}
	if updated == nil {
		return "", "", fmt.Errorf("bind lead agent %q returned nil", leadName)
	}
	if updated.Parent != parent {
		return "", "", fmt.Errorf("lead agent %q is already running epic %s; ask the lead to clear or finish that epic before running %s", leadName, updated.Parent, parent)
	}
	if effectiveOrchestratorID == "" {
		effectiveOrchestratorID = strings.TrimSpace(updated.OrchestratorSessionID)
	}
	return leadName, effectiveOrchestratorID, nil
}

func acquireLeadBindLock(workspace, leadName string) (func(), error) {
	dir := bootstrap.LoomDir()
	if dir == "" {
		return func() {}, errors.New("cannot resolve loom data directory for lead assignment lock")
	}
	lockDir := filepath.Join(dir, "epic-runner-locks")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return func() {}, fmt.Errorf("create lead assignment lock directory: %w", err)
	}
	lockName := sanitizePrefix(workspace + "-" + leadName)
	if lockName == "" {
		lockName = "lead"
	}
	lockPath := filepath.Join(lockDir, lockName+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // path is under loom data dir with sanitized filename
	if err != nil {
		return func() {}, fmt.Errorf("open lead assignment lock %s: %w", lockPath, err)
	}
	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		_ = f.Close()
		return func() {}, fmt.Errorf("acquire lead assignment lock %s: %w", lockPath, err)
	}
	return func() {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
	}, nil
}

func isLeadRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "lead", "orchestrator":
		return true
	default:
		return false
	}
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
			if errors.Is(err, errStalledWorker) {
				return err
			}
			fmt.Fprintf(os.Stderr, "[epic-run] reconcile error: %v (continuing)\n", err)
		}
		if done {
			fmt.Printf("[epic-run] epic %s drained, exiting\n", r.parent)
			return nil
		}

		select {
		case <-ctx.Done():
			fmt.Printf("[epic-run] interrupted; worker state remains in fleet-db and workers continue independently\n")
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

	openChildren, err := r.openChildren(ctx)
	if err != nil {
		return false, err
	}

	// Drain only after no child work remains and every epic worker has stopped,
	// so downstream orchestration can trust the lifecycle.
	if len(openChildren) == 0 {
		activeWorkers := r.countActiveWorkers(ctx, openChildren)
		if activeWorkers > 0 {
			fmt.Printf("[epic-run] child work closed; waiting for %d active worker(s) to stop\n", activeWorkers)
			return false, nil
		}
		return true, nil
	}

	// Spawn for new ready tasks, respecting concurrency.
	activeWorkers := r.countActiveWorkers(ctx, openChildren)
	if stalled := r.stalledTasks(ctx, openChildren); len(stalled) > 0 {
		return false, fmt.Errorf("%w: %s", errStalledWorker, strings.Join(stalled, "; "))
	}
	slots := r.maxConcurrency - activeWorkers
	if slots <= 0 {
		fmt.Printf("[epic-run] %d ready, %d blocked, %d active workers (at cap)\n", len(ready), len(blocked), activeWorkers)
		return false, nil
	}

	dispatched := 0
	for _, task := range ready {
		if dispatched >= slots {
			break
		}
		if task.Status == "in_progress" {
			// Someone else is already working on it (could be a peer agent or a
			// re-run of this command). If it reopens, a later pass can dispatch it.
			continue
		}
		if task.Status == "closed" || task.Status == "deferred" {
			continue
		}
		name := workerName(r.prefix, task.ID)
		active, err := r.workerActiveForTask(ctx, name, task.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[epic-run] active worker check for %s failed: %v\n", task.ID, err)
			continue
		}
		if active {
			continue
		}
		if err := r.spawnWorker(ctx, task); err != nil {
			fmt.Fprintf(os.Stderr, "[epic-run] spawn for %s failed: %v\n", task.ID, err)
			continue
		}
		dispatched++
	}

	fmt.Printf("[epic-run] %d ready, %d blocked, dispatched %d (active %d/%d)\n",
		len(ready), len(blocked), dispatched, activeWorkers+dispatched, r.maxConcurrency)
	return false, nil
}

func (r *runner) openChildren(ctx context.Context) ([]backend.IssueData, error) {
	issues, err := r.ib.List(ctx, backend.ListOpts{ParentID: r.parent, Limit: 10000})
	if err != nil {
		return nil, fmt.Errorf("list child work: %w", err)
	}
	open := make([]backend.IssueData, 0, len(issues))
	for _, t := range issues {
		if t.Status != "closed" && t.Status != "deferred" {
			open = append(open, t)
		}
	}
	return open, nil
}

// countActiveWorkers derives active epic worker count from fleet-db state, not
// from this process. On store errors it fails closed and reports the cap as
// active to avoid over-dispatching.
func (r *runner) countActiveWorkers(ctx context.Context, openChildren []backend.IssueData) int {
	active := make(map[string]struct{})
	for _, task := range openChildren {
		name := workerName(r.prefix, task.ID)
		ok, err := r.workerActiveForTask(ctx, name, task.ID)
		if err != nil {
			return r.maxConcurrency
		}
		if ok {
			active[name] = struct{}{}
		}
	}

	agents, err := r.store.Agents().List(ctx, r.workspace)
	if err != nil {
		return r.maxConcurrency
	}
	for _, agent := range agents {
		if agent == nil || agent.Parent != r.parent || agent.Mode != domain.AgentModeEphemeral {
			continue
		}
		if _, counted := active[agent.Name]; counted {
			continue
		}
		ok, err := r.workerActiveForTask(ctx, agent.Name, "")
		if err != nil {
			return r.maxConcurrency
		}
		if ok {
			active[agent.Name] = struct{}{}
		}
	}
	return len(active)
}

func (r *runner) workerActiveForTask(ctx context.Context, name, taskID string) (bool, error) {
	if name == "" {
		return false, nil
	}
	liveCommand, err := r.hasLiveStartCommand(ctx, name, taskID)
	if err != nil || liveCommand {
		return liveCommand, err
	}
	liveSession, err := r.hasLiveTaskSession(ctx, name, taskID)
	if err != nil || liveSession {
		return liveSession, err
	}
	a, err := r.store.Agents().Get(ctx, r.workspace, name)
	if errors.Is(err, domain.ErrNotFound) || a == nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return a.Mode == domain.AgentModeEphemeral && a.Parent == r.parent && a.State == domain.AgentStateActive, nil
}

func (r *runner) hasLiveStartCommand(ctx context.Context, name, taskID string) (bool, error) {
	if r.store.AgentCommands() == nil {
		return false, nil
	}
	cmds, err := r.store.AgentCommands().List(ctx, r.workspace, store.AgentCommandFilter{
		TargetAgentID: name,
		Limit:         100,
	})
	if err != nil {
		return false, err
	}
	for _, cmd := range cmds {
		if cmd == nil || cmd.Type != "start" || !liveAgentCommandStatus(cmd.Status) {
			continue
		}
		if taskID != "" && cmd.Payload["task_id"] != taskID {
			continue
		}
		return true, nil
	}
	return false, nil
}

func (r *runner) hasLiveTaskSession(ctx context.Context, name, taskID string) (bool, error) {
	if r.store.AgentSessions() == nil {
		return false, nil
	}
	filter := store.AgentSessionFilter{AgentID: name, Limit: 100}
	if taskID != "" {
		filter.TaskID = taskID
	}
	sessions, err := r.store.AgentSessions().List(ctx, r.workspace, filter)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if session == nil || session.Kind != domain.AgentSessionKindTask || !liveAgentSessionStatus(session.Status) {
			continue
		}
		if taskID != "" && session.TaskID != taskID {
			continue
		}
		return true, nil
	}
	return false, nil
}

func liveAgentCommandStatus(status domain.AgentCommandStatus) bool {
	switch status {
	case domain.AgentCommandQueued, domain.AgentCommandAcked, domain.AgentCommandRunning:
		return true
	default:
		return false
	}
}

func liveAgentSessionStatus(status domain.AgentSessionStatus) bool {
	switch status {
	case domain.AgentSessionQueued, domain.AgentSessionLeased, domain.AgentSessionStarting, domain.AgentSessionRunning:
		return true
	default:
		return false
	}
}

func (r *runner) stalledTasks(ctx context.Context, openChildren []backend.IssueData) []string {
	var stalled []string
	for _, task := range openChildren {
		if task.Status != "in_progress" {
			continue
		}
		name := workerName(r.prefix, task.ID)
		if task.Assignee != name {
			continue
		}
		active, err := r.workerActiveForTask(ctx, name, task.ID)
		if err != nil || active {
			continue
		}
		stalled = append(stalled, fmt.Sprintf("%s (%s) assigned to stopped worker %s", task.ID, task.Title, name))
	}
	return stalled
}

// spawnWorker creates an ephemeral worker agent pinned to the given task and
// enqueues a start command. Agent creation is idempotent because the worker name
// is deterministic for the task.
func (r *runner) spawnWorker(ctx context.Context, task backend.IssueData) error {
	name := workerName(r.prefix, task.ID)

	if r.dryRun {
		fmt.Printf("[epic-run] DRY-RUN would spawn: %s for task %s (%s)\n", name, task.ID, task.Title)
		return nil
	}

	mode := domain.AgentModeEphemeral
	desired := domain.AgentDesiredStopped
	if r.store.AgentCommands() == nil {
		desired = domain.AgentDesiredRunning
	}
	agent, err := r.store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:          r.workspace,
		Name:                  name,
		RoleName:              r.role,
		Auto:                  true,
		Parent:                r.parent,
		OrchestratorSessionID: r.orchestratorSessionID,
		Mode:                  mode,
		DesiredState:          desired,
	})
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create agent %s: %w", name, err)
	}
	if errors.Is(err, domain.ErrAlreadyExists) {
		agent, err = r.store.Agents().Get(ctx, r.workspace, name)
		if err != nil {
			return fmt.Errorf("get existing agent %s: %w", name, err)
		}
	}
	if agent == nil {
		return fmt.Errorf("create agent %s returned nil", name)
	}
	if err := r.ensureLocalWorkerWorktrees(ctx, *agent); err != nil {
		return err
	}

	if r.store.AgentCommands() == nil {
		// No command channel available (e.g. memstore in tests) — agent will
		// auto-claim from Ready() on its own polling cycle.
		fmt.Printf("[epic-run] spawned %s for %s (%s) — no command channel, agent will pick task via Ready())\n", name, task.ID, task.Title)
		return nil
	}

	if _, err := r.store.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  r.workspace,
		TargetAgentID: name,
		TargetNodeID:  r.targetNodeID,
		Type:          "start",
		Payload:       map[string]string{"task_id": task.ID},
	}); err != nil {
		return fmt.Errorf("enqueue start command for %s: %w", name, err)
	}

	fmt.Printf("[epic-run] spawned %s -> %s (%s)\n", name, task.ID, task.Title)
	return nil
}

func (r *runner) ensureLocalWorkerWorktrees(ctx context.Context, agent domain.Agent) error {
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		return fmt.Errorf("load local workspace state: %w", err)
	}
	local := sc.Workspaces[agent.WorkspaceKey]
	if local.Path == "" {
		return nil
	}
	repos, err := r.store.Repos().List(ctx, agent.WorkspaceKey)
	if err != nil {
		return fmt.Errorf("list workspace repos: %w", err)
	}
	localRepos := make([]localworkspace.Repo, 0, len(repos))
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		localRepos = append(localRepos, localworkspace.Repo{
			Name:          repo.Name,
			Path:          localworkspace.RepoPath(local, repo.Name),
			Remote:        repo.Remote,
			DefaultBranch: repo.DefaultBranch,
			Groups:        append([]string(nil), repo.Groups...),
		})
	}
	selected, err := localworkspace.SelectAgentRepos(localRepos, agent)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return fmt.Errorf("workspace %s has no repos for agent %q", agent.WorkspaceKey, agent.Name)
	}

	created := make(map[string]string, len(selected))
	for _, repo := range selected {
		target := localworkspace.AgentWorktreePath(local.Path, repo.Name, agent.Name)
		if err := localworkspace.EnsureGitWorktreeFromBranch(repo.Path, target, agent.Name, repo.Remote, repo.DefaultBranch); err != nil {
			return fmt.Errorf("create worktree for repo %q: %w", repo.Name, err)
		}
		created[repo.Name] = target
	}
	if err := localworkspace.RememberAgentWorktree(agent.WorkspaceKey, agent.Name, localworkspace.FirstWorktreePath(created)); err != nil {
		return fmt.Errorf("remember agent worktree: %w", err)
	}
	return nil
}

// workerName derives a deterministic agent name from prefix + task ID. Idempotent
// per task — re-running the command produces the same name, so fleet-db's
// unique-name constraint becomes our restart safety net.
func workerName(prefix, taskID string) string {
	hashBytes := sha256.Sum256([]byte(taskID))
	hash := hex.EncodeToString(hashBytes[:])[:8]
	base := sanitizePrefix(prefix + "-" + taskID)
	const maxNameLen = 63
	suffix := "-" + hash
	if len(base)+len(suffix) > maxNameLen {
		base = strings.TrimRight(base[:maxNameLen-len(suffix)], "-")
	}
	if base == "" {
		base = "task"
	}
	return base + suffix
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

func selectTargetNodeID(ctx context.Context, st store.Store, workspace string) (string, error) {
	if st.Nodes() == nil {
		return "", nil
	}
	nodes, err := st.Nodes().List(ctx, workspace)
	if err != nil {
		return "", fmt.Errorf("list daemon nodes: %w", err)
	}
	now := time.Now().UTC()
	active := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if node.DrainState != domain.NodeDrainActive {
			continue
		}
		if !node.ExpiresAt.IsZero() && node.ExpiresAt.Before(now) {
			continue
		}
		active = append(active, node.NodeID)
	}
	switch len(active) {
	case 0:
		return "", errors.New("no active daemon node registered for this workspace; start `loom daemon` before running `loom epic run`")
	case 1:
		return active[0], nil
	default:
		return "", fmt.Errorf("multiple active daemon nodes found (%s); rerun with --node-id", strings.Join(active, ", "))
	}
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
