//go:build e2e
// +build e2e

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupStatusTestWorkspace creates a temp dir with git init + bd init.
func setupStatusTestWorkspace(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd binary not found on PATH; skipping E2E test")
	}
	dir := initTempGitRepo(t)

	cmd := exec.Command("bd", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bd init failed: %v\n%s", err, out)
	}
	return dir
}

// runLoomStatus executes `loom status` with given args in the specified directory.
func runLoomStatus(t *testing.T, dir string, args ...string) (stdout string, exitCode int) {
	t.Helper()
	loom := loomBinaryPath(t)
	fullArgs := append([]string{"status"}, args...)
	cmd := exec.Command(loom, fullArgs...)
	cmd.Dir = dir

	// Isolate from host environment
	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, "LOOM_REDIS_ADDR=") ||
			strings.HasPrefix(e, "LOOM_FLEETDB_ENABLED=") ||
			strings.HasPrefix(e, "LOOM_BACKEND=") {
			continue
		}
		filtered = append(filtered, e)
	}
	filtered = append(filtered, "HOME="+dir)
	filtered = append(filtered, "LOOM_CONFIG_DIR="+filepath.Join(dir, ".loom-config"))
	filtered = append(filtered, "GIT_CONFIG_NOSYSTEM=1")
	cmd.Env = filtered

	var stdoutBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &strings.Builder{}

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run loom status: %v", err)
		}
	}
	return stdoutBuf.String(), exitCode
}

// runLoomStatusWithEnv executes `loom status` with extra environment variables.
func runLoomStatusWithEnv(t *testing.T, dir string, extraEnv []string, args ...string) (stdout string, exitCode int) {
	t.Helper()
	loom := loomBinaryPath(t)
	fullArgs := append([]string{"status"}, args...)
	cmd := exec.Command(loom, fullArgs...)
	cmd.Dir = dir

	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, "LOOM_REDIS_ADDR=") ||
			strings.HasPrefix(e, "LOOM_FLEETDB_ENABLED=") ||
			strings.HasPrefix(e, "LOOM_BACKEND=") {
			continue
		}
		filtered = append(filtered, e)
	}
	filtered = append(filtered, "HOME="+dir)
	filtered = append(filtered, "LOOM_CONFIG_DIR="+filepath.Join(dir, ".loom-config"))
	filtered = append(filtered, "GIT_CONFIG_NOSYSTEM=1")
	filtered = append(filtered, extraEnv...)
	cmd.Env = filtered

	var stdoutBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &strings.Builder{}

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run loom status: %v", err)
		}
	}
	return stdoutBuf.String(), exitCode
}

// statusJSONOutput mirrors StatusData for subprocess JSON parsing.
type statusJSONOutput struct {
	Daemon    statusJSONDaemon    `json:"daemon"`
	Backend   statusJSONBackend   `json:"backend"`
	Worktrees statusJSONWorktrees `json:"worktrees"`
	Beads     statusJSONBeads     `json:"beads"`
	Git       statusJSONGit       `json:"git"`
	Redis     statusJSONRedis     `json:"redis"`
	Issues    []json.RawMessage   `json:"issues,omitempty"`
}

type statusJSONDaemon struct {
	Running  bool `json:"running"`
	PID      int  `json:"pid,omitempty"`
	StalePID bool `json:"stale_pid,omitempty"`
}

type statusJSONBackend struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

type statusJSONWorktrees struct {
	Active int               `json:"active"`
	Idle   int               `json:"idle"`
	List   []json.RawMessage `json:"list,omitempty"`
}

type statusJSONBeads struct {
	Open       int `json:"open"`
	InProgress int `json:"in_progress"`
	Review     int `json:"review"`
	Closed     int `json:"closed"`
}

type statusJSONGit struct {
	NeedsPush int `json:"needs_push"`
	NeedsPull int `json:"needs_pull"`
}

type statusJSONRedis struct {
	Configured bool   `json:"configured"`
	Connected  bool   `json:"connected,omitempty"`
	Error      string `json:"error,omitempty"`
}

// parseStatusJSON parses the JSON output from loom status --json.
func parseStatusJSON(t *testing.T, stdout string) statusJSONOutput {
	t.Helper()
	var out statusJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse status JSON output: %v\nraw output:\n%s", err, stdout)
	}
	return out
}

func TestE2E_StatusExitCode(t *testing.T) {
	dir := setupStatusTestWorkspace(t)

	_, exitCode := runLoomStatus(t, dir)
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
}

func TestE2E_StatusHumanOutput(t *testing.T) {
	dir := setupStatusTestWorkspace(t)

	stdout, _ := runLoomStatus(t, dir)

	// Verify section headers are present
	for _, header := range []string{"Daemon:", "Backend:", "Beads:", "Git:", "Redis:"} {
		if !strings.Contains(stdout, header) {
			t.Errorf("expected output to contain %q, got:\n%s", header, stdout)
		}
	}

	// Daemon should not be running in container
	if !strings.Contains(stdout, "not running") {
		t.Errorf("expected 'not running' in daemon line, got:\n%s", stdout)
	}

	// Backend should show source in parens
	if !strings.Contains(stdout, "(via") {
		t.Errorf("expected '(via' in backend line, got:\n%s", stdout)
	}

	// Beads line should use hyphen "in-progress" (human format)
	if !strings.Contains(stdout, "in-progress") {
		t.Errorf("expected 'in-progress' (hyphen) in human Beads line, got:\n%s", stdout)
	}

	// Human output must NOT contain "in_progress" (that's the JSON key)
	// Only check within the Beads line to avoid false positives
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "Beads:") && strings.Contains(line, "in_progress") {
			t.Errorf("human Beads line should use 'in-progress' (hyphen), not 'in_progress' (underscore), got: %s", line)
		}
	}

	// Redis should not be configured
	if !strings.Contains(stdout, "Redis:      not configured") {
		t.Errorf("expected 'Redis:      not configured' in output, got:\n%s", stdout)
	}
}

func TestE2E_StatusJSONOutput(t *testing.T) {
	dir := setupStatusTestWorkspace(t)

	stdout, exitCode := runLoomStatus(t, dir, "--json")
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}

	data := parseStatusJSON(t, stdout)

	// Daemon should not be running
	if data.Daemon.Running {
		t.Error("expected daemon.running to be false")
	}

	// Backend name should be non-empty
	if data.Backend.Name == "" {
		t.Error("expected backend.name to be non-empty")
	}

	// Redis should not be configured (LOOM_REDIS_ADDR filtered out)
	if data.Redis.Configured {
		t.Error("expected redis.configured to be false")
	}

	// Worktrees should be valid integers >= 0
	if data.Worktrees.Active < 0 {
		t.Errorf("expected worktrees.active >= 0, got %d", data.Worktrees.Active)
	}
	if data.Worktrees.Idle < 0 {
		t.Errorf("expected worktrees.idle >= 0, got %d", data.Worktrees.Idle)
	}

	// Verify in_progress key is correctly parsed (underscore in JSON)
	// Re-parse raw JSON to check the actual key name
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("failed to parse raw JSON: %v", err)
	}
	var beadsRaw map[string]json.RawMessage
	if err := json.Unmarshal(raw["beads"], &beadsRaw); err != nil {
		t.Fatalf("failed to parse beads JSON: %v", err)
	}
	if _, ok := beadsRaw["in_progress"]; !ok {
		t.Error("expected 'in_progress' key (underscore) in beads JSON")
	}
}

func TestE2E_StatusJSONBackendName(t *testing.T) {
	dir := setupStatusTestWorkspace(t)

	stdout, exitCode := runLoomStatusWithEnv(t, dir, []string{"LOOM_BACKEND=codex"}, "--json")
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}

	data := parseStatusJSON(t, stdout)

	if data.Backend.Name != "codex" {
		t.Errorf("expected backend.name = 'codex', got %q", data.Backend.Name)
	}
	if data.Backend.Source != "env" {
		t.Errorf("expected backend.source = 'env', got %q", data.Backend.Source)
	}
}

func TestE2E_StatusWithBeads(t *testing.T) {
	dir := setupStatusTestWorkspace(t)

	// Create a few tasks via bd create
	for _, title := range []string{"Test task one", "Test task two"} {
		cmd := exec.Command("bd", "create", "--title", title)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd create failed: %v\n%s", err, out)
		}
	}

	// JSON output should show beads.open > 0
	stdout, exitCode := runLoomStatus(t, dir, "--json")
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}

	data := parseStatusJSON(t, stdout)
	if data.Beads.Open < 1 {
		t.Errorf("expected beads.open >= 1 after creating tasks, got %d", data.Beads.Open)
	}

	// Human output should contain Beads section with "open" and "in-progress" (hyphen)
	humanOut, _ := runLoomStatus(t, dir)
	if !strings.Contains(humanOut, "Beads:") {
		t.Error("expected 'Beads:' in human output")
	}
	if !strings.Contains(humanOut, "open") {
		t.Error("expected 'open' in human Beads line")
	}
	if !strings.Contains(humanOut, "in-progress") {
		t.Error("expected 'in-progress' (hyphen) in human Beads line")
	}
}

func TestE2E_StatusStalePIDDetection(t *testing.T) {
	dir := setupStatusTestWorkspace(t)

	// Create .loom directory and write a stale PID file
	loomDir := filepath.Join(dir, ".loom")
	if err := os.MkdirAll(loomDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Use an impossible PID that won't be running
	pidFile := filepath.Join(loomDir, "daemon.pid")
	if err := os.WriteFile(pidFile, []byte("2147483647\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Human output should contain "not running (stale pid file)" (lowercase pid)
	humanOut, _ := runLoomStatus(t, dir)
	if !strings.Contains(humanOut, "not running (stale pid file)") {
		t.Errorf("expected 'not running (stale pid file)' in human output, got:\n%s", humanOut)
	}

	// JSON output should have daemon.stale_pid == true and daemon.running == false
	jsonOut, _ := runLoomStatus(t, dir, "--json")
	data := parseStatusJSON(t, jsonOut)
	if data.Daemon.Running {
		t.Error("expected daemon.running to be false with stale PID")
	}
	if !data.Daemon.StalePID {
		t.Error("expected daemon.stale_pid to be true with stale PID file")
	}
}

func TestE2E_StatusBranchFlag(t *testing.T) {
	dir := setupStatusTestWorkspace(t)

	stdout, exitCode := runLoomStatus(t, dir, "--branch=main", "--json")
	if exitCode != 0 {
		t.Errorf("expected exit code 0 with --branch=main, got %d", exitCode)
	}

	// Verify output is valid JSON
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("--branch=main output is not valid JSON: %v\nraw:\n%s", err, stdout)
	}
}

func TestE2E_StatusRedisNotConfigured(t *testing.T) {
	dir := setupStatusTestWorkspace(t)

	// Human output
	humanOut, _ := runLoomStatus(t, dir)
	if !strings.Contains(humanOut, "Redis:      not configured") {
		t.Errorf("expected 'Redis:      not configured' (exact whitespace), got:\n%s", humanOut)
	}

	// JSON output
	jsonOut, _ := runLoomStatus(t, dir, "--json")
	data := parseStatusJSON(t, jsonOut)
	if data.Redis.Configured {
		t.Error("expected redis.configured to be false")
	}
	if data.Redis.Connected {
		t.Error("expected redis.connected to be false")
	}
}

func TestE2E_StatusDaemonNotRunning(t *testing.T) {
	dir := setupStatusTestWorkspace(t)

	// Ensure no .loom/daemon.pid exists
	pidFile := filepath.Join(dir, ".loom", "daemon.pid")
	os.Remove(pidFile)

	// Human output should say "not running" but NOT "stale pid"
	humanOut, _ := runLoomStatus(t, dir)
	if !strings.Contains(humanOut, "not running") {
		t.Errorf("expected 'not running' in daemon line, got:\n%s", humanOut)
	}
	// Check the Daemon line specifically doesn't contain "stale pid"
	for _, line := range strings.Split(humanOut, "\n") {
		if strings.HasPrefix(line, "Daemon:") {
			if strings.Contains(line, "stale pid") {
				t.Errorf("daemon line should NOT contain 'stale pid' when no pid file exists, got: %s", line)
			}
			break
		}
	}

	// JSON: daemon.running == false, stale_pid should be false/absent
	jsonOut, _ := runLoomStatus(t, dir, "--json")
	data := parseStatusJSON(t, jsonOut)
	if data.Daemon.Running {
		t.Error("expected daemon.running to be false")
	}
	if data.Daemon.StalePID {
		t.Error("expected daemon.stale_pid to be false when no pid file exists")
	}
}

func TestE2E_StatusGitSynced(t *testing.T) {
	dir := setupStatusTestWorkspace(t)

	humanOut, _ := runLoomStatus(t, dir)
	if !strings.Contains(humanOut, "Git:        all synced") {
		t.Errorf("expected 'Git:        all synced' (exact whitespace), got:\n%s", humanOut)
	}
}
