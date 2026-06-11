package onboarding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestHandleRunFirstTaskCreatesClaimableTaskAndQueuesAgentStart(t *testing.T) {
	issueSvc := &stubIssueService{
		createFunc: func(_ context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			if params.Title != "Explore Hello-World onboarding" {
				t.Fatalf("Title = %q", params.Title)
			}
			if params.Assignee != "" || params.Status != "open" {
				t.Fatalf("create assignment = %q/%q", params.Assignee, params.Status)
			}
			if params.SourceRepo != "Hello-World" {
				t.Fatalf("SourceRepo = %q", params.SourceRepo)
			}
			return json.RawMessage(`{"id":"task-1","title":"Explore Hello-World onboarding"}`), nil
		},
		patchFunc: func(_ context.Context, params service.PatchIssueParams) error {
			t.Fatalf("PatchIssue should not pre-claim the first task; got %#v", params)
			return nil
		},
	}
	agentSvc := &stubAgentService{
		agents: []*domain.Agent{{Name: "planner", RoleName: "plan"}},
		lifecycleFunc: func(_ context.Context, wsKey, name string, in service.AgentLifecycleInput) (*domain.Agent, error) {
			if wsKey != "HELLO-WORLD" || name != "planner" {
				t.Fatalf("lifecycle target = %s/%s", wsKey, name)
			}
			if in.CommandType != "start" || in.Payload["task_id"] != "task-1" {
				t.Fatalf("lifecycle input = %#v", in)
			}
			return &domain.Agent{Name: name}, nil
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/HELLO-WORLD/onboarding/first-task", strings.NewReader(`{
		"agent_name":"planner",
		"title":"Explore Hello-World onboarding",
		"description":"Try the sample repo",
		"issue_type":"task",
		"priority":2,
		"source_repo":"Hello-World"
	}`))
	req.SetPathValue("ws", "HELLO-WORLD")

	HandleRunFirstTask(issueSvc, agentSvc).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body runFirstTaskResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Success || body.Started || !body.Queued || body.AgentName != "planner" {
		t.Fatalf("response = %#v", body)
	}
}

func TestHandleRunFirstTaskKeepsTaskClaimableForDaemon(t *testing.T) {
	issueSvc := &stubIssueService{
		createFunc: func(_ context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			if params.Status != "open" || params.Assignee != "" {
				t.Fatalf("created task must remain claimable, got status=%q assignee=%q", params.Status, params.Assignee)
			}
			return json.RawMessage(`{"id":"task-1"}`), nil
		},
		patchFunc: func(_ context.Context, params service.PatchIssueParams) error {
			t.Fatalf("PatchIssue should not run before daemon claim; got %#v", params)
			return nil
		},
	}
	agentSvc := &stubAgentService{
		agents: []*domain.Agent{{Name: "planner", RoleName: "plan"}},
		lifecycleFunc: func(context.Context, string, string, service.AgentLifecycleInput) (*domain.Agent, error) {
			return &domain.Agent{Name: "planner"}, nil
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/HELLO-WORLD/onboarding/first-task", strings.NewReader(`{
		"agent_name":"planner",
		"title":"Explore Hello-World onboarding"
	}`))
	req.SetPathValue("ws", "HELLO-WORLD")

	HandleRunFirstTask(issueSvc, agentSvc).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRunFirstTaskDeletesCreatedIssueWhenStartFails(t *testing.T) {
	var deletedIssueID string
	issueSvc := &stubIssueService{
		createFunc: func(context.Context, service.CreateIssueParams) (json.RawMessage, error) {
			return json.RawMessage(`{"id":"task-1"}`), nil
		},
		patchFunc: func(_ context.Context, params service.PatchIssueParams) error {
			t.Fatalf("PatchIssue should not run when lifecycle fails; got %#v", params)
			return nil
		},
		deleteFunc: func(_ context.Context, issueID string) (json.RawMessage, error) {
			deletedIssueID = issueID
			return json.RawMessage(`{"deleted_count":1}`), nil
		},
	}
	agentSvc := &stubAgentService{
		agents: []*domain.Agent{{Name: "planner", RoleName: "plan"}},
		lifecycleFunc: func(context.Context, string, string, service.AgentLifecycleInput) (*domain.Agent, error) {
			return nil, service.ErrUnavailable("daemon unavailable")
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/HELLO-WORLD/onboarding/first-task", strings.NewReader(`{
		"agent_name":"planner",
		"title":"Explore Hello-World onboarding"
	}`))
	req.SetPathValue("ws", "HELLO-WORLD")

	HandleRunFirstTask(issueSvc, agentSvc).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if deletedIssueID != "task-1" {
		t.Fatalf("deleted issue = %q, want task-1", deletedIssueID)
	}
}

// Regression: client disconnect must not orphan a just-created first task.
// deleteCreatedFirstTask uses a detached cleanup context with a bounded
// timeout, so a canceled request context still lets the compensating delete
// run to completion.
func TestHandleRunFirstTaskCleanupRunsWithLiveContextWhenClientDisconnects(t *testing.T) {
	var deleteCtxErr error
	var deleteCalled bool
	var deletedID string
	issueSvc := &stubIssueService{
		createFunc: func(context.Context, service.CreateIssueParams) (json.RawMessage, error) {
			return json.RawMessage(`{"id":"task-1"}`), nil
		},
		deleteFunc: func(ctx context.Context, id string) (json.RawMessage, error) {
			deleteCalled = true
			deleteCtxErr = ctx.Err()
			deletedID = id
			return json.RawMessage(`{"deleted_count":1}`), nil
		},
	}
	agentSvc := &stubAgentService{
		agents: []*domain.Agent{{Name: "planner", RoleName: "plan"}},
		lifecycleFunc: func(context.Context, string, string, service.AgentLifecycleInput) (*domain.Agent, error) {
			return nil, service.ErrUnavailable("daemon unavailable")
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/HELLO-WORLD/onboarding/first-task", strings.NewReader(`{
		"agent_name":"planner",
		"title":"Explore Hello-World onboarding"
	}`))
	req.SetPathValue("ws", "HELLO-WORLD")
	// Simulate client disconnect: pre-cancel the request context.
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	HandleRunFirstTask(issueSvc, agentSvc).ServeHTTP(rec, req)

	if !deleteCalled {
		t.Fatal("cleanup delete was not called")
	}
	if deletedID != "task-1" {
		t.Fatalf("deleted issue = %q, want task-1", deletedID)
	}
	if deleteCtxErr != nil {
		t.Fatalf("cleanup observed a canceled context: %v — orphans task on client disconnect", deleteCtxErr)
	}
}

func TestHandleRunFirstTaskRejectsUnknownAgent(t *testing.T) {
	issueSvc := &stubIssueService{
		createFunc: func(context.Context, service.CreateIssueParams) (json.RawMessage, error) {
			t.Fatal("CreateIssue should not be called")
			return nil, nil
		},
	}
	agentSvc := &stubAgentService{
		agents: []*domain.Agent{{Name: "builder", RoleName: "task"}},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/HELLO-WORLD/onboarding/first-task", strings.NewReader(`{
		"agent_name":"planner",
		"title":"Explore Hello-World onboarding"
	}`))
	req.SetPathValue("ws", "HELLO-WORLD")

	HandleRunFirstTask(issueSvc, agentSvc).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

type stubIssueService struct {
	createFunc func(context.Context, service.CreateIssueParams) (json.RawMessage, error)
	patchFunc  func(context.Context, service.PatchIssueParams) error
	deleteFunc func(context.Context, string) (json.RawMessage, error)
}

func (s *stubIssueService) GetIssue(context.Context, string) (json.RawMessage, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) ListIssues(context.Context, service.ListIssuesParams) (*service.ListIssuesResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) CreateIssue(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
	if s.createFunc != nil {
		return s.createFunc(ctx, params)
	}
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) PatchIssue(ctx context.Context, params service.PatchIssueParams) error {
	if s.patchFunc != nil {
		return s.patchFunc(ctx, params)
	}
	return nil
}
func (s *stubIssueService) CloseIssue(context.Context, service.CloseIssueParams) (json.RawMessage, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) ReopenIssue(context.Context, service.ReopenIssueParams) error {
	return service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) ClaimIssue(context.Context, service.ClaimIssueParams) (json.RawMessage, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) DeleteIssue(ctx context.Context, issueID string) (json.RawMessage, error) {
	if s.deleteFunc != nil {
		return s.deleteFunc(ctx, issueID)
	}
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) AddComment(context.Context, service.AddCommentParams) (*types.Comment, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) ListComments(context.Context, string) ([]*types.Comment, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) AddDependency(context.Context, service.AddDependencyParams) error {
	return service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) RemoveDependency(context.Context, service.RemoveDependencyParams) error {
	return service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) ListDependencies(context.Context, string) (json.RawMessage, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) ListEvents(context.Context, service.EventListParams) ([]*types.Event, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) MoveIssue(context.Context, service.MoveIssueParams) (*service.MoveIssueResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubIssueService) SearchIssues(context.Context, service.SearchIssuesParams) (json.RawMessage, error) {
	return nil, service.ErrNotImplemented("not implemented")
}

type stubAgentService struct {
	agents        []*domain.Agent
	lifecycleFunc func(context.Context, string, string, service.AgentLifecycleInput) (*domain.Agent, error)
}

func (s *stubAgentService) GetTerminalInfo(context.Context, string, string) (*service.AgentTerminalInfoResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) GenerateTerminalToken(context.Context, string, string, string) (string, error) {
	return "", service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) GetLog(context.Context, string, string, int, int64) (*service.AgentLogResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) GetDiffStat(context.Context, string, string) (*service.AgentDiffStatResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) GitPush(context.Context, string, string, string) (*ops.GitPushResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) GitPushAll(context.Context, string) (*service.GitPushAllResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) GitPull(context.Context, string, string, string) (*ops.GitPullResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) GitSync(context.Context, string, string) (*service.GitSyncResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) CreatePR(context.Context, string, string, string) (*ops.GitPRResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}

func (s *stubAgentService) ListPullRequests(context.Context, string, string) (*ops.GitPullRequestList, error) {
	return &ops.GitPullRequestList{PullRequests: []ops.GitPullRequest{}}, nil
}

func (s *stubAgentService) GitReset(context.Context, string, string, string, bool, bool) (*ops.GitResetResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) GitStatus(context.Context, string, string) (*ops.GitStatusResult, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) SetTargetBranch(context.Context, string, string, string) error {
	return service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) ListAgents(context.Context, string) ([]*domain.Agent, error) {
	return s.agents, nil
}
func (s *stubAgentService) CreateAgent(context.Context, service.AgentCreateInput) (*domain.Agent, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) UpdateAgent(context.Context, string, string, service.AgentUpdateInput) (*domain.Agent, error) {
	return nil, service.ErrNotImplemented("not implemented")
}
func (s *stubAgentService) RequestAgentLifecycle(ctx context.Context, wsKey, name string, in service.AgentLifecycleInput) (*domain.Agent, error) {
	if s.lifecycleFunc != nil {
		return s.lifecycleFunc(ctx, wsKey, name, in)
	}
	return &domain.Agent{Name: name}, nil
}
func (s *stubAgentService) DeleteAgent(context.Context, string, string) error {
	return service.ErrNotImplemented("not implemented")
}
