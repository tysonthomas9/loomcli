package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
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

// TestSessionWriteRoutes_FencingEnforced drives the mounted token-optional
// TaskRun auth+fencing middleware through the real route table: a write bearing
// a token with the current lease's fencing token is accepted (201), while a
// stale fencing token is rejected (409) — using a real minted token + a
// lease-backed lookup, against the in-memory store (no distributed stack).
func TestSessionWriteRoutes_FencingEnforced(t *testing.T) {
	ctx := t.Context()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	key := []byte("test-taskrun-signing-key-32bytes!")

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "PARITY", Name: "Parity"}); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "PARITY", SessionID: "sess-f1", AgentID: "nova", TaskID: "PARITY-1",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	lease, err := st.AgentLeases().Create(ctx, store.AgentLeaseCreate{
		WorkspaceKey: "PARITY", SessionID: "sess-f1", LeaseID: "sess-f1-lease", AgentID: "nova", TTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{"PARITY": {Path: t.TempDir()}},
	}); err != nil {
		t.Fatalf("seed state cache: %v", err)
	}

	gitOps := &mockGitOps{}
	app := &Server{
		multiPool:  daemon.NewMultiPool(middleware.WorkspaceFromContext, 1),
		config:     webui.ServerConfig{Store: st, GitOps: gitOps, FleetJWTKey: key},
		agentSvc:   svcimpl.NewAgentService(gitOps, nil, nil, st),
		wsExistsFn: func(id string) bool { return id == "PARITY" },
	}
	setupTestRoutes(t, app)

	mint := func(fencing int64) string {
		tok, err := fleet.GenerateTaskRunToken(fleet.TaskRunClaims{
			Workspace: "PARITY", TaskID: "PARITY-1", SessionID: "sess-f1", FencingToken: fencing,
		}, key, time.Hour)
		if err != nil {
			t.Fatalf("mint token: %v", err)
		}
		return tok
	}
	post := func(token string) int {
		body := bytes.NewBufferString(`{"type":"patch","uri":"/tmp/x.patch","files_changed":1}`)
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/PARITY/sessions/sess-f1/artifacts", body)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		app.mux.ServeHTTP(rr, req)
		return rr.Code
	}

	// Current lease holder (matching fencing token) → accepted.
	if code := post(mint(lease.FencingToken)); code != http.StatusCreated {
		t.Errorf("current fencing token: status = %d, want 201", code)
	}
	// Stale writer (older fencing token) → rejected 409 by the fencing middleware.
	if code := post(mint(lease.FencingToken - 1)); code != http.StatusConflict {
		t.Errorf("stale fencing token: status = %d, want 409", code)
	}
}
