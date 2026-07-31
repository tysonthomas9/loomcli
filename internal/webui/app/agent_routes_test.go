package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentscompat"
	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/agentscompatstore"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentcontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

func TestStorelessAgentLifecycleRoutesReturnUnifiedReceipt(t *testing.T) {
	var calls []string
	multiPool := daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	t.Cleanup(func() { _ = multiPool.Close() })
	app := &Server{
		multiPool: multiPool,
		config: webui.ServerConfig{
			AgentControlFn: func(op, agentName string, _ bool) (*agentcontrol.AgentControlResult, error) {
				calls = append(calls, op+":"+agentName)
				return &agentcontrol.AgentControlResult{Success: true}, nil
			},
		},
		wsExistsFn: func(id string) bool { return id == "LOCAL" },
	}
	setupTestRoutes(t, app)

	for _, action := range []string{"start", "stop", "restart", "yield"} {
		t.Run(action, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodPost,
				"/api/workspaces/LOCAL/agents/direct-agent/"+action,
				nil,
			)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("%s status = %d, body = %s", action, rr.Code, rr.Body.String())
			}
			var receipt struct {
				Message   string `json:"message"`
				Pending   bool   `json:"pending"`
				CommandID string `json:"command_id"`
				Status    string `json:"status"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &receipt); err != nil {
				t.Fatalf("%s response JSON: %v", action, err)
			}
			if receipt.Message == "" || receipt.Pending || receipt.CommandID != "" || receipt.Status != "succeeded" {
				t.Fatalf("%s receipt = %+v, want synchronous succeeded receipt", action, receipt)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(rr.Body.Bytes(), &fields); err != nil {
				t.Fatalf("%s response fields: %v", action, err)
			}
			if len(fields) != 4 {
				t.Fatalf("%s response fields = %v, want exact lifecycle contract", action, fields)
			}
			for _, key := range []string{"message", "pending", "command_id", "status"} {
				if _, ok := fields[key]; !ok {
					t.Fatalf("%s response missing %q: %s", action, key, rr.Body.String())
				}
			}
		})
	}

	if len(calls) != 4 {
		t.Fatalf("daemon control calls = %v, want one per lifecycle action", calls)
	}
}

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
	agentsCapability := newAgentRouteAgentsCapability(t, st)
	app := &Server{
		multiPool: daemon.NewMultiPool(middleware.WorkspaceFromContext, 1),
		config: webui.ServerConfig{
			Store: st, GitOps: gitOps, AutomationCapability: newAgentRouteAutomationCapability(st),
			AgentsCapability: agentsCapability,
		},
		agentSvc: svcimpl.NewAgentServiceWithCompatibility(
			gitOps, nil, nil, st, nil,
			agentsCapability.compatibility,
			agentsCapability.managed,
			agentsCapability.retirements,
		),
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

	body = bytes.NewBufferString(`{"desired_state":"running"}`)
	req = httptest.NewRequest(http.MethodPatch, "/api/workspaces/PARITY/agents/worker-one", body)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("update agent status = %d, body = %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/PARITY/agents/worker-one/stop", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
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
	if got := list.Data[0]; got.State != "idle" || got.DesiredState != "running" {
		t.Fatalf("request-side lifecycle path mutated the durable agent projection: %+v", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/PARITY/agents/worker-one/start", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
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
	agentsCapability := newAgentRouteAgentsCapability(t, st)
	app := &Server{
		multiPool: daemon.NewMultiPool(middleware.WorkspaceFromContext, 1),
		config: webui.ServerConfig{
			Store: st, GitOps: gitOps, AutomationCapability: newAgentRouteAutomationCapability(st),
			AgentsCapability: agentsCapability,
		},
		agentSvc: svcimpl.NewAgentServiceWithCompatibility(
			gitOps, nil, nil, st, nil,
			agentsCapability.compatibility,
			agentsCapability.managed,
			agentsCapability.retirements,
		),
		hub: hub,
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

	serveAgentRequest(t, app, http.MethodPatch, "/api/workspaces/PARITY/agents/worker-one", `{"desired_state":"running"}`, http.StatusOK)
	expectAgentRefresh(t, client.Send(), "PARITY", "worker-one")

	serveAgentRequest(t, app, http.MethodPost, "/api/workspaces/PARITY/agents/worker-one/stop", "", http.StatusAccepted)
	expectAgentRefresh(t, client.Send(), "PARITY", "worker-one")

	serveAgentRequest(t, app, http.MethodDelete, "/api/workspaces/PARITY/agents/worker-one", "", http.StatusOK)
	expectAgentRefresh(t, client.Send(), "PARITY", "worker-one")
}

type agentRouteIdentityAPI struct {
	agentsmodule.API
}

func (agentRouteIdentityAPI) GetAgent(
	context.Context,
	string,
	string,
) (*agentsmodule.Agent, error) {
	return nil, agentsmodule.ErrNotFound
}

func (agentRouteIdentityAPI) ListAgents(
	context.Context,
	string,
	agentsmodule.AgentFilter,
) ([]*agentsmodule.Agent, error) {
	return []*agentsmodule.Agent{}, nil
}

type agentRouteAgentsCapability struct {
	api           agentsmodule.API
	compatibility agentsmodule.CompatibilityAPI
	managed       agentscompat.ManagedCommands
	parentBinding agentscompat.ParentBindingCommands
	retirements   agentscompat.ManagedRetirements
	issuer        *authority.Issuer
}

func newAgentRouteAgentsCapability(t *testing.T, st store.Store) *agentRouteAgentsCapability {
	t.Helper()
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(agentsmodule.OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	compatibilityPersistence, err := agentscompatstore.New(
		st.Roles(),
		st.AgentServices(),
		st.Agents(),
	)
	if err != nil {
		t.Fatal(err)
	}
	compatibility, err := agentscompat.NewAPI(compatibilityPersistence, admission)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := agentscompat.NewManagedCommandsWithIssuer(compatibility, issuer)
	if err != nil {
		t.Fatal(err)
	}
	parentBinding, err := agentscompat.NewParentBindingCommands(compatibility, issuer)
	if err != nil {
		t.Fatal(err)
	}
	retirements, err := agentscompat.NewManagedRetirements(compatibility, issuer)
	if err != nil {
		t.Fatal(err)
	}
	return &agentRouteAgentsCapability{
		api:           agentRouteIdentityAPI{},
		compatibility: compatibility,
		managed:       managed,
		parentBinding: parentBinding,
		retirements:   retirements,
		issuer:        issuer,
	}
}

func (capability *agentRouteAgentsCapability) AgentsAPI() agentsmodule.API {
	return capability.api
}

func (capability *agentRouteAgentsCapability) CompatibilityAPI() agentsmodule.CompatibilityAPI {
	return capability.compatibility
}

func (capability *agentRouteAgentsCapability) ManagedCompatibility() agentscompat.ManagedCommands {
	return capability.managed
}

func (capability *agentRouteAgentsCapability) ParentBindingCommands() agentscompat.ParentBindingCommands {
	return capability.parentBinding
}

func (capability *agentRouteAgentsCapability) ManagedRetirements() agentscompat.ManagedRetirements {
	return capability.retirements
}

func (capability *agentRouteAgentsCapability) OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver {
	return capability
}

func (capability *agentRouteAgentsCapability) ResolveOperatorAuthority(
	_ *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	principal, err := capability.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   "agent-route-test",
		Class:     authority.ClassOperator,
		Workspace: workspace,
		Actions:   []authority.Action{action},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return capability.issuer.IssueOperator(principal, workspace, action)
}

func (*agentRouteAgentsCapability) PRReviewerProvisioning() prreviewer.Commands {
	return nil
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
