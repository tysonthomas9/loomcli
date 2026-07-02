// This file references NewFileService from the root webui package which
// cannot be imported without creating a cycle (see test_bridge_test.go).

package misc

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// wsRootFor returns a mockFileOps whose workspace root resolves to dir.
func wsRootFor(dir string) *mockFileOps {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	return &mockFileOps{
		resolveWsRootFunc: func() (string, error) {
			return resolved, nil
		},
	}
}

// scopedReq builds a GET request for a scope-rooted file route with the
// workspace context the handler reads wsID from.
func scopedReq(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	return req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
}

func TestHandleScopedFileTree_WorkspaceRootListsEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "pkg"), 0755); err != nil {
		t.Fatal(err)
	}

	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=workspace"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp FileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := map[string]bool{}
	for _, e := range resp.Entries {
		names[e.Name] = true
	}
	if !names["readme.txt"] || !names["pkg"] {
		t.Errorf("entries = %+v, want readme.txt and pkg", resp.Entries)
	}
}

func TestHandleScopedFileTree_DefaultsToWorkspaceScope(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}

	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	// No scope param — handler must default to the workspace scope.
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleScopedFileRead_WorkspaceRootReadsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("Hello, world!\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := HandleScopedFileRead(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files?scope=workspace&path=hello.txt"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp FileReadResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Content != "Hello, world!\n" {
		t.Errorf("content = %q, want %q", resp.Content, "Hello, world!\n")
	}
}

func TestHandleScopedFileTree_HiddenDirsHiddenFromListing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "src.go"), []byte("package x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[core]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".loom"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".loom", "state.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=workspace"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp FileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, e := range resp.Entries {
		if e.Name == ".git" || e.Name == ".loom" {
			t.Fatalf("%s must be hidden from the listing; entries: %+v", e.Name, resp.Entries)
		}
	}
}

func TestHandleScopedFileRead_HiddenPathDenied(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("url = https://x:tok@host/r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".loom"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".loom", "state.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := HandleScopedFileRead(NewFileService(wsRootFor(dir)))
	for _, path := range []string{".git/config", ".loom/state.json"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files?scope=workspace&path="+path))
		if w.Code != http.StatusForbidden {
			t.Fatalf("path=%s: status = %d, want 403; body: %s", path, w.Code, w.Body.String())
		}
	}
}

func TestHandleScopedFileTree_PathTraversalDenied(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	// filepath.Clean("/"+path) clamps traversal inside the root, so an escape
	// attempt maps to a non-existent in-root path (never a file outside it).
	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	for _, p := range []string{"../../../etc/passwd", "subdir/../../../etc/shadow"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=workspace&path="+p))
		if w.Code == http.StatusOK {
			t.Errorf("path=%q: got 200, want rejection", p)
		}
	}
}

func TestHandleScopedFile_UnsupportedScope(t *testing.T) {
	dir := t.TempDir()
	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=bogus"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unsupported scope; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleScopedFileTree_WorkspaceScopeRejectsTarget(t *testing.T) {
	dir := t.TempDir()
	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=workspace&target=loomcli"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when workspace scope gets a target; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleScopedFile_WorkspaceNotCheckedOut(t *testing.T) {
	fo := &mockFileOps{
		resolveWsRootFunc: func() (string, error) {
			return "", errors.New("workspace \"test-ws\" is not checked out on this machine at /x")
		},
	}
	h := HandleScopedFileTree(NewFileService(fo))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=workspace"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when workspace not checked out; body: %s", w.Code, w.Body.String())
	}
}
