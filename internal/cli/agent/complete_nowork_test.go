package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func TestRunComplete_NoWork_WritesMarker(t *testing.T) {
	tmpDir := t.TempDir()
	worktreePath := filepath.Join(tmpDir, "test-worktree")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("failed to create worktree dir: %v", err)
	}

	absPath, _ := filepath.Abs(worktreePath)
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolvedPath = absPath
	}

	t.Setenv("LOOM_WORKTREE_PATH", worktreePath)
	t.Setenv("LOOM_AGENT_NAME", "critic")

	completeNoWork = true
	completeReason = "design unchanged since my last critique"
	t.Cleanup(func() {
		completeNoWork = false
		completeReason = ""
	})

	runComplete(nil, nil)

	markerPath := filepath.Join(resolvedPath, noWorkFileName)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("expected a parseable no-work marker at %s, got error: %v", markerPath, err)
	}

	var report noWorkReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("no-work marker is not valid JSON: %v", err)
	}
	if report.Reason != completeReason {
		t.Errorf("Reason = %q, want %q", report.Reason, completeReason)
	}
	if report.ReportedBy != "critic" {
		t.Errorf("ReportedBy = %q, want %q", report.ReportedBy, "critic")
	}

	// The legacy signal file must still be written.
	signalFile := GetSignalFilePath(resolvedPath)
	t.Cleanup(func() { os.Remove(signalFile) })
	if _, err := os.Stat(signalFile); err != nil {
		t.Errorf("legacy signal file should still exist after --no-work: %v", err)
	}
}

func TestRunComplete_NoWork_CarriesTaskIDFromLock(t *testing.T) {
	tmpDir := t.TempDir()
	worktreePath := filepath.Join(tmpDir, "test-worktree")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("failed to create worktree dir: %v", err)
	}

	absPath, _ := filepath.Abs(worktreePath)
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolvedPath = absPath
	}

	writeLockFileForTest(t, resolvedPath, "loom-42", "critic")

	t.Setenv("LOOM_WORKTREE_PATH", worktreePath)

	completeNoWork = true
	completeReason = ""
	t.Cleanup(func() {
		completeNoWork = false
		completeReason = ""
	})

	runComplete(nil, nil)

	markerPath := filepath.Join(resolvedPath, noWorkFileName)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("expected a no-work marker, got error: %v", err)
	}
	var report noWorkReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("no-work marker is not valid JSON: %v", err)
	}
	if report.TaskID != "loom-42" {
		t.Errorf("TaskID = %q, want %q (from lock file)", report.TaskID, "loom-42")
	}
	if report.Reason != "agent reported no work" {
		t.Errorf("Reason = %q, want the default fallback message when --reason is unset", report.Reason)
	}
	if report.ReportedBy != "critic" {
		t.Errorf("ReportedBy = %q, want %q (from lock file's AgentName)", report.ReportedBy, "critic")
	}
}

func TestRunComplete_NoWork_UnwritableDir_StillExitsCleanly(t *testing.T) {
	tmpDir := t.TempDir()
	worktreePath := filepath.Join(tmpDir, "test-worktree")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("failed to create worktree dir: %v", err)
	}

	t.Setenv("LOOM_WORKTREE_PATH", worktreePath)
	// Point the marker at a directory that does not exist so the write fails —
	// this must be a warning, never a crash or non-zero exit.
	t.Setenv("LOOM_NOWORK_FILE", filepath.Join(tmpDir, "does-not-exist", noWorkFileName))

	completeNoWork = true
	completeReason = "unwritable"
	t.Cleanup(func() {
		completeNoWork = false
		completeReason = ""
	})

	// Must not panic or os.Exit.
	runComplete(nil, nil)
}

// writeLockFileForTest writes a minimal lock file with the given task/agent
// so releaseClaimOnComplete/writeNoWorkMarker can read TaskID/AgentName from
// it. Distinct from writeLockFile (emit_events_test.go), which hardcodes
// AgentName.
func writeLockFileForTest(t *testing.T, dir, taskID, agentName string) {
	t.Helper()
	info := cli.LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: agentName,
		TaskID:    taskID,
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("failed to marshal lock info: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, cli.LockFileName), data, 0600); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}
}
