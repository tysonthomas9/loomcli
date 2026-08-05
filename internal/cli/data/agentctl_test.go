package data

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// agentMockServer records observed request paths and bodies so tests can
// assert on them. Each handler writes back a MessageResponse envelope.
type agentMockServer struct {
	mu     sync.Mutex
	paths  []string
	bodies map[string]string
}

func newAgentMockServer(t *testing.T) (*httptest.Server, *agentMockServer) {
	t.Helper()
	state := &agentMockServer{
		bodies: map[string]string{},
	}
	mux := http.NewServeMux()
	registerAuthConfig(mux)

	// List endpoint.
	mux.HandleFunc("/api/workspaces/default/agents", func(w http.ResponseWriter, r *http.Request) {
		state.record(r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(agentsListEnvelope{
			Success: true,
			Data: []agentListEntry{
				{ID: "falcon", Name: "falcon", Kind: "prompt", Enabled: false, Behavior: agentListBehavior{RoleName: "task"}, WorkspaceKey: "default"},
				{ID: "nova", Name: "nova", Kind: "interactive", Enabled: true, Behavior: agentListBehavior{RoleName: "plan"}, WorkspaceKey: "default"},
			},
			Total: 2,
		})
	})

	// Canonical lifecycle endpoints return a settled 200 response.
	mux.HandleFunc("/api/workspaces/default/agents/falcon/stop", func(w http.ResponseWriter, r *http.Request) {
		state.record(r)
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<12))
		state.mu.Lock()
		state.bodies["/agents/falcon/stop"] = string(body)
		state.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(agentMessageEnvelope{Success: true, Message: `agent "falcon" stopped`})
	})

	for _, verb := range []string{"start", "restart"} {
		v := verb
		mux.HandleFunc("/api/workspaces/default/agents/falcon/"+v, func(w http.ResponseWriter, r *http.Request) {
			state.record(r)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(agentMessageEnvelope{Success: true, Message: `agent "falcon" ` + v})
		})
	}

	// 503 endpoint for runtime-unavailable test.
	mux.HandleFunc("/api/workspaces/default/agents/ghost/stop", func(w http.ResponseWriter, r *http.Request) {
		state.record(r)
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	return httptest.NewServer(mux), state
}

func (s *agentMockServer) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, r.URL.Path)
}

func (s *agentMockServer) seenPath(p string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, seen := range s.paths {
		if seen == p {
			return true
		}
	}
	return false
}

func setupAgentTest(t *testing.T, srvURL string) {
	t.Helper()
	resetClient()
	serverURL = srvURL
	t.Setenv("LOOM_WORKSPACE", "default")
	outputFormat = "text"
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
}

func TestAgentList(t *testing.T) {
	srv, state := newAgentMockServer(t)
	defer srv.Close()
	withDataClientState(t, func() {
		setupAgentTest(t, srv.URL)
		ctx := context.Background()
		cli, url, err := getHTTPClient()
		if err != nil {
			t.Fatalf("getHTTPClient: %v", err)
		}
		entries, err := fetchAgents(ctx, cli, url, "default")
		if err != nil {
			t.Fatalf("fetchAgents: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("entries = %d, want 2", len(entries))
		}
		if entries[0].Name != "falcon" {
			t.Errorf("first entry name = %q, want falcon", entries[0].Name)
		}
		if !state.seenPath("/api/workspaces/default/agents") {
			t.Errorf("did not GET /agents; saw %v", state.paths)
		}
	})
}

func TestAgentStopDefault(t *testing.T) {
	srv, state := newAgentMockServer(t)
	defer srv.Close()
	withDataClientState(t, func() {
		setupAgentTest(t, srv.URL)
		if err := runAgentControl(context.Background(), "falcon", "stop"); err != nil {
			t.Fatalf("runAgentControl: %v", err)
		}
		body := state.bodies["/agents/falcon/stop"]
		if body != "" {
			t.Errorf("expected empty body on canonical stop; got %q", body)
		}
		if !state.seenPath("/api/workspaces/default/agents/falcon/stop") {
			t.Errorf("did not POST to stop path; saw %v", state.paths)
		}
	})
}

func TestAgentStartRestart(t *testing.T) {
	srv, state := newAgentMockServer(t)
	defer srv.Close()
	withDataClientState(t, func() {
		setupAgentTest(t, srv.URL)
		for _, verb := range []string{"start", "restart"} {
			if err := runAgentControl(context.Background(), "falcon", verb); err != nil {
				t.Errorf("%s: %v", verb, err)
			}
			if !state.seenPath("/api/workspaces/default/agents/falcon/" + verb) {
				t.Errorf("did not POST to %s path; saw %v", verb, state.paths)
			}
		}
	})
}

func TestAgent503(t *testing.T) {
	srv, _ := newAgentMockServer(t)
	defer srv.Close()
	withDataClientState(t, func() {
		setupAgentTest(t, srv.URL)
		err := runAgentControl(context.Background(), "ghost", "stop")
		if err == nil {
			t.Fatal("expected 503 to surface as error")
		}
		if !strings.Contains(err.Error(), "unavailable") {
			t.Errorf("err = %q, want one containing 'unavailable'", err.Error())
		}
	})
}

func TestFetchAgentsErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantErrSub string
	}{
		{name: "unavailable", status: http.StatusServiceUnavailable, wantErrSub: "agent runtime unavailable"},
		{name: "no content", status: http.StatusNoContent, wantErrSub: "no body"},
		{name: "server error", status: http.StatusInternalServerError, body: "boom", wantErrSub: "HTTP 500"},
		{name: "bad json", status: http.StatusOK, body: "{", wantErrSub: "decode agents response"},
		{name: "envelope error", status: http.StatusOK, body: `{"success":false,"error":"bad workspace"}`, wantErrSub: "bad workspace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.status != 0 {
					w.WriteHeader(tt.status)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			_, err := fetchAgents(context.Background(), srv.Client(), srv.URL, "default")
			if err == nil {
				t.Fatal("expected fetchAgents error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("error = %q, want %q", err.Error(), tt.wantErrSub)
			}
		})
	}
}

func TestDecodeAgentMessageFallbackAndError(t *testing.T) {
	msg, err := decodeAgentMessage([]byte(`{"success":true}`), "start")
	if err != nil {
		t.Fatalf("decode empty success message: %v", err)
	}
	if msg != "" {
		t.Fatalf("message = %q, want empty fallback trigger", msg)
	}

	_, err = decodeAgentMessage([]byte(`{"success":false,"error":"agent busy"}`), "restart")
	if err == nil {
		t.Fatal("expected error response to fail")
	}
	if !strings.Contains(err.Error(), "agent busy") {
		t.Fatalf("error = %q, want agent busy", err.Error())
	}

	_, err = decodeAgentMessage([]byte(`{`), "stop")
	if err == nil {
		t.Fatal("expected malformed JSON to fail")
	}
	if !strings.Contains(err.Error(), "decode agent stop response") {
		t.Fatalf("error = %q, want decode context", err.Error())
	}
}

func TestPostAgentActionHTTPErrorIncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte("already running"))
	}))
	defer srv.Close()

	_, err := postAgentAction(context.Background(), srv.Client(), srv.URL, "start")
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if !strings.Contains(err.Error(), "HTTP 409") || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("error = %q, want status and body", err.Error())
	}
}

func TestRunAgentControlUsesFallbackMessage(t *testing.T) {
	mux := http.NewServeMux()
	registerAuthConfig(mux)
	mux.HandleFunc("/api/workspaces/default/agents/falcon/start", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withDataClientState(t, func() {
		setupAgentTest(t, srv.URL)
		out, err := captureDataStdout(t, func() error {
			return runAgentControl(context.Background(), "falcon", "start")
		})
		if err != nil {
			t.Fatalf("runAgentControl: %v", err)
		}
		if !strings.Contains(out, `agent "falcon" start`) {
			t.Fatalf("output = %q, want fallback message", out)
		}
	})
}
