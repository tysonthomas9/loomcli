package subscription

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
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

	multi := NewMultiWorkspaceSubscriber(hub, nil, nil, nil)
	// Manually wire up a subscriber for "ws-1" without going through MultiPool
	sub := NewDaemonSubscriber(pool, hub)
	sub.workspaceID = "ws-1"
	multi.mu.Lock()
	multi.subscribers["ws-1"] = &subscriberEntry{sub: sub}
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

	multi := NewMultiWorkspaceSubscriber(hub, nil, nil, nil)

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

	multi := NewMultiWorkspaceSubscriber(hub, nil, nil, nil)

	// Manually register subscribers for ws-1 and ws-2
	sub1 := NewDaemonSubscriber(ws1Pool, hub)
	sub1.workspaceID = "ws-1"
	sub2 := NewDaemonSubscriber(ws2Pool, hub)
	sub2.workspaceID = "ws-2"

	multi.mu.Lock()
	multi.subscribers["ws-1"] = &subscriberEntry{sub: sub1}
	multi.subscribers["ws-2"] = &subscriberEntry{sub: sub2}
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

// TestMultiWorkspaceSubscriber_AgentStatePathFnWired verifies that the
// agentStatePathFn passed to NewMultiWorkspaceSubscriber is stored on the
// struct so AddWorkspace can consult it when configuring per-workspace
// subscribers. Full end-to-end wiring through AddWorkspace requires a real
// MultiPool and is covered elsewhere; this test just pins the construction
// contract so a future refactor can't silently drop the hook.
func TestMultiWorkspaceSubscriber_AgentStatePathFnWired(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	called := make(map[string]int)
	fn := func(wsID string) string {
		called[wsID]++
		return "/tmp/fake-daemon-agents-" + wsID + ".json"
	}

	multi := NewMultiWorkspaceSubscriber(hub, nil, fn, nil)
	if multi == nil {
		t.Fatal("NewMultiWorkspaceSubscriber returned nil")
	}
	if multi.agentStatePathFn == nil {
		t.Fatal("expected agentStatePathFn to be stored on the struct")
	}

	// Sanity check: calling the stored fn should produce the expected path.
	if got := multi.agentStatePathFn("ws-x"); got != "/tmp/fake-daemon-agents-ws-x.json" {
		t.Errorf("stored fn returned %q, want %q", got, "/tmp/fake-daemon-agents-ws-x.json")
	}
	if called["ws-x"] != 1 {
		t.Errorf("expected fn to be invoked once for ws-x, got %d", called["ws-x"])
	}
}

// TestMultiWorkspaceSubscriber_AddWorkspaceCallsSetAgentStatePath verifies
// the full AddWorkspace wiring: the agentStatePathFn is invoked with the
// workspace ID and the resolved path is installed on the new DaemonSubscriber.
// Regression guard for the `if m.agentStatePathFn != nil` block in multi.go.
func TestMultiWorkspaceSubscriber_AddWorkspaceCallsSetAgentStatePath(t *testing.T) {
	// A minimal mock server so the per-workspace subscriber's goroutines have
	// something to talk to — the test does not depend on RPC behavior, only
	// on the path being stored on the subscriber before Start().
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{
				Status: "healthy", Version: "0.0.0", Compatible: true,
			})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		default:
			return rpc.Response{Success: true}
		}
	})
	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	mp := daemon.NewMultiPool(func(context.Context) string { return "" }, 1)
	defer func() { _ = mp.Close() }()
	if err := mp.Register("ws-w", pool); err != nil {
		t.Fatalf("failed to register pool: %v", err)
	}

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	const wantPath = "/tmp/fake-daemon-agents-ws-w.json"
	called := 0
	fn := func(wsID string) string {
		called++
		if wsID != "ws-w" {
			t.Errorf("agentStatePathFn invoked with wsID=%q, want ws-w", wsID)
		}
		return wantPath
	}

	multi := NewMultiWorkspaceSubscriber(hub, mp, fn, nil)
	defer multi.Stop()

	if err := multi.AddWorkspace("ws-w"); err != nil {
		t.Fatalf("AddWorkspace returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("agentStatePathFn called %d times, want 1", called)
	}

	multi.mu.RLock()
	entry, ok := multi.subscribers["ws-w"]
	multi.mu.RUnlock()
	if !ok {
		t.Fatal("expected subscriber entry for ws-w after AddWorkspace")
	}

	entry.sub.agentStateMu.RLock()
	got := entry.sub.agentStatePath
	entry.sub.agentStateMu.RUnlock()
	if got != wantPath {
		t.Errorf("subscriber agentStatePath = %q, want %q", got, wantPath)
	}
}
