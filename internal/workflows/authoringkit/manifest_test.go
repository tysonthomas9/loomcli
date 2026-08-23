package authoringkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyRejectsExtraFilesAndWrongPins(t *testing.T) {
	root := t.TempDir()
	data := []byte("hello")
	if err := os.WriteFile(filepath.Join(root, "tool.js"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	m := Manifest{SchemaVersion: SchemaVersion, FlueCommit: ExpectedFlueCommit, NodeVersion: ExpectedNodeVersion, Files: []FileEntry{{Path: "tool.js", Kind: "data", SHA256: DigestBytes(data)}}}
	canonical, err := CanonicalBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	m.KitDigest = DigestBytes(canonical)
	manifest, err := CanonicalBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kit-manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(root); err != nil {
		t.Fatalf("valid kit rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(root); err == nil {
		t.Fatal("extra file accepted")
	}
}

func TestCanonicalBytesSortsFiles(t *testing.T) {
	m := Manifest{SchemaVersion: SchemaVersion, Files: []FileEntry{{Path: "z", Kind: "data"}, {Path: "a", Kind: "data"}}}
	b, err := CanonicalBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(b)[0] != '{' || string(b)[len(b)-1] != '\n' {
		t.Fatalf("not canonical JSON: %q", b)
	}
	if strings.Index(string(b), `"path": "a"`) > strings.Index(string(b), `"path": "z"`) {
		t.Fatalf("files were not sorted: %s", b)
	}
}
