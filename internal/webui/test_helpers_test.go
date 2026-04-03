package webui

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// testSSEClient wraps a realtime.Client for tests, providing convenience methods.
type testSSEClient struct {
	client *realtime.Client
	hub    *realtime.Hub
}

// WaitForMutation waits for a mutation to arrive on the client's send channel.
func (tc *testSSEClient) WaitForMutation(timeout time.Duration) (*realtime.MutationPayload, error) {
	select {
	case m, ok := <-tc.client.Send():
		if !ok {
			return nil, context.DeadlineExceeded
		}
		return m, nil
	case <-time.After(timeout):
		return nil, context.DeadlineExceeded
	}
}

// WaitForMutations waits for n mutations to arrive.
func (tc *testSSEClient) WaitForMutations(n int, timeout time.Duration) ([]*realtime.MutationPayload, error) {
	var results []*realtime.MutationPayload
	deadline := time.After(timeout)
	for i := 0; i < n; i++ {
		select {
		case m, ok := <-tc.client.Send():
			if !ok {
				return results, context.DeadlineExceeded
			}
			results = append(results, m)
		case <-deadline:
			return results, context.DeadlineExceeded
		}
	}
	return results, nil
}

// DrainMutations returns all buffered mutations without blocking.
func (tc *testSSEClient) DrainMutations() []*realtime.MutationPayload {
	var results []*realtime.MutationPayload
	for {
		select {
		case m, ok := <-tc.client.Send():
			if !ok {
				return results
			}
			results = append(results, m)
		default:
			return results
		}
	}
}

// Close unregisters the client from the hub and closes the done channel.
func (tc *testSSEClient) Close() {
	tc.hub.UnregisterClient(tc.client)
	close(tc.client.Done())
}

// stubPool is a minimal daemon.Pool for handler tests.
type stubPool struct{}

func (s *stubPool) Get(_ context.Context) (*rpc.Client, error) { return &rpc.Client{}, nil }
func (s *stubPool) Put(_ *rpc.Client)                          {}
func (s *stubPool) PutAfterError(_ *rpc.Client)                {}
func (s *stubPool) Discard(_ *rpc.Client)                      {}
func (s *stubPool) Stats() daemon.PoolStats {
	return daemon.PoolStats{Size: 10, Created: 2, Active: 1, Available: 1}
}
func (s *stubPool) Close() error { return nil }
