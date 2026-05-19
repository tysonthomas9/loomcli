package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreUpdatePrompt(t *testing.T) {
	store := createTestStore(t)
	session := createTestSession(t, store, "nova", "codex")

	if err := store.UpdatePrompt(session.Meta.SessionID, "updated prompt"); err != nil {
		t.Fatalf("UpdatePrompt: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(store.Dir(), session.Meta.SessionID, "prompt.txt"))
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if string(data) != "updated prompt" {
		t.Fatalf("prompt = %q", data)
	}
	if err := store.UpdatePrompt("../escape", "bad"); err == nil {
		t.Fatal("invalid session ID error = nil")
	}
	err = store.UpdatePrompt("missing-session", "bad")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing session error = %v", err)
	}
}
