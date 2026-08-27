package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newPersistSupervisor builds a quarantine supervisor whose ledger is backed
// by daemon-quarantine.json under projectDir. Passing the SAME projectDir to a
// second call models a daemon restart: a brand-new Supervisor, same state dir.
func newPersistSupervisor(projectDir string, mock *clitest.MockIssueBackend) *Supervisor {
	s := newQuarantineSupervisor(mock)
	s.ProjectDir = projectDir
	return s
}

// quarantineFile is where the ledger is expected to live for projectDir —
// derived the same way production does, so the test pins the location rather
// than restating it.
func quarantineFile(projectDir string) string {
	return filepath.Join(filepath.Dir(config.ResolveDaemonStatePath(projectDir)), quarantineStateFileName)
}

// writeQuarantineFile writes a raw ledger file for projectDir, creating the
// state directory. Used to seed loads that production code would not produce.
func writeQuarantineFile(t *testing.T, projectDir string, body []byte) string {
	t.Helper()
	path := quarantineFile(projectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write ledger: %v", err)
	}
	return path
}

func readQuarantineFile(t *testing.T, projectDir string) quarantineStateFile {
	t.Helper()
	data, err := os.ReadFile(quarantineFile(projectDir))
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var state quarantineStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshal ledger: %v", err)
	}
	return state
}

// ---------------------------------------------------------------------------
// the regression tests for PUPPET-108
// ---------------------------------------------------------------------------

// TestQuarantinePersist_SurvivesSupervisorRestart is THE regression test: on
// the pre-fix code the second kill landed in a fresh in-memory map and logged
// count=1 again, which is exactly what made threshold=3 unreachable on a host
// where the daemon crash-loops.
func TestQuarantinePersist_SurvivesSupervisorRestart(t *testing.T) {
	dir := t.TempDir()
	status, design := "open", "d1"

	sA := newPersistSupervisor(dir, openIssueMock(&status, &design))
	killNTimes(sA, newKilledAgent(t, "falcon", "T-1", timeoutOutcome()), 1)
	if got := recordCount(sA, "T-1"); got != 1 {
		t.Fatalf("setup: count in daemon A = %d, want 1", got)
	}

	// Daemon restart: a brand-new Supervisor over the same state directory.
	sB := newPersistSupervisor(dir, openIssueMock(&status, &design))
	killNTimes(sB, newKilledAgent(t, "falcon", "T-1", timeoutOutcome()), 1)

	if got := recordCount(sB, "T-1"); got != 2 {
		t.Fatalf("count after restart = %d, want 2 (the counter must survive the restart)", got)
	}
}

func TestQuarantinePersist_ReachesThresholdAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	status, design := "open", "d1"

	var mocks []*clitest.MockIssueBackend
	for i := 0; i < defaultQuarantineThreshold; i++ {
		mock := openIssueMock(&status, &design)
		mocks = append(mocks, mock)
		s := newPersistSupervisor(dir, mock)
		ap := newKilledAgent(t, "falcon", "T-2", timeoutOutcome())
		killNTimes(s, ap, 1)
		s.sweepQuarantineDue(ap)
	}

	// Exactly one blocked-write, on the daemon that saw the threshold reached.
	total := 0
	for i, mock := range mocks {
		n := mock.CallCount("Update")
		total += n
		if i < len(mocks)-1 && n != 0 {
			t.Errorf("daemon %d wrote %d times below threshold, want 0", i+1, n)
		}
	}
	if total != 1 {
		t.Fatalf("Update called %d times across %d restarts, want exactly 1", total, len(mocks))
	}

	calls := updateCalls(mocks[len(mocks)-1])
	if len(calls) != 1 {
		t.Fatalf("final daemon Update calls = %d, want 1", len(calls))
	}
	params := updateParamsOf(t, calls[0])
	if params.Status == nil || *params.Status != "blocked" {
		t.Errorf("Update status = %v, want blocked", params.Status)
	}
}

func TestQuarantinePersist_RoundTripsAllFields(t *testing.T) {
	dir := t.TempDir()
	status, design := "open", "d1"

	sA := newPersistSupervisor(dir, openIssueMock(&status, &design))
	ap := newKilledAgent(t, "falcon", "T-3", timeoutOutcome())
	killNTimes(sA, ap, 2)
	sA.qrec().markWriteFailed("T-3")
	sA.qrec().latch("T-3", true)

	before := record(sA, "T-3")
	if before == nil {
		t.Fatal("setup: no record in daemon A")
	}

	after := record(newPersistSupervisor(dir, nil), "T-3")
	if after == nil {
		t.Fatal("record did not survive the restart")
	}

	if after.Count != before.Count || after.QuarantineKills != before.QuarantineKills {
		t.Errorf("counts = {Count:%d QuarantineKills:%d}, want {%d %d}",
			after.Count, after.QuarantineKills, before.Count, before.QuarantineKills)
	}
	if after.DaemonWrote != before.DaemonWrote || after.WriteFailed != before.WriteFailed {
		t.Errorf("flags = {DaemonWrote:%v WriteFailed:%v}, want {%v %v}",
			after.DaemonWrote, after.WriteFailed, before.DaemonWrote, before.WriteFailed)
	}
	if !after.QuarantinedAt.Equal(before.QuarantinedAt) {
		t.Errorf("QuarantinedAt = %v, want %v", after.QuarantinedAt, before.QuarantinedAt)
	}
	if after.BaselineKnown != before.BaselineKnown ||
		after.BaselineDesignHash != before.BaselineDesignHash ||
		after.BaselineNotesHash != before.BaselineNotesHash {
		t.Errorf("baseline = {%v %d %d}, want {%v %d %d}",
			after.BaselineKnown, after.BaselineDesignHash, after.BaselineNotesHash,
			before.BaselineKnown, before.BaselineDesignHash, before.BaselineNotesHash)
	}
	if after.LastKillReason != before.LastKillReason {
		t.Errorf("LastKillReason = %q, want %q", after.LastKillReason, before.LastKillReason)
	}
	if len(after.Kills) != len(before.Kills) {
		t.Fatalf("kill timeline length = %d, want %d", len(after.Kills), len(before.Kills))
	}
	for i := range after.Kills {
		got, want := after.Kills[i], before.Kills[i]
		if got.Agent != want.Agent || got.ErrClass != want.ErrClass || got.ExitCode != want.ExitCode ||
			got.StopReason != want.StopReason || got.FleetSessionID != want.FleetSessionID ||
			got.ClaudeSessionID != want.ClaudeSessionID || got.RunID != want.RunID ||
			!got.At.Equal(want.At) {
			t.Errorf("kill[%d] = %+v, want %+v", i, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// load-time robustness: a bad ledger must never stop the daemon
// ---------------------------------------------------------------------------

func TestQuarantinePersist_MissingFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	s := newPersistSupervisor(dir, nil)

	if n := len(s.qrec().rec); n != 0 {
		t.Fatalf("ledger has %d records with no file on disk, want 0", n)
	}
	// Still fully usable: the first kill creates and persists a record.
	killNTimes(s, newKilledAgent(t, "falcon", "T-4", timeoutOutcome()), 1)
	if got := recordCount(s, "T-4"); got != 1 {
		t.Fatalf("count = %d after first kill, want 1", got)
	}
	if _, err := os.Stat(quarantineFile(dir)); err != nil {
		t.Fatalf("ledger not written to %s: %v", quarantineFile(dir), err)
	}
}

func TestQuarantinePersist_CorruptFileStartsEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"truncated", `{`},
		{"future version", `{"version":99,"records":{"T-5":{"count":7,"last_updated":"2999-01-01T00:00:00Z"}}}`},
		{"wrong shape", `{"version":1,"records":[]}`},
		{"empty file", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeQuarantineFile(t, dir, []byte(tc.body))

			s := newPersistSupervisor(dir, nil)
			if n := len(s.qrec().rec); n != 0 {
				t.Fatalf("ledger has %d records from a bad file, want 0", n)
			}
			// The daemon stays usable and overwrites the bad file.
			killNTimes(s, newKilledAgent(t, "falcon", "T-5", timeoutOutcome()), 1)
			if got := recordCount(s, "T-5"); got != 1 {
				t.Fatalf("count = %d after a bad load, want 1", got)
			}
		})
	}
}

func TestQuarantinePersist_PrunesStaleRecords(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	state := quarantineStateFile{
		Version: quarantineStateVersion,
		Records: map[string]*taskFailureRecord{
			"T-fresh": {Count: 2, LastUpdated: now.Add(-quarantineRecordTTL + time.Hour)},
			"T-stale": {Count: 2, LastUpdated: now.Add(-quarantineRecordTTL - time.Hour)},
			"T-zero":  {Count: 2},
		},
	}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeQuarantineFile(t, dir, body)

	s := newPersistSupervisor(dir, nil)
	if rec := record(s, "T-fresh"); rec == nil || rec.Count != 2 {
		t.Errorf("fresh record = %+v, want Count 2 (within TTL)", rec)
	}
	if rec := record(s, "T-stale"); rec != nil {
		t.Errorf("stale record survived load: %+v", rec)
	}
	if rec := record(s, "T-zero"); rec != nil {
		t.Errorf("record with zero LastUpdated survived load: %+v", rec)
	}
}

// TestQuarantinePersist_ClearsInFlightOnLoad guards the flag that would
// otherwise be a permanent trap: a daemon killed between takeDue and the
// blocked-write persists inFlight=true, and a record stuck inFlight is never
// returned by takeDue again.
func TestQuarantinePersist_ClearsInFlightOnLoad(t *testing.T) {
	dir := t.TempDir()
	status, design := "open", "d1"

	sA := newPersistSupervisor(dir, openIssueMock(&status, &design))
	killNTimes(sA, newKilledAgent(t, "falcon", "T-6", timeoutOutcome()), defaultQuarantineThreshold)
	if due := sA.qrec().takeDue(defaultQuarantineThreshold); len(due) != 1 {
		t.Fatalf("setup: takeDue returned %d tasks, want 1", len(due))
	}
	// Daemon A dies here, mid-write, with inFlight latched on.
	if rec := record(sA, "T-6"); rec == nil || !rec.inFlight {
		t.Fatalf("setup: record = %+v, want inFlight true", rec)
	}

	sB := newPersistSupervisor(dir, openIssueMock(&status, &design))
	rec := record(sB, "T-6")
	if rec == nil {
		t.Fatal("record did not survive the restart")
	}
	if rec.inFlight {
		t.Error("inFlight must be cleared on load, otherwise the record can never be swept again")
	}
	if due := sB.qrec().takeDue(defaultQuarantineThreshold); len(due) != 1 {
		t.Fatalf("takeDue after restart returned %d tasks, want 1", len(due))
	}
}

// ---------------------------------------------------------------------------
// persistence is best-effort: it never degrades the in-memory ledger
// ---------------------------------------------------------------------------

func TestQuarantinePersist_NoProjectDirIsNoOp(t *testing.T) {
	s := newQuarantineSupervisor(nil) // ProjectDir deliberately empty
	if got := s.quarantineStatePath(); got != "" {
		t.Fatalf("quarantineStatePath() = %q with no ProjectDir, want empty (persistence disabled)", got)
	}

	killNTimes(s, newKilledAgent(t, "falcon", "T-7", timeoutOutcome()), 2)
	if got := recordCount(s, "T-7"); got != 2 {
		t.Fatalf("count = %d, want 2 (the in-memory ledger must work unchanged)", got)
	}
}

func TestQuarantinePersist_UnwritablePathDoesNotBreakLedger(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	s := newQuarantineSupervisor(nil)
	// Seam: set before the first qrec, or it is a silent no-op.
	s.quarantineStatePathCache = filepath.Join(blocker, "daemon-quarantine.json")

	killNTimes(s, newKilledAgent(t, "falcon", "T-8", timeoutOutcome()), 2)
	if got := recordCount(s, "T-8"); got != 2 {
		t.Fatalf("count = %d against an unwritable path, want 2", got)
	}
}

func TestQuarantinePersist_WritesVersionedEnvelopeNextToAgentState(t *testing.T) {
	dir := t.TempDir()
	s := newPersistSupervisor(dir, nil)
	killNTimes(s, newKilledAgent(t, "falcon", "T-9", timeoutOutcome()), 1)

	stateDir := filepath.Dir(config.ResolveDaemonStatePath(dir))
	want := filepath.Join(stateDir, "daemon-quarantine.json")
	if got := s.quarantineStatePath(); got != want {
		t.Errorf("quarantineStatePath() = %q, want %q (next to daemon-agents.json)", got, want)
	}

	state := readQuarantineFile(t, dir)
	if state.Version != quarantineStateVersion {
		t.Errorf("on-disk version = %d, want %d", state.Version, quarantineStateVersion)
	}
	if rec := state.Records["T-9"]; rec == nil || rec.Count != 1 {
		t.Errorf("on-disk record = %+v, want Count 1", rec)
	}
}

func TestQuarantinePersist_EvictRemovesRecordFromDisk(t *testing.T) {
	dir := t.TempDir()
	s := newPersistSupervisor(dir, nil)
	killNTimes(s, newKilledAgent(t, "falcon", "T-10", timeoutOutcome()), 1)
	s.qrec().evict("T-10")

	if state := readQuarantineFile(t, dir); len(state.Records) != 0 {
		t.Fatalf("on-disk records = %+v after evict, want none", state.Records)
	}
	if rec := record(newPersistSupervisor(dir, nil), "T-10"); rec != nil {
		t.Fatalf("evicted record came back after a restart: %+v", rec)
	}
}

// ---------------------------------------------------------------------------
// eligibility: a daemon-initiated stop is not task evidence
// ---------------------------------------------------------------------------

// TestRecordTaskExit_SkipsDrainStopReasons is the direct regression test for
// the PUPPET-105 lines: a config-reconciler drain escalating to SIGTERM
// classifies as Timeout, which the outcome filter accepts — so config churn
// manufactured quarantine credit against tasks that were never at fault.
func TestRecordTaskExit_SkipsDrainStopReasons(t *testing.T) {
	for _, reason := range []StopReason{
		StopReasonConfigRemoved,
		StopReasonShutdown,
		StopReasonManualStop,
		StopReasonYielded,
		StopReasonEphemeralDone,
	} {
		t.Run(string(reason), func(t *testing.T) {
			s := newQuarantineSupervisor(nil)
			ap := newKilledAgent(t, "falcon", "T-drain", timeoutOutcome())
			ap.StopReason = reason

			s.recordTaskExitForQuarantine(ap, 137)

			if rec := record(s, "T-drain"); rec != nil {
				t.Fatalf("%s kill created a record %+v, want none (daemon decision, not task evidence)", reason, rec)
			}
		})
	}
}

// TestRecordTaskExit_DrainDoesNotResetExistingCount pins the gate as a SKIP,
// not an evict: the drained run carries no evidence in either direction, so an
// accumulated count must neither grow nor be erased.
func TestRecordTaskExit_DrainDoesNotResetExistingCount(t *testing.T) {
	status, design := "open", "d1"
	s := newQuarantineSupervisor(openIssueMock(&status, &design))

	stalled := newKilledAgent(t, "falcon", "T-mixed", timeoutOutcome())
	stalled.StopReason = StopReasonWatchdog
	s.recordTaskExitForQuarantine(stalled, 137)
	if got := recordCount(s, "T-mixed"); got != 1 {
		t.Fatalf("setup: count after watchdog kill = %d, want 1", got)
	}

	drained := newKilledAgent(t, "falcon", "T-mixed", timeoutOutcome())
	drained.StopReason = StopReasonConfigRemoved
	s.recordTaskExitForQuarantine(drained, 137)

	if got := recordCount(s, "T-mixed"); got != 1 {
		t.Fatalf("count after an interleaved drain = %d, want still 1 (skip, not increment, not reset)", got)
	}
}

// TestRecordTaskExit_NonLifecycleStopsStillEligible guards against
// over-filtering: the reasons the mechanism exists for must keep counting.
//
// PUPPET-198 narrowed this list on purpose. run_duration_exceeded, fast_fail
// and max_retries used to be here and now count only conditionally or not at
// all — the cap fires whether or not the run was making progress, and the two
// agent budgets already escalate agent-side, so charging the task double-counts
// one failure against two breakers. TestQuarantineCountable_* owns those cases
// now; what remains here is the set that must never stop counting.
func TestRecordTaskExit_NonLifecycleStopsStillEligible(t *testing.T) {
	for _, reason := range []StopReason{
		"", // bare crash / ownership kill
		StopReasonWatchdog,
		StopReasonFatalError,
	} {
		name := string(reason)
		if name == "" {
			name = "bare_crash"
		}
		t.Run(name, func(t *testing.T) {
			s := newQuarantineSupervisor(nil)
			ap := newKilledAgent(t, "falcon", "T-eligible", timeoutOutcome())
			ap.StopReason = reason

			s.recordTaskExitForQuarantine(ap, 137)

			if got := recordCount(s, "T-eligible"); got != 1 {
				t.Fatalf("count for stop reason %q = %d, want 1 (must stay eligible)", reason, got)
			}
		})
	}
}

// TestRecordTaskExit_DrainOfCommittingAgentStillEvicts pins the ORDER of the
// two checks: the clean/commit-progress eviction runs first, so an agent that
// did commit still clears its ledger even when a drain killed it.
func TestRecordTaskExit_DrainOfCommittingAgentStillEvicts(t *testing.T) {
	status, design := "open", "d1"
	s := newQuarantineSupervisor(openIssueMock(&status, &design))

	stalled := newKilledAgent(t, "falcon", "T-committed", timeoutOutcome())
	stalled.StopReason = StopReasonWatchdog
	s.recordTaskExitForQuarantine(stalled, 137)
	if got := recordCount(s, "T-committed"); got != 1 {
		t.Fatalf("setup: count = %d, want 1", got)
	}

	// An agent that committed, then got drained by a config change.
	worker := newKilledAgent(t, "falcon", "T-committed", timeoutOutcome())
	worker.BeforeRef = initGitRepo(t, worker.WorktreePath)
	gitCommit(t, worker.WorktreePath, "real work")
	worker.StopReason = StopReasonConfigRemoved

	s.recordTaskExitForQuarantine(worker, 137)

	if rec := record(s, "T-committed"); rec != nil {
		t.Fatalf("record = %+v after a drain of an agent that committed, want evicted", rec)
	}
}

func TestStopReasonQuarantineEligible(t *testing.T) {
	notEligible := map[StopReason]bool{
		StopReasonConfigRemoved: true,
		StopReasonShutdown:      true,
		StopReasonManualStop:    true,
		StopReasonYielded:       true,
		StopReasonEphemeralDone: true,
	}
	all := []StopReason{
		"", StopReasonNoWork, StopReasonRateLimited, StopReasonMaxRetries,
		StopReasonFatalError, StopReasonManualStop, StopReasonConfigRemoved,
		StopReasonShutdown, StopReasonYielded, StopReasonWatchdog,
		StopReasonBackendUnavailable, StopReasonEphemeralDone,
		StopReasonMaxRetriesBlocked, StopReasonFastFail, StopReasonRunDurationExceeded,
	}
	for _, r := range all {
		want := !notEligible[r]
		if got := stopReasonQuarantineEligible(r); got != want {
			t.Errorf("stopReasonQuarantineEligible(%q) = %v, want %v", r, got, want)
		}
	}
}
