package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/local"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

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

func TestShouldWarnLocalRuntimeUnhealthy(t *testing.T) {
	enoent := &os.PathError{Op: "open", Path: "runtime.json", Err: os.ErrNotExist}
	healthy := &local.RuntimeStatusSnapshot{Healthy: true}
	unhealthy := &local.RuntimeStatusSnapshot{Healthy: false}

	tests := []struct {
		name        string
		fleetMode   bool
		runtime     *local.RuntimeStatusSnapshot
		err         error
		wantWarning bool
	}{
		{name: "healthy never warns", runtime: healthy, wantWarning: false},
		{name: "missing in fleet mode is silent", fleetMode: true, runtime: nil, err: enoent, wantWarning: false},
		{name: "missing in non-fleet mode warns", fleetMode: false, runtime: nil, err: enoent, wantWarning: true},
		{name: "unhealthy file exists always warns", fleetMode: true, runtime: unhealthy, err: nil, wantWarning: true},
		{name: "non-ENOENT error warns even in fleet mode", fleetMode: true, runtime: nil, err: errors.New("permission denied"), wantWarning: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fleetMode {
				t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
			} else {
				t.Setenv("LOOM_ISSUE_BACKEND", "")
			}
			if got := shouldWarnLocalRuntimeUnhealthy(tc.runtime, tc.err); got != tc.wantWarning {
				t.Errorf("shouldWarnLocalRuntimeUnhealthy = %v, want %v", got, tc.wantWarning)
			}
		})
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
