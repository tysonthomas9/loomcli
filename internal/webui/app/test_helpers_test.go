package app

import (
	"context"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/ops"
	storepkg "github.com/tysonthomas9/loomcli/internal/store"
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

// testWorkspaceStore returns a FleetDB-style workspace store for testing.
func testWorkspaceStore(_ string, workspaces []ops.WorkspaceSummary) storepkg.Store {
	st := memstore.New()
	for _, ws := range workspaces {
		key := ws.ID
		if key == "" {
			key = ws.Name
		}
		if key == "" {
			continue
		}
		_, _ = st.Workspaces().Create(context.Background(), storepkg.WorkspaceCreate{Key: key, Name: ws.Name})
	}
	return st
}

// mockFileOps implements Source Control's private FileMechanics seam for tests.
type mockFileOps struct {
	resolveFunc       func(name string) (*sourcecontrol.Worktree, error)
	resolveWsRootFunc func() (string, error)
	resolveWsDataFunc func() (*sourcecontrol.WorkspaceTopology, error)
}

func (m *mockFileOps) ResolveAgentWorktree(_, name string) (*sourcecontrol.Worktree, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(name)
	}
	return nil, errors.New("not found")
}

func (m *mockFileOps) ResolveAgentWorktreeForRepo(_, name, _ string) (*sourcecontrol.Worktree, error) {
	return m.ResolveAgentWorktree("", name)
}

func (m *mockFileOps) ResolveWorkspaceRoot(_ string) (string, error) {
	if m.resolveWsRootFunc != nil {
		return m.resolveWsRootFunc()
	}
	return "", errors.New("not found")
}

func (m *mockFileOps) ResolveWorkspaceData(_ string) (*sourcecontrol.WorkspaceTopology, error) {
	if m.resolveWsDataFunc != nil {
		return m.resolveWsDataFunc()
	}
	return nil, errors.New("not found")
}

func (m *mockFileOps) GitStatusPorcelain(_ context.Context, worktreePath string) (sourcecontrol.GitFileStatusResult, error) {
	return sourcecontrol.GitFileStatusResult{Entries: map[string]string{}}, nil
}

func (m *mockFileOps) GitShowFileAtRev(_ context.Context, worktreePath, rev, path string, maxBytes int64) (*sourcecontrol.GitFileContentAtRev, error) {
	return &sourcecontrol.GitFileContentAtRev{Content: []byte(""), Size: 0}, nil
}

func (m *mockFileOps) GitDiffFile(_ context.Context, worktreePath, path, from, to string) (sourcecontrol.GitBoundedTextResult, error) {
	return sourcecontrol.GitBoundedTextResult{}, nil
}

func (m *mockFileOps) GitLogFile(_ context.Context, worktreePath, path string, limit int) (sourcecontrol.GitBoundedTextResult, error) {
	return sourcecontrol.GitBoundedTextResult{}, nil
}

func (m *mockFileOps) GitBlamePorcelain(_ context.Context, worktreePath, path string) (sourcecontrol.GitBoundedTextResult, error) {
	return sourcecontrol.GitBoundedTextResult{}, nil
}

func (m *mockFileOps) ResolveLoomDataDir() (string, error) {
	return "", errors.New("loom data directory not configured")
}

func (m *mockFileOps) GitCurrentBranch(_ context.Context, _ string) (string, error) {
	return "main", nil
}

func (m *mockFileOps) RepairCheckout(_, _, _, _ string, _ bool) (sourcecontrol.RepairResult, error) {
	return sourcecontrol.RepairResult{Repaired: false, Method: "none", Message: "not implemented"}, nil
}
