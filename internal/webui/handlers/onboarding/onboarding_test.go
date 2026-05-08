package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type stubDeps struct {
	hasAnyWorkspace func(ctx context.Context) (bool, error)
	getWorkspace    func(ctx context.Context, wsID string) (*ops.WorkspaceData, error)
	backendsHealth  func(ctx context.Context) ([]ops.BackendHealth, error)
	issueCount      func(ctx context.Context) (int, error)
}

func (s stubDeps) HasAnyWorkspace(ctx context.Context) (bool, error) {
	if s.hasAnyWorkspace != nil {
		return s.hasAnyWorkspace(ctx)
	}
	return false, nil
}

func (s stubDeps) GetWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	if s.getWorkspace != nil {
		return s.getWorkspace(ctx, wsID)
	}
	return nil, errors.New("not found")
}

func (s stubDeps) BackendsHealth(ctx context.Context) ([]ops.BackendHealth, error) {
	if s.backendsHealth != nil {
		return s.backendsHealth(ctx)
	}
	return nil, nil
}

func (s stubDeps) IssueCount(ctx context.Context) (int, error) {
	if s.issueCount != nil {
		return s.issueCount(ctx)
	}
	return 0, nil
}

func stepByID(t *testing.T, s Status, id StepID) Step {
	t.Helper()
	for _, step := range s.Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("step %s not found in status: %+v", id, s.Steps)
	return Step{}
}

func TestComputeStatus_NoWorkspace_NoneExist(t *testing.T) {
	deps := stubDeps{}
	got := ComputeStatus(context.Background(), deps, "")

	if want := 6; len(got.Steps) != want {
		t.Fatalf("want %d steps, got %d", want, len(got.Steps))
	}
	step1 := stepByID(t, got, StepWorkspaceRepo)
	if step1.Status != StatusActionable {
		t.Errorf("step 1 status = %s, want actionable", step1.Status)
	}
	for _, id := range []StepID{StepVerifyRepo, StepSetupBackend, StepCreateAgent, StepCreateIssue, StepRunAgent} {
		s := stepByID(t, got, id)
		if s.Status != StatusBlocked {
			t.Errorf("step %s = %s, want blocked when no workspace", id, s.Status)
		}
	}
	if got.AllComplete {
		t.Error("AllComplete must be false when no workspace exists")
	}
}

func TestComputeStatus_NoWorkspace_HasWorkspaces(t *testing.T) {
	deps := stubDeps{
		hasAnyWorkspace: func(ctx context.Context) (bool, error) { return true, nil },
	}
	got := ComputeStatus(context.Background(), deps, "")

	step1 := stepByID(t, got, StepWorkspaceRepo)
	if step1.Status != StatusComplete {
		t.Errorf("step 1 status = %s, want complete (has any workspace)", step1.Status)
	}
}

func TestComputeStatus_NoWorkspace_HasAnyError(t *testing.T) {
	deps := stubDeps{
		hasAnyWorkspace: func(ctx context.Context) (bool, error) { return false, errors.New("boom") },
	}
	got := ComputeStatus(context.Background(), deps, "")

	step1 := stepByID(t, got, StepWorkspaceRepo)
	if step1.Status != StatusError {
		t.Errorf("step 1 status = %s, want error", step1.Status)
	}
	if step1.Message == "" {
		t.Error("step 1 should carry a message on error")
	}
}

func TestComputeStatus_WorkspaceScoped_NotFound(t *testing.T) {
	deps := stubDeps{}
	got := ComputeStatus(context.Background(), deps, "ws-1")

	step1 := stepByID(t, got, StepWorkspaceRepo)
	if step1.Status != StatusError {
		t.Errorf("step 1 status = %s, want error when GetWorkspace returns error", step1.Status)
	}
	for _, id := range []StepID{StepVerifyRepo, StepSetupBackend, StepCreateAgent, StepCreateIssue, StepRunAgent} {
		s := stepByID(t, got, id)
		if s.Status != StatusBlocked {
			t.Errorf("step %s should be blocked downstream of error, got %s", id, s.Status)
		}
	}
}

func TestComputeStatus_WorkspaceScoped_NoRepos(t *testing.T) {
	deps := stubDeps{
		getWorkspace: func(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
			return &ops.WorkspaceData{ID: wsID, Repos: nil}, nil
		},
	}
	got := ComputeStatus(context.Background(), deps, "ws-1")

	if got.WorkspaceID != "ws-1" {
		t.Errorf("WorkspaceID = %q, want ws-1", got.WorkspaceID)
	}
	step1 := stepByID(t, got, StepWorkspaceRepo)
	if step1.Status != StatusActionable {
		t.Errorf("step 1 status = %s, want actionable when no repos", step1.Status)
	}
	for _, id := range []StepID{StepVerifyRepo, StepSetupBackend, StepCreateAgent, StepCreateIssue, StepRunAgent} {
		s := stepByID(t, got, id)
		if s.Status != StatusBlocked {
			t.Errorf("step %s should be blocked when step 1 actionable, got %s", id, s.Status)
		}
	}
}

func TestComputeStatus_WorkspaceScoped_WarningRepo_UnblocksDownstream(t *testing.T) {
	deps := stubDeps{
		getWorkspace: func(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
			// Repo with no DefaultBranch triggers warning at verify step.
			return &ops.WorkspaceData{
				ID:    wsID,
				Repos: []ops.WorkspaceRepo{{Name: "my-app", Path: "/tmp/my-app"}},
			}, nil
		},
		backendsHealth: func(ctx context.Context) ([]ops.BackendHealth, error) {
			return []ops.BackendHealth{
				{Name: "claude", Available: true, Installed: true, APIKeySet: true},
			}, nil
		},
	}
	got := ComputeStatus(context.Background(), deps, "ws-1")

	verify := stepByID(t, got, StepVerifyRepo)
	if verify.Status != StatusWarning {
		t.Errorf("verify-repo = %s, want warning when repo has no default branch", verify.Status)
	}
	if got.ActiveRepo != "my-app" {
		t.Errorf("ActiveRepo = %q, want my-app", got.ActiveRepo)
	}
	// Warning must unblock downstream.
	backend := stepByID(t, got, StepSetupBackend)
	if backend.Status != StatusComplete {
		t.Errorf("setup-backend = %s, want complete (warning should unblock)", backend.Status)
	}
}

func TestComputeStatus_BackendInstalledNoKey(t *testing.T) {
	deps := stubDeps{
		getWorkspace: func(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
			return &ops.WorkspaceData{
				Repos: []ops.WorkspaceRepo{{Name: "r", DefaultBranch: "main"}},
			}, nil
		},
		backendsHealth: func(ctx context.Context) ([]ops.BackendHealth, error) {
			return []ops.BackendHealth{
				{Name: "codex", DisplayName: "Codex", Installed: true, APIKeySet: false},
			}, nil
		},
	}
	got := ComputeStatus(context.Background(), deps, "ws-1")

	backend := stepByID(t, got, StepSetupBackend)
	if backend.Status != StatusActionable {
		t.Errorf("setup-backend = %s, want actionable when installed but no key", backend.Status)
	}
	if backend.Message == "" {
		t.Error("setup-backend should explain the missing auth in message")
	}
	// Downstream should be blocked.
	agent := stepByID(t, got, StepCreateAgent)
	if agent.Status != StatusBlocked {
		t.Errorf("create-agent = %s, want blocked downstream of actionable", agent.Status)
	}
}

func TestComputeStatus_BackendsHealthError(t *testing.T) {
	deps := stubDeps{
		getWorkspace: func(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
			return &ops.WorkspaceData{Repos: []ops.WorkspaceRepo{{Name: "r", DefaultBranch: "main"}}}, nil
		},
		backendsHealth: func(ctx context.Context) ([]ops.BackendHealth, error) {
			return nil, errors.New("ops down")
		},
	}
	got := ComputeStatus(context.Background(), deps, "ws-1")

	backend := stepByID(t, got, StepSetupBackend)
	if backend.Status != StatusUnknown {
		t.Errorf("setup-backend = %s, want unknown on backend health error", backend.Status)
	}
	agent := stepByID(t, got, StepCreateAgent)
	if agent.Status != StatusBlocked {
		t.Errorf("create-agent = %s, want blocked when prior is unknown", agent.Status)
	}
}

func TestComputeStatus_AllComplete(t *testing.T) {
	deps := stubDeps{
		getWorkspace: func(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
			return &ops.WorkspaceData{
				Repos:  []ops.WorkspaceRepo{{Name: "r", DefaultBranch: "main"}},
				Agents: []ops.WorkspaceAgentInfo{{Name: "falcon"}},
			}, nil
		},
		backendsHealth: func(ctx context.Context) ([]ops.BackendHealth, error) {
			return []ops.BackendHealth{{Name: "claude", Available: true, Installed: true, APIKeySet: true}}, nil
		},
		issueCount: func(ctx context.Context) (int, error) { return 3, nil },
	}
	got := ComputeStatus(context.Background(), deps, "ws-1")

	for _, id := range []StepID{StepWorkspaceRepo, StepVerifyRepo, StepSetupBackend, StepCreateAgent, StepCreateIssue} {
		s := stepByID(t, got, id)
		if s.Status != StatusComplete {
			t.Errorf("step %s = %s, want complete", id, s.Status)
		}
	}
	// Run-agent stays actionable until session detection lands (Phase 6).
	run := stepByID(t, got, StepRunAgent)
	if run.Status != StatusActionable {
		t.Errorf("run-agent = %s, want actionable (Phase 1 stub)", run.Status)
	}
	if got.AllComplete {
		t.Error("AllComplete should be false while run-agent is actionable")
	}
}

func TestComputeStatus_IssueCountError_BlocksRunAgent(t *testing.T) {
	deps := stubDeps{
		getWorkspace: func(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
			return &ops.WorkspaceData{
				Repos:  []ops.WorkspaceRepo{{Name: "r", DefaultBranch: "main"}},
				Agents: []ops.WorkspaceAgentInfo{{Name: "a"}},
			}, nil
		},
		backendsHealth: func(ctx context.Context) ([]ops.BackendHealth, error) {
			return []ops.BackendHealth{{Available: true, Installed: true, APIKeySet: true}}, nil
		},
		issueCount: func(ctx context.Context) (int, error) { return 0, errors.New("backend down") },
	}
	got := ComputeStatus(context.Background(), deps, "ws-1")

	issue := stepByID(t, got, StepCreateIssue)
	if issue.Status != StatusUnknown {
		t.Errorf("create-issue = %s, want unknown when count errors", issue.Status)
	}
	run := stepByID(t, got, StepRunAgent)
	if run.Status != StatusBlocked {
		t.Errorf("run-agent = %s, want blocked downstream of unknown", run.Status)
	}
}

func TestHandleStatus_NoWorkspace(t *testing.T) {
	deps := stubDeps{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/onboarding/status", nil)
	HandleStatus(deps).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body response
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Success {
		t.Errorf("Success = false, error: %s", body.Error)
	}
	if body.Data.WorkspaceID != "" {
		t.Errorf("WorkspaceID = %q, want empty for top-level route", body.Data.WorkspaceID)
	}
}

func TestHandleStatus_WorkspaceScopedFromContext(t *testing.T) {
	deps := stubDeps{
		getWorkspace: func(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
			return &ops.WorkspaceData{ID: wsID}, nil
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-uuid/onboarding/status", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws-uuid"))
	HandleStatus(deps).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body response
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.WorkspaceID != "ws-uuid" {
		t.Errorf("WorkspaceID = %q, want ws-uuid", body.Data.WorkspaceID)
	}
}
