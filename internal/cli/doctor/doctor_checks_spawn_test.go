package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// tally builds an outcomeTally the way the real one comes out of
// tallyOutcomes, so the pure tests exercise the same shape production does.
func tally(completed, failed, aborted int, classes map[string]int, latest *sessions.SessionRecord) outcomeTally {
	t := outcomeTally{
		Completed:  completed,
		Failed:     failed,
		Aborted:    aborted,
		ErrorClass: make(map[string]int),
		ClassLabel: make(map[string]string),
		LatestFail: latest,
	}
	for raw, n := range classes {
		key := normalizeErrorClass(raw)
		t.ErrorClass[key] += n
		t.ClassLabel[key] = raw
	}
	return t
}

func failRecord(id, agent string, at time.Time) *sessions.SessionRecord {
	return &sessions.SessionRecord{
		SessionID:  id,
		AgentName:  agent,
		StartedAt:  at,
		Status:     sessions.StatusFailed,
		ErrorClass: "spawn_failure",
	}
}

// TestEvaluateSpawnHealth_HealthyFleet is the ordinary day: doctor stays quiet
// about spawns and says how much work got done.
func TestEvaluateSpawnHealth_HealthyFleet(t *testing.T) {
	res := evaluateSpawnHealth(tally(28, 1, 0, map[string]int(nil), nil), "")
	if res.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%+v)", res.Status, res)
	}
	if !strings.Contains(res.Summary, "28 of 29") {
		t.Errorf("summary = %q, want both counts", res.Summary)
	}
}

// TestEvaluateSpawnHealth_TotalOutage is the reported defect: 103 failed
// spawns and not one success must fail the command and name the class.
func TestEvaluateSpawnHealth_TotalOutage(t *testing.T) {
	drift := `agent process failed to spawn: build command: agent tester profile: ` +
		`profile harness version drift: manifest pins "2.1.250 (Claude Code)", ` +
		`claude reports "2.1.251 (Claude Code)"`
	latest := failRecord("20260829-024235-planner--4e2b0570", "planner",
		time.Date(2026, 8, 29, 2, 42, 35, 0, time.UTC))

	res := evaluateSpawnHealth(
		tally(0, 103, 0, map[string]int{"spawn_failure": 103}, latest), drift,
	)

	if res.Status != StatusFail {
		t.Fatalf("status = %v, want fail (%+v)", res.Status, res)
	}
	if !strings.Contains(res.Detail, "spawn_failure") {
		t.Errorf("detail = %q, want it to name spawn_failure", res.Detail)
	}
	if !strings.Contains(res.Detail, `manifest pins "2.1.250 (Claude Code)"`) {
		t.Errorf("detail = %q, want the supervisor's own error quoted", res.Detail)
	}
	if !strings.Contains(res.Detail, latest.SessionID) {
		t.Errorf("detail = %q, want the newest failing session named", res.Detail)
	}
}

// TestEvaluateSpawnHealth_DegradedRate is the 19:00 row of the real outage:
// still completing a little, so a warning rather than a failure.
func TestEvaluateSpawnHealth_DegradedRate(t *testing.T) {
	res := evaluateSpawnHealth(
		tally(10, 50, 0, map[string]int{"spawn_failure": 50}, nil), "",
	)
	if res.Status != StatusWarn {
		t.Fatalf("status = %v, want warn (%+v)", res.Status, res)
	}
	if !strings.Contains(res.Summary, "17%") {
		t.Errorf("summary = %q, want the success rate", res.Summary)
	}
}

// TestEvaluateSpawnHealth_TooFewRuns keeps doctor from crying wolf on a fleet
// that has barely started.
func TestEvaluateSpawnHealth_TooFewRuns(t *testing.T) {
	res := evaluateSpawnHealth(
		tally(0, 2, 0, map[string]int{"spawn_failure": 2}, nil), "",
	)
	if res.Name != "" {
		t.Fatalf("result = %+v, want zero CheckResult (skipped)", res)
	}
}

// TestEvaluateFleetProgress_IdleFleet is the regression guard for "a quiet
// weekend is not an outage".
func TestEvaluateFleetProgress_IdleFleet(t *testing.T) {
	res := evaluateFleetProgress(tally(0, 0, 0, nil, nil), 0)
	if res.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%+v)", res.Status, res)
	}
}

// TestEvaluateFleetProgress_ReadyWorkNoAttempts covers the state spawn_health
// cannot see: nothing is even being attempted.
func TestEvaluateFleetProgress_ReadyWorkNoAttempts(t *testing.T) {
	res := evaluateFleetProgress(tally(0, 0, 0, nil, nil), 3)
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want fail (%+v)", res.Status, res)
	}
	if !strings.Contains(res.Detail, "no runs were even attempted") {
		t.Errorf("detail = %q, want the no-attempts diagnosis", res.Detail)
	}
}

func TestEvaluateFleetProgress_MakingProgress(t *testing.T) {
	res := evaluateFleetProgress(tally(6, 2, 0, nil, nil), 4)
	if res.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%+v)", res.Status, res)
	}
}

// TestDominantErrorClass_Ties pins the ordering, because doctor output is read
// by humans and compared in tests.
func TestDominantErrorClass_Ties(t *testing.T) {
	tl := tally(0, 4, 0, map[string]int{"spawn_failure": 2, "auth_failure": 2}, nil)
	class, count := tl.dominantErrorClass()
	if class != "auth_failure" || count != 2 {
		t.Fatalf("dominant = (%q, %d), want (auth_failure, 2)", class, count)
	}
}

// TestNormalizeErrorClass_FoldsSpellings covers the two disjoint producers of
// error_class that both appear in a live ledger.
func TestNormalizeErrorClass_FoldsSpellings(t *testing.T) {
	if normalizeErrorClass("spawn_failure") != normalizeErrorClass("SpawnFailure") {
		t.Error("snake_case and CamelCase spellings must land in one bucket")
	}
	if got := normalizeErrorClass("  "); got != "unknown" {
		t.Errorf("empty class = %q, want unknown", got)
	}
}

// TestTallyOutcomes_ExcludesRunningAndOldRecords is the counting contract:
// running records are not terminal, and the window is honored.
func TestTallyOutcomes_ExcludesRunningAndOldRecords(t *testing.T) {
	now := time.Now()
	since := now.Add(-spawnHealthWindow)
	recs := []sessions.SessionRecord{
		{SessionID: "a", Status: sessions.StatusRunning, StartedAt: now.Add(-time.Minute)},
		{SessionID: "b", Status: sessions.StatusCompleted, StartedAt: now.Add(-time.Minute)},
		{SessionID: "c", Status: sessions.StatusFailed, StartedAt: now.Add(-2 * time.Minute), ErrorClass: "spawn_failure"},
		{SessionID: "d", Status: sessions.StatusAborted, StartedAt: now.Add(-3 * time.Minute)},
		{SessionID: "e", Status: sessions.StatusFailed, StartedAt: now.Add(-3 * time.Hour)},
	}
	tl := tallyOutcomes(recs, since)
	if tl.Completed != 1 || tl.Failed != 1 || tl.Aborted != 1 {
		t.Fatalf("tally = %+v, want 1/1/1", tl)
	}
	if tl.terminal() != 3 {
		t.Errorf("terminal = %d, want 3", tl.terminal())
	}
	if tl.LatestFail == nil || tl.LatestFail.SessionID != "c" {
		t.Errorf("latest fail = %+v, want session c", tl.LatestFail)
	}
	if tl.ErrorClass["unknown"] != 1 {
		t.Errorf("aborted run with no class should bucket as unknown: %+v", tl.ErrorClass)
	}
}

func TestCondenseLastError_TruncatesOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("é", spawnLastErrorRunes+50)
	got := condenseLastError(long)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("want an ellipsis marker, got %q", got[len(got)-8:])
	}
	if n := len([]rune(got)); n != spawnLastErrorRunes+1 {
		t.Errorf("rune count = %d, want %d", n, spawnLastErrorRunes+1)
	}
	if got := condenseLastError("a\nb"); got != "a / b" {
		t.Errorf("newline collapse = %q, want %q", got, "a / b")
	}
}

// --- reconcileLiveness -------------------------------------------------------

func TestReconcileLiveness_DemotesPassingLiveness(t *testing.T) {
	in := []CheckResult{
		{Name: "daemon_stuck", Status: StatusPass, Summary: "daemon supervisor liveness OK"},
		{Name: "spawn_health", Status: StatusFail, Summary: "no agent run has succeeded"},
	}
	out := reconcileLiveness(in)

	if out[0].Status != StatusWarn {
		t.Fatalf("daemon_stuck status = %v, want warn", out[0].Status)
	}
	if strings.Contains(out[0].Summary, "liveness OK") {
		t.Errorf("summary %q must stop claiming liveness", out[0].Summary)
	}
	if !strings.Contains(out[0].Detail, "spawn_health") {
		t.Errorf("detail %q should name the failing check", out[0].Detail)
	}
	if !strings.Contains(out[0].Detail, "previously: daemon supervisor liveness OK") {
		t.Errorf("detail %q should preserve the original summary", out[0].Detail)
	}

	sum := tallyResults(out)
	if sum.Pass != 0 || sum.Warn != 1 || sum.Fail != 1 {
		t.Errorf("tally = %+v, want 0 pass / 1 warn / 1 fail", sum)
	}
}

func TestReconcileLiveness_LeavesFailingLivenessAlone(t *testing.T) {
	in := []CheckResult{
		{Name: "daemon_stuck", Status: StatusFail, Summary: "daemon supervisor appears stuck"},
		{Name: "fleet_progress", Status: StatusFail, Summary: "ready work is waiting"},
	}
	out := reconcileLiveness(in)
	if out[0].Status != StatusFail || out[0].Summary != "daemon supervisor appears stuck" {
		t.Fatalf("a check with its own specific verdict must be untouched: %+v", out[0])
	}
}

func TestReconcileLiveness_NoOpWhenSpawnHealthy(t *testing.T) {
	in := []CheckResult{
		{Name: "daemon_stuck", Status: StatusPass, Summary: "daemon supervisor liveness OK"},
		{Name: "spawn_health", Status: StatusPass, Summary: "28 of 29 agent run(s) succeeded"},
	}
	out := reconcileLiveness(in)
	if out[0].Status != StatusPass || out[0].Detail != "" {
		t.Fatalf("healthy fleet must leave liveness alone: %+v", out[0])
	}
}

// TestReconcileLiveness_SkippedDaemonStuck guards against inventing a row for
// a check that deliberately said nothing.
func TestReconcileLiveness_SkippedDaemonStuck(t *testing.T) {
	in := []CheckResult{
		{Name: "spawn_health", Status: StatusFail, Summary: "no agent run has succeeded"},
	}
	out := reconcileLiveness(in)
	if len(out) != 1 || out[0].Name != "spawn_health" {
		t.Fatalf("results = %+v, want the input unchanged", out)
	}
}

// --- ledger-backed integration ----------------------------------------------

// stageLedger writes a sessions/index.jsonl containing a running record AND a
// terminal record for each session, which is how the real supervisor writes
// it — so the dedup path is exercised rather than bypassed.
func stageLedger(t *testing.T, runtimeDir string, recs []sessions.SessionRecord) {
	t.Helper()
	dir := filepath.Join(runtimeDir, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	var b strings.Builder
	for _, rec := range recs {
		running := rec
		running.Status = sessions.StatusRunning
		running.ErrorClass = ""
		for _, r := range []sessions.SessionRecord{running, rec} {
			line, err := json.Marshal(r)
			if err != nil {
				t.Fatalf("marshal record: %v", err)
			}
			b.Write(line)
			b.WriteByte('\n')
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "index.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write index.jsonl: %v", err)
	}
}

func stageMetadata(t *testing.T, runtimeDir, sessionID, lastErr string) {
	t.Helper()
	dir := filepath.Join(runtimeDir, "sessions", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	meta := sessions.SessionMetadata{
		SessionRecord: sessions.SessionRecord{SessionID: sessionID, Status: sessions.StatusFailed},
		LastError:     lastErr,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0o644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
}

// TestCheckSpawnHealth_ReadsLedger drives the whole impure path: paired
// running/terminal records, dedup, and the one metadata read that recovers
// the supervisor's own words.
func TestCheckSpawnHealth_ReadsLedger(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	now := time.Now()
	drift := `agent process failed to spawn: agent tester profile: profile harness ` +
		`version drift: manifest pins "2.1.250 (Claude Code)", claude reports "2.1.251 (Claude Code)"`

	recs := make([]sessions.SessionRecord, 0, 8)
	for i := 0; i < 8; i++ {
		recs = append(recs, sessions.SessionRecord{
			SessionID:  "sess-" + string(rune('a'+i)),
			AgentName:  "planner",
			StartedAt:  now.Add(-time.Duration(30-i) * time.Minute),
			Status:     sessions.StatusFailed,
			ErrorClass: "spawn_failure",
		})
	}
	newest := recs[len(recs)-1]
	stageLedger(t, runtimeDir, recs)
	stageMetadata(t, runtimeDir, newest.SessionID, drift)

	res := checkSpawnHealth()
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want fail (%+v)", res.Status, res)
	}
	if !strings.Contains(res.Detail, "spawn_failure") {
		t.Errorf("detail = %q, want spawn_failure named", res.Detail)
	}
	if !strings.Contains(res.Detail, `manifest pins "2.1.250 (Claude Code)"`) {
		t.Errorf("detail = %q, want the drift message quoted", res.Detail)
	}
	// The dedup contract: 8 sessions written twice each must count as 8.
	if !strings.Contains(res.Summary, "8 failed") {
		t.Errorf("summary = %q, want 8 failed (raw lines must not be counted)", res.Summary)
	}
}

// TestCheckSpawnHealth_MissingLedger is the fresh-workspace case: no output,
// no failure.
func TestCheckSpawnHealth_MissingLedger(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	if res := checkSpawnHealth(); res.Name != "" {
		t.Fatalf("result = %+v, want zero CheckResult (skipped)", res)
	}
}

// TestCheckFleetProgress_NoBackendSkips keeps the check quiet when there is
// nothing to ask about ready work.
func TestCheckFleetProgress_NoBackendSkips(t *testing.T) {
	if res := checkFleetProgress(nil); res.Name != "" {
		t.Fatalf("result = %+v, want zero CheckResult (skipped)", res)
	}
}

func TestCheckFleetProgress_ReadyErrorSkips(t *testing.T) {
	deps, _, _, _, mockIB := NewTestDeps(t)
	mockIB.ReadyErr = os.ErrDeadlineExceeded
	if res := checkFleetProgress(deps); res.Name != "" {
		t.Fatalf("result = %+v, want zero CheckResult (checkIssueBackend owns reachability)", res)
	}
}

// TestCheckFleetProgress_ReadyWorkNothingCompleted is the end-to-end
// dead-man's switch against a staged ledger.
func TestCheckFleetProgress_ReadyWorkNothingCompleted(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	now := time.Now()
	stageLedger(t, runtimeDir, []sessions.SessionRecord{{
		SessionID:  "sess-x",
		AgentName:  "worker",
		StartedAt:  now.Add(-5 * time.Minute),
		Status:     sessions.StatusFailed,
		ErrorClass: "spawn_failure",
	}})

	deps, _, _, _, mockIB := NewTestDeps(t)
	mockIB.ReadyResult = []backend.IssueData{{ID: "PUPPET-1"}}

	res := checkFleetProgress(deps)
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want fail (%+v)", res.Status, res)
	}
	if !strings.Contains(res.Detail, "spawn_failure") {
		t.Errorf("detail = %q, want the dominant class named", res.Detail)
	}
}

// TestCheckFleetProgress_IdleFleetPasses is the same path with no ready work.
func TestCheckFleetProgress_IdleFleetPasses(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	deps, _, _, _, mockIB := NewTestDeps(t)
	mockIB.ReadyResult = nil

	res := checkFleetProgress(deps)
	if res.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%+v)", res.Status, res)
	}
}
