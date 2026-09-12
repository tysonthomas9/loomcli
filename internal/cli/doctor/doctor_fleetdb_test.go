package doctor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
)

var orphanFixedNow = time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

func liveSet(ids ...string) map[string]struct{} {
	live := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		live[id] = struct{}{}
	}
	return live
}

func countOrphanLines(detail string) int {
	n := 0
	for _, line := range strings.Split(detail, "\n") {
		if strings.HasPrefix(line, "issue=") {
			n++
		}
	}
	return n
}

// The regression test for PUPPET-240: issues assigned to a shared fleet-db
// actor, all genuinely held by live agents, must reach the PASS branch. The
// old name-based lookup reported all three as orphaned.
func TestEvaluateOrphanedFleetLocks_SharedActorPasses(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "PUPPET-240", Assignee: "loom", UpdatedAt: orphanFixedNow.Add(-3 * time.Hour)},
		{ID: "PUPPET-241", Assignee: "loom", UpdatedAt: orphanFixedNow.Add(-2 * time.Hour)},
		{ID: "PUPPET-242", Assignee: "loom", UpdatedAt: orphanFixedNow.Add(-1 * time.Hour)},
	}
	live := liveSet("PUPPET-240", "PUPPET-241", "PUPPET-242")

	result := evaluateOrphanedFleetLocks(issues, live, orphanFixedNow)

	if result.Status != StatusPass {
		t.Fatalf("expected StatusPass, got %v (summary=%q detail=%q)", result.Status, result.Summary, result.Detail)
	}
	if result.Name != "orphaned_fleet_locks" {
		t.Fatalf("unexpected check name %q", result.Name)
	}
	if !strings.Contains(result.Summary, "3 in_progress: 3 held by live agents") {
		t.Fatalf("summary does not report the live-held count: %q", result.Summary)
	}
}

func TestEvaluateOrphanedFleetLocks_OnlyTheDeadIssueWarns(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "PUPPET-1", Assignee: "loom", UpdatedAt: orphanFixedNow.Add(-5 * time.Hour)},
		{ID: "PUPPET-2", Assignee: "loom", UpdatedAt: orphanFixedNow.Add(-5 * time.Hour)},
		{ID: "PUPPET-3", Assignee: "loom", UpdatedAt: orphanFixedNow.Add(-30 * time.Second)},
		{ID: "PUPPET-4", Assignee: "loom", UpdatedAt: orphanFixedNow.Add(-2 * time.Hour)},
	}
	live := liveSet("PUPPET-1", "PUPPET-2")

	result := evaluateOrphanedFleetLocks(issues, live, orphanFixedNow)

	if result.Status != StatusWarn {
		t.Fatalf("expected StatusWarn, got %v (%q)", result.Status, result.Summary)
	}
	if got := countOrphanLines(result.Detail); got != 1 {
		t.Fatalf("expected exactly 1 orphan line, got %d:\n%s", got, result.Detail)
	}
	if !strings.Contains(result.Detail, "issue=PUPPET-4") {
		t.Fatalf("stale issue PUPPET-4 not reported:\n%s", result.Detail)
	}
	for _, id := range []string{"issue=PUPPET-1", "issue=PUPPET-2", "issue=PUPPET-3"} {
		if strings.Contains(result.Detail, id) {
			t.Fatalf("%s should not be reported:\n%s", id, result.Detail)
		}
	}
	if !strings.Contains(result.Summary, "1 of 4 in_progress") {
		t.Fatalf("summary should count 1 of 4: %q", result.Summary)
	}
}

func TestEvaluateOrphanedFleetLocks_GraceWindowSuppresses(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "PUPPET-9", Assignee: "loom", UpdatedAt: orphanFixedNow.Add(-30 * time.Second)},
	}

	result := evaluateOrphanedFleetLocks(issues, liveSet(), orphanFixedNow)

	if result.Status != StatusPass {
		t.Fatalf("expected StatusPass inside the grace window, got %v (%q)", result.Status, result.Detail)
	}
	if !strings.Contains(result.Summary, "1 claimed within") {
		t.Fatalf("summary should report the grace-suppressed count: %q", result.Summary)
	}
}

func TestEvaluateOrphanedFleetLocks_RemediationOnlyOnWarn(t *testing.T) {
	pass := evaluateOrphanedFleetLocks(
		[]backend.IssueData{{ID: "PUPPET-9", Assignee: "loom", UpdatedAt: orphanFixedNow.Add(-time.Hour)}},
		liveSet("PUPPET-9"), orphanFixedNow)
	if pass.Detail != "" {
		t.Fatalf("PASS result must carry no detail, got %q", pass.Detail)
	}

	warn := evaluateOrphanedFleetLocks(
		[]backend.IssueData{{ID: "PUPPET-9", Assignee: "loom", UpdatedAt: orphanFixedNow.Add(-time.Hour)}},
		liveSet(), orphanFixedNow)
	if warn.Status != StatusWarn {
		t.Fatalf("expected StatusWarn, got %v", warn.Status)
	}
	if !strings.Contains(warn.Detail, "loom data update") {
		t.Fatalf("WARN detail should carry the non-destructive remediation:\n%s", warn.Detail)
	}
	// `loom recover` kills the agent process and cleans its worktree; it must
	// not be suggested for an issue no live agent holds.
	if strings.Contains(warn.Detail, "loom recover") {
		t.Fatalf("WARN detail must not suggest the destructive `loom recover`:\n%s", warn.Detail)
	}
}

func TestEvaluateOrphanedFleetLocks_ZeroUpdatedAt(t *testing.T) {
	issues := []backend.IssueData{{ID: "PUPPET-9", Assignee: ""}}

	result := evaluateOrphanedFleetLocks(issues, liveSet(), orphanFixedNow)

	if result.Status != StatusWarn {
		t.Fatalf("expected StatusWarn for a zero UpdatedAt, got %v", result.Status)
	}
	if !strings.Contains(result.Detail, "idle=unknown") {
		t.Fatalf("expected idle=unknown for a zero timestamp:\n%s", result.Detail)
	}
}

func TestEvaluateOrphanedFleetLocks_OrphansAreSorted(t *testing.T) {
	stale := orphanFixedNow.Add(-3 * time.Hour)
	issues := []backend.IssueData{
		{ID: "PUPPET-3", Assignee: "loom", UpdatedAt: stale},
		{ID: "PUPPET-1", Assignee: "loom", UpdatedAt: stale},
		{ID: "PUPPET-2", Assignee: "loom", UpdatedAt: stale},
	}

	result := evaluateOrphanedFleetLocks(issues, liveSet(), orphanFixedNow)

	lines := strings.Split(result.Detail, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected 3 orphan lines:\n%s", result.Detail)
	}
	for i, want := range []string{"issue=PUPPET-1", "issue=PUPPET-2", "issue=PUPPET-3"} {
		if !strings.HasPrefix(lines[i], want) {
			t.Fatalf("line %d = %q, want prefix %q", i, lines[i], want)
		}
	}
}

// writeOrphanLockFixture points the workspace runtime dir at a temp dir and,
// when state != nil, writes a .loom/daemon-agents.json there.
func writeOrphanLockFixture(t *testing.T, state *monitor.DaemonAgentState) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", dir)
	t.Setenv("LOOM_FLEET_DB_NO_DISCOVERY", "1")
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	if state != nil {
		loomDir := filepath.Join(dir, ".loom")
		if err := os.MkdirAll(loomDir, 0o755); err != nil {
			t.Fatalf("mkdir .loom: %v", err)
		}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("marshal state: %v", err)
		}
		if err := os.WriteFile(filepath.Join(loomDir, "daemon-agents.json"), data, 0o600); err != nil {
			t.Fatalf("write state file: %v", err)
		}
	}
	t.Chdir(dir)
}

func freezeOrphanLockClock(t *testing.T, now time.Time) {
	t.Helper()
	orig := orphanLockNow
	orphanLockNow = func() time.Time { return now }
	t.Cleanup(func() { orphanLockNow = orig })
}

func TestCheckOrphanedFleetLocks_NoBackendSkips(t *testing.T) {
	deps, _, _, _, _ := NewTestDeps(t)
	deps.IssueBackend = nil

	if result := checkOrphanedFleetLocks(deps); result.Name != "" {
		t.Fatalf("expected empty CheckResult with no backend, got %+v", result)
	}
	if result := checkOrphanedFleetLocks(nil); result.Name != "" {
		t.Fatalf("expected empty CheckResult with nil deps, got %+v", result)
	}
}

func TestCheckOrphanedFleetLocks_ListErrorSkips(t *testing.T) {
	deps, _, _, _, issues := NewTestDeps(t)
	issues.ListErr = errors.New("backend down")

	if result := checkOrphanedFleetLocks(deps); result.Name != "" {
		t.Fatalf("expected empty CheckResult when List fails, got %+v", result)
	}
}

func TestCheckOrphanedFleetLocks_NoDaemonStateSkips(t *testing.T) {
	writeOrphanLockFixture(t, nil)
	deps, _, _, _, issues := NewTestDeps(t)
	issues.ListResult = []backend.IssueData{
		{ID: "PUPPET-1", Assignee: "loom", UpdatedAt: time.Now().Add(-3 * time.Hour)},
		{ID: "PUPPET-2", Assignee: "loom", UpdatedAt: time.Now().Add(-3 * time.Hour)},
	}

	// Previously this produced a 2-issue WARN; a missing state file is an
	// absence of evidence, not evidence of orphanhood.
	if result := checkOrphanedFleetLocks(deps); result.Name != "" {
		t.Fatalf("expected empty CheckResult with no daemon state, got %+v", result)
	}
}

func TestCheckOrphanedFleetLocks_DeadDaemonPIDSkips(t *testing.T) {
	writeOrphanLockFixture(t, &monitor.DaemonAgentState{
		PID: 2147483600,
		Agents: []monitor.DaemonAgentStateEntry{
			{Worktree: "planner", Status: "running", TaskID: "PUPPET-1"},
		},
	})
	deps, _, _, _, issues := NewTestDeps(t)
	issues.ListResult = []backend.IssueData{
		{ID: "PUPPET-1", Assignee: "loom", UpdatedAt: time.Now().Add(-3 * time.Hour)},
	}

	if result := checkOrphanedFleetLocks(deps); result.Name != "" {
		t.Fatalf("expected empty CheckResult when the daemon PID is dead, got %+v", result)
	}
}

// The end-to-end shape of the PUPPET-240 fix, mirroring live PUPPET data: a
// running agent naming its task, a stopped agent naming none, and an issue
// assigned to the shared fleet-db actor.
func TestCheckOrphanedFleetLocks_LiveAgentsPass(t *testing.T) {
	now := time.Now()
	writeOrphanLockFixture(t, &monitor.DaemonAgentState{
		PID: os.Getpid(),
		Agents: []monitor.DaemonAgentStateEntry{
			{Worktree: "planner", Status: "running", Role: "planner", TaskID: "PUPPET-240"},
			{Worktree: "worker-2", Status: "stopped", Role: "coder", TaskID: ""},
		},
	})
	freezeOrphanLockClock(t, now)

	deps, _, _, _, issues := NewTestDeps(t)
	issues.ListResult = []backend.IssueData{
		{ID: "PUPPET-240", Assignee: "loom", UpdatedAt: now.Add(-3 * time.Hour)},
	}

	result := checkOrphanedFleetLocks(deps)

	if result.Status != StatusPass {
		t.Fatalf("expected StatusPass for a live-held issue, got %v (%q / %q)", result.Status, result.Summary, result.Detail)
	}
	if !strings.Contains(result.Summary, "1 held by live agents") {
		t.Fatalf("summary should report the live-held issue: %q", result.Summary)
	}
}

func TestCheckOrphanedFleetLocks_StaleIssueWarns(t *testing.T) {
	now := time.Now()
	writeOrphanLockFixture(t, &monitor.DaemonAgentState{
		PID: os.Getpid(),
		Agents: []monitor.DaemonAgentStateEntry{
			{Worktree: "planner", Status: "running", Role: "planner", TaskID: "PUPPET-240"},
		},
	})
	freezeOrphanLockClock(t, now)

	deps, _, _, _, issues := NewTestDeps(t)
	issues.ListResult = []backend.IssueData{
		{ID: "PUPPET-240", Assignee: "loom", UpdatedAt: now.Add(-3 * time.Hour)},
		{ID: "PUPPET-999", Assignee: "loom", UpdatedAt: now.Add(-3 * time.Hour)},
	}

	result := checkOrphanedFleetLocks(deps)

	if result.Status != StatusWarn {
		t.Fatalf("expected StatusWarn, got %v (%q)", result.Status, result.Summary)
	}
	if got := countOrphanLines(result.Detail); got != 1 {
		t.Fatalf("expected exactly 1 orphan line, got %d:\n%s", got, result.Detail)
	}
	if !strings.Contains(result.Detail, "issue=PUPPET-999") {
		t.Fatalf("expected the unheld issue to be named:\n%s", result.Detail)
	}
}
