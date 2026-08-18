package domain

import (
	"strings"
	"testing"
)

// TestBuiltinWorkerPromptsRegistry guards the registry itself: every entry must
// be a distinct, non-empty, labeled team-* ID, because the ID doubles as the
// name of the embedded prompt body (internal/cli/agent/prompts/<ID>.md).
func TestBuiltinWorkerPromptsRegistry(t *testing.T) {
	prompts := BuiltinWorkerPrompts()
	if len(prompts) == 0 {
		t.Fatal("BuiltinWorkerPrompts() is empty; the worker builtin: path resolves nothing")
	}

	seen := map[string]bool{}
	for _, p := range prompts {
		if p.ID == "" {
			t.Error("registry has an entry with an empty ID")
		}
		if p.Label == "" {
			t.Errorf("worker prompt %q has no label", p.ID)
		}
		if !strings.HasPrefix(p.ID, "team-") {
			t.Errorf("worker prompt ID %q does not use the team- prefix", p.ID)
		}
		if seen[p.ID] {
			t.Errorf("worker prompt ID %q registered twice", p.ID)
		}
		seen[p.ID] = true
	}
}

// TestBuiltinWorkerPromptsAreNotInteractive is the reason this registry is a
// sibling of the interactive one rather than hidden entries inside it: a worker
// body must never be offered as a terminal-agent prompt, and an interactive ID
// must not resolve on a worker agent role's prompt_file.
func TestBuiltinWorkerPromptsAreNotInteractive(t *testing.T) {
	for _, p := range BuiltinWorkerPrompts() {
		if IsBuiltinInteractivePrompt(p.ID) {
			t.Errorf("worker prompt %q also resolves as an interactive prompt", p.ID)
		}
	}
	for _, p := range BuiltinInteractivePrompts() {
		if IsBuiltinWorkerPrompt(p.ID) {
			t.Errorf("interactive prompt %q also resolves as a worker prompt", p.ID)
		}
	}
}

func TestIsBuiltinWorkerPrompt(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"team-architect", true},
		{"team-qa", true},
		{"team-data-engineer", true},
		{"team-nope", false},
		// Interactive IDs are registered elsewhere and must not resolve here.
		{"lead", false},
		{"pr-review", false},
		// "builtin:" with nothing after it.
		{"", false},
		// IDs name embedded files, so they are case-sensitive.
		{"Team-Architect", false},
	}
	for _, tt := range tests {
		if got := IsBuiltinWorkerPrompt(tt.id); got != tt.want {
			t.Errorf("IsBuiltinWorkerPrompt(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

// TestBuiltinWorkerPromptsIsACopy makes sure a caller cannot mutate the
// registry through the slice it is handed.
func TestBuiltinWorkerPromptsIsACopy(t *testing.T) {
	prompts := BuiltinWorkerPrompts()
	original := prompts[0].ID
	prompts[0].ID = "mutated"
	if !IsBuiltinWorkerPrompt(original) {
		t.Fatalf("mutating the returned slice changed the registry: %q no longer resolves", original)
	}
}

func TestParseBuiltinPromptRef(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		wantID string
		wantOK bool
	}{
		{"worker reference", "builtin:team-architect", "team-architect", true},
		{"interactive reference", "builtin:pr-review", "pr-review", true},
		{"surrounding whitespace", "  builtin: team-qa  ", "team-qa", true},
		{"bare id after prefix", "builtin:", "", true},
		{"relative path", "./prompts/reviewer.md", "", false},
		{"absolute path", "/tmp/reviewer.md", "", false},
		{"empty", "", "", false},
		{"prefix in the middle", "prompts/builtin:team-qa.md", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotOK := ParseBuiltinPromptRef(tt.value)
			if gotID != tt.wantID || gotOK != tt.wantOK {
				t.Errorf("ParseBuiltinPromptRef(%q) = (%q, %v), want (%q, %v)", tt.value, gotID, gotOK, tt.wantID, tt.wantOK)
			}
		})
	}
}
