//go:build darwin || linux

package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/browserauth"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

type recordingBrowserBackend struct {
	mu    sync.Mutex
	calls []string
}

func (b *recordingBrowserBackend) add(op, ws string) {
	b.mu.Lock()
	b.calls = append(b.calls, op+"@"+ws)
	b.mu.Unlock()
}

func (b *recordingBrowserBackend) Create(_ context.Context, ws, _ string, req domain.BrowserCreate) (*domain.Browser, error) {
	b.add("create", ws)
	return &domain.Browser{ID: "b1", WorkspaceKey: ws, Name: req.Name, RequestID: req.RequestID, Status: domain.BrowserStatusStarting}, nil
}

func (b *recordingBrowserBackend) List(_ context.Context, ws, _ string) ([]domain.Browser, error) {
	b.add("list", ws)
	return []domain.Browser{}, nil
}

func (b *recordingBrowserBackend) Get(_ context.Context, ws, _, id string) (*domain.Browser, error) {
	b.add("get", ws)
	return &domain.Browser{ID: id}, nil
}

func (b *recordingBrowserBackend) Select(_ context.Context, ws, _, id string) (*domain.Browser, error) {
	b.add("select", ws)
	return &domain.Browser{ID: id, Selected: true}, nil
}

func newBrowserTestServer(t *testing.T, socketPath string) (*Server, *recordingBrowserBackend) {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ws", Name: "ws"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "ws", Name: "lead"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "ws", Name: "lead", RoleName: "lead"}); err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := browserauth.NewSigner("k1", priv, "loom", "fleet-db")
	if err != nil {
		t.Fatal(err)
	}
	backend := &recordingBrowserBackend{}
	app, err := NewServer(ctx, webui.ServerConfig{
		Port: freeTCPPort(t), BindAddress: "127.0.0.1", MaxPortAttempts: 1,
		FleetClient: true, Store: st,
		BrowserBackend: backend, BrowserSigner: signer, BrowserOperatorSocketPath: socketPath,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { app.Close() })
	return app, backend
}

// Launch-env keys the server writes for interactive agent terminals
// (modbuilder.BrowserWiring.SpawnEnv).
const (
	launchEnvAgentName        = "LOOM_AGENT_NAME"
	launchEnvAgentTerminalID  = "LOOM_AGENT_TERMINAL_ID"
	launchEnvOrchestratorSess = "LOOM_ORCHESTRATOR_SESSION_ID"
)

func interactiveLaunch(agent, terminalID string) *tabmeta.LaunchSpec {
	return &tabmeta.LaunchSpec{Env: map[string]string{
		launchEnvAgentName: agent, launchEnvAgentTerminalID: terminalID, launchEnvOrchestratorSess: "orch-1",
	}}
}

func TestAgentBrowserSpawnEnvBindsOnlyServerBuiltAgentLaunches(t *testing.T) {
	app, _ := newBrowserTestServer(t, "")
	key := terminal.SessionKey{Workspace: "ws", Name: "lead-term"}

	for name, launch := range map[string]*tabmeta.LaunchSpec{
		"nil launch":        nil,
		"plain shell":       {Env: map[string]string{}},
		"terminal mismatch": interactiveLaunch("lead", "other-term"),
		"no orchestrator":   {Env: map[string]string{launchEnvAgentName: "lead", launchEnvAgentTerminalID: "lead-term"}},
	} {
		if env := app.browsers.SpawnEnv(key, launch); env != nil {
			t.Errorf("%s: bound a session: %v", name, env)
		}
	}
	env := app.browsers.SpawnEnv(key, interactiveLaunch("lead", "lead-term"))
	tok := env[browserauth.EnvAgentSessionToken]
	if tok == "" || env[browserauth.EnvAgentBrowserURL] == "" {
		t.Fatalf("env = %v", env)
	}
	b, err := app.browsers.AgentSessions().Resolve(tok)
	if err != nil || b.AgentName != "lead" || b.Workspace != "ws" || b.TerminalID != "lead-term" {
		t.Fatalf("binding = %+v %v", b, err)
	}
}

func TestAgentBrowserRoutesThroughServerMux(t *testing.T) {
	app, backend := newBrowserTestServer(t, "")
	env := app.browsers.SpawnEnv(terminal.SessionKey{Workspace: "ws", Name: "t1"}, interactiveLaunch("lead", "t1"))
	tok := env[browserauth.EnvAgentSessionToken]

	body, _ := json.Marshal(map[string]string{"name": "Docs", "request_id": "r1"})
	req := httptest.NewRequest(http.MethodPost, "/api/agent/browsers", bytes.NewReader(body))
	req.Header.Set(browserauth.AgentSessionHeader, tok)
	rec := httptest.NewRecorder()
	app.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}

	// Without the header the same route never reaches FleetDB.
	req = httptest.NewRequest(http.MethodGet, "/api/agent/browsers", nil)
	req.Header.Set("X-Actor", "lead")
	rec = httptest.NewRecorder()
	app.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", rec.Code)
	}
	if len(backend.calls) != 1 || backend.calls[0] != "create@ws" {
		t.Fatalf("backend calls = %v", backend.calls)
	}
}

func TestLocalOperatorSocketWiredIntoServer(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := browserauth.OperatorSocketPath(dir)
	app, backend := newBrowserTestServer(t, sock)
	if !app.browsers.OperatorBridgeListening() {
		t.Fatal("operator socket not started in local mode")
	}
	resp, err := browserauth.CallOperatorSocket(context.Background(), sock, browserauth.SocketRequest{Op: browserauth.OpIssue, Workspace: "ws"})
	if err != nil || !resp.OK {
		t.Fatalf("issue = %+v %v", resp, err)
	}
	if bad, _ := browserauth.CallOperatorSocket(context.Background(), sock, browserauth.SocketRequest{Op: browserauth.OpIssue, Workspace: "nope"}); bad.OK {
		t.Fatal("session issued for an unknown workspace")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws/agents/lead/browsers", nil)
	req.Header.Set(browserauth.OperatorSessionHeader, resp.Token)
	rec := httptest.NewRecorder()
	app.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator list = %d %s", rec.Code, rec.Body.String())
	}
	if len(backend.calls) != 1 || backend.calls[0] != "list@ws" {
		t.Fatalf("backend calls = %v", backend.calls)
	}

	// Shutdown revokes every operator session.
	app.browsers.Close()
	rec = httptest.NewRecorder()
	app.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("after bridge close = %d", rec.Code)
	}
}

func TestOperatorSocketNeverStartsInRemoteMode(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	app := &Server{config: webui.ServerConfig{
		Store: memstore.New(), ExtAuthURL: "https://auth.example.com",
		BrowserOperatorSocketPath: browserauth.OperatorSocketPath(dir),
	}}
	app.buildBrowserModule()
	if app.browsers.OperatorBridgeListening() {
		t.Fatal("local operator socket started in remote-auth mode")
	}
	if _, err := os.Stat(browserauth.OperatorSocketPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("socket file exists in remote mode: %v", err)
	}
}
