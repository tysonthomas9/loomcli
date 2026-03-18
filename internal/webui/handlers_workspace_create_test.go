package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleWorkspaceCreate_EmptyType_Success(t *testing.T) {
	createCalled := false
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		createCalled = true
		if req.Name != "my-ws" {
			t.Errorf("expected name %q, got %q", "my-ws", req.Name)
		}
		if req.Type != "empty" {
			t.Errorf("expected type %q, got %q", "empty", req.Type)
		}
		if len(req.Repos) != 1 || req.Repos[0] != "/home/user/repo" {
			t.Errorf("expected repos [/home/user/repo], got %v", req.Repos)
		}
		return nil
	}

	handler := handleWorkspaceCreate(createFn, mockWorkspaceConfigFn)

	body := strings.NewReader(`{"name":"my-ws","type":"empty","repos":["/home/user/repo"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if !createCalled {
		t.Error("expected createFn to be called")
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil when workspaceConfigFn provided")
	}
	if resp.Data.Name != "test-ws" {
		t.Errorf("expected data name %q, got %q", "test-ws", resp.Data.Name)
	}
}

func TestHandleWorkspaceCreate_CloneType_Success(t *testing.T) {
	createCalled := false
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		createCalled = true
		if req.Name != "cloned-ws" {
			t.Errorf("expected name %q, got %q", "cloned-ws", req.Name)
		}
		if req.Type != "clone" {
			t.Errorf("expected type %q, got %q", "clone", req.Type)
		}
		if req.CloneURL != "https://github.com/user/repo.git" {
			t.Errorf("expected clone_url %q, got %q", "https://github.com/user/repo.git", req.CloneURL)
		}
		if req.Branch != "main" {
			t.Errorf("expected branch %q, got %q", "main", req.Branch)
		}
		return nil
	}

	handler := handleWorkspaceCreate(createFn, mockWorkspaceConfigFn)

	body := strings.NewReader(`{"name":"cloned-ws","type":"clone","clone_url":"https://github.com/user/repo.git","branch":"main"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if !createCalled {
		t.Error("expected createFn to be called")
	}
}

func TestHandleWorkspaceCreate_CloneType_GitAtURL(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		if req.CloneURL != "git@github.com:user/repo.git" {
			t.Errorf("expected clone_url %q, got %q", "git@github.com:user/repo.git", req.CloneURL)
		}
		return nil
	}

	handler := handleWorkspaceCreate(createFn, nil)

	body := strings.NewReader(`{"name":"ssh-ws","type":"clone","clone_url":"git@github.com:user/repo.git"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceCreate_TemplateType_NotImplemented(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		t.Fatal("createFn should not be called for template type")
		return nil
	}

	handler := handleWorkspaceCreate(createFn, nil)

	body := strings.NewReader(`{"name":"tpl-ws","type":"template"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "not yet supported") {
		t.Errorf("expected 'not yet supported' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceCreate_NameValidation(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		t.Fatal("createFn should not be called for invalid name")
		return nil
	}

	handler := handleWorkspaceCreate(createFn, nil)

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantError  string
	}{
		{
			name:       "empty name",
			body:       `{"name":"","type":"empty","repos":["/a"]}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "name is required",
		},
		{
			name:       "name with spaces",
			body:       `{"name":"my workspace","type":"empty","repos":["/a"]}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "alphanumeric",
		},
		{
			name:       "name with dots",
			body:       `{"name":"my.ws","type":"empty","repos":["/a"]}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "alphanumeric",
		},
		{
			name:       "name with slashes",
			body:       `{"name":"my/ws","type":"empty","repos":["/a"]}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "alphanumeric",
		},
		{
			name:       "name too long",
			body:       `{"name":"` + strings.Repeat("a", 65) + `","type":"empty","repos":["/a"]}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "too long",
		},
		{
			name:       "name exactly 64 chars is valid",
			body:       `{"name":"` + strings.Repeat("a", 64) + `","type":"empty","repos":["/a"]}`,
			wantStatus: http.StatusCreated,
			wantError:  "",
		},
	}

	// Override createFn for the 64-char success case
	successCreateFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		return nil
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h http.HandlerFunc
			if tt.wantStatus == http.StatusCreated {
				h = handleWorkspaceCreate(successCreateFn, nil)
			} else {
				h = handler
			}

			req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}

			if tt.wantError != "" {
				var resp workspaceResponse
				json.NewDecoder(rec.Body).Decode(&resp)
				if resp.Success {
					t.Fatal("expected failure")
				}
				if !strings.Contains(resp.Error, tt.wantError) {
					t.Errorf("expected %q in error, got: %s", tt.wantError, resp.Error)
				}
			}
		})
	}
}

func TestHandleWorkspaceCreate_URLValidation(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		t.Fatal("createFn should not be called for invalid URL")
		return nil
	}

	handler := handleWorkspaceCreate(createFn, nil)

	tests := []struct {
		name     string
		cloneURL string
	}{
		{"http URL", "http://github.com/user/repo.git"},
		{"ftp URL", "ftp://example.com/repo.git"},
		{"bare path", "/home/user/repo"},
		{"relative path", "user/repo"},
		{"ssh without git@", "ssh://github.com/user/repo.git"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":"ws","type":"clone","clone_url":%q}`, tt.cloneURL)
			req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", strings.NewReader(body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}

			var resp workspaceResponse
			json.NewDecoder(rec.Body).Decode(&resp)
			if resp.Success {
				t.Fatal("expected failure")
			}
			if !strings.Contains(resp.Error, "clone_url must start with") {
				t.Errorf("expected URL validation error, got: %s", resp.Error)
			}
		})
	}
}

func TestHandleWorkspaceCreate_MissingRequiredFields(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		t.Fatal("createFn should not be called for missing required fields")
		return nil
	}

	handler := handleWorkspaceCreate(createFn, nil)

	tests := []struct {
		name      string
		body      string
		wantError string
	}{
		{
			name:      "empty type missing repos",
			body:      `{"name":"ws","type":"empty"}`,
			wantError: "repos is required",
		},
		{
			name:      "empty type empty repos",
			body:      `{"name":"ws","type":"empty","repos":[]}`,
			wantError: "repos is required",
		},
		{
			name:      "clone type missing clone_url",
			body:      `{"name":"ws","type":"clone"}`,
			wantError: "clone_url is required",
		},
		{
			name:      "clone type empty clone_url",
			body:      `{"name":"ws","type":"clone","clone_url":""}`,
			wantError: "clone_url is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}

			var resp workspaceResponse
			json.NewDecoder(rec.Body).Decode(&resp)
			if resp.Success {
				t.Fatal("expected failure")
			}
			if !strings.Contains(resp.Error, tt.wantError) {
				t.Errorf("expected %q in error, got: %s", tt.wantError, resp.Error)
			}
		})
	}
}

func TestHandleWorkspaceCreate_CreateFnError(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		return fmt.Errorf("disk full")
	}

	handler := handleWorkspaceCreate(createFn, nil)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure")
	}
	if resp.Error != "failed to create workspace" {
		t.Errorf("expected generic error message, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceCreate_NilCreateFn(t *testing.T) {
	handler := handleWorkspaceCreate(nil, nil)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure")
	}
	if resp.Error != "workspace creation is not available" {
		t.Errorf("expected %q, got: %s", "workspace creation is not available", resp.Error)
	}
}

func TestHandleWorkspaceCreate_RequestBodyTooLarge(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		t.Fatal("createFn should not be called for oversized body")
		return nil
	}

	handler := handleWorkspaceCreate(createFn, nil)

	// Create a JSON body larger than 1MB (maxRequestBody)
	largeBody := `{"name":"` + strings.Repeat("a", 1<<20+1) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", strings.NewReader(largeBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "request body too large") {
		t.Errorf("expected 'request body too large' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceCreate_InvalidJSON(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		t.Fatal("createFn should not be called for invalid JSON")
		return nil
	}

	handler := handleWorkspaceCreate(createFn, nil)

	body := strings.NewReader(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "invalid request body") {
		t.Errorf("expected 'invalid request body' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceCreate_MissingType(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		t.Fatal("createFn should not be called for missing type")
		return nil
	}

	handler := handleWorkspaceCreate(createFn, nil)

	body := strings.NewReader(`{"name":"ws"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "type is required") {
		t.Errorf("expected 'type is required' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceCreate_InvalidType(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		t.Fatal("createFn should not be called for invalid type")
		return nil
	}

	handler := handleWorkspaceCreate(createFn, nil)

	body := strings.NewReader(`{"name":"ws","type":"foobar"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "invalid type") {
		t.Errorf("expected 'invalid type' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceCreate_SuccessWithWorkspaceConfigFn(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		return nil
	}

	handler := handleWorkspaceCreate(createFn, mockWorkspaceConfigFn)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil when workspaceConfigFn provided")
	}
	if resp.Data.Name != "test-ws" {
		t.Errorf("expected data name %q, got %q", "test-ws", resp.Data.Name)
	}
}

func TestHandleWorkspaceCreate_SuccessNilWorkspaceConfigFn(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		return nil
	}

	handler := handleWorkspaceCreate(createFn, nil)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if resp.Data != nil {
		t.Error("expected Data to be nil when workspaceConfigFn is nil")
	}
}

func TestHandleWorkspaceCreate_ContextTimeout(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) error {
		// Simulate a timeout by canceling the context before returning
		// The handler wraps r.Context() with workspaceCreateTimeout,
		// so we simulate the createFn detecting a cancelled context.
		<-ctx.Done()
		return ctx.Err()
	}

	handler := handleWorkspaceCreate(createFn, nil)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/create", body)

	// Cancel the parent context to trigger the timeout path immediately.
	// The handler creates a child context with workspaceCreateTimeout from r.Context().
	// If we cancel the parent, the child context is also cancelled, and
	// ctx.Err() returns context.Canceled (not DeadlineExceeded).
	// To properly test the 504 path, we need the child deadline to expire.
	// We use a pre-cancelled context with a deadline to simulate this.
	ctx, cancel := context.WithTimeout(req.Context(), 0) // already expired
	defer cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "timed out") {
		t.Errorf("expected 'timed out' in error, got: %s", resp.Error)
	}
}

func TestValidateWorkspaceCreateRequest(t *testing.T) {
	tests := []struct {
		name       string
		req        WorkspaceCreateRequest
		wantStatus int
		wantError  string
	}{
		{
			name:       "valid empty type",
			req:        WorkspaceCreateRequest{Name: "ws", Type: "empty", Repos: []string{"/a"}},
			wantStatus: 0,
			wantError:  "",
		},
		{
			name:       "valid clone with https",
			req:        WorkspaceCreateRequest{Name: "ws", Type: "clone", CloneURL: "https://github.com/u/r.git"},
			wantStatus: 0,
			wantError:  "",
		},
		{
			name:       "valid clone with git@",
			req:        WorkspaceCreateRequest{Name: "ws", Type: "clone", CloneURL: "git@github.com:u/r.git"},
			wantStatus: 0,
			wantError:  "",
		},
		{
			name:       "empty name",
			req:        WorkspaceCreateRequest{Name: "", Type: "empty", Repos: []string{"/a"}},
			wantStatus: http.StatusBadRequest,
			wantError:  "name is required",
		},
		{
			name:       "name too long",
			req:        WorkspaceCreateRequest{Name: strings.Repeat("x", 65), Type: "empty", Repos: []string{"/a"}},
			wantStatus: http.StatusBadRequest,
			wantError:  "too long",
		},
		{
			name:       "invalid name chars",
			req:        WorkspaceCreateRequest{Name: "my ws!", Type: "empty", Repos: []string{"/a"}},
			wantStatus: http.StatusBadRequest,
			wantError:  "alphanumeric",
		},
		{
			name:       "empty type",
			req:        WorkspaceCreateRequest{Name: "ws", Type: ""},
			wantStatus: http.StatusBadRequest,
			wantError:  "type is required",
		},
		{
			name:       "invalid type",
			req:        WorkspaceCreateRequest{Name: "ws", Type: "invalid"},
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid type",
		},
		{
			name:       "template not implemented",
			req:        WorkspaceCreateRequest{Name: "ws", Type: "template"},
			wantStatus: http.StatusNotImplemented,
			wantError:  "not yet supported",
		},
		{
			name:       "empty type no repos",
			req:        WorkspaceCreateRequest{Name: "ws", Type: "empty"},
			wantStatus: http.StatusBadRequest,
			wantError:  "repos is required",
		},
		{
			name:       "clone type no url",
			req:        WorkspaceCreateRequest{Name: "ws", Type: "clone"},
			wantStatus: http.StatusBadRequest,
			wantError:  "clone_url is required",
		},
		{
			name:       "clone type invalid url",
			req:        WorkspaceCreateRequest{Name: "ws", Type: "clone", CloneURL: "http://example.com/repo"},
			wantStatus: http.StatusBadRequest,
			wantError:  "clone_url must start with",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, msg := validateWorkspaceCreateRequest(&tt.req)
			if status != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, status)
			}
			if tt.wantError == "" && msg != "" {
				t.Errorf("expected no error, got: %s", msg)
			}
			if tt.wantError != "" && !strings.Contains(msg, tt.wantError) {
				t.Errorf("expected %q in error, got: %s", tt.wantError, msg)
			}
		})
	}
}
