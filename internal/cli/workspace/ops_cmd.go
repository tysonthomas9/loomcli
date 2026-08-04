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
	"github.com/tysonthomas9/loomcli/internal/cli/local"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

var (
	workspaceOpsJSON       bool
	workspaceOpsTimeoutSec int
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
	Short: "Ensure the local platform runtime is running",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkspaceOpsEnsureRuntime,
}

func init() {
	workspaceOpsCmd.PersistentFlags().BoolVar(&workspaceOpsJSON, "json", false, "Output JSON")
	workspaceOpsEnsureRuntimeCmd.Flags().IntVar(&workspaceOpsTimeoutSec, "timeout", 20, "Seconds to wait for runtime readiness")
	workspaceOpsCmd.AddCommand(workspaceOpsStatusCmd, workspaceOpsDiagnoseCmd, workspaceOpsEnsureRuntimeCmd)
	workspaceCmd.AddCommand(workspaceOpsCmd)
}

type WorkspaceOpsStatus struct {
	OK           bool                      `json:"ok"`
	Workspace    WorkspaceOpsWorkspace     `json:"workspace"`
	LocalRuntime *WorkspaceOpsLocalRuntime `json:"local_runtime,omitempty"`
	Repos        []WorkspaceOpsRepo        `json:"repos"`
	Agents       []WorkspaceOpsAgent       `json:"agents"`
	Problems     []WorkspaceOpsProblem     `json:"problems,omitempty"`
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
	// DataDir is the local runtime state root used by ensure-runtime.
	DataDir string `json:"data_dir,omitempty"`
}

type WorkspaceOpsWorkspace struct {
	Key       string `json:"key"`
	Name      string `json:"name,omitempty"`
	State     string `json:"state"`
	LocalPath string `json:"local_path,omitempty"`
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

	timeout := time.Duration(workspaceOpsTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	if !shouldEnsureLocalRuntime(initial) {
		return renderWorkspaceOpsStatus(cmd, initial)
	}
	dataDir := ""
	if initial.LocalRuntime != nil {
		dataDir = initial.LocalRuntime.DataDir
	}
	if dataDir == "" {
		dataDir, err = local.DefaultDataDir()
		if err != nil {
			return err
		}
	}
	if _, err := local.EnsureRuntimeStarted(ctx, dataDir, 0); err != nil {
		return fmt.Errorf("ensure local runtime: %w", err)
	}
	if rt, err := local.ReadRuntimeStatus(ctx, dataDir); err == nil &&
		rt != nil && rt.Runtime != nil && rt.Runtime.URL != "" {
		if err := local.WaitForWorkspaceReady(ctx, rt.Runtime.URL, key); err != nil {
			return fmt.Errorf("ensure local runtime: %w", err)
		}
	}

	status, err := workspaceOpsStatusForArgs([]string{key})
	if err != nil {
		return fmt.Errorf("refresh workspace runtime status: %w", err)
	}
	return renderWorkspaceOpsStatus(cmd, status)
}

func withWorkspaceOpsStatus(args []string, fn func(*WorkspaceOpsStatus) error) error {
	status, err := workspaceOpsStatusForArgs(args)
	if err != nil {
		return err
	}
	return fn(status)
}

func workspaceOpsStatusForArgs(args []string) (*WorkspaceOpsStatus, error) {
	var loaded *WorkspaceOpsStatus
	err := cmdstore.WithWorkspaceCatalog(func(ctx context.Context, h *bootstrap.StoreHandle, workspace workspacemodule.API) error {
		key, err := pickWorkspaceKey(ctx, workspace, args)
		if err != nil {
			return err
		}
		if err := os.Setenv(bootstrap.EnvWorkspace, key); err != nil {
			return err
		}
		ws, repos, agents, roles, err := gatherWorkspaceDetails(ctx, h.Store, workspace, key)
		if err != nil {
			return err
		}
		status, err := buildWorkspaceOpsStatus(ctx, ws, repos, agents, roles)
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

func buildWorkspaceOpsStatus(
	ctx context.Context,
	ws *domain.Workspace,
	repos []*domain.Repo,
	agents []*domain.AgentService,
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
	repoByName := collectOpsRepos(status, repos, localState)
	rolesByName := collectRolesByName(roles)
	collectOpsAgents(status, agents, localState, repoByName, rolesByName)

	status.Problems = append(status.Problems, workspaceOpsGlobalProblems(status)...)
	status.OK = !hasErrorProblem(status.Problems)
	return status, nil
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
		Repos:        make([]WorkspaceOpsRepo, 0, repoCap),
		Agents:       make([]WorkspaceOpsAgent, 0, agentCap),
	}
	if status.LocalRuntime != nil {
		status.LocalRuntime.DataDir = dataDir
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

func collectRolesByName(roles []*domain.Role) map[string]*domain.Role {
	rolesByName := map[string]*domain.Role{}
	for _, role := range roles {
		if role != nil {
			rolesByName[role.Name] = role
		}
	}
	return rolesByName
}

func collectOpsAgents(status *WorkspaceOpsStatus, agents []*domain.AgentService, localState bootstrap.WorkspaceLocalState, repoByName map[string]*domain.Repo, rolesByName map[string]*domain.Role) {
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		item, problems := workspaceOpsAgentStatus(localState, repoByName, rolesByName, agent)
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
	rolesByName map[string]*domain.Role,
	agent *domain.AgentService,
) (WorkspaceOpsAgent, []WorkspaceOpsProblem) {
	runtime, runtimeErr := agentsmodule.ParseRuntimeMetadata(agent.Metadata)
	item := WorkspaceOpsAgent{
		Name:          agent.ServiceID,
		Role:          agent.RoleName,
		State:         string(agent.DesiredState),
		DesiredState:  string(agent.DesiredState),
		Auto:          runtime.Auto,
		Runnable:      agentDesiredRunnable(agent),
		WorktreePath:  agentWorktreePath(localState, repoByName, agent.ServiceID, runtime),
		WorktreeReady: false,
	}
	if item.WorktreePath != "" {
		if _, err := os.Stat(filepath.Join(item.WorktreePath, ".git")); err == nil {
			item.WorktreeReady = true
		}
	}
	var problems []WorkspaceOpsProblem
	if runtimeErr != nil {
		item.Runnable = false
		item.Reason = "invalid_runtime_metadata"
		problems = append(problems, WorkspaceOpsProblem{
			Severity: "error", Code: "agent_invalid_runtime_metadata",
			Message: fmt.Sprintf("agent %q has invalid runtime metadata", agent.ServiceID),
			Agent:   agent.ServiceID,
			Fix:     "update or recreate the canonical Agent",
		})
	}
	role, roleExists := rolesByName[agent.RoleName]
	if !roleExists {
		item.Runnable = false
		item.Reason = "unknown_role"
		problems = append(problems, WorkspaceOpsProblem{
			Severity: "error",
			Code:     "agent_unknown_role",
			Message:  fmt.Sprintf("agent %q references unknown role %q", agent.ServiceID, agent.RoleName),
			Agent:    agent.ServiceID,
			Fix:      "use `loom role list` and update the agent role",
		})
	}
	requiresWorktree := roleExists && domain.ResolveRoleKind(role, agent.RoleName) != domain.RoleKindInteractive
	if item.Runnable && requiresWorktree && localState.Path != "" && !item.WorktreeReady {
		if item.Reason == "" {
			item.Reason = "missing_local_worktree"
		}
		problems = append(problems, WorkspaceOpsProblem{
			Severity: "error",
			Code:     "agent_missing_worktree",
			Message:  fmt.Sprintf("agent %q has no local git worktree", agent.ServiceID),
			Agent:    agent.ServiceID,
			Fix:      "remove and recreate the agent with `loom agentdef add ... --auto` so Loom creates the local worktree",
		})
	}
	if item.Runnable {
		if p, ok := agentBackendProblem(agent.ServiceID, runtime.Backend); ok {
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

func agentDesiredRunnable(agent *domain.AgentService) bool {
	return agent != nil && agent.DesiredState == domain.AgentServiceDesiredRunning
}

// agentBackendProblem returns a WorkspaceOpsProblem describing a missing
// backend CLI when the agent's resolved backend is not on PATH. Returns
// (zero, false) when the backend is installed, when no backend resolves,
// or when discovery itself errored (we'd rather under-report than show
// a false positive driven by a discovery-internal failure).
func agentBackendProblem(agentID, backend string) (WorkspaceOpsProblem, bool) {
	eff := backend
	if eff == "" {
		eff = cli.ResolveBackendName()
	}
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
		Message:  fmt.Sprintf("agent %q backend %q is not on PATH", agentID, eff),
		Agent:    agentID,
		Fix:      info.InstallHint,
	}, true
}

// agentEffectiveBackend resolves the backend name an agent would run
// under, using the precedence chain visible from the workspace surface:
// agent override → CLI/env default.
func agentWorktreePath(localState bootstrap.WorkspaceLocalState, repoByName map[string]*domain.Repo, agentID string, runtime agentsmodule.RuntimeMetadata) string {
	if localState.Agents != nil && localState.Agents[agentID].Worktree != "" {
		return localState.Agents[agentID].Worktree
	}
	if localState.Path == "" {
		return ""
	}
	repoNames := runtime.Repos
	if runtime.CrossRepo || len(repoNames) == 0 {
		repoNames = make([]string, 0, len(repoByName))
		for name := range repoByName {
			repoNames = append(repoNames, name)
		}
	}
	for _, repoName := range repoNames {
		candidate := localworkspace.AgentWorktreePath(localState.Path, repoName, agentID)
		if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
			return candidate
		}
	}
	if len(repoNames) > 0 {
		return localworkspace.AgentWorktreePath(localState.Path, repoNames[0], agentID)
	}
	return ""
}

// workspaceOpsGlobalProblems returns the workspace-level problems for
// status (vs. per-agent problems already collected in collectOpsAgents).
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
	return problems
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
	if status.LocalRuntime != nil && !status.LocalRuntime.Applicable {
		_, _ = fmt.Fprintf(out, "Runtime:   not applicable (%s)\n", status.LocalRuntime.Reason)
	} else if status.LocalRuntime != nil && status.LocalRuntime.Runtime != nil {
		_, _ = fmt.Fprintf(out, "Runtime:   healthy=%t url=%s pid=%d\n",
			status.LocalRuntime.Healthy, status.LocalRuntime.Runtime.URL, status.LocalRuntime.Runtime.PID)
	} else {
		_, _ = fmt.Fprintln(out, "Runtime:   unavailable")
	}
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
