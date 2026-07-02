package serve

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

// TestDriverAutomationWorkspaceScope pins the env-var wrapper that ALL driver-run
// automation loops share — cron, executor, sweepers, and now the issue-journal
// bridge (which used to sit on LOOM_WORKSPACE alone). The bridge feeds task.ready
// events that fire prompt-agent bindings whose runs the executor must claim, so
// bridge and executor MUST resolve the same workspace scope.
func TestDriverAutomationWorkspaceScope(t *testing.T) {
	cases := []struct {
		name      string
		override  string // LOOM_DRIVER_EXECUTOR_WORKSPACE
		inherited string // LOOM_WORKSPACE
		want      string
	}{
		{"unset inherits LOOM_WORKSPACE", "", "SANDBOX", "SANDBOX"},
		{"star unscopes to all workspaces", "*", "SANDBOX", ""},
		{"explicit override wins", "OTHER", "SANDBOX", "OTHER"},
		{"both unset stays empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envLoomDriverExecutorWorkspace, tc.override)
			t.Setenv(bootstrap.EnvWorkspace, tc.inherited)
			if got := driverAutomationWorkspaceScope(); got != tc.want {
				t.Fatalf("driverAutomationWorkspaceScope() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveDriverExecutorWorkspace pins the LOOM_DRIVER_EXECUTOR_WORKSPACE
// override semantics: unset inherits LOOM_WORKSPACE, "*" unscopes to all
// workspaces (empty string, which Executor/TaskWorker treat as "all"), and any
// other value overrides the inherited workspace.
func TestResolveDriverExecutorWorkspace(t *testing.T) {
	cases := []struct {
		name      string
		override  string
		inherited string
		want      string
	}{
		{"unset inherits LOOM_WORKSPACE", "", "LOCALMODE", "LOCALMODE"},
		{"unset with no inherited stays empty", "", "", ""},
		{"whitespace override inherits", "   ", "LOCALMODE", "LOCALMODE"},
		{"star unscopes to all workspaces", "*", "LOCALMODE", ""},
		{"star unscopes even with no inherited", "*", "", ""},
		{"explicit name overrides inherited", "SANDBOX", "LOCALMODE", "SANDBOX"},
		{"explicit name is trimmed", "  SANDBOX  ", "LOCALMODE", "SANDBOX"},
		{"explicit name with no inherited", "SANDBOX", "", "SANDBOX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveDriverExecutorWorkspace(tc.override, tc.inherited); got != tc.want {
				t.Fatalf("resolveDriverExecutorWorkspace(%q, %q) = %q, want %q", tc.override, tc.inherited, got, tc.want)
			}
		})
	}
}
