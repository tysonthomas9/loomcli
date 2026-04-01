package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

func TestHandleWorkspaceCreate_EmptyType_Success(t *testing.T) {
	createCalled := false
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
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
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, mockWorkspaceConfigFn, nil)

	body := strings.NewReader(`{"name":"my-ws","type":"empty","repos":["/home/user/repo"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
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
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, mockWorkspaceConfigFn, nil)

	body := strings.NewReader(`{"name":"cloned-ws","type":"clone","clone_url":"https://github.com/user/repo.git","branch":"main"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		if req.CloneURL != "git@github.com:user/repo.git" {
			t.Errorf("expected clone_url %q, got %q", "git@github.com:user/repo.git", req.CloneURL)
		}
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

	body := strings.NewReader(`{"name":"ssh-ws","type":"clone","clone_url":"git@github.com:user/repo.git"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceCreate_TemplateType_NotImplemented(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		t.Fatal("createFn should not be called for template type")
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

	body := strings.NewReader(`{"name":"tpl-ws","type":"template"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		t.Fatal("createFn should not be called for invalid name")
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

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
	successCreateFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		return WorkspaceCreateResult{}, nil
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h http.HandlerFunc
			if tt.wantStatus == http.StatusCreated {
				h = handleWorkspaceCreate(successCreateFn, nil, nil)
			} else {
				h = handler
			}

			req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(tt.body))
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		t.Fatal("createFn should not be called for invalid URL")
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

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
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(body))
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
			if !strings.Contains(resp.Error, "clone URL must start with") {
				t.Errorf("expected URL validation error, got: %s", resp.Error)
			}
		})
	}
}

func TestHandleWorkspaceCreate_MissingRequiredFields(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		t.Fatal("createFn should not be called for missing required fields")
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

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
			wantError: "at least one clone URL is required",
		},
		{
			name:      "clone type empty clone_url",
			body:      `{"name":"ws","type":"clone","clone_url":""}`,
			wantError: "at least one clone URL is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(tt.body))
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		return WorkspaceCreateResult{}, fmt.Errorf("disk full")
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
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
	handler := handleWorkspaceCreate(nil, nil, nil)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		t.Fatal("createFn should not be called for oversized body")
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

	// Create a JSON body larger than 1MB (maxRequestBody)
	largeBody := `{"name":"` + strings.Repeat("a", 1<<20+1) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(largeBody))
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		t.Fatal("createFn should not be called for invalid JSON")
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

	body := strings.NewReader(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		t.Fatal("createFn should not be called for missing type")
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

	body := strings.NewReader(`{"name":"ws"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		t.Fatal("createFn should not be called for invalid type")
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

	body := strings.NewReader(`{"name":"ws","type":"foobar"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, mockWorkspaceConfigFn, nil)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
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
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		// Simulate a timeout by canceling the context before returning
		// The handler wraps r.Context() with workspaceCreateTimeout,
		// so we simulate the createFn detecting a cancelled context.
		<-ctx.Done()
		return WorkspaceCreateResult{}, ctx.Err()
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)

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

func TestHandleWorkspaceCreate_SuccessWithWarnings(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		AddCreateWarning(ctx, "test warning")
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, mockWorkspaceConfigFn, nil)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
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
	if len(resp.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(resp.Warnings), resp.Warnings)
	}
	if resp.Warnings[0] != "test warning" {
		t.Errorf("expected warning %q, got %q", "test warning", resp.Warnings[0])
	}
}

func TestHandleWorkspaceCreate_SuccessNoWarnings(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		// No warnings added
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, mockWorkspaceConfigFn, nil)

	body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Parse the raw JSON to verify warnings field is absent (omitempty)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw response: %v", err)
	}
	if _, exists := raw["warnings"]; exists {
		t.Error("expected warnings field to be absent (omitempty), but it was present")
	}

	// Also verify the typed response
	var resp workspaceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if resp.Warnings != nil {
		t.Errorf("expected nil warnings, got %v", resp.Warnings)
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
			wantError:  "at least one clone URL is required",
		},
		{
			name:       "clone type invalid url",
			req:        WorkspaceCreateRequest{Name: "ws", Type: "clone", CloneURL: "http://example.com/repo"},
			wantStatus: http.StatusBadRequest,
			wantError:  "clone URL must start with",
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

func TestClassifyWorkspaceCreateError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "AlreadyExists returns 409",
			err:        workspaceerrors.New(workspaceerrors.AlreadyExists, "workspace already exists at /tmp/ws", nil),
			wantStatus: http.StatusConflict,
			wantMsg:    "workspace already exists at /tmp/ws",
		},
		{
			name:       "PathNotFound returns 422",
			err:        workspaceerrors.New(workspaceerrors.PathNotFound, "parent directory does not exist", nil),
			wantStatus: http.StatusUnprocessableEntity,
			wantMsg:    "parent directory does not exist",
		},
		{
			name:       "NotGitRepo returns 422",
			err:        workspaceerrors.New(workspaceerrors.NotGitRepo, "not a git repository", nil),
			wantStatus: http.StatusUnprocessableEntity,
			wantMsg:    "not a git repository",
		},
		{
			name:       "GitFailed returns 422",
			err:        workspaceerrors.New(workspaceerrors.GitFailed, "git clone failed", fmt.Errorf("exit status 128")),
			wantStatus: http.StatusUnprocessableEntity,
			wantMsg:    "git clone failed",
		},
		{
			name:       "SecurityViolation returns 403",
			err:        workspaceerrors.New(workspaceerrors.SecurityViolation, "path traversal detected", nil),
			wantStatus: http.StatusForbidden,
			wantMsg:    "path traversal detected",
		},
		{
			name:       "ConfigFailed returns 500 with specific message",
			err:        workspaceerrors.New(workspaceerrors.ConfigFailed, "failed to write config", fmt.Errorf("permission denied")),
			wantStatus: http.StatusInternalServerError,
			wantMsg:    "failed to write config",
		},
		{
			name:       "unknown error returns 500 generic",
			err:        fmt.Errorf("disk full"),
			wantStatus: http.StatusInternalServerError,
			wantMsg:    "failed to create workspace",
		},
		{
			name:       "wrapped CreateError unwraps correctly",
			err:        fmt.Errorf("outer: %w", workspaceerrors.New(workspaceerrors.AlreadyExists, "workspace exists", nil)),
			wantStatus: http.StatusConflict,
			wantMsg:    "workspace exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, msg := classifyWorkspaceCreateError(tt.err)
			if status != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, status)
			}
			if msg != tt.wantMsg {
				t.Errorf("expected message %q, got %q", tt.wantMsg, msg)
			}
		})
	}
}

func TestIsBlockedCloneHost(t *testing.T) {
	tests := []struct {
		host    string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"localhost", true},
		{"LOCALHOST", true},
		{"localhost.localdomain", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"169.254.169.254", true},
		{"0.0.0.0", true},
		{"::", true},
		{"metadata.google.internal", true},
		{"metadata.internal", true},
		{"fe80::1", true},
		{"fd00::1", true},
		// Trailing dot (FQDN)
		{"localhost.", true},
		// With port
		{"127.0.0.1:8080", true},
		{"localhost:3000", true},
		// CGNAT (RFC 6598)
		{"100.64.0.1", true},
		{"100.127.255.254", true},
		// Allowed
		{"100.63.255.255", false},
		{"100.128.0.0", false},
		{"github.com", false},
		{"203.0.113.1", false},
		{"172.32.0.1", false},
		{"8.8.8.8", false},
		{"gitlab.com", false},
		{"git.example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := isBlockedCloneHost(tt.host)
			if got != tt.blocked {
				t.Errorf("isBlockedCloneHost(%q) = %v, want %v", tt.host, got, tt.blocked)
			}
		})
	}
}

func TestExtractCloneHost(t *testing.T) {
	tests := []struct {
		url      string
		wantHost string
		wantErr  bool
	}{
		{"https://github.com/user/repo.git", "github.com", false},
		{"https://10.0.0.1/repo.git", "10.0.0.1", false},
		{"https://[::1]/repo.git", "::1", false},
		{"https://git.example.com:8443/repo.git", "git.example.com", false},
		{"git@github.com:user/repo.git", "github.com", false},
		{"git@10.0.0.1:repo.git", "10.0.0.1", false},
		{"git@[::1]:repo.git", "::1", false},
		// Error cases
		{"https:///repo.git", "", true},
		{"git@:repo.git", "", true},
		{"ftp://example.com/repo", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			host, err := extractCloneHost(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("extractCloneHost(%q) = %q, nil; want error", tt.url, host)
				}
				return
			}
			if err != nil {
				t.Errorf("extractCloneHost(%q) error: %v", tt.url, err)
				return
			}
			if host != tt.wantHost {
				t.Errorf("extractCloneHost(%q) = %q, want %q", tt.url, host, tt.wantHost)
			}
		})
	}
}

func TestValidateCloneURL_SSRFBlocked(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"IPv4 loopback", "https://127.0.0.1/repo.git"},
		{"IPv4 loopback alt", "https://127.0.0.2/repo.git"},
		{"IPv6 loopback", "https://[::1]/repo.git"},
		{"localhost", "https://localhost/repo.git"},
		{"localhost.localdomain", "https://localhost.localdomain/repo.git"},
		{"Private 10.x", "https://10.0.0.1/repo.git"},
		{"Private 172.16.x", "https://172.16.0.1/repo.git"},
		{"Private 192.168.x", "https://192.168.1.1/repo.git"},
		{"Link-local / cloud metadata", "https://169.254.169.254/repo.git"},
		{"Metadata hostname", "https://metadata.google.internal/repo.git"},
		{"metadata.internal", "https://metadata.internal/repo.git"},
		{"Unspecified 0.0.0.0", "https://0.0.0.0/repo.git"},
		{"git@ loopback", "git@127.0.0.1:repo.git"},
		{"git@ localhost", "git@localhost:repo.git"},
		{"git@ private", "git@10.0.0.1:repo.git"},
		{"Loopback with port", "https://127.0.0.1:8080/repo.git"},
		{"localhost with port", "https://localhost:3000/repo.git"},
		{"Uppercase LOCALHOST", "https://LOCALHOST/repo.git"},
		{"CGNAT range", "https://100.64.0.1/repo.git"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCloneURL(tt.url)
			if err == nil {
				t.Fatalf("validateCloneURL(%q) = nil, want error containing 'blocked host'", tt.url)
			}
			if !strings.Contains(err.Error(), "blocked host") {
				t.Errorf("validateCloneURL(%q) error = %q, want 'blocked host'", tt.url, err.Error())
			}
		})
	}
}

func TestValidateCloneURL_SSRFAllowed(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"GitHub HTTPS", "https://github.com/user/repo.git"},
		{"GitLab HTTPS", "https://gitlab.com/user/repo.git"},
		{"Custom domain", "https://git.example.com/repo.git"},
		{"GitHub SSH", "git@github.com:user/repo.git"},
		{"GitLab SSH", "git@gitlab.com:user/repo.git"},
		{"Public IP", "https://203.0.113.1/repo.git"},
		{"URL with port", "https://git.example.com:443/repo.git"},
		{"Just outside private 172.32", "https://172.32.0.1/repo.git"},
		{"URL with userinfo", "https://user:pass@github.com/repo.git"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCloneURL(tt.url)
			if err != nil {
				t.Errorf("validateCloneURL(%q) = %v, want nil", tt.url, err)
			}
		})
	}
}

func TestHandleWorkspaceCreate_SSRFBlocked(t *testing.T) {
	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		t.Fatal("createFn should not be called for SSRF-blocked URLs")
		return WorkspaceCreateResult{}, nil
	}

	handler := handleWorkspaceCreate(createFn, nil, nil)

	body := strings.NewReader(`{"name":"ws","type":"clone","clone_url":"https://127.0.0.1/repo.git"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "blocked host") {
		t.Errorf("expected 'blocked host' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceCreate_ClassifiedErrors(t *testing.T) {
	tests := []struct {
		name       string
		createErr  error
		wantStatus int
		wantError  string
	}{
		{
			name:       "AlreadyExists returns 409",
			createErr:  workspaceerrors.New(workspaceerrors.AlreadyExists, "workspace already exists", nil),
			wantStatus: http.StatusConflict,
			wantError:  "workspace already exists",
		},
		{
			name:       "PathNotFound returns 422",
			createErr:  workspaceerrors.New(workspaceerrors.PathNotFound, "parent dir missing", nil),
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "parent dir missing",
		},
		{
			name:       "SecurityViolation returns 403",
			createErr:  workspaceerrors.New(workspaceerrors.SecurityViolation, "path escapes root", nil),
			wantStatus: http.StatusForbidden,
			wantError:  "path escapes root",
		},
		{
			name:       "ConfigFailed returns 500 with message",
			createErr:  workspaceerrors.New(workspaceerrors.ConfigFailed, "config write error", nil),
			wantStatus: http.StatusInternalServerError,
			wantError:  "config write error",
		},
		{
			name:       "unknown error returns 500 generic",
			createErr:  fmt.Errorf("unexpected failure"),
			wantStatus: http.StatusInternalServerError,
			wantError:  "failed to create workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
				return WorkspaceCreateResult{}, tt.createErr
			}
			handler := handleWorkspaceCreate(createFn, nil, nil)

			body := strings.NewReader(`{"name":"ws","type":"empty","repos":["/a"]}`)
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}

			var resp workspaceResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Success {
				t.Fatal("expected failure")
			}
			if resp.Error != tt.wantError {
				t.Errorf("expected error %q, got %q", tt.wantError, resp.Error)
			}
		})
	}
}
