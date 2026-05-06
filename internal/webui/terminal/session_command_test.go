package terminal

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestArgvForSession(t *testing.T) {
	oldExecutable := currentExecutable
	currentExecutable = func() (string, error) {
		return "/Applications/Loom.app/Contents/MacOS/loom-aarch64-apple-darwin", nil
	}
	t.Cleanup(func() { currentExecutable = oldExecutable })

	leadCommand := func(backend string) []string {
		return []string{
			"-c",
			fmt.Sprintf("'/Applications/Loom.app/Contents/MacOS/loom-aarch64-apple-darwin' lead --backend %s", backend),
		}
	}

	tests := []struct {
		name    string
		session string
		want    []string
	}{
		{"lead-shell", "lead-shell-1", []string{"-l"}},
		{"lead-shell with workspace prefix", "my-ws--lead-shell-1", []string{"-l"}},
		{"lead-claude", "lead-claude-1", leadCommand("claude")},
		{"lead-codex", "lead-codex-42", leadCommand("codex")},
		{"lead-opencode", "lead-opencode-1", leadCommand("opencode")},
		{"lead-gemini", "lead-gemini-1", leadCommand("gemini")},
		{"lead-cursor", "lead-cursor-1", leadCommand("cursor")},
		{"workspace prefix + AI", "v2-refactor--lead-claude-3", leadCommand("claude")},
		{"unknown backend falls back", "lead-unknown-1", nil},
		{"non-lead session falls back", "talk-to-lead", nil},
		{"empty session falls back", "", nil},
		{"malformed session falls back", "lead-foo", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ArgvForSession(tt.session)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ArgvForSession(%q) = %v, want %v", tt.session, got, tt.want)
			}
		})
	}
}

func TestArgvForSessionFallsBackToLoomOnExecutableError(t *testing.T) {
	oldExecutable := currentExecutable
	currentExecutable = func() (string, error) {
		return "", errors.New("boom")
	}
	t.Cleanup(func() { currentExecutable = oldExecutable })

	got := ArgvForSession("lead-codex-1")
	want := []string{"-c", "'loom' lead --backend codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ArgvForSession() = %v, want %v", got, want)
	}
}

func TestArgvForSessionQuotesExecutablePath(t *testing.T) {
	oldExecutable := currentExecutable
	currentExecutable = func() (string, error) {
		return "/tmp/Loom's App/loom", nil
	}
	t.Cleanup(func() { currentExecutable = oldExecutable })

	got := ArgvForSession("lead-codex-1")
	want := []string{"-c", "'/tmp/Loom'\\''s App/loom' lead --backend codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ArgvForSession() = %v, want %v", got, want)
	}
}
