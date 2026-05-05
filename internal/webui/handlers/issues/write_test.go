package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// ===========================================================================
// handlePatchIssue tests
// ===========================================================================

func TestHandlePatchIssueW_UpdateStatus(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		wantStatus string
	}{
		{"open", "open", "open"},
		{"review to open (approve flow)", "open", "open"},
		{"in_progress", "in_progress", "in_progress"},
		{"review", "review", "review"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockIssueService{
				patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
					if params.Status == nil || *params.Status != tt.wantStatus {
						t.Errorf("PatchIssue() Status = %v, want %q", params.Status, tt.wantStatus)
					}
					return nil
				},
			}
			handler := handlePatchIssue(svc)

			body := `{"status":"` + tt.status + `"}`
			req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc123", strings.NewReader(body))
			req.SetPathValue("id", "abc123")
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}
			result := assertJSONResponse(t, w)
			assertEnvelopeSuccess(t, result)
		})
	}
}

func TestHandlePatchIssueW_UpdateTitle(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			if params.Title == nil || *params.Title != "New Title" {
				t.Errorf("PatchIssue() Title = %v, want %q", params.Title, "New Title")
			}
			if params.IssueID != "issue-42" {
				t.Errorf("PatchIssue() IssueID = %q, want %q", params.IssueID, "issue-42")
			}
			return nil
		},
	}
	handler := handlePatchIssue(svc)

	body := `{"title":"New Title"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/issue-42", strings.NewReader(body))
	req.SetPathValue("id", "issue-42")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	result := assertJSONResponse(t, w)
	assertEnvelopeSuccess(t, result)
}

func TestHandlePatchIssueW_ReturnsUpdatedIssue(t *testing.T) {
	updatedIssue := json.RawMessage(`{"id":"issue-42","title":"Existing title","source_repo":"hello-world"}`)
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			if params.IssueID != "issue-42" {
				t.Errorf("PatchIssue() IssueID = %q, want %q", params.IssueID, "issue-42")
			}
			return nil
		},
		getIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			if issueID != "issue-42" {
				t.Errorf("GetIssue() issueID = %q, want %q", issueID, "issue-42")
			}
			return updatedIssue, nil
		},
	}
	handler := handlePatchIssue(svc)

	body := `{"set_labels":["repo:hello-world"]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/issue-42", strings.NewReader(body))
	req.SetPathValue("id", "issue-42")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp PatchIssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data = %#v, want object", resp.Data)
	}
	if got := data["title"]; got != "Existing title" {
		t.Fatalf("data.title = %v, want Existing title", got)
	}
}

func TestHandlePatchIssueW_UpdatePriority(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			if params.Priority == nil || *params.Priority != 2 {
				t.Errorf("PatchIssue() Priority = %v, want 2", params.Priority)
			}
			return nil
		},
	}
	handler := handlePatchIssue(svc)

	body := `{"priority":2}`
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/p-1", strings.NewReader(body))
	req.SetPathValue("id", "p-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandlePatchIssueW_UpdateAssignee(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			if params.Assignee == nil || *params.Assignee != "alice" {
				t.Errorf("PatchIssue() Assignee = %v, want %q", params.Assignee, "alice")
			}
			return nil
		},
	}
	handler := handlePatchIssue(svc)

	body := `{"assignee":"alice"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/a-1", strings.NewReader(body))
	req.SetPathValue("id", "a-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandlePatchIssueW_InvalidStatusValue(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			return service.ErrValidation("invalid status: bogus")
		},
	}
	handler := handlePatchIssue(svc)

	body := `{"status":"bogus"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/x-1", strings.NewReader(body))
	req.SetPathValue("id", "x-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandlePatchIssueW_MissingIssueID(t *testing.T) {
	svc := &mockIssueService{}
	handler := handlePatchIssue(svc)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	result := assertJSONResponse(t, w)
	if result["success"] != false {
		t.Error("expected success=false")
	}
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "missing issue ID") {
		t.Errorf("error = %q, want to contain 'missing issue ID'", errMsg)
	}
}

func TestHandlePatchIssueW_EmptyBody(t *testing.T) {
	svc := &mockIssueService{}
	handler := handlePatchIssue(svc)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/e-1", strings.NewReader(""))
	req.SetPathValue("id", "e-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	result := assertJSONResponse(t, w)
	if result["success"] != false {
		t.Error("expected success=false")
	}
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "invalid request body") {
		t.Errorf("error = %q, want to contain 'invalid request body'", errMsg)
	}
}

func TestHandlePatchIssueW_NotFound(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			return service.ErrNotFound("issue not found")
		},
	}
	handler := handlePatchIssue(svc)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/missing", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "missing")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandlePatchIssueW_ServiceUnavailable(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			return service.ErrUnavailable("daemon not available")
		},
	}
	handler := handlePatchIssue(svc)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/pg-1", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "pg-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandlePatchIssueW_InternalError(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			return service.ErrInternal("connection reset", nil)
		},
	}
	handler := handlePatchIssue(svc)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/rpc-1", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "rpc-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandlePatchIssueW_RPCErrorNotFound(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			return service.ErrNotFound("issue not found: abc")
		},
	}
	handler := handlePatchIssue(svc)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandlePatchIssueW_CannotUpdateTemplate(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			return service.ErrConflict("cannot update template issue")
		},
	}
	handler := handlePatchIssue(svc)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/tmpl-1", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "tmpl-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

// ===========================================================================
// validatePatchRequest tests (still in handler layer)
// ===========================================================================

func TestValidatePatchRequest_MissingID(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()

	_, _, ok := validatePatchRequest(w, req)
	if ok {
		t.Error("expected ok=false for missing ID")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestValidatePatchRequest_EmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/x-1", strings.NewReader(""))
	req.SetPathValue("id", "x-1")
	w := httptest.NewRecorder()

	_, _, ok := validatePatchRequest(w, req)
	if ok {
		t.Error("expected ok=false for empty body")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestValidatePatchRequest_ValidRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/v-1", strings.NewReader(`{"title":"Test"}`))
	req.SetPathValue("id", "v-1")
	w := httptest.NewRecorder()

	issueID, patchReq, ok := validatePatchRequest(w, req)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if issueID != "v-1" {
		t.Errorf("issueID = %q, want %q", issueID, "v-1")
	}
	if patchReq.Title == nil || *patchReq.Title != "Test" {
		t.Errorf("Title = %v, want %q", patchReq.Title, "Test")
	}
}

// ===========================================================================
// handleCreateIssue tests
// ===========================================================================

func TestHandleCreateIssueW_WithTitleAndType(t *testing.T) {
	tests := []struct {
		name      string
		issueType string
	}{
		{"bug type", "bug"},
		{"feature type", "feature"},
		{"task type", "task"},
		{"epic type", "epic"},
		{"chore type", "chore"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectedData, _ := json.Marshal(map[string]interface{}{
				"id":         "new-1",
				"title":      "Test Issue",
				"issue_type": tt.issueType,
				"status":     "open",
			})
			svc := &mockIssueService{
				createIssueFunc: func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
					if params.Title != "Test Issue" {
						t.Errorf("CreateIssue() Title = %q, want %q", params.Title, "Test Issue")
					}
					if params.IssueType != tt.issueType {
						t.Errorf("CreateIssue() IssueType = %q, want %q", params.IssueType, tt.issueType)
					}
					return expectedData, nil
				},
			}
			handler := handleCreateIssue(svc)

			body := `{"title":"Test Issue","issue_type":"` + tt.issueType + `","priority":1}`
			req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusCreated {
				t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
			}
			var resp IssuesResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if !resp.Success {
				t.Error("expected success=true")
			}
		})
	}
}

func TestHandleCreateIssueW_WithParent(t *testing.T) {
	svc := &mockIssueService{
		createIssueFunc: func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			if params.Parent != "epic-42" {
				t.Errorf("CreateIssue() Parent = %q, want %q", params.Parent, "epic-42")
			}
			if params.Title != "Child Task" {
				t.Errorf("CreateIssue() Title = %q, want %q", params.Title, "Child Task")
			}
			return json.RawMessage(`{"id":"child-1","title":"Child Task","parent":"epic-42"}`), nil
		},
	}
	handler := handleCreateIssue(svc)

	body := `{"title":"Child Task","issue_type":"task","priority":2,"parent":"epic-42"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	var resp IssuesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
}

func TestHandleCreateIssueW_WithStatus(t *testing.T) {
	svc := &mockIssueService{
		createIssueFunc: func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			if params.Status != "deferred" {
				t.Errorf("CreateIssue() Status = %q, want %q", params.Status, "deferred")
			}
			return json.RawMessage(`{"id":"deferred-1","title":"Deferred Task","status":"deferred"}`), nil
		},
	}
	handler := handleCreateIssue(svc)

	body := `{"title":"Deferred Task","issue_type":"task","priority":2,"status":"deferred"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
}

func TestHandleCreateIssueW_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantContain string
	}{
		{
			name:        "missing title",
			body:        `{"issue_type":"bug","priority":1}`,
			wantContain: "title",
		},
		{
			name:        "empty title",
			body:        `{"title":"   ","issue_type":"bug","priority":1}`,
			wantContain: "title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockIssueService{
				createIssueFunc: func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
					return nil, service.ErrValidation(tt.wantContain + " is required")
				},
			}
			handler := handleCreateIssue(svc)

			req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			// WriteServiceError for validation errors returns 400
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d: %s", w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}

func TestHandleCreateIssueW_InvalidType(t *testing.T) {
	svc := &mockIssueService{
		createIssueFunc: func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			return nil, service.ErrValidation("invalid issue_type")
		},
	}
	handler := handleCreateIssue(svc)

	body := `{"title":"Test","issue_type":"invalid_type","priority":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateIssueW_ServiceUnavailable(t *testing.T) {
	svc := &mockIssueService{
		createIssueFunc: func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			return nil, service.ErrUnavailable("daemon not available")
		},
	}
	handler := handleCreateIssue(svc)

	body := `{"title":"Test","issue_type":"bug","priority":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleCreateIssueW_InternalError(t *testing.T) {
	svc := &mockIssueService{
		createIssueFunc: func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			return nil, service.ErrInternal("connection reset", nil)
		},
	}
	handler := handleCreateIssue(svc)

	body := `{"title":"Test","issue_type":"bug","priority":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleCreateIssueW_DaemonError(t *testing.T) {
	svc := &mockIssueService{
		createIssueFunc: func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			return nil, service.ErrInternal("duplicate issue ID", nil)
		},
	}
	handler := handleCreateIssue(svc)

	body := `{"title":"Test","issue_type":"bug","priority":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleCreateIssueW_EmptyBody(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleCreateIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// ===========================================================================
// handleCloseIssue tests
// ===========================================================================

func TestHandleCloseIssueW_WithReason(t *testing.T) {
	svc := &mockIssueService{
		closeIssueFunc: func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
			if params.IssueID != "close-1" {
				t.Errorf("CloseIssue() IssueID = %q, want %q", params.IssueID, "close-1")
			}
			if params.Reason != "completed" {
				t.Errorf("CloseIssue() Reason = %q, want %q", params.Reason, "completed")
			}
			return json.RawMessage(`{"id":"close-1","status":"closed"}`), nil
		},
	}
	handler := handleCloseIssue(svc)

	body := `{"reason":"completed"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/close-1/close", strings.NewReader(body))
	req.SetPathValue("id", "close-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp CloseResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
}

func TestHandleCloseIssueW_WithoutReason(t *testing.T) {
	svc := &mockIssueService{
		closeIssueFunc: func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
			if params.Reason != "" {
				t.Errorf("CloseIssue() Reason = %q, want empty", params.Reason)
			}
			return json.RawMessage(`{"id":"close-2","status":"closed"}`), nil
		},
	}
	handler := handleCloseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/close-2/close", nil)
	req.SetPathValue("id", "close-2")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleCloseIssueW_NotFound(t *testing.T) {
	svc := &mockIssueService{
		closeIssueFunc: func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
			return nil, service.ErrNotFound("issue not found: ghost-1")
		},
	}
	handler := handleCloseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/ghost-1/close", nil)
	req.SetPathValue("id", "ghost-1")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	result := assertJSONResponse(t, w)
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "not found") {
		t.Errorf("error = %q, want to contain 'not found'", errMsg)
	}
}

func TestHandleCloseIssueW_MissingID(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleCloseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues//close", nil)
	req.SetPathValue("id", "")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCloseIssueW_ServiceUnavailable(t *testing.T) {
	svc := &mockIssueService{
		closeIssueFunc: func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
			return nil, service.ErrUnavailable("daemon not available")
		},
	}
	handler := handleCloseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/pge-1/close", nil)
	req.SetPathValue("id", "pge-1")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleCloseIssueW_InternalError(t *testing.T) {
	svc := &mockIssueService{
		closeIssueFunc: func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
			return nil, service.ErrInternal("connection reset", nil)
		},
	}
	handler := handleCloseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/rpc-close/close", nil)
	req.SetPathValue("id", "rpc-close")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleCloseIssueW_ConflictError(t *testing.T) {
	svc := &mockIssueService{
		closeIssueFunc: func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
			return nil, service.ErrConflict("issue already closed")
		},
	}
	handler := handleCloseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/dup-close/close", nil)
	req.SetPathValue("id", "dup-close")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestHandleCloseIssueW_BlockerConflict(t *testing.T) {
	svc := &mockIssueService{
		closeIssueFunc: func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
			return nil, service.ErrConflict("has open blocker dependencies")
		},
	}
	handler := handleCloseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/blocked-1/close", nil)
	req.SetPathValue("id", "blocked-1")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

// ===========================================================================
// handleDeleteIssue tests
// ===========================================================================

func TestHandleDeleteIssueW_MissingID(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleDeleteIssue(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	result := assertJSONResponse(t, w)
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "missing issue ID") {
		t.Errorf("error = %q, want to contain 'missing issue ID'", errMsg)
	}
}

func TestHandleDeleteIssueW_Success(t *testing.T) {
	svc := &mockIssueService{
		deleteIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			if issueID != "del-ok" {
				t.Errorf("DeleteIssue() ID = %q, want %q", issueID, "del-ok")
			}
			return json.RawMessage(`{"id":"del-ok","deleted":true}`), nil
		},
	}
	handler := handleDeleteIssue(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/del-ok", nil)
	req.SetPathValue("id", "del-ok")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	result := assertJSONResponse(t, w)
	success, _ := result["success"].(bool)
	if !success {
		t.Error("expected success=true")
	}
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be a JSON object")
	}
	if data["id"] != "del-ok" {
		t.Errorf("data.id = %v, want del-ok", data["id"])
	}
}

func TestHandleDeleteIssueW_InternalError(t *testing.T) {
	svc := &mockIssueService{
		deleteIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			return nil, service.ErrInternal("connection reset", nil)
		},
	}
	handler := handleDeleteIssue(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/rpc-del", nil)
	req.SetPathValue("id", "rpc-del")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	result := assertJSONResponse(t, w)
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "connection reset") {
		t.Errorf("error = %q, want to contain 'connection reset'", errMsg)
	}
}

func TestHandleDeleteIssueW_DaemonReturnsFalse(t *testing.T) {
	svc := &mockIssueService{
		deleteIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			return nil, service.ErrInternal("cannot delete: issue has children", nil)
		},
	}
	handler := handleDeleteIssue(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/fail-del", nil)
	req.SetPathValue("id", "fail-del")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleDeleteIssueW_NotFound(t *testing.T) {
	svc := &mockIssueService{
		deleteIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			return nil, service.ErrNotFound("issue not found: ghost-del")
		},
	}
	handler := handleDeleteIssue(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/ghost-del", nil)
	req.SetPathValue("id", "ghost-del")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	result := assertJSONResponse(t, w)
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "not found") {
		t.Errorf("error = %q, want to contain 'not found'", errMsg)
	}
}

func TestHandleDeleteIssueW_ServiceUnavailable(t *testing.T) {
	svc := &mockIssueService{
		deleteIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			return nil, service.ErrUnavailable("daemon not available")
		},
	}
	handler := handleDeleteIssue(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/pge-del", nil)
	req.SetPathValue("id", "pge-del")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleDeleteIssueW_Timeout(t *testing.T) {
	svc := &mockIssueService{
		deleteIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			return nil, service.ErrTimeout("daemon not available")
		},
	}
	handler := handleDeleteIssue(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/timeout-del", nil)
	req.SetPathValue("id", "timeout-del")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d", w.Code, http.StatusGatewayTimeout)
	}
}

// ===========================================================================
// PatchIssueRequest JSON deserialization — AgentState field threading
// ===========================================================================

func TestPatchIssueRequest_AgentState_Deserialized(t *testing.T) {
	body := `{"agent_state":"running"}`
	var req PatchIssueRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if req.AgentState == nil {
		t.Fatal("expected AgentState to be non-nil")
	}
	if *req.AgentState != "running" {
		t.Errorf("AgentState = %q, want %q", *req.AgentState, "running")
	}
}

func TestPatchIssueRequest_AgentState_OmittedWhenAbsent(t *testing.T) {
	body := `{"title":"Test"}`
	var req PatchIssueRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if req.AgentState != nil {
		t.Errorf("expected AgentState to be nil when not in JSON, got %q", *req.AgentState)
	}
}

func TestHandlePatchIssueW_AgentState_ThreadedToService(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			if params.AgentState == nil {
				t.Fatal("expected AgentState to be non-nil in PatchIssueParams")
			}
			if *params.AgentState != "stuck" {
				t.Errorf("AgentState = %q, want %q", *params.AgentState, "stuck")
			}
			return nil
		},
	}
	handler := handlePatchIssue(svc)

	body := `{"agent_state":"stuck"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/agent-1", strings.NewReader(body))
	req.SetPathValue("id", "agent-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	result := assertJSONResponse(t, w)
	assertEnvelopeSuccess(t, result)
}

func TestHandlePatchIssueW_AgentState_NilWhenAbsent(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			if params.AgentState != nil {
				t.Errorf("expected AgentState to be nil when not in request, got %q", *params.AgentState)
			}
			return nil
		},
	}
	handler := handlePatchIssue(svc)

	body := `{"title":"No agent state"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/no-agent", strings.NewReader(body))
	req.SetPathValue("id", "no-agent")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// ===========================================================================
// handleClaimIssue tests
// ===========================================================================

func TestHandleClaimIssueW_Success(t *testing.T) {
	svc := &mockIssueService{
		claimIssueFunc: func(_ context.Context, params service.ClaimIssueParams) (json.RawMessage, error) {
			if params.IssueID != "claim-1" {
				t.Errorf("ClaimIssue IssueID = %q, want claim-1", params.IssueID)
			}
			return json.RawMessage(`{"id":"claim-1","assignee":"server","status":"in_progress"}`), nil
		},
	}
	h := handleClaimIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/claim-1/claim", nil)
	req.SetPathValue("id", "claim-1")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp IssuesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
	if !strings.Contains(string(resp.Data), `"assignee":"server"`) {
		t.Errorf("data should include assignee, got %s", string(resp.Data))
	}
}

func TestHandleClaimIssueW_MissingID(t *testing.T) {
	svc := &mockIssueService{
		claimIssueFunc: func(_ context.Context, _ service.ClaimIssueParams) (json.RawMessage, error) {
			t.Fatal("service should not be called with empty ID")
			return nil, nil
		},
	}
	h := handleClaimIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues//claim", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	result := assertJSONResponse(t, w)
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "missing") {
		t.Errorf("error = %q, want to contain 'missing'", errMsg)
	}
}

func TestHandleClaimIssueW_AlreadyClaimed(t *testing.T) {
	svc := &mockIssueService{
		claimIssueFunc: func(_ context.Context, _ service.ClaimIssueParams) (json.RawMessage, error) {
			return nil, service.ErrConflict("already claimed by other-agent")
		},
	}
	h := handleClaimIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/claim-2/claim", nil)
	req.SetPathValue("id", "claim-2")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
	result := assertJSONResponse(t, w)
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "already claimed") {
		t.Errorf("error = %q, want to contain 'already claimed'", errMsg)
	}
}

func TestHandleClaimIssueW_NotFound(t *testing.T) {
	svc := &mockIssueService{
		claimIssueFunc: func(_ context.Context, _ service.ClaimIssueParams) (json.RawMessage, error) {
			return nil, service.ErrNotFound("issue not found: ghost")
		},
	}
	h := handleClaimIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/ghost/claim", nil)
	req.SetPathValue("id", "ghost")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleClaimIssueW_ServiceUnavailable(t *testing.T) {
	svc := &mockIssueService{
		claimIssueFunc: func(_ context.Context, _ service.ClaimIssueParams) (json.RawMessage, error) {
			return nil, service.ErrUnavailable("daemon not available")
		},
	}
	h := handleClaimIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/x/claim", nil)
	req.SetPathValue("id", "x")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// ===========================================================================
// HandleReopenIssue tests
// ===========================================================================

// TestHandleReopenIssue_SuccessEmptyBody exercises the common UI path where
// the body is absent. The handler must forward to the service with an empty
// reason and emit {"success":true}.
func TestHandleReopenIssue_SuccessEmptyBody(t *testing.T) {
	var capturedParams service.ReopenIssueParams
	svc := &mockIssueService{
		reopenIssueFunc: func(_ context.Context, params service.ReopenIssueParams) error {
			capturedParams = params
			return nil
		},
	}
	h := HandleReopenIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/x/reopen", nil)
	req.SetPathValue("id", "x")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if capturedParams.IssueID != "x" {
		t.Errorf("expected issueID 'x', got %q", capturedParams.IssueID)
	}
	if capturedParams.Reason != "" {
		t.Errorf("expected empty reason, got %q", capturedParams.Reason)
	}

	var resp ReopenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false (error: %s)", resp.Error)
	}
}

// TestHandleReopenIssue_SuccessWithReason verifies the reason field is
// threaded through to the service.
func TestHandleReopenIssue_SuccessWithReason(t *testing.T) {
	var capturedParams service.ReopenIssueParams
	svc := &mockIssueService{
		reopenIssueFunc: func(_ context.Context, params service.ReopenIssueParams) error {
			capturedParams = params
			return nil
		},
	}
	h := HandleReopenIssue(svc)

	body := `{"reason":"broken again"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/x/reopen", strings.NewReader(body))
	req.SetPathValue("id", "x")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if capturedParams.Reason != "broken again" {
		t.Errorf("expected reason 'broken again', got %q", capturedParams.Reason)
	}
}

// TestHandleReopenIssue_MissingID returns 400.
func TestHandleReopenIssue_MissingID(t *testing.T) {
	svc := &mockIssueService{}
	h := HandleReopenIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues//reopen", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestHandleReopenIssue_NotFound surfaces 404 when the service reports the
// issue doesn't exist.
func TestHandleReopenIssue_NotFound(t *testing.T) {
	svc := &mockIssueService{
		reopenIssueFunc: func(_ context.Context, _ service.ReopenIssueParams) error {
			return service.ErrNotFound("issue not found")
		},
	}
	h := HandleReopenIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/ghost/reopen", nil)
	req.SetPathValue("id", "ghost")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestHandleReopenIssue_InvalidBody verifies 400 on malformed JSON.
func TestHandleReopenIssue_InvalidBody(t *testing.T) {
	svc := &mockIssueService{}
	h := HandleReopenIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/x/reopen", strings.NewReader(`not json`))
	req.SetPathValue("id", "x")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
