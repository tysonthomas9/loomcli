package svcimpl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestFileVersions_ReadAndStatUseStrongContentHash(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "file.txt"), "content")
	svc := scopedSvc(root)
	ctx := context.Background()

	read, err := svc.ReadFileScoped(ctx, "ws", service.ScopeWorkspace, "", "", "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("content"))
	wantVersion := "sha256:" + hex.EncodeToString(want[:])
	if read.Version != wantVersion {
		t.Fatalf("read version = %q, want %q", read.Version, wantVersion)
	}
	stat, err := svc.StatPathScoped(ctx, "ws", service.ScopeWorkspace, "", "", "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if stat.Version != read.Version || stat.IsDir {
		t.Fatalf("stat = %+v, read version %q", stat, read.Version)
	}
}

func TestFileVersions_DeleteRequiresMatchingVersion(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "file.txt"), "one")
	svc := scopedSvc(root)
	ctx := context.Background()

	wantKind(t, svc.DeletePathVersionedScoped(ctx, "ws", service.ScopeWorkspace, "", "", "file.txt", false, ""), service.KindPreconditionRequired)
	version := mustScopedVersion(t, svc, ctx, service.ScopeWorkspace, "", "file.txt")
	mustWrite(t, filepath.Join(root, "file.txt"), "two") // terminal/agent writer bypasses Loom's lock
	wantKind(t, svc.DeletePathVersionedScoped(ctx, "ws", service.ScopeWorkspace, "", "", "file.txt", false, version), service.KindPreconditionFailed)
	version = mustScopedVersion(t, svc, ctx, service.ScopeWorkspace, "", "file.txt")
	if err := svc.DeletePathVersionedScoped(ctx, "ws", service.ScopeWorkspace, "", "", "file.txt", false, version); err != nil {
		t.Fatal(err)
	}
}

func TestFileVersions_MoveRequiresSourceAndOverwriteDestinationVersions(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "source.txt"), "source")
	mustWrite(t, filepath.Join(root, "destination.txt"), "destination")
	svc := scopedSvc(root)
	ctx := context.Background()
	sourceVersion := mustScopedVersion(t, svc, ctx, service.ScopeWorkspace, "", "source.txt")
	destinationVersion := mustScopedVersion(t, svc, ctx, service.ScopeWorkspace, "", "destination.txt")

	_, err := svc.MovePathVersionedScoped(ctx, "ws", service.ScopeWorkspace, "", "", "source.txt", "destination.txt", true, "", destinationVersion)
	wantKind(t, err, service.KindPreconditionRequired)
	_, err = svc.MovePathVersionedScoped(ctx, "ws", service.ScopeWorkspace, "", "", "source.txt", "destination.txt", true, "sha256:stale", destinationVersion)
	wantKind(t, err, service.KindPreconditionFailed)
	_, err = svc.MovePathVersionedScoped(ctx, "ws", service.ScopeWorkspace, "", "", "source.txt", "destination.txt", true, sourceVersion, "")
	wantKind(t, err, service.KindPreconditionRequired)
	_, err = svc.MovePathVersionedScoped(ctx, "ws", service.ScopeWorkspace, "", "", "source.txt", "destination.txt", true, sourceVersion, "sha256:stale")
	wantKind(t, err, service.KindPreconditionFailed)
	result, err := svc.MovePathVersionedScoped(ctx, "ws", service.ScopeWorkspace, "", "", "source.txt", "destination.txt", true, sourceVersion, destinationVersion)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.Version != sourceVersion {
		t.Fatalf("move result = %+v, want source version", result)
	}
}

func TestFileVersions_DirectoryChildContentInvalidatesVersion(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "dir", "child.txt"), "aa")
	svc := scopedSvc(root)
	ctx := context.Background()
	version := mustScopedVersion(t, svc, ctx, service.ScopeWorkspace, "", "dir")
	info, err := os.Stat(filepath.Join(root, "dir", "child.txt"))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "dir", "child.txt"), "bb")
	if err := os.Chtimes(filepath.Join(root, "dir", "child.txt"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	wantKind(t, svc.DeletePathVersionedScoped(ctx, "ws", service.ScopeWorkspace, "", "", "dir", true, version), service.KindPreconditionFailed)
}

func TestFileVersions_DirectorySymlinkTargetInvalidatesVersion(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "dir", "alias")
	if err := os.Symlink("first", link); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}
	svc := scopedSvc(root)
	ctx := context.Background()
	before := mustScopedVersion(t, svc, ctx, service.ScopeWorkspace, "", "dir")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other", link); err != nil {
		t.Fatal(err)
	}
	after := mustScopedVersion(t, svc, ctx, service.ScopeWorkspace, "", "dir")
	if before == after {
		t.Fatalf("directory version did not change when symlink target changed: %q", before)
	}
}

func TestFileVersions_ConditionalWritesAndCreateRace(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "existing.txt"), "one")
	svc := scopedSvc(root)
	ctx := context.Background()
	version := mustScopedVersion(t, svc, ctx, service.ScopeWorkspace, "", "existing.txt")

	updated, err := svc.WriteFileConditionalScoped(ctx, "ws", service.ScopeWorkspace, "", "", "existing.txt", "two", service.FileWritePreconditions{IfMatch: version})
	if err != nil || updated.Version == version {
		t.Fatalf("conditional write = %+v, %v", updated, err)
	}
	_, err = svc.WriteFileConditionalScoped(ctx, "ws", service.ScopeWorkspace, "", "", "existing.txt", "stale", service.FileWritePreconditions{IfMatch: version})
	wantKind(t, err, service.KindPreconditionFailed)
	if err := writeScopedFile(ctx, svc, "ws", service.ScopeWorkspace, "", "", "existing.txt", "lww"); err != nil {
		t.Fatalf("ordinary save must remain LWW: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, content := range []string{"first", "second"} {
		go func(content string) {
			<-start
			_, err := svc.WriteFileConditionalScoped(ctx, "ws", service.ScopeWorkspace, "", "", "created.txt", content, service.FileWritePreconditions{IfNoneMatch: true})
			errs <- err
		}(content)
	}
	close(start)
	var successes, failed int
	for range 2 {
		err := <-errs
		if err == nil {
			successes++
			continue
		}
		var serviceErr *service.ServiceError
		if errors.As(err, &serviceErr) && serviceErr.Kind == service.KindPreconditionFailed {
			failed++
			continue
		}
		t.Fatalf("unexpected create-race error: %v", err)
	}
	if successes != 1 || failed != 1 {
		t.Fatalf("create race successes=%d precondition_failures=%d", successes, failed)
	}
}

func TestFileVersions_TwoBrowserWritersWithSameVersion(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "file.txt"), "base")
	svc := scopedSvc(root)
	ctx := context.Background()
	version := mustScopedVersion(t, svc, ctx, service.ScopeWorkspace, "", "file.txt")
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, content := range []string{"first", "second"} {
		go func(content string) {
			<-start
			_, err := svc.WriteFileConditionalScoped(ctx, "ws", service.ScopeWorkspace, "", "", "file.txt", content, service.FileWritePreconditions{IfMatch: version})
			errs <- err
		}(content)
	}
	close(start)
	var successes, failed int
	for range 2 {
		err := <-errs
		if err == nil {
			successes++
			continue
		}
		var serviceErr *service.ServiceError
		if errors.As(err, &serviceErr) && serviceErr.Kind == service.KindPreconditionFailed {
			failed++
			continue
		}
		t.Fatalf("unexpected browser writer error: %v", err)
	}
	if successes != 1 || failed != 1 {
		t.Fatalf("writer race successes=%d precondition_failures=%d", successes, failed)
	}
}

func TestFileVersions_CaseAliasWritersSerialize(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Mixed.txt"), "base")
	if _, err := os.Stat(filepath.Join(root, "mixed.txt")); err != nil {
		t.Skip("filesystem is case-sensitive")
	}
	svc := scopedSvc(root)
	ctx := context.Background()
	version := mustScopedVersion(t, svc, ctx, service.ScopeWorkspace, "", "Mixed.txt")
	start := make(chan struct{})
	errs := make(chan error, 2)
	paths := []string{"Mixed.txt", "mixed.TXT"}
	for i, path := range paths {
		go func(path, content string) {
			<-start
			_, err := svc.WriteFileConditionalScoped(ctx, "ws", service.ScopeWorkspace, "", "", path, content, service.FileWritePreconditions{IfMatch: version})
			errs <- err
		}(path, string(rune('a'+i)))
	}
	close(start)
	var successes, failed int
	for range paths {
		err := <-errs
		if err == nil {
			successes++
			continue
		}
		var serviceErr *service.ServiceError
		if errors.As(err, &serviceErr) && serviceErr.Kind == service.KindPreconditionFailed {
			failed++
			continue
		}
		t.Fatalf("unexpected case-alias writer error: %v", err)
	}
	if successes != 1 || failed != 1 {
		t.Fatalf("case-alias race successes=%d precondition_failures=%d", successes, failed)
	}
}

func TestPathLockSet_DeterministicMultiPathOrdering(t *testing.T) {
	want := []string{"SCOPE\x00A", "SCOPE\x00A/B", "SCOPE\x00C", "SCOPE\x00C/D"}
	if got := canonicalMutationLockKeys("scope", "c/d", "a/b"); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %#v, want %#v", got, want)
	}
	var locks pathLockSet
	first := locks.lock("scope", "a/b", "c/d")
	acquired := make(chan func(), 1)
	go func() { acquired <- locks.lock("scope", "c/d", "a/b") }()
	select {
	case release := <-acquired:
		release()
		t.Fatal("reversed acquisition did not block")
	case <-time.After(20 * time.Millisecond):
	}
	first()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("reversed acquisition deadlocked")
	}
}

func TestPathLockSet_CaseAliasesShareKeys(t *testing.T) {
	upper := canonicalMutationLockKeys("/Root/Repo", "Dir/File")
	lower := canonicalMutationLockKeys("/root/repo", "dir/file")
	if !reflect.DeepEqual(upper, lower) {
		t.Fatalf("case-alias keys differ: %#v != %#v", upper, lower)
	}

	var locks pathLockSet
	releaseFirst := locks.lock("/Root/Repo", "Dir/File")
	acquired := make(chan func(), 1)
	go func() { acquired <- locks.lock("/root/repo", "dir/file") }()
	select {
	case release := <-acquired:
		release()
		t.Fatal("case-alias acquisition did not serialize")
	case <-time.After(20 * time.Millisecond):
	}
	releaseFirst()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("case-alias acquisition deadlocked")
	}
}

func TestOpenScopedRoot_UsesResolvedCanonicalIdentity(t *testing.T) {
	realParent := t.TempDir()
	realRoot := filepath.Join(realParent, "root")
	if err := os.Mkdir(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasBase := filepath.Join(t.TempDir(), "parent-alias")
	if err := os.Symlink(realParent, aliasBase); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}
	direct, err := openScopedRoot(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	alias, err := openScopedRoot(filepath.Join(aliasBase, "root"))
	if err != nil {
		t.Fatal(err)
	}
	defer alias.Close()
	if direct.identity != alias.identity {
		t.Fatalf("root identities differ: %q != %q", direct.identity, alias.identity)
	}
}

func TestDirectoryManifestBoundsFailClosed(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "file.txt"), "x")
	scoped, err := openScopedRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer scoped.Close()
	tests := []struct {
		name    string
		entries int
		bytes   int64
	}{
		{name: "entries", entries: maxDirectoryVersionEntries},
		{name: "bytes", bytes: maxDirectoryVersionBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries, bytes := tt.entries, tt.bytes
			h := sha256.New()
			err := hashDirectoryManifest(scoped.store, ".", ".", h, &entries, &bytes)
			wantKind(t, err, service.KindPayloadTooLarge)
		})
	}
}

func TestPathLockSet_ConcurrentRelease(t *testing.T) {
	var locks pathLockSet
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := locks.lock("scope", "dir/file")
			release()
		}()
	}
	wg.Wait()
	locks.mu.Lock()
	defer locks.mu.Unlock()
	if len(locks.locks) != 0 {
		t.Fatalf("lock entries leaked: %d", len(locks.locks))
	}
}
