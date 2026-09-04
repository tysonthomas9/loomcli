package supervisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestClassifyAgentExit_WorkScanFailureMarkerBeatsNoWork(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "agent.log")
	cause := "failed to check ready tasks: HTTP 429 rate limit exceeded"
	if err := os.WriteFile(logPath, []byte(agenterr.WorkScanFailureMarker+": "+cause+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "test", Role: "plan", Backend: "codex"},
		WorktreePath: tmpDir,
		LogFilePath:  logPath,
	}

	s.classifyAgentExit(ap, 0)

	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.WorkScanFailureOutcome) {
		t.Fatalf("LastError = %#v, want WorkScanFailure", ap.LastError)
	}
	if ap.LastError.Message != cause {
		t.Errorf("LastError.Message = %q, want %q", ap.LastError.Message, cause)
	}
	if ap.LastNoWork {
		t.Fatal("LastNoWork = true, want false for a failed work scan")
	}
}

func TestClassifyAgentExit_CleanIdleWithoutMarkerRemainsNoWork(t *testing.T) {
	tmpDir := t.TempDir()
	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "test", Role: "plan", Backend: "codex"},
		WorktreePath: tmpDir,
	}

	s.classifyAgentExit(ap, 0)

	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome) {
		t.Fatalf("LastError = %#v, want NoWork", ap.LastError)
	}
	if !ap.LastNoWork {
		t.Fatal("LastNoWork = false, want true for a genuine idle exit")
	}
}
