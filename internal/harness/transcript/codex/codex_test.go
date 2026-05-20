package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadAgainstFixture(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions", "2026", "05", "14")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"response_item","payload":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"response_item","payload":{"role":"assistant","content":[{"type":"text","text":"hi there"}]}}
{"type":"other"}
`
	path := filepath.Join(root, "rollout-2026-05-14T12-00-00-abc-def-ghi.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &Reader{SessionsRoot: filepath.Join(dir, "sessions")}
	turns, err := r.Read("abc-def-ghi", "/unused")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Role != "user" || turns[0].Text != "hello" {
		t.Errorf("turn 0 mismatch: %+v", turns[0])
	}
	if turns[1].Role != "assistant" || turns[1].Text != "hi there" {
		t.Errorf("turn 1 mismatch: %+v", turns[1])
	}
}

func TestReadMissingSessionErrors(t *testing.T) {
	r := &Reader{SessionsRoot: t.TempDir()}
	if _, err := r.Read("missing", ""); err == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestReadAgainstRealCorpusIfAvailable(t *testing.T) {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".codex", "sessions")
	if _, err := os.Stat(root); err != nil {
		t.Skip("no real ~/.codex/sessions on this machine")
	}
	// Find any one rollout-*.jsonl and parse it as a smoke test.
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(d.Name()) == ".jsonl" {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if found == "" {
		t.Skip("no Codex session files found")
	}
	// Extract UUID from filename suffix "...-<uuid>.jsonl".
	base := filepath.Base(found)
	withoutExt := base[:len(base)-len(".jsonl")]
	// Last 36 chars of withoutExt is the UUID (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx).
	if len(withoutExt) < 36 {
		t.Skipf("can't parse UUID out of %q", base)
	}
	uuid := withoutExt[len(withoutExt)-36:]
	if _, err := New().Read(uuid, ""); err != nil {
		t.Errorf("Read against real corpus failed: %v", err)
	}
}
