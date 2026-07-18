package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProvisionScopedKeySendsWorkspaceRoleAndReturnsKey(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/admin/apikeys" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["actor_id"] != "sandbox:WS1:coder:1" || body["workspace"] != "WS1" || body["role"] != "developer" {
			t.Fatalf("provision body = %+v (want scoped developer)", body)
		}
		writeJSON(t, w, map[string]any{"key": "sk-minted", "actor_id": "sandbox:WS1:coder:1", "workspace": "WS1", "role": "developer"})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, APIKey: "admin-key", Actor: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := client.ProvisionScopedKey(t.Context(), "sandbox:WS1:coder:1", "WS1", "developer", 3600)
	if err != nil {
		t.Fatalf("ProvisionScopedKey: %v", err)
	}
	if key != "sk-minted" {
		t.Errorf("key = %q, want sk-minted", key)
	}
}

func TestRevokeKeyHitsDeleteRoute(t *testing.T) {
	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, APIKey: "admin-key", Actor: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RevokeKey(t.Context(), "sandbox:WS1:coder:1"); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/admin/apikeys/sandbox:WS1:coder:1" {
		t.Errorf("got %s %s, want DELETE /api/v1/admin/apikeys/sandbox:WS1:coder:1", gotMethod, gotPath)
	}
}
