package local

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestCreateLocalBrowserSessionUsesOnlyLoopbackAndDurableCredential(t *testing.T) {
	dataDir := t.TempDir()
	credentialDir := filepath.Join(dataDir, ".loom", "operator")
	if _, err := authority.LoadOrCreateLocalOperatorCredential(credentialDir, "TEST"); err != nil {
		t.Fatalf("LoadOrCreateLocalOperatorCredential: %v", err)
	}
	durable, err := authority.ReadLocalOperatorToken(credentialDir)
	if err != nil {
		t.Fatalf("ReadLocalOperatorToken: %v", err)
	}
	launch := strings.Repeat("ab", 32)
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/workspaces/active":
			if r.Header.Get("Authorization") != "" {
				t.Error("active workspace request carried durable credential")
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"TEST"}}`))
		case "/api/workspaces/TEST/operator-sessions/launch":
			if got := r.Header.Get("Authorization"); got != "Bearer "+durable {
				t.Errorf("Authorization = %q", got)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"launch_code": launch, "workspace": "TEST", "expires_at": "soon"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	result, err := createLocalBrowserSession(context.Background(), dataDir, server.URL, server.Client())
	if err != nil {
		t.Fatalf("createLocalBrowserSession: %v", err)
	}
	if result.RuntimeURL != server.URL || result.Workspace != "TEST" || result.LaunchCode != launch {
		t.Fatalf("result = %+v", result)
	}
	if got := strings.Join(calls, ","); got != "GET /api/workspaces/active,POST /api/workspaces/TEST/operator-sessions/launch" {
		t.Fatalf("calls = %s", got)
	}
}

func TestCreateLocalBrowserSessionRejectsCredentialExfiltrationAndBadResponses(t *testing.T) {
	dataDir := t.TempDir()
	credentialDir := filepath.Join(dataDir, ".loom", "operator")
	if _, err := authority.LoadOrCreateLocalOperatorCredential(credentialDir, "TEST"); err != nil {
		t.Fatalf("LoadOrCreateLocalOperatorCredential: %v", err)
	}

	for _, value := range []string{
		"https://127.0.0.1:8080",
		"http://example.com:8080",
		"http://localhost:8080",
		"http://127.0.0.1",
		"http://127.0.0.1:8080/path",
		"http://user@127.0.0.1:8080",
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := createLocalBrowserSession(context.Background(), dataDir, value, http.DefaultClient); err == nil {
				t.Fatalf("URL %q accepted", value)
			}
		})
	}

	t.Run("workspace mismatch", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/workspaces/active" {
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":"TEST"}}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"launch_code":"abababababababababababababababababababababababababababababababab","workspace":"OTHER"}`))
		}))
		t.Cleanup(server.Close)
		if _, err := createLocalBrowserSession(context.Background(), dataDir, server.URL, server.Client()); err == nil || !strings.Contains(err.Error(), "workspace mismatch") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("missing credential", func(t *testing.T) {
		empty := t.TempDir()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"TEST"}}`))
		}))
		t.Cleanup(server.Close)
		if _, err := createLocalBrowserSession(context.Background(), empty, server.URL, server.Client()); err == nil || !strings.Contains(err.Error(), "authentication") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestRunBrowserSessionEmitsJSONWithoutDurableToken(t *testing.T) {
	// Keep this test at the helper boundary: cobra globals are shared across
	// package tests, while createLocalBrowserSession above proves the full HTTP
	// exchange and output struct. This assertion guards JSON field names and
	// confirms no durable-token field can be serialized.
	payload, err := json.Marshal(localBrowserSessionOutput{
		RuntimeURL: "http://127.0.0.1:8080", Workspace: "TEST",
		LaunchCode: strings.Repeat("ab", 32), ExpiresAt: "soon",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(payload), authority.LocalOperatorTokenFileName) || strings.Contains(string(payload), "operator_token") {
		t.Fatalf("JSON exposed durable credential field: %s", payload)
	}
	if !strings.Contains(string(payload), `"launch_code"`) {
		t.Fatalf("JSON missing launch code: %s", payload)
	}
}
