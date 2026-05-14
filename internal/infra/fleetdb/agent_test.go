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

func TestFleetDBAgentCreateOmitsUnsupportedOrchestratorSessionID(t *testing.T) {
	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/agents" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		if _, ok := body["orchestrator_session_id"]; ok {
			t.Fatalf("create body contains unsupported orchestrator_session_id: %#v", body)
		}
		if body["name"] != "worker" || body["role_name"] != "task" {
			t.Fatalf("create body = %#v", body)
		}
		writeJSON(t, w, domain.Agent{
			WorkspaceKey: "WS",
			Name:         "worker",
			RoleName:     "task",
			State:        domain.AgentStateIdle,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := client.Agents().Create(t.Context(), store.AgentCreate{
		WorkspaceKey:          "WS",
		Name:                  "worker",
		RoleName:              "task",
		OrchestratorSessionID: "lead-session",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if agent.Name != "worker" || agent.RoleName != "task" {
		t.Fatalf("agent = %+v", agent)
	}
}

func TestFleetDBAgentUpdateOmitsUnsupportedOrchestratorSessionID(t *testing.T) {
	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/WS/agents/lead" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode update: %v", err)
		}
		if _, ok := body["orchestrator_session_id"]; ok {
			t.Fatalf("update body contains unsupported orchestrator_session_id: %#v", body)
		}
		if body["backend"] != "codex" {
			t.Fatalf("update body = %#v", body)
		}
		writeJSON(t, w, domain.Agent{
			WorkspaceKey: "WS",
			Name:         "lead",
			RoleName:     "lead",
			Backend:      "codex",
			State:        domain.AgentStateIdle,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	backend := "codex"
	orchestratorID := "lead-session"
	agent, err := client.Agents().Update(t.Context(), "WS", "lead", store.AgentUpdate{
		Backend:               &backend,
		OrchestratorSessionID: &orchestratorID,
	})
	if err != nil {
		t.Fatalf("update agent: %v", err)
	}
	if agent.Backend != "codex" {
		t.Fatalf("backend = %q, want codex", agent.Backend)
	}
}

func TestFleetDBAgentUpdateOrchestratorSessionOnlyUsesGet(t *testing.T) {
	now := time.Now().UTC()
	var sawPatch bool
	var sawGet bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/agents/lead":
			sawPatch = true
			t.Fatalf("orchestrator-only update should not PATCH unsupported fleet-db field")
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agents/lead":
			sawGet = true
			writeJSON(t, w, domain.Agent{
				WorkspaceKey: "WS",
				Name:         "lead",
				RoleName:     "lead",
				State:        domain.AgentStateIdle,
				CreatedAt:    now,
				UpdatedAt:    now,
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	orchestratorID := "lead-session"
	agent, err := client.Agents().Update(t.Context(), "WS", "lead", store.AgentUpdate{
		OrchestratorSessionID: &orchestratorID,
	})
	if err != nil {
		t.Fatalf("update agent: %v", err)
	}
	if sawPatch || !sawGet {
		t.Fatalf("sawPatch=%v sawGet=%v", sawPatch, sawGet)
	}
	if agent.Name != "lead" {
		t.Fatalf("agent = %+v", agent)
	}
}
