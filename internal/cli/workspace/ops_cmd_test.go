package workspace

import (
	"context"
	"errors"
	"testing"

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
