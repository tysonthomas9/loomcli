package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

func TestFleetDBAgentRoutesUseStoreInsteadOfDaemonControl(t *testing.T) {
	ctx := t.Context()
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	wsRoot := t.TempDir()
	repoPath := filepath.Join(wsRoot, "app")
	initGitRepo(t, repoPath)

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "PARITY", Name: "Parity"}); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "PARITY", Name: "app", DefaultBranch: "main"}); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces["PARITY"] = bootstrap.WorkspaceLocalState{
			Path:  wsRoot,
			Repos: map[string]string{"app": repoPath},
		}
		return nil
	}); err != nil {
		t.Fatalf("seed state cache: %v", err)
	}

	gitOps := &mockGitOps{}
	app := &Server{
		multiPool: daemon.NewMultiPool(middleware.WorkspaceFromContext, 1),
		config:    webui.ServerConfig{Store: st, GitOps: gitOps},
		agentSvc:  svcimpl.NewAgentService(gitOps, nil, nil, st),
		wsExistsFn: func(id string) bool {
			return id == "PARITY"
		},
	}
	setupTestRoutes(t, app)

	body := bytes.NewBufferString(`{"name":"worker-one","role_name":"builder","auto":true,"backend":"claude"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/PARITY/agents", body)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create agent status = %d, body = %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/PARITY/agents", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list agents status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var list struct {
		Success bool `json:"success"`
		Total   int  `json:"total"`
		Data    []struct {
			WorkspaceKey string `json:"workspace_key"`
			Name         string `json:"name"`
			RoleName     string `json:"role_name"`
			State        string `json:"state"`
			DesiredState string `json:"desired_state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("list agents JSON: %v", err)
	}
	if !list.Success || list.Total != 1 || len(list.Data) != 1 {
		t.Fatalf("unexpected list envelope: %+v", list)
	}
	if got := list.Data[0]; got.WorkspaceKey != "PARITY" || got.Name != "worker-one" || got.RoleName != "builder" || got.State != "idle" {
		t.Fatalf("unexpected listed agent: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(wsRoot, "worktrees", "app", "worker-one", ".git")); err != nil {
		t.Fatalf("agent worktree was not created: %v", err)
	}

	body = bytes.NewBufferString(`{"state":"active"}`)
	req = httptest.NewRequest(http.MethodPatch, "/api/workspaces/PARITY/agents/worker-one", body)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("update agent status = %d, body = %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/PARITY/agents/worker-one/stop", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("stop agent status = %d, body = %s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/PARITY/agents", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list after stop status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("list after stop JSON: %v", err)
	}
	if got := list.Data[0]; got.State != "stopped" || got.DesiredState != "stopped" {
		t.Fatalf("unexpected stopped agent state: %+v", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/PARITY/agents/worker-one/start", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("start agent status = %d, body = %s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/PARITY/agents/worker-one/yield", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("yield agent status = %d, body = %s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/PARITY/agents/worker-one/restart", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("restart agent status = %d, body = %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/PARITY/agents/worker-one/queue", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("agent queue status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestFleetDBAgentRoutesBroadcastMonitorRefresh(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "PARITY", Name: "Parity"}); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	gitOps := &mockGitOps{}
	app := &Server{
		multiPool: daemon.NewMultiPool(middleware.WorkspaceFromContext, 1),
		config:    webui.ServerConfig{Store: st, GitOps: gitOps},
		agentSvc:  svcimpl.NewAgentService(gitOps, nil, nil, st),
		hub:       hub,
		wsExistsFn: func(id string) bool {
			return id == "PARITY"
		},
	}
	setupTestRoutes(t, app)

	client := realtime.NewClient(1, realtime.ClientSendBuf, "", nil, "PARITY")
	hub.RegisterClient(client)
	waitForHubClient(t, hub)

	serveAgentRequest(t, app, http.MethodPost, "/api/workspaces/PARITY/agents", `{"name":"worker-one","role_name":"builder","auto":true,"backend":"claude"}`, http.StatusCreated)
	expectAgentRefresh(t, client.Send(), "PARITY", "worker-one")

	serveAgentRequest(t, app, http.MethodPatch, "/api/workspaces/PARITY/agents/worker-one", `{"state":"active"}`, http.StatusOK)
	expectAgentRefresh(t, client.Send(), "PARITY", "worker-one")

	serveAgentRequest(t, app, http.MethodPost, "/api/workspaces/PARITY/agents/worker-one/stop", "", http.StatusOK)
	expectAgentRefresh(t, client.Send(), "PARITY", "worker-one")

	serveAgentRequest(t, app, http.MethodDelete, "/api/workspaces/PARITY/agents/worker-one", "", http.StatusOK)
	expectAgentRefresh(t, client.Send(), "PARITY", "worker-one")
}

func serveAgentRequest(t *testing.T, app *Server, method, target, body string, wantStatus int) {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body = %s", method, target, rr.Code, wantStatus, rr.Body.String())
	}
}

func waitForHubClient(t *testing.T, hub *realtime.Hub) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if hub.ClientCount() == 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("SSE client was not registered")
		case <-ticker.C:
		}
	}
}

func expectAgentRefresh(t *testing.T, events <-chan *realtime.MutationPayload, workspace, agentName string) {
	t.Helper()
	select {
	case event := <-events:
		if event == nil {
			t.Fatal("SSE event channel closed")
		}
		if event.Type != "refresh" || event.WorkspaceID != workspace || event.Title != agentName {
			t.Fatalf("unexpected refresh event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for agent refresh event for %s", agentName)
	}
}

func initGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitTest(t, path, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitTest(t, path, "config", "user.email", "test@example.com")
	runGitTest(t, path, "config", "user.name", "Test User")
	runGitTest(t, path, "add", "README.md")
	runGitTest(t, path, "commit", "-m", "initial")
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test fixture setup shells out to git.
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
