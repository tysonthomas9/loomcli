package workspace

import (
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/local"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

func clearRuntimeRoutingEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LOOM_ISSUE_BACKEND", "")
	t.Setenv("LOOM_SERVER_URL", "")
	t.Setenv("LOOM_FLEET_DB_URL", "")
	t.Setenv(envLocalRuntimeMode, "")
}

func TestAgentDesiredRunnable(t *testing.T) {
	tests := []struct {
		name  string
		agent *agents.AgentServiceRecord
		want  bool
	}{
		{
			name:  "desired running is runnable",
			agent: &agents.AgentServiceRecord{DesiredState: agents.DesiredRunning},
			want:  true,
		},
		{
			name:  "desired paused is not runnable",
			agent: &agents.AgentServiceRecord{DesiredState: agents.DesiredPaused},
			want:  false,
		},
		{
			name:  "desired stopped is not runnable",
			agent: &agents.AgentServiceRecord{DesiredState: agents.DesiredStopped},
			want:  false,
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
	rolesByName := map[string]*agents.Role{
		"plan": {Name: "plan"},
		"task": {Name: "task"},
	}
	agent := &agents.AgentServiceRecord{
		ServiceID:    "rogue",
		RoleName:     "missing",
		DesiredState: agents.DesiredRunning,
	}

	item, problems := workspaceOpsAgentStatus(bootstrap.WorkspaceLocalState{}, nil, rolesByName, agent)

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
	rolesByName := map[string]*agents.Role{"plan": {Name: "plan"}}
	localState := bootstrap.WorkspaceLocalState{
		Path: "/some/workspace",
		Agents: map[string]bootstrap.AgentLocalState{
			"planner": {Worktree: "/nonexistent/path/that/has/no/dot-git"},
		},
	}
	agent := &agents.AgentServiceRecord{
		ServiceID:    "planner",
		RoleName:     "plan",
		DesiredState: agents.DesiredRunning,
	}

	item, problems := workspaceOpsAgentStatus(localState, nil, rolesByName, agent)

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

func TestWorkspaceOpsAgentStatusDoesNotRequireInteractiveWorktree(t *testing.T) {
	rolesByName := map[string]*agents.Role{
		"pr-review": {
			Name: "pr-review",
			Kind: agents.RoleKindInteractive,
		},
	}
	localState := bootstrap.WorkspaceLocalState{Path: "/some/workspace"}
	agent := &agents.AgentServiceRecord{
		ServiceID:    "reviewer",
		RoleName:     "pr-review",
		DesiredState: agents.DesiredRunning,
	}

	item, problems := workspaceOpsAgentStatus(localState, nil, rolesByName, agent)

	if !item.Runnable {
		t.Error("Runnable = false; want true for interactive agent")
	}
	if item.Reason == "missing_local_worktree" {
		t.Fatalf("Reason = %q, interactive agents run from workspace root", item.Reason)
	}
	for _, problem := range problems {
		if problem.Code == "agent_missing_worktree" {
			t.Fatalf("interactive agent got false-positive worktree problem: %+v", problem)
		}
	}
}
