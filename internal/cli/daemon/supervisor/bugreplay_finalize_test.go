//go:build daemon_bugreplay

// Bug-replay fault tests for the finalization group: completion hooks acting on
// the wrong task state, and exit classification that reports a failed run as a
// success (or a success as a failure). Each test replays one open PR from the
// daemon bug catalogue and is named after it. Every bug here is bucket C: local
// process logic, with no TLA+ invariant in test/formal/daemon-attempt that
// would catch it. Where a model invariant is the nearest statement of the
// broken property, the test comment names it.
//
// Run with: go test -tags daemon_bugreplay -run BugReplay ./internal/cli/daemon/supervisor/
// These tests are expected to FAIL on v5 (1c6dabfc8) and PASS on the fix PR head.
package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// finalizeLabelStore is a stateful issue fake: labels and status persist across
// calls, so a test can run a pipeline twice and observe what the second pass
// sees. Every write is also recorded in order.
type finalizeLabelStore struct {
	status string
	labels []string
	ops    []string
}

func finalizeNewLabelStore(status string, labels ...string) (*finalizeLabelStore, *clitest.MockIssueBackend) {
	st := &finalizeLabelStore{status: status, labels: append([]string(nil), labels...)}
	m := clitest.NewMockIssueBackend()
	m.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{
			ID: id, Status: st.status, Labels: append([]string(nil), st.labels...),
		}}, nil
	}
	m.AddLabelFn = func(_ context.Context, _ string, l string) error {
		st.ops = append(st.ops, "add:"+l)
		if !finalizeHas(st.labels, l) {
			st.labels = append(st.labels, l)
		}
		return nil
	}
	m.RemoveLabelFn = func(_ context.Context, _ string, l string) error {
		st.ops = append(st.ops, "remove:"+l)
		kept := st.labels[:0]
		for _, x := range st.labels {
			if x != l {
				kept = append(kept, x)
			}
		}
		st.labels = kept
		return nil
	}
	m.UpdateFn = func(_ context.Context, _ string, p backend.UpdateParams) error {
		if p.Status != nil {
			st.ops = append(st.ops, "status:"+*p.Status)
			st.status = *p.Status
		}
		return nil
	}
	return st, m
}

func finalizeHas(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

// TestBugReplay_PR774_ClosedTaskHookConflictDoesNotDemoteRun replays #774.
//
// Bug: a completion hook whose write reaches an issue that was closed while the
// run was in flight gets the server's "issue is closed" conflict. v5 treats any
// hook error as a hook failure (session_finalize.go runCompletionHooks), so the
// successful run is demoted to exit -1 with CompletionHookFailure. Post-mortem
// then reopens the closed task and redispatches it, and every later round hits
// the same terminal row: an unbounded reopen/redispatch loop (PUPPET-618).
//
// Nearest model property: B1 NoForeignIssueWrite ("no reset reopens a closed
// issue"). The demotion is what drives that reopen on v5.
func TestBugReplay_PR774_ClosedTaskHookConflictDoesNotDemoteRun(t *testing.T) {
	sess := statusDesignSession(t)
	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionWriteDesign, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionAddLabel, Value: "ready-to-implement"},
		{Type: domain.AgentHookActionSetStatus, Value: "open"},
	}}
	ap := newHookAgentProcess(t, "T-774", hooks)
	ap.AgentSessionID = sess.SessionID()

	var writes []string
	m := clitest.NewMockIssueBackend()
	m.UpdateFn = func(_ context.Context, _ string, p backend.UpdateParams) error {
		switch {
		case p.Design != nil:
			writes = append(writes, "design")
		case p.Status != nil:
			writes = append(writes, "status:"+*p.Status)
		default:
			writes = append(writes, "update")
		}
		// The task was closed by someone else after the agent started.
		return backend.ErrConflict("Update", "issue is closed")
	}
	m.AddLabelFn = func(_ context.Context, _ string, l string) error {
		writes = append(writes, "add:"+l)
		return backend.ErrConflict("AddLabel", "issue is closed")
	}
	s := &Supervisor{IssueBackend: m}

	got := s.runCompletionHooks(ap, 0)

	if got != 0 {
		t.Errorf("exit code = %d, want the factual 0: a closed task is a decision, not a hook failure "+
			"(a demoted run is reopened and redispatched against the same closed row)", got)
	}
	if ap.LastError != nil {
		t.Errorf("LastError = %v (class %s), want nil", ap.LastError, ap.LastError.Class)
	}
	for _, w := range writes {
		if strings.HasPrefix(w, "status:") {
			t.Errorf("writes = %v: a status write reached a closed task", writes)
		}
	}
}

// TestBugReplay_PR774_OtherConflictStillDemotes is the guard rail for the
// #774 fix: only a terminal-row conflict is exempt. Passes on v5 and must keep
// passing on the fix head.
func TestBugReplay_PR774_OtherConflictStillDemotes(t *testing.T) {
	sess := statusDesignSession(t)
	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionWriteDesign, Source: domain.AgentHookCommentSourceFinalReply},
	}}
	ap := newHookAgentProcess(t, "T-774b", hooks)
	ap.AgentSessionID = sess.SessionID()
	m := clitest.NewMockIssueBackend()
	m.UpdateFn = func(context.Context, string, backend.UpdateParams) error {
		return backend.ErrConflict("Update", "claim is held by another session")
	}
	s := &Supervisor{IssueBackend: m}

	if got := s.runCompletionHooks(ap, 0); got != -1 {
		t.Fatalf("exit code = %d, want -1: a non-terminal conflict is a real hook failure", got)
	}
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome) {
		t.Fatalf("LastError = %v, want CompletionHookFailure", ap.LastError)
	}
}

// TestBugReplay_PR428_ShippedReviewCycleDoesNotReShip replays #428.
//
// Bug: when the review cycle reaches its threshold, v5 advanceReviewCycle
// (session_finalize.go:339-345) stamps the ship label and reopens the task but
// leaves the re-arm label and the round counters in place. The previous stage's
// filter still matches the re-arm label, it reclaims the task, the recomputed
// round count is unchanged, and the ship branch runs again: the task reships
// forever.
//
// No model invariant covers label routing between stages.
func TestBugReplay_PR428_ShippedReviewCycleDoesNotReShip(t *testing.T) {
	cycle := &domain.AgentHookCycle{Threshold: 2, RearmLabel: "criticized", ShipLabel: "ready"}
	st, m := finalizeNewLabelStore("open", "plan", "criticized", "review-cycle=1")
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-428", cycle); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if !finalizeHas(st.labels, "ready") {
		t.Fatalf("labels after first pass = %v, want the ship label", st.labels)
	}
	if finalizeHas(st.labels, "criticized") {
		t.Errorf("labels after ship = %v: the re-arm label survived, so the previous stage can reclaim the task", st.labels)
	}
	for _, l := range st.labels {
		if cycle.ParseCounter(l) > 0 {
			t.Errorf("labels after ship = %v: counter %q survived, so a later pass ships with no review", st.labels, l)
		}
	}

	// The previous stage reclaims (its filter matches whatever is left) and
	// the cycle hook runs again. It must not reach the ship branch a second time.
	st.ops = nil
	if err := s.advanceReviewCycle(context.Background(), "T-428", cycle); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	for _, op := range st.ops {
		if op == "add:ready" {
			t.Fatalf("second pass ops = %v: the shipped task shipped again (re-ship loop)", st.ops)
		}
	}
}

// finalizeLogSupervisor builds a supervisor with a real per-role daemon log
// directory and an isolated archive-log runtime dir.
func finalizeLogSupervisor(t *testing.T) (*Supervisor, *AgentProcess, string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", filepath.Join(tmp, "runtime"))
	t.Setenv("LOOM_CONFIG_DIR", "")
	logDir := filepath.Join(tmp, "daemon-logs")
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{LogDir: logDir}}
		},
		ProjectDir:  tmp,
		WorkspaceID: "ws-bugreplay",
	}
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "ember", Role: "plan", Backend: "codex"},
		WorktreePath: wt,
	}
	return s, ap, filepath.Join(logDir, "ws-bugreplay", "plan-ember.log")
}

// finalizeRunOnce drives the production log/exit sequence for one run: open
// the sinks the way spawnAgent does, let the child write, close the logs the
// way waitForAgent does, then classify.
func finalizeRunOnce(t *testing.T, s *Supervisor, ap *AgentProcess, output string, exitCode int) {
	t.Helper()
	cmd := &exec.Cmd{}
	ap.Mu.Lock()
	s.setupAgentLogFile(ap, cmd)
	ap.Mu.Unlock()
	if cmd.Stdout == nil {
		t.Fatal("setupAgentLogFile opened no sink")
	}
	if _, err := fmt.Fprint(cmd.Stdout, output); err != nil {
		t.Fatalf("child write: %v", err)
	}
	ap.Mu.Lock()
	closeAgentLogs(ap)
	ap.Mu.Unlock()
	s.classifyAgentExit(ap, exitCode)
}

// TestBugReplay_PR615_ExitClassifiedFromThisRunOnly replays #615.
//
// Bug: the per-role daemon log is opened O_APPEND (spawn.go openDaemonLogFile)
// and shared by every run of that agent, but v5 classifyAgentExit
// (classify.go:53) classifies a failed exit from the whole log tail. A marker
// written by an EARLIER run (here: the harness auth-required marker) becomes the
// verdict for a later, unrelated failure, and the agent is fatally stopped for
// an account wall this run never hit.
//
// No model invariant: exit classification is not modeled.
func TestBugReplay_PR615_ExitClassifiedFromThisRunOnly(t *testing.T) {
	s, ap, logPath := finalizeLogSupervisor(t)
	authFailure := agenterr.OutcomeFromHarness(wrapper.ErrAuth)

	// Run 1 genuinely hit the auth wall; its output stays in the append-only log.
	writeLockFile(t, ap.WorktreePath, &cli.LockInfo{TaskID: "PUPPET-1", AgentName: "ember"})
	finalizeRunOnce(t, s, ap, "[loom] starting task PUPPET-1\nError: "+agenterr.AuthRequiredMarker+": renew the harness login\n", 1)
	if ap.LastError == nil || ap.LastError.Class != authFailure {
		t.Fatalf("setup: run 1 class = %v, want %s (the marker this run wrote)", ap.LastError, authFailure)
	}
	if b, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(b), agenterr.AuthRequiredMarker) {
		t.Fatalf("setup: run 1 marker not in the shared daemon log %s (err %v)", logPath, err)
	}

	// Run 2 is a different task and fails for an unrelated reason.
	writeLockFile(t, ap.WorktreePath, &cli.LockInfo{TaskID: "PUPPET-2", AgentName: "ember"})
	finalizeRunOnce(t, s, ap, "[loom] starting task PUPPET-2\nError: go test ./... failed: 3 tests failed\n", 1)

	if ap.LastError == nil {
		t.Fatal("run 2 LastError = nil, want a failure class for exit 1")
	}
	if ap.LastError.Class == authFailure {
		t.Errorf("run 2 classified as %s from run 1's marker left in the append-only log; raw: %q",
			ap.LastError.Class, ap.LastError.RawOutput)
	}
}

// TestBugReplay_PR245_WorkScanFailureMarkerIsNotNoWork replays the daemon half
// of #245.
//
// Bug: an auto-mode agent that cannot read the ready queue ends its run without
// a task, and v5 classifyAgentExit maps every task-less exit 0 to NoWork
// (classify.go markNoWork), which the restart policy treats as a healthy idle
// with an uncounted retry. The fix makes auto-mode print a
// "loom: work scan failed: <cause>" marker and the supervisor classify it as
// WorkScanFailure. The marker text is the fix PR's contract; v5 ignores it.
// The auto-mode half is in internal/cli/automode/bugreplay_finalize_test.go.
//
// No model invariant: exit classification is not modeled.
func TestBugReplay_PR245_WorkScanFailureMarkerIsNotNoWork(t *testing.T) {
	s, ap, logPath := finalizeLogSupervisor(t)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	ap.LogFilePath = logPath
	cause := "failed to check ready tasks: HTTP 401 unauthorized"
	if err := os.WriteFile(logPath, []byte("loom: work scan failed: "+cause+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s.classifyAgentExit(ap, 0)

	if ap.LastNoWork {
		t.Errorf("LastNoWork = true: a failed ready-queue scan was recorded as an idle success")
	}
	if ap.LastError != nil && ap.LastError.Class == agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome) {
		t.Errorf("class = %s, want a work-scan failure, not NoWork", ap.LastError.Class)
	}
}
