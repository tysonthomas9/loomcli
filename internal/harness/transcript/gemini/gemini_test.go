package gemini

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const (
	testSessionID = "0281fd4a-0a10-4dfe-adca-9b61b3777255"
	testShort     = "0281fd4a"
)

func writeProjectsJSON(t *testing.T, root, workingDir, slug string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"projects": map[string]string{workingDir: slug},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "projects.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSessionFile(t *testing.T, root, slug, filename, body string) string {
	t.Helper()
	dir := filepath.Join(root, "tmp", slug, "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Shape A: Gemini API-style {"role":"user","parts":[{"text":"..."}]}
func TestReadParsesAPIShape(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/Work/aether"
	writeProjectsJSON(t, root, cwd, "aether")

	body := `{"sessionId":"` + testSessionID + `","projectHash":"abc","startTime":"2026-05-14T10:00:00Z","kind":"main"}
{"role":"user","parts":[{"text":"hello"}],"timestamp":"2026-05-14T10:00:01Z"}
{"role":"model","parts":[{"text":"hi"},{"text":"there"}],"timestamp":"2026-05-14T10:00:02Z"}
`
	writeSessionFile(t, root, "aether", "session-2026-05-14T10-00-"+testShort+".jsonl", body)

	r := &Reader{GeminiRoot: root}
	turns, err := r.Read(testSessionID, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d: %+v", len(turns), turns)
	}
	if turns[0].Role != "user" || turns[0].Text != "hello" {
		t.Errorf("turn 0 mismatch: %+v", turns[0])
	}
	if turns[1].Role != "assistant" || turns[1].Text != "hi\n\nthere" {
		t.Errorf("turn 1 mismatch: %+v", turns[1])
	}
}

// Shape B: CLI-internal {"type":"user","message":"..."}
func TestReadParsesTypeMessageShape(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/Work/aether"
	writeProjectsJSON(t, root, cwd, "aether")

	body := `{"sessionId":"` + testSessionID + `","kind":"main"}
{"type":"user","message":"prompt","timestamp":"2026-05-14T10:00:01Z"}
{"type":"assistant","message":"answer","timestamp":"2026-05-14T10:00:02Z"}
`
	writeSessionFile(t, root, "aether", "session-2026-05-14T10-00-"+testShort+".jsonl", body)

	r := &Reader{GeminiRoot: root}
	turns, err := r.Read(testSessionID, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d: %+v", len(turns), turns)
	}
	if turns[0].Text != "prompt" || turns[1].Text != "answer" {
		t.Errorf("turn mismatch: %+v", turns)
	}
}

// Walk fallback: projects.json doesn't map the working dir, but the
// session file is present under some other slug. The reader should
// find it by walking ~/.gemini/tmp/*/chats/.
func TestReadWalkFallback(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/Work/unmapped"

	body := `{"sessionId":"` + testSessionID + `","kind":"main"}
{"role":"user","parts":[{"text":"hi"}]}
`
	writeSessionFile(t, root, "some-other-slug", "session-2026-05-14T10-00-"+testShort+".jsonl", body)

	r := &Reader{GeminiRoot: root}
	turns, err := r.Read(testSessionID, cwd)
	if err != nil {
		t.Fatalf("walk fallback: %v", err)
	}
	if len(turns) != 1 || turns[0].Text != "hi" {
		t.Errorf("unexpected turns: %+v", turns)
	}
}

// Concurrent-session safety: two files share the same short suffix but
// carry different sessionId values in their headers. The reader must
// pick the one matching the requested UUID.
func TestReadDisambiguatesByHeader(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/Work/aether"
	writeProjectsJSON(t, root, cwd, "aether")

	wantID := "0281fd4a-0000-0000-0000-000000000001"
	otherID := "0281fd4a-0000-0000-0000-000000000002"

	writeSessionFile(t, root, "aether",
		"session-2026-05-14T10-00-"+testShort+".jsonl",
		`{"sessionId":"`+otherID+`","kind":"main"}
{"role":"user","parts":[{"text":"wrong"}]}
`)
	// File system iteration order is not guaranteed, so write a second
	// candidate with a distinguishable timestamp suffix.
	writeSessionFile(t, root, "aether",
		"session-2026-05-14T11-00-"+testShort+".jsonl",
		`{"sessionId":"`+wantID+`","kind":"main"}
{"role":"user","parts":[{"text":"right"}]}
`)

	r := &Reader{GeminiRoot: root}
	turns, err := r.Read(wantID, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Text != "right" {
		t.Errorf("expected to read the file matching wantID, got: %+v", turns)
	}
}

func TestReadEmptySessionID(t *testing.T) {
	r := New()
	if _, err := r.Read("", "/some/dir"); err == nil {
		t.Fatal("expected error for empty session id")
	}
}

func TestReadMissingFile(t *testing.T) {
	r := &Reader{GeminiRoot: t.TempDir()}
	if _, err := r.Read(testSessionID, "/no/such/dir"); err == nil {
		t.Fatal("expected error for missing session file")
	}
}

func TestSessionShort(t *testing.T) {
	cases := map[string]string{
		"0281fd4a-0a10-4dfe-adca-9b61b3777255": "0281fd4a",
		"abcd1234":                             "abcd1234",
		"abc":                                  "abc",
		"":                                     "",
	}
	for in, want := range cases {
		if got := sessionShort(in); got != want {
			t.Errorf("sessionShort(%q) = %q, want %q", in, got, want)
		}
	}
}

// Smoke-test against ~/.gemini if present, walking up to repo root for
// CWD context.
func TestReadAgainstRealCorpus(t *testing.T) {
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".gemini")); err != nil {
		t.Skip("no ~/.gemini on this machine")
	}
	cwd, _ := os.Getwd()
	for range 6 {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			break
		}
		cwd = filepath.Dir(cwd)
	}
	r := New()
	slug, ok, err := r.ProjectSlug(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skip("no gemini project mapping for repo root")
	}
	chats := filepath.Join(home, ".gemini", "tmp", slug, "chats")
	entries, err := os.ReadDir(chats)
	if err != nil || len(entries) == 0 {
		t.Skip("no gemini chats on disk")
	}
	// Find any session-… file, parse its header, and round-trip it.
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(chats, e.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		var hdr struct {
			SessionID string `json:"sessionId"`
		}
		dec := json.NewDecoder(f)
		_ = dec.Decode(&hdr)
		f.Close()
		if hdr.SessionID == "" {
			continue
		}
		if _, err := r.Read(hdr.SessionID, cwd); err != nil {
			t.Errorf("Read against real gemini corpus failed for %s: %v", hdr.SessionID, err)
		}
		return
	}
	t.Skip("no session file with parseable header")
}
