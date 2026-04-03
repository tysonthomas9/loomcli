package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestHandleAgentDiffStat(t *testing.T) {
	t.Run("happy path returns diff stats", func(t *testing.T) {
		svc := &mockAgentService{
			getDiffStatFunc: func(_ context.Context, wsID, agentName string) (*AgentDiffStatResult, error) {
				return &AgentDiffStatResult{
					Branch:  "webui/" + agentName,
					Added:   366,
					Removed: 12,
				}, nil
			},
		}

		handler := handleAgentDiffStat(svc)

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
		svc := &mockAgentService{
			getDiffStatFunc: func(_ context.Context, _, agentName string) (*AgentDiffStatResult, error) {
				return nil, service.ErrValidation("missing agent name")
			},
		}
		handler := handleAgentDiffStat(svc)

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
		svc := &mockAgentService{
			getDiffStatFunc: func(_ context.Context, _, agentName string) (*AgentDiffStatResult, error) {
				return nil, service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", agentName))
			},
		}
		handler := handleAgentDiffStat(svc)

		req := httptest.NewRequest("GET", "/api/workspaces/ws1/agents/unknown/git/diff-stat", nil)
		req.SetPathValue("name", "unknown")
		req = req.WithContext(WithWorkspace(req.Context(), "ws1"))

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("missing workspace ID still calls service", func(t *testing.T) {
		svc := &mockAgentService{
			getDiffStatFunc: func(_ context.Context, wsID, agentName string) (*AgentDiffStatResult, error) {
				return nil, service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", agentName))
			},
		}
		handler := handleAgentDiffStat(svc)

		req := httptest.NewRequest("GET", "/api/agents/falcon/git/diff-stat", nil)
		req.SetPathValue("name", "falcon")
		// No workspace in context

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("zero lines changed returns valid response", func(t *testing.T) {
		svc := &mockAgentService{
			getDiffStatFunc: func(_ context.Context, _, agentName string) (*AgentDiffStatResult, error) {
				return &AgentDiffStatResult{
					Branch: "webui/" + agentName,
				}, nil
			},
		}

		handler := handleAgentDiffStat(svc)

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
