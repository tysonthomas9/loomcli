package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/local"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func clearRuntimeRoutingEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LOOM_ISSUE_BACKEND", "")
	t.Setenv("LOOM_SERVER_URL", "")
	t.Setenv("LOOM_FLEET_DB_URL", "")
	t.Setenv(envLocalRuntimeMode, "")
}

func TestWaitForWorkspaceOpsDaemonUsesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	initial := &WorkspaceOpsStatus{
		Daemon: WorkspaceOpsDaemon{
			AppData: DaemonInfo{Running: false},
		},
		Agents: []WorkspaceOpsAgent{
			{Name: "planner", Runnable: true},
		},
	}

	status, err := waitForWorkspaceOpsDaemon(ctx, "TEST", initial, func(context.Context, string) (*WorkspaceOpsStatus, error) {
		calls++
		return initial, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForWorkspaceOpsDaemon error = %v, want context.Canceled", err)
	}
	if status != initial {
		t.Fatalf("status = %#v, want initial status", status)
	}
	if calls != 0 {
		t.Fatalf("loader calls = %d, want 0 after canceled context", calls)
	}
}

func TestWaitForWorkspaceOpsDaemonDoesNotReturnStaleStatusOnLoaderError(t *testing.T) {
	initial := &WorkspaceOpsStatus{
		Workspace: WorkspaceOpsWorkspace{Key: "TEST"},
		Daemon: WorkspaceOpsDaemon{
			AppData: DaemonInfo{Running: false},
		},
		Agents: []WorkspaceOpsAgent{
			{Name: "planner", Runnable: true},
		},
	}
	loadErr := errors.New("load failed")

	status, err := waitForWorkspaceOpsDaemon(context.Background(), "TEST", initial, func(context.Context, string) (*WorkspaceOpsStatus, error) {
		return nil, loadErr
	})
	if !errors.Is(err, loadErr) {
		t.Fatalf("waitForWorkspaceOpsDaemon error = %v, want loadErr", err)
	}
	if status != nil {
		t.Fatalf("status = %#v, want nil on loader error", status)
	}
}

func TestWaitForWorkspaceOpsDaemonAcceptsWorkspaceLocalDaemon(t *testing.T) {
	initial := &WorkspaceOpsStatus{
		Workspace: WorkspaceOpsWorkspace{Key: "TEST"},
		Daemon: WorkspaceOpsDaemon{
			AppData: DaemonInfo{Running: false},
		},
		Agents: []WorkspaceOpsAgent{
			{Name: "planner", Runnable: true},
		},
	}
	ready := &WorkspaceOpsStatus{
		Workspace: WorkspaceOpsWorkspace{Key: "TEST"},
		Daemon: WorkspaceOpsDaemon{
			AppData:        DaemonInfo{Running: false},
			WorkspaceLocal: DaemonInfo{Running: true, PID: 42},
		},
		Agents: []WorkspaceOpsAgent{
			{Name: "planner", Runnable: true},
		},
	}

	calls := 0
	status, err := waitForWorkspaceOpsDaemon(context.Background(), "TEST", initial, func(context.Context, string) (*WorkspaceOpsStatus, error) {
		calls++
		return ready, nil
	})
	if err != nil {
		t.Fatalf("waitForWorkspaceOpsDaemon returned error: %v", err)
	}
	if status != ready {
		t.Fatalf("status = %#v, want ready status", status)
	}
	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1", calls)
	}
}

func TestAgentDesiredRunnable(t *testing.T) {
	tests := []struct {
		name  string
		agent *domain.Agent
		want  bool
	}{
		{
			name: "running without explicit desired state is runnable",
			agent: &domain.Agent{
				State: domain.AgentStateActive,
			},
			want: true,
		},
		{
			name: "stopped actual state is not runnable",
			agent: &domain.Agent{
				State: domain.AgentStateStopped,
			},
			want: false,
		},
		{
			name: "desired stopped is not runnable",
			agent: &domain.Agent{
				State:        domain.AgentStateActive,
				DesiredState: domain.AgentDesiredStopped,
			},
			want: false,
		},
		{
			name: "desired draining is not runnable",
			agent: &domain.Agent{
				State:        domain.AgentStateActive,
				DesiredState: domain.AgentDesiredDraining,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentDesiredRunnable(tt.agent); got != tt.want {
				t.Fatalf("agentDesiredRunnable() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestWorkspaceOpsGlobalProblemsAcceptsWorkspaceLocalDaemon(t *testing.T) {
	status := &WorkspaceOpsStatus{
		Daemon: WorkspaceOpsDaemon{
			AppData:        DaemonInfo{Running: false},
			WorkspaceLocal: DaemonInfo{Running: true, PID: 202},
		},
		Repos: []WorkspaceOpsRepo{{Name: "app"}},
		Agents: []WorkspaceOpsAgent{
			{Name: "planner", Runnable: true},
		},
	}

	problems := workspaceOpsGlobalProblems(status)
	for _, problem := range problems {
		if problem.Code == "daemon_not_running" {
			t.Fatalf("problems = %#v, did not expect daemon_not_running", problems)
		}
	}
}

func TestWorkspaceOpsGlobalProblemsDetectsDuplicateDaemonOwnership(t *testing.T) {
	status := &WorkspaceOpsStatus{
		Daemon: WorkspaceOpsDaemon{
			AppData:        DaemonInfo{Running: true, PID: 101},
			WorkspaceLocal: DaemonInfo{Running: true, PID: 202},
		},
		Repos: []WorkspaceOpsRepo{{Name: "app"}},
		Agents: []WorkspaceOpsAgent{
			{Name: "planner", Runnable: false},
		},
	}

	problems := workspaceOpsGlobalProblems(status)
	if len(problems) != 1 {
		t.Fatalf("problems = %#v, want exactly duplicate daemon warning", problems)
	}
	if problems[0].Code != "duplicate_daemon_ownership" || problems[0].Severity != "warning" {
		t.Fatalf("problem = %#v, want duplicate_daemon_ownership warning", problems[0])
	}
}

func TestBuildLocalRuntimeFleetModeNotApplicable(t *testing.T) {
	clearRuntimeRoutingEnv(t)
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")

	enoent := &os.PathError{Op: "open", Path: "runtime.json", Err: os.ErrNotExist}
	out := buildLocalRuntime(nil, enoent)

	if out.Applicable {
		t.Error("Applicable = true, want false in fleet mode")
	}
	if out.Reason == "" {
		t.Error("Reason should explain why runtime is not applicable")
	}
	if out.Healthy {
		t.Error("Healthy = true, want false (zero value) when not applicable")
	}
	if out.Error != "" {
		t.Errorf("Error = %q, want empty in not-applicable case", out.Error)
	}
	if out.Runtime != nil {
		t.Errorf("Runtime = %+v, want nil in not-applicable case", out.Runtime)
	}
}

func TestBuildLocalRuntimeFleetModeIgnoresEvenAStartedRuntime(t *testing.T) {
	// Even if runtime.json happens to exist (someone manually ran
	// `loom local start`), in fleet mode the desktop runtime is still
	// "not applicable" — agents talk to fleet-db directly.
	clearRuntimeRoutingEnv(t)
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")

	snap := &local.RuntimeStatusSnapshot{
		Healthy: true,
		Runtime: &local.RuntimeSnapshot{PID: 123, URL: "http://127.0.0.1:8081"},
	}
	out := buildLocalRuntime(snap, nil)

	if out.Applicable {
		t.Error("Applicable = true, want false in fleet mode regardless of runtime state")
	}
	if out.Runtime != nil {
		t.Error("Runtime metadata should be hidden in fleet mode")
	}
}

func TestBuildLocalRuntimeDisabledModeNotApplicableForFleetDB(t *testing.T) {
	clearRuntimeRoutingEnv(t)
	t.Setenv("LOOM_ISSUE_BACKEND", "fleetdb")
	t.Setenv(envLocalRuntimeMode, "disabled")

	enoent := &os.PathError{Op: "open", Path: "runtime.json", Err: os.ErrNotExist}
	out := buildLocalRuntime(nil, enoent)

	if out.Applicable {
		t.Error("Applicable = true, want false when local runtime is disabled")
	}
	if !strings.Contains(out.Reason, envLocalRuntimeMode) {
		t.Errorf("Reason = %q, want it to mention %s", out.Reason, envLocalRuntimeMode)
	}
	if out.Error != "" {
		t.Errorf("Error = %q, want empty in not-applicable case", out.Error)
	}
	if out.Runtime != nil {
		t.Errorf("Runtime = %+v, want nil in not-applicable case", out.Runtime)
	}
}

func TestBuildLocalRuntimeExternalFleetDBNotApplicable(t *testing.T) {
	clearRuntimeRoutingEnv(t)
	t.Setenv("LOOM_ISSUE_BACKEND", "fleetdb")
	t.Setenv("LOOM_FLEET_DB_URL", "http://127.0.0.1:4567")

	enoent := &os.PathError{Op: "open", Path: "runtime.json", Err: os.ErrNotExist}
	out := buildLocalRuntime(nil, enoent)

	if out.Applicable {
		t.Error("Applicable = true, want false when external FleetDB is configured")
	}
	if !strings.Contains(out.Reason, "external FleetDB") {
		t.Errorf("Reason = %q, want external FleetDB explanation", out.Reason)
	}
	if out.Error != "" {
		t.Errorf("Error = %q, want empty in not-applicable case", out.Error)
	}
}

func TestBuildLocalRuntimeAPIModeNotApplicable(t *testing.T) {
	clearRuntimeRoutingEnv(t)
	t.Setenv("LOOM_ISSUE_BACKEND", "")
	t.Setenv("LOOM_SERVER_URL", "https://loom.example.test")

	enoent := &os.PathError{Op: "open", Path: "runtime.json", Err: os.ErrNotExist}
	out := buildLocalRuntime(nil, enoent)

	if out.Applicable {
		t.Error("Applicable = true, want false in API-backed mode")
	}
	if out.Error != "" {
		t.Errorf("Error = %q, want empty in not-applicable case", out.Error)
	}
}

func TestBuildLocalRuntimeExplicitDesktopOverridesServerSignals(t *testing.T) {
	clearRuntimeRoutingEnv(t)
	t.Setenv("LOOM_ISSUE_BACKEND", "fleetdb")
	t.Setenv("LOOM_FLEET_DB_URL", "http://127.0.0.1:4567")
	t.Setenv(envLocalRuntimeMode, "desktop")

	snap := &local.RuntimeStatusSnapshot{
		Healthy: true,
		Runtime: &local.RuntimeSnapshot{PID: 123, URL: "http://127.0.0.1:8081"},
	}
	out := buildLocalRuntime(snap, nil)

	if !out.Applicable {
		t.Fatal("Applicable = false, want explicit desktop mode to keep runtime applicable")
	}
	if !out.Healthy || out.Runtime == nil {
		t.Fatalf("runtime snapshot was not mirrored: %+v", out)
	}
}

func TestShouldEnsureLocalRuntimeSkipsDisabledDeployment(t *testing.T) {
	status := &WorkspaceOpsStatus{
		LocalRuntime: &WorkspaceOpsLocalRuntime{
			Applicable: false,
			Reason:     "headless/server deployment",
		},
	}

	if shouldEnsureLocalRuntime(status) {
		t.Fatal("shouldEnsureLocalRuntime = true, want false when local runtime is not applicable")
	}
}

func TestDaemonNotRunningFixRespectsNotApplicableRuntime(t *testing.T) {
	status := &WorkspaceOpsStatus{
		LocalRuntime: &WorkspaceOpsLocalRuntime{Applicable: false},
	}

	got := daemonNotRunningFix(status)
	if strings.Contains(got, "ensure-runtime") {
		t.Fatalf("daemonNotRunningFix = %q, should not suggest ensure-runtime when local runtime is not applicable", got)
	}
}

func TestBuildLocalRuntimeDesktopModeHealthy(t *testing.T) {
	clearRuntimeRoutingEnv(t)

	snap := &local.RuntimeStatusSnapshot{
		Healthy: true,
		Runtime: &local.RuntimeSnapshot{PID: 123, URL: "http://127.0.0.1:8081"},
	}
	out := buildLocalRuntime(snap, nil)

	if !out.Applicable {
		t.Error("Applicable = false, want true in desktop mode")
	}
	if out.Reason != "" {
		t.Errorf("Reason = %q, want empty when applicable", out.Reason)
	}
	if !out.Healthy {
		t.Error("Healthy = false, want true (mirrored from snapshot)")
	}
	if out.Runtime == nil || out.Runtime.PID != 123 {
		t.Errorf("Runtime not mirrored from snapshot: %+v", out.Runtime)
	}
}

func TestBuildLocalRuntimeDesktopModeUnhealthyMirrorsError(t *testing.T) {
	clearRuntimeRoutingEnv(t)

	snap := &local.RuntimeStatusSnapshot{
		Healthy: false,
		Error:   "health check timed out",
		Runtime: &local.RuntimeSnapshot{PID: 999},
	}
	out := buildLocalRuntime(snap, nil)

	if !out.Applicable {
		t.Error("Applicable should be true in desktop mode regardless of health")
	}
	if out.Healthy {
		t.Error("Healthy = true, want false (mirrored)")
	}
	if out.Error != "health check timed out" {
		t.Errorf("Error = %q, want mirrored from snapshot", out.Error)
	}
}

func TestBuildLocalRuntimeDesktopModeReadErrorSurfacesAsError(t *testing.T) {
	clearRuntimeRoutingEnv(t)

	readErr := &os.PathError{Op: "open", Path: "runtime.json", Err: os.ErrNotExist}
	out := buildLocalRuntime(nil, readErr)

	if !out.Applicable {
		t.Error("Applicable should be true in desktop mode")
	}
	if out.Error == "" {
		t.Error("Error should surface the read failure")
	}
	if !strings.Contains(out.Error, "runtime.json") {
		t.Errorf("Error = %q, want it to reference runtime.json", out.Error)
	}
}

func TestWorkspaceOpsAgentStatusFlagsUnknownRole(t *testing.T) {
	roleNames := map[string]struct{}{"plan": {}, "task": {}}
	agent := &domain.Agent{
		Name:         "rogue",
		RoleName:     "missing",
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
	}

	item, problems := workspaceOpsAgentStatus(bootstrap.WorkspaceLocalState{}, nil, roleNames, agent)

	if item.Runnable {
		t.Error("Runnable = true, want false (unknown role forces non-runnable)")
	}
	if item.Reason != "unknown_role" {
		t.Errorf("Reason = %q, want unknown_role", item.Reason)
	}
	if len(problems) == 0 {
		t.Fatal("expected agent_unknown_role problem")
	}
	if problems[0].Code != "agent_unknown_role" || problems[0].Severity != "error" {
		t.Errorf("problem = %+v, want code=agent_unknown_role severity=error", problems[0])
	}
	if problems[0].Agent != "rogue" {
		t.Errorf("problem.Agent = %q, want rogue", problems[0].Agent)
	}
}

func TestWorkspaceOpsAgentStatusFlagsMissingWorktree(t *testing.T) {
	roleNames := map[string]struct{}{"plan": {}}
	localState := bootstrap.WorkspaceLocalState{
		Path: "/some/workspace",
		Agents: map[string]bootstrap.AgentLocalState{
			"planner": {Worktree: "/nonexistent/path/that/has/no/dot-git"},
		},
	}
	agent := &domain.Agent{
		Name:         "planner",
		RoleName:     "plan",
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
	}

	item, problems := workspaceOpsAgentStatus(localState, nil, roleNames, agent)

	if !item.Runnable {
		t.Error("Runnable = false; want true (role exists, desired running)")
	}
	if item.WorktreeReady {
		t.Error("WorktreeReady = true; want false (no .git on disk)")
	}
	if item.Reason != "missing_local_worktree" {
		t.Errorf("Reason = %q, want missing_local_worktree", item.Reason)
	}
	if len(problems) == 0 {
		t.Fatal("expected agent_missing_worktree problem")
	}
	if problems[0].Code != "agent_missing_worktree" || problems[0].Severity != "error" {
		t.Errorf("problem = %+v, want code=agent_missing_worktree severity=error", problems[0])
	}
}

func TestWorkspaceOpsGlobalProblemsDetectsDaemonNotRunningWithRunnableAgent(t *testing.T) {
	status := &WorkspaceOpsStatus{
		Daemon: WorkspaceOpsDaemon{
			AppData:        DaemonInfo{Running: false},
			WorkspaceLocal: DaemonInfo{Running: false},
		},
		Repos:  []WorkspaceOpsRepo{{Name: "app"}},
		Agents: []WorkspaceOpsAgent{{Name: "planner", Runnable: true}},
	}

	problems := workspaceOpsGlobalProblems(status)

	var got *WorkspaceOpsProblem
	for i, p := range problems {
		if p.Code == "daemon_not_running" {
			got = &problems[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("expected daemon_not_running problem, got %+v", problems)
	}
	if got.Severity != "error" {
		t.Errorf("severity = %q, want error", got.Severity)
	}
	if !strings.Contains(got.Fix, "ensure-runtime") {
		t.Errorf("fix = %q, want it to suggest ensure-runtime", got.Fix)
	}
}

func TestWorkspaceOpsGlobalProblemsSilentWhenDaemonRunningOrNoRunnableAgents(t *testing.T) {
	cases := []struct {
		name   string
		status *WorkspaceOpsStatus
	}{
		{
			name: "daemon running, agent runnable",
			status: &WorkspaceOpsStatus{
				Daemon: WorkspaceOpsDaemon{AppData: DaemonInfo{Running: true, PID: 42}},
				Repos:  []WorkspaceOpsRepo{{Name: "app"}},
				Agents: []WorkspaceOpsAgent{{Name: "planner", Runnable: true}},
			},
		},
		{
			name: "daemon down, no runnable agents",
			status: &WorkspaceOpsStatus{
				Daemon: WorkspaceOpsDaemon{AppData: DaemonInfo{Running: false}},
				Repos:  []WorkspaceOpsRepo{{Name: "app"}},
				Agents: []WorkspaceOpsAgent{{Name: "stopped", Runnable: false}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, p := range workspaceOpsGlobalProblems(tc.status) {
				if p.Code == "daemon_not_running" {
					t.Errorf("unexpected daemon_not_running problem: %+v", p)
				}
			}
		})
	}
}

// TestWorkspaceOpsGlobalProblemsAcceptsRegisteredDaemon mirrors the
// WorkspaceLocal variant: when the supervisor publishes itself via the
// fleet-db Node registry, diagnose must NOT emit daemon_not_running even
// if the path-based detection (AppData / WorkspaceLocal) found nothing.
// This is the LOOM-3 false-positive case.
func TestWorkspaceOpsGlobalProblemsAcceptsRegisteredDaemon(t *testing.T) {
	status := &WorkspaceOpsStatus{
		Daemon: WorkspaceOpsDaemon{
			AppData:        DaemonInfo{Running: false},
			WorkspaceLocal: DaemonInfo{Running: false},
			Registered:     DaemonInfo{Running: true, PID: 31337, Cwd: "/some/cwd"},
		},
		Repos: []WorkspaceOpsRepo{{Name: "app"}},
		Agents: []WorkspaceOpsAgent{
			{Name: "planner", Runnable: true},
		},
	}

	problems := workspaceOpsGlobalProblems(status)
	for _, problem := range problems {
		if problem.Code == "daemon_not_running" {
			t.Fatalf("problems = %#v, did not expect daemon_not_running (Registered daemon is alive)", problems)
		}
	}
}

func TestWaitForWorkspaceOpsDaemonAcceptsRegisteredDaemon(t *testing.T) {
	initial := &WorkspaceOpsStatus{
		Workspace: WorkspaceOpsWorkspace{Key: "TEST"},
		Daemon: WorkspaceOpsDaemon{
			AppData: DaemonInfo{Running: false},
		},
		Agents: []WorkspaceOpsAgent{
			{Name: "planner", Runnable: true},
		},
	}
	ready := &WorkspaceOpsStatus{
		Workspace: WorkspaceOpsWorkspace{Key: "TEST"},
		Daemon: WorkspaceOpsDaemon{
			AppData:    DaemonInfo{Running: false},
			Registered: DaemonInfo{Running: true, PID: 42},
		},
		Agents: []WorkspaceOpsAgent{
			{Name: "planner", Runnable: true},
		},
	}

	calls := 0
	status, err := waitForWorkspaceOpsDaemon(context.Background(), "TEST", initial, func(context.Context, string) (*WorkspaceOpsStatus, error) {
		calls++
		return ready, nil
	})
	if err != nil {
		t.Fatalf("waitForWorkspaceOpsDaemon returned error: %v", err)
	}
	if status != ready {
		t.Fatalf("status = %#v, want ready status", status)
	}
	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1", calls)
	}
}

// TestBuildWorkspaceOpsStatusUsesNodeRegistry verifies the end-to-end
// behavior: a workspace with a runnable agent and a live supervisor Node
// in the fleet-db registry should NOT report daemon_not_running, even
// when the agent's path-based AppData/WorkspaceLocal lookups come up
// empty (the LOOM-3 cwd-mismatch scenario).
func TestBuildWorkspaceOpsStatusUsesNodeRegistry(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet") // skip local-runtime probe
	st := memstore.New()
	ctx := context.Background()

	ws := &domain.Workspace{Key: "WS-NODE"}
	repo := &domain.Repo{Name: "app", Groups: []string{}}
	agent := &domain.Agent{
		Name:         "planner",
		RoleName:     "plan",
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
	}
	roles := []*domain.Role{{Name: "plan"}}

	// Register a fresh local supervisor Node with PID/Cwd labels for
	// the current process. lockfile.IsProcessRunning will report it
	// alive since it's our own PID.
	livePID := os.Getpid()
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown-host"
	}
	nodeID := fmt.Sprintf("loom-supervisor-%s-%d", hostname, livePID)
	_, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    ws.Key,
		NodeID:          nodeID,
		OwnerActor:      "local",
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels: []string{
			"loom.daemon.pid=" + strconv.Itoa(livePID),
			"loom.daemon.cwd=/tmp/registered-cwd",
		},
		Capabilities: []string{"local-supervisor"},
		DrainState:   domain.NodeDrainActive,
		TTL:          2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	status, err := buildWorkspaceOpsStatus(ctx, st, ws, []*domain.Repo{repo}, []*domain.Agent{agent}, roles)
	if err != nil {
		t.Fatalf("buildWorkspaceOpsStatus: %v", err)
	}
	if !status.Daemon.Registered.Running {
		t.Fatalf("status.Daemon.Registered.Running = false, want true (live local Node should mark daemon as registered)")
	}
	if status.Daemon.Registered.PID != livePID {
		t.Fatalf("status.Daemon.Registered.PID = %d, want %d", status.Daemon.Registered.PID, livePID)
	}
	if status.Daemon.Registered.Cwd != "/tmp/registered-cwd" {
		t.Fatalf("status.Daemon.Registered.Cwd = %q, want /tmp/registered-cwd", status.Daemon.Registered.Cwd)
	}
	for _, problem := range status.Problems {
		if problem.Code == "daemon_not_running" {
			t.Fatalf("expected no daemon_not_running, got problems %#v", status.Problems)
		}
	}
}
