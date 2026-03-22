package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// ===========================================================================
// Mock types (W-suffixed to avoid collisions with handlers_test.go)
// ===========================================================================

// wMockPatchClient implements issueUpdater for testing.
type wMockPatchClient struct {
	updateFunc func(args *rpc.UpdateArgs) (*rpc.Response, error)
}

func (m *wMockPatchClient) Update(args *rpc.UpdateArgs) (*rpc.Response, error) {
	if m.updateFunc != nil {
		return m.updateFunc(args)
	}
	return nil, errors.New("updateFunc not implemented")
}

// wMockPatchPool implements patchConnectionGetter for testing.
type wMockPatchPool struct {
	getFunc func(ctx context.Context) (issueUpdater, error)
	putFunc func(client issueUpdater)
}

func (m *wMockPatchPool) Get(ctx context.Context) (issueUpdater, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *wMockPatchPool) Put(client issueUpdater) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

// wMockCreateClient implements issueCreator for testing.
type wMockCreateClient struct {
	createFunc func(args *rpc.CreateArgs) (*rpc.Response, error)
}

func (m *wMockCreateClient) Create(args *rpc.CreateArgs) (*rpc.Response, error) {
	if m.createFunc != nil {
		return m.createFunc(args)
	}
	return nil, errors.New("createFunc not implemented")
}

// wMockCreatePool implements createConnectionGetter for testing.
type wMockCreatePool struct {
	getFunc func(ctx context.Context) (issueCreator, error)
	putFunc func(client issueCreator)
}

func (m *wMockCreatePool) Get(ctx context.Context) (issueCreator, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *wMockCreatePool) Put(client issueCreator) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

// wMockCloseClient implements issueCloser for testing.
type wMockCloseClient struct {
	closeFunc func(args *rpc.CloseArgs) (*rpc.Response, error)
}

func (m *wMockCloseClient) CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error) {
	if m.closeFunc != nil {
		return m.closeFunc(args)
	}
	return nil, errors.New("closeFunc not implemented")
}

// wMockClosePool implements closeConnectionGetter for testing.
type wMockClosePool struct {
	getFunc func(ctx context.Context) (issueCloser, error)
	putFunc func(client issueCloser)
}

func (m *wMockClosePool) Get(ctx context.Context) (issueCloser, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *wMockClosePool) Put(client issueCloser) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

// wMockDeleteClient implements issueDeleter for testing.
type wMockDeleteClient struct {
	deleteFunc func(args *rpc.DeleteArgs) (*rpc.Response, error)
}

func (m *wMockDeleteClient) Delete(args *rpc.DeleteArgs) (*rpc.Response, error) {
	if m.deleteFunc != nil {
		return m.deleteFunc(args)
	}
	return nil, errors.New("deleteFunc not implemented")
}

// wMockDeletePool implements deleteConnectionGetter for testing.
type wMockDeletePool struct {
	getFunc func(ctx context.Context) (issueDeleter, error)
	putFunc func(client issueDeleter)
}

func (m *wMockDeletePool) Get(ctx context.Context) (issueDeleter, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *wMockDeletePool) Put(client issueDeleter) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

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
			client := &wMockPatchClient{
				updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
					if args.Status == nil || *args.Status != tt.wantStatus {
						t.Errorf("Update() Status = %v, want %q", args.Status, tt.wantStatus)
					}
					return &rpc.Response{Success: true}, nil
				},
			}
			pool := &wMockPatchPool{
				getFunc: func(ctx context.Context) (issueUpdater, error) {
					return client, nil
				},
				putFunc: func(c issueUpdater) {},
			}
			handler := handlePatchIssueWithPool(pool)

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
	client := &wMockPatchClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			if args.Title == nil || *args.Title != "New Title" {
				t.Errorf("Update() Title = %v, want %q", args.Title, "New Title")
			}
			if args.ID != "issue-42" {
				t.Errorf("Update() ID = %q, want %q", args.ID, "issue-42")
			}
			return &rpc.Response{Success: true}, nil
		},
	}
	pool := &wMockPatchPool{
		getFunc: func(ctx context.Context) (issueUpdater, error) { return client, nil },
		putFunc: func(c issueUpdater) {},
	}
	handler := handlePatchIssueWithPool(pool)

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

func TestHandlePatchIssueW_UpdatePriority(t *testing.T) {
	client := &wMockPatchClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			if args.Priority == nil || *args.Priority != 2 {
				t.Errorf("Update() Priority = %v, want 2", args.Priority)
			}
			return &rpc.Response{Success: true}, nil
		},
	}
	pool := &wMockPatchPool{
		getFunc: func(ctx context.Context) (issueUpdater, error) { return client, nil },
		putFunc: func(c issueUpdater) {},
	}
	handler := handlePatchIssueWithPool(pool)

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
	client := &wMockPatchClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			if args.Assignee == nil || *args.Assignee != "alice" {
				t.Errorf("Update() Assignee = %v, want %q", args.Assignee, "alice")
			}
			return &rpc.Response{Success: true}, nil
		},
	}
	pool := &wMockPatchPool{
		getFunc: func(ctx context.Context) (issueUpdater, error) { return client, nil },
		putFunc: func(c issueUpdater) {},
	}
	handler := handlePatchIssueWithPool(pool)

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
	// The handler passes status through to the daemon which rejects invalid values.
	client := &wMockPatchClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: false,
				Error:   "invalid status: bogus",
			}, nil
		},
	}
	pool := &wMockPatchPool{
		getFunc: func(ctx context.Context) (issueUpdater, error) { return client, nil },
		putFunc: func(c issueUpdater) {},
	}
	handler := handlePatchIssueWithPool(pool)

	body := `{"status":"bogus"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/x-1", strings.NewReader(body))
	req.SetPathValue("id", "x-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	result := assertJSONResponse(t, w)
	if result["success"] != false {
		t.Error("expected success=false")
	}
}

func TestHandlePatchIssueW_MissingIssueID(t *testing.T) {
	pool := &wMockPatchPool{
		getFunc: func(ctx context.Context) (issueUpdater, error) {
			return &wMockPatchClient{}, nil
		},
		putFunc: func(c issueUpdater) {},
	}
	handler := handlePatchIssueWithPool(pool)

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
	pool := &wMockPatchPool{
		getFunc: func(ctx context.Context) (issueUpdater, error) {
			return &wMockPatchClient{}, nil
		},
		putFunc: func(c issueUpdater) {},
	}
	handler := handlePatchIssueWithPool(pool)

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

func TestHandlePatchIssueW_NilPool(t *testing.T) {
	handler := handlePatchIssueWithPool(nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/np-1", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "np-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandlePatchIssueW_NotFound(t *testing.T) {
	client := &wMockPatchClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: false,
				Error:   "issue not found",
			}, nil
		},
	}
	pool := &wMockPatchPool{
		getFunc: func(ctx context.Context) (issueUpdater, error) { return client, nil },
		putFunc: func(c issueUpdater) {},
	}
	handler := handlePatchIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/missing", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "missing")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandlePatchIssueW_PoolGetError(t *testing.T) {
	pool := &wMockPatchPool{
		getFunc: func(ctx context.Context) (issueUpdater, error) {
			return nil, errors.New("pool exhausted")
		},
	}
	handler := handlePatchIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/pg-1", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "pg-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandlePatchIssueW_RPCError(t *testing.T) {
	client := &wMockPatchClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return nil, errors.New("connection reset")
		},
	}
	pool := &wMockPatchPool{
		getFunc: func(ctx context.Context) (issueUpdater, error) { return client, nil },
		putFunc: func(c issueUpdater) {},
	}
	handler := handlePatchIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/rpc-1", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "rpc-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandlePatchIssueW_RPCErrorNotFound(t *testing.T) {
	client := &wMockPatchClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return nil, errors.New("issue not found: abc")
		},
	}
	pool := &wMockPatchPool{
		getFunc: func(ctx context.Context) (issueUpdater, error) { return client, nil },
		putFunc: func(c issueUpdater) {},
	}
	handler := handlePatchIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandlePatchIssueW_CannotUpdateTemplate(t *testing.T) {
	client := &wMockPatchClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: false,
				Error:   "cannot update template issue",
			}, nil
		},
	}
	pool := &wMockPatchPool{
		getFunc: func(ctx context.Context) (issueUpdater, error) { return client, nil },
		putFunc: func(c issueUpdater) {},
	}
	handler := handlePatchIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/tmpl-1", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "tmpl-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
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
			client := &wMockCreateClient{
				createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
					if args.Title != "Test Issue" {
						t.Errorf("Create() Title = %q, want %q", args.Title, "Test Issue")
					}
					if args.IssueType != tt.issueType {
						t.Errorf("Create() IssueType = %q, want %q", args.IssueType, tt.issueType)
					}
					return &rpc.Response{Success: true, Data: expectedData}, nil
				},
			}
			pool := &wMockCreatePool{
				getFunc: func(ctx context.Context) (issueCreator, error) { return client, nil },
				putFunc: func(c issueCreator) {},
			}
			handler := handleCreateIssueWithPool(pool)

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
	client := &wMockCreateClient{
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			if args.Parent != "epic-42" {
				t.Errorf("Create() Parent = %q, want %q", args.Parent, "epic-42")
			}
			if args.Title != "Child Task" {
				t.Errorf("Create() Title = %q, want %q", args.Title, "Child Task")
			}
			return &rpc.Response{
				Success: true,
				Data:    []byte(`{"id":"child-1","title":"Child Task","parent":"epic-42"}`),
			}, nil
		},
	}
	pool := &wMockCreatePool{
		getFunc: func(ctx context.Context) (issueCreator, error) { return client, nil },
		putFunc: func(c issueCreator) {},
	}
	handler := handleCreateIssueWithPool(pool)

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

func TestHandleCreateIssueW_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantContain string
	}{
		{
			name:        "missing title",
			body:        `{"issue_type":"bug","priority":1}`,
			wantContain: "title is required",
		},
		{
			name:        "empty title",
			body:        `{"title":"   ","issue_type":"bug","priority":1}`,
			wantContain: "title is required",
		},
		{
			name:        "missing issue_type",
			body:        `{"title":"Test","priority":1}`,
			wantContain: "issue_type is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Validation happens before pool access, so use a real pool.
			pool, err := daemon.NewConnectionPool("/tmp/test-create-w.sock", 1)
			if err != nil {
				t.Fatalf("NewConnectionPool() error = %v", err)
			}
			defer pool.Close()

			handler := handleCreateIssue(pool)

			req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
			var resp IssuesResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if resp.Success {
				t.Error("expected success=false")
			}
			if !strings.Contains(resp.Error, tt.wantContain) {
				t.Errorf("error = %q, want to contain %q", resp.Error, tt.wantContain)
			}
		})
	}
}

func TestHandleCreateIssueW_InvalidType(t *testing.T) {
	pool, err := daemon.NewConnectionPool("/tmp/test-create-type-w.sock", 1)
	if err != nil {
		t.Fatalf("NewConnectionPool() error = %v", err)
	}
	defer pool.Close()

	handler := handleCreateIssue(pool)

	body := `{"title":"Test","issue_type":"invalid_type","priority":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var resp IssuesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if !strings.Contains(resp.Error, "invalid issue_type") {
		t.Errorf("error = %q, want to contain 'invalid issue_type'", resp.Error)
	}
}

func TestHandleCreateIssueW_NilPoolReturns503(t *testing.T) {
	handler := handleCreateIssue(nil)

	body := `{"title":"Test","issue_type":"bug","priority":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleCreateIssueW_PoolGetError(t *testing.T) {
	pool := &wMockCreatePool{
		getFunc: func(ctx context.Context) (issueCreator, error) {
			return nil, errors.New("pool exhausted")
		},
	}
	handler := handleCreateIssueWithPool(pool)

	body := `{"title":"Test","issue_type":"bug","priority":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleCreateIssueW_RPCError(t *testing.T) {
	client := &wMockCreateClient{
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return nil, errors.New("connection reset")
		},
	}
	pool := &wMockCreatePool{
		getFunc: func(ctx context.Context) (issueCreator, error) { return client, nil },
		putFunc: func(c issueCreator) {},
	}
	handler := handleCreateIssueWithPool(pool)

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
	client := &wMockCreateClient{
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: false,
				Error:   "duplicate issue ID",
			}, nil
		},
	}
	pool := &wMockCreatePool{
		getFunc: func(ctx context.Context) (issueCreator, error) { return client, nil },
		putFunc: func(c issueCreator) {},
	}
	handler := handleCreateIssueWithPool(pool)

	body := `{"title":"Test","issue_type":"bug","priority":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	var resp IssuesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if resp.Error != "duplicate issue ID" {
		t.Errorf("error = %q, want %q", resp.Error, "duplicate issue ID")
	}
}

func TestHandleCreateIssueW_EmptyBody(t *testing.T) {
	pool, err := daemon.NewConnectionPool("/tmp/test-create-empty-w.sock", 1)
	if err != nil {
		t.Fatalf("NewConnectionPool() error = %v", err)
	}
	defer pool.Close()

	handler := handleCreateIssue(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateIssueW_ClientReturnedToPool(t *testing.T) {
	putCalled := false
	client := &wMockCreateClient{
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: []byte(`{"id":"t-1"}`)}, nil
		},
	}
	pool := &wMockCreatePool{
		getFunc: func(ctx context.Context) (issueCreator, error) { return client, nil },
		putFunc: func(c issueCreator) { putCalled = true },
	}
	handler := handleCreateIssueWithPool(pool)

	body := `{"title":"Test","issue_type":"bug","priority":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !putCalled {
		t.Error("Put() was not called - client not returned to pool")
	}
}

// ===========================================================================
// handleCloseIssue tests
// ===========================================================================

func TestHandleCloseIssueW_WithReason(t *testing.T) {
	client := &wMockCloseClient{
		closeFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			if args.ID != "close-1" {
				t.Errorf("CloseIssue() ID = %q, want %q", args.ID, "close-1")
			}
			if args.Reason != "completed" {
				t.Errorf("CloseIssue() Reason = %q, want %q", args.Reason, "completed")
			}
			return &rpc.Response{
				Success: true,
				Data:    []byte(`{"id":"close-1","status":"closed"}`),
			}, nil
		},
	}
	pool := &wMockClosePool{
		getFunc: func(ctx context.Context) (issueCloser, error) { return client, nil },
		putFunc: func(c issueCloser) {},
	}
	handler := handleCloseIssueWithPool(pool)

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
	client := &wMockCloseClient{
		closeFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			if args.Reason != "" {
				t.Errorf("CloseIssue() Reason = %q, want empty", args.Reason)
			}
			return &rpc.Response{
				Success: true,
				Data:    []byte(`{"id":"close-2","status":"closed"}`),
			}, nil
		},
	}
	pool := &wMockClosePool{
		getFunc: func(ctx context.Context) (issueCloser, error) { return client, nil },
		putFunc: func(c issueCloser) {},
	}
	handler := handleCloseIssueWithPool(pool)

	// No body / content-length 0
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
	client := &wMockCloseClient{
		closeFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return nil, errors.New("issue not found: ghost-1")
		},
	}
	pool := &wMockClosePool{
		getFunc: func(ctx context.Context) (issueCloser, error) { return client, nil },
		putFunc: func(c issueCloser) {},
	}
	handler := handleCloseIssueWithPool(pool)

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
	pool := &wMockClosePool{
		getFunc: func(ctx context.Context) (issueCloser, error) {
			return &wMockCloseClient{}, nil
		},
		putFunc: func(c issueCloser) {},
	}
	handler := handleCloseIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/issues//close", nil)
	req.SetPathValue("id", "")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCloseIssueW_NilPool(t *testing.T) {
	handler := handleCloseIssueWithPool(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/nil-1/close", nil)
	req.SetPathValue("id", "nil-1")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleCloseIssueW_PoolGetError(t *testing.T) {
	pool := &wMockClosePool{
		getFunc: func(ctx context.Context) (issueCloser, error) {
			return nil, errors.New("pool exhausted")
		},
	}
	handler := handleCloseIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/pge-1/close", nil)
	req.SetPathValue("id", "pge-1")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleCloseIssueW_RPCError(t *testing.T) {
	client := &wMockCloseClient{
		closeFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return nil, errors.New("connection reset")
		},
	}
	pool := &wMockClosePool{
		getFunc: func(ctx context.Context) (issueCloser, error) { return client, nil },
		putFunc: func(c issueCloser) {},
	}
	handler := handleCloseIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/rpc-close/close", nil)
	req.SetPathValue("id", "rpc-close")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleCloseIssueW_DaemonError(t *testing.T) {
	client := &wMockCloseClient{
		closeFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: false,
				Error:   "issue already closed",
			}, nil
		},
	}
	pool := &wMockClosePool{
		getFunc: func(ctx context.Context) (issueCloser, error) { return client, nil },
		putFunc: func(c issueCloser) {},
	}
	handler := handleCloseIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/dup-close/close", nil)
	req.SetPathValue("id", "dup-close")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleCloseIssueW_BlockerConflict(t *testing.T) {
	client := &wMockCloseClient{
		closeFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return nil, errors.New("has open blocker dependencies")
		},
	}
	pool := &wMockClosePool{
		getFunc: func(ctx context.Context) (issueCloser, error) { return client, nil },
		putFunc: func(c issueCloser) {},
	}
	handler := handleCloseIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/blocked-1/close", nil)
	req.SetPathValue("id", "blocked-1")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestHandleCloseIssueW_ClientReturnedToPool(t *testing.T) {
	putCalled := false
	client := &wMockCloseClient{
		closeFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: []byte(`{}`)}, nil
		},
	}
	pool := &wMockClosePool{
		getFunc: func(ctx context.Context) (issueCloser, error) { return client, nil },
		putFunc: func(c issueCloser) { putCalled = true },
	}
	handler := handleCloseIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/pool-ret/close", nil)
	req.SetPathValue("id", "pool-ret")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !putCalled {
		t.Error("Put() was not called - client not returned to pool")
	}
}

// ===========================================================================
// handleDeleteIssue tests
// ===========================================================================

func TestHandleDeleteIssueW_NilPool(t *testing.T) {
	handler := handleDeleteIssue(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/del-1", nil)
	req.SetPathValue("id", "del-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	result := assertJSONResponse(t, w)
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "connection pool not initialized") {
		t.Errorf("error = %q, want to contain 'connection pool not initialized'", errMsg)
	}
}

func TestHandleDeleteIssueW_MissingID(t *testing.T) {
	handler := handleDeleteIssue(nil)

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
	client := &wMockDeleteClient{
		deleteFunc: func(args *rpc.DeleteArgs) (*rpc.Response, error) {
			if len(args.IDs) != 1 || args.IDs[0] != "del-ok" {
				t.Errorf("Delete() IDs = %v, want [del-ok]", args.IDs)
			}
			if !args.Force {
				t.Error("Delete() Force = false, want true")
			}
			return &rpc.Response{
				Success: true,
				Data:    []byte(`{"id":"del-ok","deleted":true}`),
			}, nil
		},
	}
	pool := &wMockDeletePool{
		getFunc: func(ctx context.Context) (issueDeleter, error) { return client, nil },
		putFunc: func(c issueDeleter) {},
	}
	handler := handleDeleteIssueWithPool(pool)

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

func TestHandleDeleteIssueW_RPCError(t *testing.T) {
	client := &wMockDeleteClient{
		deleteFunc: func(args *rpc.DeleteArgs) (*rpc.Response, error) {
			return nil, errors.New("connection reset")
		},
	}
	pool := &wMockDeletePool{
		getFunc: func(ctx context.Context) (issueDeleter, error) { return client, nil },
		putFunc: func(c issueDeleter) {},
	}
	handler := handleDeleteIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/rpc-del", nil)
	req.SetPathValue("id", "rpc-del")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	result := assertJSONResponse(t, w)
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "internal server error") {
		t.Errorf("error = %q, want to contain 'internal server error'", errMsg)
	}
}

func TestHandleDeleteIssueW_DaemonReturnsFalse(t *testing.T) {
	client := &wMockDeleteClient{
		deleteFunc: func(args *rpc.DeleteArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: false,
				Error:   "cannot delete: issue has children",
			}, nil
		},
	}
	pool := &wMockDeletePool{
		getFunc: func(ctx context.Context) (issueDeleter, error) { return client, nil },
		putFunc: func(c issueDeleter) {},
	}
	handler := handleDeleteIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/fail-del", nil)
	req.SetPathValue("id", "fail-del")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	result := assertJSONResponse(t, w)
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "cannot delete: issue has children") {
		t.Errorf("error = %q, want to contain 'cannot delete: issue has children'", errMsg)
	}
}

func TestHandleDeleteIssueW_NotFound(t *testing.T) {
	client := &wMockDeleteClient{
		deleteFunc: func(args *rpc.DeleteArgs) (*rpc.Response, error) {
			return nil, errors.New("issue not found: ghost-del")
		},
	}
	pool := &wMockDeletePool{
		getFunc: func(ctx context.Context) (issueDeleter, error) { return client, nil },
		putFunc: func(c issueDeleter) {},
	}
	handler := handleDeleteIssueWithPool(pool)

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

func TestHandleDeleteIssueW_PoolGetError(t *testing.T) {
	pool := &wMockDeletePool{
		getFunc: func(ctx context.Context) (issueDeleter, error) {
			return nil, errors.New("pool exhausted")
		},
	}
	handler := handleDeleteIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/pge-del", nil)
	req.SetPathValue("id", "pge-del")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	result := assertJSONResponse(t, w)
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "daemon not available") {
		t.Errorf("error = %q, want to contain 'daemon not available'", errMsg)
	}
}

func TestHandleDeleteIssueW_PoolGetTimeout(t *testing.T) {
	pool := &wMockDeletePool{
		getFunc: func(ctx context.Context) (issueDeleter, error) {
			return nil, context.DeadlineExceeded
		},
	}
	handler := handleDeleteIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/timeout-del", nil)
	req.SetPathValue("id", "timeout-del")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d", w.Code, http.StatusGatewayTimeout)
	}
}

func TestHandleDeleteIssueW_ClientReturnedToPool(t *testing.T) {
	putCalled := false
	client := &wMockDeleteClient{
		deleteFunc: func(args *rpc.DeleteArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: []byte(`{}`)}, nil
		},
	}
	pool := &wMockDeletePool{
		getFunc: func(ctx context.Context) (issueDeleter, error) { return client, nil },
		putFunc: func(c issueDeleter) { putCalled = true },
	}
	handler := handleDeleteIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/pool-ret-del", nil)
	req.SetPathValue("id", "pool-ret-del")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !putCalled {
		t.Error("Put() was not called - client not returned to pool")
	}
}

func TestHandleDeleteIssueW_ClientReturnedToPoolOnError(t *testing.T) {
	putCalled := false
	client := &wMockDeleteClient{
		deleteFunc: func(args *rpc.DeleteArgs) (*rpc.Response, error) {
			return nil, errors.New("rpc failure")
		},
	}
	pool := &wMockDeletePool{
		getFunc: func(ctx context.Context) (issueDeleter, error) { return client, nil },
		putFunc: func(c issueDeleter) { putCalled = true },
	}
	handler := handleDeleteIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/pool-ret-err", nil)
	req.SetPathValue("id", "pool-ret-err")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !putCalled {
		t.Error("Put() was not called on error path - client not returned to pool")
	}
}
