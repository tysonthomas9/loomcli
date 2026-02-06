package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/beads/examples/beads-web-ui/daemon"
	"github.com/steveyegge/beads/internal/rpc"
	"github.com/steveyegge/beads/internal/storage/memory"
)

// testDaemon holds a test RPC server setup for SSE push testing.
type testDaemon struct {
	server     *rpc.Server
	socketPath string
	pool       *daemon.ConnectionPool
	cancel     context.CancelFunc
	tmpDir     string
	t          *testing.T
}

// setupTestDaemon creates a test RPC server with in-memory storage for SSE push testing.
// Returns the test daemon and a cleanup function.
func setupTestDaemon(t *testing.T) (*testDaemon, func()) {
	t.Helper()

	// Create temp directory for socket (empty string uses OS-appropriate temp dir)
	tmpDir, err := os.MkdirTemp("", "beads-sse-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	socketPath := filepath.Join(tmpDir, "rpc.sock")

	// Create in-memory storage
	store := memory.New(filepath.Join(tmpDir, "test.jsonl"))

	// Create RPC server
	server := rpc.NewServer(socketPath, store, tmpDir, filepath.Join(tmpDir, "test.db"))

	// Start server in background
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := server.Start(ctx); err != nil {
			// Expected error when server is stopped
			return
		}
	}()

	// Wait for server socket to appear
	maxWait := 50
	for i := 0; i < maxWait; i++ {
		time.Sleep(10 * time.Millisecond)
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if i == maxWait-1 {
			cancel()
			server.Stop()
			os.RemoveAll(tmpDir)
			t.Fatalf("Server socket not created after waiting")
		}
	}

	// Create connection pool
	pool, err := daemon.NewConnectionPool(socketPath, 5)
	if err != nil {
		cancel()
		server.Stop()
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create pool: %v", err)
	}

	td := &testDaemon{
		server:     server,
		socketPath: socketPath,
		pool:       pool,
		cancel:     cancel,
		tmpDir:     tmpDir,
		t:          t,
	}

	cleanup := func() {
		pool.Close()
		cancel()
		server.Stop()
		os.RemoveAll(tmpDir)
	}

	return td, cleanup
}

// createIssue creates a test issue via RPC and returns its ID.
func (td *testDaemon) createIssue(title string) string {
	td.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := td.pool.Get(ctx)
	if err != nil {
		td.t.Fatalf("failed to get connection: %v", err)
	}
	defer td.pool.Put(client)

	client.SetActor("test-user")

	args := &rpc.CreateArgs{
		Title:     title,
		IssueType: "task",
		Priority:  2,
		CreatedBy: "test-user",
	}

	resp, err := client.Create(args)
	if err != nil {
		td.t.Fatalf("RPC call failed: %v", err)
	}
	if !resp.Success {
		td.t.Fatalf("create failed: %s", resp.Error)
	}

	// Parse response to get issue ID
	var issue struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		td.t.Fatalf("failed to parse response: %v", err)
	}

	return issue.ID
}

// updateIssueTitle updates an issue's title.
func (td *testDaemon) updateIssueTitle(id, newTitle string) {
	td.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := td.pool.Get(ctx)
	if err != nil {
		td.t.Fatalf("failed to get connection: %v", err)
	}
	defer td.pool.Put(client)

	client.SetActor("test-user")

	args := &rpc.UpdateArgs{
		ID:    id,
		Title: &newTitle,
	}

	resp, err := client.Update(args)
	if err != nil {
		td.t.Fatalf("RPC call failed: %v", err)
	}
	if !resp.Success {
		td.t.Fatalf("update failed: %s", resp.Error)
	}
}

// updateIssueStatus updates an issue's status.
func (td *testDaemon) updateIssueStatus(id, newStatus string) {
	td.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := td.pool.Get(ctx)
	if err != nil {
		td.t.Fatalf("failed to get connection: %v", err)
	}
	defer td.pool.Put(client)

	client.SetActor("test-user")

	args := &rpc.UpdateArgs{
		ID:     id,
		Status: &newStatus,
	}

	resp, err := client.Update(args)
	if err != nil {
		td.t.Fatalf("RPC call failed: %v", err)
	}
	if !resp.Success {
		td.t.Fatalf("status update failed: %s", resp.Error)
	}
}

// closeIssue closes an issue.
func (td *testDaemon) closeIssue(id, reason string) {
	td.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := td.pool.Get(ctx)
	if err != nil {
		td.t.Fatalf("failed to get connection: %v", err)
	}
	defer td.pool.Put(client)

	client.SetActor("test-user")

	args := &rpc.CloseArgs{
		ID:     id,
		Reason: reason,
	}

	resp, err := client.CloseIssue(args)
	if err != nil {
		td.t.Fatalf("RPC call failed: %v", err)
	}
	if !resp.Success {
		td.t.Fatalf("close failed: %s", resp.Error)
	}
}

// deleteIssue deletes an issue.
func (td *testDaemon) deleteIssue(id string) {
	td.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := td.pool.Get(ctx)
	if err != nil {
		td.t.Fatalf("failed to get connection: %v", err)
	}
	defer td.pool.Put(client)

	client.SetActor("test-user")

	args := &rpc.DeleteArgs{
		IDs:   []string{id},
		Force: true,
	}

	resp, err := client.Delete(args)
	if err != nil {
		td.t.Fatalf("RPC call failed: %v", err)
	}
	if !resp.Success {
		td.t.Fatalf("delete failed: %s", resp.Error)
	}
}

// testSSEClient is a helper for testing SSE mutation delivery.
type testSSEClient struct {
	client *SSEClient
	hub    *SSEHub
	mu     sync.Mutex
	closed bool
}

// newTestSSEClient creates a test SSE client connected to the hub.
func newTestSSEClient(t *testing.T, hub *SSEHub, id int64) *testSSEClient {
	t.Helper()

	client := &SSEClient{
		id:   id,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}

	hub.RegisterClient(client)

	// Wait for registration to complete
	time.Sleep(50 * time.Millisecond)

	return &testSSEClient{
		client: client,
		hub:    hub,
	}
}

// Close unregisters the client from the hub.
// Note: We don't close client.done here - it should only be closed by the
// client's owner/creator. In production, the HTTP handler closes it.
func (tc *testSSEClient) Close() {
	tc.mu.Lock()
	if tc.closed {
		tc.mu.Unlock()
		return
	}
	tc.closed = true
	tc.mu.Unlock()

	tc.hub.UnregisterClient(tc.client)
}

// WaitForMutation waits for a mutation with the given timeout.
// Returns the mutation or error if timeout.
func (tc *testSSEClient) WaitForMutation(timeout time.Duration) (*MutationPayload, error) {
	select {
	case mutation, ok := <-tc.client.send:
		if !ok {
			return nil, context.Canceled
		}
		return mutation, nil
	case <-time.After(timeout):
		return nil, context.DeadlineExceeded
	}
}

// WaitForMutations collects multiple mutations.
func (tc *testSSEClient) WaitForMutations(count int, timeout time.Duration) ([]*MutationPayload, error) {
	var mutations []*MutationPayload
	deadline := time.After(timeout)

	for i := 0; i < count; i++ {
		select {
		case mutation, ok := <-tc.client.send:
			if !ok {
				return mutations, context.Canceled
			}
			mutations = append(mutations, mutation)
		case <-deadline:
			return mutations, context.DeadlineExceeded
		}
	}

	return mutations, nil
}

// DrainMutations reads all pending mutations from the channel without blocking.
func (tc *testSSEClient) DrainMutations() []*MutationPayload {
	var mutations []*MutationPayload
	for {
		select {
		case mutation, ok := <-tc.client.send:
			if !ok {
				return mutations
			}
			mutations = append(mutations, mutation)
		default:
			return mutations
		}
	}
}
