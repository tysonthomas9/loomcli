package service

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

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

// TestGetWorkspace_FleetModeFallback reproduces the bug where
// GET /api/workspaces/{uuid} returned 404 in fleet mode because the
// multiPool is intentionally empty (no beads daemon) even though the
// workspace is registered and reachable via configByIDFn.
//
// With the fix, GetWorkspace falls back to configByIDFn / configFn when
// the multiPool has no entry for the workspace.
func TestGetWorkspace_FleetModeFallback(t *testing.T) {
	const (
		wsID   = "11111111-2222-3333-4444-555555555555"
		wsName = "PARITY"
		wsPath = "/tmp/parity-workspace"
	)

	fleetData := &ops.WorkspaceData{
		ID:   wsID,
		Name: wsName,
		Path: wsPath,
		Workspaces: []ops.WorkspaceSummary{
			{ID: wsID, Name: wsName, Path: wsPath},
		},
	}

	configByIDCalls := 0
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		MultiPool: nil, // fleet mode: no beads daemon, so no multiPool
		ConfigByIDFn: func(id string) (*ops.WorkspaceData, error) {
			configByIDCalls++
			if id != wsID {
				return nil, errors.New("workspace not found: " + id)
			}
			return fleetData, nil
		},
		ConfigFn: func() (*ops.WorkspaceData, error) {
			return fleetData, nil
		},
	})

	got, err := svc.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("GetWorkspace returned error in fleet mode: %v", err)
	}
	if got == nil {
		t.Fatal("GetWorkspace returned nil data in fleet mode")
	}
	if got.ID != wsID {
		t.Errorf("got.ID = %q, want %q", got.ID, wsID)
	}
	if got.Name != wsName {
		t.Errorf("got.Name = %q, want %q", got.Name, wsName)
	}
	if configByIDCalls == 0 {
		t.Error("configByIDFn was not consulted during fleet-mode fallback")
	}

	// Sanity: unknown UUID still 404s.
	if _, err := svc.GetWorkspace(context.Background(), "not-a-real-uuid"); err == nil {
		t.Error("GetWorkspace for unknown UUID should return error, got nil")
	} else {
		var se *ServiceError
		if !errors.As(err, &se) || se.Kind != KindNotFound {
			t.Errorf("GetWorkspace for unknown UUID: got %v, want NotFound", err)
		}
	}
}

// TestGetWorkspace_ConfigFnFallback covers the path where configByIDFn is
// unwired but configFn still has the workspace — the service should
// synthesize a WorkspaceData from the summary rather than 404.
func TestGetWorkspace_ConfigFnFallback(t *testing.T) {
	const wsID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	svc := NewWorkspaceService(WorkspaceServiceConfig{
		MultiPool:    nil,
		ConfigByIDFn: nil,
		ConfigFn: func() (*ops.WorkspaceData, error) {
			return &ops.WorkspaceData{
				Workspaces: []ops.WorkspaceSummary{
					{ID: wsID, Name: "alpha", Path: "/tmp/alpha"},
					{ID: "other-id", Name: "beta", Path: "/tmp/beta"},
				},
			}, nil
		},
	})

	got, err := svc.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("GetWorkspace returned error: %v", err)
	}
	if got == nil {
		t.Fatal("GetWorkspace returned nil data")
	}
	if got.ID != wsID {
		t.Errorf("got.ID = %q, want %q", got.ID, wsID)
	}
	if got.Name != "alpha" {
		t.Errorf("got.Name = %q, want %q", got.Name, "alpha")
	}

	// Workspaces summary list should mark the matched one active.
	activeCount := 0
	for _, ws := range got.Workspaces {
		if ws.Active {
			activeCount++
			if ws.ID != wsID {
				t.Errorf("wrong workspace marked active: %q", ws.ID)
			}
		}
	}
	if activeCount != 1 {
		t.Errorf("expected exactly 1 active workspace, got %d", activeCount)
	}
}

func TestDeleteWorkspace_StoreBackedUsesWorkspaceKey(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	var deletedKey string
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		Store: st,
		DeleteFn: func(key string) error {
			deletedKey = key
			return st.Workspaces().Delete(ctx, key)
		},
	})

	if _, err := svc.DeleteWorkspace(ctx, "ALPHA"); err != nil {
		t.Fatalf("DeleteWorkspace returned error: %v", err)
	}
	if deletedKey != "ALPHA" {
		t.Fatalf("deleted key = %q, want ALPHA", deletedKey)
	}
	if _, err := st.Workspaces().Get(ctx, "ALPHA"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("workspace still exists or unexpected error: %v", err)
	}
}

func TestListWorkspaces_StoreBackedMarksActiveAndDefault(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BETA", Name: "Beta Project"}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if err := bootstrap.SetActiveWorkspaceKey("BETA"); err != nil {
		t.Fatalf("set active workspace: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{
		Store: st,
		ConfigFn: func() (*ops.WorkspaceData, error) {
			t.Fatal("store-backed list should not call legacy configFn")
			return nil, nil
		},
	})

	items, err := svc.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
	for _, item := range items {
		if item.ID == "BETA" {
			if !item.Active {
				t.Fatalf("BETA should be active: %+v", item)
			}
			if !item.IsDefault {
				t.Fatalf("BETA should be default: %+v", item)
			}
		}
	}
}

func TestSetDefaultWorkspace_StoreBackedResolvesNameAndReturnsStoreData(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{
		Store: st,
		SetDefaultFn: func(string) error {
			t.Fatal("store-backed set default should not call legacy SetDefaultFn")
			return nil
		},
	})

	data, err := svc.SetDefaultWorkspace(ctx, "Alpha Project")
	if err != nil {
		t.Fatalf("SetDefaultWorkspace: %v", err)
	}
	if data.ID != "ALPHA" {
		t.Fatalf("data.ID = %q, want ALPHA", data.ID)
	}
	if data.DefaultWorkspace != "Alpha Project" {
		t.Fatalf("DefaultWorkspace = %q, want display name", data.DefaultWorkspace)
	}
	for _, ws := range data.Workspaces {
		if ws.ID == "ALPHA" && !ws.IsDefault {
			t.Fatalf("ALPHA summary should be default: %+v", ws)
		}
	}
}

func TestClearDefaultWorkspace_StoreBackedClearsStateCache(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if err := bootstrap.SetActiveWorkspaceKey("ALPHA"); err != nil {
		t.Fatalf("set active workspace: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{
		Store: st,
		ClearDefaultFn: func() error {
			t.Fatal("store-backed clear default should not call legacy ClearDefaultFn")
			return nil
		},
	})

	data, err := svc.ClearDefaultWorkspace(ctx)
	if err != nil {
		t.Fatalf("ClearDefaultWorkspace: %v", err)
	}
	if data.DefaultWorkspace != "" {
		t.Fatalf("DefaultWorkspace = %q, want empty", data.DefaultWorkspace)
	}
	items, err := svc.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	for _, item := range items {
		if item.IsDefault {
			t.Fatalf("no workspace should remain default after clear: %+v", item)
		}
	}
}
