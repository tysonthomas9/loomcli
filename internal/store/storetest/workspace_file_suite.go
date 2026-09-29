package storetest

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// WorkspaceFileHarness wires one isolated workspace-file store into the
// shared behavioral suite. SetActor changes the authenticated writer identity
// represented by adapters that do not have a real authentication layer.
type WorkspaceFileHarness struct {
	Store    store.WorkspaceFileStore
	SetActor func(actor string)
	Corrupt  func(t testing.TB, workspaceKey, revision, path string, replacement []byte)
}

// RunWorkspaceFileConformance exercises the observable contract every
// WorkspaceFileStore adapter must preserve.
func RunWorkspaceFileConformance(t *testing.T, newHarness func(testing.TB) *WorkspaceFileHarness) {
	t.Helper()
	cases := []struct {
		name string
		run  func(*testing.T, *WorkspaceFileHarness)
	}{
		{"BinaryManifestRoundTrip", testWorkspaceFileBinaryManifestRoundTrip},
		{"CanonicalOrderAndFirstWriterProvenance", testWorkspaceFileCanonicalOrderAndProvenance},
		{"RejectsPathCollisions", testWorkspaceFileRejectsPathCollisions},
		{"NotFound", testWorkspaceFileNotFound},
		{"DefensiveCopies", testWorkspaceFileDefensiveCopies},
		{"CorruptBytesFailIntegrityCheck", testWorkspaceFileCorruptBytes},
		{"ConcurrentIdenticalPublish", testWorkspaceFileConcurrentPublish},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, newHarness(t)) })
	}
}

func testWorkspaceFileBinaryManifestRoundTrip(t *testing.T, h *WorkspaceFileHarness) {
	content := []byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0xff, 'x'}
	result := publishWorkspaceFiles(t, h.Store, []domain.WorkspaceFileInput{{
		Path: "archives/tool.zip", Bytes: content, MediaType: "application/zip", Executable: true,
	}})
	if result.Status != domain.WorkspaceFileTreePublished {
		t.Fatalf("status = %q, want published", result.Status)
	}
	if len(result.Tree.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(result.Tree.Files))
	}
	file := result.Tree.Files[0]
	if file.Path != "archives/tool.zip" || file.MediaType != "application/zip" || !file.Executable {
		t.Fatalf("file metadata = %+v", file)
	}
	if file.ContentHash != "sha256:f047e68c4ad68f087a04565e341cbac18644b006b5a2e76ad45b12cd434feee9" || file.SizeBytes != 7 {
		t.Fatalf("file content identity = %+v", file)
	}
	if file.Revision == "" || result.Tree.Revision == "" {
		t.Fatalf("derived revisions missing: file=%q tree=%q", file.Revision, result.Tree.Revision)
	}
	stat, err := h.Store.Stat(t.Context(), "FILES", result.Tree.Revision, file.Path)
	if err != nil || *stat != file {
		t.Fatalf("Stat = %+v, %v; want %+v", stat, err, file)
	}
	downloaded, err := h.Store.Download(t.Context(), "FILES", result.Tree.Revision, file.Path)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(downloaded, content) {
		t.Fatalf("downloaded = %v, want %v", downloaded, content)
	}
}

func testWorkspaceFileCanonicalOrderAndProvenance(t *testing.T, h *WorkspaceFileHarness) {
	h.SetActor("alice")
	first := publishWorkspaceFiles(t, h.Store, []domain.WorkspaceFileInput{
		{Path: "z.txt", Bytes: []byte("z")},
		{Path: "a.txt", Bytes: []byte("a")},
	})
	if got := []string{first.Tree.Files[0].Path, first.Tree.Files[1].Path}; fmt.Sprint(got) != "[a.txt z.txt]" {
		t.Fatalf("canonical paths = %v", got)
	}
	h.SetActor("bob")
	second := publishWorkspaceFiles(t, h.Store, []domain.WorkspaceFileInput{
		{Path: "a.txt", Bytes: []byte("a")},
		{Path: "z.txt", Bytes: []byte("z")},
	})
	if second.Status != domain.WorkspaceFileTreeExisting || second.Tree.Revision != first.Tree.Revision {
		t.Fatalf("idempotent publish = %+v, first revision %q", second, first.Tree.Revision)
	}
	if second.Tree.CreatedBy != "alice" || !second.Tree.CreatedAt.Equal(first.Tree.CreatedAt) {
		t.Fatalf("duplicate changed first-writer provenance: first=%+v second=%+v", first.Tree, second.Tree)
	}
}

func testWorkspaceFileRejectsPathCollisions(t *testing.T, h *WorkspaceFileHarness) {
	sets := [][]domain.WorkspaceFileInput{
		{{Path: "a", Bytes: nil}, {Path: "a", Bytes: nil}},
		{{Path: "A", Bytes: nil}, {Path: "a", Bytes: nil}},
		{{Path: "caf\u00e9", Bytes: nil}, {Path: "cafe\u0301", Bytes: nil}},
		{{Path: "scripts", Bytes: nil}, {Path: "scripts/run.sh", Bytes: nil}},
		{{Path: "../escape", Bytes: nil}},
	}
	for _, files := range sets {
		if _, err := h.Store.Publish(t.Context(), "FILES", files); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("Publish(%v) error = %v, want ErrInvalid", files, err)
		}
	}
}

func testWorkspaceFileNotFound(t *testing.T, h *WorkspaceFileHarness) {
	if _, err := h.Store.GetTree(t.Context(), "FILES", "wft1_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetTree error = %v, want ErrNotFound", err)
	}
	result := publishWorkspaceFiles(t, h.Store, []domain.WorkspaceFileInput{{Path: "a", Bytes: []byte("a")}})
	if _, err := h.Store.Stat(t.Context(), "FILES", result.Tree.Revision, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Stat error = %v, want ErrNotFound", err)
	}
	if _, err := h.Store.Download(t.Context(), "OTHER", result.Tree.Revision, "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-workspace Download error = %v, want ErrNotFound", err)
	}
}

func testWorkspaceFileDefensiveCopies(t *testing.T, h *WorkspaceFileHarness) {
	body := []byte("original")
	input := []domain.WorkspaceFileInput{{Path: "a", Bytes: body}}
	result := publishWorkspaceFiles(t, h.Store, input)
	body[0] = 'X'
	input[0].Path = "mutated"
	result.Tree.Files[0].Path = "mutated-return"

	stored, err := h.Store.GetTree(t.Context(), "FILES", result.Tree.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Files[0].Path != "a" {
		t.Fatalf("stored path = %q, want a", stored.Files[0].Path)
	}
	downloaded, err := h.Store.Download(t.Context(), "FILES", stored.Revision, "a")
	if err != nil {
		t.Fatal(err)
	}
	downloaded[0] = 'Y'
	again, err := h.Store.Download(t.Context(), "FILES", stored.Revision, "a")
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != "original" {
		t.Fatalf("stored bytes = %q, want original", again)
	}
}

func testWorkspaceFileCorruptBytes(t *testing.T, h *WorkspaceFileHarness) {
	result := publishWorkspaceFiles(t, h.Store, []domain.WorkspaceFileInput{{Path: "a", Bytes: []byte("expected")}})
	h.Corrupt(t, "FILES", result.Tree.Revision, "a", []byte("corrupt!"))
	if _, err := h.Store.Download(t.Context(), "FILES", result.Tree.Revision, "a"); !errors.Is(err, domain.ErrIntegrity) {
		t.Fatalf("Download corrupt bytes error = %v, want ErrIntegrity", err)
	}
}

func testWorkspaceFileConcurrentPublish(t *testing.T, h *WorkspaceFileHarness) {
	h.SetActor("alice")
	const writers = 32
	var published atomic.Int64
	revisions := make(chan string, writers)
	errorsCh := make(chan error, writers)
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := h.Store.Publish(t.Context(), "FILES", []domain.WorkspaceFileInput{{Path: "a", Bytes: []byte("same")}})
			if err != nil {
				errorsCh <- err
				return
			}
			if result.Status == domain.WorkspaceFileTreePublished {
				published.Add(1)
			}
			revisions <- result.Tree.Revision
		}()
	}
	wg.Wait()
	close(errorsCh)
	close(revisions)
	for err := range errorsCh {
		t.Fatalf("concurrent Publish: %v", err)
	}
	if published.Load() != 1 {
		t.Fatalf("published statuses = %d, want exactly 1", published.Load())
	}
	var revision string
	for got := range revisions {
		if revision == "" {
			revision = got
		}
		if got != revision {
			t.Fatalf("concurrent revisions differ: %q and %q", revision, got)
		}
	}
}

func publishWorkspaceFiles(t *testing.T, files store.WorkspaceFileStore, inputs []domain.WorkspaceFileInput) *domain.WorkspaceFileTreePublishResult {
	t.Helper()
	result, err := files.Publish(t.Context(), "FILES", inputs)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return result
}
