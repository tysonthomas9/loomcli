package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

// TestSessionWriteRoutes_MountedAndStoreBacked validates that the PRD Phase C
// session write endpoints are actually wired through the real route table
// (registerWorkspaceRoutes) → workspace middleware → handler → control-plane
// store, using the in-memory store (no distributed stack needed).
func TestSessionWriteRoutes_MountedAndStoreBacked(t *testing.T) {
	ctx := t.Context()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "PARITY", Name: "Parity"}); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "PARITY", SessionID: "sess-app-1", AgentID: "nova", TaskID: "PARITY-1",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{"PARITY": {Path: t.TempDir()}},
	}); err != nil {
		t.Fatalf("seed state cache: %v", err)
	}

	gitOps := &mockGitOps{}
	app := &Server{
		multiPool:  daemon.NewMultiPool(middleware.WorkspaceFromContext, 1),
		config:     webui.ServerConfig{Store: st, GitOps: gitOps},
		agentSvc:   svcimpl.NewAgentService(gitOps, nil, nil, st),
		wsExistsFn: func(id string) bool { return id == "PARITY" },
	}
	setupTestRoutes(t, app)

	// Register an artifact → 201, persisted with the session's task/agent backrefs.
	body := bytes.NewBufferString(`{"type":"patch","uri":"/tmp/x.patch","summary":"did X","files_changed":2}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/PARITY/sessions/sess-app-1/artifacts", body)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("artifact status = %d, body = %s", rr.Code, rr.Body.String())
	}
	arts, err := st.Artifacts().List(ctx, "PARITY", store.ArtifactFilter{SessionID: "sess-app-1"})
	if err != nil || len(arts) != 1 {
		t.Fatalf("expected 1 persisted artifact, got %d (err=%v)", len(arts), err)
	}
	if arts[0].TaskID != "PARITY-1" || arts[0].Type != "patch" {
		t.Errorf("artifact = %+v, want task PARITY-1 type patch", arts[0])
	}

	// Heartbeat → 200.
	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/PARITY/sessions/sess-app-1/heartbeat", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// Unknown session → 404 (proves the handler ran, not a routing fallthrough).
	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/PARITY/sessions/ghost/heartbeat", nil)
	rr = httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404; body = %s", rr.Code, rr.Body.String())
	}
}
