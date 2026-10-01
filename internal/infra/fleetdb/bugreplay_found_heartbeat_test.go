//go:build daemon_bugreplay

package fleetdb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Found bug #9: HTTP success can carry a failed worker heartbeat.
func TestBugReplay_Found9_WorkerHeartbeatReportsOwnershipLost(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/workers/worker-1/heartbeat" {
			t.Errorf("unexpected heartbeat request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"error":"ownership_lost"}`))
	}))
	defer ts.Close()
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Workers().Heartbeat(t.Context(), "WS", "worker-1"); err == nil || !strings.Contains(err.Error(), "ownership_lost") {
		t.Fatalf("heartbeat error = %v; want ownership_lost from the HTTP 200 response", err)
	}
}
