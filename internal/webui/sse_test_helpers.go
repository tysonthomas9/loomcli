package webui

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TODO: testDaemon helpers require rpc.Server and memory storage (server-side
// packages not vendored). Re-enable when loomcli vendors the full rpc server
// or provides its own test infrastructure.

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
