package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// spawnLedgerRecords builds n records for one agent with the given status,
// spread across the health window so every row falls inside it.
func spawnLedgerRecords(prefix, agent string, status sessions.SessionStatus, n int, now time.Time) []sessions.SessionRecord {
	recs := make([]sessions.SessionRecord, 0, n)
	for i := 0; i < n; i++ {
		rec := sessions.SessionRecord{
			SessionID: prefix + "-" + string(rune('a'+i)),
			AgentName: agent,
			StartedAt: now.Add(-time.Duration(30-i) * time.Minute),
			Status:    status,
		}
		if status != sessions.StatusCompleted {
			rec.ErrorClass = "spawn_failure"
		}
		recs = append(recs, rec)
	}
	return recs
}

// stageKnownAgentsFixture points the workspace at a fresh runtime dir and
// stages the ledger, using the same env seam 233 and 338 use.
func stageKnownAgentsFixture(t *testing.T, recs []sessions.SessionRecord, profiles ...string) string {
	t.Helper()
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)
	if len(profiles) > 0 {
		writeProfiles(t, runtimeDir, profiles...)
	}
	stageLedger(t, runtimeDir, recs)
	return runtimeDir
}

// TestCheckSpawnHealth_CountsOnlyConfiguredAgents is the defect this ticket
// exists for: rows written by an agent the workspace never configured — most
// of all the ones `go test` writes — must not prop up the success rate. Six
// real failures and six stray successes read as 50% unfiltered; scoped to the
// configured agent they are the total outage they actually are.
func TestCheckSpawnHealth_CountsOnlyConfiguredAgents(t *testing.T) {
	now := time.Now()
	recs := append(
		spawnLedgerRecords("real", "planner", sessions.StatusFailed, 6, now),
		spawnLedgerRecords("stray", "go-test-writer", sessions.StatusCompleted, 6, now)...,
	)
	stageKnownAgentsFixture(t, recs, "planner")

	res := checkSpawnHealth()
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want fail (%+v)", res.Status, res)
	}
	if !strings.Contains(res.Summary, "6 failed") {
		t.Errorf("summary = %q, want only the configured agent's 6 failures counted", res.Summary)
	}
}

// TestCheckSpawnHealth_NoProfilesDirCountsEverything pins the invariant that
// matters most: a workspace with no profiles/ directory gets an empty
// allowlist, and an empty allowlist means the whole ledger, exactly as before.
func TestCheckSpawnHealth_NoProfilesDirCountsEverything(t *testing.T) {
	now := time.Now()
	recs := append(
		spawnLedgerRecords("real", "planner", sessions.StatusFailed, 6, now),
		spawnLedgerRecords("stray", "go-test-writer", sessions.StatusCompleted, 6, now)...,
	)
	stageKnownAgentsFixture(t, recs)

	res := checkSpawnHealth()
	if res.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%+v)", res.Status, res)
	}
	if !strings.Contains(res.Summary, "6 of 12") {
		t.Errorf("summary = %q, want every row counted without profiles/", res.Summary)
	}
}

// TestCheckSpawnHealth_ExcludesEmptyAgentName covers the unattributed row: ""
// is never a profile directory name, so a non-empty allowlist drops it. That
// is the intent, not an accident — an unattributed row is exactly what this
// ticket excludes.
func TestCheckSpawnHealth_ExcludesEmptyAgentName(t *testing.T) {
	now := time.Now()
	recs := append(
		spawnLedgerRecords("anon", "", sessions.StatusCompleted, 6, now),
		spawnLedgerRecords("real", "planner", sessions.StatusFailed, 5, now)...,
	)
	stageKnownAgentsFixture(t, recs, "planner")

	res := checkSpawnHealth()
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want fail (%+v)", res.Status, res)
	}
	if !strings.Contains(res.Summary, "5 failed") {
		t.Errorf("summary = %q, want the unattributed rows excluded", res.Summary)
	}
}

// TestCheckSpawnHealth_FilteringCanFallBelowMinRuns: scoping can push a quiet
// workspace under the minimum-runs floor. It must degrade to 233's
// not-enough-data branch, never report a 0% success rate.
func TestCheckSpawnHealth_FilteringCanFallBelowMinRuns(t *testing.T) {
	now := time.Now()
	recs := append(
		spawnLedgerRecords("real", "planner", sessions.StatusFailed, 4, now),
		spawnLedgerRecords("stray", "go-test-writer", sessions.StatusFailed, 10, now)...,
	)
	stageKnownAgentsFixture(t, recs, "planner")

	if res := checkSpawnHealth(); res.Name != "" {
		t.Fatalf("result = %+v, want zero CheckResult (below spawnHealthMinRuns after filtering)", res)
	}
}

// TestCheckFleetProgress_AllRowsFiltered: a window whose every row belongs to
// an unconfigured agent reads as no progress observed, with no divide-by-zero.
func TestCheckFleetProgress_AllRowsFiltered(t *testing.T) {
	now := time.Now()
	stageKnownAgentsFixture(t,
		spawnLedgerRecords("stray", "go-test-writer", sessions.StatusCompleted, 6, now), "planner")

	deps, _, _, _, mockIB := NewTestDeps(t)
	mockIB.ReadyResult = []backend.IssueData{{ID: "PUPPET-1"}}

	res := checkFleetProgress(deps)
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want fail (%+v)", res.Status, res)
	}
	if !strings.Contains(res.Detail, "no runs were even attempted") {
		t.Errorf("detail = %q, want the no-attempts branch", res.Detail)
	}
}
