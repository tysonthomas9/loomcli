package local

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

	result, err := createLocalBrowserSession(context.Background(), dataDir, server.URL, nil, server.Client())
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

func TestCreateLocalBrowserSessionUsesExplicitWorkspaceWithoutActiveLookup(t *testing.T) {
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
		if r.URL.Path != "/api/workspaces/OTHER/operator-sessions/launch" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+durable {
			t.Errorf("Authorization = %q", got)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"launch_code": launch, "workspace": "OTHER", "expires_at": "soon"})
	}))
	t.Cleanup(server.Close)

	requestedWorkspace := " OTHER "
	result, err := createLocalBrowserSession(context.Background(), dataDir, server.URL, &requestedWorkspace, server.Client())
	if err != nil {
		t.Fatalf("createLocalBrowserSession: %v", err)
	}
	if result.Workspace != "OTHER" || result.LaunchCode != launch {
		t.Fatalf("result = %+v", result)
	}
	if got := strings.Join(calls, ","); got != "POST /api/workspaces/OTHER/operator-sessions/launch" {
		t.Fatalf("calls = %s", got)
	}
}

func TestCreateLocalBrowserSessionUsesSelectedWorkspaceHintBeforeActiveLookup(t *testing.T) {
	dataDir := t.TempDir()
	credentialDir := filepath.Join(dataDir, ".loom", "operator")
	if _, err := authority.LoadOrCreateLocalOperatorCredential(credentialDir, "TEST"); err != nil {
		t.Fatalf("LoadOrCreateLocalOperatorCredential: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "state.json"), []byte(`{"last_workspace":"PHASE4"}`), 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	launch := strings.Repeat("cd", 32)
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.URL.Path != "/api/workspaces/PHASE4/operator-sessions/launch" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"launch_code": launch, "workspace": "PHASE4", "expires_at": "soon"})
	}))
	t.Cleanup(server.Close)

	result, err := createLocalBrowserSession(context.Background(), dataDir, server.URL, nil, server.Client())
	if err != nil {
		t.Fatalf("createLocalBrowserSession: %v", err)
	}
	if result.Workspace != "PHASE4" || result.LaunchCode != launch {
		t.Fatalf("result = %+v", result)
	}
	if got := strings.Join(calls, ","); got != "POST /api/workspaces/PHASE4/operator-sessions/launch" {
		t.Fatalf("calls = %s", got)
	}
}

func TestCreateLocalBrowserSessionFallsBackFromStaleSelectedWorkspaceHint(t *testing.T) {
	dataDir := t.TempDir()
	credentialDir := filepath.Join(dataDir, ".loom", "operator")
	if _, err := authority.LoadOrCreateLocalOperatorCredential(credentialDir, "LIVE"); err != nil {
		t.Fatalf("LoadOrCreateLocalOperatorCredential: %v", err)
	}
	durable, err := authority.ReadLocalOperatorToken(credentialDir)
	if err != nil {
		t.Fatalf("ReadLocalOperatorToken: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "state.json"), []byte(`{"last_workspace":"STALE"}`), 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	launch := strings.Repeat("ef", 32)
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/workspaces/STALE/operator-sessions/launch":
			if got := r.Header.Get("Authorization"); got != "Bearer "+durable {
				t.Errorf("stale launch Authorization = %q", got)
			}
			http.NotFound(w, r)
		case "/api/workspaces/active":
			if got := r.Header.Get("Authorization"); got != "" {
				t.Errorf("active workspace request carried durable credential: %q", got)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"LIVE"}}`))
		case "/api/workspaces/LIVE/operator-sessions/launch":
			if got := r.Header.Get("Authorization"); got != "Bearer "+durable {
				t.Errorf("live launch Authorization = %q", got)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"launch_code": launch, "workspace": "LIVE", "expires_at": "soon"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	result, err := createLocalBrowserSession(context.Background(), dataDir, server.URL, nil, server.Client())
	if err != nil {
		t.Fatalf("createLocalBrowserSession: %v", err)
	}
	if result.Workspace != "LIVE" || result.LaunchCode != launch {
		t.Fatalf("result = %+v", result)
	}
	if got := strings.Join(calls, ","); got != "POST /api/workspaces/STALE/operator-sessions/launch,GET /api/workspaces/active,POST /api/workspaces/LIVE/operator-sessions/launch" {
		t.Fatalf("calls = %s", got)
	}
}

func TestCreateLocalBrowserSessionDoesNotFallBackForExplicitMissingWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	credentialDir := filepath.Join(dataDir, ".loom", "operator")
	if _, err := authority.LoadOrCreateLocalOperatorCredential(credentialDir, "LIVE"); err != nil {
		t.Fatalf("LoadOrCreateLocalOperatorCredential: %v", err)
	}
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	requestedWorkspace := "STALE"
	_, err := createLocalBrowserSession(context.Background(), dataDir, server.URL, &requestedWorkspace, server.Client())
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("error = %v", err)
	}
	if got := strings.Join(calls, ","); got != "POST /api/workspaces/STALE/operator-sessions/launch" {
		t.Fatalf("calls = %s", got)
	}
}

func TestCreateLocalBrowserSessionRejectsExplicitBlankWorkspace(t *testing.T) {
	requestedWorkspace := "   "
	_, err := createLocalBrowserSession(
		context.Background(),
		t.TempDir(),
		"http://127.0.0.1:8080",
		&requestedWorkspace,
		http.DefaultClient,
	)
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("error = %v", err)
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
			if _, err := createLocalBrowserSession(context.Background(), dataDir, value, nil, http.DefaultClient); err == nil {
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
		if _, err := createLocalBrowserSession(context.Background(), dataDir, server.URL, nil, server.Client()); err == nil || !strings.Contains(err.Error(), "workspace mismatch") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("missing credential", func(t *testing.T) {
		empty := t.TempDir()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"TEST"}}`))
		}))
		t.Cleanup(server.Close)
		if _, err := createLocalBrowserSession(context.Background(), empty, server.URL, nil, server.Client()); err == nil || !strings.Contains(err.Error(), "authentication") {
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
