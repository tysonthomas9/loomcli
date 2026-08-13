//go:build unix

package filesystem

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUnixRootedFileStore_ConcurrentSymlinkSwapCannotReachProtectedTrees(t *testing.T) {
	rootPath := t.TempDir()
	outside := t.TempDir()
	prepareProtectedTree(t, filepath.Join(rootPath, ".git", "sub"), "git-secret")
	prepareProtectedTree(t, filepath.Join(outside, "sub"), "outside-secret")
	prepareSafeTree(t, filepath.Join(rootPath, "alias", "sub"))

	root, err := openScopedRoot(rootPath)
	if err != nil {
		t.Fatalf("openScopedRoot: %v", err)
	}
	defer root.Close()
	if root.store.platform == nil {
		t.Skip("descriptor-relative platform backend unavailable")
	}

	done := make(chan struct{})
	finding := make(chan string, 1)
	var workers sync.WaitGroup
	workers.Add(2)
	go swapAliasUntilDone(rootPath, outside, done, &workers)
	go exerciseSwappedAlias(rootPath, root.store, done, finding, &workers)
	time.Sleep(750 * time.Millisecond)
	close(done)
	workers.Wait()
	restoreAlias(rootPath)

	select {
	case message := <-finding:
		t.Fatal(message)
	default:
	}
	assertProtectedTreeUnchanged(t, filepath.Join(rootPath, ".git", "sub"), "git-secret")
	assertProtectedTreeUnchanged(t, filepath.Join(outside, "sub"), "outside-secret")
}

func prepareProtectedTree(t *testing.T, dir, secret string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "config"), secret)
	mustWrite(t, filepath.Join(dir, "write-target"), secret)
	mustWrite(t, filepath.Join(dir, "victim"), secret)
	mustWrite(t, filepath.Join(dir, "protected-only"), secret)
}

func prepareSafeTree(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"config", "write-target", "victim"} {
		mustWrite(t, filepath.Join(dir, name), "safe")
	}
}

func swapAliasUntilDone(rootPath, outside string, done <-chan struct{}, workers *sync.WaitGroup) {
	defer workers.Done()
	alias := filepath.Join(rootPath, "alias")
	parked := filepath.Join(rootPath, "alias-parked")
	useOutside := false
	for {
		select {
		case <-done:
			return
		default:
		}
		if err := os.Rename(alias, parked); err != nil {
			runtime.Gosched()
			continue
		}
		target := ".git"
		if useOutside {
			target = outside
		}
		useOutside = !useOutside
		if err := os.Symlink(target, alias); err == nil {
			runtime.Gosched()
			_ = os.Remove(alias)
		}
		_ = os.Rename(parked, alias)
	}
}

func exerciseSwappedAlias(rootPath string, store *rootedFileRoot, done <-chan struct{}, finding chan<- string, workers *sync.WaitGroup) {
	defer workers.Done()
	for i := 0; ; i++ {
		select {
		case <-done:
			return
		default:
		}
		checkSwappedRead(store, finding)
		checkSwappedList(store, finding)
		_ = store.WriteAtomic(filepath.Join("alias", "sub", "write-target"), []byte("mutated"))
		_ = store.MkdirAll(filepath.Join("alias", "sub", "new-dir"))
		_ = store.Delete(filepath.Join("alias", "sub", "victim"), false)
		source := filepath.Join(rootPath, "move-source")
		_ = os.WriteFile(source, []byte("move"), 0o644)
		_ = store.Move("move-source", filepath.Join("alias", "sub", "moved"), true)
		checkSwappedWalk(store, finding)
	}
}

func checkSwappedRead(store *rootedFileRoot, finding chan<- string) {
	data, _, _, err := store.Read(filepath.Join("alias", "sub", "config"), -1)
	if err == nil && string(data) != "safe" {
		recordStressFinding(finding, "read protected content through swapped component: "+string(data))
	}
}

func checkSwappedList(store *rootedFileRoot, finding chan<- string) {
	entries, err := store.List(filepath.Join("alias", "sub"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Name() == "protected-only" {
			recordStressFinding(finding, "listed protected content through swapped component")
		}
	}
}

func checkSwappedWalk(store *rootedFileRoot, finding chan<- string) {
	_ = store.Walk(func() error { return nil }, func(_ string, _ os.DirEntry) (bool, error) {
		return true, nil
	}, func(path string, _ os.FileInfo) error {
		if strings.HasSuffix(filepath.ToSlash(path), "alias/sub/protected-only") {
			recordStressFinding(finding, "walked protected content through swapped component")
		}
		return nil
	})
}

func recordStressFinding(findings chan<- string, message string) {
	select {
	case findings <- message:
	default:
	}
}

func restoreAlias(rootPath string) {
	alias := filepath.Join(rootPath, "alias")
	parked := filepath.Join(rootPath, "alias-parked")
	if info, err := os.Lstat(alias); err == nil && info.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(alias)
	}
	if _, err := os.Stat(parked); err == nil {
		_ = os.Rename(parked, alias)
	}
}

func assertProtectedTreeUnchanged(t *testing.T, dir, secret string) {
	t.Helper()
	for _, name := range []string{"config", "write-target", "victim", "protected-only"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != secret {
			t.Fatalf("protected %s changed: content=%q err=%v", filepath.Join(dir, name), data, err)
		}
	}
	for _, name := range []string{"new-dir", "moved"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("protected path %s was created: %v", filepath.Join(dir, name), err)
		}
	}
}

// TestUnixRootedFileStore_ListSkipsUnopenableSocket guards the regression where a
// directory containing a unix socket (e.g. the workspace .loom folder with its
// live daemon.sock) failed the whole listing with "stat rooted directory entry",
// because the lister tried to open every non-symlink entry O_RDONLY.
func TestUnixRootedFileStore_ListSkipsUnopenableSocket(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "regular.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write regular file: %v", err)
	}
	// Bind the socket via a short symlink dir: macOS caps sun_path at ~104
	// bytes, and t.TempDir() paths exceed that, which would otherwise skip this
	// regression guard on developer machines. The listener still creates the
	// socket file at its real path inside rootPath.
	shortDir, err := os.MkdirTemp("", "s")
	if err != nil {
		t.Fatalf("short temp dir: %v", err)
	}
	defer os.RemoveAll(shortDir)
	shortLink := filepath.Join(shortDir, "s.sock")
	sockPath := filepath.Join(rootPath, "daemon.sock")
	ln, err := net.Listen("unix", shortLink)
	if err != nil {
		t.Skipf("cannot create unix socket: %v", err)
	}
	defer ln.Close()
	if err := os.Rename(shortLink, sockPath); err != nil {
		t.Fatalf("move socket into root: %v", err)
	}

	root, err := openScopedRoot(rootPath)
	if err != nil {
		t.Fatalf("openScopedRoot: %v", err)
	}
	defer root.Close()
	if root.store.platform == nil {
		t.Skip("descriptor-relative platform backend unavailable")
	}

	entries, err := root.store.List(".")
	if err != nil {
		t.Fatalf("List must not fail on a directory containing a socket: %v", err)
	}
	names := map[string]os.FileMode{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("entry Info(): %v", err)
		}
		names[e.Name()] = info.Mode()
	}
	if _, ok := names["regular.txt"]; !ok {
		t.Errorf("regular file missing from listing: %v", names)
	}
	mode, ok := names["daemon.sock"]
	if !ok {
		t.Fatalf("socket entry missing from listing: %v", names)
	}
	if mode&os.ModeSocket == 0 {
		t.Errorf("socket entry should carry os.ModeSocket, got %v", mode)
	}
}
