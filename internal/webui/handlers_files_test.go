package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// mockFileOps implements FileOps for testing.
type mockFileOps struct {
	resolveFunc func(name string) (*AgentWorktree, error)
}

func (m *mockFileOps) ResolveAgentWorktree(workspaceID, name string) (*AgentWorktree, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(name)
	}
	return nil, errors.New("not found")
}

// setupTestWorktree creates a temporary directory with test files.
func setupTestWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Text file
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("Hello, world!\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Subdirectory with nested file
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "nested.txt"), []byte("nested content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Binary file (non-UTF-8 content)
	if err := os.WriteFile(filepath.Join(dir, "binary.dat"), []byte{0x00, 0xFF, 0xFE, 0x80, 0x81}, 0644); err != nil {
		t.Fatal(err)
	}

	// Denied extension file
	if err := os.WriteFile(filepath.Join(dir, "secret.key"), []byte("secret key data"), 0644); err != nil {
		t.Fatal(err)
	}

	// Denied filename
	if err := os.WriteFile(filepath.Join(dir, "id_rsa"), []byte("private key"), 0644); err != nil {
		t.Fatal(err)
	}

	// File with custom permissions
	if err := os.WriteFile(filepath.Join(dir, "executable.sh"), []byte("#!/bin/sh\necho hello\n"), 0755); err != nil {
		t.Fatal(err)
	}

	return dir
}

// resolveToDir returns a mockFileOps that resolves to the given directory.
// It evaluates symlinks on the path so that validatePathWithinDir (which also
// resolves symlinks) can match prefixes correctly (e.g. /var → /private/var on macOS).
func resolveToDir(dir string) *mockFileOps {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	return &mockFileOps{
		resolveFunc: func(name string) (*AgentWorktree, error) {
			return &AgentWorktree{
				Name:          name,
				Path:          resolved,
				Branch:        "test-branch",
				DefaultBranch: "main",
			}, nil
		},
	}
}

// --- File Tree tests ---

func TestFileTree_ListRoot(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileTree(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp FileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Path != "." {
		t.Errorf("path = %q, want %q", resp.Path, ".")
	}
	if len(resp.Entries) == 0 {
		t.Fatal("expected entries, got none")
	}

	// First entry should be a directory (dirs-first sorting)
	if !resp.Entries[0].IsDir {
		t.Error("expected first entry to be a directory (dirs-first sorting)")
	}

	// Verify subdir is present
	found := false
	for _, e := range resp.Entries {
		if e.Name == "subdir" && e.IsDir {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find 'subdir' directory in entries")
	}
}

func TestFileTree_ListSubdirectory(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileTree(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree?path=subdir", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp FileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Path != "subdir" {
		t.Errorf("path = %q, want %q", resp.Path, "subdir")
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("entries count = %d, want 1", len(resp.Entries))
	}
	if resp.Entries[0].Name != "nested.txt" {
		t.Errorf("entry name = %q, want %q", resp.Entries[0].Name, "nested.txt")
	}
}

func TestFileTree_NonexistentDir(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileTree(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree?path=nonexistent", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestFileTree_PathTraversal(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileTree(NewFileService(ops))

	// "../" is normalized by filepath.Clean to root (safe — stays within worktree)
	// "../../etc" is normalized to "/etc" which becomes worktree/etc (does not exist)
	tests := []struct {
		path       string
		wantStatus int
	}{
		{"../", http.StatusOK},             // normalized to "." (worktree root)
		{"../../etc", http.StatusNotFound}, // normalized to "etc" subdir (doesn't exist)
		{"../../../tmp", http.StatusNotFound},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree?path="+tt.path, nil)
		req.SetPathValue("name", "test-agent")
		req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != tt.wantStatus {
			t.Errorf("path=%q: status = %d, want %d", tt.path, w.Code, tt.wantStatus)
		}
	}
}

func TestFileTree_PathIsFile(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileTree(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree?path=hello.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestFileTree_InvalidAgent(t *testing.T) {
	ops := &mockFileOps{}
	handler := handleFileTree(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/bad.agent/files/tree", nil)
	req.SetPathValue("name", "bad.agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestFileTree_AgentNotFound(t *testing.T) {
	ops := &mockFileOps{
		resolveFunc: func(name string) (*AgentWorktree, error) {
			return nil, errors.New("not found")
		},
	}
	handler := handleFileTree(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent/files/tree", nil)
	req.SetPathValue("name", "nonexistent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestFileTree_Symlink(t *testing.T) {
	dir := setupTestWorktree(t)

	// Create a symlink directory
	symDir := filepath.Join(dir, "symdir")
	if err := os.Symlink("/tmp", symDir); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}

	ops := resolveToDir(dir)
	handler := handleFileTree(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree?path=symdir", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// --- File Read tests ---

func TestFileRead_TextFile(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileRead(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=hello.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp FileReadResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Path != "hello.txt" {
		t.Errorf("path = %q, want %q", resp.Path, "hello.txt")
	}
	if resp.Content != "Hello, world!\n" {
		t.Errorf("content = %q, want %q", resp.Content, "Hello, world!\n")
	}
	if resp.Binary {
		t.Error("expected binary = false")
	}
	if resp.Size != 14 {
		t.Errorf("size = %d, want %d", resp.Size, 14)
	}
}

func TestFileRead_NestedFile(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileRead(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=subdir/nested.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp FileReadResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Content != "nested content\n" {
		t.Errorf("content = %q, want %q", resp.Content, "nested content\n")
	}
}

func TestFileRead_BinaryFile(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileRead(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=binary.dat", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp FileReadResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Binary {
		t.Error("expected binary = true")
	}
	if resp.Content != "" {
		t.Errorf("expected no content for binary file, got %q", resp.Content)
	}
}

func TestFileRead_NonexistentFile(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileRead(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=nonexistent.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestFileRead_Directory(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileRead(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=subdir", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestFileRead_MissingPath(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileRead(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestFileRead_DeniedExtension(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileRead(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=secret.key", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestFileRead_DeniedFilename(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileRead(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=id_rsa", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestFileRead_PathTraversal(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileRead(NewFileService(ops))

	paths := []string{"../etc/passwd", "../../etc/shadow", "subdir/../../etc/passwd"}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path="+p, nil)
		req.SetPathValue("name", "test-agent")
		req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
			t.Errorf("path=%q: status = %d, want 403 or 404", p, w.Code)
		}
	}
}

func TestFileRead_LargeFile(t *testing.T) {
	dir := setupTestWorktree(t)

	// Create a file larger than 1MB
	largeData := make([]byte, maxRequestBody+1)
	for i := range largeData {
		largeData[i] = 'A'
	}
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), largeData, 0644); err != nil {
		t.Fatal(err)
	}

	ops := resolveToDir(dir)
	handler := handleFileRead(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=large.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestFileRead_Symlink(t *testing.T) {
	dir := setupTestWorktree(t)

	// Create a symlink file
	symFile := filepath.Join(dir, "link.txt")
	if err := os.Symlink(filepath.Join(dir, "hello.txt"), symFile); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}

	ops := resolveToDir(dir)
	handler := handleFileRead(NewFileService(ops))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=link.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// --- File Write tests ---

func TestFileWrite_NewFile(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	body := `{"content": "new file content"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=newfile.txt", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify file was created
	data, err := os.ReadFile(filepath.Join(dir, "newfile.txt"))
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(data) != "new file content" {
		t.Errorf("file content = %q, want %q", string(data), "new file content")
	}

	// Verify permissions (should be 0644)
	fi, err := os.Stat(filepath.Join(dir, "newfile.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0644 {
		t.Errorf("permissions = %o, want %o", fi.Mode().Perm(), 0644)
	}
}

func TestFileWrite_OverwriteExisting(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	body := `{"content": "updated content"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=executable.sh", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify content was updated
	data, err := os.ReadFile(filepath.Join(dir, "executable.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "updated content" {
		t.Errorf("content = %q, want %q", string(data), "updated content")
	}

	// Verify permissions preserved (should still be 0755)
	fi, err := os.Stat(filepath.Join(dir, "executable.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Errorf("permissions = %o, want %o", fi.Mode().Perm(), 0755)
	}
}

func TestFileWrite_Subdirectory(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	body := `{"content": "subdir content"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=subdir/new.txt", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	data, err := os.ReadFile(filepath.Join(dir, "subdir", "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "subdir content" {
		t.Errorf("content = %q, want %q", string(data), "subdir content")
	}
}

func TestFileWrite_MissingPath(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	body := `{"content": "data"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestFileWrite_DeniedExtension(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	body := `{"content": "secrets"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=credentials.pem", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestFileWrite_DeniedFilename(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	body := `{"content": "key"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=id_ed25519", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestFileWrite_PathTraversal(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	// "../evil.txt" is normalized to "/evil.txt" → worktree/evil.txt (safe, within worktree)
	// "../../etc/passwd" is normalized to "/etc/passwd" → worktree/etc/passwd (parent "etc" doesn't exist → 404)
	tests := []struct {
		path       string
		wantStatus int
	}{
		{"../evil.txt", http.StatusOK},            // normalized to "evil.txt" in worktree root
		{"../../etc/passwd", http.StatusNotFound}, // parent dir "etc" doesn't exist
	}
	for _, tt := range tests {
		body := `{"content": "test"}`
		req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path="+tt.path, strings.NewReader(body))
		req.SetPathValue("name", "test-agent")
		req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != tt.wantStatus {
			t.Errorf("path=%q: status = %d, want %d", tt.path, w.Code, tt.wantStatus)
		}

		// Verify that successfully written files are inside the worktree, not outside
		if w.Code == http.StatusOK {
			// The file should exist inside dir (the worktree root)
			expectedFile := filepath.Join(dir, filepath.Base(tt.path))
			data, err := os.ReadFile(expectedFile)
			if err != nil {
				t.Errorf("path=%q: file not found at expected location %s: %v", tt.path, expectedFile, err)
			} else if string(data) != "test" {
				t.Errorf("path=%q: content = %q, want %q", tt.path, string(data), "test")
			}
		}
	}
}

func TestFileWrite_InvalidJSON(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	body := `{invalid json}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=test.txt", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestFileWrite_NoBody(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=test.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	req.Body = nil
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestFileWrite_ParentDirNotExist(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	body := `{"content": "data"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=nonexistent/file.txt", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestFileWrite_SymlinkParent(t *testing.T) {
	dir := setupTestWorktree(t)

	// Create a symlink directory
	symDir := filepath.Join(dir, "symparent")
	if err := os.Symlink("/tmp", symDir); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}

	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	body := `{"content": "evil"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=symparent/file.txt", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestFileWrite_OverwriteSymlink(t *testing.T) {
	dir := setupTestWorktree(t)

	// Create a symlink file
	symFile := filepath.Join(dir, "link.txt")
	if err := os.Symlink(filepath.Join(dir, "hello.txt"), symFile); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}

	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	body := `{"content": "evil"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=link.txt", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestFileWrite_AtomicIntegrity(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)
	handler := handleFileWrite(NewFileService(ops))

	content := strings.Repeat("X", 1000)
	body := `{"content": "` + content + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=atomic.txt", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify full content was written
	data, err := os.ReadFile(filepath.Join(dir, "atomic.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("content length = %d, want %d", len(data), len(content))
	}
}

// --- Content-Type tests ---

func TestFileHandlers_ContentType(t *testing.T) {
	dir := setupTestWorktree(t)
	ops := resolveToDir(dir)

	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
	}{
		{"tree", handleFileTree(NewFileService(ops)), http.MethodGet, "/api/agents/test-agent/files/tree"},
		{"read", handleFileRead(NewFileService(ops)), http.MethodGet, "/api/agents/test-agent/files?path=hello.txt"},
		{"write", handleFileWrite(NewFileService(ops)), http.MethodPut, "/api/agents/test-agent/files?path=ct-test.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body *strings.Reader
			if tt.method == http.MethodPut {
				body = strings.NewReader(`{"content": "test"}`)
			}
			var req *http.Request
			if body != nil {
				req = httptest.NewRequest(tt.method, tt.path, body)
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}
			req.SetPathValue("name", "test-agent")
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
			w := httptest.NewRecorder()

			tt.handler.ServeHTTP(w, req)

			ct := w.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}
		})
	}
}

// --- isDeniedPath tests ---

func TestIsDeniedPath(t *testing.T) {
	tests := []struct {
		path   string
		denied bool
	}{
		{"file.txt", false},
		{"main.go", false},
		{"secret.key", true},
		{"cert.pem", true},
		{"store.p12", true},
		{"keystore.pfx", true},
		{"config.env", true},
		{"message.gpg", true},
		{"signature.asc", true},
		{"id_rsa", true},
		{"id_ed25519", true},
		{"id_ecdsa", true},
		{"id_dsa", true},
		{".env", true},
		{".env.local", true},
		{".env.production", true},
		{".netrc", true},
		{"subdir/id_rsa", true},
		{"subdir/file.txt", false},
		{"ID_RSA", true},     // case-insensitive filename check
		{".ENV", true},       // case-insensitive filename check
		{"SECRET.KEY", true}, // case-insensitive extension check
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isDeniedPath(tt.path); got != tt.denied {
				t.Errorf("isDeniedPath(%q) = %v, want %v", tt.path, got, tt.denied)
			}
		})
	}
}

// --- isBinaryContent tests ---

func TestIsBinaryContent(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		binary bool
	}{
		{"text", []byte("Hello, world!"), false},
		{"empty", []byte{}, false},
		{"binary", []byte{0x00, 0xFF, 0xFE}, true},
		{"utf8", []byte("日本語テキスト"), false},
		{"null byte", []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F, 0x00}, true},
		{"invalid utf8 sequence", []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F, 0xC0, 0xAF}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBinaryContent(tt.data); got != tt.binary {
				t.Errorf("isBinaryContent(%v) = %v, want %v", tt.data, got, tt.binary)
			}
		})
	}
}
