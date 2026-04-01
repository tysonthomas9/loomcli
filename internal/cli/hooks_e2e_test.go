//go:build e2e
// +build e2e

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runLoomHooks runs the loom binary with the given args, optionally piping stdinData
// and injecting extra env vars. Returns stdout, stderr, and exit code.
func runLoomHooks(t *testing.T, dir string, stdinData string, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	loom := loomBinaryPath(t)
	cmd := exec.Command(loom, args...)
	cmd.Dir = dir

	if stdinData != "" {
		cmd.Stdin = strings.NewReader(stdinData)
	}

	// Filter out loom-specific env vars to avoid cross-contamination from
	// a running loom agent session, then append test-specific overrides.
	filtered := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "LOOM_SESSION_ID=") ||
			strings.HasPrefix(e, "LOOM_BEADS_DIR=") {
			continue
		}
		filtered = append(filtered, e)
	}
	cmd.Env = append(filtered, env...)

	var stdoutBuf, stderrBuf bytes.Buffer
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

// setupHookBeadsDir creates a temp dir structure with sessions/<sessionID>/
// ready for transcript appending, and returns the beads root path.
func setupHookBeadsDir(t *testing.T, sessionID string) string {
	t.Helper()
	beadsDir := t.TempDir()
	sessDir := filepath.Join(beadsDir, "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("create session dir: %v", err)
	}
	return beadsDir
}

// readTranscriptLines reads transcript.jsonl from the session directory
// and returns each line as a string. Returns empty slice if the file doesn't exist.
func readTranscriptLines(t *testing.T, beadsDir, sessionID string) []string {
	t.Helper()
	txPath := filepath.Join(beadsDir, "sessions", sessionID, "transcript.jsonl")
	data, err := os.ReadFile(txPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read transcript: %v", err)
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}

// --- Install / Uninstall / Status tests ---

func TestE2E_HooksInstall_Fresh(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	stdout, _, exitCode := runLoomHooks(t, ".", "", nil, "hooks", "install", dir)
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	if !strings.Contains(stdout, "Hooks installed in") || !strings.Contains(stdout, dir) {
		t.Errorf("unexpected stdout: %s", stdout)
	}

	// Verify settings.json exists
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}

	// Verify all 6 managed hook types are present
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	var hooks map[string]json.RawMessage
	if err := json.Unmarshal(settings["hooks"], &hooks); err != nil {
		t.Fatalf("parse hooks: %v", err)
	}

	expectedTypes := []string{"SessionStart", "UserPromptSubmit", "Stop", "SessionEnd", "PreToolUse", "PostToolUse"}
	for _, ht := range expectedTypes {
		if _, ok := hooks[ht]; !ok {
			t.Errorf("missing hook type %q in settings.json", ht)
		}
	}

	// Verify each hook type contains the correct loom command
	settingsStr := string(data)
	expectedCmds := []string{
		"loom hooks claude-code session-start",
		"loom hooks claude-code user-prompt-submit",
		"loom hooks claude-code stop",
		"loom hooks claude-code session-end",
		"loom hooks claude-code pre-task",
		"loom hooks claude-code post-task",
	}
	for _, cmd := range expectedCmds {
		if !strings.Contains(settingsStr, cmd) {
			t.Errorf("settings.json missing command %q", cmd)
		}
	}
}

func TestE2E_HooksInstall_Idempotent(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()

	// First install
	_, _, exitCode := runLoomHooks(t, ".", "", nil, "hooks", "install", dir)
	if exitCode != 0 {
		t.Fatalf("first install: expected exit 0, got %d", exitCode)
	}
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	first, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings after first install: %v", err)
	}

	// Second install
	_, _, exitCode = runLoomHooks(t, ".", "", nil, "hooks", "install", dir)
	if exitCode != 0 {
		t.Fatalf("second install: expected exit 0, got %d", exitCode)
	}
	second, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings after second install: %v", err)
	}

	// Settings should be byte-identical
	if !bytes.Equal(first, second) {
		t.Errorf("settings.json changed after second install:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestE2E_HooksUninstall_AfterInstall(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()

	// Install
	_, _, exitCode := runLoomHooks(t, ".", "", nil, "hooks", "install", dir)
	if exitCode != 0 {
		t.Fatalf("install: expected exit 0, got %d", exitCode)
	}

	// Uninstall
	stdout, _, exitCode := runLoomHooks(t, ".", "", nil, "hooks", "uninstall", dir)
	if exitCode != 0 {
		t.Fatalf("uninstall: expected exit 0, got %d", exitCode)
	}

	if !strings.Contains(stdout, "Hooks uninstalled from") || !strings.Contains(stdout, dir) {
		t.Errorf("unexpected stdout: %s", stdout)
	}

	// settings.json should still exist but have no loom hooks
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json should still exist: %v", err)
	}
	if strings.Contains(string(data), "loom hooks claude-code") {
		t.Errorf("settings.json still contains loom hooks after uninstall:\n%s", data)
	}
}

func TestE2E_HooksUninstall_NoSettingsFile(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir() // No .claude dir
	stdout, _, exitCode := runLoomHooks(t, ".", "", nil, "hooks", "uninstall", dir)
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	if !strings.Contains(stdout, "Hooks uninstalled from") || !strings.Contains(stdout, dir) {
		t.Errorf("unexpected stdout: %s", stdout)
	}
}

func TestE2E_HooksStatus_NotInstalled(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	stdout, _, exitCode := runLoomHooks(t, ".", "", nil, "hooks", "status", dir)
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	if !strings.Contains(stdout, "Hooks not installed in") || !strings.Contains(stdout, dir) {
		t.Errorf("unexpected stdout: %s", stdout)
	}
}

func TestE2E_HooksStatus_Installed(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()

	// Install first
	_, _, exitCode := runLoomHooks(t, ".", "", nil, "hooks", "install", dir)
	if exitCode != 0 {
		t.Fatalf("install: expected exit 0, got %d", exitCode)
	}

	// Check status
	stdout, _, exitCode := runLoomHooks(t, ".", "", nil, "hooks", "status", dir)
	if exitCode != 0 {
		t.Fatalf("status: expected exit 0, got %d", exitCode)
	}

	if !strings.Contains(stdout, "Hooks installed in") || !strings.Contains(stdout, dir) {
		t.Errorf("unexpected stdout: %s", stdout)
	}
	if strings.Contains(stdout, "Hooks not installed") {
		t.Errorf("stdout should NOT contain 'Hooks not installed': %s", stdout)
	}

	// Verify all 6 hook commands listed with 2-space indent
	expectedLines := []string{
		"  loom hooks claude-code session-start",
		"  loom hooks claude-code user-prompt-submit",
		"  loom hooks claude-code stop",
		"  loom hooks claude-code session-end",
		"  loom hooks claude-code pre-task",
		"  loom hooks claude-code post-task",
	}
	for _, line := range expectedLines {
		if !strings.Contains(stdout, line) {
			t.Errorf("stdout missing %q\nfull output:\n%s", line, stdout)
		}
	}
}

// --- Claude Code hook handler tests ---

func TestE2E_HooksClaudeCode_SessionStart(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	sessionID := "test-session-start"
	beadsDir := setupHookBeadsDir(t, sessionID)
	env := []string{
		"LOOM_SESSION_ID=" + sessionID,
		"LOOM_BEADS_DIR=" + beadsDir,
	}
	stdinJSON := `{"session_id":"abc","transcript_path":"/tmp/t.jsonl","model":"claude-sonnet-4-20250514"}`

	_, _, exitCode := runLoomHooks(t, ".", stdinJSON, env, "hooks", "claude-code", "session-start")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	lines := readTranscriptLines(t, beadsDir, sessionID)
	if len(lines) == 0 {
		t.Fatal("expected at least one transcript entry")
	}
	if !strings.Contains(lines[0], "Session started (model: claude-sonnet-4-20250514)") {
		t.Errorf("transcript entry missing expected content: %s", lines[0])
	}
}

func TestE2E_HooksClaudeCode_UserPromptSubmit(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	sessionID := "test-prompt"
	beadsDir := setupHookBeadsDir(t, sessionID)
	env := []string{
		"LOOM_SESSION_ID=" + sessionID,
		"LOOM_BEADS_DIR=" + beadsDir,
	}
	stdinJSON := `{"session_id":"abc","transcript_path":"/tmp/t.jsonl","prompt":"Write a function"}`

	_, _, exitCode := runLoomHooks(t, ".", stdinJSON, env, "hooks", "claude-code", "user-prompt-submit")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	lines := readTranscriptLines(t, beadsDir, sessionID)
	if len(lines) == 0 {
		t.Fatal("expected at least one transcript entry")
	}
	if !strings.Contains(lines[0], "Write a function") {
		t.Errorf("transcript entry missing expected content: %s", lines[0])
	}
}

func TestE2E_HooksClaudeCode_Stop(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	sessionID := "test-stop"
	beadsDir := setupHookBeadsDir(t, sessionID)
	env := []string{
		"LOOM_SESSION_ID=" + sessionID,
		"LOOM_BEADS_DIR=" + beadsDir,
	}
	stdinJSON := `{"session_id":"abc","transcript_path":"/tmp/t.jsonl"}`

	_, _, exitCode := runLoomHooks(t, ".", stdinJSON, env, "hooks", "claude-code", "stop")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	lines := readTranscriptLines(t, beadsDir, sessionID)
	if len(lines) == 0 {
		t.Fatal("expected at least one transcript entry")
	}
	if !strings.Contains(lines[0], "Turn completed") {
		t.Errorf("transcript entry missing expected content: %s", lines[0])
	}
}

func TestE2E_HooksClaudeCode_SessionEnd(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	sessionID := "test-session-end"
	beadsDir := setupHookBeadsDir(t, sessionID)
	env := []string{
		"LOOM_SESSION_ID=" + sessionID,
		"LOOM_BEADS_DIR=" + beadsDir,
	}
	stdinJSON := `{"session_id":"abc","transcript_path":"/tmp/t.jsonl"}`

	_, _, exitCode := runLoomHooks(t, ".", stdinJSON, env, "hooks", "claude-code", "session-end")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	lines := readTranscriptLines(t, beadsDir, sessionID)
	if len(lines) == 0 {
		t.Fatal("expected at least one transcript entry")
	}
	if !strings.Contains(lines[0], "Session ended") {
		t.Errorf("transcript entry missing expected content: %s", lines[0])
	}
}

func TestE2E_HooksClaudeCode_PreTask(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	sessionID := "test-pre-task"
	beadsDir := setupHookBeadsDir(t, sessionID)
	env := []string{
		"LOOM_SESSION_ID=" + sessionID,
		"LOOM_BEADS_DIR=" + beadsDir,
	}
	stdinJSON := `{"session_id":"abc","transcript_path":"/tmp/t.jsonl","tool_use_id":"tu_123","tool_input":{"prompt":"do something"}}`

	_, _, exitCode := runLoomHooks(t, ".", stdinJSON, env, "hooks", "claude-code", "pre-task")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	lines := readTranscriptLines(t, beadsDir, sessionID)
	if len(lines) == 0 {
		t.Fatal("expected at least one transcript entry")
	}
	if !strings.Contains(lines[0], "Subagent started (tool_use_id: tu_123)") {
		t.Errorf("transcript entry missing expected content: %s", lines[0])
	}
}

func TestE2E_HooksClaudeCode_PostTask(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	sessionID := "test-post-task"
	beadsDir := setupHookBeadsDir(t, sessionID)
	env := []string{
		"LOOM_SESSION_ID=" + sessionID,
		"LOOM_BEADS_DIR=" + beadsDir,
	}
	stdinJSON := `{"session_id":"abc","transcript_path":"/tmp/t.jsonl","tool_use_id":"tu_456","tool_input":{"prompt":"do something"},"tool_response":{"agentId":"agent-789"}}`

	_, _, exitCode := runLoomHooks(t, ".", stdinJSON, env, "hooks", "claude-code", "post-task")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	lines := readTranscriptLines(t, beadsDir, sessionID)
	if len(lines) == 0 {
		t.Fatal("expected at least one transcript entry")
	}
	if !strings.Contains(lines[0], "Subagent completed (tool_use_id: tu_456, agent_id: agent-789)") {
		t.Errorf("transcript entry missing expected content: %s", lines[0])
	}
}

func TestE2E_HooksClaudeCode_PostTaskNoAgentID(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	sessionID := "test-post-task-no-agent"
	beadsDir := setupHookBeadsDir(t, sessionID)
	env := []string{
		"LOOM_SESSION_ID=" + sessionID,
		"LOOM_BEADS_DIR=" + beadsDir,
	}
	stdinJSON := `{"session_id":"abc","transcript_path":"/tmp/t.jsonl","tool_use_id":"tu_456","tool_input":{"prompt":"do something"},"tool_response":{}}`

	_, _, exitCode := runLoomHooks(t, ".", stdinJSON, env, "hooks", "claude-code", "post-task")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	lines := readTranscriptLines(t, beadsDir, sessionID)
	if len(lines) == 0 {
		t.Fatal("expected at least one transcript entry")
	}
	// Should have tool_use_id but NOT agent_id
	if !strings.Contains(lines[0], "Subagent completed (tool_use_id: tu_456)") {
		t.Errorf("transcript entry missing expected content: %s", lines[0])
	}
	if strings.Contains(lines[0], "agent_id") {
		t.Errorf("transcript entry should NOT contain agent_id when absent: %s", lines[0])
	}
}

func TestE2E_HooksClaudeCode_EmptyStdin(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	sessionID := "test-empty"
	beadsDir := setupHookBeadsDir(t, sessionID)
	env := []string{
		"LOOM_SESSION_ID=" + sessionID,
		"LOOM_BEADS_DIR=" + beadsDir,
	}

	_, stderr, exitCode := runLoomHooks(t, ".", "", env, "hooks", "claude-code", "session-start")
	if exitCode != 0 {
		t.Fatalf("expected exit 0 (hooks always exit 0), got %d", exitCode)
	}
	if !strings.Contains(stderr, "parse error") {
		t.Errorf("stderr should contain 'parse error': %s", stderr)
	}
	if !strings.Contains(stderr, "empty hook input") {
		t.Errorf("stderr should contain 'empty hook input': %s", stderr)
	}

	// No transcript should be written
	lines := readTranscriptLines(t, beadsDir, sessionID)
	if len(lines) > 0 {
		t.Errorf("expected no transcript entries, got %d", len(lines))
	}
}

func TestE2E_HooksClaudeCode_InvalidJSON(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	sessionID := "test-invalid-json"
	beadsDir := setupHookBeadsDir(t, sessionID)
	env := []string{
		"LOOM_SESSION_ID=" + sessionID,
		"LOOM_BEADS_DIR=" + beadsDir,
	}

	_, stderr, exitCode := runLoomHooks(t, ".", "not valid json", env, "hooks", "claude-code", "stop")
	if exitCode != 0 {
		t.Fatalf("expected exit 0 (hooks always exit 0), got %d", exitCode)
	}
	if !strings.Contains(stderr, "parse error") {
		t.Errorf("stderr should contain 'parse error': %s", stderr)
	}

	// No transcript should be written
	lines := readTranscriptLines(t, beadsDir, sessionID)
	if len(lines) > 0 {
		t.Errorf("expected no transcript entries, got %d", len(lines))
	}
}

func TestE2E_HooksClaudeCode_MissingEnvVars(t *testing.T) {
	t.Parallel()

	stdinJSON := `{"session_id":"abc","transcript_path":"/tmp/t.jsonl","model":"claude-sonnet-4-20250514"}`

	// Build a clean env that explicitly excludes LOOM_SESSION_ID and LOOM_BEADS_DIR
	// so the hook handler sees them as unset (silent no-op path).
	loom := loomBinaryPath(t)
	cmd := exec.Command(loom, "hooks", "claude-code", "session-start")
	cmd.Stdin = strings.NewReader(stdinJSON)

	filtered := make([]string, 0)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "LOOM_SESSION_ID=") || strings.HasPrefix(e, "LOOM_BEADS_DIR=") {
			continue
		}
		filtered = append(filtered, e)
	}
	cmd.Env = filtered

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run loom: %v", err)
		}
	}

	if exitCode != 0 {
		t.Fatalf("expected exit 0 (hooks silently no-op), got %d", exitCode)
	}
	if stdoutBuf.String() != "" {
		t.Errorf("expected empty stdout, got: %s", stdoutBuf.String())
	}
	if stderrBuf.String() != "" {
		t.Errorf("expected empty stderr, got: %s", stderrBuf.String())
	}
}
