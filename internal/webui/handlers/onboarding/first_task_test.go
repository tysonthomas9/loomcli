package onboarding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestHandleRunFirstTaskCreatesClaimableTaskAndQueuesAgentStart(t *testing.T) {
	issueSvc := &stubIssueService{
		createFunc: func(_ context.Context, params workitems.CreateCommand) (*workitems.CreatedIssue, error) {
			if params.Title != "Explore Hello-World onboarding" {
				t.Fatalf("Title = %q", params.Title)
			}
			if params.Assignee != "" || params.Status != "open" {
				t.Fatalf("create assignment = %q/%q", params.Assignee, params.Status)
			}
			if params.SourceRepo != "Hello-World" {
				t.Fatalf("SourceRepo = %q", params.SourceRepo)
			}
			return createdWorkItem("task-1", "Explore Hello-World onboarding"), nil
		},
	}
	agentSvc := &stubAgentService{
		agent: &agentsmodule.Agent{WorkspaceKey: "HELLO-WORLD", AgentID: "planner"},
		lifecycleFunc: func(_ context.Context, _ authority.OperatorAuthority, command agentsmodule.ApplyLifecycleCommand) (*agentsmodule.LifecycleResult, error) {
			if command.WorkspaceKey != "HELLO-WORLD" || command.AgentID != "planner" {
				t.Fatalf("lifecycle target = %s/%s", command.WorkspaceKey, command.AgentID)
			}
			if command.Action != agentsmodule.LifecycleEnable || command.IdempotencyKey != "onboarding-first-task:task-1" {
				t.Fatalf("lifecycle input = %#v", command)
			}
			return &agentsmodule.LifecycleResult{Agent: &agentsmodule.Agent{AgentID: command.AgentID}}, nil
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

	HandleRunFirstTask(issueSvc, agentSvc, testOnboardingAuthorityResolver{}).ServeHTTP(rec, req)

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

func TestHandleRunFirstTaskKeepsTaskClaimableForExecution(t *testing.T) {
	issueSvc := &stubIssueService{
		createFunc: func(_ context.Context, params workitems.CreateCommand) (*workitems.CreatedIssue, error) {
			if params.Status != "open" || params.Assignee != "" {
				t.Fatalf("created task must remain claimable, got status=%q assignee=%q", params.Status, params.Assignee)
			}
			return createdWorkItem("task-1", "Explore Hello-World onboarding"), nil
		},
	}
	agentSvc := &stubAgentService{
		agent: &agentsmodule.Agent{WorkspaceKey: "HELLO-WORLD", AgentID: "planner"},
		lifecycleFunc: func(context.Context, authority.OperatorAuthority, agentsmodule.ApplyLifecycleCommand) (*agentsmodule.LifecycleResult, error) {
			return &agentsmodule.LifecycleResult{Agent: &agentsmodule.Agent{AgentID: "planner"}}, nil
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/HELLO-WORLD/onboarding/first-task", strings.NewReader(`{
		"agent_name":"planner",
		"title":"Explore Hello-World onboarding"
	}`))
	req.SetPathValue("ws", "HELLO-WORLD")

	HandleRunFirstTask(issueSvc, agentSvc, testOnboardingAuthorityResolver{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRunFirstTaskDeletesCreatedIssueWhenStartFails(t *testing.T) {
	var deletedIssueID string
	issueSvc := &stubIssueService{
		createFunc: func(context.Context, workitems.CreateCommand) (*workitems.CreatedIssue, error) {
			return createdWorkItem("task-1", "Explore Hello-World onboarding"), nil
		},
		deleteFunc: func(_ context.Context, command workitems.DeleteCommand) (workitems.DeleteResult, error) {
			deletedIssueID = command.IssueID
			return workitems.DeleteResult{DeletedCount: 1, DeletedIDs: []string{command.IssueID}}, nil
		},
	}
	agentSvc := &stubAgentService{
		agent: &agentsmodule.Agent{WorkspaceKey: "HELLO-WORLD", AgentID: "planner"},
		lifecycleFunc: func(context.Context, authority.OperatorAuthority, agentsmodule.ApplyLifecycleCommand) (*agentsmodule.LifecycleResult, error) {
			return nil, agentsmodule.ErrUnavailable
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/HELLO-WORLD/onboarding/first-task", strings.NewReader(`{
		"agent_name":"planner",
		"title":"Explore Hello-World onboarding"
	}`))
	req.SetPathValue("ws", "HELLO-WORLD")

	HandleRunFirstTask(issueSvc, agentSvc, testOnboardingAuthorityResolver{}).ServeHTTP(rec, req)

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
		createFunc: func(context.Context, workitems.CreateCommand) (*workitems.CreatedIssue, error) {
			return createdWorkItem("task-1", "Explore Hello-World onboarding"), nil
		},
		deleteFunc: func(ctx context.Context, command workitems.DeleteCommand) (workitems.DeleteResult, error) {
			deleteCalled = true
			deleteCtxErr = ctx.Err()
			deletedID = command.IssueID
			return workitems.DeleteResult{DeletedCount: 1, DeletedIDs: []string{command.IssueID}}, nil
		},
	}
	agentSvc := &stubAgentService{
		agent: &agentsmodule.Agent{WorkspaceKey: "HELLO-WORLD", AgentID: "planner"},
		lifecycleFunc: func(context.Context, authority.OperatorAuthority, agentsmodule.ApplyLifecycleCommand) (*agentsmodule.LifecycleResult, error) {
			return nil, agentsmodule.ErrUnavailable
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

	HandleRunFirstTask(issueSvc, agentSvc, testOnboardingAuthorityResolver{}).ServeHTTP(rec, req)

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
		createFunc: func(context.Context, workitems.CreateCommand) (*workitems.CreatedIssue, error) {
			t.Fatal("CreateIssue should not be called")
			return nil, nil
		},
	}
	agentSvc := &stubAgentService{
		agent: &agentsmodule.Agent{WorkspaceKey: "HELLO-WORLD", AgentID: "builder"},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/HELLO-WORLD/onboarding/first-task", strings.NewReader(`{
		"agent_name":"planner",
		"title":"Explore Hello-World onboarding"
	}`))
	req.SetPathValue("ws", "HELLO-WORLD")

	HandleRunFirstTask(issueSvc, agentSvc, testOnboardingAuthorityResolver{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

type stubIssueService struct {
	createFunc func(context.Context, workitems.CreateCommand) (*workitems.CreatedIssue, error)
	deleteFunc func(context.Context, workitems.DeleteCommand) (workitems.DeleteResult, error)
}

func createdWorkItem(id, title string) *workitems.CreatedIssue {
	return &workitems.CreatedIssue{Summary: &workitems.IssueSummary{ID: id, Title: title, Status: "open", IssueType: "task"}}
}

func (s *stubIssueService) Create(ctx context.Context, command workitems.CreateCommand) (*workitems.CreatedIssue, error) {
	if s.createFunc != nil {
		return s.createFunc(ctx, command)
	}
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) List(context.Context, workitems.ListQuery) (*workitems.ListResult, error) {
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) Ready(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) Search(context.Context, workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) Get(context.Context, workitems.GetQuery) (*workitems.IssueDetail, error) {
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) Patch(context.Context, workitems.PatchCommand) (*workitems.IssueDetail, error) {
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) Close(context.Context, workitems.CloseCommand) (*workitems.CloseResult, error) {
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) Claim(context.Context, workitems.ClaimCommand) (*workitems.IssueDetail, error) {
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) Reopen(context.Context, workitems.ReopenCommand) error {
	return workitems.ErrNotImplemented
}
func (s *stubIssueService) AssignRepository(context.Context, workitems.AssignRepositoryCommand) (*workitems.IssueSummary, error) {
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) Delete(ctx context.Context, command workitems.DeleteCommand) (workitems.DeleteResult, error) {
	if s.deleteFunc != nil {
		return s.deleteFunc(ctx, command)
	}
	return workitems.DeleteResult{}, workitems.ErrNotImplemented
}
func (s *stubIssueService) ListEvents(context.Context, workitems.ListEventsQuery) ([]*workitems.Event, error) {
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) AddComment(context.Context, workitems.AddCommentCommand) (*workitems.Comment, error) {
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) ListComments(context.Context, workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	return nil, workitems.ErrNotImplemented
}
func (s *stubIssueService) AddDependency(context.Context, workitems.AddDependencyCommand) error {
	return workitems.ErrNotImplemented
}
func (s *stubIssueService) RemoveDependency(context.Context, workitems.RemoveDependencyCommand) error {
	return workitems.ErrNotImplemented
}
func (s *stubIssueService) ListDependencies(context.Context, workitems.ListDependenciesQuery) ([]workitems.Dependency, error) {
	return nil, workitems.ErrNotImplemented
}

type stubAgentService struct {
	agent         *agentsmodule.Agent
	lifecycleFunc func(context.Context, authority.OperatorAuthority, agentsmodule.ApplyLifecycleCommand) (*agentsmodule.LifecycleResult, error)
}

func (s *stubAgentService) GetAgent(_ context.Context, workspace, agentID string) (*agentsmodule.Agent, error) {
	if s.agent == nil || s.agent.WorkspaceKey != workspace || s.agent.AgentID != agentID {
		return nil, agentsmodule.ErrNotFound
	}
	return s.agent, nil
}

func (s *stubAgentService) ApplyLifecycle(ctx context.Context, auth authority.OperatorAuthority, command agentsmodule.ApplyLifecycleCommand) (*agentsmodule.LifecycleResult, error) {
	if s.lifecycleFunc != nil {
		return s.lifecycleFunc(ctx, auth, command)
	}
	return &agentsmodule.LifecycleResult{Agent: s.agent}, nil
}

type testOnboardingAuthorityResolver struct{}

func (testOnboardingAuthorityResolver) ResolveOperatorAuthority(_ *http.Request, _ string, _ authority.Action) (authority.OperatorAuthority, error) {
	return authority.OperatorAuthority{}, nil
}
