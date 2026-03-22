package cli

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
var managedHookTypes = []string{
	"SessionStart",
	"UserPromptSubmit",
	"Stop",
	"SessionEnd",
}

// hookCommands maps each managed hook type to its loom command.
var hookCommands = map[string]string{
	"SessionStart":     "loom hooks claude-code session-start",
	"UserPromptSubmit": "loom hooks claude-code user-prompt-submit",
	"Stop":             "loom hooks claude-code stop",
	"SessionEnd":       "loom hooks claude-code session-end",
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

	// Ensure .claude/ directory exists
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("failed to create .claude directory: %w", err)
	}

	// Read existing settings (or start fresh)
	var rawSettings map[string]json.RawMessage
	existingData, readErr := os.ReadFile(settingsPath) //nolint:gosec
	if readErr == nil {
		if err := json.Unmarshal(existingData, &rawSettings); err != nil {
			return fmt.Errorf("failed to parse existing settings.json: %w", err)
		}
	} else {
		rawSettings = make(map[string]json.RawMessage)
	}

	// Parse hooks key preserving unknown hook types
	var rawHooks map[string]json.RawMessage
	if hooksRaw, ok := rawSettings["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &rawHooks); err != nil {
			return fmt.Errorf("failed to parse hooks in settings.json: %w", err)
		}
	}
	if rawHooks == nil {
		rawHooks = make(map[string]json.RawMessage)
	}

	// For each managed hook type, parse its array of matchers, check for
	// existing loom entry, and append if missing.
	for _, hookType := range managedHookTypes {
		var matchers []claudeHookMatcher
		if data, ok := rawHooks[hookType]; ok {
			if err := json.Unmarshal(data, &matchers); err != nil {
				return fmt.Errorf("failed to parse %s hooks: %w", hookType, err)
			}
		}

		// Check if a loom hook already exists in any matcher (prefix match for idempotency)
		if hasLoomHook(matchers) {
			continue
		}

		// Append a new matcher with the loom hook entry
		cmd := hookCommands[hookType]
		matchers = append(matchers, claudeHookMatcher{
			Matcher: "",
			Hooks: []claudeHookEntry{{
				Type:    "command",
				Command: cmd,
			}},
		})

		// Marshal back into rawHooks
		data, err := json.Marshal(matchers)
		if err != nil {
			return fmt.Errorf("failed to marshal %s hooks: %w", hookType, err)
		}
		rawHooks[hookType] = data
	}

	// Marshal hooks back into rawSettings
	hooksJSON, err := json.Marshal(rawHooks)
	if err != nil {
		return fmt.Errorf("failed to marshal hooks: %w", err)
	}
	rawSettings["hooks"] = hooksJSON

	// Write back with indentation and trailing newline
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

	var rawHooks map[string]json.RawMessage
	if hooksRaw, ok := rawSettings["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &rawHooks); err != nil {
			return fmt.Errorf("failed to parse hooks: %w", err)
		}
	}
	if rawHooks == nil {
		return nil // No hooks section, nothing to remove
	}

	// For each managed hook type, remove loom entries from matchers
	for _, hookType := range managedHookTypes {
		data, ok := rawHooks[hookType]
		if !ok {
			continue
		}

		var matchers []claudeHookMatcher
		if err := json.Unmarshal(data, &matchers); err != nil {
			continue // Skip unparseable hook types
		}

		filtered := removeLoomMatchers(matchers)
		if len(filtered) == 0 {
			delete(rawHooks, hookType)
		} else {
			marshaled, err := json.Marshal(filtered)
			if err != nil {
				return fmt.Errorf("failed to marshal %s hooks: %w", hookType, err)
			}
			rawHooks[hookType] = marshaled
		}
	}

	// Update or remove hooks from settings
	if len(rawHooks) > 0 {
		hooksJSON, err := json.Marshal(rawHooks)
		if err != nil {
			return fmt.Errorf("failed to marshal hooks: %w", err)
		}
		rawSettings["hooks"] = hooksJSON
	} else {
		delete(rawSettings, "hooks")
	}

	// Write back
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
