package git

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	serverhandler "github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func TestDecodeOptionalRequest(t *testing.T) {
	for _, test := range []struct {
		name       string
		body       string
		wantOK     bool
		wantStatus int
	}{
		{name: "empty", body: "", wantOK: true, wantStatus: http.StatusOK},
		{name: "valid", body: `{"target":"main"}`, wantOK: true, wantStatus: http.StatusOK},
		{name: "malformed", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "trailing", body: `{"target":"main"} {}`, wantStatus: http.StatusBadRequest},
		{name: "oversized", body: `{"target":"` + strings.Repeat("a", serverhandler.MaxRequestBody) + `"}`, wantStatus: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			var destination loomapi.GitPushRequest
			ok := decodeOptionalRequest(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body)), &destination)
			if ok != test.wantOK || recorder.Code != test.wantStatus {
				t.Fatalf("decode = %v/%d, want %v/%d; body=%s", ok, recorder.Code, test.wantOK, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestGitPushMapsCommandAndConflict(t *testing.T) {
	var captured sourcecontrol.PushCommand
	checkout := &stubCheckout{push: func(_ context.Context, command sourcecontrol.PushCommand) (*sourcecontrol.PushResult, error) {
		captured = command
		return &sourcecontrol.PushResult{Message: "conflict", ConflictedFiles: []string{"a.go"}}, nil
	}}
	recorder := serveRoute(
		"POST /api/workspaces/{ws}/agents/{name}/git/push",
		HandleGitPush(checkout),
		request(http.MethodPost, "/api/workspaces/test-ws/agents/coder/git/push", `{"target":"develop"}`),
	)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	if captured != (sourcecontrol.PushCommand{WorkspaceKey: "test-ws", AgentID: "coder", TargetBranch: "develop"}) {
		t.Fatalf("command = %+v", captured)
	}
	if !strings.Contains(recorder.Body.String(), `"conflicted_files":["a.go"]`) {
		t.Fatalf("response = %s", recorder.Body.String())
	}
}

func TestGitPullMapsCommand(t *testing.T) {
	var captured sourcecontrol.PullCommand
	checkout := &stubCheckout{pull: func(_ context.Context, command sourcecontrol.PullCommand) (*sourcecontrol.PullResult, error) {
		captured = command
		return &sourcecontrol.PullResult{Success: true, Message: "pulled"}, nil
	}}
	recorder := serveRoute(
		"POST /api/workspaces/{ws}/agents/{name}/git/pull",
		HandleGitPull(checkout),
		request(http.MethodPost, "/api/workspaces/test-ws/agents/coder/git/pull", `{"source":"release"}`),
	)
	if recorder.Code != http.StatusOK || captured.SourceBranch != "release" || captured.AgentID != "coder" || captured.WorkspaceKey != "test-ws" {
		t.Fatalf("status=%d command=%+v body=%s", recorder.Code, captured, recorder.Body.String())
	}
}

func TestGitSyncReturnsPartialConflict(t *testing.T) {
	checkout := &stubCheckout{sync: func(context.Context, sourcecontrol.SyncCommand) (*sourcecontrol.SyncResult, error) {
		return &sourcecontrol.SyncResult{Push: &sourcecontrol.PushResult{Message: "conflict", ConflictedFiles: []string{"a.go"}}}, nil
	}}
	recorder := serveRoute(
		"POST /api/workspaces/{ws}/agents/{name}/git/sync",
		HandleGitSync(checkout),
		request(http.MethodPost, "/api/workspaces/test-ws/agents/coder/git/sync", ""),
	)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"push_result"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGitPushAllMapsAggregate(t *testing.T) {
	checkout := &stubCheckout{pushAll: func(_ context.Context, command sourcecontrol.PushAllCommand) (*sourcecontrol.PushAllResult, error) {
		if command.WorkspaceKey != "test-ws" {
			t.Fatalf("workspace = %q", command.WorkspaceKey)
		}
		return &sourcecontrol.PushAllResult{
			Pushed: 1, Failed: 1,
			Results: []sourcecontrol.PushAllCheckoutResult{{AgentID: "one", Success: true}, {AgentID: "two", Error: "failed"}},
		}, nil
	}}
	recorder := serveRoute(
		"POST /api/workspaces/{ws}/git/push-all",
		HandleGitPushAll(checkout),
		request(http.MethodPost, "/api/workspaces/test-ws/git/push-all", ""),
	)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"name":"one"`) || !strings.Contains(recorder.Body.String(), `"failed":1`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGitPullRequestCreated(t *testing.T) {
	var captured sourcecontrol.CreatePullRequestCommand
	checkout := &stubCheckout{createPullRequest: func(_ context.Context, command sourcecontrol.CreatePullRequestCommand) (*sourcecontrol.PullRequestCreation, error) {
		captured = command
		return &sourcecontrol.PullRequestCreation{Created: true, URL: "https://example.test/pull/1"}, nil
	}}
	recorder := serveRoute(
		"POST /api/workspaces/{ws}/agents/{name}/git/pr",
		HandleGitPR(checkout),
		request(http.MethodPost, "/api/workspaces/test-ws/agents/coder/git/pr", `{"target":"main"}`),
	)
	if recorder.Code != http.StatusCreated || captured.TargetBranch != "main" || !strings.Contains(recorder.Body.String(), `"created":true`) {
		t.Fatalf("status=%d command=%+v body=%s", recorder.Code, captured, recorder.Body.String())
	}
}

func TestGitResetMapsLockedError(t *testing.T) {
	checkout := &stubCheckout{reset: func(context.Context, sourcecontrol.ResetCommand) (*sourcecontrol.ResetResult, error) {
		return nil, &sourcecontrol.ResetLockedError{AgentID: "coder", PID: 42, Age: "2m", TaskID: "TASK-1"}
	}}
	recorder := serveRoute(
		"POST /api/workspaces/{ws}/agents/{name}/git/reset",
		HandleGitReset(checkout),
		request(http.MethodPost, "/api/workspaces/test-ws/agents/coder/git/reset", `{"force":true}`),
	)
	if recorder.Code != http.StatusLocked || !strings.Contains(recorder.Body.String(), `"task_id":"TASK-1"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGitStatusMapsOwnerResult(t *testing.T) {
	checkout := &stubCheckout{agentStatus: func(_ context.Context, query sourcecontrol.AgentStatusQuery) (*sourcecontrol.AgentStatusResult, error) {
		if query.WorkspaceKey != "test-ws" || query.AgentID != "coder" {
			t.Fatalf("query = %+v", query)
		}
		return &sourcecontrol.AgentStatusResult{Branch: "feature", TargetBranch: "main", Clean: true, Ahead: 2}, nil
	}}
	recorder := serveRoute(
		"GET /api/workspaces/{ws}/agents/{name}/git/status",
		HandleGitStatus(checkout),
		request(http.MethodGet, "/api/workspaces/test-ws/agents/coder/git/status", ""),
	)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"is_clean":true`) || !strings.Contains(recorder.Body.String(), `"ahead":2`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGitTargetUpdateMapsCommand(t *testing.T) {
	var captured sourcecontrol.SetTargetBranchCommand
	checkout := &stubCheckout{setTargetBranch: func(_ context.Context, command sourcecontrol.SetTargetBranchCommand) error {
		captured = command
		return nil
	}}
	recorder := serveRoute(
		"PATCH /api/workspaces/{ws}/agents/{name}/git/target",
		HandleGitTargetUpdate(checkout),
		request(http.MethodPatch, "/api/workspaces/test-ws/agents/coder/git/target", `{"branch":"develop"}`),
	)
	if recorder.Code != http.StatusOK || captured.Branch != "develop" || captured.AgentID != "coder" {
		t.Fatalf("status=%d command=%+v body=%s", recorder.Code, captured, recorder.Body.String())
	}
}

func TestGitHandlersRejectInvalidRefs(t *testing.T) {
	for _, test := range []struct {
		name    string
		pattern string
		handler http.HandlerFunc
		method  string
		path    string
		body    string
	}{
		{name: "push", pattern: "POST /api/workspaces/{ws}/agents/{name}/git/push", handler: HandleGitPush(&stubCheckout{}), method: http.MethodPost, path: "/api/workspaces/test-ws/agents/coder/git/push", body: `{"target":"../main"}`},
		{name: "pull", pattern: "POST /api/workspaces/{ws}/agents/{name}/git/pull", handler: HandleGitPull(&stubCheckout{}), method: http.MethodPost, path: "/api/workspaces/test-ws/agents/coder/git/pull", body: `{"source":"-main"}`},
		{name: "reset", pattern: "POST /api/workspaces/{ws}/agents/{name}/git/reset", handler: HandleGitReset(&stubCheckout{}), method: http.MethodPost, path: "/api/workspaces/test-ws/agents/coder/git/reset", body: `{"branch":"main..bad"}`},
		{name: "target", pattern: "PATCH /api/workspaces/{ws}/agents/{name}/git/target", handler: HandleGitTargetUpdate(&stubCheckout{}), method: http.MethodPatch, path: "/api/workspaces/test-ws/agents/coder/git/target", body: `{"branch":""}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := serveRoute(test.pattern, test.handler, request(test.method, test.path, test.body))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestGitHandlersSanitizeSourceControlErrors(t *testing.T) {
	checkout := &stubCheckout{push: func(context.Context, sourcecontrol.PushCommand) (*sourcecontrol.PushResult, error) {
		return nil, errors.Join(sourcecontrol.ErrRemote, errors.New("secret remote stderr"))
	}}
	recorder := serveRoute(
		"POST /api/workspaces/{ws}/agents/{name}/git/push",
		HandleGitPush(checkout),
		request(http.MethodPost, "/api/workspaces/test-ws/agents/coder/git/push", ""),
	)
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), "secret remote stderr") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
