package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/editor"
)

// fakeDetectedEditors returns a detection function with a fixed result.
func fakeDetectedEditors(detected []editor.DetectedEditor) editorDetectFunc {
	return func() []editor.DetectedEditor { return detected }
}

// fakeLauncher returns a launch function that records the call.
func fakeLauncher(errToReturn error) (editorLaunchFunc, *editor.DetectedEditor, *[]string) {
	var calledWith editor.DetectedEditor
	var calledPaths []string
	fn := func(de editor.DetectedEditor, targets []string) error {
		calledWith = de
		calledPaths = targets
		return errToReturn
	}
	return fn, &calledWith, &calledPaths
}

// --- GET /api/editors tests ---

func TestHandleListEditors_ReturnsAllWithDetectedFlags(t *testing.T) {
	detected := []editor.DetectedEditor{
		{Editor: editor.Editor{ID: "vscode"}, ResolvedPath: "/usr/bin/code", Method: "cli"},
	}
	cache := newEditorCache(time.Minute, fakeDetectedEditors(detected))
	handler := handleListEditors(cache)

	req := httptest.NewRequest(http.MethodGet, "/api/editors", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp EditorsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if resp.Data == nil {
		t.Fatal("expected data to be non-nil")
	}

	if len(resp.Data.Editors) != len(editor.Registry) {
		t.Fatalf("expected %d editors, got %d", len(editor.Registry), len(resp.Data.Editors))
	}

	// vscode should be detected, others should not.
	for _, e := range resp.Data.Editors {
		if e.ID == "vscode" {
			if !e.Detected {
				t.Error("expected vscode to be detected")
			}
		} else {
			if e.Detected {
				t.Errorf("expected %s to not be detected", e.ID)
			}
		}
	}
}

func TestHandleListEditors_NoneDetected(t *testing.T) {
	cache := newEditorCache(time.Minute, fakeDetectedEditors(nil))
	handler := handleListEditors(cache)

	req := httptest.NewRequest(http.MethodGet, "/api/editors", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp EditorsListResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Data == nil {
		t.Fatal("expected data to be non-nil")
	}
	for _, e := range resp.Data.Editors {
		if e.Detected {
			t.Errorf("expected %s to not be detected", e.ID)
		}
	}
}

func TestEditorCache_RefreshesAfterTTL(t *testing.T) {
	callCount := 0
	detect := func() []editor.DetectedEditor {
		callCount++
		return nil
	}
	cache := newEditorCache(10*time.Millisecond, detect)

	cache.get()
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	// Within TTL, should not re-detect.
	cache.get()
	if callCount != 1 {
		t.Fatalf("expected still 1 call, got %d", callCount)
	}

	// After TTL, should re-detect.
	time.Sleep(15 * time.Millisecond)
	cache.get()
	if callCount != 2 {
		t.Fatalf("expected 2 calls, got %d", callCount)
	}
}

// --- POST /api/editors/open validation tests ---

func TestHandleOpenEditor_OversizedBody(t *testing.T) {
	cache := newEditorCache(time.Minute, fakeDetectedEditors(nil))
	launch, _, _ := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	// Create a JSON body larger than 1MB (maxRequestBody).
	big := `{"editor_id":"` + strings.Repeat("x", 1<<20+1) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(big))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleOpenEditor_EmptyBody(t *testing.T) {
	cache := newEditorCache(time.Minute, fakeDetectedEditors(nil))
	launch, _, _ := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(""))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleOpenEditor_MissingEditorID(t *testing.T) {
	cache := newEditorCache(time.Minute, fakeDetectedEditors(nil))
	launch, _, _ := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	body := `{"path":"/tmp/test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleOpenEditor_InvalidEditorIDFormat(t *testing.T) {
	cache := newEditorCache(time.Minute, fakeDetectedEditors(nil))
	launch, _, _ := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	body := `{"editor_id":"VS Code!","path":"/tmp/test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleOpenEditor_MissingPath(t *testing.T) {
	cache := newEditorCache(time.Minute, fakeDetectedEditors(nil))
	launch, _, _ := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	body := `{"editor_id":"vscode"}`
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleOpenEditor_RelativePath(t *testing.T) {
	cache := newEditorCache(time.Minute, fakeDetectedEditors(nil))
	launch, _, _ := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	body := `{"editor_id":"vscode","path":"relative/path"}`
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	assertErrorContains(t, rec, "path must be absolute")
}

func TestHandleOpenEditor_PathTraversal(t *testing.T) {
	cache := newEditorCache(time.Minute, fakeDetectedEditors(nil))
	launch, _, _ := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	body := `{"editor_id":"vscode","path":"/tmp/../etc/passwd"}`
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleOpenEditor_NonexistentPath(t *testing.T) {
	cache := newEditorCache(time.Minute, fakeDetectedEditors(nil))
	launch, _, _ := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	body := `{"editor_id":"vscode","path":"/nonexistent/path/xyz"}`
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- POST /api/editors/open editor lookup tests ---

func TestHandleOpenEditor_UnknownEditorID(t *testing.T) {
	cache := newEditorCache(time.Minute, fakeDetectedEditors(nil))
	launch, _, _ := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	dir := t.TempDir()
	body := fmt.Sprintf(`{"editor_id":"nonexistent-editor","path":%q}`, dir)
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	assertErrorContains(t, rec, "unknown editor")
}

func TestHandleOpenEditor_EditorNotDetected(t *testing.T) {
	// No editors detected, but vscode exists in registry.
	cache := newEditorCache(time.Minute, fakeDetectedEditors(nil))
	launch, _, _ := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	dir := t.TempDir()
	body := fmt.Sprintf(`{"editor_id":"vscode","path":%q}`, dir)
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
	assertErrorContains(t, rec, "editor not detected")
}

// --- POST /api/editors/open launch tests ---

func TestHandleOpenEditor_Success(t *testing.T) {
	detected := []editor.DetectedEditor{
		{Editor: editor.Editor{ID: "vscode"}, ResolvedPath: "/usr/bin/code", Method: "cli"},
	}
	cache := newEditorCache(time.Minute, fakeDetectedEditors(detected))
	launch, calledDE, calledPaths := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	dir := t.TempDir()
	body := fmt.Sprintf(`{"editor_id":"vscode","path":%q}`, dir)
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if calledDE.ID != "vscode" {
		t.Errorf("expected launch called with vscode, got %s", calledDE.ID)
	}
	cleanDir := filepath.Clean(dir)
	if len(*calledPaths) != 1 || (*calledPaths)[0] != cleanDir {
		t.Errorf("expected launch called with [%s], got %v", cleanDir, *calledPaths)
	}
}

func TestHandleOpenEditor_LaunchFailure(t *testing.T) {
	detected := []editor.DetectedEditor{
		{Editor: editor.Editor{ID: "vscode"}, ResolvedPath: "/usr/bin/code", Method: "cli"},
	}
	cache := newEditorCache(time.Minute, fakeDetectedEditors(detected))
	launch, _, _ := fakeLauncher(fmt.Errorf("spawn failed"))
	handler := handleOpenEditor(cache, launch)

	dir := t.TempDir()
	body := fmt.Sprintf(`{"editor_id":"vscode","path":%q}`, dir)
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	assertErrorContains(t, rec, "failed to launch editor")
}

func TestHandleOpenEditor_FilePathInsteadOfDir(t *testing.T) {
	detected := []editor.DetectedEditor{
		{Editor: editor.Editor{ID: "vscode"}, ResolvedPath: "/usr/bin/code", Method: "cli"},
	}
	cache := newEditorCache(time.Minute, fakeDetectedEditors(detected))
	launch, _, _ := fakeLauncher(nil)
	handler := handleOpenEditor(cache, launch)

	// Create a temp file (not directory) — should still work.
	dir := t.TempDir()
	file := filepath.Join(dir, "test.go")
	os.WriteFile(file, []byte("package main"), 0644)

	body := fmt.Sprintf(`{"editor_id":"vscode","path":%q}`, file)
	req := httptest.NewRequest(http.MethodPost, "/api/editors/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- helpers ---

func assertErrorContains(t *testing.T, rec *httptest.ResponseRecorder, substr string) {
	t.Helper()
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp["error"], substr) {
		t.Errorf("expected error to contain %q, got %q", substr, resp["error"])
	}
}
