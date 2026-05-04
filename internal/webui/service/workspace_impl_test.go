package service

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
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

func TestGetActiveWorkspace_StoreBackedReturnsData(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")

	st := memstore.New()
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		Store: st,
	})

	data, err := svc.GetActiveWorkspace(context.Background())
	if err != nil {
		t.Fatalf("GetActiveWorkspace returned error: %v", err)
	}
	if data == nil {
		t.Fatal("GetActiveWorkspace returned nil")
	}
}

func TestCreateWorkspace_StoreBackedReturnsCreatedWorkspaceData(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "alpha"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if err := bootstrap.SetActiveWorkspaceKey("ALPHA"); err != nil {
		t.Fatalf("set active workspace: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{
		Store: st,
		CreateFn: func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
			if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BETA", Name: req.Name}); err != nil {
				return WorkspaceCreateResult{}, err
			}
			return WorkspaceCreateResult{WorkspaceID: "BETA"}, nil
		},
	})

	data, _, err := svc.CreateWorkspace(ctx, WorkspaceCreateRequest{Name: "beta", Type: "empty"})
	if err != nil {
		t.Fatalf("CreateWorkspace returned error: %v", err)
	}
	if data == nil {
		t.Fatal("CreateWorkspace returned nil data")
	}
	if data.ID != "BETA" {
		t.Fatalf("data.ID = %q, want BETA", data.ID)
	}
	if data.Name != "beta" {
		t.Fatalf("data.Name = %q, want beta", data.Name)
	}
	if len(data.Workspaces) != 2 {
		t.Fatalf("workspace summary count = %d, want 2", len(data.Workspaces))
	}
}

func TestGetWorkspace_StoreBackedMissReturnsNotFound(t *testing.T) {
	st := memstore.New()
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		Store: st,
	})

	_, err := svc.GetWorkspace(context.Background(), "MISSING")
	var se *ServiceError
	if !errors.As(err, &se) || se.Kind != KindNotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestGetWorkspaceBackend_StoreBackedReadsDaemonProfile(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Daemon().Upsert(ctx, &domain.DaemonProfile{WorkspaceKey: "ALPHA", AgentBackend: "codex"}); err != nil {
		t.Fatalf("upsert daemon profile: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})
	cfg, err := svc.GetWorkspaceBackend(ctx, "ALPHA")
	if err != nil {
		t.Fatalf("GetWorkspaceBackend: %v", err)
	}
	if cfg.Backend != "codex" {
		t.Fatalf("Backend = %q, want codex", cfg.Backend)
	}
	if cfg.Source != "fleetdb" {
		t.Fatalf("Source = %q, want fleetdb", cfg.Source)
	}
}

func TestGetWorkspaceBackend_StoreBackedDefaultsToCodex(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})
	cfg, err := svc.GetWorkspaceBackend(ctx, "ALPHA")
	if err != nil {
		t.Fatalf("GetWorkspaceBackend: %v", err)
	}
	if cfg.Backend != "codex" {
		t.Fatalf("Backend = %q, want codex", cfg.Backend)
	}
	if cfg.Source != "default" {
		t.Fatalf("Source = %q, want default", cfg.Source)
	}
}

func TestPatchWorkspaceBackend_StoreBackedWritesDaemonProfile(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})
	data, err := svc.PatchWorkspaceBackend(ctx, "ALPHA", "codex")
	if err != nil {
		t.Fatalf("PatchWorkspaceBackend: %v", err)
	}
	if data.ID != "ALPHA" {
		t.Fatalf("data.ID = %q, want ALPHA", data.ID)
	}
	profile, err := st.Daemon().Get(ctx, "ALPHA")
	if err != nil {
		t.Fatalf("get daemon profile: %v", err)
	}
	if profile.AgentBackend != "codex" {
		t.Fatalf("AgentBackend = %q, want codex", profile.AgentBackend)
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
			t.Fatal("store-backed set default should not call old SetDefaultFn")
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
			t.Fatal("store-backed clear default should not call old ClearDefaultFn")
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

func TestGetWorkspaceJob_StoreFallbackSurvivesJobStoreLoss(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "CLONE-WS", Name: "clone-ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	cloning := domain.WorkspaceStateCloning
	if _, err := st.Workspaces().Update(ctx, "CLONE-WS", store.WorkspaceUpdate{State: &cloning}); err != nil {
		t.Fatalf("mark cloning: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})
	job, err := svc.GetWorkspaceJob(ctx, "CLONE-WS")
	if err != nil {
		t.Fatalf("GetWorkspaceJob returned error: %v", err)
	}
	if job.Status != JobStatusRunning {
		t.Fatalf("status = %q, want running", job.Status)
	}
	if job.Progress != "cloning repository..." {
		t.Fatalf("progress = %q", job.Progress)
	}
	if job.WorkspaceID != "CLONE-WS" {
		t.Fatalf("workspace_id = %q, want CLONE-WS", job.WorkspaceID)
	}
}

func TestGetWorkspaceJob_StoreFallbackReturnsFailedForErrorWorkspace(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "CLONE-WS", Name: "clone-ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	failed := domain.WorkspaceStateError
	msg := "git clone failed"
	if _, err := st.Workspaces().Update(ctx, "CLONE-WS", store.WorkspaceUpdate{
		State:        &failed,
		ErrorMessage: &msg,
	}); err != nil {
		t.Fatalf("mark error: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})
	job, err := svc.GetWorkspaceJob(ctx, "CLONE-WS")
	if err != nil {
		t.Fatalf("GetWorkspaceJob returned error: %v", err)
	}
	if job.Status != JobStatusFailed {
		t.Fatalf("status = %q, want failed", job.Status)
	}
	if job.Error != msg {
		t.Fatalf("error = %q, want %q", job.Error, msg)
	}
}
