package onboarding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
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
		createFunc: func(context.Context, service.CreateIssueParams) (json.RawMessage, error) {
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
