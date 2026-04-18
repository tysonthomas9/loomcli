package terminal

import (
	"reflect"
	"testing"
)

func TestArgvForSession(t *testing.T) {
	tests := []struct {
		name    string
		session string
		want    []string
	}{
		{"lead-shell", "lead-shell-1", []string{"-l"}},
		{"lead-shell with workspace prefix", "my-ws--lead-shell-1", []string{"-l"}},
		{"lead-claude", "lead-claude-1", []string{"-c", "loom lead --backend claude"}},
		{"lead-codex", "lead-codex-42", []string{"-c", "loom lead --backend codex"}},
		{"lead-opencode", "lead-opencode-1", []string{"-c", "loom lead --backend opencode"}},
		{"lead-gemini", "lead-gemini-1", []string{"-c", "loom lead --backend gemini"}},
		{"lead-cursor", "lead-cursor-1", []string{"-c", "loom lead --backend cursor"}},
		{"workspace prefix + AI", "v2-refactor--lead-claude-3", []string{"-c", "loom lead --backend claude"}},
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
