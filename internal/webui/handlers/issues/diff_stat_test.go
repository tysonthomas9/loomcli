package issues

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// --- handleGetIssueDiffStat tests ---

func TestHandleGetIssueDiffStat_Success(t *testing.T) {
	issueData, _ := json.Marshal(map[string]string{"assignee": "falcon"})

	socketPath := startHandlersMockServer(t, func(req rpc.Request) rpc.Response {
		if resp, ok := defaultHealthPingHandler(req); ok {
			return resp
		}
		switch req.Operation {
		case "show":
			return rpc.Response{Success: true, Data: issueData}
		default:
			return rpc.Response{Success: false, Error: "unknown: " + req.Operation}
		}
	})

	pool := newHandlersMockPool(t, socketPath)

	wt := testWorktree()
	gitOps := &mockGitOps{
		resolveFunc: func(name string) (*ops.AgentWorktree, error) {
			if name != "falcon" {
				t.Errorf("resolveFunc name = %q, want %q", name, "falcon")
			}
			return wt, nil
		},
		diffStatFunc: func(worktreePath, fromRef string) ops.DiffStatResult {
			if worktreePath != wt.Path {
				t.Errorf("diffStatFunc worktreePath = %q, want %q", worktreePath, wt.Path)
			}
			if fromRef != wt.DefaultBranch {
				t.Errorf("diffStatFunc fromRef = %q, want %q", fromRef, wt.DefaultBranch)
			}
			return ops.DiffStatResult{
				FilesChanged: 3,
				LinesAdded:   42,
				LinesRemoved: 7,
			}
		},
	}

	handler := handleGetIssueDiffStat(NewDiffService(gitOps, pool))

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/git/diff-stat", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp DiffStatResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Branch != wt.Branch {
		t.Errorf("Branch = %q, want %q", resp.Branch, wt.Branch)
	}
	if resp.Added != 42 {
		t.Errorf("Added = %d, want %d", resp.Added, 42)
	}
	if resp.Removed != 7 {
		t.Errorf("Removed = %d, want %d", resp.Removed, 7)
	}
}

func TestHandleGetIssueDiffStat_MissingIssueID(t *testing.T) {
	// Pool and gitOps won't be reached — handler returns early.
	pool, err := daemon.NewConnectionPool("/tmp/unused.sock", 1)
	if err != nil {
		t.Fatalf("NewConnectionPool error: %v", err)
	}
	defer pool.Close()

	handler := handleGetIssueDiffStat(NewDiffService(&mockGitOps{}, pool))

	req := httptest.NewRequest(http.MethodGet, "/api/issues//git/diff-stat", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp["error"] != "missing issue ID" {
		t.Errorf("error = %q, want %q", resp["error"], "missing issue ID")
	}
}

func TestHandleGetIssueDiffStat_IssueNotFound(t *testing.T) {
	socketPath := startHandlersMockServer(t, func(req rpc.Request) rpc.Response {
		if resp, ok := defaultHealthPingHandler(req); ok {
			return resp
		}
		switch req.Operation {
		case "show":
			return rpc.Response{Success: false, Error: "not found: nonexistent-id"}
		default:
			return rpc.Response{Success: false, Error: "unknown: " + req.Operation}
		}
	})

	pool := newHandlersMockPool(t, socketPath)
	handler := handleGetIssueDiffStat(NewDiffService(&mockGitOps{}, pool))

	req := httptest.NewRequest(http.MethodGet, "/api/issues/nonexistent-id/git/diff-stat", nil)
	req.SetPathValue("id", "nonexistent-id")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.Contains(resp["error"], "issue not found") {
		t.Errorf("error = %q, want to contain 'issue not found'", resp["error"])
	}
}

func TestHandleGetIssueDiffStat_NoAssignee(t *testing.T) {
	issueData, _ := json.Marshal(map[string]string{"assignee": ""})

	socketPath := startHandlersMockServer(t, func(req rpc.Request) rpc.Response {
		if resp, ok := defaultHealthPingHandler(req); ok {
			return resp
		}
		switch req.Operation {
		case "show":
			return rpc.Response{Success: true, Data: issueData}
		default:
			return rpc.Response{Success: false, Error: "unknown: " + req.Operation}
		}
	})

	pool := newHandlersMockPool(t, socketPath)
	handler := handleGetIssueDiffStat(NewDiffService(&mockGitOps{}, pool))

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/git/diff-stat", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.Contains(resp["error"], "no assignee") {
		t.Errorf("error = %q, want to contain 'no assignee'", resp["error"])
	}
}

func TestHandleGetIssueDiffStat_WorktreeNotFound(t *testing.T) {
	issueData, _ := json.Marshal(map[string]string{"assignee": "ghost-agent"})

	socketPath := startHandlersMockServer(t, func(req rpc.Request) rpc.Response {
		if resp, ok := defaultHealthPingHandler(req); ok {
			return resp
		}
		switch req.Operation {
		case "show":
			return rpc.Response{Success: true, Data: issueData}
		default:
			return rpc.Response{Success: false, Error: "unknown: " + req.Operation}
		}
	})

	pool := newHandlersMockPool(t, socketPath)
	gitOps := &mockGitOps{
		resolveFunc: func(name string) (*ops.AgentWorktree, error) {
			return nil, errors.New("worktree not found")
		},
	}

	handler := handleGetIssueDiffStat(NewDiffService(gitOps, pool))

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/git/diff-stat", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.Contains(resp["error"], "agent worktree not found") {
		t.Errorf("error = %q, want to contain 'agent worktree not found'", resp["error"])
	}
}

func TestHandleGetIssueDiffStat_DaemonUnavailable(t *testing.T) {
	// Create a pool pointed at a non-existent socket to simulate daemon unavailability.
	pool, err := daemon.NewConnectionPool("/tmp/nonexistent-socket-for-diffstat-test.sock", 1)
	if err != nil {
		t.Fatalf("NewConnectionPool error: %v", err)
	}
	pool.SetDialTimeout(100 * 1e6) // 100ms to fail fast
	pool.SetPoolTimeout(100 * 1e6) // 100ms
	defer pool.Close()

	handler := handleGetIssueDiffStat(NewDiffService(&mockGitOps{}, pool))

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/git/diff-stat", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.Contains(resp["error"], "issue backend unavailable") {
		t.Errorf("error = %q, want to contain 'issue backend unavailable'", resp["error"])
	}
}
