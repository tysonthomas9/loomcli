package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleAgentDiffStat(t *testing.T) {
	t.Run("happy path returns diff stats", func(t *testing.T) {
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*AgentWorktree, error) {
				return &AgentWorktree{
					Name:          name,
					Path:          "/tmp/wt/" + name,
					Branch:        "webui/" + name,
					DefaultBranch: "main",
				}, nil
			},
			diffStatFunc: func(path, fromRef string) DiffStatResult {
				return DiffStatResult{
					FilesChanged: 5,
					LinesAdded:   366,
					LinesRemoved: 12,
				}
			},
		}

		handler := handleAgentDiffStat(gitOps)

		req := httptest.NewRequest("GET", "/api/workspaces/ws1/agents/falcon/git/diff-stat", nil)
		req.SetPathValue("name", "falcon")
		req = req.WithContext(WithWorkspace(req.Context(), "ws1"))

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		var resp DiffStatResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Branch != "webui/falcon" {
			t.Errorf("branch = %q, want %q", resp.Branch, "webui/falcon")
		}
		if resp.Added != 366 {
			t.Errorf("added = %d, want 366", resp.Added)
		}
		if resp.Removed != 12 {
			t.Errorf("removed = %d, want 12", resp.Removed)
		}
	})

	t.Run("missing agent name returns 400", func(t *testing.T) {
		gitOps := &mockGitOps{}
		handler := handleAgentDiffStat(gitOps)

		req := httptest.NewRequest("GET", "/api/workspaces/ws1/agents//git/diff-stat", nil)
		req.SetPathValue("name", "")
		req = req.WithContext(WithWorkspace(req.Context(), "ws1"))

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("agent not found returns 404", func(t *testing.T) {
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*AgentWorktree, error) {
				return nil, fmt.Errorf("not found")
			},
		}
		handler := handleAgentDiffStat(gitOps)

		req := httptest.NewRequest("GET", "/api/workspaces/ws1/agents/unknown/git/diff-stat", nil)
		req.SetPathValue("name", "unknown")
		req = req.WithContext(WithWorkspace(req.Context(), "ws1"))

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("missing workspace ID returns 400", func(t *testing.T) {
		gitOps := &mockGitOps{}
		handler := handleAgentDiffStat(gitOps)

		req := httptest.NewRequest("GET", "/api/agents/falcon/git/diff-stat", nil)
		req.SetPathValue("name", "falcon")
		// No workspace in context

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("zero lines changed returns valid response", func(t *testing.T) {
		gitOps := &mockGitOps{
			resolveFunc: func(name string) (*AgentWorktree, error) {
				return &AgentWorktree{
					Name:          name,
					Path:          "/tmp/wt/" + name,
					Branch:        "webui/" + name,
					DefaultBranch: "main",
				}, nil
			},
			diffStatFunc: func(path, fromRef string) DiffStatResult {
				return DiffStatResult{}
			},
		}

		handler := handleAgentDiffStat(gitOps)

		req := httptest.NewRequest("GET", "/api/workspaces/ws1/agents/idle-agent/git/diff-stat", nil)
		req.SetPathValue("name", "idle-agent")
		req = req.WithContext(WithWorkspace(req.Context(), "ws1"))

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		var resp DiffStatResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Added != 0 || resp.Removed != 0 {
			t.Errorf("expected 0/0, got %d/%d", resp.Added, resp.Removed)
		}
	})
}
