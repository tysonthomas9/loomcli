package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// --- handleDiffCommits tests ---

func TestDiffCommits_Success(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	gitOps.diffCommitsFunc = func(_, mergeBase string, limit int) ([]ops.DiffCommitResult, error) {
		return []ops.DiffCommitResult{
			{Hash: "aaa111", ShortHash: "aaa", Subject: "first commit", Author: "Alice", Email: "a@b.com", Date: "2026-01-01"},
			{Hash: "bbb222", ShortHash: "bbb", Subject: "second commit", Author: "Bob", Email: "b@b.com", Date: "2026-01-02"},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffCommits(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp diffResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be a map")
	}
	commits, ok := data["commits"].([]interface{})
	if !ok {
		t.Fatal("expected commits to be an array")
	}
	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}
}

func TestDiffCommits_NoAgent(t *testing.T) {
	gitOps := &mockGitOps{
		resolveFunc: func(name string) (*ops.AgentWorktree, error) {
			return nil, errors.New("not found")
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent/diff/commits", nil)
	req.SetPathValue("name", "nonexistent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffCommits(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDiffCommits_MergeBaseError(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "", errors.New("no common ancestor")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffCommits(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDiffCommits_WithLimit(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	var capturedLimit int
	gitOps.diffCommitsFunc = func(_, _ string, limit int) ([]ops.DiffCommitResult, error) {
		capturedLimit = limit
		return []ops.DiffCommitResult{
			{Hash: "aaa111", ShortHash: "aaa", Subject: "first commit", Author: "Alice", Email: "a@b.com", Date: "2026-01-01"},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits?limit=1", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffCommits(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if capturedLimit != 1 {
		t.Fatalf("expected limit=1 forwarded to mock, got %d", capturedLimit)
	}
}

func TestDiffCommits_EmptyResult(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	gitOps.diffCommitsFunc = func(_, _ string, _ int) ([]ops.DiffCommitResult, error) {
		return nil, nil // return nil slice
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffCommits(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify JSON has [] not null
	var raw map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	data := raw["data"].(map[string]interface{})
	commits := data["commits"].([]interface{})
	if commits == nil {
		t.Fatal("expected empty array, got null")
	}
	if len(commits) != 0 {
		t.Fatalf("expected 0 commits, got %d", len(commits))
	}
}

// --- handleDiffFiles tests ---

func TestDiffFiles_Success(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	gitOps.diffFilesFunc = func(_, from, to string) ([]ops.DiffFileResult, error) {
		return []ops.DiffFileResult{
			{Path: "main.go", Status: "M", Additions: 10, Deletions: 5},
			{Path: "new.go", Status: "A", Additions: 20, Deletions: 0},
			{Path: "old.go", Status: "D", Additions: 0, Deletions: 15},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFiles(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp diffResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	data := resp.Data.(map[string]interface{})
	files := data["files"].([]interface{})
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
}

func TestDiffFiles_MissingTo(t *testing.T) {
	gitOps := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFiles(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFiles_InvalidRef(t *testing.T) {
	gitOps := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=../bad", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFiles(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFiles_DefaultFrom(t *testing.T) {
	gitOps := resolveOK()
	var capturedFrom string
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "merge-base-sha", nil
	}
	gitOps.diffFilesFunc = func(_, from, to string) ([]ops.DiffFileResult, error) {
		capturedFrom = from
		return []ops.DiffFileResult{}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFiles(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if capturedFrom != "merge-base-sha" {
		t.Fatalf("expected from=merge-base-sha, got %q", capturedFrom)
	}
}

func TestDiffFiles_ExplicitFrom(t *testing.T) {
	gitOps := resolveOK()
	var capturedFrom, capturedTo string
	gitOps.diffFilesFunc = func(_, from, to string) ([]ops.DiffFileResult, error) {
		capturedFrom = from
		capturedTo = to
		return []ops.DiffFileResult{}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD&from=abc123", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFiles(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if capturedFrom != "abc123" {
		t.Fatalf("expected from=abc123, got %q", capturedFrom)
	}
	if capturedTo != "HEAD" {
		t.Fatalf("expected to=HEAD, got %q", capturedTo)
	}
}

// --- handleDiffFile tests ---

func TestDiffFile_Success(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	gitOps.diffFilePatchFunc = func(_, _, _, path string) (*ops.DiffFilePatchResult, error) {
		return &ops.DiffFilePatchResult{
			Patch:     "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,4 @@\n+new line\n",
			IsBinary:  false,
			Additions: 1,
			Deletions: 0,
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=main.go&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp diffResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
}

func TestDiffFile_MissingPath(t *testing.T) {
	gitOps := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFile_MissingTo(t *testing.T) {
	gitOps := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=main.go", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFile_PathTraversal(t *testing.T) {
	gitOps := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=../../etc/passwd&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFile_DotPath(t *testing.T) {
	gitOps := resolveOK()

	// "." would trigger a repo-wide diff instead of a single file
	for _, p := range []string{".", "./", "./."} {
		req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path="+p+"&to=HEAD", nil)
		req.SetPathValue("name", "test-agent")
		req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
		w := httptest.NewRecorder()

		handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("path=%q: status = %d, want %d", p, w.Code, http.StatusBadRequest)
		}
	}
}

func TestDiffFile_AbsolutePath(t *testing.T) {
	gitOps := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=/etc/passwd&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFile_BinaryFile(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	gitOps.diffFilePatchFunc = func(_, _, _, _ string) (*ops.DiffFilePatchResult, error) {
		return &ops.DiffFilePatchResult{
			IsBinary: true,
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=image.png&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp diffResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	data := resp.Data.(map[string]interface{})
	if data["is_binary"] != true {
		t.Fatal("expected is_binary=true")
	}
}

func TestDiffFile_TooLarge(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	gitOps.diffFilePatchFunc = func(_, _, _, _ string) (*ops.DiffFilePatchResult, error) {
		return &ops.DiffFilePatchResult{
			IsTooLarge: true,
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=huge.sql&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp diffResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	data := resp.Data.(map[string]interface{})
	if data["is_too_large"] != true {
		t.Fatal("expected is_too_large=true")
	}
}

// --- Additional coverage tests ---

// TestDiffFiles_EmptyDiff tests handleDiffFiles when DiffFiles returns an empty (nil) slice.
func TestDiffFiles_EmptyDiff(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	gitOps.diffFilesFunc = func(_, _, _ string) ([]ops.DiffFileResult, error) {
		return nil, nil // nil slice => should be normalized to []
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFiles(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var raw map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	data := raw["data"].(map[string]interface{})
	files := data["files"].([]interface{})
	if files == nil {
		t.Fatal("expected empty array, got null")
	}
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}
}

// TestDiffFiles_DiffFilesError tests handleDiffFiles when DiffFiles returns an error.
func TestDiffFiles_DiffFilesError(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	gitOps.diffFilesFunc = func(_, _, _ string) ([]ops.DiffFileResult, error) {
		return nil, errors.New("pool timeout")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFiles(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var resp diffResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
	if resp.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}

// TestDiffFiles_MergeBaseError tests handleDiffFiles when merge-base resolution fails.
func TestDiffFiles_MergeBaseError(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "", errors.New("no common ancestor")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFiles(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// TestDiffFiles_InvalidFromRef tests handleDiffFiles with invalid "from" query param.
func TestDiffFiles_InvalidFromRef(t *testing.T) {
	gitOps := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD&from=../bad", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFiles(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestDiffFiles_NoAgent tests handleDiffFiles when agent resolution fails.
func TestDiffFiles_NoAgent(t *testing.T) {
	gitOps := &mockGitOps{
		resolveFunc: func(name string) (*ops.AgentWorktree, error) {
			return nil, errors.New("not found")
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "nonexistent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFiles(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestDiffFile_InvalidPathChars tests handleDiffFile with various invalid file paths.
func TestDiffFile_InvalidPathChars(t *testing.T) {
	gitOps := resolveOK()

	tests := []struct {
		name string
		path string
	}{
		{"dotdot only", ".."},
		{"dotdot prefix", "../secret/file"},
		{"absolute unix", "/etc/shadow"},
		{"empty string", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/api/agents/test-agent/diff/file?to=HEAD&path=" + tt.path
			req := httptest.NewRequest(http.MethodGet, url, nil)
			req.SetPathValue("name", "test-agent")
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
			w := httptest.NewRecorder()

			handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("path=%q: status = %d, want %d", tt.path, w.Code, http.StatusBadRequest)
			}
		})
	}
}

// TestDiffFile_DiffFilePatchError tests handleDiffFile when DiffFilePatch returns an error.
func TestDiffFile_DiffFilePatchError(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	gitOps.diffFilePatchFunc = func(_, _, _, _ string) (*ops.DiffFilePatchResult, error) {
		return nil, errors.New("git diff failed")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=main.go&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var resp diffResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
	if resp.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}

// TestDiffFile_InvalidToRef tests handleDiffFile with invalid "to" ref.
func TestDiffFile_InvalidToRef(t *testing.T) {
	gitOps := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=main.go&to=abc..def", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestDiffFile_MergeBaseError tests handleDiffFile when merge-base resolution fails.
func TestDiffFile_MergeBaseError(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "", errors.New("no merge base found")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=main.go&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var resp diffResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
}

// TestDiffFile_NoAgent tests handleDiffFile when agent resolution fails.
func TestDiffFile_NoAgent(t *testing.T) {
	gitOps := &mockGitOps{
		resolveFunc: func(name string) (*ops.AgentWorktree, error) {
			return nil, errors.New("not found")
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent/diff/file?path=main.go&to=HEAD", nil)
	req.SetPathValue("name", "nonexistent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffFile(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestDiffCommits_DiffCommitsError tests handleDiffCommits when DiffCommits returns an error.
func TestDiffCommits_DiffCommitsError(t *testing.T) {
	gitOps := resolveOK()
	gitOps.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	gitOps.diffCommitsFunc = func(_, _ string, _ int) ([]ops.DiffCommitResult, error) {
		return nil, errors.New("pool timeout")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffCommits(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var resp diffResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
	if resp.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}

// TestDiffCommits_InvalidLimit tests handleDiffCommits with an invalid limit value.
func TestDiffCommits_InvalidLimit(t *testing.T) {
	gitOps := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits?limit=abc", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffCommits(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestDiffCommits_ExplicitFrom tests handleDiffCommits with explicit from parameter.
func TestDiffCommits_ExplicitFrom(t *testing.T) {
	gitOps := resolveOK()
	var capturedMergeBase string
	gitOps.diffCommitsFunc = func(_, mergeBase string, _ int) ([]ops.DiffCommitResult, error) {
		capturedMergeBase = mergeBase
		return []ops.DiffCommitResult{}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits?from=deadbeef", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffCommits(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if capturedMergeBase != "deadbeef" {
		t.Fatalf("expected from=deadbeef, got %q", capturedMergeBase)
	}
}

// TestDiffCommits_InvalidFrom tests handleDiffCommits with an invalid from ref.
func TestDiffCommits_InvalidFrom(t *testing.T) {
	gitOps := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits?from=abc..def", nil)
	req.SetPathValue("name", "test-agent")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handleDiffCommits(NewDiffService(gitOps, nil)).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestValidateDiffPath_Table(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		// Valid paths
		{"simple file", "main.go", true},
		{"nested path", "subdir/file.go", true},
		{"deeply nested", "a/b/c.txt", true},

		// Reject empty
		{"empty string", "", false},

		// Reject absolute paths
		{"absolute unix", "/etc/passwd", false},

		// Reject traversal
		{"dotdot only", "..", false},
		{"dotdot prefix", "../secret", false},
		{"deep traversal", "subdir/../../../etc/passwd", false},

		// Reject dot path (current directory)
		{"dot path", ".", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateDiffPath(tt.path)
			if got != tt.want {
				t.Errorf("validateDiffPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
