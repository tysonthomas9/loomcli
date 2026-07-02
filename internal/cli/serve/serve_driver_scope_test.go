package serve

import "testing"

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
