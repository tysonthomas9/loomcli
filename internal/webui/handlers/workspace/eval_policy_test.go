package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func evalPolicyPatchRequest(wsID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+wsID+"/config/eval-policy", strings.NewReader(body))
	return req.WithContext(middleware.WithWorkspace(req.Context(), wsID))
}

func TestHandleWorkspaceEvalPolicyPatch(t *testing.T) {
	svc := &mockWorkspaceService{patchWorkspaceEvalPolicyFn: func(_ context.Context, wsID string, got service.WorkspaceEvalPolicyPatch) (*ops.WorkspaceData, error) {
		if wsID != "ALPHA" || got.EvalSamplingPercent == nil || *got.EvalSamplingPercent != 50 || got.EvalBatchSize == nil || *got.EvalBatchSize != 10 || got.EvalLookbackDays != nil {
			t.Fatalf("patch args = %q, %+v", wsID, got)
		}
		return &ops.WorkspaceData{ID: wsID, EvalSamplingPercent: 50, EvalBatchSize: 10}, nil
	}}
	rec := httptest.NewRecorder()
	handleWorkspaceEvalPolicyPatch(svc).ServeHTTP(rec, evalPolicyPatchRequest("ALPHA", `{"eval_sampling_percent":50,"eval_batch_size":10}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response WorkspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil || !response.Success {
		t.Fatalf("response = %+v, err = %v", response, err)
	}
}

func TestHandleWorkspaceEvalPolicyPatchRejectsInvalidValues(t *testing.T) {
	rec := httptest.NewRecorder()
	handleWorkspaceEvalPolicyPatch(&mockWorkspaceService{}).ServeHTTP(rec, evalPolicyPatchRequest("ALPHA", `{"eval_sampling_percent":0}`))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "eval_sampling_percent must be between 1 and 100") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
