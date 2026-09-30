package retention

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDriverTaskCopyWritesUseLeases(t *testing.T) {
	driverDir := filepath.Join("..", "..", "driver")
	entries, err := os.ReadDir(driverDir)
	if err != nil {
		t.Fatal(err)
	}
	unsafeWrite := regexp.MustCompile(`agentcapture\.Capture\s*\(|gitexec\.New\s*\(`)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || len(entry.Name()) >= 8 && entry.Name()[len(entry.Name())-8:] == "_test.go" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(driverDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if unsafeWrite.Match(content) {
			t.Errorf("%s contains an unleased task-copy write", entry.Name())
		}
	}
	root, err := os.OpenRoot(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := fs.ReadFile(root.FS(), path)
		if err != nil {
			return err
		}
		if regexp.MustCompile(`agentcapture\.Capture\s*\(`).Match(content) {
			t.Errorf("%s calls capture without the task-copy lease", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
