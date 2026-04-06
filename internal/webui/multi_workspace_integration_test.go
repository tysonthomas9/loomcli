package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// trackingMockPool is a daemon.Pool implementation that tracks calls for assertions.
type trackingMockPool struct {
	mu       sync.Mutex
	getCalls int
	putCalls int
	closed   bool
	getErr   error
}

func (p *trackingMockPool) Get(_ context.Context) (*rpc.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.getCalls++
	if p.getErr != nil {
		return nil, p.getErr
	}
	return &rpc.Client{}, nil
}

func (p *trackingMockPool) Put(_ *rpc.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.putCalls++
}

func (p *trackingMockPool) PutAfterError(_ *rpc.Client) {}
func (p *trackingMockPool) Discard(_ *rpc.Client)       {}

func (p *trackingMockPool) Stats() daemon.PoolStats {
	return daemon.PoolStats{Size: 10, Created: 1}
}

func (p *trackingMockPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func (p *trackingMockPool) GetCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.getCalls
}

func (p *trackingMockPool) PutCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.putCalls
}

func (p *trackingMockPool) IsClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// newTestSSEClientWithWorkspace creates a testSSEClient with a specific workspace ID.
func newTestSSEClientWithWorkspace(t *testing.T, hub *realtime.Hub, id int64, workspaceID string) *testSSEClient {
	t.Helper()

	client := realtime.NewClient(id, 64, 0, nil, workspaceID)

	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	return &testSSEClient{
		client: client,
		hub:    hub,
	}
}

// waitForClientCount polls hub.ClientCount() until it reaches the expected
// value or the deadline expires. This avoids race conditions between client
// registration and broadcast calls.
func waitForClientCount(t *testing.T, hub *realtime.Hub, expected int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() < expected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() < expected {
		t.Fatalf("expected %d clients registered in hub, got %d", expected, hub.ClientCount())
	}
}

// --- Test 1: Stale Workspace UUID ---

func TestMultiWorkspace_StaleWorkspaceUUID(t *testing.T) {
	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	poolAlpha := &trackingMockPool{}
	if err := mp.Register("ws-alpha", poolAlpha); err != nil {
		t.Fatal(err)
	}

	wsExists := func(id string) bool {
		return mp.PoolForWorkspace(id) != nil
	}

	// Build a mux with WorkspaceMiddleware-protected issue route
	mux := http.NewServeMux()
	wsMux := http.NewServeMux()
	wsMux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}", workspaceProbeHandler(mp))
	mux.Handle("/api/workspaces/{ws}/", middleware.Workspace(wsExists)(wsMux))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	t.Run("unknown workspace returns 404", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/api/workspaces/ws-nonexistent/issues/test-1")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}

		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["error"] == "" {
			t.Error("expected JSON error message in response body")
		}
	})

	t.Run("deregistered workspace returns 404", func(t *testing.T) {
		mp.Deregister("ws-alpha")

		resp, err := http.Get(ts.URL + "/api/workspaces/ws-alpha/issues/test-1")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404 after deregister, got %d", resp.StatusCode)
		}
	})
}

// --- Test 2: Rename While Running ---

// workspaceProbeHandler returns a simple handler that calls MultiPool.Get to
// verify routing and returns the resolved workspace ID. This avoids using real
// RPC handlers (which panic on nil socket) while exercising the full
// WorkspaceMiddleware → MultiPool.Get dispatch chain.
func workspaceProbeHandler(mp *daemon.MultiPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client, err := mp.Get(r.Context())
		if err != nil {
			respondError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		mp.Put(client)
		wsID := middleware.WorkspaceFromContext(r.Context())
		respondJSON(w, http.StatusOK, map[string]string{"workspace": wsID})
	}
}

func TestMultiWorkspace_RenameWhileRunning(t *testing.T) {
	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	pool := &trackingMockPool{}
	if err := mp.Register("original-name", pool); err != nil {
		t.Fatal(err)
	}

	wsExists := func(id string) bool {
		return mp.PoolForWorkspace(id) != nil
	}

	mux := http.NewServeMux()
	wsMux := http.NewServeMux()
	wsMux.HandleFunc("GET /api/workspaces/{ws}/probe", workspaceProbeHandler(mp))
	mux.Handle("/api/workspaces/{ws}/", middleware.Workspace(wsExists)(wsMux))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Verify original name works
	resp, err := http.Get(ts.URL + "/api/workspaces/original-name/probe")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("original-name should be accessible before rename, got %d", resp.StatusCode)
	}
	if pool.GetCalls() != 1 {
		t.Errorf("expected pool.getCalls=1, got %d", pool.GetCalls())
	}

	// Simulate rename: deregister old, register same pool under new name
	mp.Deregister("original-name")
	newPool := &trackingMockPool{}
	if err := mp.Register("new-name", newPool); err != nil {
		t.Fatal(err)
	}

	// Verify new name works
	resp, err = http.Get(ts.URL + "/api/workspaces/new-name/probe")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("new-name should be accessible after rename, got %d", resp.StatusCode)
	}
	if newPool.GetCalls() != 1 {
		t.Errorf("expected newPool.getCalls=1, got %d", newPool.GetCalls())
	}

	// Verify old name fails
	resp, err = http.Get(ts.URL + "/api/workspaces/original-name/probe")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("original-name should return 404 after rename, got %d", resp.StatusCode)
	}

	// Verify old pool was closed by Deregister
	if !pool.IsClosed() {
		t.Error("expected old pool to be closed after deregister")
	}
}

// --- Test 3: Delete While Running ---

func TestMultiWorkspace_DeleteWhileRunning(t *testing.T) {
	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	pool := &trackingMockPool{}
	if err := mp.Register("ws-to-delete", pool); err != nil {
		t.Fatal(err)
	}

	// Verify pool is registered
	if mp.PoolForWorkspace("ws-to-delete") == nil {
		t.Fatal("expected pool to be registered")
	}

	// Deregister
	mp.Deregister("ws-to-delete")

	// Verify pool is gone
	if mp.PoolForWorkspace("ws-to-delete") != nil {
		t.Error("expected pool to be nil after deregister")
	}

	// Verify Get fails with ErrWorkspaceNotRegistered
	ctx := middleware.WithWorkspace(context.Background(), "ws-to-delete")
	_, err := mp.Get(ctx)
	if err == nil {
		t.Fatal("expected error after deregister")
	}
	if !errors.Is(err, daemon.ErrWorkspaceNotRegistered) {
		t.Errorf("expected ErrWorkspaceNotRegistered, got: %v", err)
	}

	// Verify pool's Close() was called
	if !pool.IsClosed() {
		t.Error("expected pool.Close() to have been called")
	}

	// Verify WorkspaceIDs no longer contains the deleted workspace
	for _, id := range mp.WorkspaceIDs() {
		if id == "ws-to-delete" {
			t.Error("expected ws-to-delete to be removed from WorkspaceIDs")
		}
	}

	// Verify double-deregister is idempotent (no panic)
	mp.Deregister("ws-to-delete")
}

// --- Test 4: Duplicate Agent Names ---

func TestMultiWorkspace_DuplicateAgentNames(t *testing.T) {
	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	poolAlpha := &trackingMockPool{}
	poolBeta := &trackingMockPool{}
	if err := mp.Register("ws-alpha", poolAlpha); err != nil {
		t.Fatal(err)
	}
	if err := mp.Register("ws-beta", poolBeta); err != nil {
		t.Fatal(err)
	}

	wsExists := func(id string) bool {
		return mp.PoolForWorkspace(id) != nil
	}

	mux := http.NewServeMux()
	wsMux := http.NewServeMux()
	wsMux.HandleFunc("GET /api/workspaces/{ws}/probe", workspaceProbeHandler(mp))
	mux.Handle("/api/workspaces/{ws}/", middleware.Workspace(wsExists)(wsMux))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Request for same resource in ws-alpha — only poolAlpha should be hit
	resp, err := http.Get(ts.URL + "/api/workspaces/ws-alpha/probe")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if poolAlpha.GetCalls() != 1 {
		t.Errorf("expected poolAlpha.getCalls=1, got %d", poolAlpha.GetCalls())
	}
	if poolBeta.GetCalls() != 0 {
		t.Errorf("expected poolBeta.getCalls=0, got %d", poolBeta.GetCalls())
	}

	// Same resource name in ws-beta — only poolBeta should be hit
	resp, err = http.Get(ts.URL + "/api/workspaces/ws-beta/probe")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if poolBeta.GetCalls() != 1 {
		t.Errorf("expected poolBeta.getCalls=1, got %d", poolBeta.GetCalls())
	}
	if poolAlpha.GetCalls() != 1 {
		t.Errorf("expected poolAlpha.getCalls still 1, got %d", poolAlpha.GetCalls())
	}
}

// --- Test 5: Duplicate Issue IDs ---

func TestMultiWorkspace_DuplicateIssueIDs(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	clientAlpha := newTestSSEClientWithWorkspace(t, hub, 1, "ws-alpha")
	defer clientAlpha.Close()
	clientBeta := newTestSSEClientWithWorkspace(t, hub, 2, "ws-beta")
	defer clientBeta.Close()

	waitForClientCount(t, hub, 2)

	// Broadcast mutation for same issue ID "issue-1" but different workspaces
	hub.Broadcast(&realtime.MutationPayload{
		Type:        "create",
		IssueID:     "issue-1",
		Title:       "Alpha Issue",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		WorkspaceID: "ws-alpha",
	})

	hub.Broadcast(&realtime.MutationPayload{
		Type:        "create",
		IssueID:     "issue-1",
		Title:       "Beta Issue",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		WorkspaceID: "ws-beta",
	})

	time.Sleep(100 * time.Millisecond)

	// clientAlpha should receive exactly the alpha mutation
	alphaMutation, err := clientAlpha.WaitForMutation(500 * time.Millisecond)
	if err != nil {
		t.Fatalf("clientAlpha: expected mutation, got error: %v", err)
	}
	if alphaMutation.Title != "Alpha Issue" {
		t.Errorf("clientAlpha: expected Title 'Alpha Issue', got %q", alphaMutation.Title)
	}
	if alphaMutation.IssueID != "issue-1" {
		t.Errorf("clientAlpha: expected IssueID 'issue-1', got %q", alphaMutation.IssueID)
	}

	// clientAlpha should not receive any more mutations
	extra := clientAlpha.DrainMutations()
	if len(extra) > 0 {
		t.Errorf("clientAlpha: expected no more mutations, got %d", len(extra))
	}

	// clientBeta should receive exactly the beta mutation
	betaMutation, err := clientBeta.WaitForMutation(500 * time.Millisecond)
	if err != nil {
		t.Fatalf("clientBeta: expected mutation, got error: %v", err)
	}
	if betaMutation.Title != "Beta Issue" {
		t.Errorf("clientBeta: expected Title 'Beta Issue', got %q", betaMutation.Title)
	}
	if betaMutation.IssueID != "issue-1" {
		t.Errorf("clientBeta: expected IssueID 'issue-1', got %q", betaMutation.IssueID)
	}

	// clientBeta should not receive any more mutations
	extra = clientBeta.DrainMutations()
	if len(extra) > 0 {
		t.Errorf("clientBeta: expected no more mutations, got %d", len(extra))
	}
}

// --- Test 6: Two-Tab SSE Independence ---

func TestMultiWorkspace_TwoTabSSEIndependence(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	tab1 := newTestSSEClientWithWorkspace(t, hub, 1, "ws-alpha")
	defer tab1.Close()
	tab2 := newTestSSEClientWithWorkspace(t, hub, 2, "ws-beta")
	defer tab2.Close()

	waitForClientCount(t, hub, 2)

	t.Run("alpha mutation reaches only tab1", func(t *testing.T) {
		hub.Broadcast(&realtime.MutationPayload{
			Type:        "create",
			IssueID:     "bd-alpha-1",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: "ws-alpha",
		})

		time.Sleep(100 * time.Millisecond)

		m, err := tab1.WaitForMutation(500 * time.Millisecond)
		if err != nil {
			t.Fatalf("tab1 should receive ws-alpha mutation: %v", err)
		}
		if m.IssueID != "bd-alpha-1" {
			t.Errorf("tab1: expected bd-alpha-1, got %s", m.IssueID)
		}

		extra := tab2.DrainMutations()
		if len(extra) > 0 {
			t.Errorf("tab2 should NOT receive ws-alpha mutation, got %d mutations", len(extra))
		}
	})

	t.Run("beta mutation reaches only tab2", func(t *testing.T) {
		hub.Broadcast(&realtime.MutationPayload{
			Type:        "update",
			IssueID:     "bd-beta-1",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: "ws-beta",
		})

		time.Sleep(100 * time.Millisecond)

		m, err := tab2.WaitForMutation(500 * time.Millisecond)
		if err != nil {
			t.Fatalf("tab2 should receive ws-beta mutation: %v", err)
		}
		if m.IssueID != "bd-beta-1" {
			t.Errorf("tab2: expected bd-beta-1, got %s", m.IssueID)
		}

		extra := tab1.DrainMutations()
		if len(extra) > 0 {
			t.Errorf("tab1 should NOT receive ws-beta mutation, got %d mutations", len(extra))
		}
	})

	t.Run("untagged mutation reaches neither tab", func(t *testing.T) {
		hub.Broadcast(&realtime.MutationPayload{
			Type:        "create",
			IssueID:     "bd-untagged",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: "", // empty = untagged
		})

		time.Sleep(100 * time.Millisecond)

		extra1 := tab1.DrainMutations()
		extra2 := tab2.DrainMutations()
		if len(extra1) > 0 {
			t.Errorf("tab1 should NOT receive untagged mutation, got %d", len(extra1))
		}
		if len(extra2) > 0 {
			t.Errorf("tab2 should NOT receive untagged mutation, got %d", len(extra2))
		}
	})

	t.Run("workspace UUID unchanged after rename — SSE continues", func(t *testing.T) {
		// Drain any mutations leaked from prior sub-tests.
		tab1.DrainMutations()
		tab2.DrainMutations()

		// Simulate rename: the UUID (ws-alpha) stays the same. A new mutation
		// with the same UUID should still be delivered to tab1.
		hub.Broadcast(&realtime.MutationPayload{
			Type:        "status",
			IssueID:     "bd-post-rename",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: "ws-alpha", // UUID unchanged
		})

		time.Sleep(100 * time.Millisecond)

		m, err := tab1.WaitForMutation(500 * time.Millisecond)
		if err != nil {
			t.Fatalf("tab1 should still receive ws-alpha mutations after rename: %v", err)
		}
		if m.IssueID != "bd-post-rename" {
			t.Errorf("tab1: expected bd-post-rename, got %s", m.IssueID)
		}
	})

	t.Run("stale workspace SSE client receives nothing", func(t *testing.T) {
		// Drain any mutations leaked from prior sub-tests.
		tab1.DrainMutations()
		tab2.DrainMutations()

		staleTab := newTestSSEClientWithWorkspace(t, hub, 99, "ws-deleted")
		defer staleTab.Close()

		hub.Broadcast(&realtime.MutationPayload{
			Type:        "create",
			IssueID:     "bd-stale",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: "ws-alpha",
		})

		time.Sleep(100 * time.Millisecond)

		extra := staleTab.DrainMutations()
		if len(extra) > 0 {
			t.Errorf("stale workspace client should receive nothing, got %d", len(extra))
		}

		// Drain tab1 which legitimately received the ws-alpha mutation
		tab1.DrainMutations()
	})
}

// --- Test 7: Cross-Workspace Move ---

func TestMultiWorkspace_CrossWorkspaceMove(t *testing.T) {
	t.Run("successful cross-workspace move", func(t *testing.T) {
		svc := &mockIssueService{
			moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
				if params.IssueID != "src-001" {
					t.Errorf("expected IssueID src-001, got %s", params.IssueID)
				}
				if params.TargetWorkspace != "beta" {
					t.Errorf("expected TargetWorkspace beta, got %s", params.TargetWorkspace)
				}
				return &service.MoveIssueResult{
					SourceID: "src-001",
					TargetID: "tgt-001",
				}, nil
			},
		}

		workspaces := []ops.WorkspaceSummary{
			{ID: "ws-alpha-uuid", Name: "alpha", Path: "/ws/alpha", Active: true},
			{ID: "ws-beta-uuid", Name: "beta", Path: "/ws/beta", Active: false},
		}
		wsCfg := testWorkspaceConfigFn("alpha", workspaces)

		handler := issues.HandleMoveIssue(svc, wsCfg)

		body := `{"target_workspace":"beta"}`
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-alpha/issues/src-001/move", strings.NewReader(body))
		req.SetPathValue("id", "src-001")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp issues.MoveIssueResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if !resp.Success {
			t.Errorf("expected success=true, got error: %s", resp.Error)
		}
		if resp.Data.SourceID != "src-001" {
			t.Errorf("expected SourceID src-001, got %s", resp.Data.SourceID)
		}
		if resp.Data.TargetID != "tgt-001" {
			t.Errorf("expected TargetID tgt-001, got %s", resp.Data.TargetID)
		}
	})

	t.Run("move to non-existent workspace returns 400", func(t *testing.T) {
		svc := &mockIssueService{
			moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
				_, err := params.Validator.ValidateTarget(params.TargetWorkspace)
				if err != nil {
					return nil, err
				}
				return nil, nil
			},
		}

		workspaces := []ops.WorkspaceSummary{
			{Name: "alpha", Path: "/ws/alpha", Active: true},
		}
		wsCfg := testWorkspaceConfigFn("alpha", workspaces)

		handler := issues.HandleMoveIssue(svc, wsCfg)

		body := `{"target_workspace":"nonexistent"}`
		req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
		req.SetPathValue("id", "src-001")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("move to same workspace returns 400", func(t *testing.T) {
		svc := &mockIssueService{
			moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
				_, err := params.Validator.ValidateTarget(params.TargetWorkspace)
				if err != nil {
					return nil, err
				}
				return nil, nil
			},
		}

		workspaces := []ops.WorkspaceSummary{
			{Name: "alpha", Path: "/ws/alpha", Active: true},
			{Name: "beta", Path: "/ws/beta", Active: false},
		}
		wsCfg := testWorkspaceConfigFn("alpha", workspaces)

		handler := issues.HandleMoveIssue(svc, wsCfg)

		body := `{"target_workspace":"alpha"}`
		req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
		req.SetPathValue("id", "src-001")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
