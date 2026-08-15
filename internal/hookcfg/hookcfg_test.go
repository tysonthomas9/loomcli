package hookcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureCreatesFreshBackendFiles(t *testing.T) {
	t.Parallel()

	for _, backend := range []string{"claude", "codex"} {
		backend := backend
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			workDir := t.TempDir()
			if err := Ensure(workDir, backend, []HookSpec{{
				Event: UserPromptSubmit, Command: "loom skill materialize",
			}}); err != nil {
				t.Fatalf("Ensure() error = %v", err)
			}

			path := configPath(workDir, backend)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertManagedCommands(t, data, map[string][]string{
				"UserPromptSubmit": {"loom skill materialize"},
			})
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Fatalf("mode = %o, want 600", got)
			}
		})
	}
}

func TestEnsureMergesUserHooksAndPreservesUnrelatedSettings(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	path := configPath(workDir, "claude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{
  "permissions": {"allow": ["Read", "Bash"]},
  "hooks": {
    "UserPromptSubmit": [{"matcher":"","hooks":[{"type":"command","command":"user-pre-turn"},{"type":"command","command":"loom old-command"}]}],
    "Stop": [{"matcher":"","hooks":[{"type":"command","command":"user-stop"}]}]
  },
  "custom": {"nested": true}
}
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Ensure(workDir, "claude", []HookSpec{{
		Event: UserPromptSubmit, Command: "loom skill materialize",
	}}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertCommands(t, data, map[string][]string{
		"UserPromptSubmit": {"user-pre-turn", "loom skill materialize"},
		"Stop":             {"user-stop"},
	})
	text := string(data)
	if strings.Index(text, `"permissions"`) > strings.Index(text, `"hooks"`) ||
		strings.Index(text, `"hooks"`) > strings.Index(text, `"custom"`) {
		t.Fatalf("top-level field order changed:\n%s", text)
	}
	if !strings.Contains(text, `"allow": [`) || !strings.Contains(text, `"nested": true`) {
		t.Fatalf("unrelated settings were not preserved:\n%s", text)
	}
}

func TestEnsureIdempotentWithoutRewrite(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	specs := []HookSpec{{Event: UserPromptSubmit, Command: "loom skill materialize"}}
	path := configPath(workDir, "codex")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Deliberately differs from Ensure's formatting. Semantic idempotence must
	// preserve a file that already expresses the desired managed state.
	original := []byte(`{"custom":{"keep":true},"hooks":{"UserPromptSubmit":[{"hooks":[{"command":"loom skill materialize","type":"command"}]}]}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oldTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	if err := Ensure(workDir, "codex", specs); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("second Ensure changed content\nbefore: %s\nafter: %s", before, after)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(oldTime) {
		t.Fatalf("second Ensure changed mtime: got %v, want %v", info.ModTime(), oldTime)
	}
}

func TestEnsureMalformedJSONDoesNotClobber(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	path := configPath(workDir, "claude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"hooks": definitely-not-json}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	err := Ensure(workDir, "claude", []HookSpec{{Event: UserPromptSubmit, Command: "loom skill materialize"}})
	if err == nil {
		t.Fatal("Ensure() error = nil, want malformed JSON error")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(original) {
		t.Fatalf("malformed file was clobbered: got %q, want %q", after, original)
	}
}

func TestEnsureRemovesManagedHookForUnspecifiedEvent(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	path := configPath(workDir, "codex")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{
  "hooks": {
    "Stop": [{"matcher":"","hooks":[{"type":"command","command":"loom transcript save"},{"type":"command","command":"user-stop"}]}],
    "SessionEnd": [{"matcher":"","hooks":[{"type":"command","command":"loom transcript finish"}]}]
  }
}
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Ensure(workDir, "codex", []HookSpec{{
		Event: UserPromptSubmit, Command: "loom skill materialize",
	}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertCommands(t, data, map[string][]string{
		"Stop":             {"user-stop"},
		"SessionEnd":       {},
		"UserPromptSubmit": {"loom skill materialize"},
	})
}

func configPath(workDir, backend string) string {
	adapter := backendAdapters[backend]
	return filepath.Join(workDir, adapter.dirName, adapter.fileName)
}

func assertManagedCommands(t *testing.T, data []byte, want map[string][]string) {
	t.Helper()
	all := readCommands(t, data)
	managed := make(map[string][]string)
	for event, commands := range all {
		for _, command := range commands {
			if strings.HasPrefix(command, loomCommandPrefix) {
				managed[event] = append(managed[event], command)
			}
		}
	}
	assertCommandMap(t, managed, want)
}

func assertCommands(t *testing.T, data []byte, want map[string][]string) {
	t.Helper()
	assertCommandMap(t, readCommands(t, data), want)
}

func assertCommandMap(t *testing.T, got, want map[string][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
	for event, wantCommands := range want {
		gotCommands, ok := got[event]
		if !ok {
			t.Fatalf("commands missing event %s: %#v", event, got)
		}
		if strings.Join(gotCommands, "\x00") != strings.Join(wantCommands, "\x00") {
			t.Fatalf("commands[%s] = %#v, want %#v", event, gotCommands, wantCommands)
		}
	}
}

func readCommands(t *testing.T, data []byte) map[string][]string {
	t.Helper()
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	commands := make(map[string][]string, len(config.Hooks))
	for event, groups := range config.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				commands[event] = append(commands[event], hook.Command)
			}
		}
		if _, ok := commands[event]; !ok {
			commands[event] = []string{}
		}
	}
	return commands
}
