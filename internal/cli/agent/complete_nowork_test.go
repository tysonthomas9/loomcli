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

// An advisory role can claim a task, discover there is nothing actionable, and
// report --no-work. Reporting no work must not strand that task: the claim has
// to be released so the issue returns from in_progress to open with no owner,
// otherwise the "no work" signal quietly parks a real task forever.
//
// runComplete calls releaseClaimOnComplete unconditionally, before the marker
// is written; backend.ClaimReleaser's contract (and FleetBackend's /release
// call) is what performs the in_progress -> open transition. This pins that the
// --no-work path does not skip that step and that the marker still records the
// task the agent had claimed.
func TestRunComplete_NoWork_ReleasesHeldClaim(t *testing.T) {
	stub := newReleaserStub(nil)
	cli.SetDefaultIssueBackend(stub)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	worktreePath := t.TempDir()
	resolvedPath, err := filepath.EvalSymlinks(worktreePath)
	if err != nil {
		resolvedPath = worktreePath
	}
	writeReleaseTestLock(t, worktreePath, "ISSUE-77")

	t.Setenv("LOOM_WORKTREE_PATH", worktreePath)
	t.Setenv("LOOM_AGENT_NAME", "")

	completeNoWork = true
	completeReason = "reviewed, nothing to change"
	t.Cleanup(func() {
		completeNoWork = false
		completeReason = ""
		os.Remove(GetSignalFilePath(resolvedPath))
	})

	runComplete(nil, nil)

	if got := stub.called.Load(); got != 1 {
		t.Fatalf("ReleaseClaim call count = %d, want 1 -- --no-work must not skip the claim release", got)
	}
	if got, _ := stub.lastID.Load().(string); got != "ISSUE-77" {
		t.Errorf("released %q, want ISSUE-77 (the task recorded in the agent lock)", got)
	}
	if got, _ := stub.lastActor.Load().(string); got != "test-planner" {
		t.Errorf("release actor = %q, want the lock's agent name", got)
	}

	data, err := os.ReadFile(filepath.Join(resolvedPath, noWorkFileName))
	if err != nil {
		t.Fatalf("expected a no-work marker: %v", err)
	}
	var report noWorkReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("no-work marker is not valid JSON: %v", err)
	}
	if report.TaskID != "ISSUE-77" {
		t.Errorf("marker TaskID = %q, want ISSUE-77 so an operator can see which task was claimed and then abandoned", report.TaskID)
	}
}
