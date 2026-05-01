package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

func TestFleetDBAgentRoutesUseStoreInsteadOfDaemonControl(t *testing.T) {
	st := memstore.New()
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
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("lifecycle control status = %d, body = %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/PARITY/agents/worker-one/queue", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("agent queue status = %d, body = %s", rr.Code, rr.Body.String())
	}
}
