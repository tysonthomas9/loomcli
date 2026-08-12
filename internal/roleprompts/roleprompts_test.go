package roleprompts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	if !containsAll(planBody, "loom data update", "--design", "workflow host", "moves", "review") {
		t.Fatal("plan body must persist the design and leave the review transition to the workflow host")
	}
	if strings.Contains(planBody, "--status review") || strings.Contains(planBody, "--assignee=\"\"") {
		t.Fatal("plan body must not mutate lifecycle fields while its TaskRun claim is live")
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
	if !containsAll(taskBody, "LOOM_TASK_OUTCOME_FILE", `"disposition":"needs_revision"`, "workflow host", "open/unassigned") {
		t.Fatal("task body must use the typed needs-revision outcome and leave lifecycle to the workflow host")
	}
	if strings.Contains(taskBody, "--status open") || strings.Contains(taskBody, "--assignee") {
		t.Fatal("task body must not mutate status or assignee while its typed TaskRun claim is live")
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

func TestEnsurePromptFileIsExactIdempotent(t *testing.T) {
	wsDir := t.TempDir()
	const writers = 16
	var wg sync.WaitGroup
	errorsByWriter := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := EnsurePromptFile(wsDir, "reviewer", "", "READ ONLY")
			errorsByWriter <- err
		}()
	}
	wg.Wait()
	close(errorsByWriter)
	for err := range errorsByWriter {
		if err != nil {
			t.Fatalf("concurrent exact EnsurePromptFile: %v", err)
		}
	}
	first := filepath.Join(wsDir, ".loom", "prompts", "reviewer.md")
	if _, err := EnsurePromptFile(wsDir, "reviewer", "", "MUTATING"); !errors.Is(err, ErrPromptFileConflict) {
		t.Fatalf("mismatched EnsurePromptFile error = %v, want ErrPromptFileConflict", err)
	}
	data, err := os.ReadFile(first)
	if err != nil || string(data) != "READ ONLY" {
		t.Fatalf("collision overwrote prompt = %q err=%v", string(data), err)
	}
}

func TestPromptFileReceiptReportsPublicationAndReuse(t *testing.T) {
	wsDir := t.TempDir()

	owned, err := EnsurePromptFileWithReceipt(wsDir, "reviewer", "owned.md", "OWNED")
	if err != nil {
		t.Fatalf("EnsurePromptFileWithReceipt owned: %v", err)
	}
	if !owned.Created() {
		t.Fatal("new prompt receipt Created = false, want true")
	}
	if data, err := os.ReadFile(owned.Path); err != nil || string(data) != "OWNED" {
		t.Fatalf("published prompt = %q err=%v", string(data), err)
	}

	preexisting, err := EnsurePromptFileWithReceipt(wsDir, "reviewer", "shared.md", "SHARED")
	if err != nil {
		t.Fatalf("publish preexisting fixture: %v", err)
	}
	same, err := EnsurePromptFileWithReceipt(wsDir, "reviewer", "shared.md", "SHARED")
	if err != nil {
		t.Fatalf("ensure preexisting prompt: %v", err)
	}
	if !preexisting.Created() || same.Created() {
		t.Fatalf("receipt ownership = first:%v second:%v, want true/false", preexisting.Created(), same.Created())
	}
	if data, err := os.ReadFile(same.Path); err != nil || string(data) != "SHARED" {
		t.Fatalf("preexisting prompt changed = %q err=%v", string(data), err)
	}
}

func TestImmutablePromptFilenameNamespacesExplicitFilenameByRole(t *testing.T) {
	const (
		explicitFilename = "../../shared.md"
		body             = "SAME BODY"
	)
	reviewer := ImmutablePromptFilename("reviewer", explicitFilename, body)
	auditor := ImmutablePromptFilename("auditor", explicitFilename, body)

	if reviewer == auditor {
		t.Fatalf("explicit filename mapped distinct roles to shared path %q", reviewer)
	}
	if !strings.HasPrefix(reviewer, "reviewer.shared.") {
		t.Fatalf("reviewer filename = %q, want role-scoped shared stem", reviewer)
	}
	if !strings.HasPrefix(auditor, "auditor.shared.") {
		t.Fatalf("auditor filename = %q, want role-scoped shared stem", auditor)
	}

	// A delimiter alone is not a namespace: these two role/name pairs would
	// both flatten to a.b.shared without the role-identity digest.
	ambiguousA := ImmutablePromptFilename("a", "b.shared.md", body)
	ambiguousB := ImmutablePromptFilename("a.b", "shared.md", body)
	if ambiguousA == ambiguousB {
		t.Fatalf("dot-delimited role namespace collided at %q", ambiguousA)
	}
}
