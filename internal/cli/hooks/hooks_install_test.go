package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readSettingsRaw reads and returns the raw JSON map from settings.json.
func readSettingsRaw(t *testing.T, worktreePath string) map[string]json.RawMessage {
	t.Helper()
	settingsPath := filepath.Join(worktreePath, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}
	return raw
}

// readHooksRaw extracts the hooks map from settings.json.
func readHooksRaw(t *testing.T, worktreePath string) map[string]json.RawMessage {
	t.Helper()
	rawSettings := readSettingsRaw(t, worktreePath)
	hooksRaw, ok := rawSettings["hooks"]
	if !ok {
		return nil
	}
	var hooks map[string]json.RawMessage
	if err := json.Unmarshal(hooksRaw, &hooks); err != nil {
		t.Fatalf("failed to parse hooks: %v", err)
	}
	return hooks
}

// readHookMatchers parses a specific hook type's matchers from settings.json.
func readHookMatchers(t *testing.T, worktreePath, hookType string) []claudeHookMatcher {
	t.Helper()
	hooks := readHooksRaw(t, worktreePath)
	if hooks == nil {
		return nil
	}
	raw, ok := hooks[hookType]
	if !ok {
		return nil
	}
	var matchers []claudeHookMatcher
	if err := json.Unmarshal(raw, &matchers); err != nil {
		t.Fatalf("failed to parse %s matchers: %v", hookType, err)
	}
	return matchers
}

// matcherContainsCommand checks if any matcher's hooks contain the given command.
func matcherContainsCommand(matchers []claudeHookMatcher, command string) bool {
	for _, m := range matchers {
		for _, h := range m.Hooks {
			if h.Command == command {
				return true
			}
		}
	}
	return false
}

// writeSettings writes a settings.json file with the given content.
func writeSettings(t *testing.T, worktreePath, content string) {
	t.Helper()
	claudeDir := filepath.Join(worktreePath, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("failed to create .claude dir: %v", err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write settings.json: %v", err)
	}
}

func TestInstallClaudeHooks_FreshInstall(t *testing.T) {
	dir := t.TempDir()

	err := InstallClaudeHooks(dir)
	if err != nil {
		t.Fatalf("InstallClaudeHooks() error = %v", err)
	}

	// Verify settings.json was created
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}

	// Verify all hook types are present with the two-level matcher structure
	for _, hookType := range managedHookTypes {
		matchers := readHookMatchers(t, dir, hookType)
		if len(matchers) == 0 {
			t.Errorf("%s: no hook matchers found", hookType)
			continue
		}
		expectedCmd := hookCommands[hookType]
		if !matcherContainsCommand(matchers, expectedCmd) {
			t.Errorf("%s: expected command %q not found in matchers %+v", hookType, expectedCmd, matchers)
		}
		// Verify the matcher matches expected value (empty for session/turn hooks, "Task" for tool hooks)
		expectedMatcher := hookMatchers[hookType]
		if matchers[0].Matcher != expectedMatcher {
			t.Errorf("%s: expected matcher %q, got %q", hookType, expectedMatcher, matchers[0].Matcher)
		}
	}
}

func TestInstallClaudeHooks_Idempotent(t *testing.T) {
	dir := t.TempDir()

	// First install
	if err := InstallClaudeHooks(dir); err != nil {
		t.Fatalf("first InstallClaudeHooks() error = %v", err)
	}

	// Read the file after first install
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	first, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json after first install: %v", err)
	}

	// Second install
	if err := InstallClaudeHooks(dir); err != nil {
		t.Fatalf("second InstallClaudeHooks() error = %v", err)
	}

	// Read the file after second install
	second, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json after second install: %v", err)
	}

	// Files should be byte-identical
	if string(first) != string(second) {
		t.Errorf("double install produced different JSON.\nFirst:\n%s\nSecond:\n%s", first, second)
	}

	// Each hook type should have exactly one loom entry across all matchers
	for _, hookType := range managedHookTypes {
		matchers := readHookMatchers(t, dir, hookType)
		loomCount := 0
		for _, m := range matchers {
			for _, e := range m.Hooks {
				if e.Command == hookCommands[hookType] {
					loomCount++
				}
			}
		}
		if loomCount != 1 {
			t.Errorf("%s: expected 1 loom hook, got %d", hookType, loomCount)
		}
	}
}

func TestInstallClaudeHooks_PreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()

	// Seed with allowedTools, model, and a third-party hook (two-level format)
	writeSettings(t, dir, `{
  "allowedTools": ["Read", "Write", "Bash"],
  "model": "claude-sonnet-4-20250514",
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo third-party stop"}]
      }
    ]
  }
}`)

	if err := InstallClaudeHooks(dir); err != nil {
		t.Fatalf("InstallClaudeHooks() error = %v", err)
	}

	rawSettings := readSettingsRaw(t, dir)

	// Verify allowedTools preserved
	if _, ok := rawSettings["allowedTools"]; !ok {
		t.Error("allowedTools field was not preserved")
	} else {
		var tools []string
		if err := json.Unmarshal(rawSettings["allowedTools"], &tools); err != nil {
			t.Fatalf("failed to parse allowedTools: %v", err)
		}
		if len(tools) != 3 {
			t.Errorf("allowedTools = %v, want 3 items", tools)
		}
	}

	// Verify model preserved
	if _, ok := rawSettings["model"]; !ok {
		t.Error("model field was not preserved")
	} else {
		var model string
		if err := json.Unmarshal(rawSettings["model"], &model); err != nil {
			t.Fatalf("failed to parse model: %v", err)
		}
		if model != "claude-sonnet-4-20250514" {
			t.Errorf("model = %q, want %q", model, "claude-sonnet-4-20250514")
		}
	}

	// Verify third-party Stop hook preserved alongside loom hook
	stopMatchers := readHookMatchers(t, dir, "Stop")
	if !matcherContainsCommand(stopMatchers, "echo third-party stop") {
		t.Error("third-party Stop hook was not preserved")
	}
	if !matcherContainsCommand(stopMatchers, hookCommands["Stop"]) {
		t.Error("loom Stop hook was not installed")
	}
}

func TestInstallClaudeHooks_PreservesUnknownHookTypes(t *testing.T) {
	dir := t.TempDir()

	// Seed with Notification hooks (a real Claude Code hook type we don't manage)
	writeSettings(t, dir, `{
  "hooks": {
    "Notification": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo notification received"}]
      }
    ],
    "SubagentStop": [
      {
        "matcher": ".*",
        "hooks": [{"type": "command", "command": "echo subagent stopped"}]
      }
    ]
  }
}`)

	if err := InstallClaudeHooks(dir); err != nil {
		t.Fatalf("InstallClaudeHooks() error = %v", err)
	}

	hooks := readHooksRaw(t, dir)

	// Verify Notification hook is preserved (unknown types are kept as raw JSON)
	if _, ok := hooks["Notification"]; !ok {
		t.Error("Notification hook type was not preserved")
	} else {
		var matchers []claudeHookMatcher
		if err := json.Unmarshal(hooks["Notification"], &matchers); err != nil {
			t.Fatalf("failed to parse Notification: %v", err)
		}
		if !matcherContainsCommand(matchers, "echo notification received") {
			t.Errorf("Notification matchers = %+v, want to contain 'echo notification received'", matchers)
		}
	}

	// Verify SubagentStop hook is preserved
	if _, ok := hooks["SubagentStop"]; !ok {
		t.Error("SubagentStop hook type was not preserved")
	} else {
		var matchers []claudeHookMatcher
		if err := json.Unmarshal(hooks["SubagentStop"], &matchers); err != nil {
			t.Fatalf("failed to parse SubagentStop: %v", err)
		}
		if !matcherContainsCommand(matchers, "echo subagent stopped") {
			t.Errorf("SubagentStop matchers = %+v, want to contain 'echo subagent stopped'", matchers)
		}
	}

	// Verify our managed hooks were also installed
	for _, hookType := range managedHookTypes {
		if _, ok := hooks[hookType]; !ok {
			t.Errorf("%s hook type was not installed", hookType)
		}
	}
}

func TestUninstallClaudeHooks_OnlyRemovesLoom(t *testing.T) {
	dir := t.TempDir()

	// Seed with both loom and third-party hooks (two-level format)
	writeSettings(t, dir, `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo third-party stop"}]
      },
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "loom hooks claude-code stop"}]
      }
    ],
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "loom hooks claude-code session-start"}]
      },
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo third-party session-start"}]
      }
    ],
    "UserPromptSubmit": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "loom hooks claude-code user-prompt-submit"}]
      }
    ],
    "SessionEnd": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "loom hooks claude-code session-end"}]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo notification received"}]
      }
    ]
  }
}`)

	if err := UninstallClaudeHooks(dir); err != nil {
		t.Fatalf("UninstallClaudeHooks() error = %v", err)
	}

	hooks := readHooksRaw(t, dir)

	// Stop should still have the third-party matcher
	stopMatchers := readHookMatchers(t, dir, "Stop")
	if len(stopMatchers) != 1 {
		t.Fatalf("Stop: expected 1 matcher after uninstall, got %d", len(stopMatchers))
	}
	if !matcherContainsCommand(stopMatchers, "echo third-party stop") {
		t.Errorf("Stop: third-party hook was not preserved, got %+v", stopMatchers)
	}

	// SessionStart should still have the third-party matcher
	startMatchers := readHookMatchers(t, dir, "SessionStart")
	if len(startMatchers) != 1 {
		t.Fatalf("SessionStart: expected 1 matcher after uninstall, got %d", len(startMatchers))
	}
	if !matcherContainsCommand(startMatchers, "echo third-party session-start") {
		t.Errorf("SessionStart: third-party hook was not preserved, got %+v", startMatchers)
	}

	// UserPromptSubmit and SessionEnd had only loom hooks, so they should be removed
	if _, ok := hooks["UserPromptSubmit"]; ok {
		t.Error("UserPromptSubmit should be removed (had only loom hooks)")
	}
	if _, ok := hooks["SessionEnd"]; ok {
		t.Error("SessionEnd should be removed (had only loom hooks)")
	}

	// Notification should be preserved (not a managed type)
	if _, ok := hooks["Notification"]; !ok {
		t.Error("Notification hook type was not preserved")
	}
}

func TestUninstallClaudeHooks_NoLoomHooks(t *testing.T) {
	dir := t.TempDir()

	// Seed with only third-party hooks (no loom hooks) in two-level format
	writeSettings(t, dir, `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo third-party stop"}]
      }
    ]
  },
  "model": "claude-sonnet-4-20250514"
}`)

	if err := UninstallClaudeHooks(dir); err != nil {
		t.Fatalf("UninstallClaudeHooks() error = %v", err)
	}

	// Verify the file is unchanged (third-party hook still present)
	stopMatchers := readHookMatchers(t, dir, "Stop")
	if len(stopMatchers) != 1 {
		t.Fatalf("Stop: expected 1 matcher, got %d", len(stopMatchers))
	}
	if !matcherContainsCommand(stopMatchers, "echo third-party stop") {
		t.Errorf("Stop: third-party hook was not preserved, got %+v", stopMatchers)
	}

	// Verify model is preserved
	rawSettings := readSettingsRaw(t, dir)
	if _, ok := rawSettings["model"]; !ok {
		t.Error("model field was not preserved")
	}
}

func TestUninstallClaudeHooks_NoSettingsFile(t *testing.T) {
	dir := t.TempDir()

	// Should not error when no settings file exists
	err := UninstallClaudeHooks(dir)
	if err != nil {
		t.Fatalf("UninstallClaudeHooks() should not error when no settings file: %v", err)
	}
}

func TestClaudeHooksStatus(t *testing.T) {
	t.Run("not installed - no settings file", func(t *testing.T) {
		dir := t.TempDir()

		installed, hooks, err := ClaudeHooksStatus(dir)
		if err != nil {
			t.Fatalf("ClaudeHooksStatus() error = %v", err)
		}
		if installed {
			t.Error("expected installed=false for empty dir")
		}
		if len(hooks) != 0 {
			t.Errorf("expected no hooks, got %v", hooks)
		}
	})

	t.Run("not installed - no loom hooks", func(t *testing.T) {
		dir := t.TempDir()
		writeSettings(t, dir, `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo third-party stop"}]
      }
    ]
  }
}`)

		installed, hooks, err := ClaudeHooksStatus(dir)
		if err != nil {
			t.Fatalf("ClaudeHooksStatus() error = %v", err)
		}
		if installed {
			t.Error("expected installed=false when no loom hooks")
		}
		if len(hooks) != 0 {
			t.Errorf("expected no hooks, got %v", hooks)
		}
	})

	t.Run("installed - all hooks present", func(t *testing.T) {
		dir := t.TempDir()
		if err := InstallClaudeHooks(dir); err != nil {
			t.Fatalf("InstallClaudeHooks() error = %v", err)
		}

		installed, hooks, err := ClaudeHooksStatus(dir)
		if err != nil {
			t.Fatalf("ClaudeHooksStatus() error = %v", err)
		}
		if !installed {
			t.Error("expected installed=true after install")
		}
		expectedCount := len(managedHookTypes) + len(yieldHookEntries)
		if len(hooks) != expectedCount {
			t.Errorf("expected %d hooks, got %d: %v", expectedCount, len(hooks), hooks)
		}
	})

	t.Run("installed - partial hooks", func(t *testing.T) {
		dir := t.TempDir()
		writeSettings(t, dir, `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "loom hooks claude-code stop"}]
      }
    ]
  }
}`)

		installed, hooks, err := ClaudeHooksStatus(dir)
		if err != nil {
			t.Fatalf("ClaudeHooksStatus() error = %v", err)
		}
		if !installed {
			t.Error("expected installed=true with partial hooks")
		}
		if len(hooks) != 1 {
			t.Errorf("expected 1 hook, got %d: %v", len(hooks), hooks)
		}
		if hooks[0] != "loom hooks claude-code stop" {
			t.Errorf("hook = %q, want %q", hooks[0], "loom hooks claude-code stop")
		}
	})
}

func TestInstallClaudeHooks_CreatesClaudeDir(t *testing.T) {
	dir := t.TempDir()

	// Ensure .claude/ does not exist
	claudeDir := filepath.Join(dir, ".claude")
	if _, err := os.Stat(claudeDir); err == nil {
		t.Fatal(".claude/ should not exist before install")
	}

	if err := InstallClaudeHooks(dir); err != nil {
		t.Fatalf("InstallClaudeHooks() error = %v", err)
	}

	// Verify .claude/ was created
	info, err := os.Stat(claudeDir)
	if err != nil {
		t.Fatalf(".claude/ was not created: %v", err)
	}
	if !info.IsDir() {
		t.Error(".claude is not a directory")
	}

	// Verify settings.json exists inside it
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}
}

// TestInstallClaudeHooks_RealClaudeCodeFormat seeds settings.json with the
// exact two-level structure that Claude Code writes, then verifies that
// InstallClaudeHooks appends loom matchers without corrupting existing hooks.
func TestInstallClaudeHooks_RealClaudeCodeFormat(t *testing.T) {
	dir := t.TempDir()

	// This fixture mirrors the real Claude Code settings format from
	// the Entire CLI reference (claudecode/hooks_test.go).
	writeSettings(t, dir, `{
  "permissions": {
    "allow": ["Read(**)", "Write(**)"],
    "deny": ["Bash(rm -rf *)"]
  },
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "echo user stop hook"}
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Write",
        "hooks": [
          {"type": "command", "command": "echo user wrote file"}
        ]
      }
    ]
  }
}`)

	if err := InstallClaudeHooks(dir); err != nil {
		t.Fatalf("InstallClaudeHooks() error = %v", err)
	}

	// Read raw settings to verify the full structure
	rawSettings := readSettingsRaw(t, dir)

	// Verify permissions are preserved
	if _, ok := rawSettings["permissions"]; !ok {
		t.Fatal("permissions field was not preserved")
	}

	hooks := readHooksRaw(t, dir)

	// Verify user Stop hook is preserved and loom hook added
	stopMatchers := readHookMatchers(t, dir, "Stop")
	if !matcherContainsCommand(stopMatchers, "echo user stop hook") {
		t.Error("user Stop hook was not preserved")
	}
	if !matcherContainsCommand(stopMatchers, "loom hooks claude-code stop") {
		t.Error("loom Stop hook was not installed")
	}

	// Verify user PostToolUse hook is preserved (loom doesn't manage PostToolUse)
	if _, ok := hooks["PostToolUse"]; !ok {
		t.Error("PostToolUse hook was not preserved")
	} else {
		var ptMatchers []claudeHookMatcher
		if err := json.Unmarshal(hooks["PostToolUse"], &ptMatchers); err != nil {
			t.Fatalf("failed to parse PostToolUse: %v", err)
		}
		if !matcherContainsCommand(ptMatchers, "echo user wrote file") {
			t.Error("user PostToolUse hook was not preserved")
		}
	}

	// Verify all managed hook types were installed
	for _, hookType := range managedHookTypes {
		matchers := readHookMatchers(t, dir, hookType)
		expectedCmd := hookCommands[hookType]
		if !matcherContainsCommand(matchers, expectedCmd) {
			t.Errorf("%s: loom hook %q was not installed", hookType, expectedCmd)
		}
	}

	// Verify the output JSON has the correct two-level structure by
	// re-reading and checking a Stop matcher's shape
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	// Ensure we can parse the full output as the expected structure
	var fullSettings struct {
		Hooks map[string][]claudeHookMatcher `json:"hooks"`
	}
	if err := json.Unmarshal(data, &fullSettings); err != nil {
		t.Fatalf("output JSON does not match two-level structure: %v", err)
	}

	// Verify Stop matchers have the expected shape
	for _, m := range fullSettings.Hooks["Stop"] {
		if len(m.Hooks) == 0 {
			t.Errorf("Stop matcher %q has empty hooks array", m.Matcher)
		}
		for _, h := range m.Hooks {
			if h.Type != "command" {
				t.Errorf("Stop hook type = %q, want %q", h.Type, "command")
			}
		}
	}
}
