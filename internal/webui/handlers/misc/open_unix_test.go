//go:build !windows && !wasm

package misc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenLogFileSecureUnixBranches(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")
	if err := os.WriteFile(logPath, []byte("hello"), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	f, err := OpenLogFileSecure(logPath, dir)
	if err != nil {
		t.Fatalf("OpenLogFileSecure regular file: %v", err)
	}
	_ = f.Close()

	linkPath := filepath.Join(dir, "link.log")
	if err := os.Symlink(logPath, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := OpenLogFileSecure(linkPath, dir); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("OpenLogFileSecure symlink err = %v", err)
	}
	if _, err := OpenLogFileSecure(filepath.Join(dir, "missing.log"), dir); err == nil {
		t.Fatal("OpenLogFileSecure missing file err = nil")
	}
}
