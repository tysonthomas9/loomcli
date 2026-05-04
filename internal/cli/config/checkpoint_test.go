package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveAndLoadCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	cp := &Checkpoint{
		AgentName:  "falcon",
		TaskID:     "loom-123",
		EpicID:     "loom-epic1",
		GitDiff:    "diff --git a/main.go\n+added line",
		ExitCode:   1,
		ErrorClass: "RateLimited",
		Timestamp:  time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
	}

	if err := SaveCheckpoint(tmpDir, cp); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	loaded, err := LoadCheckpoint(tmpDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadCheckpoint returned nil")
	}

	if loaded.AgentName != cp.AgentName {
		t.Errorf("AgentName: got %q, want %q", loaded.AgentName, cp.AgentName)
	}
	if loaded.TaskID != cp.TaskID {
		t.Errorf("TaskID: got %q, want %q", loaded.TaskID, cp.TaskID)
	}
	if loaded.EpicID != cp.EpicID {
		t.Errorf("EpicID: got %q, want %q", loaded.EpicID, cp.EpicID)
	}
	if loaded.GitDiff != cp.GitDiff {
		t.Errorf("GitDiff: got %q, want %q", loaded.GitDiff, cp.GitDiff)
	}
	if loaded.ExitCode != cp.ExitCode {
		t.Errorf("ExitCode: got %d, want %d", loaded.ExitCode, cp.ExitCode)
	}
	if loaded.ErrorClass != cp.ErrorClass {
		t.Errorf("ErrorClass: got %q, want %q", loaded.ErrorClass, cp.ErrorClass)
	}
	if !loaded.Timestamp.Equal(cp.Timestamp) {
		t.Errorf("Timestamp: got %v, want %v", loaded.Timestamp, cp.Timestamp)
	}
}

func TestLoadCheckpointNotExists(t *testing.T) {
	tmpDir := t.TempDir()

	cp, err := LoadCheckpoint(tmpDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint should not error for missing file: %v", err)
	}
	if cp != nil {
		t.Error("LoadCheckpoint should return nil for missing file")
	}
}

func TestClearCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	// Save then clear
	cp := &Checkpoint{
		AgentName: "falcon",
		TaskID:    "loom-456",
		ExitCode:  1,
		Timestamp: time.Now(),
	}
	if err := SaveCheckpoint(tmpDir, cp); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	if err := ClearCheckpoint(tmpDir); err != nil {
		t.Fatalf("ClearCheckpoint failed: %v", err)
	}

	loaded, err := LoadCheckpoint(tmpDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint after clear failed: %v", err)
	}
	if loaded != nil {
		t.Error("LoadCheckpoint should return nil after clear")
	}
}

func TestClearCheckpointNotExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Clearing a non-existent checkpoint should not error
	if err := ClearCheckpoint(tmpDir); err != nil {
		t.Fatalf("ClearCheckpoint should not error for missing file: %v", err)
	}
}

func TestSaveCheckpointAtomicity(t *testing.T) {
	tmpDir := t.TempDir()

	cp := &Checkpoint{
		AgentName: "test",
		TaskID:    "loom-789",
		ExitCode:  137,
		Timestamp: time.Now(),
	}

	if err := SaveCheckpoint(tmpDir, cp); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	// Verify no .tmp file left behind
	tmpPath := filepath.Join(tmpDir, CheckpointFileName+".tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("Temp file should not exist after successful save")
	}

	// Verify the checkpoint file is valid JSON
	data, err := os.ReadFile(filepath.Join(tmpDir, CheckpointFileName))
	if err != nil {
		t.Fatalf("Failed to read checkpoint file: %v", err)
	}
	var loaded Checkpoint
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Checkpoint file is not valid JSON: %v", err)
	}
}

func TestTruncateDiff(t *testing.T) {
	// Short diff — no truncation
	short := "abc"
	if got := truncateDiff(short, 100); got != short {
		t.Errorf("Short diff truncated: got %q, want %q", got, short)
	}

	// Large diff — should be truncated
	large := strings.Repeat("x", 8000)
	result := truncateDiff(large, 4096)
	if len(result) > 4096 {
		t.Errorf("Truncated diff too long: %d bytes", len(result))
	}
	if !strings.Contains(result, "truncated") {
		t.Error("Truncated diff should contain truncation notice")
	}
	if !strings.Contains(result, "8000") {
		t.Error("Truncation notice should include original size")
	}
}

func TestCaptureGitDiffCleanWorktree(t *testing.T) {
	clearGitEnvVars(t)
	// Create a temp git repo with no changes
	tmpDir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = tmpDir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("commit", "--allow-empty", "-m", "initial")

	diff := captureSingleRepoDiff(tmpDir, maxDiffBytes)
	if diff != "" {
		t.Errorf("Expected empty diff for clean worktree, got %q", diff)
	}
}

func TestCaptureGitDiff(t *testing.T) {
	clearGitEnvVars(t)
	tmpDir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = tmpDir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init")
	// Create and commit a file
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "main.go")
	run("commit", "-m", "initial")

	// Make an uncommitted change
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\n\nfunc hello() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	diff := captureSingleRepoDiff(tmpDir, maxDiffBytes)
	if diff == "" {
		t.Error("Expected non-empty diff for dirty worktree")
	}
	if !strings.Contains(diff, "hello") {
		t.Errorf("Diff should contain 'hello', got %q", diff)
	}
}

func TestCaptureGitDiffTruncation(t *testing.T) {
	clearGitEnvVars(t)
	tmpDir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = tmpDir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init")
	// Create and commit a file
	if err := os.WriteFile(filepath.Join(tmpDir, "big.txt"), []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "big.txt")
	run("commit", "-m", "initial")

	// Make a large change (> 4KB)
	bigContent := strings.Repeat("line of content here\n", 500) // ~10KB
	if err := os.WriteFile(filepath.Join(tmpDir, "big.txt"), []byte(bigContent), 0644); err != nil {
		t.Fatal(err)
	}

	diff := captureSingleRepoDiff(tmpDir, 4096)
	if len(diff) > 4096 {
		t.Errorf("Diff should be truncated to 4096 bytes, got %d", len(diff))
	}
	if !strings.Contains(diff, "truncated") {
		t.Error("Truncated diff should contain truncation notice")
	}
}

func TestSaveAndLoadCheckpoint_WithYieldReason(t *testing.T) {
	tmpDir := t.TempDir()

	cp := &Checkpoint{
		AgentName:   "falcon",
		TaskID:      "loom-yield-1",
		EpicID:      "loom-epic1",
		GitDiff:     "+yielded change",
		ExitCode:    0,
		ErrorClass:  "Yielded",
		YieldReason: "config_removed",
		Timestamp:   time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
	}

	if err := SaveCheckpoint(tmpDir, cp); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	loaded, err := LoadCheckpoint(tmpDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadCheckpoint returned nil")
	}

	if loaded.YieldReason != "config_removed" {
		t.Errorf("YieldReason: got %q, want %q", loaded.YieldReason, "config_removed")
	}
	if loaded.ErrorClass != "Yielded" {
		t.Errorf("ErrorClass: got %q, want %q", loaded.ErrorClass, "Yielded")
	}
	if loaded.ExitCode != 0 {
		t.Errorf("ExitCode: got %d, want 0", loaded.ExitCode)
	}
	if loaded.AgentName != cp.AgentName {
		t.Errorf("AgentName: got %q, want %q", loaded.AgentName, cp.AgentName)
	}
	if loaded.TaskID != cp.TaskID {
		t.Errorf("TaskID: got %q, want %q", loaded.TaskID, cp.TaskID)
	}
	if loaded.GitDiff != cp.GitDiff {
		t.Errorf("GitDiff: got %q, want %q", loaded.GitDiff, cp.GitDiff)
	}
}

func TestLoadCheckpoint_BackwardsCompatible(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a checkpoint JSON manually WITHOUT the yield_reason field,
	// simulating a checkpoint from an older version of the code.
	oldJSON := `{
  "agent_name": "hawk",
  "task_id": "loom-old-1",
  "git_diff": "+old change",
  "exit_code": 1,
  "error_class": "RateLimited",
  "timestamp": "2026-03-01T12:00:00Z"
}`
	cpPath := filepath.Join(tmpDir, CheckpointFileName)
	if err := os.WriteFile(cpPath, []byte(oldJSON), 0600); err != nil {
		t.Fatalf("failed to write old checkpoint: %v", err)
	}

	loaded, err := LoadCheckpoint(tmpDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadCheckpoint returned nil")
	}

	if loaded.YieldReason != "" {
		t.Errorf("YieldReason: got %q, want empty string for old checkpoint", loaded.YieldReason)
	}
	if loaded.ErrorClass != "RateLimited" {
		t.Errorf("ErrorClass: got %q, want %q", loaded.ErrorClass, "RateLimited")
	}
	if loaded.ExitCode != 1 {
		t.Errorf("ExitCode: got %d, want 1", loaded.ExitCode)
	}
}
