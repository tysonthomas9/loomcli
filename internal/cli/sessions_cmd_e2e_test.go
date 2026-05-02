//go:build e2e
// +build e2e

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupSessionsTestRepo creates an isolated temp dir with a real git repo and .beads/.
func setupSessionsTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	runGit(t, resolved, "init")
	runGit(t, resolved, "config", "user.email", "e2e@test.local")
	runGit(t, resolved, "config", "user.name", "E2E Test")
	runGit(t, resolved, "commit", "--allow-empty", "-m", "init")

	// Run bd init to create .beads/
	cmd := exec.Command("bd", "init")
	cmd.Dir = resolved
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd init failed: %v\n%s", err, out)
	}
	return resolved
}

// seedSession creates a session subdirectory with metadata.json and appends to index.jsonl.
// baseDir is the working directory where loom will run (GetWorkspaceRuntimeDir returns "." in legacy mode,
// so sessions are stored at <baseDir>/sessions/).
func seedSession(t *testing.T, baseDir string, sid string, status string, endedAt *time.Time) {
	t.Helper()

	sessDir := filepath.Join(baseDir, "sessions", sid)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("create session dir %s: %v", sessDir, err)
	}

	startedAt := time.Now().UTC().Add(-1 * time.Hour)
	if endedAt != nil {
		startedAt = endedAt.Add(-1 * time.Hour)
	}

	var durationS float64
	exitCode := 0
	if endedAt != nil {
		durationS = endedAt.Sub(startedAt).Seconds()
	}
	if status == "failed" {
		exitCode = 1
	}

	meta := map[string]interface{}{
		"schema_version": 1,
		"session_id":     sid,
		"agent_name":     "test-agent",
		"backend":        "echo",
		"started_at":     startedAt.Format(time.RFC3339Nano),
		"status":         status,
		"exit_code":      exitCode,
		"duration_s":     durationS,
	}
	if endedAt != nil {
		meta["ended_at"] = endedAt.Format(time.RFC3339Nano)
	}

	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), metaData, 0o600); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "prompt.txt"), []byte("test prompt"), 0o600); err != nil {
		t.Fatalf("write prompt.txt: %v", err)
	}

	// Append to index.jsonl
	indexRec := map[string]interface{}{
		"schema_version": 1,
		"session_id":     sid,
		"agent_name":     "test-agent",
		"backend":        "echo",
		"started_at":     startedAt.Format(time.RFC3339Nano),
		"status":         status,
		"exit_code":      exitCode,
		"duration_s":     durationS,
	}
	if endedAt != nil {
		indexRec["ended_at"] = endedAt.Format(time.RFC3339Nano)
	}

	indexData, err := json.Marshal(indexRec)
	if err != nil {
		t.Fatalf("marshal index record: %v", err)
	}
	indexData = append(indexData, '\n')

	indexPath := filepath.Join(baseDir, "sessions", "index.jsonl")
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open index.jsonl: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(indexData); err != nil {
		t.Fatalf("write index.jsonl: %v", err)
	}
}

// runLoomSessions runs the loom binary as a subprocess with Dir set, LOOM_CONFIG_DIR
// pointed to an empty temp dir (so GetWorkspaceRuntimeDir returns "."), and captures output.
func runLoomSessions(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	loom := loomBinaryPath(t)
	cmd := exec.Command(loom, args...)
	cmd.Dir = dir

	// Point LOOM_CONFIG_DIR to an empty temp dir so GetWorkspaceRuntimeDir() returns "."
	// (no workspace config => legacy mode => beads dir = cwd).
	emptyConfigDir := t.TempDir()
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "LOOM_CONFIG_DIR=") ||
			strings.HasPrefix(e, "LOOM_BACKEND=") ||
			strings.HasPrefix(e, "LOOM_WORKTREES_DIR=") ||
			strings.HasPrefix(e, "LOOM_FLEETDB_ENABLED=") {
			continue
		}
		filtered = append(filtered, e)
	}
	filtered = append(filtered, "LOOM_CONFIG_DIR="+emptyConfigDir)
	filtered = append(filtered, "GIT_CONFIG_NOSYSTEM=1")
	cmd.Env = filtered

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run loom %s: %v", strings.Join(args, " "), err)
		}
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

func TestE2E_SessionsHelp(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	stdout, _, exitCode := runLoomSessions(t, t.TempDir(), "sessions")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	for _, want := range []string{"Manage agent sessions", "clean", "Remove old session data"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\nfull output:\n%s", want, stdout)
		}
	}
}

func TestE2E_SessionsHelpFlag(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	stdout, _, exitCode := runLoomSessions(t, t.TempDir(), "sessions", "--help")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	for _, want := range []string{"Manage agent sessions", "clean"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\nfull output:\n%s", want, stdout)
		}
	}
}

func TestE2E_SessionsCleanNoSessions(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	requireBdBinary(t)

	dir := setupSessionsTestRepo(t)
	stdout, _, exitCode := runLoomSessions(t, dir, "sessions", "clean")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}
	if !strings.Contains(stdout, "Purged 0 sessions older than 30d") {
		t.Errorf("expected 'Purged 0 sessions older than 30d' in output\nfull output:\n%s", stdout)
	}
}

func TestE2E_SessionsCleanPurgesOldSessions(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	requireBdBinary(t)

	dir := setupSessionsTestRepo(t)

	// Old session (60 days ago)
	oldEnded := time.Now().UTC().Add(-60 * 24 * time.Hour)
	seedSession(t, dir, "20260101-120000-test--aabbccdd", "completed", &oldEnded)

	// Recent session (1 hour ago)
	recentEnded := time.Now().UTC().Add(-1 * time.Hour)
	seedSession(t, dir, "20260327-120000-test--11223344", "completed", &recentEnded)

	stdout, _, exitCode := runLoomSessions(t, dir, "sessions", "clean")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}
	if !strings.Contains(stdout, "Purged 1 sessions older than 30d") {
		t.Errorf("expected 'Purged 1 sessions older than 30d'\nfull output:\n%s", stdout)
	}

	// Old session directory should be removed
	if _, err := os.Stat(filepath.Join(dir, "sessions", "20260101-120000-test--aabbccdd")); !os.IsNotExist(err) {
		t.Error("old session directory should have been removed")
	}
	// Recent session directory should still exist
	if _, err := os.Stat(filepath.Join(dir, "sessions", "20260327-120000-test--11223344")); err != nil {
		t.Error("recent session directory should still exist")
	}
}

func TestE2E_SessionsCleanCustomDuration(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	requireBdBinary(t)

	dir := setupSessionsTestRepo(t)

	// Session ended 3 days ago
	ended := time.Now().UTC().Add(-3 * 24 * time.Hour)
	seedSession(t, dir, "20260325-120000-test--custom01", "completed", &ended)

	// With --older-than 7d, session should survive (3 days < 7 days)
	stdout, _, exitCode := runLoomSessions(t, dir, "sessions", "clean", "--older-than", "7d")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}
	if !strings.Contains(stdout, "Purged 0 sessions older than 7d") {
		t.Errorf("expected 'Purged 0 sessions older than 7d'\nfull output:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "20260325-120000-test--custom01")); err != nil {
		t.Error("session directory should still exist after --older-than 7d")
	}

	// With --older-than 1d, session should be purged (3 days > 1 day)
	stdout, _, exitCode = runLoomSessions(t, dir, "sessions", "clean", "--older-than", "1d")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}
	if !strings.Contains(stdout, "Purged 1 sessions older than 1d") {
		t.Errorf("expected 'Purged 1 sessions older than 1d'\nfull output:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "20260325-120000-test--custom01")); !os.IsNotExist(err) {
		t.Error("session directory should have been removed after --older-than 1d")
	}
}

func TestE2E_SessionsCleanPurgeAllWithZeroDuration(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	requireBdBinary(t)

	dir := setupSessionsTestRepo(t)

	// Create 3 completed sessions ended 1 hour ago
	ended := time.Now().UTC().Add(-1 * time.Hour)
	for i := 0; i < 3; i++ {
		sid := fmt.Sprintf("20260328-12000%d-test--zero%04d", i, i)
		seedSession(t, dir, sid, "completed", &ended)
	}

	stdout, _, exitCode := runLoomSessions(t, dir, "sessions", "clean", "--older-than", "0s")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}
	if !strings.Contains(stdout, "Purged 3 sessions older than 0s") {
		t.Errorf("expected 'Purged 3 sessions older than 0s'\nfull output:\n%s", stdout)
	}

	// All session directories should be removed
	for i := 0; i < 3; i++ {
		sid := fmt.Sprintf("20260328-12000%d-test--zero%04d", i, i)
		if _, err := os.Stat(filepath.Join(dir, "sessions", sid)); !os.IsNotExist(err) {
			t.Errorf("session %s should have been removed", sid)
		}
	}
}

func TestE2E_SessionsCleanSkipsRunning(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	requireBdBinary(t)

	dir := setupSessionsTestRepo(t)

	// Running session with recent started_at (1 hour ago, within 4h StaleSessionThreshold)
	seedSession(t, dir, "20260328-115000-test--running1", "running", nil)

	// Completed session ended 1 hour ago
	ended := time.Now().UTC().Add(-1 * time.Hour)
	seedSession(t, dir, "20260328-100000-test--done0001", "completed", &ended)

	stdout, _, exitCode := runLoomSessions(t, dir, "sessions", "clean", "--older-than", "0s")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}
	if !strings.Contains(stdout, "Purged 1 sessions older than 0s") {
		t.Errorf("expected 'Purged 1 sessions older than 0s'\nfull output:\n%s", stdout)
	}

	// Running session directory should still exist
	if _, err := os.Stat(filepath.Join(dir, "sessions", "20260328-115000-test--running1")); err != nil {
		t.Error("running session directory should still exist")
	}
	// Completed session directory should be removed
	if _, err := os.Stat(filepath.Join(dir, "sessions", "20260328-100000-test--done0001")); !os.IsNotExist(err) {
		t.Error("completed session directory should have been removed")
	}
}

func TestE2E_SessionsCleanInvalidDuration(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	stdout, stderr, exitCode := runLoomSessions(t, t.TempDir(), "sessions", "clean", "--older-than", "notaduration")
	if exitCode == 0 {
		t.Fatal("expected non-zero exit code for invalid duration")
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "invalid --older-than value") {
		t.Errorf("expected 'invalid --older-than value' in output\nstdout: %s\nstderr: %s", stdout, stderr)
	}
}

func TestE2E_SessionsCleanHourDuration(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	requireBdBinary(t)

	dir := setupSessionsTestRepo(t)

	// Session ended 48 hours ago
	ended := time.Now().UTC().Add(-48 * time.Hour)
	seedSession(t, dir, "20260326-120000-test--hours001", "completed", &ended)

	stdout, _, exitCode := runLoomSessions(t, dir, "sessions", "clean", "--older-than", "24h")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}
	if !strings.Contains(stdout, "Purged 1 sessions older than 24h") {
		t.Errorf("expected 'Purged 1 sessions older than 24h'\nfull output:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "20260326-120000-test--hours001")); !os.IsNotExist(err) {
		t.Error("session directory should have been removed")
	}
}

func TestE2E_SessionsCleanHelpFlag(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	stdout, _, exitCode := runLoomSessions(t, t.TempDir(), "sessions", "clean", "--help")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	for _, want := range []string{"Remove old session data", "--older-than", "Remove sessions older than this duration"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\nfull output:\n%s", want, stdout)
		}
	}
}
