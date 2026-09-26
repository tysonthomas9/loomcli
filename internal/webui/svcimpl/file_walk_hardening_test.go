package svcimpl

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/skillpaths"
)

func TestFileSearchMandatoryGitExclusionAndDoublestar(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".GIT", "config"), "needle secret")
	mustWrite(t, filepath.Join(root, "main.go"), "needle root")
	mustWrite(t, filepath.Join(root, "src", "nested", "main.go"), "needle nested")
	mustWrite(t, filepath.Join(root, "src", "nested", "main.ts"), "needle ts")
	mustWrite(t, filepath.Join(root, ".loom", "terminal-worktrees", "visible.txt"), "needle visible")

	emptyExclude := []string{}
	result, err := scopedSvc(root).SearchFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", "", service.FileSearchRequest{
		Query:   "needle",
		Include: []string{"**/*.go", ".GIT/**"},
		Exclude: &emptyExclude,
	})
	if err != nil {
		t.Fatalf("SearchFilesScoped: %v", err)
	}
	got := strings.Join(resultPaths(result.Results), ",")
	if got != "main.go,src/nested/main.go" {
		t.Fatalf("search paths = %q, want recursive Go files without .GIT", got)
	}

	index, err := scopedSvc(root).IndexFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("IndexFilesScoped: %v", err)
	}
	for _, path := range index.Paths {
		if strings.Contains(strings.ToLower(path), ".git") {
			t.Fatalf("index exposed mixed-case git metadata: %v", index.Paths)
		}
	}
	if !containsPath(index.Paths, ".loom/terminal-worktrees/visible.txt") {
		t.Fatalf("index hid explicitly-visible .loom/terminal-worktrees namespace: %v", index.Paths)
	}
}

func TestFileSearchInvalidDoublestarPatternIsValidationError(t *testing.T) {
	_, err := newFileSearchExecution(service.FileSearchRequest{Query: "needle", Include: []string{"[unterminated"}})
	if err == nil {
		t.Fatal("invalid glob succeeded")
	}
	var serviceErr *service.ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != service.KindValidation {
		t.Fatalf("error = %T %v, want typed validation error", err, err)
	}
}

func TestFileSearchValidationFailureClosesScopedRoot(t *testing.T) {
	root := t.TempDir()
	svc := scopedSvc(root)
	baseline, err := countOpenFileDescriptors()
	if err != nil {
		t.Skipf("open file descriptor accounting unavailable: %v", err)
	}

	for i := 0; i < 250; i++ {
		request := service.FileSearchRequest{Query: "needle", Include: []string{"[unterminated"}}
		if i%2 == 1 {
			request = service.FileSearchRequest{Query: "[unterminated", Regex: true}
		}
		if _, err := svc.SearchFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", "", request); err == nil {
			t.Fatalf("invalid search request %d succeeded", i)
		}
	}

	after, err := countOpenFileDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	if after > baseline+4 {
		t.Fatalf("open descriptors grew from %d to %d after invalid searches", baseline, after)
	}
}

func countOpenFileDescriptors() (int, error) {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

func TestFileSearchOneMiBBoundaryPreservesUTF8AndSkipsInvalidFiles(t *testing.T) {
	withNavigationCaps(t, func() {
		fileSearchMaxFileBytes = 1 << 20
		fileSearchMaxBytes = 4 << 20
		root := t.TempDir()
		data := make([]byte, fileSearchMaxFileBytes+2)
		for i := range data {
			data[i] = 'a'
		}
		copy(data, "needle\n")
		data[fileSearchMaxFileBytes-1] = 0xe2
		data[fileSearchMaxFileBytes] = 0x82
		data[fileSearchMaxFileBytes+1] = 0xac
		if err := os.WriteFile(filepath.Join(root, "boundary.txt"), data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "invalid.txt"), []byte("needle\xff"), 0600); err != nil {
			t.Fatal(err)
		}

		result, err := scopedSvc(root).SearchFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", "", service.FileSearchRequest{Query: "needle"})
		if err != nil {
			t.Fatalf("SearchFilesScoped: %v", err)
		}
		if got := resultPaths(result.Results); len(got) != 1 || got[0] != "boundary.txt" {
			t.Fatalf("search paths = %v, want only boundary.txt", got)
		}
		if !hasPartialReason(result.PartialReasons, service.FilePartialFileSize) {
			t.Fatalf("partial reasons = %v, want file_size", result.PartialReasons)
		}
		if preview := result.Results[0].Matches[0].Preview; !utf8.ValidString(preview) {
			t.Fatalf("preview is invalid UTF-8: %q", preview)
		}
	})
}

func TestSearchMatcherCaseInsensitiveUnicodeUsesOriginalOffsets(t *testing.T) {
	matcher, err := newSearchMatcher("äbc", false, false)
	if err != nil {
		t.Fatal(err)
	}
	matches, clipped := matcher.find("prefix ÄBC suffix", 10)
	if clipped || len(matches) != 1 || matches[0].Col != 8 || !utf8.ValidString(matches[0].Preview) {
		t.Fatalf("matches = %+v, clipped=%v", matches, clipped)
	}
}

func TestFileSearchReportsEveryEnforcedBound(t *testing.T) {
	t.Run("file count", func(t *testing.T) {
		withNavigationCaps(t, func() {
			fileSearchMaxFiles = 1
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "a.txt"), "needle")
			mustWrite(t, filepath.Join(root, "b.txt"), "needle")
			result := mustSearch(t, root, context.Background())
			assertPartialReason(t, result.PartialReasons, service.FilePartialFileCount)
		})
	})
	t.Run("result count", func(t *testing.T) {
		withNavigationCaps(t, func() {
			fileSearchMaxMatches = 1
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "a.txt"), "needle needle")
			result := mustSearch(t, root, context.Background())
			assertPartialReason(t, result.PartialReasons, service.FilePartialResultCount)
		})
	})
	t.Run("byte limit", func(t *testing.T) {
		withNavigationCaps(t, func() {
			fileSearchMaxBytes = 3
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "a.txt"), "needle")
			result := mustSearch(t, root, context.Background())
			assertPartialReason(t, result.PartialReasons, service.FilePartialByteLimit)
		})
	})
	t.Run("deadline", func(t *testing.T) {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, "a.txt"), "needle")
		scoped, err := openScopedRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		defer scoped.Close()
		result, err := walkScopeFiles(context.Background(), scoped.store, boundedFileWalkOptions{timeBudget: time.Nanosecond}, func(string, os.FileInfo) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		assertPartialReason(t, result.partialReasons, service.FilePartialDeadline)
	})
	t.Run("canceled", func(t *testing.T) {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, "a.txt"), "needle")
		scoped, err := openScopedRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		defer scoped.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := walkScopeFiles(ctx, scoped.store, boundedFileWalkOptions{}, func(string, os.FileInfo) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		assertPartialReason(t, result.partialReasons, service.FilePartialCanceled)
	})
}

func TestFileSearchExactResultCapIsNotReportedAsPartial(t *testing.T) {
	withNavigationCaps(t, func() {
		fileSearchMaxMatches = 1
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, "a.txt"), "needle")
		result := mustSearch(t, root, context.Background())
		if hasPartialReason(result.PartialReasons, service.FilePartialResultCount) {
			t.Fatalf("exact result cap was reported as partial: %v", result.PartialReasons)
		}
	})
}

func TestFileIndexCacheLRUTTLCopyAndPolicyIsolation(t *testing.T) {
	now := time.Unix(1_000, 0)
	cache := newFileIndexCache(2, 1<<20, 10*time.Second)
	cache.now = func() time.Time { return now }
	a := &service.FileIndexResult{Paths: []string{"a"}, PartialReasons: []service.FilePartialReason{service.FilePartialFileCount}}
	cache.put("/root/a", true, "skills-v1", a)
	a.Paths[0] = "mutated"
	got, ok := cache.get("/root/a", true, "skills-v1")
	if !ok || got.Paths[0] != "a" {
		t.Fatalf("stored result was not isolated: %+v, ok=%v", got, ok)
	}
	got.Paths[0] = "also-mutated"
	got, _ = cache.get("/root/a", true, "skills-v1")
	if got.Paths[0] != "a" {
		t.Fatalf("read result was not isolated: %+v", got)
	}
	if _, ok := cache.get("/root/a", false, "skills-v1"); ok {
		t.Fatal("sensitive-capable entry leaked into viewer cache")
	}
	cache.put("/root/a", false, "skills-v1", &service.FileIndexResult{Paths: []string{"public"}})
	if cache.lru.Len() != 2 {
		t.Fatalf("policy entries = %d, want 2", cache.lru.Len())
	}

	cache.put("/root/b", true, "skills-v1", &service.FileIndexResult{Paths: []string{"b"}})
	if _, ok := cache.get("/root/a", true, "skills-v1"); ok {
		t.Fatal("least-recently-used entry was not evicted at entry cap")
	}
	now = now.Add(11 * time.Second)
	if _, ok := cache.get("/root/b", true, "skills-v1"); ok {
		t.Fatal("expired entry was returned")
	}
}

func TestFileIndexCacheByteEvictionAndOverlappingInvalidation(t *testing.T) {
	first := &service.FileIndexResult{Paths: []string{strings.Repeat("a", 200)}}
	oneSize := estimateFileIndexSize(first)
	cache := newFileIndexCache(32, oneSize+10, time.Hour)
	cache.put("/workspace", true, "skills-v1", first)
	cache.put("/other", true, "skills-v1", &service.FileIndexResult{Paths: []string{strings.Repeat("b", 200)}})
	if _, ok := cache.get("/workspace", true, "skills-v1"); ok {
		t.Fatal("byte bound did not evict least-recently-used payload")
	}

	cache = newFileIndexCache(32, 1<<20, time.Hour)
	for _, root := range []string{"/workspace", "/workspace/repo", "/workspace/repo/nested", "/workspace-other"} {
		cache.put(root, true, "skills-v1", &service.FileIndexResult{Paths: []string{root}})
	}
	cache.invalidateOverlapping("/workspace/repo")
	for _, root := range []string{"/workspace", "/workspace/repo", "/workspace/repo/nested"} {
		if _, ok := cache.get(root, true, "skills-v1"); ok {
			t.Fatalf("overlapping root %q was not invalidated", root)
		}
	}
	if _, ok := cache.get("/workspace-other", true, "skills-v1"); !ok {
		t.Fatal("prefix-only sibling was incorrectly invalidated")
	}
}

func TestFileIndexCacheInvalidationFencesInflightStore(t *testing.T) {
	cache := newFileIndexCache(32, 1<<20, time.Hour)
	generation := cache.currentGeneration()
	cache.invalidateOverlapping("/workspace")
	cache.putIfGeneration("/workspace", true, "skills-v1", &service.FileIndexResult{Paths: []string{"stale.txt"}}, generation)
	if _, ok := cache.get("/workspace", true, "skills-v1"); ok {
		t.Fatal("result from an invalidated in-flight build was cached")
	}
}

func TestFileIndexKeysIncludeSkillPolicyIdentity(t *testing.T) {
	first := skillpaths.NewPolicy("services/api").Identity()
	changed := skillpaths.NewPolicy("packages/api").Identity()
	if fileIndexCacheKey("/workspace", true, first) == fileIndexCacheKey("/workspace", true, changed) {
		t.Fatal("cache key did not change with skill hiding policy")
	}
	if fileIndexBuildKey("/workspace", true, first, 7) == fileIndexBuildKey("/workspace", true, changed, 7) {
		t.Fatal("build key did not change with skill hiding policy")
	}
}

func TestFileIndexSingleflightDoesNotReturnBuildInvalidatedMidFlight(t *testing.T) {
	root := t.TempDir()
	impl := scopedSvc(root).(*fileServiceImpl)
	scoped, err := openScopedRoot(root)
	if err != nil {
		t.Fatalf("openScopedRoot: %v", err)
	}
	identity := scoped.identity
	scoped.Close()

	releaseFirst := make(chan struct{})
	started := make(chan int, 2)
	var starts atomic.Int32
	impl.indexBuilder = func(context.Context, string, string, bool, skillpaths.Policy) (*service.FileIndexResult, error) {
		start := int(starts.Add(1))
		started <- start
		if start == 1 {
			<-releaseFirst
			return &service.FileIndexResult{Paths: []string{"stale.txt"}}, nil
		}
		return &service.FileIndexResult{Paths: []string{"fresh.txt"}}, nil
	}

	firstResult := make(chan *service.FileIndexResult, 1)
	firstErr := make(chan error, 1)
	go func() {
		res, err := impl.IndexFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", "")
		firstResult <- res
		firstErr <- err
	}()
	if got := <-started; got != 1 {
		t.Fatalf("first build start = %d, want 1", got)
	}
	impl.invalidateIndex(identity)

	secondResult := make(chan *service.FileIndexResult, 1)
	secondErr := make(chan error, 1)
	go func() {
		res, err := impl.IndexFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", "")
		secondResult <- res
		secondErr <- err
	}()
	if got := <-started; got != 2 {
		t.Fatalf("second build start = %d, want 2", got)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("second index: %v", err)
	}
	if paths := (<-secondResult).Paths; !containsPath(paths, "fresh.txt") || containsPath(paths, "stale.txt") {
		t.Fatalf("second paths = %+v, want fresh only", paths)
	}

	close(releaseFirst)
	if err := <-firstErr; err != nil {
		t.Fatalf("first index: %v", err)
	}
	if paths := (<-firstResult).Paths; !containsPath(paths, "fresh.txt") || containsPath(paths, "stale.txt") {
		t.Fatalf("first paths = %+v, want fresh retry", paths)
	}
	if starts.Load() != 2 {
		t.Fatalf("build starts = %d, want 2", starts.Load())
	}
}

func TestFileIndexResultSerializesEmptySlicesAsArrays(t *testing.T) {
	index, err := scopedSvc(t.TempDir()).IndexFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("IndexFilesScoped: %v", err)
	}
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("Marshal index: %v", err)
	}
	body := string(data)
	for _, want := range []string{`"paths":[]`, `"partial_reasons":[]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("JSON = %s, missing %s", body, want)
		}
	}
}

func TestFileIndexSingleflightCancellationDoesNotCancelSharedBuild(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	impl := scopedSvc(root).(*fileServiceImpl)
	original := impl.indexBuilder
	started := make(chan struct{})
	release := make(chan struct{})
	var starts atomic.Int32
	impl.indexBuilder = func(ctx context.Context, rootPath, identity string, allowSensitive bool, skillPolicy skillpaths.Policy) (*service.FileIndexResult, error) {
		if starts.Add(1) == 1 {
			close(started)
		}
		<-release
		return original(ctx, rootPath, identity, allowSensitive, skillPolicy)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	firstResult := make(chan error, 1)
	go func() {
		defer wg.Done()
		_, err := impl.IndexFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", "")
		firstResult <- err
	}()
	<-started

	canceledCtx, cancel := context.WithCancel(context.Background())
	canceledResult := make(chan error, 1)
	go func() {
		_, err := impl.IndexFilesScoped(canceledCtx, "ws", service.ScopeWorkspace, "", "")
		canceledResult <- err
	}()
	cancel()
	select {
	case err := <-canceledResult:
		if err == nil {
			t.Fatal("canceled waiter succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter did not return promptly")
	}
	if starts.Load() != 1 {
		t.Fatalf("shared builds = %d, want 1", starts.Load())
	}
	close(release)
	wg.Wait()
	if err := <-firstResult; err != nil {
		t.Fatalf("shared build was canceled by waiter: %v", err)
	}
	if starts.Load() != 1 {
		t.Fatalf("shared builds = %d, want 1", starts.Load())
	}
}

func TestFileIndexReopenRejectsChangedRootIdentity(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	impl := scopedSvc(root).(*fileServiceImpl)
	_, err := impl.buildFileIndex(context.Background(), root, "different-canonical-root", true, skillpaths.NewPolicy(""))
	if err == nil {
		t.Fatal("index build accepted a reopened root under the wrong cache identity")
	}
	var serviceErr *service.ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != service.KindForbidden {
		t.Fatalf("error = %T %v, want forbidden", err, err)
	}
}

func mustSearch(t *testing.T, root string, ctx context.Context) *service.FileSearchResult {
	t.Helper()
	result, err := scopedSvc(root).SearchFilesScoped(ctx, "ws", service.ScopeWorkspace, "", "", service.FileSearchRequest{Query: "needle"})
	if err != nil {
		t.Fatalf("SearchFilesScoped: %v", err)
	}
	return result
}

func assertPartialReason(t *testing.T, reasons []service.FilePartialReason, want service.FilePartialReason) {
	t.Helper()
	if !hasPartialReason(reasons, want) {
		t.Fatalf("partial reasons = %v, want %q", reasons, want)
	}
}

func hasPartialReason(reasons []service.FilePartialReason, want service.FilePartialReason) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}
