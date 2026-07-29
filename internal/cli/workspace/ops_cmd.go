package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backendcheck"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/daemonregistry"
	"github.com/tysonthomas9/loomcli/internal/cli/local"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	workspaceOpsJSON       bool
	workspaceOpsTimeoutSec int
)

// Seams for ensure-runtime. The command's whole deliverable is an honest report
// of what it did, so every outcome of these three calls has to be reachable
// from a test without a live desktop runtime.
var (
	ensureRuntimeStartedFn  = local.EnsureRuntimeStarted
	readRuntimeStatusFn     = local.ReadRuntimeStatus
	waitForWorkspaceReadyFn = local.WaitForWorkspaceReady
)

const envLocalRuntimeMode = "LOOM_LOCAL_RUNTIME"

var workspaceOpsCmd = &cobra.Command{
	Use:   "ops",
	Short: "Agent-safe workspace operations",
	Long: `Agent-safe workspace operations expose one-shot JSON-friendly status,
diagnostics, and runtime repair for local/desktop workspaces.`,
}

var workspaceOpsStatusCmd = &cobra.Command{
	Use:   "status [KEY]",
	Short: "Show workspace runtime status",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkspaceOpsStatus,
}

var workspaceOpsDiagnoseCmd = &cobra.Command{
	Use:   "diagnose [KEY]",
	Short: "Diagnose workspace runtime and agent configuration",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkspaceOpsStatus,
}

var workspaceOpsEnsureRuntimeCmd = &cobra.Command{
	Use:   "ensure-runtime [KEY]",
	Short: "Ensure the local runtime and workspace daemon are running",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkspaceOpsEnsureRuntime,
}

func init() {
	workspaceOpsCmd.PersistentFlags().BoolVar(&workspaceOpsJSON, "json", false, "Output JSON")
	workspaceOpsEnsureRuntimeCmd.Flags().IntVar(&workspaceOpsTimeoutSec, "timeout", 20, "Seconds to wait for runtime and daemon readiness")
	workspaceOpsCmd.AddCommand(workspaceOpsStatusCmd, workspaceOpsDiagnoseCmd, workspaceOpsEnsureRuntimeCmd)
	workspaceCmd.AddCommand(workspaceOpsCmd)
}

type WorkspaceOpsStatus struct {
	OK           bool                      `json:"ok"`
	Workspace    WorkspaceOpsWorkspace     `json:"workspace"`
	LocalRuntime *WorkspaceOpsLocalRuntime `json:"local_runtime,omitempty"`
	Daemon       WorkspaceOpsDaemon        `json:"daemon"`
	Repos        []WorkspaceOpsRepo        `json:"repos"`
	Agents       []WorkspaceOpsAgent       `json:"agents"`
	Problems     []WorkspaceOpsProblem     `json:"problems,omitempty"`

	// EnsureRuntime is set only by `workspace ops ensure-runtime`, and reports
	// whether that command actually did anything. Without it the command's
	// output is indistinguishable from `workspace ops status`, so a caller
	// reading ok=true concludes the runtime was ensured when the command may
	// have returned without acting.
	EnsureRuntime *WorkspaceOpsEnsureRuntime `json:"ensure_runtime,omitempty"`
}

// WorkspaceOpsEnsureRuntime records what ensure-runtime did, so "no action
// needed" is distinguishable from "runtime started" by a machine as well as a
// human.
type WorkspaceOpsEnsureRuntime struct {
	// ActionTaken is false when the command returned without touching the
	// runtime — including the common local case where the runtime was already
	// healthy and EnsureRuntimeStarted returned immediately.
	ActionTaken bool `json:"action_taken"`

	// Action names the outcome precisely: "none", "started" or "restarted".
	// ActionTaken alone cannot distinguish a cold start from a restart of a
	// runtime that was recorded but unhealthy.
	Action string `json:"action,omitempty"`

	// Reason explains the outcome, whether or not anything was done. A skip
	// carries the reason it was skipped; a no-op carries the reason there was
	// nothing to do.
	Reason string `json:"reason,omitempty"`

	// Scope names what this command can and cannot manage, because the name
	// oversells it: it governs the local desktop runtime only and never starts
	// the agent supervisor.
	Scope string `json:"scope,omitempty"`
}

// WorkspaceOpsLocalRuntime reports whether the local desktop runtime is
// relevant to this deployment, and (when applicable) its health.
//
// The local desktop runtime is a Loom.app concept: a per-machine HTTP
// service that backs the Mac/desktop UI. It is started by `loom local
// start` and tracked via /loom-config/runtime.json.
//
// On fleet/headless deployments (LOOM_ISSUE_BACKEND=fleet, e.g. the
// docker-compose stacks) there is no Loom.app and no local runtime — the
// agent CLI talks to fleet-db directly. In that case Applicable=false
// and the runtime / error fields are intentionally empty so callers do
// not mistake "not used here" for "unhealthy".
//
// Field compatibility for desktop consumers (Loom.app reads runtime.json
// directly, not this response, so it is unaffected): the prior Healthy
// / Error / Runtime fields keep their JSON names and meaning when
// Applicable=true. Old consumers that only inspected Healthy continue to
// see the same value they saw before in desktop mode.
type WorkspaceOpsLocalRuntime struct {
	// Applicable is true when this deployment uses the desktop runtime
	// (Loom.app). False on fleet/headless deployments where the concept
	// does not apply.
	Applicable bool `json:"applicable"`
	// Reason is a short human-readable explanation, populated only when
	// Applicable=false.
	Reason string `json:"reason,omitempty"`
	// Healthy mirrors the underlying RuntimeStatusSnapshot.Healthy when
	// Applicable=true. When Applicable=false it is false (zero value)
	// and should be ignored — check Applicable first.
	Healthy bool `json:"healthy"`
	// Error mirrors the underlying RuntimeStatusSnapshot.Error when
	// Applicable=true. Empty when Applicable=false.
	Error string `json:"error,omitempty"`
	// Runtime is the desktop runtime metadata (PID, URL, build, ...)
	// when Applicable=true and runtime.json was readable.
	Runtime *local.RuntimeSnapshot `json:"runtime,omitempty"`
}

type WorkspaceOpsWorkspace struct {
	Key       string `json:"key"`
	Name      string `json:"name,omitempty"`
	State     string `json:"state"`
	LocalPath string `json:"local_path,omitempty"`
}

type WorkspaceOpsDaemon struct {
	AppData        DaemonInfo `json:"app_data"`
	WorkspaceLocal DaemonInfo `json:"workspace_local,omitempty"`
	// Registered reports daemon liveness as advertised via the
	// fleet-db Node registry (see daemonregistry.Detect). It is
	// cwd-independent and correct for daemons launched from arbitrary
	// directories, unlike AppData / WorkspaceLocal which only see
	// daemons whose runtime files sit under the desktop data dir or
	// the workspace local path.
	Registered DaemonInfo `json:"registered,omitempty"`
	DataDir    string     `json:"data_dir,omitempty"`
}

type WorkspaceOpsRepo struct {
	Name       string   `json:"name"`
	LocalPath  string   `json:"local_path,omitempty"`
	RemoteURL  string   `json:"remote_url,omitempty"`
	Groups     []string `json:"groups,omitempty"`
	SourceRepo string   `json:"source_repo_id,omitempty"`
}

type WorkspaceOpsAgent struct {
	Name          string `json:"name"`
	Role          string `json:"role"`
	State         string `json:"state"`
	DesiredState  string `json:"desired_state,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Auto          bool   `json:"auto,omitempty"`
	Parent        string `json:"parent,omitempty"`
	Runnable      bool   `json:"runnable"`
	WorktreePath  string `json:"worktree_path,omitempty"`
	WorktreeReady bool   `json:"worktree_ready"`
	Reason        string `json:"reason,omitempty"`
}

type WorkspaceOpsProblem struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Agent    string `json:"agent,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

func runWorkspaceOpsStatus(cmd *cobra.Command, args []string) error {
	return withWorkspaceOpsStatus(args, func(status *WorkspaceOpsStatus) error {
		return renderWorkspaceOpsStatus(cmd, status)
	})
}

func runWorkspaceOpsEnsureRuntime(cmd *cobra.Command, args []string) error {
	initial, err := workspaceOpsStatusForArgs(args)
	if err != nil {
		return err
	}
	key := initial.Workspace.Key
	if err := bootstrap.SetActiveWorkspaceKey(key); err != nil {
		return fmt.Errorf("select workspace for local runtime: %w", err)
	}
	if err := os.Setenv(bootstrap.EnvWorkspace, key); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), workspaceOpsEnsureTimeout())
	defer cancel()

	status, err := ensureRuntimeAndReport(ctx, key, initial, loadWorkspaceOpsStatus)
	if err != nil {
		return err
	}
	return renderWorkspaceOpsStatus(cmd, status)
}

func workspaceOpsEnsureTimeout() time.Duration {
	if workspaceOpsTimeoutSec <= 0 {
		return 20 * time.Second
	}
	return time.Duration(workspaceOpsTimeoutSec) * time.Second
}

// ensureRuntimeAndReport is everything ensure-runtime decides: whether to act,
// what acting actually did, and what to report. It is split out from the
// workspace-selection preamble so every outcome is reachable from a test —
// the "runtime started" this command used to print for an already-healthy
// runtime was untruthful precisely because nothing exercised the path.
func ensureRuntimeAndReport(
	ctx context.Context,
	key string,
	initial *WorkspaceOpsStatus,
	load workspaceOpsStatusLoader,
) (*WorkspaceOpsStatus, error) {
	if !shouldEnsureLocalRuntime(initial) {
		initial.EnsureRuntime = ensureRuntimeSkipped(initial)
		return initial, nil
	}
	var dataDir string
	if initial != nil {
		dataDir = initial.Daemon.DataDir
	}
	action, err := ensureLocalRuntime(ctx, key, dataDir)
	if err != nil {
		return nil, err
	}
	status, err := waitForWorkspaceOpsDaemon(ctx, key, initial, load)
	if err != nil {
		return nil, fmt.Errorf("wait for workspace daemon: %w", err)
	}
	if status != nil {
		status.EnsureRuntime = ensureRuntimeActed(action)
	}
	return status, nil
}

func ensureLocalRuntime(ctx context.Context, key, dataDir string) (local.RuntimeEnsureAction, error) {
	_, action, err := ensureRuntimeStartedFn(ctx, dataDir, 0)
	if err != nil {
		return action, fmt.Errorf("ensure local runtime: %w", err)
	}
	if rt, err := readRuntimeStatusFn(ctx, dataDir); err == nil &&
		rt != nil && rt.Runtime != nil && rt.Runtime.URL != "" {
		if err := waitForWorkspaceReadyFn(ctx, rt.Runtime.URL, key); err != nil {
			return action, fmt.Errorf("ensure local runtime: %w", err)
		}
	}
	return action, nil
}

// ensureRuntimeActed reports what EnsureRuntimeStarted did rather than assuming
// it started something. It early-returns when the runtime is already healthy,
// which is the common local case; claiming "runtime started" there recreates
// exactly the authoritative-but-wrong report this command exists to fix, and
// does it in the scenario that prompted the report — everything reads green
// while nothing is actually being driven.
func ensureRuntimeActed(action local.RuntimeEnsureAction) *WorkspaceOpsEnsureRuntime {
	out := &WorkspaceOpsEnsureRuntime{Action: string(action), Scope: ensureRuntimeScopeNote}
	switch action {
	case local.RuntimeEnsureNoAction:
		out.Reason = "local desktop runtime was already healthy"
	case local.RuntimeEnsureRestarted:
		out.ActionTaken = true
		out.Reason = "local desktop runtime was not healthy and was restarted"
	case local.RuntimeEnsureStarted:
		out.ActionTaken = true
		out.Reason = "local desktop runtime was not running and was started"
	default:
		out.ActionTaken = true
	}
	return out
}

// ensureRuntimeSkipped records that ensure-runtime returned without acting, and
// why. Without it the command's output is indistinguishable from
// `workspace ops status` and a caller reading ok=true concludes the runtime was
// ensured.
func ensureRuntimeSkipped(status *WorkspaceOpsStatus) *WorkspaceOpsEnsureRuntime {
	reason := "local desktop runtime not applicable to this deployment"
	if status != nil && status.LocalRuntime != nil && status.LocalRuntime.Reason != "" {
		reason = status.LocalRuntime.Reason
	}
	return &WorkspaceOpsEnsureRuntime{
		ActionTaken: false,
		Action:      string(local.RuntimeEnsureNoAction),
		Reason:      reason,
		Scope:       ensureRuntimeScopeNote,
	}
}

// ensureRuntimeScopeNote states the command's limits. The name implies it will
// make the workspace runnable; it governs the local desktop runtime only, and a
// wedged agent supervisor is outside what it can repair.
const ensureRuntimeScopeNote = "manages the local desktop runtime only; does not start or repair the agent supervisor"

func withWorkspaceOpsStatus(args []string, fn func(*WorkspaceOpsStatus) error) error {
	status, err := workspaceOpsStatusForArgs(args)
	if err != nil {
		return err
	}
	return fn(status)
}

func workspaceOpsStatusForArgs(args []string) (*WorkspaceOpsStatus, error) {
	var loaded *WorkspaceOpsStatus
	err := cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		key, err := pickWorkspaceKey(ctx, h.Store, args)
		if err != nil {
			return err
		}
		if err := os.Setenv(bootstrap.EnvWorkspace, key); err != nil {
			return err
		}
		ws, repos, agents, roles, err := gatherWorkspaceDetails(ctx, h.Store, key)
		if err != nil {
			return err
		}
		status, err := buildWorkspaceOpsStatus(ctx, h.Store, ws, repos, agents, roles)
		if err != nil {
			return err
		}
		loaded = status
		return nil
	})
	if err != nil {
		return nil, err
	}
	if loaded == nil {
		return nil, fmt.Errorf("load workspace ops status: no status returned")
	}
	return loaded, nil
}

func loadWorkspaceOpsStatus(ctx context.Context, key string) (*WorkspaceOpsStatus, error) {
	h, err := cmdstore.OpenStore(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = h.Close() }()
	ws, repos, agents, roles, err := gatherWorkspaceDetails(ctx, h.Store, key)
	if err != nil {
		return nil, err
	}
	return buildWorkspaceOpsStatus(ctx, h.Store, ws, repos, agents, roles)
}

type workspaceOpsStatusLoader func(context.Context, string) (*WorkspaceOpsStatus, error)

func waitForWorkspaceOpsDaemon(
	ctx context.Context,
	key string,
	initial *WorkspaceOpsStatus,
	load workspaceOpsStatusLoader,
) (*WorkspaceOpsStatus, error) {
	status := initial
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		default:
		}
		refreshed, err := load(ctx, key)
		if err != nil {
			return nil, err
		}
		status = refreshed
		if !statusNeedsDaemonWait(status) || workspaceDaemonRunning(status) {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-ticker.C:
		}
	}
}

func buildWorkspaceOpsStatus(
	ctx context.Context,
	st store.Store,
	ws *domain.Workspace,
	repos []*domain.Repo,
	agents []*domain.Agent,
	roles []*domain.Role,
) (*WorkspaceOpsStatus, error) {
	sc, _ := bootstrap.LoadStateCache()
	localState := bootstrap.WorkspaceLocalState{}
	if sc != nil {
		localState = sc.Workspaces[ws.Key]
	}
	dataDir, err := local.DefaultDataDir()
	if err != nil {
		return nil, err
	}
	runtime, runtimeErr := local.ReadRuntimeStatus(ctx, dataDir)

	status := newWorkspaceOpsStatus(ws, localState, dataDir, runtime, runtimeErr, len(repos), len(agents))
	// Source 3: fleet-db Node registry. Cwd-independent — see
	// daemonregistry.Detect. This is the LOOM-3 fix: it correctly
	// reports daemons launched from arbitrary cwds.
	status.Daemon.Registered = collectRegisteredDaemonStatus(ctx, st, ws.Key)
	repoByName := collectOpsRepos(status, repos, localState)
	roleNames := collectRoleNames(roles)
	collectOpsAgents(status, agents, localState, repoByName, roleNames)

	status.Problems = append(status.Problems, workspaceOpsGlobalProblems(status)...)
	status.OK = !hasErrorProblem(status.Problems)
	return status, nil
}

// collectRegisteredDaemonStatus translates the daemonregistry.Info
// into a DaemonInfo for WorkspaceOpsDaemon. The translation
// deliberately discards the Socket field — diagnose consumers don't
// need it, and we keep the JSON surface narrow.
func collectRegisteredDaemonStatus(ctx context.Context, st store.Store, workspaceKey string) DaemonInfo {
	info := daemonregistry.Detect(ctx, st, workspaceKey)
	if !info.Running {
		return DaemonInfo{}
	}
	return DaemonInfo{
		Running: true,
		PID:     info.PID,
		Cwd:     info.Cwd,
	}
}

// newWorkspaceOpsStatus seeds the response struct, populates the
// local-runtime block, and records the local-runtime-unhealthy warning
// when applicable.
func newWorkspaceOpsStatus(ws *domain.Workspace, localState bootstrap.WorkspaceLocalState, dataDir string, runtime *local.RuntimeStatusSnapshot, runtimeErr error, repoCap, agentCap int) *WorkspaceOpsStatus {
	status := &WorkspaceOpsStatus{
		Workspace: WorkspaceOpsWorkspace{
			Key:       ws.Key,
			Name:      ws.Name,
			State:     workspaceStateString(ws.State),
			LocalPath: localState.Path,
		},
		LocalRuntime: buildLocalRuntime(runtime, runtimeErr),
		Daemon: WorkspaceOpsDaemon{
			AppData: collectDaemonStatusForDir(dataDir),
			DataDir: dataDir,
		},
		Repos:  make([]WorkspaceOpsRepo, 0, repoCap),
		Agents: make([]WorkspaceOpsAgent, 0, agentCap),
	}
	if localState.Path != "" {
		status.Daemon.WorkspaceLocal = collectDaemonStatusForDir(localState.Path)
	}
	if status.LocalRuntime.Applicable && !status.LocalRuntime.Healthy {
		status.Problems = append(status.Problems, WorkspaceOpsProblem{
			Severity: "warning",
			Code:     "local_runtime_unhealthy",
			Message:  "local desktop runtime is not healthy",
			Fix:      "run `loom workspace ops ensure-runtime --json`",
		})
	}
	return status
}

// buildLocalRuntime composes the WorkspaceOpsLocalRuntime block.
//
// Fleet/headless deployments do not use the desktop runtime, so the block
// reports Applicable=false with a brief Reason and leaves Healthy / Error /
// Runtime empty (zero values). On desktop deployments the existing
// RuntimeStatusSnapshot is mirrored field-for-field so consumers that
// inspect Healthy / Runtime see exactly what they saw before.
func buildLocalRuntime(runtime *local.RuntimeStatusSnapshot, runtimeErr error) *WorkspaceOpsLocalRuntime {
	if applicable, reason, ok := localRuntimeModeOverride(); ok {
		if applicable {
			return buildApplicableLocalRuntime(runtime, runtimeErr)
		}
		return &WorkspaceOpsLocalRuntime{
			Applicable: false,
			Reason:     reason,
		}
	}
	if cli.IsFleetActive() || cli.IsAPIActive() {
		return &WorkspaceOpsLocalRuntime{
			Applicable: false,
			Reason:     "remote issue backend active — local desktop runtime not required",
		}
	}
	if strings.TrimSpace(os.Getenv(bootstrap.EnvFleetDBURL)) != "" {
		return &WorkspaceOpsLocalRuntime{
			Applicable: false,
			Reason:     "external FleetDB URL configured — local desktop runtime not required",
		}
	}
	return buildApplicableLocalRuntime(runtime, runtimeErr)
}

func buildApplicableLocalRuntime(runtime *local.RuntimeStatusSnapshot, runtimeErr error) *WorkspaceOpsLocalRuntime {
	out := &WorkspaceOpsLocalRuntime{Applicable: true}
	if runtime != nil {
		out.Healthy = runtime.Healthy
		out.Error = runtime.Error
		out.Runtime = runtime.Runtime
	}
	if runtimeErr != nil {
		// Surface ENOENT and other read errors in the Error field but
		// keep Healthy=false (the zero value already covers that).
		if out.Error == "" {
			out.Error = runtimeErr.Error()
		}
	}
	return out
}

func localRuntimeModeOverride() (bool, string, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envLocalRuntimeMode))) {
	case "0", "false", "off", "disabled", "none", "headless":
		return false, "local runtime disabled by " + envLocalRuntimeMode + " (headless/server deployment)", true
	case "1", "true", "on", "enabled", "desktop", "local":
		return true, "", true
	default:
		return false, "", false
	}
}

func shouldEnsureLocalRuntime(status *WorkspaceOpsStatus) bool {
	return status == nil || status.LocalRuntime == nil || status.LocalRuntime.Applicable
}

// collectOpsRepos appends a WorkspaceOpsRepo for each non-nil repo and
// returns a name-keyed lookup used during agent validation.
func collectOpsRepos(status *WorkspaceOpsStatus, repos []*domain.Repo, localState bootstrap.WorkspaceLocalState) map[string]*domain.Repo {
	repoByName := map[string]*domain.Repo{}
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		repoByName[repo.Name] = repo
		status.Repos = append(status.Repos, WorkspaceOpsRepo{
			Name:       repo.Name,
			LocalPath:  repoLocalPath(localState, repo.Name),
			RemoteURL:  repo.RemoteURL,
			Groups:     append([]string(nil), repo.Groups...),
			SourceRepo: repo.SourceRepoID,
		})
	}
	return repoByName
}

func collectRoleNames(roles []*domain.Role) map[string]struct{} {
	roleNames := map[string]struct{}{}
	for _, role := range roles {
		if role != nil {
			roleNames[role.Name] = struct{}{}
		}
	}
	return roleNames
}

func collectOpsAgents(status *WorkspaceOpsStatus, agents []*domain.Agent, localState bootstrap.WorkspaceLocalState, repoByName map[string]*domain.Repo, roleNames map[string]struct{}) {
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		item, problems := workspaceOpsAgentStatus(localState, repoByName, roleNames, agent)
		status.Agents = append(status.Agents, item)
		status.Problems = append(status.Problems, problems...)
	}
}

func workspaceStateString(state domain.WorkspaceState) string {
	if state == "" {
		return "ready"
	}
	return string(state)
}

func repoLocalPath(localState bootstrap.WorkspaceLocalState, name string) string {
	return localworkspace.RepoPath(localState, name)
}

//nolint:funlen // Linear "build status struct then collect problems with their per-problem Reason side-effect"; extracting the unknown_role / missing_worktree blocks would require returning the Runnable side-effect alongside the problem, which obscures more than it clarifies.
func workspaceOpsAgentStatus(
	localState bootstrap.WorkspaceLocalState,
	repoByName map[string]*domain.Repo,
	roleNames map[string]struct{},
	agent *domain.Agent,
) (WorkspaceOpsAgent, []WorkspaceOpsProblem) {
	item := WorkspaceOpsAgent{
		Name:          agent.Name,
		Role:          agent.RoleName,
		State:         string(agent.State),
		DesiredState:  string(agent.DesiredState),
		Mode:          string(agent.Mode),
		Auto:          agent.Auto,
		Parent:        agent.Parent,
		Runnable:      agentDesiredRunnable(agent),
		WorktreePath:  agentWorktreePath(localState, repoByName, agent),
		WorktreeReady: false,
	}
	if item.WorktreePath != "" {
		if _, err := os.Stat(filepath.Join(item.WorktreePath, ".git")); err == nil {
			item.WorktreeReady = true
		}
	}
	var problems []WorkspaceOpsProblem
	if _, ok := roleNames[agent.RoleName]; !ok {
		item.Runnable = false
		item.Reason = "unknown_role"
		problems = append(problems, WorkspaceOpsProblem{
			Severity: "error",
			Code:     "agent_unknown_role",
			Message:  fmt.Sprintf("agent %q references unknown role %q", agent.Name, agent.RoleName),
			Agent:    agent.Name,
			Fix:      "use `loom role list` and update the agent role",
		})
	}
	if item.Runnable && localState.Path != "" && !item.WorktreeReady {
		if item.Reason == "" {
			item.Reason = "missing_local_worktree"
		}
		problems = append(problems, WorkspaceOpsProblem{
			Severity: "error",
			Code:     "agent_missing_worktree",
			Message:  fmt.Sprintf("agent %q has no local git worktree", agent.Name),
			Agent:    agent.Name,
			Fix:      "remove and recreate the agent with `loom agentdef add ... --auto` so Loom creates the local worktree",
		})
	}
	if item.Runnable {
		if p, ok := agentBackendProblem(agent); ok {
			if item.Reason == "" {
				item.Reason = "backend_unavailable"
			}
			problems = append(problems, p)
		}
	}
	if !item.Runnable && item.Reason == "" {
		item.Reason = "desired_state_not_running"
	}
	return item, problems
}

// agentBackendProblem returns a WorkspaceOpsProblem describing a missing
// backend CLI when the agent's resolved backend is not on PATH. Returns
// (zero, false) when the backend is installed, when no backend resolves,
// or when discovery itself errored (we'd rather under-report than show
// a false positive driven by a discovery-internal failure).
func agentBackendProblem(agent *domain.Agent) (WorkspaceOpsProblem, bool) {
	eff := agentEffectiveBackend(agent)
	if eff == "" {
		return WorkspaceOpsProblem{}, false
	}
	info, err := backendcheck.CheckBackend(eff)
	if err != nil || info.Installed {
		return WorkspaceOpsProblem{}, false
	}
	return WorkspaceOpsProblem{
		Severity: "error",
		Code:     "agent_backend_unavailable",
		Message:  fmt.Sprintf("agent %q backend %q is not on PATH", agent.Name, eff),
		Agent:    agent.Name,
		Fix:      info.InstallHint,
	}, true
}

// agentEffectiveBackend resolves the backend name an agent would run
// under, using the precedence chain visible from the workspace surface:
// agent override → CLI/env default. The richer supervisor-side
// resolution (role + daemon-config) is deliberately not duplicated here
// to keep the diagnose code free of daemon-runtime coupling. The
// fallback resolution still matches the supervisor for the common case
// where the agent does not set its own backend.
func agentEffectiveBackend(agent *domain.Agent) string {
	if agent.Backend != "" {
		return agent.Backend
	}
	return cli.ResolveBackendName()
}

func agentDesiredRunnable(agent *domain.Agent) bool {
	switch agent.DesiredState {
	case domain.AgentDesiredStopped, domain.AgentDesiredDraining:
		return false
	default:
		return agent.State != domain.AgentStateStopped
	}
}

func agentWorktreePath(localState bootstrap.WorkspaceLocalState, repoByName map[string]*domain.Repo, agent *domain.Agent) string {
	if localState.Agents != nil && localState.Agents[agent.Name].Worktree != "" {
		return localState.Agents[agent.Name].Worktree
	}
	if localState.Path == "" {
		return ""
	}
	repoNames := agent.Repos
	if agent.CrossRepo || len(repoNames) == 0 {
		repoNames = make([]string, 0, len(repoByName))
		for name := range repoByName {
			repoNames = append(repoNames, name)
		}
	}
	for _, repoName := range repoNames {
		candidate := localworkspace.AgentWorktreePath(localState.Path, repoName, agent.Name)
		if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
			return candidate
		}
	}
	if len(repoNames) > 0 {
		return localworkspace.AgentWorktreePath(localState.Path, repoNames[0], agent.Name)
	}
	return ""
}

// workspaceOpsGlobalProblems returns the workspace-level problems for
// status (vs. per-agent problems already collected in collectOpsAgents).
//
// The daemon_not_running problem fires only when there is a runnable
// agent AND none of the three daemon-liveness sources reports the
// daemon running. The third source — Registered, via the fleet-db Node
// registry — is the LOOM-3 fix: it makes the check cwd-independent so
// daemons launched from arbitrary directories are no longer reported as
// missing.
func workspaceOpsGlobalProblems(status *WorkspaceOpsStatus) []WorkspaceOpsProblem {
	var problems []WorkspaceOpsProblem
	if len(status.Repos) == 0 {
		problems = append(problems, WorkspaceOpsProblem{
			Severity: "info",
			Code:     "workspace_has_no_repos",
			Message:  "workspace has no repositories",
			Fix:      "add a repository before creating runnable agents",
		})
	}
	if len(status.Agents) == 0 {
		problems = append(problems, WorkspaceOpsProblem{
			Severity: "info",
			Code:     "workspace_has_no_agents",
			Message:  "workspace has no agent definitions",
			Fix:      "create planner/worker agents for background work",
		})
	}
	if statusNeedsDaemonWait(status) && !workspaceDaemonRunning(status) {
		problems = append(problems, WorkspaceOpsProblem{
			Severity: "error",
			Code:     "daemon_not_running",
			Message:  "workspace has runnable agents but no supervisor daemon is running for this workspace",
			Fix:      daemonNotRunningFix(status),
		})
	}
	if status.Daemon.AppData.Running && status.Daemon.WorkspaceLocal.Running &&
		status.Daemon.AppData.PID != 0 && status.Daemon.WorkspaceLocal.PID != 0 &&
		status.Daemon.AppData.PID != status.Daemon.WorkspaceLocal.PID {
		problems = append(problems, WorkspaceOpsProblem{
			Severity: "warning",
			Code:     "duplicate_daemon_ownership",
			Message:  "both desktop-owned and workspace-local daemons appear to be running",
			Fix:      "stop the manual workspace-local daemon and let desktop local runtime supervise agents",
		})
	}
	return problems
}

func daemonNotRunningFix(status *WorkspaceOpsStatus) string {
	if status != nil && status.LocalRuntime != nil && !status.LocalRuntime.Applicable {
		return "start the workspace daemon for this deployment; local desktop runtime is not applicable"
	}
	return "run `loom workspace ops ensure-runtime --json`"
}

func workspaceDaemonRunning(status *WorkspaceOpsStatus) bool {
	return status.Daemon.AppData.Running ||
		status.Daemon.WorkspaceLocal.Running ||
		status.Daemon.Registered.Running
}

func statusNeedsDaemonWait(status *WorkspaceOpsStatus) bool {
	for _, agent := range status.Agents {
		if agent.Runnable {
			return true
		}
	}
	return false
}

func hasErrorProblem(problems []WorkspaceOpsProblem) bool {
	for _, problem := range problems {
		if problem.Severity == "error" {
			return true
		}
	}
	return false
}

func renderWorkspaceOpsStatus(cmd *cobra.Command, status *WorkspaceOpsStatus) error {
	if workspaceOpsJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Workspace: %s (%s)\n", status.Workspace.Key, status.Workspace.State)
	if er := status.EnsureRuntime; er != nil {
		if er.ActionTaken {
			_, _ = fmt.Fprintf(out, "Ensure:    %s (%s)\n", ensureRuntimeActionText(er), er.Scope)
		} else {
			_, _ = fmt.Fprintf(out, "Ensure:    NO ACTION TAKEN - %s (%s)\n", er.Reason, er.Scope)
		}
	}
	if status.LocalRuntime != nil && !status.LocalRuntime.Applicable {
		_, _ = fmt.Fprintf(out, "Runtime:   not applicable (%s)\n", status.LocalRuntime.Reason)
	} else if status.LocalRuntime != nil && status.LocalRuntime.Runtime != nil {
		_, _ = fmt.Fprintf(out, "Runtime:   healthy=%t url=%s pid=%d\n",
			status.LocalRuntime.Healthy, status.LocalRuntime.Runtime.URL, status.LocalRuntime.Runtime.PID)
	} else {
		_, _ = fmt.Fprintln(out, "Runtime:   unavailable")
	}
	_, _ = fmt.Fprintf(out, "Daemon:    desktop=%s workspace=%s registered=%s\n",
		daemonHuman(status.Daemon.AppData),
		daemonHuman(status.Daemon.WorkspaceLocal),
		daemonHuman(status.Daemon.Registered))
	_, _ = fmt.Fprintf(out, "Repos:     %d\nAgents:    %d\n", len(status.Repos), len(status.Agents))
	if len(status.Problems) > 0 {
		_, _ = fmt.Fprintf(out, "Problems:  %d\n", len(status.Problems))
		for _, problem := range status.Problems {
			parts := []string{problem.Severity, problem.Code}
			if problem.Agent != "" {
				parts = append(parts, "agent="+problem.Agent)
			}
			_, _ = fmt.Fprintf(out, "  - %s: %s\n", strings.Join(parts, " "), problem.Message)
			if problem.Fix != "" {
				_, _ = fmt.Fprintf(out, "    fix: %s\n", problem.Fix)
			}
		}
	}
	return nil
}

// ensureRuntimeActionText names the action for a human. A restart of a wedged
// runtime and a cold start are different events to whoever is debugging one.
func ensureRuntimeActionText(er *WorkspaceOpsEnsureRuntime) string {
	if er.Action == string(local.RuntimeEnsureRestarted) {
		return "runtime restarted"
	}
	return "runtime started"
}

func daemonHuman(info DaemonInfo) string {
	if info.Running {
		switch {
		case info.PID != 0 && info.Cwd != "":
			return fmt.Sprintf("running(pid=%d, cwd=%s)", info.PID, info.Cwd)
		case info.PID != 0:
			return fmt.Sprintf("running(pid=%d)", info.PID)
		default:
			return "running"
		}
	}
	if info.StalePID {
		return "stale"
	}
	return "stopped"
}
