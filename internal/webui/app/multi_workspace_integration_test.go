package app

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
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
