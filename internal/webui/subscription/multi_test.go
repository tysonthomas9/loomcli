package subscription

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// TestGetMutationsSinceForWorkspace_KnownWorkspace verifies that querying
// a workspace with an active subscriber returns its mutations.
func TestGetMutationsSinceForWorkspace_KnownWorkspace(t *testing.T) {
	ts := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	mutations := []rpc.MutationEvent{
		{Type: "create", IssueID: "bd-ws1-1", Timestamp: ts},
		{Type: "update", IssueID: "bd-ws1-2", Timestamp: ts},
	}
	mutData, _ := json.Marshal(mutations)

	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{
				Status: "healthy", Version: "0.0.0", Compatible: true,
			})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "get_mutations":
			return rpc.Response{Success: true, Data: mutData}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	multi := NewMultiWorkspaceSubscriber(hub, nil, nil)
	// Manually wire up a subscriber for "ws-1" without going through MultiPool
	sub := NewDaemonSubscriber(pool, hub)
	sub.workspaceID = "ws-1"
	multi.mu.Lock()
	multi.subscribers["ws-1"] = sub
	multi.mu.Unlock()

	got := multi.GetMutationsSinceForWorkspace("ws-1", 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 mutations, got %d", len(got))
	}
	if got[0].IssueID != "bd-ws1-1" {
		t.Errorf("expected first mutation IssueID bd-ws1-1, got %s", got[0].IssueID)
	}
	if got[1].IssueID != "bd-ws1-2" {
		t.Errorf("expected second mutation IssueID bd-ws1-2, got %s", got[1].IssueID)
	}
}

// TestGetMutationsSinceForWorkspace_UnknownWorkspace verifies that querying
// a workspace with no active subscriber returns nil.
func TestGetMutationsSinceForWorkspace_UnknownWorkspace(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	multi := NewMultiWorkspaceSubscriber(hub, nil, nil)

	got := multi.GetMutationsSinceForWorkspace("no-such-ws", 0)
	if got != nil {
		t.Errorf("expected nil for unknown workspace, got %v", got)
	}
}

// TestGetMutationsSinceForWorkspace_OnlyQueriesCorrectSubscriber verifies that
// GetMutationsSinceForWorkspace only queries the subscriber for the requested
// workspace, not other workspace subscribers.
func TestGetMutationsSinceForWorkspace_OnlyQueriesCorrectSubscriber(t *testing.T) {
	ts := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)

	// Set up ws-1 mock server returning ws-1 mutations
	ws1Mutations := []rpc.MutationEvent{
		{Type: "create", IssueID: "bd-from-ws1", Timestamp: ts},
	}
	ws1Data, _ := json.Marshal(ws1Mutations)
	ws1Socket := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "get_mutations":
			return rpc.Response{Success: true, Data: ws1Data}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})
	ws1Pool := newSubscriptionMockPool(ws1Socket)
	defer ws1Pool.Close()

	// Set up ws-2 mock server returning ws-2 mutations
	ws2Mutations := []rpc.MutationEvent{
		{Type: "update", IssueID: "bd-from-ws2", Timestamp: ts},
	}
	ws2Data, _ := json.Marshal(ws2Mutations)
	ws2Socket := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "get_mutations":
			return rpc.Response{Success: true, Data: ws2Data}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})
	ws2Pool := newSubscriptionMockPool(ws2Socket)
	defer ws2Pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	multi := NewMultiWorkspaceSubscriber(hub, nil, nil)

	// Manually register subscribers for ws-1 and ws-2
	sub1 := NewDaemonSubscriber(ws1Pool, hub)
	sub1.workspaceID = "ws-1"
	sub2 := NewDaemonSubscriber(ws2Pool, hub)
	sub2.workspaceID = "ws-2"

	multi.mu.Lock()
	multi.subscribers["ws-1"] = sub1
	multi.subscribers["ws-2"] = sub2
	multi.mu.Unlock()

	// Query ws-1 only
	got := multi.GetMutationsSinceForWorkspace("ws-1", 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 mutation from ws-1, got %d", len(got))
	}
	if got[0].IssueID != "bd-from-ws1" {
		t.Errorf("expected bd-from-ws1, got %s", got[0].IssueID)
	}

	// Query ws-2 only
	got = multi.GetMutationsSinceForWorkspace("ws-2", 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 mutation from ws-2, got %d", len(got))
	}
	if got[0].IssueID != "bd-from-ws2" {
		t.Errorf("expected bd-from-ws2, got %s", got[0].IssueID)
	}
}
