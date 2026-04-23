package service

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// stubPool is a minimal daemon.Pool for workspace registration in tests.
type stubPool struct{}

func (stubPool) Get(_ context.Context) (*rpc.Client, error) { return nil, nil }
func (stubPool) Put(_ *rpc.Client)                          {}
func (stubPool) PutAfterError(_ *rpc.Client)                {}
func (stubPool) Discard(_ *rpc.Client)                      {}
func (stubPool) Stats() daemon.PoolStats                    { return daemon.PoolStats{} }
func (stubPool) Close() error                               { return nil }

func TestListWorkspaces_EnrichesRepoCountAndIsDefault(t *testing.T) {
	mp := daemon.NewMultiPool(func(_ context.Context) string { return "" }, 1)
	if err := mp.Register("ws-uuid-alpha", stubPool{}); err != nil {
		t.Fatalf("register alpha: %v", err)
	}
	if err := mp.Register("ws-uuid-beta", stubPool{}); err != nil {
		t.Fatalf("register beta: %v", err)
	}

	configFn := func() (*ops.WorkspaceData, error) {
		return &ops.WorkspaceData{
			Workspaces: []ops.WorkspaceSummary{
				{ID: "ws-uuid-alpha", Name: "alpha", Path: "/p/alpha", Active: true, RepoCount: 3, IsDefault: true},
				{ID: "ws-uuid-beta", Name: "beta", Path: "/p/beta", Active: false, RepoCount: 0, IsDefault: false},
			},
		}, nil
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{ConfigFn: configFn, MultiPool: mp})

	items, err := svc.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}

	byID := map[string]WorkspaceListItem{}
	for _, it := range items {
		byID[it.ID] = it
	}

	alpha, ok := byID["ws-uuid-alpha"]
	if !ok {
		t.Fatal("alpha missing")
	}
	if alpha.RepoCount != 3 || !alpha.IsDefault {
		t.Errorf("alpha enrichment: RepoCount=%d IsDefault=%v, want 3,true", alpha.RepoCount, alpha.IsDefault)
	}

	beta, ok := byID["ws-uuid-beta"]
	if !ok {
		t.Fatal("beta missing")
	}
	if beta.RepoCount != 0 || beta.IsDefault {
		t.Errorf("beta enrichment: RepoCount=%d IsDefault=%v, want 0,false", beta.RepoCount, beta.IsDefault)
	}
}

func TestListWorkspaces_NilConfigFnLeavesZeroDefaults(t *testing.T) {
	mp := daemon.NewMultiPool(func(_ context.Context) string { return "" }, 1)
	if err := mp.Register("ws-only-pool", stubPool{}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// No configFn: enrichment block must be skipped; RepoCount=0, IsDefault=false.
	svc := NewWorkspaceService(WorkspaceServiceConfig{ConfigFn: nil, MultiPool: mp})

	items, err := svc.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if items[0].RepoCount != 0 || items[0].IsDefault {
		t.Errorf("want zero defaults, got RepoCount=%d IsDefault=%v", items[0].RepoCount, items[0].IsDefault)
	}
}

func TestPoolStatsFromDaemon(t *testing.T) {
	input := daemon.PoolStats{
		Size:      10,
		Created:   7,
		Active:    3,
		Available: 4,
		Closed:    true,
	}

	got := poolStatsFromDaemon(input)

	if got.Size != input.Size {
		t.Errorf("Size = %d, want %d", got.Size, input.Size)
	}
	if got.Created != input.Created {
		t.Errorf("Created = %d, want %d", got.Created, input.Created)
	}
	if got.Active != input.Active {
		t.Errorf("Active = %d, want %d", got.Active, input.Active)
	}
	if got.Available != input.Available {
		t.Errorf("Available = %d, want %d", got.Available, input.Available)
	}
	if got.Closed != input.Closed {
		t.Errorf("Closed = %v, want %v", got.Closed, input.Closed)
	}
}

func TestPoolStatsFromDaemon_Zero(t *testing.T) {
	got := poolStatsFromDaemon(daemon.PoolStats{})

	if got.Size != 0 {
		t.Errorf("Size = %d, want 0", got.Size)
	}
	if got.Created != 0 {
		t.Errorf("Created = %d, want 0", got.Created)
	}
	if got.Active != 0 {
		t.Errorf("Active = %d, want 0", got.Active)
	}
	if got.Available != 0 {
		t.Errorf("Available = %d, want 0", got.Available)
	}
	if got.Closed != false {
		t.Errorf("Closed = %v, want false", got.Closed)
	}
}
