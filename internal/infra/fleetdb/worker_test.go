package fleetdb

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWorkerStoreHeartbeatAndDeregister verifies the worker client hits the
// fleet-db worker endpoints with the right method + path and tolerates the
// heartbeat 200-body / deregister 204-no-body shapes.
func TestWorkerStoreHeartbeatAndDeregister(t *testing.T) {
	var sawHeartbeat, sawDeregister bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/workers/agent-1/heartbeat":
			sawHeartbeat = true
			writeJSON(t, w, map[string]any{"success": true, "ttl": 90})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/WS/workers/agent-1":
			sawDeregister = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}

	if err := client.Workers().Heartbeat(t.Context(), "WS", "agent-1"); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := client.Workers().Deregister(t.Context(), "WS", "agent-1"); err != nil {
		t.Fatalf("deregister: %v", err)
	}
	if !sawHeartbeat || !sawDeregister {
		t.Fatalf("sawHeartbeat=%v sawDeregister=%v", sawHeartbeat, sawDeregister)
	}
}
