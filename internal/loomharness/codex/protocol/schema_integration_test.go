package protocol

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// TestSchemaMatchesInstalled regenerates the schema with the installed codex
// and fails when any pinned file (a type the adapter uses) changed.
func TestSchemaMatchesInstalled(t *testing.T) {
	if os.Getenv("LOOM_REAL_CODEX") != "1" {
		t.Skip("set LOOM_REAL_CODEX=1 to check the pinned schema against the installed codex")
	}
	bin := os.Getenv("LOOM_CODEX_BIN")
	if bin == "" {
		bin = "codex"
	}
	out := t.TempDir()
	cmd := exec.Command(bin, "app-server", "generate-json-schema", "--experimental", "--out", out) //nolint:gosec // G204: the codex binary under test.
	cmd.Env = append(os.Environ(), "CODEX_HOME="+t.TempDir())                                      // never the user's ~/.codex
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate-json-schema: %v\n%s", err, b)
	}
	n := 0
	err := filepath.WalkDir("schema", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel("schema", path)
		n++
		pinned, installed := readJSON(t, path), readJSON(t, filepath.Join(out, rel))
		if !reflect.DeepEqual(pinned, installed) {
			t.Errorf("%s changed in the installed codex; review the change, re-pin and go generate", rel)
		}
		return nil
	})
	if err != nil || n == 0 {
		t.Fatalf("walk pinned schema: %v (%d files)", err, n)
	}
	t.Logf("%d pinned schema files match the installed codex", n)
}

func readJSON(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}
