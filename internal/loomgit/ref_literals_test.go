package loomgit

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRefLiteralsOnlyInLayout(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFS.Close()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := rootFS.ReadFile(rel)
		if err != nil {
			return err
		}
		for lineNo, line := range strings.Split(string(body), "\n") {
			if !strings.Contains(line, `"refs/loom/`) && !strings.Contains(line, `"loom/ws/`) {
				continue
			}
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "internal/loomgit/internal/layout/") {
				continue
			}
			t.Errorf("ref literal outside layout: %s:%d: %s", rel, lineNo+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
