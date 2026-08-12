package roleprompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultBodiesHonorTSContract locks the prompt-agent → local-task-runner
// contract into the embedded bodies: the task is pre-claimed and the worktree is
// prepared, so the bodies must PROHIBIT the self-claim loop and stacked publish
// that the Go daemon templates use as imperative steps, while the planner still
// persists its design.
func TestDefaultBodiesHonorTSContract(t *testing.T) {
	for _, name := range BuiltinPromptRoleNames() {
		body, ok := DefaultPromptBody(name)
		if !ok || strings.TrimSpace(body) == "" {
			t.Fatalf("role %q has no default prompt body", name)
		}
		// The bodies must reference the self-claim commands only inside a
		// prohibition ("do NOT run ... loom data ready ... loom data claim").
		if !containsAll(body, "do NOT run", "loom data ready", "loom data claim") {
			t.Fatalf("role %q body must prohibit the self-claim loop, not perform it", name)
		}
	}

	planBody, _ := DefaultPromptBody("plan")
	if !containsAll(planBody, "loom data update", "--design", "--status review") {
		t.Fatal("plan body must persist the design and move the card to review via loom data update")
	}
	if !containsAll(planBody, "Do NOT", "loom stack publish") {
		t.Fatal("plan body must prohibit stacked publish")
	}

	taskBody, _ := DefaultPromptBody("task")
	if !strings.Contains(taskBody, "git commit") {
		t.Fatal("task body must commit locally")
	}
	// Delivery is the runner's job: the coder body must prohibit push/publish and
	// prohibit closing the task itself.
	if !containsAll(taskBody, "do NOT", "git push", "loom stack publish") {
		t.Fatal("task body must prohibit push and stacked publish (delivery is the runner's job)")
	}
	if !containsAll(taskBody, "Do NOT run", "loom data close") {
		t.Fatal("task body must prohibit closing the task itself")
	}
}

func containsAll(body string, subs ...string) bool {
	for _, s := range subs {
		if !strings.Contains(body, s) {
			return false
		}
	}
	return true
}

// TestDefaultPromptBodyUnknownRole confirms roles without a default body (lead,
// custom) report absent rather than an empty string that looks materialized.
func TestDefaultPromptBodyUnknownRole(t *testing.T) {
	if _, ok := DefaultPromptBody("lead"); ok {
		t.Fatal("lead unexpectedly has a default body")
	}
	if HasDefaultPromptBody("custom-role") {
		t.Fatal("custom-role unexpectedly reports a default body")
	}
}

// TestWritePromptFilePlacesUnderPromptsDir verifies the shared writer lands the
// body under <ws>/.loom/prompts and sanitizes a traversal filename to its base.
func TestWritePromptFilePlacesUnderPromptsDir(t *testing.T) {
	wsDir := t.TempDir()
	path, err := WritePromptFile(wsDir, "plan", "", "BODY")
	if err != nil {
		t.Fatalf("WritePromptFile: %v", err)
	}
	want := filepath.Join(wsDir, ".loom", "prompts", "plan.md")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "BODY" {
		t.Fatalf("read back = %q err=%v", string(data), err)
	}

	// A traversal filename is reduced to its base, staying inside the dir.
	evil, err := WritePromptFile(wsDir, "plan", "../../escape.md", "X")
	if err != nil {
		t.Fatalf("WritePromptFile traversal: %v", err)
	}
	if filepath.Dir(evil) != filepath.Join(wsDir, ".loom", "prompts") {
		t.Fatalf("traversal filename escaped prompts dir: %q", evil)
	}
}
