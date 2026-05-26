package cli

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// mockIPCMutator implements ipcMutator for testing the decorator logic.
type mockIPCMutator struct {
	claimFn       func(issueID string, lockTTL time.Duration) error
	updateFn      func(issueID string, params backend.UpdateParams) error
	completeFn    func(issueID string, params backend.CloseParams) (*backend.CloseResult, error)
	releaseLockFn func(issueID string) error
}

func (m *mockIPCMutator) Claim(issueID string, lockTTL time.Duration) error {
	if m.claimFn != nil {
		return m.claimFn(issueID, lockTTL)
	}
	return nil
}

func (m *mockIPCMutator) Update(issueID string, params backend.UpdateParams) error {
	if m.updateFn != nil {
		return m.updateFn(issueID, params)
	}
	return nil
}

func (m *mockIPCMutator) Complete(issueID string, params backend.CloseParams) (*backend.CloseResult, error) {
	if m.completeFn != nil {
		return m.completeFn(issueID, params)
	}
	return &backend.CloseResult{}, nil
}

func (m *mockIPCMutator) ReleaseLock(issueID string) error {
	if m.releaseLockFn != nil {
		return m.releaseLockFn(issueID)
	}
	return nil
}

// --- IPC routing tests ---

func TestIPCIssueBackend_Update_RoutesToIPC(t *testing.T) {
	var ipcCalled bool
	ipc := &mockIPCMutator{
		updateFn: func(issueID string, params backend.UpdateParams) error {
			ipcCalled = true
			if issueID != "task-1" {
				t.Errorf("got issueID %q, want task-1", issueID)
			}
			return nil
		},
	}
	direct := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, direct)

	err := b.Update(context.Background(), "task-1", backend.UpdateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ipcCalled {
		t.Error("IPC Update was not called")
	}
	if direct.Called("Update") {
		t.Error("direct Update should NOT have been called")
	}
}

func TestIPCIssueBackend_ClaimIssue_RoutesToIPC(t *testing.T) {
	var ipcCalled bool
	ipc := &mockIPCMutator{
		claimFn: func(issueID string, lockTTL time.Duration) error {
			ipcCalled = true
			if issueID != "task-2" {
				t.Errorf("got issueID %q, want task-2", issueID)
			}
			if lockTTL != 5*time.Minute {
				t.Errorf("got lockTTL %v, want 5m", lockTTL)
			}
			return nil
		},
	}
	direct := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, direct)

	err := b.ClaimIssue(context.Background(), "task-2", 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ipcCalled {
		t.Error("IPC Claim was not called")
	}
	if direct.Called("ClaimIssue") {
		t.Error("direct ClaimIssue should NOT have been called")
	}
}

func TestIPCIssueBackend_Close_RoutesToIPC(t *testing.T) {
	closed := &backend.IssueData{ID: "task-3", Title: "closed"}
	want := &backend.CloseResult{Closed: closed}
	ipc := &mockIPCMutator{
		completeFn: func(issueID string, params backend.CloseParams) (*backend.CloseResult, error) {
			return want, nil
		},
	}
	direct := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, direct)

	got, err := b.Close(context.Background(), "task-3", backend.CloseParams{Reason: "done"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Closed.ID != "task-3" {
		t.Errorf("got issue ID %q, want task-3", got.Closed.ID)
	}
	if direct.Called("Close") {
		t.Error("direct Close should NOT have been called")
	}
}

// --- KindUnavailable propagation tests ---

func TestIPCIssueBackend_Update_PropagatesUnavailable(t *testing.T) {
	ipc := &mockIPCMutator{
		updateFn: func(string, backend.UpdateParams) error {
			return backend.ErrUnavailable("ipc.update", "daemon not running", nil)
		},
	}
	direct := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, direct)

	err := b.Update(context.Background(), "task-1", backend.UpdateParams{})
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("expected KindUnavailable, got %v", err)
	}
	if direct.Called("Update") {
		t.Error("direct Update should NOT have been called")
	}
}

func TestIPCIssueBackend_ClaimIssue_PropagatesUnavailable(t *testing.T) {
	ipc := &mockIPCMutator{
		claimFn: func(string, time.Duration) error {
			return backend.ErrUnavailable("ipc.claim", "daemon not running", nil)
		},
	}
	direct := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, direct)

	err := b.ClaimIssue(context.Background(), "task-1", 0)
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("expected KindUnavailable, got %v", err)
	}
	if direct.Called("ClaimIssue") {
		t.Error("direct ClaimIssue should NOT have been called")
	}
}

func TestIPCIssueBackend_Close_PropagatesUnavailable(t *testing.T) {
	ipc := &mockIPCMutator{
		completeFn: func(string, backend.CloseParams) (*backend.CloseResult, error) {
			return nil, backend.ErrUnavailable("ipc.complete", "daemon not running", nil)
		},
	}
	direct := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, direct)

	_, err := b.Close(context.Background(), "task-1", backend.CloseParams{})
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("expected KindUnavailable, got %v", err)
	}
	if direct.Called("Close") {
		t.Error("direct Close should NOT have been called")
	}
}

// --- Error propagation tests ---

func TestIPCIssueBackend_Update_PropagatesNonUnavailableError(t *testing.T) {
	ipc := &mockIPCMutator{
		updateFn: func(string, backend.UpdateParams) error {
			return backend.NewBackendError(backend.KindConflict, "ipc.update", "conflict", nil)
		},
	}
	direct := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, direct)

	err := b.Update(context.Background(), "task-1", backend.UpdateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Errorf("expected KindConflict, got %v", err)
	}
	if direct.Called("Update") {
		t.Error("direct should NOT be called on KindConflict")
	}
}

func TestIPCIssueBackend_ClaimIssue_PropagatesConflict(t *testing.T) {
	ipc := &mockIPCMutator{
		claimFn: func(string, time.Duration) error {
			return backend.NewBackendError(backend.KindConflict, "ipc.claim", "already claimed", nil)
		},
	}
	direct := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, direct)

	err := b.ClaimIssue(context.Background(), "task-1", 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Errorf("expected KindConflict, got %v", err)
	}
	if direct.Called("ClaimIssue") {
		t.Error("direct should NOT be called on KindConflict")
	}
}

func TestIPCIssueBackend_Close_PropagatesNotFound(t *testing.T) {
	ipc := &mockIPCMutator{
		completeFn: func(string, backend.CloseParams) (*backend.CloseResult, error) {
			return nil, backend.NewBackendError(backend.KindNotFound, "ipc.complete", "not found", nil)
		},
	}
	direct := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, direct)

	_, err := b.Close(context.Background(), "task-1", backend.CloseParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Errorf("expected KindNotFound, got %v", err)
	}
	if direct.Called("Close") {
		t.Error("direct should NOT be called on KindNotFound")
	}
}

// --- Direct backend delegation tests ---

func TestIPCIssueBackend_Ready_DelegatesToDirectBackend(t *testing.T) {
	ipc := &mockIPCMutator{}
	direct := NewMockIssueBackend()
	direct.ReadyResult = []backend.IssueData{{ID: "ready-1"}}
	b := newIPCIssueBackend(ipc, direct)

	got, err := b.Ready(context.Background(), backend.ReadyOpts{Limit: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "ready-1" {
		t.Errorf("got %v, want [{ID:ready-1}]", got)
	}
	if !direct.Called("Ready") {
		t.Error("direct Ready should have been called")
	}
}

func TestIPCIssueBackend_Get_DelegatesToDirectBackend(t *testing.T) {
	ipc := &mockIPCMutator{}
	fb := NewMockIssueBackend()
	fb.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "get-1"}}
	b := newIPCIssueBackend(ipc, fb)

	got, err := b.Get(context.Background(), "get-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "get-1" {
		t.Errorf("got ID %q, want get-1", got.ID)
	}
}

func TestIPCIssueBackend_List_DelegatesToDirectBackend(t *testing.T) {
	ipc := &mockIPCMutator{}
	fb := NewMockIssueBackend()
	fb.ListResult = []backend.IssueData{{ID: "list-1"}}
	b := newIPCIssueBackend(ipc, fb)

	got, err := b.List(context.Background(), backend.ListOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "list-1" {
		t.Errorf("got %v, want [{ID:list-1}]", got)
	}
}

func TestIPCIssueBackend_GetChildren_DelegatesToDirectBackend(t *testing.T) {
	ipc := &mockIPCMutator{}
	fb := NewMockIssueBackend()
	fb.GetChildrenResult = []backend.IssueData{{ID: "child-1"}}
	b := newIPCIssueBackend(ipc, fb)

	got, err := b.GetChildren(context.Background(), "epic-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "child-1" {
		t.Errorf("got %v, want [{ID:child-1}]", got)
	}
	if !fb.Called("GetChildren") {
		t.Error("direct GetChildren should have been called")
	}
}

func TestIPCIssueBackend_SearchIssues_DelegatesToDirectBackend(t *testing.T) {
	ipc := &mockIPCMutator{}
	fb := NewMockIssueBackend()
	fb.SearchIssuesResult = []backend.IssueData{{ID: "search-1"}}
	b := newIPCIssueBackend(ipc, fb)

	got, err := b.SearchIssues(context.Background(), "auth bug", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "search-1" {
		t.Errorf("got %v, want [{ID:search-1}]", got)
	}
	if !fb.Called("SearchIssues") {
		t.Error("direct SearchIssues should have been called")
	}
}

func TestIPCIssueBackend_DeferIssue_DelegatesToDirectBackend(t *testing.T) {
	ipc := &mockIPCMutator{}
	fb := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, fb)

	until := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	if err := b.DeferIssue(context.Background(), "loom-1", until); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fb.Called("DeferIssue") {
		t.Error("direct DeferIssue should have been called")
	}
}

func TestIPCIssueBackend_UndeferIssue_DelegatesToDirectBackend(t *testing.T) {
	ipc := &mockIPCMutator{}
	fb := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, fb)

	if err := b.UndeferIssue(context.Background(), "loom-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fb.Called("UndeferIssue") {
		t.Error("direct UndeferIssue should have been called")
	}
}

func TestIPCIssueBackend_Create_DelegatesToDirectBackend(t *testing.T) {
	ipc := &mockIPCMutator{}
	fb := NewMockIssueBackend()
	fb.CreateResult = &backend.IssueData{ID: "new-1"}
	b := newIPCIssueBackend(ipc, fb)

	got, err := b.Create(context.Background(), backend.CreateParams{Title: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "new-1" {
		t.Errorf("got ID %q, want new-1", got.ID)
	}
}

// --- BackendName test ---

func TestIPCIssueBackend_BackendName(t *testing.T) {
	ipc := &mockIPCMutator{}
	fb := NewMockIssueBackend()
	fb.BackendNameResult = "fleetdb"
	b := newIPCIssueBackend(ipc, fb)

	got := b.BackendName()
	if got != "ipc:fleetdb" {
		t.Errorf("got %q, want ipc:fleetdb", got)
	}
}

func TestIPCIssueBackend_BackendName_DefaultMock(t *testing.T) {
	ipc := &mockIPCMutator{}
	fb := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, fb)

	got := b.BackendName()
	if got != "ipc:mock" {
		t.Errorf("got %q, want ipc:mock", got)
	}
}

// --- defaultIssueBackend integration tests ---

func TestDefaultIssueBackend_WithDaemonSocket(t *testing.T) {
	t.Setenv("LOOM_DAEMON_SOCKET", "/tmp/test-ipc.sock")
	t.Setenv("LOOM_AGENT_NAME", "test-agent")

	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	ib := defaultIssueBackend()
	_, ok := ib.(*ipcIssueBackend)
	if !ok {
		t.Errorf("expected *ipcIssueBackend, got %T", ib)
	}
}

func TestDefaultIssueBackend_WithoutDaemonSocket(t *testing.T) {
	t.Setenv("LOOM_DAEMON_SOCKET", "")

	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	ib := defaultIssueBackend()
	if _, ok := ib.(*ipcIssueBackend); ok {
		t.Error("should NOT return *ipcIssueBackend when LOOM_DAEMON_SOCKET is empty")
	}
}

func TestDefaultIssueBackend_DaemonSocket_BackendName(t *testing.T) {
	t.Setenv("LOOM_DAEMON_SOCKET", "/tmp/test-ipc.sock")
	t.Setenv("LOOM_AGENT_NAME", "test-agent")

	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	ib := defaultIssueBackend()
	name := ib.BackendName()
	if len(name) < 4 || name[:4] != "ipc:" {
		t.Errorf("expected BackendName starting with 'ipc:', got %q", name)
	}
}

func TestDefaultIssueBackend_SetOverrideBypassesIPC(t *testing.T) {
	t.Setenv("LOOM_DAEMON_SOCKET", "/tmp/test-ipc.sock")

	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	mock := NewMockIssueBackend()
	mock.BackendNameResult = "override"
	setDefaultIssueBackend(mock)

	ib := defaultIssueBackend()
	if ib.BackendName() != "override" {
		t.Errorf("expected override backend, got %q", ib.BackendName())
	}
}

// --- Concurrent safety test ---

func TestIPCIssueBackend_ConcurrentAccess(t *testing.T) {
	ipc := &mockIPCMutator{
		updateFn: func(string, backend.UpdateParams) error {
			return nil
		},
		claimFn: func(string, time.Duration) error {
			return nil
		},
		completeFn: func(string, backend.CloseParams) (*backend.CloseResult, error) {
			return &backend.CloseResult{}, nil
		},
	}
	fb := NewMockIssueBackend()
	b := newIPCIssueBackend(ipc, fb)
	ctx := context.Background()

	// Run concurrent mutations and queries — should not race
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			_ = b.Update(ctx, fmt.Sprintf("task-%d", n), backend.UpdateParams{})
			_ = b.ClaimIssue(ctx, fmt.Sprintf("task-%d", n), 0)
			_, _ = b.Close(ctx, fmt.Sprintf("task-%d", n), backend.CloseParams{})
			_, _ = b.Ready(ctx, backend.ReadyOpts{})
			_ = b.BackendName()
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
