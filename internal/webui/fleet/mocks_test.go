package fleet

import (
	"context"
	"errors"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// mockFleetClient implements fleetClaimClient for testing.
type mockFleetClient struct {
	updateFunc func(args *rpc.UpdateArgs) (*rpc.Response, error)
	readyFunc  func(args *rpc.ReadyArgs) (*rpc.Response, error)
}

func (m *mockFleetClient) Update(args *rpc.UpdateArgs) (*rpc.Response, error) {
	if m.updateFunc != nil {
		return m.updateFunc(args)
	}
	return nil, errors.New("updateFunc not implemented")
}

func (m *mockFleetClient) Ready(args *rpc.ReadyArgs) (*rpc.Response, error) {
	if m.readyFunc != nil {
		return m.readyFunc(args)
	}
	return nil, errors.New("readyFunc not implemented")
}

// mockFleetPool implements fleetClaimPoolGetter for testing.
//
// putCount and discardCount let tests verify the pool-cleanup branch taken
// (Put for healthy connections, Discard for transport-corrupted connections).
// Required for loomcli-67meg-style transport-vs-logical-failure assertions.
type mockFleetPool struct {
	getFunc func(ctx context.Context) (fleetClaimClient, error)
	putFunc func(client fleetClaimClient)

	mu           sync.Mutex
	putCount     int
	discardCount int
}

func (m *mockFleetPool) Get(ctx context.Context) (fleetClaimClient, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *mockFleetPool) Put(client fleetClaimClient) {
	m.mu.Lock()
	m.putCount++
	m.mu.Unlock()
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

func (m *mockFleetPool) Discard(client fleetClaimClient) {
	m.mu.Lock()
	m.discardCount++
	m.mu.Unlock()
}

// counts returns a snapshot of put/discard counts for assertions.
func (m *mockFleetPool) counts() (put, discard int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.putCount, m.discardCount
}
