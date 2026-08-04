package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localnodeconfig"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

type workspaceAgentDirectoryStub struct {
	agents    []*agentsmodule.Agent
	roles     []*agentsmodule.Role
	listCalls int
}

type workspaceCapabilityStub struct {
	setDesignFormatFn func(context.Context, workspacemodule.SetDesignFormatCommand) (*workspacemodule.Reference, error)
	deleteFn          func(context.Context, workspacemodule.DeleteCommand) (*workspacemodule.Reference, error)
}

func (stub *workspaceCapabilityStub) Resolve(context.Context, workspacemodule.ResolveQuery) (*workspacemodule.Reference, error) {
	return nil, workspacemodule.ErrNotFound
}

func (stub *workspaceCapabilityStub) List(context.Context, workspacemodule.ListQuery) ([]workspacemodule.Reference, error) {
	return nil, nil
}

func (stub *workspaceCapabilityStub) Rename(context.Context, workspacemodule.RenameCommand) (*workspacemodule.Reference, error) {
	return nil, workspacemodule.ErrUnavailable
}

func (stub *workspaceCapabilityStub) SetDesignFormat(ctx context.Context, command workspacemodule.SetDesignFormatCommand) (*workspacemodule.Reference, error) {
	if stub.setDesignFormatFn == nil {
		return nil, workspacemodule.ErrInvalid
	}
	return stub.setDesignFormatFn(ctx, command)
}

func (stub *workspaceCapabilityStub) Delete(ctx context.Context, command workspacemodule.DeleteCommand) (*workspacemodule.Reference, error) {
	if stub.deleteFn == nil {
		return nil, workspacemodule.ErrUnavailable
	}
	return stub.deleteFn(ctx, command)
}

func (stub *workspaceCapabilityStub) GetRepository(context.Context, workspacemodule.GetRepositoryQuery) (*workspacemodule.Repository, error) {
	return nil, workspacemodule.ErrUnavailable
}

func (stub *workspaceCapabilityStub) ListRepositories(context.Context, workspacemodule.ListRepositoriesQuery) ([]workspacemodule.Repository, error) {
	return nil, workspacemodule.ErrUnavailable
}

func (stub *workspaceAgentDirectoryStub) ListAgents(
	_ context.Context,
	_ string,
	_ agentsmodule.AgentFilter,
) ([]*agentsmodule.Agent, error) {
	stub.listCalls++
	return stub.agents, nil
}

func (stub *workspaceAgentDirectoryStub) ListRoles(context.Context, string) ([]*agentsmodule.Role, error) {
	return stub.roles, nil
}

func TestGetWorkspaceProjectsCanonicalAgentsOutsideTopologyCache(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha"}); err != nil {
		t.Fatal(err)
	}
	metadata, err := agentsmodule.WithRuntimeMetadata(nil, agentsmodule.RuntimeMetadata{
		RoleKind: "interactive", Backend: "codex", Repos: []string{"loomcli"},
		RepoGroups: []string{"core"}, CrossRepo: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := &workspaceAgentDirectoryStub{
		agents: []*agentsmodule.Agent{{
			WorkspaceKey: "ALPHA", AgentID: "reviewer", Name: "reviewer",
			Behavior: agentsmodule.BehaviorReference{RoleName: "review"}, Metadata: metadata,
		}},
		roles: []*agentsmodule.Role{{WorkspaceKey: "ALPHA", Name: "review", Kind: "interactive", Backend: "claude"}},
	}
	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st, AgentDirectory: directory})

	first, err := svc.GetWorkspace(ctx, "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Agents) != 1 {
		t.Fatalf("workspace agents = %#v", first.Agents)
	}
	got := first.Agents[0]
	if got.Name != "reviewer" || got.Kind != "interactive" || got.RoleName != "review" ||
		got.Backend != "codex" || len(got.Repos) != 1 || got.Repos[0] != "loomcli" ||
		len(got.RepoGroups) != 1 || got.RepoGroups[0] != "core" || !got.CrossRepo {
		t.Fatalf("workspace agent = %#v", got)
	}

	directory.agents = nil
	second, err := svc.GetWorkspace(ctx, "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Agents) != 0 || directory.listCalls != 2 {
		t.Fatalf("second workspace agents = %#v, list calls = %d", second.Agents, directory.listCalls)
	}
}

func TestGetWorkspaceRejectsCanonicalAgentWithMissingRole(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha"}); err != nil {
		t.Fatal(err)
	}
	directory := &workspaceAgentDirectoryStub{agents: []*agentsmodule.Agent{{
		WorkspaceKey: "ALPHA", AgentID: "orphan", Behavior: agentsmodule.BehaviorReference{RoleName: "missing"},
	}}}
	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st, AgentDirectory: directory})
	if _, err := svc.GetWorkspace(ctx, "ALPHA"); err == nil {
		t.Fatal("GetWorkspace succeeded with an orphan canonical Agent")
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

func TestDeleteWorkspaceUsesOwnerCommandThenLocalCleanup(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatal(err)
	}
	legacyCalls := 0
	cleanupKey := ""
	capability := &workspaceCapabilityStub{deleteFn: func(ctx context.Context, command workspacemodule.DeleteCommand) (*workspacemodule.Reference, error) {
		if command.Reference != "Alpha Project" {
			t.Fatalf("delete reference = %q", command.Reference)
		}
		if err := st.Workspaces().Delete(ctx, "ALPHA"); err != nil {
			return nil, err
		}
		return &workspacemodule.Reference{Key: "ALPHA", Name: "Alpha Project"}, nil
	}}
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		Store:     st,
		Workspace: capability,
		DeleteFn: func(string) error {
			legacyCalls++
			return nil
		},
		DeleteCleanupFn: func(key string) error {
			cleanupKey = key
			return nil
		},
	})

	if _, err := svc.DeleteWorkspace(ctx, "Alpha Project"); err != nil {
		t.Fatal(err)
	}
	if legacyCalls != 0 || cleanupKey != "ALPHA" {
		t.Fatalf("legacy calls=%d cleanup key=%q", legacyCalls, cleanupKey)
	}
	if _, err := st.Workspaces().Get(ctx, "ALPHA"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("workspace still exists: %v", err)
	}
}

func TestDeleteWorkspaceDoesNotReportFailureAfterDurableDelete(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha"}); err != nil {
		t.Fatal(err)
	}
	capability := &workspaceCapabilityStub{deleteFn: func(ctx context.Context, _ workspacemodule.DeleteCommand) (*workspacemodule.Reference, error) {
		if err := st.Workspaces().Delete(ctx, "ALPHA"); err != nil {
			return nil, err
		}
		return &workspacemodule.Reference{Key: "ALPHA", Name: "Alpha"}, nil
	}}
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		Store: st, Workspace: capability,
		DeleteCleanupFn: func(string) error { return errors.New("disk cleanup failed") },
	})
	if _, err := svc.DeleteWorkspace(ctx, "ALPHA"); err != nil {
		t.Fatalf("durable delete was reported as failed: %v", err)
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

func TestStartAsyncAddReposNormalizesAndSchedulesWithoutRunningInline(t *testing.T) {
	jobStore := &recordingWorkspaceJobStore{}
	addCalled := false
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		AddReposFn: func(_ context.Context, req WorkspaceAddReposRequest) (WorkspaceCreateResult, error) {
			addCalled = true
			return WorkspaceCreateResult{WorkspaceID: req.WorkspaceID}, nil
		},
		JobStore: jobStore,
	})

	jobID, err := svc.StartAsyncAddRepos(context.Background(), WorkspaceAddReposRequest{
		WorkspaceID: "ALPHA",
		Repos:       []string{" https://github.com/acme/slow.git "},
	})
	if err != nil {
		t.Fatalf("StartAsyncAddRepos returned error: %v", err)
	}
	if jobID != "add-repos-job" {
		t.Fatalf("job ID = %q, want add-repos-job", jobID)
	}
	if addCalled {
		t.Fatal("add function ran before the async job store started it")
	}
	if len(jobStore.addReq.Repos) != 0 {
		t.Fatalf("repos = %#v, want normalized empty local list", jobStore.addReq.Repos)
	}
	if len(jobStore.addReq.CloneURLs) != 1 || jobStore.addReq.CloneURLs[0] != "https://github.com/acme/slow.git" {
		t.Fatalf("clone URLs = %#v", jobStore.addReq.CloneURLs)
	}
	if jobStore.addFn == nil {
		t.Fatal("expected async add function to be scheduled")
	}
}

func TestStartAsyncCreatePreparesAdmissionInlineAndUsesExactJobID(t *testing.T) {
	prepareEntered := make(chan WorkspaceCreateRequest, 1)
	releasePrepare := make(chan struct{})
	coordinator := &testWorkspaceAdmissionCoordinator{
		prepareCreate: func(_ context.Context, req WorkspaceCreateRequest) (string, error) {
			prepareEntered <- req
			<-releasePrepare
			return "durable-create-admission", nil
		},
	}
	jobStore := &recordingWorkspaceJobStore{
		preparedCreateStarted: make(chan string, 1),
	}
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		CreateFn: func(context.Context, WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
			return WorkspaceCreateResult{WorkspaceID: "CLONE-WS"}, nil
		},
		JobStore:             jobStore,
		AdmissionCoordinator: coordinator,
	})

	type startResult struct {
		id  string
		err error
	}
	result := make(chan startResult, 1)
	go func() {
		id, err := svc.StartAsyncCreate(context.Background(), WorkspaceCreateRequest{
			Name:      "clone_ws",
			Type:      "clone",
			CloneURLs: []string{"https://github.com/acme/clone.git"},
		})
		result <- startResult{id: id, err: err}
	}()

	select {
	case req := <-prepareEntered:
		if req.Name != "clone_ws" {
			t.Fatalf("prepare request name = %q, want clone_ws", req.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("durable prepare was not called")
	}
	select {
	case got := <-result:
		t.Fatalf("StartAsyncCreate returned before durable prepare completed: %+v", got)
	default:
	}
	select {
	case id := <-jobStore.preparedCreateStarted:
		t.Fatalf("runner was scheduled before durable prepare completed with ID %q", id)
	default:
	}

	close(releasePrepare)
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("StartAsyncCreate returned error: %v", got.err)
		}
		if got.id != "durable-create-admission" {
			t.Fatalf("job ID = %q, want exact durable admission ID", got.id)
		}
	case <-time.After(time.Second):
		t.Fatal("StartAsyncCreate did not return after durable prepare")
	}
	select {
	case id := <-jobStore.preparedCreateStarted:
		if id != "durable-create-admission" {
			t.Fatalf("scheduled job ID = %q, want exact durable admission ID", id)
		}
	case <-time.After(time.Second):
		t.Fatal("prepared create runner was not scheduled")
	}
}

func TestStartAsyncAddReposPreparesNormalizedAdmissionAndUsesExactJobID(t *testing.T) {
	var prepared WorkspaceAddReposRequest
	coordinator := &testWorkspaceAdmissionCoordinator{
		prepareAddRepos: func(_ context.Context, req WorkspaceAddReposRequest) (string, error) {
			prepared = req
			return "durable-add-repos-admission", nil
		},
	}
	jobStore := &recordingWorkspaceJobStore{}
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		AddReposFn: func(context.Context, WorkspaceAddReposRequest) (WorkspaceCreateResult, error) {
			return WorkspaceCreateResult{WorkspaceID: "ALPHA"}, nil
		},
		JobStore:             jobStore,
		AdmissionCoordinator: coordinator,
	})

	jobID, err := svc.StartAsyncAddRepos(context.Background(), WorkspaceAddReposRequest{
		WorkspaceID: "ALPHA",
		Repos:       []string{" https://github.com/acme/slow.git "},
	})
	if err != nil {
		t.Fatalf("StartAsyncAddRepos returned error: %v", err)
	}
	if jobID != "durable-add-repos-admission" {
		t.Fatalf("job ID = %q, want exact durable admission ID", jobID)
	}
	if jobStore.preparedAddReposID != jobID {
		t.Fatalf("scheduled job ID = %q, want %q", jobStore.preparedAddReposID, jobID)
	}
	if len(prepared.Repos) != 0 {
		t.Fatalf("prepared repos = %#v, want normalized empty local list", prepared.Repos)
	}
	if len(prepared.CloneURLs) != 1 || prepared.CloneURLs[0] != "https://github.com/acme/slow.git" {
		t.Fatalf("prepared clone URLs = %#v", prepared.CloneURLs)
	}
}

func TestStartAsyncCreateClassifiesAdmissionConflict(t *testing.T) {
	coordinator := &testWorkspaceAdmissionCoordinator{
		prepareCreate: func(context.Context, WorkspaceCreateRequest) (string, error) {
			return "", workspaceerrors.New(
				workspaceerrors.AlreadyExists,
				"workspace already exists",
				errors.New("repository admission conflict"),
			)
		},
	}
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		CreateFn: func(context.Context, WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
			return WorkspaceCreateResult{}, nil
		},
		JobStore:             &recordingWorkspaceJobStore{},
		AdmissionCoordinator: coordinator,
	})

	_, err := svc.StartAsyncCreate(context.Background(), WorkspaceCreateRequest{
		Name: "clone_ws", Type: "clone",
		CloneURLs: []string{"https://github.com/acme/clone.git"},
	})
	var serviceErr *ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != KindConflict {
		t.Fatalf("error = %v, want conflict", err)
	}
}

func TestStartAsyncAddReposClassifiesAdmissionConflict(t *testing.T) {
	coordinator := &testWorkspaceAdmissionCoordinator{
		prepareAddRepos: func(context.Context, WorkspaceAddReposRequest) (string, error) {
			return "", workspaceerrors.New(
				workspaceerrors.AlreadyExists,
				"repository already exists in workspace",
				errors.New("repository admission conflict"),
			)
		},
	}
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		AddReposFn: func(context.Context, WorkspaceAddReposRequest) (WorkspaceCreateResult, error) {
			return WorkspaceCreateResult{}, nil
		},
		JobStore:             &recordingWorkspaceJobStore{},
		AdmissionCoordinator: coordinator,
	})

	_, err := svc.StartAsyncAddRepos(context.Background(), WorkspaceAddReposRequest{
		WorkspaceID: "ALPHA",
		CloneURLs:   []string{"https://github.com/acme/clone.git"},
	})
	var serviceErr *ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != KindConflict {
		t.Fatalf("error = %v, want conflict", err)
	}
}

func TestStartAsyncAddReposRejectsLocalOnlyRequest(t *testing.T) {
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		AddReposFn: func(context.Context, WorkspaceAddReposRequest) (WorkspaceCreateResult, error) {
			return WorkspaceCreateResult{}, nil
		},
		JobStore: &recordingWorkspaceJobStore{},
	})

	_, err := svc.StartAsyncAddRepos(context.Background(), WorkspaceAddReposRequest{
		WorkspaceID: "ALPHA",
		Repos:       []string{"/workspace/local"},
	})
	var serviceErr *ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != KindValidation {
		t.Fatalf("error = %v, want validation error", err)
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

func TestGetWorkspace_StoreBackedCanonicalizesDisplayNameRoute(t *testing.T) {
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{
		Key: "LOOM-P61", Name: "Loom-P61",
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})

	workspace, err := svc.GetWorkspace(context.Background(), "Loom-P61")
	if err != nil {
		t.Fatalf("GetWorkspace display route: %v", err)
	}
	if workspace.ID != "LOOM-P61" {
		t.Fatalf("workspace ID = %q, want LOOM-P61", workspace.ID)
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
}

func TestGetWorkspaceBackend_ReadsLocalNodeConfig(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := localnodeconfig.SetRuntimeProvider("ALPHA", "codex"); err != nil {
		t.Fatalf("set runtime provider: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})
	cfg, err := svc.GetWorkspaceBackend(ctx, "ALPHA")
	if err != nil {
		t.Fatalf("GetWorkspaceBackend: %v", err)
	}
	if cfg.Backend != "codex" {
		t.Fatalf("Backend = %q, want codex", cfg.Backend)
	}
	if cfg.Source != "local_node" {
		t.Fatalf("Source = %q, want local_node", cfg.Source)
	}
}

func TestGetWorkspaceBackend_StoreBackedDefaultsToCodex(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
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

func TestPatchWorkspaceBackend_WritesLocalNodeConfig(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
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
	provider, err := localnodeconfig.RuntimeProvider("ALPHA")
	if err != nil {
		t.Fatalf("get runtime provider: %v", err)
	}
	if provider != "codex" {
		t.Fatalf("runtime provider = %q, want codex", provider)
	}
}

func TestPatchWorkspaceDesignFormat_StoreBackedUpdatesWorkspace(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha Project", DesignFormat: "markdown"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	svc := NewWorkspaceService(WorkspaceServiceConfig{
		Store: st,
		Workspace: &workspaceCapabilityStub{setDesignFormatFn: func(ctx context.Context, command workspacemodule.SetDesignFormatCommand) (*workspacemodule.Reference, error) {
			updated, err := st.Workspaces().Update(ctx, command.Reference, store.WorkspaceUpdate{DesignFormat: &command.Format})
			return updated, err
		}},
	})
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
	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: memstore.New(), Workspace: &workspaceCapabilityStub{}})
	if _, err := svc.PatchWorkspaceDesignFormat(context.Background(), "ALPHA", "svg"); err == nil {
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

func TestGetWorkspaceJob_AdmissionLookupFallbackAfterInMemoryMiss(t *testing.T) {
	want := &WorkspaceJob{
		ID:          "durable-add-repos-admission",
		Status:      JobStatusRunning,
		Progress:    "cloning repository...",
		WorkspaceID: "ALPHA",
	}
	coordinator := &testWorkspaceAdmissionCoordinator{
		lookupJob: func(_ context.Context, jobID string) (*WorkspaceJob, bool, error) {
			if jobID != want.ID {
				t.Fatalf("lookup job ID = %q, want %q", jobID, want.ID)
			}
			return want, true, nil
		},
	}
	svc := NewWorkspaceService(WorkspaceServiceConfig{
		JobStore:             &recordingWorkspaceJobStore{},
		AdmissionCoordinator: coordinator,
	})

	got, err := svc.GetWorkspaceJob(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("GetWorkspaceJob returned error: %v", err)
	}
	if got != want {
		t.Fatalf("job = %+v, want coordinator snapshot %+v", got, want)
	}
}

type recordingWorkspaceJobStore struct {
	addReq                WorkspaceAddReposRequest
	addFn                 WorkspaceAddReposFn
	preparedCreateID      string
	preparedCreateStarted chan string
	preparedAddReposID    string
}

func (*recordingWorkspaceJobStore) Start(WorkspaceCreateRequest, WorkspaceCreateFn) string {
	return "create-job"
}

func (s *recordingWorkspaceJobStore) StartPrepared(id string, _ WorkspaceCreateRequest, _ WorkspaceCreateFn) string {
	s.preparedCreateID = id
	if s.preparedCreateStarted != nil {
		s.preparedCreateStarted <- id
	}
	return id
}

func (s *recordingWorkspaceJobStore) StartAddRepos(req WorkspaceAddReposRequest, fn WorkspaceAddReposFn) string {
	s.addReq = req
	s.addFn = fn
	return "add-repos-job"
}

func (s *recordingWorkspaceJobStore) StartPreparedAddRepos(
	id string,
	req WorkspaceAddReposRequest,
	fn WorkspaceAddReposFn,
) string {
	s.preparedAddReposID = id
	s.addReq = req
	s.addFn = fn
	return id
}

func (*recordingWorkspaceJobStore) Get(string) *WorkspaceJob {
	return nil
}

type testWorkspaceAdmissionCoordinator struct {
	prepareCreate   func(context.Context, WorkspaceCreateRequest) (string, error)
	prepareAddRepos func(context.Context, WorkspaceAddReposRequest) (string, error)
	lookupJob       func(context.Context, string) (*WorkspaceJob, bool, error)
}

func (c *testWorkspaceAdmissionCoordinator) PrepareCreate(
	ctx context.Context,
	req WorkspaceCreateRequest,
) (string, error) {
	if c.prepareCreate == nil {
		return "", errors.New("unexpected PrepareCreate call")
	}
	return c.prepareCreate(ctx, req)
}

func (c *testWorkspaceAdmissionCoordinator) PrepareAddRepos(
	ctx context.Context,
	req WorkspaceAddReposRequest,
) (string, error) {
	if c.prepareAddRepos == nil {
		return "", errors.New("unexpected PrepareAddRepos call")
	}
	return c.prepareAddRepos(ctx, req)
}

func (c *testWorkspaceAdmissionCoordinator) LookupJob(
	ctx context.Context,
	jobID string,
) (*WorkspaceJob, bool, error) {
	if c.lookupJob == nil {
		return nil, false, nil
	}
	return c.lookupJob(ctx, jobID)
}

type workspaceCountingStore struct {
	store.Store
	workspaces *workspaceCountingWorkspaceStore
	repos      *workspaceCountingRepoStore
}

func newWorkspaceCountingStore(base store.Store) *workspaceCountingStore {
	return &workspaceCountingStore{
		Store:      base,
		workspaces: &workspaceCountingWorkspaceStore{WorkspaceStore: base.Workspaces()},
		repos:      &workspaceCountingRepoStore{RepoStore: base.Repos(), listByWorkspace: make(map[string]int)},
	}
}

func (s *workspaceCountingStore) Workspaces() store.WorkspaceStore { return s.workspaces }
func (s *workspaceCountingStore) Repos() store.RepoStore           { return s.repos }

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
