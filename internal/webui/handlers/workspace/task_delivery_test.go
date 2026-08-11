package workspace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func taskDeliveryPatchRequest(wsID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+wsID+"/config/task-delivery", strings.NewReader(body))
	return req.WithContext(middleware.WithWorkspace(req.Context(), wsID))
}

func TestHandleWorkspaceTaskDeliveryPatch(t *testing.T) {
	svc := &mockWorkspaceService{patchWorkspaceTaskDeliveryFn: func(_ context.Context, wsID, repoName string, requirement domain.TaskDeliveryRequirement) (*ops.WorkspaceData, error) {
		if wsID != "ALPHA" || repoName != "app" || requirement != domain.TaskDeliveryPullRequest {
			t.Fatalf("patch args = %q, %q, %q", wsID, repoName, requirement)
		}
		return &ops.WorkspaceData{ID: wsID}, nil
	}}
	rec := httptest.NewRecorder()
	HandleWorkspaceTaskDeliveryPatch(svc).ServeHTTP(rec, taskDeliveryPatchRequest("ALPHA", `{"repository":"app","task_delivery_requirement":"pull_request"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceTaskDeliveryPatchRejectsEmptyWorkspaceRequirement(t *testing.T) {
	rec := httptest.NewRecorder()
	HandleWorkspaceTaskDeliveryPatch(&mockWorkspaceService{}).ServeHTTP(rec, taskDeliveryPatchRequest("ALPHA", `{"task_delivery_requirement":""}`))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid task delivery requirement") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
