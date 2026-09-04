package supervisor

import (
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func runTurnTimeoutEnv(t *testing.T, env []string) (string, bool) {
	t.Helper()
	const key = "LOOM_RUN_TURN_TIMEOUT_SECONDS="
	found := ""
	seen := 0
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, key); ok {
			found = v
			seen++
		}
	}
	if seen > 1 {
		t.Fatalf("%s exported %d times, want at most once — a duplicate leaves the winner up to exec's dedup rules: %v", key, seen, env)
	}
	return found, seen == 1
}

func agentWithCap(seconds *int) *AgentProcess {
	return &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "worker-2", Role: "coder"},
		RoleConfig: cfgpkg.RoleConfig{MaxRunDuration: seconds},
	}
}

// The whole point of PUPPET-443: the child's per-turn deadline comes from THIS
// agent's run-duration cap, so a role that raises max_run_duration actually gets
// the longer turn. Before this, every role inherited one fleet-wide number
// derived from the silence watchdog and the role's setting was dead config.
func TestAppendRoleEnv_RunTurnTimeoutFromRoleCap(t *testing.T) {
	t.Parallel()

	cap3600 := 3600
	cap60 := 60
	cap120 := 120
	cap121 := 121
	cap0 := 0

	tests := []struct {
		name    string
		role    *int
		daemon  int
		want    string
		wantSet bool
	}{
		{"role cap wins over the daemon default", &cap3600, defaultMaxRunDurationSeconds, "3480", true},
		{"role naming nothing inherits the daemon default", nil, 5400, "5280", true},
		{"role cap of zero disables: export nothing", &cap0, defaultMaxRunDurationSeconds, "", false},
		{"daemon cap of zero disables: export nothing", nil, 0, "", false},
		{"cap inside the margin: export nothing", &cap60, defaultMaxRunDurationSeconds, "", false},
		{"cap equal to the margin: export nothing", &cap120, defaultMaxRunDurationSeconds, "", false},
		{"cap one second past the margin", &cap121, defaultMaxRunDurationSeconds, "1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := runTurnTimeoutEnv(t, appendRoleEnv(nil, agentWithCap(tt.role), tt.daemon))
			if ok != tt.wantSet {
				t.Fatalf("LOOM_RUN_TURN_TIMEOUT_SECONDS exported = %v (%q), want exported = %v", ok, got, tt.wantSet)
			}
			if got != tt.want {
				t.Errorf("LOOM_RUN_TURN_TIMEOUT_SECONDS = %q, want %q", got, tt.want)
			}
		})
	}
}

// An operator can set LOOM_RUN_TURN_TIMEOUT_SECONDS on the daemon by hand, and
// every LOOM_ variable is inherited by the child. The per-agent value must win,
// and must win by REPLACING the inherited entry rather than trailing it — a
// duplicate would leave the outcome to exec's dedup rules instead of to us.
func TestAppendRoleEnv_RunTurnTimeoutOverridesInherited(t *testing.T) {
	t.Parallel()

	inherited := []string{"LOOM_RUN_TURN_TIMEOUT_SECONDS=999", "PATH=/usr/bin"}

	t.Run("per-role export replaces it", func(t *testing.T) {
		t.Parallel()
		capSeconds := 3600
		env := appendRoleEnv(append([]string(nil), inherited...), agentWithCap(&capSeconds), defaultMaxRunDurationSeconds)
		got, ok := runTurnTimeoutEnv(t, env)
		if !ok || got != "3480" {
			t.Fatalf("LOOM_RUN_TURN_TIMEOUT_SECONDS = %q (exported=%v), want %q", got, ok, "3480")
		}
	})

	t.Run("disabled cap drops it", func(t *testing.T) {
		t.Parallel()
		capSeconds := 0
		env := appendRoleEnv(append([]string(nil), inherited...), agentWithCap(&capSeconds), defaultMaxRunDurationSeconds)
		if got, ok := runTurnTimeoutEnv(t, env); ok {
			t.Fatalf("LOOM_RUN_TURN_TIMEOUT_SECONDS = %q, want it dropped — a disabled cap means no deadline", got)
		}
		// Unrelated inherited entries must survive the filtering.
		if len(env) == 0 || env[0] != "PATH=/usr/bin" {
			t.Errorf("env = %v, want the unrelated inherited entries preserved", env)
		}
	})
}

// The precedence rule lives in exactly one place; this pins that the exported
// deadline and the cap the health checker enforces cannot disagree.
func TestRunTurnTimeoutIsInsideTheEnforcedCap(t *testing.T) {
	t.Parallel()

	capSeconds := 3600
	ap := agentWithCap(&capSeconds)
	s := &Supervisor{}

	enforced := s.maxRunDurationFor(ap)
	exported := runTurnTimeoutSecondsFor(ap, defaultMaxRunDurationSeconds)
	if exported <= 0 {
		t.Fatalf("runTurnTimeoutSecondsFor() = %d, want a positive deadline", exported)
	}
	if int(enforced.Seconds())-exported != runTurnDeadlineMarginSeconds {
		t.Errorf("margin between the enforced cap (%v) and the exported deadline (%ds) = %d, want %d",
			enforced, exported, int(enforced.Seconds())-exported, runTurnDeadlineMarginSeconds)
	}
}
