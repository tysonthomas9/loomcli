package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/commits"
)

func TestHandleGetIssueCommits_NoFile(t *testing.T) {
	dir := t.TempDir()
	handler := handleGetIssueCommits(dir)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/TASK-1/commits", nil)
	req.SetPathValue("id", "TASK-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CommitsResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(resp.Data) != 0 {
		t.Errorf("expected empty data array, got %d items", len(resp.Data))
	}
}

func TestHandleGetIssueCommits_ReturnsCommitsForIssue(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	// Write commits for two different tasks
	commits.Append(dir, commits.Record{TaskID: "TASK-1", SHA: "aaa111", Subject: "first", Author: "alice", Timestamp: ts})
	commits.Append(dir, commits.Record{TaskID: "TASK-2", SHA: "bbb222", Subject: "other", Author: "bob", Timestamp: ts.Add(time.Hour)})
	commits.Append(dir, commits.Record{TaskID: "TASK-1", SHA: "ccc333", Subject: "second", Author: "alice", Timestamp: ts.Add(2 * time.Hour)})

	handler := handleGetIssueCommits(dir)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/TASK-1/commits", nil)
	req.SetPathValue("id", "TASK-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CommitsResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 commits for TASK-1, got %d", len(resp.Data))
	}
	for _, c := range resp.Data {
		if c.TaskID != "TASK-1" {
			t.Errorf("expected all commits to have task ID TASK-1, got %s", c.TaskID)
		}
	}
}

func TestHandleGetIssueCommits_MissingIssueID(t *testing.T) {
	dir := t.TempDir()
	handler := handleGetIssueCommits(dir)

	req := httptest.NewRequest(http.MethodGet, "/api/issues//commits", nil)
	// Do not set path value to simulate missing ID
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CommitsResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Success {
		t.Fatal("expected failure for missing issue ID")
	}
	if resp.Error != "missing issue ID" {
		t.Errorf("expected 'missing issue ID' error, got: %s", resp.Error)
	}
}

func TestHandleGetIssueCommits_EmptyBeadsDir(t *testing.T) {
	handler := handleGetIssueCommits("")

	req := httptest.NewRequest(http.MethodGet, "/api/issues/TASK-1/commits", nil)
	req.SetPathValue("id", "TASK-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CommitsResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Success {
		t.Fatal("expected failure for empty beadsDir")
	}
	if resp.Error != "beads directory not configured" {
		t.Errorf("expected 'beads directory not configured' error, got: %s", resp.Error)
	}
}

func TestHandleGetIssueCommits_RespectsLimitParam(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	// Write 5 commits for the same task
	for i := 0; i < 5; i++ {
		commits.Append(dir, commits.Record{
			TaskID:    "TASK-1",
			SHA:       "sha" + string(rune('a'+i)),
			Subject:   "commit",
			Author:    "alice",
			Timestamp: ts.Add(time.Duration(i) * time.Hour),
		})
	}

	handler := handleGetIssueCommits(dir)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/TASK-1/commits?limit=2", nil)
	req.SetPathValue("id", "TASK-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CommitsResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 commits with limit=2, got %d", len(resp.Data))
	}
}

func TestHandleGetIssueCommits_InvalidLimitIgnored(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	commits.Append(dir, commits.Record{TaskID: "TASK-1", SHA: "aaa111", Subject: "first", Author: "alice", Timestamp: ts})
	commits.Append(dir, commits.Record{TaskID: "TASK-1", SHA: "bbb222", Subject: "second", Author: "alice", Timestamp: ts.Add(time.Hour)})

	handler := handleGetIssueCommits(dir)

	// Invalid limit should be ignored and default used
	req := httptest.NewRequest(http.MethodGet, "/api/issues/TASK-1/commits?limit=notanumber", nil)
	req.SetPathValue("id", "TASK-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CommitsResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	// Both records should be returned since invalid limit falls back to default (100)
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 commits, got %d", len(resp.Data))
	}
}

func TestHandleGetIssueCommits_ResponseContentType(t *testing.T) {
	dir := t.TempDir()
	handler := handleGetIssueCommits(dir)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/TASK-1/commits", nil)
	req.SetPathValue("id", "TASK-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}
}

func TestHandleGetIssueCommits_CorruptFile(t *testing.T) {
	dir := t.TempDir()

	// Write a valid commit followed by corrupt data
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	rec := commits.Record{TaskID: "TASK-1", SHA: "aaa111", Subject: "valid", Author: "alice", Timestamp: ts}
	data, _ := json.Marshal(rec)
	content := string(data) + "\nnot valid json\n"
	os.WriteFile(filepath.Join(dir, "commits.jsonl"), []byte(content), 0644)

	handler := handleGetIssueCommits(dir)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/TASK-1/commits", nil)
	req.SetPathValue("id", "TASK-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	// Should still succeed with the valid record (malformed lines are skipped)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var resp CommitsResponse
	json.NewDecoder(recorder.Body).Decode(&resp)

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(resp.Data) != 1 {
		t.Errorf("expected 1 valid commit, got %d", len(resp.Data))
	}
}
