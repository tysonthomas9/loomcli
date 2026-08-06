package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestAgentCreateBodyRuntimeProvider(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/agents" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		var provider domain.RuntimeProvider
		if err := json.Unmarshal(body["runtime_provider"], &provider); err != nil {
			t.Fatalf("decode runtime_provider: %v", err)
		}
		if provider != domain.RuntimeProviderDaytona {
			t.Fatalf("runtime_provider = %q, want daytona", provider)
		}
		writeJSON(t, w, agentWire{
			WorkspaceKey:    "WS",
			Name:            "nova",
			RoleName:        "lead",
			RuntimeProvider: string(provider),
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := client.Agents().Create(t.Context(), store.AgentCreate{
		WorkspaceKey:    "WS",
		Name:            "nova",
		RoleName:        "lead",
		RuntimeProvider: domain.RuntimeProviderDaytona,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if agent.RuntimeProvider != domain.RuntimeProviderDaytona {
		t.Fatalf("RuntimeProvider = %q, want daytona", agent.RuntimeProvider)
	}
}

func TestAgentUpdateBodyRuntimeProvider(t *testing.T) {
	provider := domain.RuntimeProviderDaytona
	patch := store.AgentUpdate{RuntimeProvider: &provider}
	body := agentUpdateBody(patch)
	if got, ok := body["runtime_provider"]; !ok || got != string(provider) {
		t.Fatalf("runtime_provider = %#v, present = %v; want %q", got, ok, provider)
	}
	if !agentUpdateHasFleetDBFields(patch) {
		t.Fatal("runtime_provider-only patch is not recognized as a fleet-db field")
	}

	body = agentUpdateBody(store.AgentUpdate{})
	if _, ok := body["runtime_provider"]; ok {
		t.Fatalf("empty patch contains runtime_provider: %#v", body)
	}
}
