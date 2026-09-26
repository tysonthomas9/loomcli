package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/store"
)

// Execution must round-trip through Create (sent in the body) and the
// agentWire→domain projection (returned), so a `sandbox` agent definition
// survives the loom⇄fleet-db boundary.
func TestAgentStoreCreateRoundTripsExecution(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS1/agents" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["execution"] != "sandbox" {
			t.Fatalf("create body execution = %v, want sandbox", body["execution"])
		}
		writeJSON(t, w, map[string]any{
			"workspace_key": "WS1", "name": "coder", "role_name": "task",
			"execution": "sandbox", "state": "idle",
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := client.Agents().Create(t.Context(), store.AgentCreate{
		WorkspaceKey: "WS1", Name: "coder", RoleName: "task", Execution: "sandbox",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.Execution != "sandbox" {
		t.Errorf("returned Execution = %q, want sandbox", a.Execution)
	}
}
