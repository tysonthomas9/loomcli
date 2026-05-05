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

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
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
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"PARITY": {
				Path:  wsRoot,
				Repos: map[string]string{"app": repoPath},
			},
		},
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
