package issues

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func TestHandleAssignWorkItemRepositoryReturnsCanonicalIssue(t *testing.T) {
	api := &routeWorkItems{}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/workspaces/{ws}/issues/{id}/repository", HandleAssignWorkItemRepository(api))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/test-ws/issues/task-11/repository", strings.NewReader(`{"repo":"hello-world"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(api.repositoryIDs) != 1 || api.repositoryIDs[0] != "task-11" {
		t.Fatalf("repository IDs = %v", api.repositoryIDs)
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			ID         string `json:"id"`
			SourceRepo string `json:"source_repo"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !body.Success || body.Data.ID != "task-11" || body.Data.SourceRepo != "hello-world" {
		t.Fatalf("response = %+v", body)
	}
}

func TestHandleAssignWorkItemRepositoryRejectsUnknownFields(t *testing.T) {
	api := &routeWorkItems{}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"repo":"hello-world","status":"open"}`))
	req.SetPathValue("id", "task-11")
	rec := httptest.NewRecorder()

	HandleAssignWorkItemRepository(api).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(api.repositoryIDs) != 0 {
		t.Fatal("capability called for invalid request")
	}
}

func TestHandleAssignWorkItemRepositoryMapsConflict(t *testing.T) {
	api := &routeWorkItems{assignRepositoryErr: workitems.ErrConflict}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"repo":"hello-world"}`))
	req.SetPathValue("id", "task-11")
	rec := httptest.NewRecorder()

	HandleAssignWorkItemRepository(api).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
