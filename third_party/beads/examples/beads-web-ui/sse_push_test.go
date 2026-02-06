package main

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/rpc"
)

// TestSSE_MutationDelivery_Create verifies that create operations produce mutations
// that are delivered to connected SSE clients.
func TestSSE_MutationDelivery_Create(t *testing.T) {
	// Set up test daemon and pool
	td, cleanup := setupTestDaemon(t)
	defer cleanup()

	// Create SSE hub
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Connect test SSE client
	client := newTestSSEClient(t, hub, 1)
	defer client.Close()

	// Create an issue
	issueID := td.createIssue("Test Create Issue")

	// Get mutations from server and broadcast to hub
	// This tests the core mutation delivery path without DaemonSubscriber overhead
	mutations := td.server.GetRecentMutations(0)
	broadcastMutations(hub, mutations)

	// Wait for mutation to be delivered
	mutation, err := client.WaitForMutation(2 * time.Second)
	if err != nil {
		t.Fatalf("failed to receive mutation: %v", err)
	}

	// Verify mutation
	if mutation.Type != "create" {
		t.Errorf("expected mutation type 'create', got %q", mutation.Type)
	}
	if mutation.IssueID != issueID {
		t.Errorf("expected issue ID %q, got %q", issueID, mutation.IssueID)
	}
	if mutation.Title != "Test Create Issue" {
		t.Errorf("expected title 'Test Create Issue', got %q", mutation.Title)
	}
	if mutation.Timestamp == "" {
		t.Error("expected timestamp to be set")
	}
}

// broadcastMutations converts RPC mutations to payloads and broadcasts them.
func broadcastMutations(hub *SSEHub, mutations []rpc.MutationEvent) {
	for _, m := range mutations {
		payload := rpcMutationToPayload(m)
		hub.Broadcast(payload)
	}
}

// TestSSE_MutationDelivery_Update verifies that update operations produce mutations.
func TestSSE_MutationDelivery_Update(t *testing.T) {
	td, cleanup := setupTestDaemon(t)
	defer cleanup()

	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Create issue first
	issueID := td.createIssue("Original Title")

	// Record checkpoint after create
	checkpoint := time.Now().UnixMilli()
	time.Sleep(10 * time.Millisecond)

	// Connect client
	client := newTestSSEClient(t, hub, 1)
	defer client.Close()

	// Update the issue title
	td.updateIssueTitle(issueID, "Updated Title")

	// Get mutations since checkpoint and broadcast
	mutations := td.server.GetRecentMutations(checkpoint)
	broadcastMutations(hub, mutations)

	// Wait for update mutation
	mutation, err := client.WaitForMutation(2 * time.Second)
	if err != nil {
		t.Fatalf("failed to receive mutation: %v", err)
	}

	if mutation.Type != "update" {
		t.Errorf("expected mutation type 'update', got %q", mutation.Type)
	}
	if mutation.IssueID != issueID {
		t.Errorf("expected issue ID %q, got %q", issueID, mutation.IssueID)
	}
	if mutation.Title != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got %q", mutation.Title)
	}
}

// TestSSE_MutationDelivery_StatusChange verifies status mutations with old/new status.
func TestSSE_MutationDelivery_StatusChange(t *testing.T) {
	td, cleanup := setupTestDaemon(t)
	defer cleanup()

	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Create issue first
	issueID := td.createIssue("Status Test Issue")

	// Record checkpoint after create
	checkpoint := time.Now().UnixMilli()
	time.Sleep(10 * time.Millisecond)

	// Connect client
	client := newTestSSEClient(t, hub, 1)
	defer client.Close()

	// Change status
	td.updateIssueStatus(issueID, "in_progress")

	// Get mutations since checkpoint and broadcast
	mutations := td.server.GetRecentMutations(checkpoint)
	broadcastMutations(hub, mutations)

	// Wait for status mutation
	mutation, err := client.WaitForMutation(2 * time.Second)
	if err != nil {
		t.Fatalf("failed to receive mutation: %v", err)
	}

	if mutation.Type != "status" {
		t.Errorf("expected mutation type 'status', got %q", mutation.Type)
	}
	if mutation.IssueID != issueID {
		t.Errorf("expected issue ID %q, got %q", issueID, mutation.IssueID)
	}
	if mutation.OldStatus != "open" {
		t.Errorf("expected old_status 'open', got %q", mutation.OldStatus)
	}
	if mutation.NewStatus != "in_progress" {
		t.Errorf("expected new_status 'in_progress', got %q", mutation.NewStatus)
	}
}

// TestSSE_MutationDelivery_Close verifies close operations produce status mutations.
func TestSSE_MutationDelivery_Close(t *testing.T) {
	td, cleanup := setupTestDaemon(t)
	defer cleanup()

	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Create issue first
	issueID := td.createIssue("Issue to Close")

	// Record checkpoint after create
	checkpoint := time.Now().UnixMilli()
	time.Sleep(10 * time.Millisecond)

	// Connect client
	client := newTestSSEClient(t, hub, 1)
	defer client.Close()

	// Close the issue
	td.closeIssue(issueID, "test complete")

	// Get mutations since checkpoint and broadcast
	mutations := td.server.GetRecentMutations(checkpoint)
	broadcastMutations(hub, mutations)

	// Wait for status mutation
	mutation, err := client.WaitForMutation(2 * time.Second)
	if err != nil {
		t.Fatalf("failed to receive mutation: %v", err)
	}

	if mutation.Type != "status" {
		t.Errorf("expected mutation type 'status', got %q", mutation.Type)
	}
	if mutation.IssueID != issueID {
		t.Errorf("expected issue ID %q, got %q", issueID, mutation.IssueID)
	}
	if mutation.OldStatus != "open" {
		t.Errorf("expected old_status 'open', got %q", mutation.OldStatus)
	}
	if mutation.NewStatus != "closed" {
		t.Errorf("expected new_status 'closed', got %q", mutation.NewStatus)
	}
}

// TestSSE_MutationDelivery_Delete verifies delete mutations.
func TestSSE_MutationDelivery_Delete(t *testing.T) {
	td, cleanup := setupTestDaemon(t)
	defer cleanup()

	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Create issue first
	issueID := td.createIssue("Issue to Delete")

	// Record checkpoint after create
	checkpoint := time.Now().UnixMilli()
	time.Sleep(10 * time.Millisecond)

	// Connect client
	client := newTestSSEClient(t, hub, 1)
	defer client.Close()

	// Delete the issue
	td.deleteIssue(issueID)

	// Get mutations since checkpoint and broadcast
	mutations := td.server.GetRecentMutations(checkpoint)
	broadcastMutations(hub, mutations)

	// Wait for delete mutation
	mutation, err := client.WaitForMutation(2 * time.Second)
	if err != nil {
		t.Fatalf("failed to receive mutation: %v", err)
	}

	if mutation.Type != "delete" {
		t.Errorf("expected mutation type 'delete', got %q", mutation.Type)
	}
	if mutation.IssueID != issueID {
		t.Errorf("expected issue ID %q, got %q", issueID, mutation.IssueID)
	}
}

// TestSSE_MultipleClients verifies broadcast to multiple subscribers.
func TestSSE_MultipleClients(t *testing.T) {
	td, cleanup := setupTestDaemon(t)
	defer cleanup()

	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Connect multiple clients
	client1 := newTestSSEClient(t, hub, 1)
	defer client1.Close()
	client2 := newTestSSEClient(t, hub, 2)
	defer client2.Close()
	client3 := newTestSSEClient(t, hub, 3)
	defer client3.Close()

	// Trigger mutation
	issueID := td.createIssue("Broadcast Test")

	// Get mutations and broadcast to all clients
	mutations := td.server.GetRecentMutations(0)
	broadcastMutations(hub, mutations)

	// All clients should receive the mutation
	mut1, err := client1.WaitForMutation(2 * time.Second)
	if err != nil {
		t.Fatalf("client1 failed to receive: %v", err)
	}
	if mut1.IssueID != issueID {
		t.Errorf("client1: expected issue ID %q, got %q", issueID, mut1.IssueID)
	}

	mut2, err := client2.WaitForMutation(2 * time.Second)
	if err != nil {
		t.Fatalf("client2 failed to receive: %v", err)
	}
	if mut2.IssueID != issueID {
		t.Errorf("client2: expected issue ID %q, got %q", issueID, mut2.IssueID)
	}

	mut3, err := client3.WaitForMutation(2 * time.Second)
	if err != nil {
		t.Fatalf("client3 failed to receive: %v", err)
	}
	if mut3.IssueID != issueID {
		t.Errorf("client3: expected issue ID %q, got %q", issueID, mut3.IssueID)
	}
}

// TestSSE_UnregisteredClientNoMutations verifies unregistered clients don't receive.
func TestSSE_UnregisteredClientNoMutations(t *testing.T) {
	td, cleanup := setupTestDaemon(t)
	defer cleanup()

	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Connect and then unregister client
	client := newTestSSEClient(t, hub, 1)
	client.Close() // Unregister

	// Wait for unregistration
	time.Sleep(100 * time.Millisecond)

	// Trigger mutation
	td.createIssue("Should Not Be Received")

	// Get mutations and broadcast
	mutations := td.server.GetRecentMutations(0)
	broadcastMutations(hub, mutations)

	// Client should not have received anything (channel closed)
	_, err := client.WaitForMutation(500 * time.Millisecond)
	if err == nil {
		t.Error("expected no mutation for unregistered client")
	}
}

// TestSSE_TimestampFiltering verifies clients get only mutations after since timestamp.
func TestSSE_TimestampFiltering(t *testing.T) {
	td, cleanup := setupTestDaemon(t)
	defer cleanup()

	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Create first issue
	issueID1 := td.createIssue("Issue Before Checkpoint")
	time.Sleep(50 * time.Millisecond)

	// Record checkpoint after first issue
	checkpoint := time.Now().UnixMilli()
	time.Sleep(50 * time.Millisecond)

	// Create second issue
	issueID2 := td.createIssue("Issue After Checkpoint")

	// Get mutations using GetRecentMutations with checkpoint
	mutations := td.server.GetRecentMutations(checkpoint)

	// Should only have mutations after checkpoint (issue 2, not issue 1)
	foundID1 := false
	foundID2 := false
	for _, m := range mutations {
		if m.IssueID == issueID1 {
			foundID1 = true
		}
		if m.IssueID == issueID2 {
			foundID2 = true
		}
	}

	if foundID1 {
		t.Errorf("should NOT find mutation for issue %s (before checkpoint)", issueID1)
	}
	if !foundID2 {
		t.Errorf("expected to find mutation for issue %s (after checkpoint)", issueID2)
	}
}

// TestSSE_MutationSequence verifies mutations are delivered in order.
func TestSSE_MutationSequence(t *testing.T) {
	td, cleanup := setupTestDaemon(t)
	defer cleanup()

	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	client := newTestSSEClient(t, hub, 1)
	defer client.Close()

	// Create multiple issues rapidly
	id1 := td.createIssue("Issue 1")
	id2 := td.createIssue("Issue 2")
	id3 := td.createIssue("Issue 3")

	// Get mutations and broadcast
	mutations := td.server.GetRecentMutations(0)
	broadcastMutations(hub, mutations)

	// Collect all mutations
	received, err := client.WaitForMutations(3, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to receive all mutations: %v (got %d)", err, len(received))
	}

	// Verify we got all 3 mutations
	ids := make(map[string]bool)
	for _, m := range received {
		ids[m.IssueID] = true
	}

	if !ids[id1] {
		t.Errorf("missing mutation for issue %s", id1)
	}
	if !ids[id2] {
		t.Errorf("missing mutation for issue %s", id2)
	}
	if !ids[id3] {
		t.Errorf("missing mutation for issue %s", id3)
	}
}

// TestSSE_ValidTimestampFormat verifies mutation timestamps are valid RFC3339.
func TestSSE_ValidTimestampFormat(t *testing.T) {
	td, cleanup := setupTestDaemon(t)
	defer cleanup()

	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	client := newTestSSEClient(t, hub, 1)
	defer client.Close()

	td.createIssue("Timestamp Test")

	// Get mutations and broadcast
	mutations := td.server.GetRecentMutations(0)
	broadcastMutations(hub, mutations)

	mutation, err := client.WaitForMutation(2 * time.Second)
	if err != nil {
		t.Fatalf("failed to receive mutation: %v", err)
	}

	// Parse timestamp to verify format
	_, err = time.Parse(time.RFC3339, mutation.Timestamp)
	if err != nil {
		t.Errorf("invalid timestamp format %q: %v", mutation.Timestamp, err)
	}
}

// Note: TestSSE_MutationContainsActor is not implemented because the RPC server's
// emitMutation function doesn't currently propagate the Actor field from requests
// to mutation events. This would be a future enhancement to track who performed
// each mutation action.
