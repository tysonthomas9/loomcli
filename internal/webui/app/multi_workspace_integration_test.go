package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// newTestSSEClientWithWorkspace creates a testSSEClient with a specific workspace ID.
func newTestSSEClientWithWorkspace(t *testing.T, hub *realtime.Hub, id int64, workspaceID string) *testSSEClient {
	t.Helper()

	client := realtime.NewClient(id, 64, "0", nil, workspaceID)

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

// --- Test 2: Rename While Running ---

// --- Test 3: Delete While Running ---

// --- Test 4: Duplicate Agent Names ---

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
			IssueID:     "loom-alpha-1",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: "ws-alpha",
		})

		time.Sleep(100 * time.Millisecond)

		m, err := tab1.WaitForMutation(500 * time.Millisecond)
		if err != nil {
			t.Fatalf("tab1 should receive ws-alpha mutation: %v", err)
		}
		if m.IssueID != "loom-alpha-1" {
			t.Errorf("tab1: expected loom-alpha-1, got %s", m.IssueID)
		}

		extra := tab2.DrainMutations()
		if len(extra) > 0 {
			t.Errorf("tab2 should NOT receive ws-alpha mutation, got %d mutations", len(extra))
		}
	})

	t.Run("beta mutation reaches only tab2", func(t *testing.T) {
		hub.Broadcast(&realtime.MutationPayload{
			Type:        "update",
			IssueID:     "loom-beta-1",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: "ws-beta",
		})

		time.Sleep(100 * time.Millisecond)

		m, err := tab2.WaitForMutation(500 * time.Millisecond)
		if err != nil {
			t.Fatalf("tab2 should receive ws-beta mutation: %v", err)
		}
		if m.IssueID != "loom-beta-1" {
			t.Errorf("tab2: expected loom-beta-1, got %s", m.IssueID)
		}

		extra := tab1.DrainMutations()
		if len(extra) > 0 {
			t.Errorf("tab1 should NOT receive ws-beta mutation, got %d mutations", len(extra))
		}
	})

	t.Run("untagged mutation reaches neither tab", func(t *testing.T) {
		hub.Broadcast(&realtime.MutationPayload{
			Type:        "create",
			IssueID:     "loom-untagged",
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
			IssueID:     "loom-post-rename",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: "ws-alpha", // UUID unchanged
		})

		time.Sleep(100 * time.Millisecond)

		m, err := tab1.WaitForMutation(500 * time.Millisecond)
		if err != nil {
			t.Fatalf("tab1 should still receive ws-alpha mutations after rename: %v", err)
		}
		if m.IssueID != "loom-post-rename" {
			t.Errorf("tab1: expected loom-post-rename, got %s", m.IssueID)
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
			IssueID:     "loom-stale",
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
		wsCfg := testWorkspaceStore("alpha", workspaces)

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
		wsCfg := testWorkspaceStore("alpha", workspaces)

		handler := issues.HandleMoveIssue(svc, wsCfg)

		body := `{"target_workspace":"nonexistent"}`
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/alpha/issues/src-001/move", strings.NewReader(body))
		req.SetPathValue("ws", "alpha")
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
		wsCfg := testWorkspaceStore("alpha", workspaces)

		handler := issues.HandleMoveIssue(svc, wsCfg)

		body := `{"target_workspace":"alpha"}`
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/alpha/issues/src-001/move", strings.NewReader(body))
		req.SetPathValue("ws", "alpha")
		req.SetPathValue("id", "src-001")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
