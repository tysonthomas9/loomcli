package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDaemonSupervisorAndConfigHandlers(t *testing.T) {
	started := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	supervisor := HandleDaemonSupervisor(func() (*DaemonSupervisorData, error) {
		return &DaemonSupervisorData{
			PID:           123,
			StartedAt:     started,
			UptimeSeconds: 5,
			Agents:        []DaemonAgentEntry{{Worktree: "nova", Role: "task", Status: "running"}},
		}, nil
	})
	rec := httptest.NewRecorder()
	supervisor.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/daemon/supervisor", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"worktree":"nova"`) {
		t.Fatalf("supervisor success status=%d body=%s", rec.Code, rec.Body.String())
	}

	supervisorMissing := HandleDaemonSupervisor(func() (*DaemonSupervisorData, error) {
		return nil, os.ErrNotExist
	})
	rec = httptest.NewRecorder()
	supervisorMissing.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/daemon/supervisor", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("supervisor missing status=%d body=%s", rec.Code, rec.Body.String())
	}

	supervisorError := HandleDaemonSupervisor(func() (*DaemonSupervisorData, error) {
		return nil, errors.New("boom")
	})
	rec = httptest.NewRecorder()
	supervisorError.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/daemon/supervisor", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("supervisor error status=%d body=%s", rec.Code, rec.Body.String())
	}

	configHandler := HandleDaemonConfig(func() (json.RawMessage, error) {
		return json.RawMessage(`{"agents":[{"name":"nova"}]}`), nil
	})
	rec = httptest.NewRecorder()
	configHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/daemon/config", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"agents"`) {
		t.Fatalf("config success status=%d body=%s", rec.Code, rec.Body.String())
	}

	configError := HandleDaemonConfig(func() (json.RawMessage, error) {
		return nil, errors.New("bad config")
	})
	rec = httptest.NewRecorder()
	configError.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/daemon/config", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("config error status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAgentQueueHandlerBranches(t *testing.T) {
	handler := HandleAgentQueue(func(name string) ([]AgentQueueEntry, error) {
		if name != "nova" {
			t.Fatalf("name = %q, want nova", name)
		}
		return []AgentQueueEntry{{IssueID: "loom-1", Title: "Task", Priority: 1, Score: 10, Labels: []string{"api"}}}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agents/nova/queue", nil)
	req.SetPathValue("name", "nova")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"issue_id":"loom-1"`) {
		t.Fatalf("queue success status=%d body=%s", rec.Code, rec.Body.String())
	}

	notFound := HandleAgentQueue(func(string) ([]AgentQueueEntry, error) {
		return nil, ErrAgentNotFound
	})
	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agents/missing/queue", nil)
	req.SetPathValue("name", "missing")
	rec = httptest.NewRecorder()
	notFound.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("queue not found status=%d body=%s", rec.Code, rec.Body.String())
	}

	unavailable := HandleAgentQueue(func(string) ([]AgentQueueEntry, error) {
		return nil, errors.New("socket down")
	})
	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agents/nova/queue", nil)
	req.SetPathValue("name", "nova")
	rec = httptest.NewRecorder()
	unavailable.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("queue unavailable status=%d body=%s", rec.Code, rec.Body.String())
	}
}
