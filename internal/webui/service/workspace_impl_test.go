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

func TestListWorkspaces_StoreBackedMarksActiveWithoutDefault(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "BETA")

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BETA", Name: "Beta Project"}); err != nil {
		t.Fatalf("create beta: %v", err)
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
			if item.IsDefault {
				t.Fatalf("BETA should not be marked default: %+v", item)
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

func TestAddWorkspaceReposNormalizesCloneURLInput(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "alpha"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}

	var got WorkspaceAddReposRequest
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		Store: st,
		AddReposFn: func(_ context.Context, req WorkspaceAddReposRequest) (WorkspaceCreateResult, error) {
			got = req
			return WorkspaceCreateResult{WorkspaceID: "ALPHA"}, nil
		},
	})

	if _, err := svc.AddWorkspaceRepos(ctx, WorkspaceAddReposRequest{
		WorkspaceID: "ALPHA",
		Repos:       []string{" https://github.com/octocat/Hello-World "},
	}); err != nil {
		t.Fatalf("AddWorkspaceRepos returned error: %v", err)
	}
	if len(got.Repos) != 0 {
		t.Fatalf("Repos = %#v, want none", got.Repos)
	}
	if len(got.CloneURLs) != 1 || got.CloneURLs[0] != "https://github.com/octocat/Hello-World" {
		t.Fatalf("CloneURLs = %#v, want GitHub URL", got.CloneURLs)
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

func TestGetWorkspace_StoreBackedCachesTopologyAcrossRepeatedCalls(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	base := memstore.New()
	if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BETA", Name: "Beta Project"}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if _, err := base.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "ALPHA", Name: "repo-a"}); err != nil {
		t.Fatalf("create alpha repo: %v", err)
	}
	if _, err := base.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "BETA", Name: "repo-b"}); err != nil {
		t.Fatalf("create beta repo: %v", err)
	}
	if _, err := base.Daemon().Upsert(ctx, &domain.DaemonProfile{WorkspaceKey: "BETA", AgentBackend: "codex"}); err != nil {
		t.Fatalf("upsert beta daemon: %v", err)
	}

	counted := newWorkspaceCountingStore(base)
	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: counted})

	first, err := svc.GetWorkspace(ctx, "ALPHA")
	if err != nil {
		t.Fatalf("GetWorkspace first: %v", err)
	}
	if len(first.Repos) != 1 || first.Repos[0].Name != "repo-a" {
		t.Fatalf("first repos = %+v, want repo-a", first.Repos)
	}
	first.Repos[0].Name = "mutated"

	for i := 0; i < 2; i++ {
		data, err := svc.GetWorkspace(ctx, "ALPHA")
		if err != nil {
			t.Fatalf("GetWorkspace cached %d: %v", i, err)
		}
		if len(data.Repos) != 1 || data.Repos[0].Name != "repo-a" {
			t.Fatalf("cached repos = %+v, want independent cached copy", data.Repos)
		}
	}

	if got := counted.workspaces.getCalls; got != 1 {
		t.Fatalf("workspace Get calls = %d, want one cached topology load", got)
	}
	if got := counted.workspaces.listCalls; got != 1 {
		t.Fatalf("workspace List calls = %d, want one summary load", got)
	}
	if got := counted.repos.listByWorkspace["ALPHA"]; got != 2 {
		t.Fatalf("ALPHA repo List calls = %d, want active workspace repos plus summary", got)
	}
	if got := counted.repos.listByWorkspace["BETA"]; got != 1 {
		t.Fatalf("BETA repo List calls = %d, want one cross-workspace summary read", got)
	}
	if got := counted.daemon.getByWorkspace["BETA"]; got != 1 {
		t.Fatalf("BETA daemon Get calls = %d, want one cross-workspace summary read", got)
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

func TestPatchWorkspaceDesignFormat_StoreBackedUpdatesWorkspace(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project", DesignFormat: "markdown"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})
	data, err := svc.PatchWorkspaceDesignFormat(ctx, "ALPHA", "html")
	if err != nil {
		t.Fatalf("PatchWorkspaceDesignFormat: %v", err)
	}
	if data.ID != "ALPHA" || data.DesignFormat != "html" {
		t.Fatalf("workspace data = %+v", data)
	}
	ws, err := st.Workspaces().Get(ctx, "ALPHA")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if ws.DesignFormat != "html" {
		t.Fatalf("DesignFormat = %q, want html", ws.DesignFormat)
	}
}

func TestPatchWorkspaceDesignFormat_RejectsInvalidFormat(t *testing.T) {
	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: memstore.New()})
	if _, err := svc.PatchWorkspaceDesignFormat(context.Background(), "ALPHA", "svg"); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestPatchWorkspaceEvalPolicy_StoreBackedUpdatesWorkspace(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})
	sampling := 50
	batch := 75
	data, err := svc.PatchWorkspaceEvalPolicy(ctx, "ALPHA", WorkspaceEvalPolicyPatch{
		EvalSamplingPercent: &sampling,
		EvalBatchSize:       &batch,
	})
	if err != nil {
		t.Fatalf("PatchWorkspaceEvalPolicy: %v", err)
	}
	if data.ID != "ALPHA" || data.EvalSamplingPercent != 50 || data.EvalBatchSize != 75 {
		t.Fatalf("workspace data = %+v", data)
	}
	ws, err := st.Workspaces().Get(ctx, "ALPHA")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if ws.EvalSamplingPercent != 50 || ws.EvalBatchSize != 75 || ws.EvalLookbackDays != 0 {
		t.Fatalf("eval policy = %+v", ws)
	}
}

func TestPatchWorkspaceEvalPolicy_RejectsInvalidValues(t *testing.T) {
	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: memstore.New()})
	zero := 0
	if _, err := svc.PatchWorkspaceEvalPolicy(context.Background(), "ALPHA", WorkspaceEvalPolicyPatch{EvalLookbackDays: &zero}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSetDefaultWorkspace_Removed(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})

	data, err := svc.SetDefaultWorkspace(ctx, "Alpha Project")
	if data != nil {
		t.Fatalf("data = %+v, want nil", data)
	}
	var serr *ServiceError
	if !errors.As(err, &serr) || serr.Kind != KindUnavailable {
		t.Fatalf("SetDefaultWorkspace err = %v, want unavailable ServiceError", err)
	}
}

func TestClearDefaultWorkspace_Removed(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})

	data, err := svc.ClearDefaultWorkspace(ctx)
	if data != nil {
		t.Fatalf("data = %+v, want nil", data)
	}
	var serr *ServiceError
	if !errors.As(err, &serr) || serr.Kind != KindUnavailable {
		t.Fatalf("ClearDefaultWorkspace err = %v, want unavailable ServiceError", err)
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

type workspaceCountingStore struct {
	store.Store
	workspaces *workspaceCountingWorkspaceStore
	repos      *workspaceCountingRepoStore
	daemon     *workspaceCountingDaemonStore
}

func newWorkspaceCountingStore(base store.Store) *workspaceCountingStore {
	return &workspaceCountingStore{
		Store:      base,
		workspaces: &workspaceCountingWorkspaceStore{WorkspaceStore: base.Workspaces()},
		repos:      &workspaceCountingRepoStore{RepoStore: base.Repos(), listByWorkspace: make(map[string]int)},
		daemon:     &workspaceCountingDaemonStore{DaemonProfileStore: base.Daemon(), getByWorkspace: make(map[string]int)},
	}
}

func (s *workspaceCountingStore) Workspaces() store.WorkspaceStore { return s.workspaces }
func (s *workspaceCountingStore) Repos() store.RepoStore           { return s.repos }
func (s *workspaceCountingStore) Daemon() store.DaemonProfileStore { return s.daemon }

type workspaceCountingWorkspaceStore struct {
	store.WorkspaceStore
	getCalls  int
	listCalls int
}

func (s *workspaceCountingWorkspaceStore) Get(ctx context.Context, key string) (*domain.Workspace, error) {
	s.getCalls++
	return s.WorkspaceStore.Get(ctx, key)
}

func (s *workspaceCountingWorkspaceStore) List(ctx context.Context) ([]*domain.Workspace, error) {
	s.listCalls++
	return s.WorkspaceStore.List(ctx)
}

type workspaceCountingRepoStore struct {
	store.RepoStore
	listByWorkspace map[string]int
}

func (s *workspaceCountingRepoStore) List(ctx context.Context, workspaceKey string) ([]*domain.Repo, error) {
	s.listByWorkspace[workspaceKey]++
	return s.RepoStore.List(ctx, workspaceKey)
}

type workspaceCountingDaemonStore struct {
	store.DaemonProfileStore
	getByWorkspace map[string]int
}

func (s *workspaceCountingDaemonStore) Get(ctx context.Context, workspaceKey string) (*domain.DaemonProfile, error) {
	s.getByWorkspace[workspaceKey]++
	return s.DaemonProfileStore.Get(ctx, workspaceKey)
}
