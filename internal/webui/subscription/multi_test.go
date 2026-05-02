package subscription

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// TestGetMutationsSinceForWorkspace_KnownWorkspace verifies that querying
// a workspace with an active subscriber returns its mutations.
func TestGetMutationsSinceForWorkspace_KnownWorkspace(t *testing.T) {
	ts := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	multi := NewMultiWorkspaceSubscriber(hub, nil)
	multi.AddWorkspaceWithBackend("ws-1", &fakeBackend{getFn: func(_ context.Context, _ int64) ([]backend.MutationData, error) {
		return []backend.MutationData{
			{Type: "create", IssueID: "fleet-ws1-1", Timestamp: ts},
			{Type: "update", IssueID: "fleet-ws1-2", Timestamp: ts},
		}, nil
	}})

	got := multi.GetMutationsSinceForWorkspace("ws-1", "0")
	if len(got) != 2 {
		t.Fatalf("expected 2 mutations, got %d", len(got))
	}
	if got[0].IssueID != "fleet-ws1-1" {
		t.Errorf("expected first mutation IssueID fleet-ws1-1, got %s", got[0].IssueID)
	}
	if got[1].IssueID != "fleet-ws1-2" {
		t.Errorf("expected second mutation IssueID fleet-ws1-2, got %s", got[1].IssueID)
	}
}

// TestGetMutationsSinceForWorkspace_UnknownWorkspace verifies that querying
// a workspace with no active subscriber returns nil.
func TestGetMutationsSinceForWorkspace_UnknownWorkspace(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	multi := NewMultiWorkspaceSubscriber(hub, nil)

	got := multi.GetMutationsSinceForWorkspace("no-such-ws", "0")
	if got != nil {
		t.Errorf("expected nil for unknown workspace, got %v", got)
	}
}

// TestGetMutationsSinceForWorkspace_OnlyQueriesCorrectSubscriber verifies that
// GetMutationsSinceForWorkspace only queries the subscriber for the requested
// workspace, not other workspace subscribers.
func TestGetMutationsSinceForWorkspace_OnlyQueriesCorrectSubscriber(t *testing.T) {
	ts := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	multi := NewMultiWorkspaceSubscriber(hub, nil)
	multi.AddWorkspaceWithBackend("ws-1", &fakeBackend{getFn: func(_ context.Context, _ int64) ([]backend.MutationData, error) {
		return []backend.MutationData{{Type: "create", IssueID: "fleet-from-ws1", Timestamp: ts}}, nil
	}})
	multi.AddWorkspaceWithBackend("ws-2", &fakeBackend{getFn: func(_ context.Context, _ int64) ([]backend.MutationData, error) {
		return []backend.MutationData{{Type: "update", IssueID: "fleet-from-ws2", Timestamp: ts}}, nil
	}})

	// Query ws-1 only
	got := multi.GetMutationsSinceForWorkspace("ws-1", "0")
	if len(got) != 1 {
		t.Fatalf("expected 1 mutation from ws-1, got %d", len(got))
	}
	if got[0].IssueID != "fleet-from-ws1" {
		t.Errorf("expected fleet-from-ws1, got %s", got[0].IssueID)
	}

	// Query ws-2 only
	got = multi.GetMutationsSinceForWorkspace("ws-2", "0")
	if len(got) != 1 {
		t.Fatalf("expected 1 mutation from ws-2, got %d", len(got))
	}
	if got[0].IssueID != "fleet-from-ws2" {
		t.Errorf("expected fleet-from-ws2, got %s", got[0].IssueID)
	}
}

// TestAddWorkspaceWithBackend_Idempotent verifies that calling
// AddWorkspaceWithBackend twice for the same wsID does not start a second
// subscriber, mirroring AddWorkspace's idempotent contract.
func TestAddWorkspaceWithBackend_Idempotent(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	multi := NewMultiWorkspaceSubscriber(hub, nil)
	defer multi.Stop()

	be := &fakeBackend{}
	if err := multi.AddWorkspaceWithBackend("ws-fleet-1", be); err != nil {
		t.Fatalf("first AddWorkspaceWithBackend: %v", err)
	}
	if err := multi.AddWorkspaceWithBackend("ws-fleet-1", be); err != nil {
		t.Fatalf("second AddWorkspaceWithBackend: %v", err)
	}

	if !multi.HasSubscriber("ws-fleet-1") {
		t.Error("expected subscriber for ws-fleet-1 after Add")
	}
	if ids := multi.WorkspaceIDs(); len(ids) != 1 {
		t.Errorf("expected 1 subscriber, got %v", ids)
	}
}

// TestAddWorkspaceWithBackend_NilBackend_Errors verifies the input
// validation guard (nil backend should not silently start a subscriber
// with a typed-nil reference).
func TestAddWorkspaceWithBackend_NilBackend_Errors(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	multi := NewMultiWorkspaceSubscriber(hub, nil)
	defer multi.Stop()

	if err := multi.AddWorkspaceWithBackend("ws-nil", nil); err == nil {
		t.Error("expected error when backend is nil")
	}
	if multi.HasSubscriber("ws-nil") {
		t.Error("subscriber should not be registered when backend is nil")
	}
}

// TestAddWorkspaceWithBackend_TOCTOUSafe verifies that two concurrent
// AddWorkspaceWithBackend calls for the same wsID result in exactly one
// subscriber, not two. The mu.Lock() guard in the implementation closes
// the time-of-check / time-of-use window between the existence check and
// the insertion.
func TestAddWorkspaceWithBackend_TOCTOUSafe(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	multi := NewMultiWorkspaceSubscriber(hub, nil)
	defer multi.Stop()

	const goroutines = 16
	be := &fakeBackend{}
	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			_ = multi.AddWorkspaceWithBackend("ws-race", be)
		}()
	}
	close(start)
	wg.Wait()

	if ids := multi.WorkspaceIDs(); len(ids) != 1 {
		t.Errorf("expected exactly 1 subscriber under concurrent activation, got %v", ids)
	}
}
