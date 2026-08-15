//go:build unix

package skillmat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestUnixSecureRootCreateFileRefusesSymlinkedParent(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(target, "safe"), 0o755); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("create outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "safe", "link")); err != nil {
		t.Fatalf("plant parent symlink: %v", err)
	}
	root, err := openSecureRoot(target)
	if err != nil {
		t.Fatalf("openSecureRoot: %v", err)
	}
	defer root.Close()

	err = root.CreateFile("safe/link/escaped", []byte("nope"), 0o755)
	if err == nil || !strings.Contains(err.Error(), "refusing to follow symlink") {
		t.Fatalf("CreateFile error = %v, want fd-relative symlink refusal", err)
	}
	if _, statErr := os.Lstat(filepath.Join(outside, "escaped")); !os.IsNotExist(statErr) {
		t.Fatalf("secure writer escaped through planted parent symlink: %v", statErr)
	}
}

func TestUnixSecureRootReadDirRefusesSymlinkedDirectory(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(target, "safe"), 0o755); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("create outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "safe", "link")); err != nil {
		t.Fatalf("plant directory symlink: %v", err)
	}
	root, err := openSecureRoot(target)
	if err != nil {
		t.Fatalf("openSecureRoot: %v", err)
	}
	defer root.Close()

	_, err = root.ReadDir("safe/link")
	if err == nil || !strings.Contains(err.Error(), "refusing to follow symlink") {
		t.Fatalf("ReadDir error = %v, want fd-relative symlink refusal", err)
	}
}

func TestUnixSecureRootRejectsFIFOMarkerWithoutBlocking(t *testing.T) {
	target := t.TempDir()
	markerPath := filepath.Join(target, filepath.FromSlash(MarkerPath))
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		t.Fatalf("create marker parent: %v", err)
	}
	if err := unix.Mkfifo(markerPath, 0o600); err != nil {
		t.Fatalf("create marker FIFO: %v", err)
	}
	root, err := openSecureRoot(target)
	if err != nil {
		t.Fatalf("openSecureRoot: %v", err)
	}
	defer root.Close()

	done := make(chan error, 1)
	go func() {
		_, _, readErr := root.ReadFile(MarkerPath, maxMarkerBytes)
		done <- readErr
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("ReadFile error = %v, want non-regular marker refusal", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadFile blocked while opening FIFO marker")
	}
}
