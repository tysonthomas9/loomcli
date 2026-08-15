package roleprompts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishIsContentAddressedIdempotentAndNoClobber(t *testing.T) {
	workspace := t.TempDir()
	first, err := Publish(workspace, "reviewer", "first body")
	if err != nil {
		t.Fatalf("Publish first: %v", err)
	}
	if !filepath.IsAbs(first) || !strings.HasPrefix(filepath.Base(first), "reviewer.") {
		t.Fatalf("published path = %q", first)
	}
	again, err := Publish(workspace, "reviewer", "first body")
	if err != nil || again != first {
		t.Fatalf("Publish identical = %q, %v; want %q", again, err, first)
	}
	second, err := Publish(workspace, "reviewer", "second body")
	if err != nil {
		t.Fatalf("Publish second: %v", err)
	}
	if second == first {
		t.Fatalf("different content reused %q", first)
	}
	firstBody, err := os.ReadFile(first)
	if err != nil || string(firstBody) != "first body" {
		t.Fatalf("first immutable file = %q, %v", firstBody, err)
	}

	if err := os.WriteFile(first, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper fixture: %v", err)
	}
	if _, err := Publish(workspace, "reviewer", "first body"); !errors.Is(err, ErrPromptFileConflict) {
		t.Fatalf("Publish collision error = %v, want ErrPromptFileConflict", err)
	}
	unchanged, _ := os.ReadFile(first)
	if string(unchanged) != "tampered" {
		t.Fatalf("collision rewrote existing file to %q", unchanged)
	}
}

func TestReadValidatedContainment(t *testing.T) {
	workspace := t.TempDir()
	prompt, err := Publish(workspace, "worker", "safe body")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	body, err := ReadValidated(workspace, prompt)
	if err != nil || body != "safe body" {
		t.Fatalf("ReadValidated = %q, %v", body, err)
	}

	external := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(external, []byte("do not disclose"), 0o600); err != nil {
		t.Fatalf("write external fixture: %v", err)
	}
	if body, err := ReadValidated(workspace, external); !errors.Is(err, ErrExternal) || body != "" {
		t.Fatalf("external read = %q, %v; want typed rejection", body, err)
	}

	link := filepath.Join(workspace, "escape.md")
	if err := os.Symlink(external, link); err != nil {
		t.Fatalf("symlink fixture: %v", err)
	}
	if body, err := ReadValidated(workspace, link); !errors.Is(err, ErrExternal) || body != "" {
		t.Fatalf("symlink read = %q, %v; want typed rejection", body, err)
	}
}
