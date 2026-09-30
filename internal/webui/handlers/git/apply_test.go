package git

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type applyAgentService struct {
	service.AgentService
	apply func(context.Context, ops.ApplyRevisionRequest) (*ops.GitPushResult, error)
}

func (s applyAgentService) GitApply(ctx context.Context, request ops.ApplyRevisionRequest) (*ops.GitPushResult, error) {
	return s.apply(ctx, request)
}

func TestGitApplyUsesExplicitRevisionAndWorkspace(t *testing.T) {
	svc := applyAgentService{apply: func(_ context.Context, request ops.ApplyRevisionRequest) (*ops.GitPushResult, error) {
		if request.Workspace != "workspace" || request.Change != "change" || request.Revision != 2 || request.Lead != "lead" {
			t.Fatalf("unexpected Apply request: %+v", request)
		}
		return &ops.GitPushResult{Success: true}, nil
	}}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/workspace/git/apply", strings.NewReader(`{"workspace":"foreign","change":"change","revision":2,"lead":"lead"}`))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "workspace"))
	response := httptest.NewRecorder()
	HandleGitApply(svc).ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestGitApplyReportsReviewRequired(t *testing.T) {
	svc := applyAgentService{apply: func(context.Context, ops.ApplyRevisionRequest) (*ops.GitPushResult, error) {
		return nil, loomgit.NewError(loomgit.ReviewRequired, "approval missing", nil)
	}}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/workspace/git/apply", strings.NewReader(`{"change":"change","revision":2,"lead":"lead"}`))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "workspace"))
	response := httptest.NewRecorder()
	HandleGitApply(svc).ServeHTTP(response, req)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "review_required") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestGitApplyReportsConflictPaths(t *testing.T) {
	svc := applyAgentService{apply: func(context.Context, ops.ApplyRevisionRequest) (*ops.GitPushResult, error) {
		return &ops.GitPushResult{ConflictedFiles: []string{"main.go"}}, nil
	}}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/workspace/git/apply", strings.NewReader(`{"change":"change","revision":2,"lead":"lead"}`))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "workspace"))
	response := httptest.NewRecorder()
	HandleGitApply(svc).ServeHTTP(response, req)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "main.go") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
