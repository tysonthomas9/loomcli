package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// The reported incident's values, kept verbatim so a regression is
// recognisable: a live PUPPET supervisor's PID printed next to the dogfood
// workspace's six-day-old corpse.
const (
	incidentLivePID  = 75714
	incidentDeadPID  = 61906
	incidentDeadTime = "2026-08-09T18:23:08+02:00"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ts
}

// TestBuildDaemonStatusView_PUPPET57Regression is the bug itself: detection
// resolved the live workspace daemon, but the state file on disk belongs to a
// different, dead daemon. Nothing from that file may reach the output — and
// crucially, its empty agent list must NOT be reported as "Agents: 0".
func TestBuildDaemonStatusView_PUPPET57Regression(t *testing.T) {
	now := time.Now()
	liveStart := now.Add(-2 * time.Hour)
	statePath := "/Users/x/.loom/workspaces/dogfood/.loom/daemon-agents.json"

	v := buildDaemonStatusView(daemonStatusInputs{
		RT: cli.DaemonRuntimeInfo{
			Running:   true,
			PID:       incidentLivePID,
			Source:    "workspace-lock",
			StartedAt: liveStart,
			Dir:       "/Users/x/.loom/workspaces/PUPPET",
		},
		State: &DaemonState{
			PID:       incidentDeadPID,
			StartedAt: mustTime(t, incidentDeadTime),
			Agents:    nil,
		},
		StatePath:  statePath,
		StateMTime: now,
		LiveCount:  agentCountUnknown,
		Now:        now,
	})

	if v.Trusted {
		t.Error("Trusted = true; a state file naming a different PID must not be believed")
	}
	if v.AgentCount != agentCountUnknown {
		t.Errorf("AgentCount = %d, want unknown (%d)", v.AgentCount, agentCountUnknown)
	}
	if !v.StartedAt.Equal(liveStart) {
		t.Errorf("StartedAt = %v, want the detected daemon's %v", v.StartedAt, liveStart)
	}
	if len(v.Warnings) != 1 {
		t.Fatalf("want exactly one warning, got %v", v.Warnings)
	}
	for _, want := range []string{statePath, "61906", "75714"} {
		if !strings.Contains(v.Warnings[0], want) {
			t.Errorf("warning %q does not name %q", v.Warnings[0], want)
		}
	}

	out := strings.Join(v.HeaderLines(), "\n")
	if strings.Contains(out, "2026-08-09") {
		t.Errorf("output leaks the dead daemon's start time:\n%s", out)
	}
	if strings.Contains(out, "Agents: 0") {
		t.Errorf("output reports an unknown agent count as zero:\n%s", out)
	}
	if !strings.Contains(out, "Agents: unknown") {
		t.Errorf("output should say the agent count is unknown:\n%s", out)
	}
	// The reader must be able to tell which daemon is being described.
	if !strings.Contains(out, "Source: workspace-lock (/Users/x/.loom/workspaces/PUPPET)") {
		t.Errorf("output does not disclose the cross-directory target:\n%s", out)
	}
}

// TestBuildDaemonStatusView_MatchingPIDIsTrusted: the ordinary in-project case
// must behave exactly as before — the state file is the richer source, so it
// wins on both count and start time, with nothing to warn about.
func TestBuildDaemonStatusView_MatchingPIDIsTrusted(t *testing.T) {
	now := time.Now()
	stateStart := now.Add(-30 * time.Minute)

	v := buildDaemonStatusView(daemonStatusInputs{
		RT: cli.DaemonRuntimeInfo{
			Running:   true,
			PID:       incidentLivePID,
			Source:    "lock",
			StartedAt: now.Add(-31 * time.Minute),
			Dir:       "/proj",
		},
		State: &DaemonState{
			PID:       incidentLivePID,
			StartedAt: stateStart,
			Agents:    make([]DaemonAgentStatus, 8),
		},
		StatePath:  "/proj/.loom/daemon-agents.json",
		StateMTime: now.Add(-2 * time.Second),
		LiveCount:  agentCountUnknown,
		Now:        now,
	})

	if !v.Trusted {
		t.Fatalf("Trusted = false for a matching, fresh state file; warnings=%v", v.Warnings)
	}
	if v.AgentCount != 8 {
		t.Errorf("AgentCount = %d, want 8", v.AgentCount)
	}
	if !v.StartedAt.Equal(stateStart) {
		t.Errorf("StartedAt = %v, want the state file's %v", v.StartedAt, stateStart)
	}
	if len(v.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", v.Warnings)
	}

	out := strings.Join(v.HeaderLines(), "\n")
	if !strings.Contains(out, "Agents: 8") {
		t.Errorf("want a trusted count in the output:\n%s", out)
	}
	// "lock" is the cwd's own daemon; no provenance line needed.
	if strings.Contains(out, "Source:") {
		t.Errorf("in-project status should not add a Source line:\n%s", out)
	}
}

// TestBuildDaemonStatusView_MissingStateFile: today an unreadable state file
// suppresses Started: entirely. The lock already knows the start time, so it
// must still be printed.
func TestBuildDaemonStatusView_MissingStateFile(t *testing.T) {
	now := time.Now()
	started := now.Add(-time.Hour)

	v := buildDaemonStatusView(daemonStatusInputs{
		RT:        cli.DaemonRuntimeInfo{Running: true, PID: 4242, Source: "lock", StartedAt: started, Dir: "/proj"},
		State:     nil,
		StatePath: "/proj/.loom/daemon-agents.json",
		LiveCount: agentCountUnknown,
		Now:       now,
	})

	if v.Trusted {
		t.Error("Trusted = true with no state file")
	}
	if !v.StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, want %v from the lock evidence", v.StartedAt, started)
	}
	if len(v.Warnings) != 0 {
		t.Errorf("a missing state file is not suspicious on its own; warnings=%v", v.Warnings)
	}
	out := strings.Join(v.HeaderLines(), "\n")
	if !strings.Contains(out, "Started: "+started.Format(time.RFC3339)) {
		t.Errorf("Started: should survive a missing state file:\n%s", out)
	}
	if !strings.Contains(out, "Agents: unknown") {
		t.Errorf("want Agents: unknown:\n%s", out)
	}
}

// TestBuildDaemonStatusView_ZeroStartedAtRendersUnknown guards the specific
// ugliness of a formatted zero time.
func TestBuildDaemonStatusView_ZeroStartedAtRendersUnknown(t *testing.T) {
	v := buildDaemonStatusView(daemonStatusInputs{
		RT:  cli.DaemonRuntimeInfo{Running: true, PID: 7, Source: "lock", Dir: "/proj"},
		Now: time.Now(),
	})

	lines := v.HeaderLines()
	found := false
	for _, l := range lines {
		if l == "Started: unknown" {
			found = true
		}
		if strings.Contains(l, "0001-01-01") {
			t.Errorf("zero time formatted into output: %q", l)
		}
	}
	if !found {
		t.Errorf("want an exact %q line, got %v", "Started: unknown", lines)
	}
}

// TestBuildDaemonStatusView_LiveCountWinsOverUntrustedState: the daemon's own
// answer is current and identity-bound, so it outranks a snapshot that failed
// the identity check.
func TestBuildDaemonStatusView_LiveCountWinsOverUntrustedState(t *testing.T) {
	now := time.Now()

	v := buildDaemonStatusView(daemonStatusInputs{
		RT:         cli.DaemonRuntimeInfo{Running: true, PID: incidentLivePID, Source: "workspace-lock", StartedAt: now, Dir: "/ws"},
		State:      &DaemonState{PID: incidentDeadPID, Agents: make([]DaemonAgentStatus, 3)},
		StatePath:  "/other/.loom/daemon-agents.json",
		StateMTime: now,
		LiveCount:  8,
		Now:        now,
	})

	if v.Trusted {
		t.Error("Trusted = true despite a PID mismatch")
	}
	if v.AgentCount != 8 {
		t.Errorf("AgentCount = %d, want the live socket's 8 (not the state file's 3)", v.AgentCount)
	}
	if !strings.Contains(strings.Join(v.HeaderLines(), "\n"), "Agents: 8") {
		t.Error("live count should be rendered")
	}
}

// TestBuildDaemonStatusView_StaleMTimeDistrusted: a matching PID is not enough
// if nothing is writing the file — a wedged daemon's last snapshot is not a
// description of now.
func TestBuildDaemonStatusView_StaleMTimeDistrusted(t *testing.T) {
	now := time.Now()
	mtime := now.Add(-5 * time.Minute)

	v := buildDaemonStatusView(daemonStatusInputs{
		RT:         cli.DaemonRuntimeInfo{Running: true, PID: incidentLivePID, Source: "lock", StartedAt: now.Add(-time.Hour), Dir: "/proj"},
		State:      &DaemonState{PID: incidentLivePID, Agents: make([]DaemonAgentStatus, 4)},
		StatePath:  "/proj/.loom/daemon-agents.json",
		StateMTime: mtime,
		LiveCount:  agentCountUnknown,
		Now:        now,
	})

	if v.Trusted {
		t.Error("Trusted = true for a state file older than the stale threshold")
	}
	if len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], mtime.Format(time.RFC3339)) {
		t.Errorf("warning should name the mtime %s, got %v", mtime.Format(time.RFC3339), v.Warnings)
	}
	if v.AgentCount != agentCountUnknown {
		t.Errorf("AgentCount = %d, want unknown", v.AgentCount)
	}
}

// TestBuildDaemonStatusView_FreshWithinThreshold pins the boundary so the
// stale check cannot start rejecting healthy daemons.
func TestBuildDaemonStatusView_FreshWithinThreshold(t *testing.T) {
	now := time.Now()

	v := buildDaemonStatusView(daemonStatusInputs{
		RT:         cli.DaemonRuntimeInfo{Running: true, PID: 100, Source: "lock", StartedAt: now.Add(-time.Hour), Dir: "/proj"},
		State:      &DaemonState{PID: 100, StartedAt: now.Add(-time.Hour), Agents: make([]DaemonAgentStatus, 2)},
		StatePath:  "/proj/.loom/daemon-agents.json",
		StateMTime: now.Add(-statusStateStaleThreshold + time.Second),
		LiveCount:  agentCountUnknown,
		Now:        now,
	})

	if !v.Trusted {
		t.Errorf("a state file inside the %s threshold must stay trusted; warnings=%v",
			statusStateStaleThreshold, v.Warnings)
	}
	if v.AgentCount != 2 {
		t.Errorf("AgentCount = %d, want 2", v.AgentCount)
	}
}

// TestBuildDaemonStatusView_UnknownPIDCannotVerify: liveness proved without an
// identity (lock held, contents unreadable) leaves nothing to match the state
// file against, so it stays untrusted rather than being assumed to fit.
func TestBuildDaemonStatusView_UnknownPIDCannotVerify(t *testing.T) {
	now := time.Now()
	statePath := "/proj/.loom/daemon-agents.json"

	v := buildDaemonStatusView(daemonStatusInputs{
		RT:         cli.DaemonRuntimeInfo{Running: true, PID: 0, Source: "lock", Dir: "/proj"},
		State:      &DaemonState{PID: incidentDeadPID, Agents: make([]DaemonAgentStatus, 5)},
		StatePath:  statePath,
		StateMTime: now,
		LiveCount:  agentCountUnknown,
		Now:        now,
	})

	if v.Trusted {
		t.Error("Trusted = true with an unknown daemon PID")
	}
	if v.AgentCount != agentCountUnknown {
		t.Errorf("AgentCount = %d, want unknown", v.AgentCount)
	}
	if len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], statePath) {
		t.Errorf("warning should name the unverifiable file, got %v", v.Warnings)
	}
}

// TestDaemonStatusViewHeaderLines_TrustedZeroIsReal: `Agents: 0` remains
// meaningful — a trusted daemon with no agents genuinely has none.
func TestDaemonStatusViewHeaderLines_TrustedZeroIsReal(t *testing.T) {
	now := time.Now()

	v := buildDaemonStatusView(daemonStatusInputs{
		RT:         cli.DaemonRuntimeInfo{Running: true, PID: 55, Source: "lock", StartedAt: now, Dir: "/proj"},
		State:      &DaemonState{PID: 55, StartedAt: now, Agents: nil},
		StatePath:  "/proj/.loom/daemon-agents.json",
		StateMTime: now,
		LiveCount:  agentCountUnknown,
		Now:        now,
	})

	if !v.Trusted {
		t.Fatalf("expected trust; warnings=%v", v.Warnings)
	}
	if !strings.Contains(strings.Join(v.HeaderLines(), "\n"), "Agents: 0") {
		t.Error("a trusted empty agent list must still print Agents: 0")
	}
}
