package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestControlPlaneClientNodeLifecycle(t *testing.T) {
	now := time.Now().UTC()
	var sawCreate bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/nodes":
			sawCreate = true
			var req struct {
				NodeID     string `json:"node_id"`
				TTLSeconds int    `json:"ttl_seconds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create: %v", err)
			}
			if req.NodeID != "node-1" || req.TTLSeconds != 30 {
				t.Fatalf("create body = %+v", req)
			}
			writeJSON(t, w, domain.Node{WorkspaceKey: "WS", NodeID: req.NodeID, LastHeartbeat: now, ExpiresAt: now.Add(30 * time.Second), CreatedAt: now, UpdatedAt: now})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/nodes/node-1/heartbeat":
			if r.URL.Query().Get("ttl_seconds") != "45" {
				t.Fatalf("ttl_seconds = %q", r.URL.Query().Get("ttl_seconds"))
			}
			writeJSON(t, w, domain.Node{WorkspaceKey: "WS", NodeID: "node-1", LastHeartbeat: now, ExpiresAt: now.Add(45 * time.Second), CreatedAt: now, UpdatedAt: now})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := client.Nodes().Create(t.Context(), store.NodeCreate{WorkspaceKey: "WS", NodeID: "node-1", TTL: 30 * time.Second})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if !sawCreate || node.NodeID != "node-1" {
		t.Fatalf("node = %+v sawCreate=%v", node, sawCreate)
	}
	if _, err := client.Nodes().Heartbeat(t.Context(), "WS", "node-1", 45*time.Second); err != nil {
		t.Fatalf("heartbeat node: %v", err)
	}
}

func TestControlPlaneClientAgentSessionListQuery(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/agent-sessions" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		q := r.URL.Query()
		if q.Get("agent_id") != "agent-1" || q.Get("node_id") != "node-1" || q.Get("task_id") != "T-1" || q.Get("status") != "running" || q.Get("limit") != "2" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		writeJSON(t, w, map[string]any{"agent_sessions": []domain.AgentSession{{WorkspaceKey: "WS", SessionID: "sess-1", AgentID: "agent-1"}}})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := client.AgentSessions().List(t.Context(), "WS", store.AgentSessionFilter{
		AgentID: "agent-1",
		NodeID:  "node-1",
		TaskID:  "T-1",
		Status:  domain.AgentSessionRunning,
		Limit:   2,
	})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "sess-1" {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestControlPlaneClientAgentSessionUpdateBodyUsesWireNames(t *testing.T) {
	finishedAt := time.Now().UTC()
	exitCode := 7
	finishedAtPtr := &finishedAt
	exitCodePtr := &exitCode
	status := domain.AgentSessionFailed
	taskID := "T-1"
	errClass := "Fatal"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/WS/agent-sessions/sess-1" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode update: %v", err)
		}
		if _, ok := body["Status"]; ok {
			t.Fatalf("body contains Go field name Status: %#v", body)
		}
		if _, ok := body["NodeID"]; ok {
			t.Fatalf("body contains nil Go field NodeID: %#v", body)
		}
		if _, ok := body["node_id"]; ok {
			t.Fatalf("body contains nil wire field node_id: %#v", body)
		}
		if body["task_id"] != "T-1" || body["status"] != "failed" || body["error_class"] != "Fatal" || body["exit_code"] != float64(7) {
			t.Fatalf("body = %#v", body)
		}
		if body["finished_at"] == nil {
			t.Fatalf("body missing finished_at: %#v", body)
		}
		writeJSON(t, w, domain.AgentSession{
			WorkspaceKey: "WS",
			SessionID:    "sess-1",
			AgentID:      "agent-1",
			TaskID:       "T-1",
			Status:       domain.AgentSessionFailed,
			ExitCode:     &exitCode,
			FinishedAt:   &finishedAt,
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.AgentSessions().Update(t.Context(), "WS", "sess-1", store.AgentSessionUpdate{
		TaskID:     &taskID,
		Status:     &status,
		FinishedAt: &finishedAtPtr,
		ErrorClass: &errClass,
		ExitCode:   &exitCodePtr,
	})
	if err != nil {
		t.Fatalf("update session: %v", err)
	}
	if session.TaskID != "T-1" || session.Status != domain.AgentSessionFailed {
		t.Fatalf("session = %+v", session)
	}
}

func TestControlPlaneClientAgentCommandCreateQueuesCommand(t *testing.T) {
	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/agent-commands" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		if body["status"] != string(domain.AgentCommandQueued) {
			t.Fatalf("command create status = %#v, want queued; body=%#v", body["status"], body)
		}
		if body["target_agent_id"] != "agent-1" || body["target_node_id"] != "node-1" || body["type"] != "start" {
			t.Fatalf("body = %#v", body)
		}
		writeJSON(t, w, domain.AgentCommand{
			WorkspaceKey:  "WS",
			CommandID:     "cmd-1",
			TargetAgentID: "agent-1",
			TargetNodeID:  "node-1",
			Type:          "start",
			Status:        domain.AgentCommandQueued,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := client.AgentCommands().Create(t.Context(), store.AgentCommandCreate{
		WorkspaceKey:  "WS",
		TargetAgentID: "agent-1",
		TargetNodeID:  "node-1",
		Type:          "start",
	})
	if err != nil {
		t.Fatalf("create command: %v", err)
	}
	if cmd.Status != domain.AgentCommandQueued {
		t.Fatalf("status = %q, want queued", cmd.Status)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
