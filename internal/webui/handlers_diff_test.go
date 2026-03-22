package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- handleDiffCommits tests ---

func TestDiffCommits_Success(t *testing.T) {
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	ops.diffCommitsFunc = func(_, mergeBase string, limit int) ([]DiffCommitResult, error) {
		return []DiffCommitResult{
			{Hash: "aaa111", ShortHash: "aaa", Subject: "first commit", Author: "Alice", Email: "a@b.com", Date: "2026-01-01"},
			{Hash: "bbb222", ShortHash: "bbb", Subject: "second commit", Author: "Bob", Email: "b@b.com", Date: "2026-01-02"},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffCommits(ops).ServeHTTP(w, req)

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
	ops := &mockGitOps{
		resolveFunc: func(name string) (*AgentWorktree, error) {
			return nil, errors.New("not found")
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent/diff/commits", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()

	handleDiffCommits(ops).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDiffCommits_MergeBaseError(t *testing.T) {
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "", errors.New("no common ancestor")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffCommits(ops).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDiffCommits_WithLimit(t *testing.T) {
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	var capturedLimit int
	ops.diffCommitsFunc = func(_, _ string, limit int) ([]DiffCommitResult, error) {
		capturedLimit = limit
		return []DiffCommitResult{
			{Hash: "aaa111", ShortHash: "aaa", Subject: "first commit", Author: "Alice", Email: "a@b.com", Date: "2026-01-01"},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits?limit=1", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffCommits(ops).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if capturedLimit != 1 {
		t.Fatalf("expected limit=1 forwarded to mock, got %d", capturedLimit)
	}
}

func TestDiffCommits_EmptyResult(t *testing.T) {
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	ops.diffCommitsFunc = func(_, _ string, _ int) ([]DiffCommitResult, error) {
		return nil, nil // return nil slice
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffCommits(ops).ServeHTTP(w, req)

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
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	ops.diffFilesFunc = func(_, from, to string) ([]DiffFileResult, error) {
		return []DiffFileResult{
			{Path: "main.go", Status: "M", Additions: 10, Deletions: 5},
			{Path: "new.go", Status: "A", Additions: 20, Deletions: 0},
			{Path: "old.go", Status: "D", Additions: 0, Deletions: 15},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFiles(ops).ServeHTTP(w, req)

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
	ops := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFiles(ops).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFiles_InvalidRef(t *testing.T) {
	ops := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=../bad", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFiles(ops).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFiles_DefaultFrom(t *testing.T) {
	ops := resolveOK()
	var capturedFrom string
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "merge-base-sha", nil
	}
	ops.diffFilesFunc = func(_, from, to string) ([]DiffFileResult, error) {
		capturedFrom = from
		return []DiffFileResult{}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFiles(ops).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if capturedFrom != "merge-base-sha" {
		t.Fatalf("expected from=merge-base-sha, got %q", capturedFrom)
	}
}

func TestDiffFiles_ExplicitFrom(t *testing.T) {
	ops := resolveOK()
	var capturedFrom, capturedTo string
	ops.diffFilesFunc = func(_, from, to string) ([]DiffFileResult, error) {
		capturedFrom = from
		capturedTo = to
		return []DiffFileResult{}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD&from=abc123", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFiles(ops).ServeHTTP(w, req)

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
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	ops.diffFilePatchFunc = func(_, _, _, path string) (*DiffFilePatchResult, error) {
		return &DiffFilePatchResult{
			Patch:     "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,4 @@\n+new line\n",
			IsBinary:  false,
			Additions: 1,
			Deletions: 0,
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=main.go&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFile(ops).ServeHTTP(w, req)

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
	ops := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFile(ops).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFile_MissingTo(t *testing.T) {
	ops := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=main.go", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFile(ops).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFile_PathTraversal(t *testing.T) {
	ops := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=../../etc/passwd&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFile(ops).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFile_DotPath(t *testing.T) {
	ops := resolveOK()

	// "." would trigger a repo-wide diff instead of a single file
	for _, p := range []string{".", "./", "./."} {
		req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path="+p+"&to=HEAD", nil)
		req.SetPathValue("name", "test-agent")
		w := httptest.NewRecorder()

		handleDiffFile(ops).ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("path=%q: status = %d, want %d", p, w.Code, http.StatusBadRequest)
		}
	}
}

func TestDiffFile_AbsolutePath(t *testing.T) {
	ops := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=/etc/passwd&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFile(ops).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDiffFile_BinaryFile(t *testing.T) {
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	ops.diffFilePatchFunc = func(_, _, _, _ string) (*DiffFilePatchResult, error) {
		return &DiffFilePatchResult{
			IsBinary: true,
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=image.png&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFile(ops).ServeHTTP(w, req)

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
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	ops.diffFilePatchFunc = func(_, _, _, _ string) (*DiffFilePatchResult, error) {
		return &DiffFilePatchResult{
			IsTooLarge: true,
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=huge.sql&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFile(ops).ServeHTTP(w, req)

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
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	ops.diffFilesFunc = func(_, _, _ string) ([]DiffFileResult, error) {
		return nil, nil // nil slice => should be normalized to []
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFiles(ops).ServeHTTP(w, req)

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
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	ops.diffFilesFunc = func(_, _, _ string) ([]DiffFileResult, error) {
		return nil, errors.New("pool timeout")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFiles(ops).ServeHTTP(w, req)

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
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "", errors.New("no common ancestor")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFiles(ops).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// TestDiffFiles_InvalidFromRef tests handleDiffFiles with invalid "from" query param.
func TestDiffFiles_InvalidFromRef(t *testing.T) {
	ops := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/files?to=HEAD&from=../bad", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFiles(ops).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestDiffFiles_NoAgent tests handleDiffFiles when agent resolution fails.
func TestDiffFiles_NoAgent(t *testing.T) {
	ops := &mockGitOps{
		resolveFunc: func(name string) (*AgentWorktree, error) {
			return nil, errors.New("not found")
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent/diff/files?to=HEAD", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()

	handleDiffFiles(ops).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestDiffFile_InvalidPathChars tests handleDiffFile with various invalid file paths.
func TestDiffFile_InvalidPathChars(t *testing.T) {
	ops := resolveOK()

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
			w := httptest.NewRecorder()

			handleDiffFile(ops).ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("path=%q: status = %d, want %d", tt.path, w.Code, http.StatusBadRequest)
			}
		})
	}
}

// TestDiffFile_DiffFilePatchError tests handleDiffFile when DiffFilePatch returns an error.
func TestDiffFile_DiffFilePatchError(t *testing.T) {
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	ops.diffFilePatchFunc = func(_, _, _, _ string) (*DiffFilePatchResult, error) {
		return nil, errors.New("git diff failed")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=main.go&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFile(ops).ServeHTTP(w, req)

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
	ops := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=main.go&to=abc..def", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFile(ops).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestDiffFile_MergeBaseError tests handleDiffFile when merge-base resolution fails.
func TestDiffFile_MergeBaseError(t *testing.T) {
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "", errors.New("no merge base found")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/file?path=main.go&to=HEAD", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffFile(ops).ServeHTTP(w, req)

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
	ops := &mockGitOps{
		resolveFunc: func(name string) (*AgentWorktree, error) {
			return nil, errors.New("not found")
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent/diff/file?path=main.go&to=HEAD", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()

	handleDiffFile(ops).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestDiffCommits_DiffCommitsError tests handleDiffCommits when DiffCommits returns an error.
func TestDiffCommits_DiffCommitsError(t *testing.T) {
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "abc123", nil
	}
	ops.diffCommitsFunc = func(_, _ string, _ int) ([]DiffCommitResult, error) {
		return nil, errors.New("pool timeout")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffCommits(ops).ServeHTTP(w, req)

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
	ops := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits?limit=abc", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffCommits(ops).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestDiffCommits_ExplicitFrom tests handleDiffCommits with explicit from parameter.
func TestDiffCommits_ExplicitFrom(t *testing.T) {
	ops := resolveOK()
	var capturedMergeBase string
	ops.diffCommitsFunc = func(_, mergeBase string, _ int) ([]DiffCommitResult, error) {
		capturedMergeBase = mergeBase
		return []DiffCommitResult{}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits?from=deadbeef", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffCommits(ops).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if capturedMergeBase != "deadbeef" {
		t.Fatalf("expected from=deadbeef, got %q", capturedMergeBase)
	}
}

// TestDiffCommits_InvalidFrom tests handleDiffCommits with an invalid from ref.
func TestDiffCommits_InvalidFrom(t *testing.T) {
	ops := resolveOK()

	req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent/diff/commits?from=abc..def", nil)
	req.SetPathValue("name", "test-agent")
	w := httptest.NewRecorder()

	handleDiffCommits(ops).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- resolveMergeBaseDefault tests ---

// TestResolveMergeBaseDefault_ExplicitFrom tests with a valid explicit from param.
func TestResolveMergeBaseDefault_ExplicitFrom(t *testing.T) {
	ops := resolveOK()
	wt := testWorktree()

	req := httptest.NewRequest(http.MethodGet, "/test?from=abc123", nil)
	w := httptest.NewRecorder()

	from, ok := resolveMergeBaseDefault(w, req, ops, wt)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if from != "abc123" {
		t.Fatalf("expected from=abc123, got %q", from)
	}
}

// TestResolveMergeBaseDefault_InvalidFrom tests with an invalid from param.
func TestResolveMergeBaseDefault_InvalidFrom(t *testing.T) {
	ops := resolveOK()
	wt := testWorktree()

	req := httptest.NewRequest(http.MethodGet, "/test?from=abc..def", nil)
	w := httptest.NewRecorder()

	_, ok := resolveMergeBaseDefault(w, req, ops, wt)
	if ok {
		t.Fatal("expected ok=false for invalid from ref")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestResolveMergeBaseDefault_NoMergeBase tests when ResolveMergeBase returns an error.
func TestResolveMergeBaseDefault_NoMergeBase(t *testing.T) {
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, _ string) (string, error) {
		return "", errors.New("no merge base found")
	}
	wt := testWorktree()

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	_, ok := resolveMergeBaseDefault(w, req, ops, wt)
	if ok {
		t.Fatal("expected ok=false when merge base not found")
	}
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

// TestResolveMergeBaseDefault_FallbackToMergeBase tests the default path that
// calls ResolveMergeBase when no from param is provided.
func TestResolveMergeBaseDefault_FallbackToMergeBase(t *testing.T) {
	ops := resolveOK()
	ops.resolveMergeBaseFunc = func(_, branch string) (string, error) {
		if branch != "main" {
			t.Errorf("expected default branch 'main', got %q", branch)
		}
		return "resolved-sha", nil
	}
	wt := testWorktree()

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	from, ok := resolveMergeBaseDefault(w, req, ops, wt)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if from != "resolved-sha" {
		t.Fatalf("expected from=resolved-sha, got %q", from)
	}
}

// TestResolveMergeBaseDefault_FromWithTraversal tests that ".." in from ref is rejected.
func TestResolveMergeBaseDefault_FromWithTraversal(t *testing.T) {
	ops := resolveOK()
	wt := testWorktree()

	req := httptest.NewRequest(http.MethodGet, "/test?from=HEAD..main", nil)
	w := httptest.NewRecorder()

	_, ok := resolveMergeBaseDefault(w, req, ops, wt)
	if ok {
		t.Fatal("expected ok=false for from ref containing '..'")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
