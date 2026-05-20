package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncodedCWD(t *testing.T) {
	got := EncodedCWD("/Users/olehluchkiv/Work/aether/harness-wrapper")
	want := "-Users-olehluchkiv-Work-aether-harness-wrapper"
	if got != want {
		t.Errorf("EncodedCWD: got %q want %q", got, want)
	}
}

func TestReadAgainstFixture(t *testing.T) {
	dir := t.TempDir()
	cwd := "/some/work/dir"
	projDir := filepath.Join(dir, "projects", EncodedCWD(cwd))
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","message":{"role":"user","content":"hello"},"timestamp":"2026-05-14T12:00:00Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"},{"type":"text","text":"there"}]},"timestamp":"2026-05-14T12:00:05Z"}
{"type":"system"}
{"type":"attachment"}
`
	path := filepath.Join(projDir, "sess-uuid.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &Reader{ProjectsRoot: filepath.Join(dir, "projects")}
	turns, err := r.Read("sess-uuid", cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Role != "user" || turns[0].Text != "hello" {
		t.Errorf("turn 0 mismatch: %+v", turns[0])
	}
	if turns[1].Role != "assistant" || turns[1].Text != "hi\n\nthere" {
		t.Errorf("turn 1 mismatch: %+v", turns[1])
	}
}

func TestReadMissingFile(t *testing.T) {
	r := &Reader{ProjectsRoot: t.TempDir()}
	if _, err := r.Read("missing", "/no/such/dir"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// Smoke-test against the real ~/.claude/projects/ if it exists.
func TestReadAgainstRealCorpus(t *testing.T) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	// cwd is .../pkg/transcript/claudecode; walk up to repo root.
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			break
		}
		cwd = filepath.Dir(cwd)
	}
	projDir := filepath.Join(home, ".claude", "projects", EncodedCWD(cwd))
	entries, err := os.ReadDir(projDir)
	if err != nil {
		t.Skipf("no Claude Code transcripts at %s", projDir)
	}
	var sessionUUID string
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) == ".jsonl" {
			sessionUUID = name[:len(name)-len(".jsonl")]
			break
		}
	}
	if sessionUUID == "" {
		t.Skip("no .jsonl files in project dir")
	}
	if _, err := New().Read(sessionUUID, cwd); err != nil {
		t.Errorf("Read against real corpus failed: %v", err)
	}
}
