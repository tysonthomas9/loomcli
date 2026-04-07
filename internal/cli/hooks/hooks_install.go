package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// loomHookPrefix identifies loom hook commands in settings.json.
const loomHookPrefix = "loom hooks claude-code"

// claudeSettingsFile is the settings file used by Claude Code.
const claudeSettingsFile = "settings.json"

// managedHookTypes lists the Claude Code hook types that loom manages.
// Session-level and turn-level hooks use an empty matcher (match all).
// PreToolUse and PostToolUse use tool-specific matchers (e.g., "Task").
var managedHookTypes = []string{
	"SessionStart",
	"UserPromptSubmit",
	"Stop",
	"SessionEnd",
	"PreToolUse",
	"PostToolUse",
}

// hookCommands maps each managed hook type to its loom command.
var hookCommands = map[string]string{
	"SessionStart":     "loom hooks claude-code session-start",
	"UserPromptSubmit": "loom hooks claude-code user-prompt-submit",
	"Stop":             "loom hooks claude-code stop",
	"SessionEnd":       "loom hooks claude-code session-end",
	"PreToolUse":       "loom hooks claude-code pre-task",
	"PostToolUse":      "loom hooks claude-code post-task",
}

// hookMatchers maps hook types to their matcher pattern.
// Empty string means "match all" (session/turn hooks).
// "Task" means only fire when the Task tool is used (subagent hooks).
var hookMatchers = map[string]string{
	"SessionStart":     "",
	"UserPromptSubmit": "",
	"Stop":             "",
	"SessionEnd":       "",
	"PreToolUse":       "Task",
	"PostToolUse":      "Task",
}

// yieldHookEntries lists yield-specific hooks that are installed separately
// from the main hooks. These use empty matchers to fire on all tools.
var yieldHookEntries = []struct {
	hookType string
	matcher  string
	command  string
}{
	{"PreToolUse", "", "loom hooks claude-code yield-guard"},
}

// claudeHookMatcher groups hook entries under a matcher pattern.
// Claude Code expects this two-level structure:
//
//	[{"matcher": "", "hooks": [{"type": "command", "command": "..."}]}]
type claudeHookMatcher struct {
	Matcher string            `json:"matcher"`
	Hooks   []claudeHookEntry `json:"hooks"`
}

// claudeHookEntry represents a single hook command in settings.json.
type claudeHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// InstallClaudeHooks installs loom hook entries into .claude/settings.json
// at the given worktree path. The function preserves all unknown top-level
// fields and unknown hook types, making it safe to coexist with other tools.
// It is idempotent: calling it twice produces identical output.
func InstallClaudeHooks(worktreePath string) error {
	claudeDir := filepath.Join(worktreePath, ".claude")
	settingsPath := filepath.Join(claudeDir, claudeSettingsFile)

	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("failed to create .claude directory: %w", err)
	}

	rawSettings, err := loadSettingsJSON(settingsPath)
	if err != nil {
		return err
	}

	rawHooks, err := parseHooksFromSettings(rawSettings)
	if err != nil {
		return err
	}

	if err := addManagedHooks(rawHooks); err != nil {
		return err
	}

	if err := installYieldHooks(rawHooks); err != nil {
		return err
	}

	return writeSettingsJSON(settingsPath, rawSettings, rawHooks)
}

// loadSettingsJSON reads and parses a Claude settings file, returning empty map if absent.
func loadSettingsJSON(path string) (map[string]json.RawMessage, error) {
	data, readErr := os.ReadFile(path) //nolint:gosec
	if readErr != nil {
		return make(map[string]json.RawMessage), nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse existing settings.json: %w", err)
	}
	return raw, nil
}

// parseHooksFromSettings extracts the hooks map from rawSettings.
func parseHooksFromSettings(rawSettings map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	var rawHooks map[string]json.RawMessage
	if hooksRaw, ok := rawSettings["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &rawHooks); err != nil {
			return nil, fmt.Errorf("failed to parse hooks in settings.json: %w", err)
		}
	}
	if rawHooks == nil {
		rawHooks = make(map[string]json.RawMessage)
	}
	return rawHooks, nil
}

// addManagedHooks adds loom hook entries for each managed hook type if not already present.
func addManagedHooks(rawHooks map[string]json.RawMessage) error {
	for _, hookType := range managedHookTypes {
		var matchers []claudeHookMatcher
		if data, ok := rawHooks[hookType]; ok {
			if err := json.Unmarshal(data, &matchers); err != nil {
				return fmt.Errorf("failed to parse %s hooks: %w", hookType, err)
			}
		}
		if hasLoomHook(matchers) {
			continue
		}
		matchers = append(matchers, claudeHookMatcher{
			Matcher: hookMatchers[hookType],
			Hooks:   []claudeHookEntry{{Type: "command", Command: hookCommands[hookType]}},
		})
		data, err := json.Marshal(matchers)
		if err != nil {
			return fmt.Errorf("failed to marshal %s hooks: %w", hookType, err)
		}
		rawHooks[hookType] = data
	}
	return nil
}

// writeSettingsJSON marshals hooks back into settings and writes the file.
func writeSettingsJSON(path string, rawSettings map[string]json.RawMessage, rawHooks map[string]json.RawMessage) error {
	hooksJSON, err := json.Marshal(rawHooks)
	if err != nil {
		return fmt.Errorf("failed to marshal hooks: %w", err)
	}
	rawSettings["hooks"] = hooksJSON

	output, err := json.MarshalIndent(rawSettings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}
	output = append(output, '\n')
	if err := os.WriteFile(path, output, 0o600); err != nil {
		return fmt.Errorf("failed to write settings.json: %w", err)
	}
	return nil
}

// UninstallClaudeHooks removes all loom hook entries from .claude/settings.json
// at the given worktree path. Third-party and unknown hook types are preserved.
func UninstallClaudeHooks(worktreePath string) error {
	settingsPath := filepath.Join(worktreePath, ".claude", claudeSettingsFile)

	data, err := os.ReadFile(settingsPath) //nolint:gosec
	if err != nil {
		return nil // No settings file means nothing to uninstall
	}

	var rawSettings map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawSettings); err != nil {
		return fmt.Errorf("failed to parse settings.json: %w", err)
	}

	rawHooks, err := parseHooksFromSettings(rawSettings)
	if err != nil {
		return err
	}
	if len(rawHooks) == 0 {
		return nil
	}

	removeLoomHookEntries(rawHooks)
	updateSettingsHooks(rawSettings, rawHooks)

	output, err := json.MarshalIndent(rawSettings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}
	output = append(output, '\n')
	if err := os.WriteFile(settingsPath, output, 0o600); err != nil {
		return fmt.Errorf("failed to write settings.json: %w", err)
	}
	return nil
}

// removeLoomHookEntries removes all loom commands from each managed hook type.
func removeLoomHookEntries(rawHooks map[string]json.RawMessage) {
	for _, hookType := range managedHookTypes {
		data, ok := rawHooks[hookType]
		if !ok {
			continue
		}
		var matchers []claudeHookMatcher
		if err := json.Unmarshal(data, &matchers); err != nil {
			continue
		}
		filtered := removeLoomMatchers(matchers)
		if len(filtered) == 0 {
			delete(rawHooks, hookType)
		} else {
			marshaled, _ := json.Marshal(filtered)
			rawHooks[hookType] = marshaled
		}
	}
}

// updateSettingsHooks updates or removes the hooks key from rawSettings.
func updateSettingsHooks(rawSettings map[string]json.RawMessage, rawHooks map[string]json.RawMessage) {
	if len(rawHooks) > 0 {
		hooksJSON, _ := json.Marshal(rawHooks)
		rawSettings["hooks"] = hooksJSON
	} else {
		delete(rawSettings, "hooks")
	}
}

// ClaudeHooksStatus checks whether loom hooks are present in .claude/settings.json
// at the given worktree path. Returns whether hooks are installed and which
// hook commands were found.
func ClaudeHooksStatus(worktreePath string) (installed bool, hooks []string, err error) {
	settingsPath := filepath.Join(worktreePath, ".claude", claudeSettingsFile)

	data, readErr := os.ReadFile(settingsPath) //nolint:gosec
	if readErr != nil {
		return false, nil, nil // No settings file means not installed
	}

	var rawSettings map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawSettings); err != nil {
		return false, nil, fmt.Errorf("failed to parse settings.json: %w", err)
	}

	var rawHooks map[string]json.RawMessage
	if hooksRaw, ok := rawSettings["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &rawHooks); err != nil {
			return false, nil, fmt.Errorf("failed to parse hooks: %w", err)
		}
	}
	if rawHooks == nil {
		return false, nil, nil
	}

	var found []string
	for _, hookType := range managedHookTypes {
		raw, ok := rawHooks[hookType]
		if !ok {
			continue
		}
		var matchers []claudeHookMatcher
		if err := json.Unmarshal(raw, &matchers); err != nil {
			continue
		}
		for _, m := range matchers {
			for _, e := range m.Hooks {
				if strings.HasPrefix(e.Command, loomHookPrefix) {
					found = append(found, e.Command)
				}
			}
		}
	}

	return len(found) > 0, found, nil
}

// installYieldHooks adds yield-specific hook entries to rawHooks.
func installYieldHooks(rawHooks map[string]json.RawMessage) error {
	for _, entry := range yieldHookEntries {
		var matchers []claudeHookMatcher
		if data, ok := rawHooks[entry.hookType]; ok {
			if err := json.Unmarshal(data, &matchers); err != nil {
				return fmt.Errorf("failed to parse %s hooks for yield: %w", entry.hookType, err)
			}
		}

		if hasYieldGuardHook(matchers) {
			continue
		}

		matchers = append(matchers, claudeHookMatcher{
			Matcher: entry.matcher,
			Hooks: []claudeHookEntry{{
				Type:    "command",
				Command: entry.command,
			}},
		})

		data, err := json.Marshal(matchers)
		if err != nil {
			return fmt.Errorf("failed to marshal %s yield hooks: %w", entry.hookType, err)
		}
		rawHooks[entry.hookType] = data
	}
	return nil
}

// hasYieldGuardHook returns true if any matcher's hooks contain the yield-guard command.
func hasYieldGuardHook(matchers []claudeHookMatcher) bool {
	for _, m := range matchers {
		for _, e := range m.Hooks {
			if strings.Contains(e.Command, "yield-guard") {
				return true
			}
		}
	}
	return false
}

// hasLoomHook returns true if any matcher's hooks contain a loom command.
func hasLoomHook(matchers []claudeHookMatcher) bool {
	for _, m := range matchers {
		for _, e := range m.Hooks {
			if strings.HasPrefix(e.Command, loomHookPrefix) {
				return true
			}
		}
	}
	return false
}

// removeLoomMatchers filters out loom commands from each matcher's hooks,
// then removes matchers that become empty after filtering.
func removeLoomMatchers(matchers []claudeHookMatcher) []claudeHookMatcher {
	result := make([]claudeHookMatcher, 0, len(matchers))
	for _, m := range matchers {
		filteredHooks := make([]claudeHookEntry, 0, len(m.Hooks))
		for _, e := range m.Hooks {
			if !strings.HasPrefix(e.Command, loomHookPrefix) {
				filteredHooks = append(filteredHooks, e)
			}
		}
		if len(filteredHooks) > 0 {
			m.Hooks = filteredHooks
			result = append(result, m)
		}
	}
	return result
}
