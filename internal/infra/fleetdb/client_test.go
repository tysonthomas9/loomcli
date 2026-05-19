package fleetdb

import (
	"net/http"
	"testing"
)

func TestClientAccessorsCloseAndAuthMutation(t *testing.T) {
	var calls int
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/headers" {
			t.Fatalf("path = %q, want /headers", r.URL.Path)
		}
		switch calls {
		case 1:
			if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
				t.Fatalf("first Authorization = %q", got)
			}
			if got := r.Header.Get("X-Fleet-API-Key"); got != "key-1" {
				t.Fatalf("first X-Fleet-API-Key = %q", got)
			}
		case 2:
			if got := r.Header.Get("Authorization"); got != "Bearer token-2" {
				t.Fatalf("second Authorization = %q", got)
			}
			if got := r.Header.Get("X-Fleet-API-Key"); got != "key-2" {
				t.Fatalf("second X-Fleet-API-Key = %q", got)
			}
			if got := r.Header.Get("X-Extra"); got != "yes" {
				t.Fatalf("second X-Extra = %q", got)
			}
		default:
			t.Fatalf("unexpected call %d", calls)
		}
		if got := r.Header.Get("X-Actor"); got != "tester" {
			t.Fatalf("X-Actor = %q", got)
		}
		writeJSON(t, w, map[string]string{"ok": "yes"})
	}))

	client, err := New(Config{
		BaseURL:    "http://fleet.test/",
		APIKey:     "key-1",
		Actor:      "tester",
		AuthToken:  "token-1",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.Workspaces() == nil || client.Repos() == nil || client.Agents() == nil ||
		client.Nodes() == nil || client.AgentSessions() == nil || client.TerminalSessions() == nil ||
		client.Artifacts() == nil || client.AgentLeases() == nil || client.AgentOwnershipLeases() == nil ||
		client.AgentCommands() == nil || client.Roles() == nil || client.Daemon() == nil {
		t.Fatal("one or more stores were nil")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var out map[string]string
	if err := client.do(t.Context(), http.MethodGet, "/headers", nil, &out); err != nil {
		t.Fatalf("first do: %v", err)
	}
	if out["ok"] != "yes" {
		t.Fatalf("first response = %#v", out)
	}
	client.SetAuthToken("token-2")
	client.SetAPIKey("key-2")
	if err := client.doWithHeaders(t.Context(), http.MethodGet, "/headers", nil, &out, map[string]string{"X-Extra": "yes"}); err != nil {
		t.Fatalf("second do: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestClientNewRequiresBaseURL(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New with empty BaseURL returned nil error")
	}
}
