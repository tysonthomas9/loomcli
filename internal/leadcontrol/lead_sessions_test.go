package leadcontrol

import (
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const (
	testWorkDir  = "/repo"
	testHarnessA = "aaaaaaaa-1111-2222-3333-444444444444"
	testHarnessB = "bbbbbbbb-1111-2222-3333-444444444444"
)

var testNow = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

// finishedRecord is a normal previous lead run: completed, with a handle.
func finishedRecord(sessionID, harnessID string, ageMinutes int) LeadSessionRecord {
	return LeadSessionRecord{
		SessionID:        sessionID,
		AgentID:          "nova",
		Status:           domain.AgentSessionCompleted,
		StartedAt:        testNow.Add(-time.Duration(ageMinutes) * time.Minute),
		Finished:         true,
		WorkDir:          testWorkDir,
		Provider:         "claude",
		HarnessSessionID: harnessID,
	}
}

func continueReq() ResumeRequest {
	return ResumeRequest{Continue: true, Backend: "claude", WorkDir: testWorkDir, AgentID: "nova", Now: testNow}
}

func refReq(ref string) ResumeRequest {
	return ResumeRequest{Ref: ref, Backend: "claude", WorkDir: testWorkDir, AgentID: "nova", Now: testNow}
}

func TestResolveResumeTargetNotRequested(t *testing.T) {
	target, err := ResolveResumeTarget(nil, ResumeRequest{Backend: "claude", WorkDir: testWorkDir})
	if err != nil || target != nil {
		t.Fatalf("target=%v err=%v, want (nil, nil) when neither flag is set", target, err)
	}
}

// Latest wins, and the input order must not decide it -- the store's ordering
// is not part of its contract.
func TestResolveResumeTargetLatestWins(t *testing.T) {
	records := []LeadSessionRecord{
		finishedRecord("lead-old", testHarnessA, 120),
		finishedRecord("lead-new", testHarnessB, 5),
		finishedRecord("lead-mid", "cccccccc-1111-2222-3333-444444444444", 60),
	}
	target, err := ResolveResumeTarget(records, continueReq())
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if target.Record.SessionID != "lead-new" || target.HarnessSessionID != testHarnessB {
		t.Fatalf("target = %+v, want lead-new/%s", target, testHarnessB)
	}
	if target.MatchedBy != ResumeMatchLatest {
		t.Fatalf("MatchedBy = %q, want %q", target.MatchedBy, ResumeMatchLatest)
	}
}

// A bare --resume carries the sentinel and must behave exactly like --continue.
func TestResolveResumeTargetBareResumeIsContinue(t *testing.T) {
	records := []LeadSessionRecord{finishedRecord("lead-old", testHarnessA, 120), finishedRecord("lead-new", testHarnessB, 5)}
	target, err := ResolveResumeTarget(records, refReq(ResumeLatestSentinel))
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if target.Record.SessionID != "lead-new" {
		t.Fatalf("bare --resume picked %q, want the latest", target.Record.SessionID)
	}
}

// Loom ids are `lead-` prefixed and harness ids are bare uuids, so the two
// spaces cannot collide -- but loom's own id is matched FIRST regardless, so
// the answer never depends on that invariant holding.
func TestResolveResumeTargetMatchesBothIDKinds(t *testing.T) {
	records := []LeadSessionRecord{finishedRecord("lead-old", testHarnessA, 120), finishedRecord("lead-new", testHarnessB, 5)}

	byLoom, err := ResolveResumeTarget(records, refReq("lead-old"))
	if err != nil {
		t.Fatalf("by loom id: %v", err)
	}
	if byLoom.Record.SessionID != "lead-old" || byLoom.MatchedBy != ResumeMatchLoomID {
		t.Fatalf("by loom id = %+v, want lead-old matched as %q", byLoom, ResumeMatchLoomID)
	}

	byHarness, err := ResolveResumeTarget(records, refReq(testHarnessA))
	if err != nil {
		t.Fatalf("by harness id: %v", err)
	}
	if byHarness.Record.SessionID != "lead-old" || byHarness.MatchedBy != ResumeMatchHarness {
		t.Fatalf("by harness id = %+v, want lead-old matched as %q", byHarness, ResumeMatchHarness)
	}
}

// A row whose loom id happens to equal another row's harness id resolves to
// the loom match: the pass order is the tiebreak, not luck.
func TestResolveResumeTargetPrefersLoomIDOverHarnessID(t *testing.T) {
	shared := "shared-id"
	loomMatch := finishedRecord(shared, testHarnessA, 120)
	harnessMatch := finishedRecord("lead-new", shared, 5)
	target, err := ResolveResumeTarget([]LeadSessionRecord{harnessMatch, loomMatch}, refReq(shared))
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if target.Record.SessionID != shared || target.MatchedBy != ResumeMatchLoomID {
		t.Fatalf("target = %+v, want the loom-id match to win", target)
	}
}

// Rows that never recorded a handle are skipped, and the count is reported so
// the operator learns the newest session was not the one reopened.
func TestResolveResumeTargetSkipsRowsWithNoHandle(t *testing.T) {
	noHandle := finishedRecord("lead-newest", "", 1)
	records := []LeadSessionRecord{noHandle, finishedRecord("lead-older", testHarnessA, 30)}
	target, err := ResolveResumeTarget(records, continueReq())
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if target.Record.SessionID != "lead-older" {
		t.Fatalf("target = %q, want the newest row WITH a handle", target.Record.SessionID)
	}
	if target.SkippedNoHandle != 1 {
		t.Fatalf("SkippedNoHandle = %d, want 1", target.SkippedNoHandle)
	}
}

// This launch's own row must never be a candidate: resuming it would point the
// new runtime at the conversation the running one is still writing.
func TestResolveResumeTargetExcludesCurrentSession(t *testing.T) {
	records := []LeadSessionRecord{finishedRecord("lead-current", testHarnessB, 0), finishedRecord("lead-prev", testHarnessA, 30)}
	req := continueReq()
	req.CurrentSessionID = "lead-current"
	target, err := ResolveResumeTarget(records, req)
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if target.Record.SessionID != "lead-prev" {
		t.Fatalf("target = %q, want the current session excluded", target.Record.SessionID)
	}
}

// The refusal must name BOTH paths -- "wrong directory" without saying which
// is a message the operator cannot act on.
func TestResolveResumeTargetWorkDirMismatchRefuses(t *testing.T) {
	rec := finishedRecord("lead-old", testHarnessA, 30)
	rec.WorkDir = "/elsewhere"
	_, err := ResolveResumeTarget([]LeadSessionRecord{rec}, continueReq())
	if err == nil {
		t.Fatal("a workdir mismatch must refuse")
	}
	if !strings.Contains(err.Error(), "/elsewhere") || !strings.Contains(err.Error(), testWorkDir) {
		t.Fatalf("err = %v, want both paths named", err)
	}
}

// A row with no recorded workdir predates the metadata and cannot be checked;
// refusing it would make old sessions permanently unresumable.
func TestResolveResumeTargetUnknownWorkDirIsAllowed(t *testing.T) {
	rec := finishedRecord("lead-old", testHarnessA, 30)
	rec.WorkDir = ""
	if _, err := ResolveResumeTarget([]LeadSessionRecord{rec}, continueReq()); err != nil {
		t.Fatalf("a row with no recorded workdir should resolve: %v", err)
	}
}

func TestResolveResumeTargetNoResumableSession(t *testing.T) {
	_, err := ResolveResumeTarget(nil, continueReq())
	if err == nil {
		t.Fatal("--continue with no sessions must refuse")
	}
	if !strings.Contains(err.Error(), `agent "nova"`) || !strings.Contains(err.Error(), testWorkDir) {
		t.Fatalf("err = %v, want the agent and workdir named", err)
	}
	if !strings.Contains(err.Error(), "loom lead") {
		t.Fatalf("err = %v, want it to say how to start one", err)
	}
}

func TestResolveResumeTargetNoResumableSessionReportsSkips(t *testing.T) {
	_, err := ResolveResumeTarget([]LeadSessionRecord{finishedRecord("lead-a", "", 5)}, continueReq())
	if err == nil || !strings.Contains(err.Error(), "skipped 1") {
		t.Fatalf("err = %v, want the skipped count reported", err)
	}
}

func TestResolveResumeTargetUnknownRefRefuses(t *testing.T) {
	records := []LeadSessionRecord{finishedRecord("lead-old", testHarnessA, 30)}
	_, err := ResolveResumeTarget(records, refReq("no-such-id"))
	if err == nil || !strings.Contains(err.Error(), "no-such-id") {
		t.Fatalf("err = %v, want the unknown id named", err)
	}
}

// An explicit loom id that resolves to a row with no handle is a different
// failure from an unknown id, and says so.
func TestResolveResumeTargetRefWithoutHandleRefuses(t *testing.T) {
	records := []LeadSessionRecord{finishedRecord("lead-old", "", 30)}
	_, err := ResolveResumeTarget(records, refReq("lead-old"))
	if err == nil || !strings.Contains(err.Error(), "no recorded") {
		t.Fatalf("err = %v, want a 'no recorded resume id' refusal", err)
	}
}

// Two processes on one transcript is a corrupting write race. The guard is
// best-effort by construction: a stale heartbeat means the owner is gone.
func TestResolveResumeTargetLiveSessionRefused(t *testing.T) {
	live := finishedRecord("lead-live", testHarnessA, 1)
	live.Status = domain.AgentSessionRunning
	live.Finished = false
	live.LastHeartbeat = testNow.Add(-10 * time.Second)
	_, err := ResolveResumeTarget([]LeadSessionRecord{live}, continueReq())
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("err = %v, want a live-session refusal", err)
	}
}

func TestResolveResumeTargetStaleHeartbeatIsResumable(t *testing.T) {
	stale := finishedRecord("lead-stale", testHarnessA, 30)
	stale.Status = domain.AgentSessionRunning
	stale.Finished = false
	stale.LastHeartbeat = testNow.Add(-30 * time.Minute)
	if _, err := ResolveResumeTarget([]LeadSessionRecord{stale}, continueReq()); err != nil {
		t.Fatalf("a force-killed lead never clears its status; it must stay resumable: %v", err)
	}
}

// ── codex ───────────────────────────────────────────────────────────────────

func codexRecord(sessionID, threadID string, ageMinutes int) LeadSessionRecord {
	rec := finishedRecord(sessionID, "", ageMinutes)
	rec.Provider = RuntimeProviderCodex
	rec.CodexThreadID = threadID
	return rec
}

func TestResolveResumeTargetCodexPrefersRecordedThread(t *testing.T) {
	req := continueReq()
	req.Backend = "codex"
	target, err := ResolveResumeTarget([]LeadSessionRecord{codexRecord("lead-c", "thread-9", 5)}, req)
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if target.CodexThreadID != "thread-9" || target.UseCodexLast {
		t.Fatalf("target = %+v, want the recorded thread and no --last fallback", target)
	}
	if target.HarnessSessionID != "" {
		t.Fatalf("HarnessSessionID = %q, want empty on codex", target.HarnessSessionID)
	}
}

// --last is codex's own notion of "most recent" and may be a thread loom never
// launched, so it is offered only for --continue and always with a warning.
func TestResolveResumeTargetCodexFallsBackToLast(t *testing.T) {
	req := continueReq()
	req.Backend = "codex"
	target, err := ResolveResumeTarget([]LeadSessionRecord{codexRecord("lead-c", "", 5)}, req)
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if !target.UseCodexLast {
		t.Fatalf("target = %+v, want the --last fallback", target)
	}
	if len(target.Warnings) == 0 || !strings.Contains(strings.Join(target.Warnings, " "), "--last") {
		t.Fatalf("Warnings = %v, want one naming --last", target.Warnings)
	}
}

func TestResolveResumeTargetCodexExplicitRefNeverUsesLast(t *testing.T) {
	req := refReq("lead-c")
	req.Backend = "codex"
	_, err := ResolveResumeTarget([]LeadSessionRecord{codexRecord("lead-c", "", 5)}, req)
	if err == nil {
		t.Fatal("an explicit id with no recorded thread must refuse, not fall back to --last")
	}
}

func TestResolveResumeTargetCodexMatchesThreadID(t *testing.T) {
	req := refReq("thread-9")
	req.Backend = "codex"
	target, err := ResolveResumeTarget([]LeadSessionRecord{codexRecord("lead-c", "thread-9", 5)}, req)
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if target.MatchedBy != ResumeMatchCodexTID {
		t.Fatalf("MatchedBy = %q, want %q", target.MatchedBy, ResumeMatchCodexTID)
	}
}

// ── projection ──────────────────────────────────────────────────────────────

func TestLeadSessionRecordFromSession(t *testing.T) {
	finished := testNow
	session := &domain.AgentSession{
		SessionID: "lead-x",
		AgentID:   "nova",
		Status:    domain.AgentSessionCompleted,
		StartedAt: testNow.Add(-time.Hour),
		Metadata: map[string]string{
			MetadataLeadWorkDir:      " /repo ",
			MetadataRuntimeProvider:  "claude",
			MetadataHarnessSessionID: testHarnessA,
			MetadataCodexThreadID:    "thread-9",
		},
		FinishedAt: &finished,
	}
	rec := LeadSessionRecordFromSession(session)
	if rec.WorkDir != testWorkDir || rec.HarnessSessionID != testHarnessA || rec.CodexThreadID != "thread-9" {
		t.Fatalf("record = %+v", rec)
	}
	if !rec.Finished {
		t.Fatal("Finished = false, want true for a row with finished_at")
	}
	if got := rec.ResumeHandle("claude"); got != testHarnessA {
		t.Fatalf("ResumeHandle(claude) = %q", got)
	}
	if got := rec.ResumeHandle("codex"); got != "thread-9" {
		t.Fatalf("ResumeHandle(codex) = %q", got)
	}
	if LeadSessionRecordFromSession(nil).SessionID != "" {
		t.Fatal("a nil session must project to the zero record")
	}
}
