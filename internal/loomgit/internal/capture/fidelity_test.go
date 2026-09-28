package capture

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

func TestCaptureWithHostGitDefaults(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir) //nolint:norawexec // Creates an isolated repository for the production-default runner.
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	runner, err := gitexec.New(dir, gitexec.Options{})
	if err != nil {
		t.Skipf("host Git identity unavailable: %v", err)
	}
	write(t, dir, "base.txt", "base")
	must(t, runner, "add", "base.txt")
	must(t, runner, "commit", "-qm", "base")
	write(t, dir, "base.txt", "edited")
	result := capture(t, dir, runner)
	if !result.Manifest.Complete || result.CaptureSHA == "" {
		t.Fatalf("default capture: %+v", result)
	}
}

func TestNestedRepositoryIsNamedAndNeverBecomesGitlink(t *testing.T) {
	dir, runner := fixture(t)
	nested := filepath.Join(dir, "vendor", "dependency")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q", nested) //nolint:norawexec // Creates a real nested Git repository fixture.
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %s: %v", out, err)
	}
	write(t, dir, "vendor/dependency/source.go", "package dependency")
	result := capture(t, dir, runner)
	entry := classes(result.Manifest.Entries)["vendor/dependency/"]
	if result.Manifest.Complete || entry.Class != Incomplete || !strings.Contains(entry.Reason, "nested") {
		t.Fatalf("nested repository: %+v", result.Manifest)
	}
	if result.CaptureSHA != "" {
		t.Fatalf("nested repository became a capture commit: %s", result.CaptureSHA)
	}
}

func TestIgnoredNestedRepositoryDoesNotMakeCaptureIncomplete(t *testing.T) {
	dir, runner := fixture(t)
	write(t, dir, ".gitignore", "node_modules/\n")
	nested := filepath.Join(dir, "node_modules", "vendored")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q", nested) //nolint:norawexec // Creates an ignored nested repository fixture.
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %s: %v", out, err)
	}
	write(t, dir, "node_modules/vendored/source.go", "package vendored")
	result := capture(t, dir, runner)
	if !result.Manifest.Complete || result.CaptureSHA == "" {
		t.Fatalf("ignored nested repository affected capture: %+v", result.Manifest)
	}
	if _, ok := classes(result.Manifest.Entries)["node_modules/vendored/"]; ok {
		t.Fatalf("ignored nested repository was inventoried: %+v", result.Manifest)
	}
}

func TestIgnoredExtendedAttributeDoesNotMakeCaptureIncomplete(t *testing.T) {
	dir, runner := fixture(t)
	write(t, dir, ".gitignore", "node_modules/\n")
	write(t, dir, "node_modules/ignored.txt", "content")
	full := filepath.Join(dir, "node_modules", "ignored.txt")
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("xattr", "-w", "com.example.capture", "value", full) //nolint:norawexec // Marks an ignored fixture file with an xattr.
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("set xattr: %s: %v", out, err)
		}
	case "linux":
		if err := unix.Lsetxattr(full, "user.capture-test", []byte("value"), 0); err != nil {
			t.Skipf("user xattrs unavailable: %v", err)
		}
	default:
		t.Skip("xattr fixture unavailable")
	}
	result := capture(t, dir, runner)
	if !result.Manifest.Complete || result.CaptureSHA == "" {
		t.Fatalf("ignored xattr affected capture: %+v", result.Manifest)
	}
}

func TestFIFOAndEmptyDirectoryAreManifested(t *testing.T) {
	dir, runner := fixture(t)
	if err := os.Mkdir(filepath.Join(dir, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	result := capture(t, dir, runner)
	entries := classes(result.Manifest.Entries)
	if entries["empty/"].Class != Listed || entries["pipe"].Class != Incomplete || result.Manifest.Complete {
		t.Fatalf("special files: %+v", result.Manifest)
	}
}

func TestExtendedAttributesAreNamedIncomplete(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS xattr fixture")
	}
	dir, runner := fixture(t)
	write(t, dir, "with-attribute.txt", "content")
	cmd := exec.Command("xattr", "-w", "com.example.capture", "value", filepath.Join(dir, "with-attribute.txt")) //nolint:norawexec // Sets an extended attribute on an isolated test fixture.
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("set xattr: %s: %v", out, err)
	}
	result := capture(t, dir, runner)
	entry := classes(result.Manifest.Entries)["with-attribute.txt"]
	if result.Manifest.Complete || entry.Class != Incomplete || !strings.Contains(entry.Reason, "extended attributes") {
		t.Fatalf("xattr: %+v", result.Manifest)
	}
}

func TestPostWriteChangeIsNamed(t *testing.T) {
	dir, runner := fixture(t)
	write(t, dir, "edit.txt", "before")
	original := syncObjectFile
	t.Cleanup(func() { syncObjectFile = original })
	changed := false
	syncObjectFile = func(path string) error {
		if !changed {
			changed = true
			write(t, dir, "edit.txt", "after")
		}
		return original(path)
	}
	result := capture(t, dir, runner)
	if !changed || result.Manifest.Complete || classes(result.Manifest.Entries)["edit.txt"].Class != Incomplete {
		t.Fatalf("post-write change: %+v", result.Manifest)
	}
}

func TestObjectSyncPrecedesRefAndSyncFailureLeavesRef(t *testing.T) {
	dir, runner := fixture(t)
	write(t, dir, "edit.txt", "first")
	first := capture(t, dir, runner)
	write(t, dir, "edit.txt", "second")
	original := syncObjectFile
	t.Cleanup(func() { syncObjectFile = original })
	called := 0
	syncObjectFile = func(path string) error {
		called++
		if got := must(t, runner, "rev-parse", first.CaptureRef); got != first.CaptureSHA {
			t.Fatalf("ref moved before fsync: %s", got)
		}
		if err := original(path); err != nil {
			return err
		}
		return syscall.ENOSPC
	}
	_, err := Capture(context.Background(), runner, dir, Params{Workspace: "ws", Attempt: "a1", TaskID: "task-1", TaskTitle: "Build capture"})
	if !errors.Is(err, syscall.ENOSPC) || called == 0 {
		t.Fatalf("fsync failure: calls=%d err=%v", called, err)
	}
	if got := must(t, runner, "rev-parse", first.CaptureRef); got != first.CaptureSHA {
		t.Fatalf("ref changed after ENOSPC: %s", got)
	}
}

func TestRandomCaptureRoundTrip(t *testing.T) {
	seed := int64(1337)
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // Repeatable fixture data, not cryptographic randomness.
	for trial := range 5 {
		t.Run(fmt.Sprintf("tree-%d", trial), func(t *testing.T) {
			dir, runner := fixture(t)
			paths := make(map[string][]byte)
			for i := range 12 {
				path := fmt.Sprintf("目录/%d-α.bin", i)
				size := rng.Intn(4096) + 1
				if trial == 0 && i == 0 {
					size = int(MaxFileBytes)
				}
				data := make([]byte, size)
				_, _ = rng.Read(data)
				paths[path] = data
				full := filepath.Join(dir, filepath.FromSlash(path))
				if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
					t.Fatal(err)
				}
				mode := os.FileMode(0600)
				if i%2 == 0 {
					mode = 0700
				}
				if err := os.WriteFile(full, data, mode); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("0-α.bin", filepath.Join(dir, "目录", "link")); err != nil {
				t.Fatal(err)
			}
			result := capture(t, dir, runner)
			if !result.Manifest.Complete || result.CaptureSHA == "" {
				t.Fatalf("round trip capture: %+v", result.Manifest)
			}
			checkout := filepath.Join(t.TempDir(), "restore")
			must(t, runner, "worktree", "add", "--detach", checkout, result.CaptureSHA)
			manifest := classes(result.Manifest.Entries)
			for path, want := range paths {
				got, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(path)))
				if err != nil || string(got) != string(want) || manifest[path].Class != Captured {
					t.Fatalf("%s: bytes or manifest differ: %v", path, err)
				}
				info, err := os.Stat(filepath.Join(checkout, filepath.FromSlash(path)))
				var index int
				_, _ = fmt.Sscanf(filepath.Base(path), "%d-α.bin", &index)
				if err != nil || (info.Mode().Perm()&0111 != 0) != (index%2 == 0) {
					t.Fatalf("%s: executable bit differs: %v", path, err)
				}
			}
			link, err := os.Readlink(filepath.Join(checkout, "目录", "link"))
			if err != nil || link != "0-α.bin" || manifest["目录/link"].Class != Captured {
				t.Fatalf("symlink: %s %v", link, err)
			}
		})
	}
}
