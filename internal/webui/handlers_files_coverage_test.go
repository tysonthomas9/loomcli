package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- handleFileRead additional coverage ---

func TestHandleFileRead_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	// Create an empty file
	if err := os.WriteFile(filepath.Join(dir, "empty.txt"), []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	ops := resolveToDir(dir)
	handler := handleFileRead(ops)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=empty.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp fileReadResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("content = %q, want empty string", resp.Content)
	}
	if resp.Size != 0 {
		t.Errorf("size = %d, want 0", resp.Size)
	}
	if resp.Binary {
		t.Error("expected binary = false for empty file")
	}
}

func TestHandleFileRead_PathTraversalVariants(t *testing.T) {
	dir := t.TempDir()
	// Create a file so the worktree is not empty
	if err := os.WriteFile(filepath.Join(dir, "safe.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	ops := resolveToDir(dir)
	handler := handleFileRead(ops)

	// All traversal attempts should be rejected (403) or not found (404), never 200
	paths := []string{
		"../../../etc/passwd",
		"..%2F..%2Fetc/passwd", // URL-encoded traversal (stays literal in query)
		"subdir/../../../etc/shadow",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path="+p, nil)
		req.SetPathValue("name", "test-agent")
		req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code == http.StatusOK {
			t.Errorf("path=%q: got 200, expected rejection", p)
		}
	}
}

func TestHandleFileRead_DeniedPathOnFullPath(t *testing.T) {
	// Test that the second isDeniedPath check (on fullPath) catches denied files
	// even when the reqPath itself doesn't trigger it.
	dir := t.TempDir()
	// Create a file with denied extension in a subdirectory
	sub := filepath.Join(dir, "config")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "secrets.env"), []byte("SECRET=yes"), 0644); err != nil {
		t.Fatal(err)
	}

	ops := resolveToDir(dir)
	handler := handleFileRead(ops)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=config/secrets.env", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestHandleFileRead_UnreadableFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "noperm.txt")
	if err := os.WriteFile(filePath, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	// Remove read permission
	if err := os.Chmod(filePath, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filePath, 0644) })

	ops := resolveToDir(dir)
	handler := handleFileRead(ops)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=noperm.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should fail with 500 (can't open file) -- the Lstat succeeds but open fails
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

func TestHandleFileRead_BinaryContentWithNullByte(t *testing.T) {
	dir := t.TempDir()
	// Create a file that is valid UTF-8 but contains a null byte
	content := []byte("Hello\x00World")
	if err := os.WriteFile(filepath.Join(dir, "nullbyte.txt"), content, 0644); err != nil {
		t.Fatal(err)
	}

	ops := resolveToDir(dir)
	handler := handleFileRead(ops)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=nullbyte.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp fileReadResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Binary {
		t.Error("expected binary = true for file with null byte")
	}
	if resp.Content != "" {
		t.Errorf("expected empty content for binary file, got %q", resp.Content)
	}
}

// --- atomicWriteFile direct unit tests ---

func TestAtomicWriteFile_BasicWrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")

	err := atomicWriteFile(target, "hello world", 0644)
	if err != nil {
		t.Fatalf("atomicWriteFile() error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("content = %q, want %q", string(data), "hello world")
	}

	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0644 {
		t.Errorf("permissions = %o, want %o", fi.Mode().Perm(), 0644)
	}
}

func TestAtomicWriteFile_PreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "script.sh")

	err := atomicWriteFile(target, "#!/bin/sh\necho hello", 0755)
	if err != nil {
		t.Fatalf("atomicWriteFile() error: %v", err)
	}

	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Errorf("permissions = %o, want %o", fi.Mode().Perm(), 0755)
	}
}

func TestAtomicWriteFile_NonExistentDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nonexistent", "subdir", "file.txt")

	err := atomicWriteFile(target, "data", 0644)
	if err == nil {
		t.Fatal("expected error for non-existent parent directory")
	}
	// The error should come from CreateTemp failing
	if got := err.Error(); !contains(got, "create temp") {
		t.Errorf("error = %q, want it to contain %q", got, "create temp")
	}
}

func TestAtomicWriteFile_TempFileCleanupOnWriteError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")

	// Write initial file successfully
	if err := atomicWriteFile(target, "initial", 0644); err != nil {
		t.Fatalf("initial write error: %v", err)
	}

	// Now write to a read-only directory to cause rename to fail
	roDir := filepath.Join(dir, "readonly")
	if err := os.Mkdir(roDir, 0755); err != nil {
		t.Fatal(err)
	}
	roTarget := filepath.Join(roDir, "file.txt")

	// Write a file, then make directory read-only (which prevents creating new files)
	if err := atomicWriteFile(roTarget, "first", 0644); err != nil {
		t.Fatal(err)
	}
	// Make the directory non-writable so temp file creation fails
	if err := os.Chmod(roDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(roDir, 0755) })

	err := atomicWriteFile(roTarget, "second", 0644)
	if err == nil {
		t.Fatal("expected error writing to read-only directory")
	}

	// Verify no temp files left behind
	entries, _ := os.ReadDir(roDir)
	for _, e := range entries {
		if e.Name() != "file.txt" {
			t.Errorf("leftover temp file found: %s", e.Name())
		}
	}
}

func TestAtomicWriteFile_ConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "concurrent.txt")

	const goroutines = 10
	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			content := string(rune('A'+idx)) + " content"
			errs[idx] = atomicWriteFile(target, content, 0644)
		}(i)
	}
	wg.Wait()

	// All writes should succeed (no errors)
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: error = %v", i, err)
		}
	}

	// File should exist and contain one of the written values
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("file is empty after concurrent writes")
	}

	// No temp files should remain
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "concurrent.txt" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestAtomicWriteFile_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "overwrite.txt")

	// Write initial content
	if err := atomicWriteFile(target, "initial", 0644); err != nil {
		t.Fatal(err)
	}

	// Overwrite
	if err := atomicWriteFile(target, "updated", 0644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "updated" {
		t.Errorf("content = %q, want %q", string(data), "updated")
	}
}

func TestAtomicWriteFile_EmptyContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "empty.txt")

	if err := atomicWriteFile(target, "", 0644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "" {
		t.Errorf("content = %q, want empty", string(data))
	}
}

func TestAtomicWriteFile_PermissionDenied(t *testing.T) {
	dir := t.TempDir()

	// Create a read-only directory
	roDir := filepath.Join(dir, "noaccess")
	if err := os.Mkdir(roDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(roDir, 0755) })

	target := filepath.Join(roDir, "file.txt")
	err := atomicWriteFile(target, "data", 0644)
	if err == nil {
		t.Fatal("expected permission error")
	}
	if got := err.Error(); !contains(got, "create temp") {
		t.Errorf("error = %q, want it to contain %q", got, "create temp")
	}
}

// --- validateParentDir direct unit tests ---

func TestValidateParentDir_ValidParent(t *testing.T) {
	dir := t.TempDir()

	resolved, _ := filepath.EvalSymlinks(dir)
	fullPathResolved := filepath.Join(resolved, "newfile.txt")

	writeErr := validateParentDir(fullPathResolved, resolved)
	if writeErr != nil {
		t.Fatalf("unexpected error: %s", writeErr.Message)
	}
}

func TestValidateParentDir_NonExistentParent(t *testing.T) {
	dir := t.TempDir()
	fullPath := filepath.Join(dir, "nonexistent", "file.txt")

	writeErr := validateParentDir(fullPath, dir)
	if writeErr == nil {
		t.Fatal("expected error for non-existent parent")
	}
	if writeErr.Status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", writeErr.Status, http.StatusNotFound)
	}
}

func TestValidateParentDir_ParentIsFile(t *testing.T) {
	dir := t.TempDir()
	// Create a file where we pretend it's a directory
	filePath := filepath.Join(dir, "notadir")
	if err := os.WriteFile(filePath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	resolved, _ := filepath.EvalSymlinks(dir)
	fullPathResolved := filepath.Join(resolved, "notadir", "child.txt")

	writeErr := validateParentDir(fullPathResolved, resolved)
	if writeErr == nil {
		t.Fatal("expected error when parent is a file, not a directory")
	}
	if writeErr.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", writeErr.Status, http.StatusBadRequest)
	}
}

func TestValidateParentDir_SymlinkParent(t *testing.T) {
	dir := t.TempDir()
	// Create a real target dir and a symlink to it
	realDir := filepath.Join(dir, "realdir")
	if err := os.Mkdir(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	symDir := filepath.Join(dir, "symdir")
	if err := os.Symlink(realDir, symDir); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}

	fullPath := filepath.Join(symDir, "file.txt")

	resolved, _ := filepath.EvalSymlinks(dir)
	writeErr := validateParentDir(fullPath, resolved)
	if writeErr == nil {
		t.Fatal("expected error for symlink parent")
	}
	if writeErr.Status != http.StatusForbidden {
		t.Errorf("status = %d, want %d", writeErr.Status, http.StatusForbidden)
	}
}

func TestValidateParentDir_OutsideWorktree(t *testing.T) {
	dir := t.TempDir()
	otherDir := t.TempDir()

	resolved, _ := filepath.EvalSymlinks(dir)
	otherResolved, _ := filepath.EvalSymlinks(otherDir)

	fullPath := filepath.Join(otherResolved, "file.txt")

	writeErr := validateParentDir(fullPath, resolved)
	if writeErr == nil {
		t.Fatal("expected error for path outside worktree")
	}
	if writeErr.Status != http.StatusForbidden {
		t.Errorf("status = %d, want %d", writeErr.Status, http.StatusForbidden)
	}
}

// contains is a test helper for checking substrings.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsSubstring(s, substr)
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- handleFileRead: symlink file via Lstat check ---

func TestHandleFileRead_SymlinkFile(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(realFile, []byte("real content"), 0644); err != nil {
		t.Fatal(err)
	}
	symFile := filepath.Join(dir, "sym.txt")
	if err := os.Symlink(realFile, symFile); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}

	ops := resolveToDir(dir)
	handler := handleFileRead(ops)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=sym.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "symlink") {
		t.Errorf("expected error body to mention 'symlink', got: %s", body)
	}
}

func TestHandleFileRead_SymlinkPointingOutsideWorktree(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	symFile := filepath.Join(dir, "escape.txt")
	if err := os.Symlink(outsideFile, symFile); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}

	ops := resolveToDir(dir)
	handler := handleFileRead(ops)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files?path=escape.txt", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// --- atomicWriteFile: rename failure path ---

func TestAtomicWriteFile_RenameFailure(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "targetdir")
	if err := os.Mkdir(targetDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Make the target path a directory so os.Rename fails (can't rename file over directory)
	targetPath := filepath.Join(targetDir, "subdir")
	if err := os.Mkdir(targetPath, 0755); err != nil {
		t.Fatal(err)
	}

	err := atomicWriteFile(targetPath, "content", 0644)
	if err == nil {
		t.Fatal("expected error when renaming file over directory")
	}
	if !strings.Contains(err.Error(), "rename") {
		t.Errorf("expected error to contain 'rename', got: %v", err)
	}

	// Verify no temp files left behind (cleanup on failure)
	entries, _ := os.ReadDir(targetDir)
	for _, e := range entries {
		if e.Name() != "subdir" {
			t.Errorf("leftover temp file found: %s", e.Name())
		}
	}
}

func TestAtomicWriteFile_ChmodVerification(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "chmod-test.txt")

	err := atomicWriteFile(target, "restricted content", 0600)
	if err != nil {
		t.Fatalf("atomicWriteFile() error: %v", err)
	}

	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("permissions = %o, want %o", fi.Mode().Perm(), 0600)
	}
}

// --- handleFileTree: nested directories ---

func TestHandleFileTree_NestedDirectories(t *testing.T) {
	dir := t.TempDir()

	aDir := filepath.Join(dir, "a")
	bDir := filepath.Join(aDir, "b")
	cDir := filepath.Join(bDir, "c")
	if err := os.MkdirAll(cDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aDir, "file_a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bDir, "file_b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cDir, "file_c.txt"), []byte("c"), 0644); err != nil {
		t.Fatal(err)
	}

	ops := resolveToDir(dir)
	handler := handleFileTree(ops)

	// List root
	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("root: status = %d, want %d", w.Code, http.StatusOK)
	}
	var rootResp fileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&rootResp); err != nil {
		t.Fatalf("decode root: %v", err)
	}
	if len(rootResp.Entries) != 1 {
		t.Fatalf("root entries = %d, want 1", len(rootResp.Entries))
	}
	if rootResp.Entries[0].Name != "a" || !rootResp.Entries[0].IsDir {
		t.Errorf("root entry = %+v, want dir 'a'", rootResp.Entries[0])
	}

	// List "a" -- dirs-first sorting: "b" before "file_a.txt"
	req = httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree?path=a", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("a: status = %d, want %d", w.Code, http.StatusOK)
	}
	var aResp fileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&aResp); err != nil {
		t.Fatalf("decode a: %v", err)
	}
	if aResp.Path != "a" {
		t.Errorf("path = %q, want 'a'", aResp.Path)
	}
	if len(aResp.Entries) != 2 {
		t.Fatalf("a entries = %d, want 2", len(aResp.Entries))
	}
	if aResp.Entries[0].Name != "b" || !aResp.Entries[0].IsDir {
		t.Errorf("a[0] = %+v, want dir 'b'", aResp.Entries[0])
	}
	if aResp.Entries[1].Name != "file_a.txt" || aResp.Entries[1].IsDir {
		t.Errorf("a[1] = %+v, want file 'file_a.txt'", aResp.Entries[1])
	}

	// List "a/b"
	req = httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree?path=a/b", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("a/b: status = %d, want %d", w.Code, http.StatusOK)
	}
	var bResp fileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&bResp); err != nil {
		t.Fatalf("decode a/b: %v", err)
	}
	if bResp.Path != filepath.Join("a", "b") {
		t.Errorf("path = %q, want 'a/b'", bResp.Path)
	}
	if len(bResp.Entries) != 2 {
		t.Fatalf("a/b entries = %d, want 2", len(bResp.Entries))
	}

	// List "a/b/c" -- leaf directory
	req = httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree?path=a/b/c", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("a/b/c: status = %d, want %d", w.Code, http.StatusOK)
	}
	var cResp fileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&cResp); err != nil {
		t.Fatalf("decode a/b/c: %v", err)
	}
	if len(cResp.Entries) != 1 {
		t.Fatalf("a/b/c entries = %d, want 1", len(cResp.Entries))
	}
	if cResp.Entries[0].Name != "file_c.txt" {
		t.Errorf("a/b/c entry = %q, want 'file_c.txt'", cResp.Entries[0].Name)
	}
}

func TestHandleFileTree_NestedSymlinksSkipped(t *testing.T) {
	dir := t.TempDir()

	if err := os.Mkdir(filepath.Join(dir, "realdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "realfile.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp", filepath.Join(dir, "symdir")); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}
	if err := os.Symlink(filepath.Join(dir, "realfile.txt"), filepath.Join(dir, "symfile.txt")); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}

	ops := resolveToDir(dir)
	handler := handleFileTree(ops)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp fileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.Entries) != 2 {
		t.Fatalf("entries = %d, want 2 (symlinks should be skipped)", len(resp.Entries))
	}
	names := map[string]bool{}
	for _, e := range resp.Entries {
		names[e.Name] = true
	}
	if !names["realdir"] {
		t.Error("expected 'realdir' in entries")
	}
	if !names["realfile.txt"] {
		t.Error("expected 'realfile.txt' in entries")
	}
}

func TestHandleFileTree_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	emptyDir := filepath.Join(dir, "empty")
	if err := os.Mkdir(emptyDir, 0755); err != nil {
		t.Fatal(err)
	}

	ops := resolveToDir(dir)
	handler := handleFileTree(ops)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree?path=empty", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp fileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("entries = %d, want 0 for empty directory", len(resp.Entries))
	}
}

func TestHandleFileTree_DirsSortedBeforeFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aaa.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "zzz_dir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bbb.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "aaa_dir"), 0755); err != nil {
		t.Fatal(err)
	}

	ops := resolveToDir(dir)
	handler := handleFileTree(ops)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/files/tree", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp fileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(resp.Entries))
	}
	if !resp.Entries[0].IsDir || resp.Entries[0].Name != "aaa_dir" {
		t.Errorf("entry[0] = %+v, want dir 'aaa_dir'", resp.Entries[0])
	}
	if !resp.Entries[1].IsDir || resp.Entries[1].Name != "zzz_dir" {
		t.Errorf("entry[1] = %+v, want dir 'zzz_dir'", resp.Entries[1])
	}
	if resp.Entries[2].IsDir || resp.Entries[2].Name != "aaa.txt" {
		t.Errorf("entry[2] = %+v, want file 'aaa.txt'", resp.Entries[2])
	}
	if resp.Entries[3].IsDir || resp.Entries[3].Name != "bbb.txt" {
		t.Errorf("entry[3] = %+v, want file 'bbb.txt'", resp.Entries[3])
	}
}

// --- handleFileWrite: validation errors ---

func TestHandleFileWrite_DeniedPathOnFullPath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "config")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}

	ops := resolveToDir(dir)
	handler := handleFileWrite(ops)

	body := `{"content": "SECRET=yes"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=config/.env.local", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestHandleFileWrite_SymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(realFile, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	symFile := filepath.Join(dir, "linked.txt")
	if err := os.Symlink(realFile, symFile); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}

	ops := resolveToDir(dir)
	handler := handleFileWrite(ops)

	body := `{"content": "hacked"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=linked.txt", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}

	data, _ := os.ReadFile(realFile)
	if string(data) != "original" {
		t.Errorf("original file was modified: %q", string(data))
	}
}

func TestHandleFileWrite_NonexistentDeepParent(t *testing.T) {
	dir := t.TempDir()
	ops := resolveToDir(dir)
	handler := handleFileWrite(ops)

	body := `{"content": "data"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=deep/nested/nonexistent/file.txt", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleFileWrite_EmptyContent(t *testing.T) {
	dir := t.TempDir()
	ops := resolveToDir(dir)
	handler := handleFileWrite(ops)

	body := `{"content": ""}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=empty_write.txt", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	data, err := os.ReadFile(filepath.Join(dir, "empty_write.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "" {
		t.Errorf("content = %q, want empty", string(data))
	}
}

func TestHandleFileWrite_InvalidAgent(t *testing.T) {
	ops := &mockFileOps{}
	handler := handleFileWrite(ops)

	body := `{"content": "data"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/bad.agent/files?path=test.txt", strings.NewReader(body))
	req.SetPathValue("name", "bad.agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleFileWrite_ParentIsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notadir"), []byte("file"), 0644); err != nil {
		t.Fatal(err)
	}

	ops := resolveToDir(dir)
	handler := handleFileWrite(ops)

	body := `{"content": "data"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/test-agent/files?path=notadir/child.txt", strings.NewReader(body))
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// validatePathWithinDir rejects first because EvalSymlinks fails on notadir/child.txt
	// (notadir is a file, not a directory), returning 403 before validateParentDir runs
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}
