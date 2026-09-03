//go:build unix

package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
)

// seedPriorRun writes a finished run's output into the per-role daemon log
// before this cycle opens it, and returns the log path. The marker is written
// from agenterr's constant rather than spelled out, so this file cannot trip
// the very detector it tests when an agent reads it.
func seedPriorRun(t *testing.T, s *Supervisor, ap *AgentProcess, tmp string) string {
	t.Helper()
	logDir := filepath.Join(tmp, "daemon-logs", s.WorkspaceID)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	path := filepath.Join(logDir, fmt.Sprintf("%s-%s.log", ap.Entry.Role, ap.Entry.Worktree))
	prior := "[loom] starting task PUPPET-1\n" +
		"Error: " + agenterr.AuthRequiredMarker + ": renew the harness login\n"
	if err := os.WriteFile(path, []byte(prior), 0o600); err != nil {
		t.Fatalf("seed prior run: %v", err)
	}
	return path
}

// runCycle drives the production exit ordering: set the sinks up the way
// spawnAgent does, let the child write, then close the logs and classify —
// which is what waitForAgent does before classifyAgentExit runs.
func runCycle(t *testing.T, s *Supervisor, ap *AgentProcess, childOutput string, exitCode int) {
	t.Helper()
	cmd := &exec.Cmd{}
	setupAndMirror(t, s, ap, cmd)
	if _, err := cmd.Stdout.Write([]byte(childOutput)); err != nil {
		t.Fatalf("child write: %v", err)
	}
	ap.Mu.Lock()
	closeAgentLogs(ap)
	ap.Mu.Unlock()
	s.classifyAgentExit(ap, exitCode)
}

// TestExitPath_ClassificationKeepsThisRunsLogOffset is the wiring this whole
// change exists to fix.
//
// TestClassifyAgentExit_LogStartOffsetSkipsPriorRunAuthBanner already proves
// the offset scopes classification correctly — but it hand-builds an
// AgentProcess with the offset set, so it never runs the real exit sequence.
// In production closeAgentLogs ran first and zeroed the offset, so
// classification always read the whole append-only log: one genuinely failed
// run left a marker behind, and every later run of that agent — including the
// ones that exited 0 — was fatally stopped as an account wall it never hit.
func TestExitPath_ClassificationKeepsThisRunsLogOffset(t *testing.T) {
	s, ap, tmp := newMirrorTestSupervisor(t, "daemon-logs")
	seedPriorRun(t, s, ap, tmp)
	writeLockFile(t, ap.WorktreePath, &cli.LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: ap.Entry.Worktree,
		TaskID:    "PUPPET-2",
		StartedAt: time.Now(),
	})

	runCycle(t, s, ap, "[loom] starting task PUPPET-2\n[loom] review complete\n", 0)

	if ap.LastError != nil && ap.LastError.Class == agenterr.OutcomeFromHarness(wrapper.ErrAuth) {
		t.Fatalf("clean exit classified as AuthFailure from a previous run's marker; "+
			"raw: %q", ap.LastError.RawOutput)
	}
}

// TestExitPath_MarkerFromThisRunStillClassifies is the other half: scoping the
// window must not turn the detector off. A marker this run wrote is still the
// account statement it always was, on a clean exit code too — that is the case
// classifyFromHarnessMarker documents (a walled agent that claimed no task).
func TestExitPath_MarkerFromThisRunStillClassifies(t *testing.T) {
	s, ap, tmp := newMirrorTestSupervisor(t, "daemon-logs")
	seedPriorRun(t, s, ap, tmp)

	runCycle(t, s, ap, "Error: "+agenterr.AuthRequiredMarker+": renew the harness login\n", 0)

	if ap.LastError == nil {
		t.Fatal("LastError = nil, want AuthFailure for a marker this run emitted")
	}
	if want := agenterr.OutcomeFromHarness(wrapper.ErrAuth); ap.LastError.Class != want {
		t.Errorf("class = %s, want %s", ap.LastError.Class, want)
	}
}

// TestCloseAgentLogs_PreservesClassificationOffset pins the contract directly,
// so a future "clear all per-cycle state here" cleanup cannot silently
// reintroduce the bug above.
func TestCloseAgentLogs_PreservesClassificationOffset(t *testing.T) {
	s, ap, tmp := newMirrorTestSupervisor(t, "daemon-logs")
	prior := seedPriorRun(t, s, ap, tmp)
	info, err := os.Stat(prior)
	if err != nil {
		t.Fatalf("stat seeded log: %v", err)
	}

	cmd := &exec.Cmd{}
	setupAndMirror(t, s, ap, cmd)
	ap.Mu.Lock()
	closeAgentLogs(ap)
	offset := ap.LogFileStartOffset
	ap.Mu.Unlock()

	if offset != info.Size() {
		t.Errorf("LogFileStartOffset after close = %d, want %d — classifyAgentExit "+
			"runs after closeAgentLogs and reads this field", offset, info.Size())
	}
}

// TestSetupAgentLogFile_NoDaemonLogClearsWindow covers the staleness the line
// above would otherwise allow: with no daemon log this cycle there is no file
// whose bytes belong to this run, so neither half of the window may survive
// from the previous one.
func TestSetupAgentLogFile_NoDaemonLogClearsWindow(t *testing.T) {
	s, ap, _ := newMirrorTestSupervisor(t, "")
	ap.LogFilePath = "/previous/cycle/plan-ember.log"
	ap.LogFileStartOffset = 4096

	ap.Mu.Lock()
	s.setupAgentLogFile(ap, &exec.Cmd{})
	ap.Mu.Unlock()
	t.Cleanup(func() {
		ap.Mu.Lock()
		closeAgentLogs(ap)
		ap.Mu.Unlock()
	})

	if ap.LogFilePath != "" || ap.LogFileStartOffset != 0 {
		t.Errorf("stale classification window survived: path=%q offset=%d",
			ap.LogFilePath, ap.LogFileStartOffset)
	}
}
