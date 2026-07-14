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
)

func designFormatPatchRequest(wsID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+wsID+"/config/design-format", strings.NewReader(body))
	return req.WithContext(middleware.WithWorkspace(req.Context(), wsID))
}

func TestHandleWorkspaceDesignFormatPatch(t *testing.T) {
	for _, format := range []string{"markdown", "html"} {
		t.Run(format, func(t *testing.T) {
			svc := &mockWorkspaceService{patchWorkspaceDesignFormatFn: func(_ context.Context, wsID, got string) (*ops.WorkspaceData, error) {
				if wsID != "ALPHA" || got != format {
					t.Fatalf("patch args = %q, %q", wsID, got)
				}
				return &ops.WorkspaceData{ID: wsID, DesignFormat: got}, nil
			}}
			rec := httptest.NewRecorder()
			handleWorkspaceDesignFormatPatch(svc).ServeHTTP(rec, designFormatPatchRequest("ALPHA", `{"design_format":"`+format+`"}`))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var response WorkspaceResponse
			if err := json.NewDecoder(rec.Body).Decode(&response); err != nil || !response.Success {
				t.Fatalf("response = %+v, err = %v", response, err)
			}
		})
	}
}

func TestHandleWorkspaceDesignFormatPatchRejectsInvalidFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	handleWorkspaceDesignFormatPatch(&mockWorkspaceService{}).ServeHTTP(rec, designFormatPatchRequest("ALPHA", `{"design_format":"svg"}`))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid design format") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
