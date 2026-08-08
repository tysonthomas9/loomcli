package executionmanagement

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type workerProfileAPIStub struct {
	creates int
	updates int
	deletes int
	err     error
	create  execution.CreateWorkerProfileCommand
	update  execution.UpdateWorkerProfileCommand
	remove  execution.DeleteWorkerProfileCommand
}

func (stub *workerProfileAPIStub) GetWorkerProfile(context.Context, string, string) (*execution.WorkerProfile, error) {
	return nil, stub.err
}

func (stub *workerProfileAPIStub) ListWorkerProfiles(context.Context, string, execution.WorkerProfileFilter) ([]*execution.WorkerProfile, error) {
	return nil, stub.err
}

func (stub *workerProfileAPIStub) CreateWorkerProfile(_ context.Context, _ authority.OperatorAuthority, command execution.CreateWorkerProfileCommand) (*execution.WorkerProfile, error) {
	stub.creates++
	stub.create = command
	if stub.err != nil {
		return nil, stub.err
	}
	now := time.Now().UTC()
	return &execution.WorkerProfile{WorkspaceKey: command.WorkspaceKey, ProfileID: command.ProfileID, Role: command.Role, Enabled: true, CreatedAt: now, UpdatedAt: now}, nil
}

func (stub *workerProfileAPIStub) UpdateWorkerProfile(_ context.Context, _ authority.OperatorAuthority, command execution.UpdateWorkerProfileCommand) (*execution.WorkerProfile, error) {
	stub.updates++
	stub.update = command
	if stub.err != nil {
		return nil, stub.err
	}
	now := time.Now().UTC()
	return &execution.WorkerProfile{WorkspaceKey: command.WorkspaceKey, ProfileID: command.ProfileID, Role: "task", Enabled: true, CreatedAt: now, UpdatedAt: now}, nil
}

func (stub *workerProfileAPIStub) DeleteWorkerProfile(_ context.Context, _ authority.OperatorAuthority, command execution.DeleteWorkerProfileCommand) (execution.DeleteWorkerProfileResult, error) {
	stub.deletes++
	stub.remove = command
	if stub.err != nil {
		return execution.DeleteWorkerProfileResult{}, stub.err
	}
	return execution.DeleteWorkerProfileResult{WorkspaceKey: command.WorkspaceKey, ProfileID: command.ProfileID}, nil
}

type workerProfileResolverStub struct {
	err       error
	workspace string
	action    authority.Action
}

func (stub *workerProfileResolverStub) ResolveOperatorAuthority(_ *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
	stub.workspace = workspace
	stub.action = action
	return authority.OperatorAuthority{}, stub.err
}

func TestWorkerProfileCreateRouteStampsWorkspaceAndAction(t *testing.T) {
	api := &workerProfileAPIStub{}
	resolver := &workerProfileResolverStub{}
	mux := http.NewServeMux()
	New(Config{WorkerProfiles: api, Authority: resolver}).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/execution/worker-profiles", strings.NewReader(`{"profile_id":"falcon","role":"task"}`))
	req = withCanonicalWorkspace(req, "WS", "WS")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
	}
	if api.creates != 1 || api.create.WorkspaceKey != "WS" || api.create.RequestID != "worker-profile-create:falcon" {
		t.Fatalf("create command = %+v calls=%d", api.create, api.creates)
	}
	if resolver.workspace != "WS" || resolver.action != execution.ActionCreateWorkerProfile {
		t.Fatalf("resolver scope = %q/%q", resolver.workspace, resolver.action)
	}
}

func TestWorkerProfileUpdateAndDeleteRoutesStampExactActionAndRequestIdentity(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantAction authority.Action
		assert     func(*testing.T, *workerProfileAPIStub)
	}{
		{
			name: "update", method: http.MethodPatch, path: "/api/workspaces/WS/execution/worker-profiles/falcon",
			body: `{"backend":"codex"}`, wantStatus: http.StatusOK, wantAction: execution.ActionUpdateWorkerProfile,
			assert: func(t *testing.T, api *workerProfileAPIStub) {
				t.Helper()
				if api.updates != 1 || api.update.WorkspaceKey != "WS" || api.update.ProfileID != "falcon" || api.update.RequestID != "worker-profile-update:falcon" {
					t.Fatalf("update command/calls = %+v/%d", api.update, api.updates)
				}
			},
		},
		{
			name: "delete", method: http.MethodDelete, path: "/api/workspaces/WS/execution/worker-profiles/falcon",
			wantStatus: http.StatusNoContent, wantAction: execution.ActionDeleteWorkerProfile,
			assert: func(t *testing.T, api *workerProfileAPIStub) {
				t.Helper()
				if api.deletes != 1 || api.remove.WorkspaceKey != "WS" || api.remove.ProfileID != "falcon" || api.remove.RequestID != "worker-profile-delete:falcon" {
					t.Fatalf("delete command/calls = %+v/%d", api.remove, api.deletes)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := &workerProfileAPIStub{}
			resolver := &workerProfileResolverStub{}
			mux := http.NewServeMux()
			New(Config{WorkerProfiles: api, Authority: resolver}).Register(mux)
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			req = withCanonicalWorkspace(req, "WS", "WS")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
			}
			if resolver.workspace != "WS" || resolver.action != test.wantAction {
				t.Fatalf("resolver scope = %q/%q", resolver.workspace, resolver.action)
			}
			test.assert(t, api)
		})
	}
}

func TestWorkerProfileRoutesRejectWrongWorkspaceAndDeniedAuthority(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "wrong workspace", err: authority.ErrWorkspaceMismatch},
		{name: "denied role", err: authority.ErrAdmissionDenied},
		{name: "action not allowed", err: authority.ErrActionNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := &workerProfileAPIStub{}
			resolver := &workerProfileResolverStub{err: test.err}
			mux := http.NewServeMux()
			New(Config{WorkerProfiles: api, Authority: resolver}).Register(mux)
			req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/OTHER/execution/worker-profiles/falcon", strings.NewReader(`{"backend":"codex"}`))
			req = withCanonicalWorkspace(req, "OTHER", "OTHER")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden || api.updates != 0 {
				t.Fatalf("status/body/calls = %d/%s/%d", rec.Code, rec.Body.String(), api.updates)
			}
		})
	}
}

func TestWorkerProfileRoutesRejectEOFAndTrailingJSON(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "truncated create", method: http.MethodPost, path: "/api/workspaces/WS/execution/worker-profiles", body: `{"profile_id":`},
		{name: "empty update EOF", method: http.MethodPatch, path: "/api/workspaces/WS/execution/worker-profiles/falcon", body: ``},
		{name: "trailing create value", method: http.MethodPost, path: "/api/workspaces/WS/execution/worker-profiles", body: `{"profile_id":"falcon","role":"task"} {}`},
		{name: "workspace field forbidden", method: http.MethodPost, path: "/api/workspaces/WS/execution/worker-profiles", body: `{"workspace_key":"OTHER","profile_id":"falcon","role":"task"}`},
		{name: "delete body forbidden", method: http.MethodDelete, path: "/api/workspaces/WS/execution/worker-profiles/falcon", body: `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := &workerProfileAPIStub{}
			mux := http.NewServeMux()
			New(Config{WorkerProfiles: api, Authority: &workerProfileResolverStub{}}).Register(mux)
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			req = withCanonicalWorkspace(req, "WS", "WS")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || api.creates+api.updates+api.deletes != 0 {
				t.Fatalf("status/body/calls = %d/%s/%d", rec.Code, rec.Body.String(), api.creates+api.updates+api.deletes)
			}
		})
	}
}

func TestWorkerProfileRouteMapsUnauthenticated(t *testing.T) {
	api := &workerProfileAPIStub{}
	resolver := &workerProfileResolverStub{err: workflowcataloghttp.ErrUnauthenticated}
	mux := http.NewServeMux()
	New(Config{WorkerProfiles: api, Authority: resolver}).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/execution/worker-profiles", strings.NewReader(`{"profile_id":"falcon","role":"task"}`))
	req = withCanonicalWorkspace(req, "WS", "WS")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || api.creates != 0 {
		t.Fatalf("status/body/calls = %d/%s/%d", rec.Code, rec.Body.String(), api.creates)
	}
}

func TestWorkerProfileRoutesFailClosedWhenCapabilityOrAuthorityUnavailable(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "authority unavailable", config: Config{WorkerProfiles: &workerProfileAPIStub{}}},
		{name: "capability unavailable", config: Config{Authority: &workerProfileResolverStub{}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mux := http.NewServeMux()
			New(test.config).Register(mux)
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/execution/worker-profiles", strings.NewReader(`{"profile_id":"falcon","role":"task"}`))
			req = withCanonicalWorkspace(req, "WS", "WS")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestWorkerProfileRouteMapsConflictingCreateWithoutSecondMutationPath(t *testing.T) {
	api := &workerProfileAPIStub{err: execution.ErrConflict}
	mux := http.NewServeMux()
	New(Config{WorkerProfiles: api, Authority: &workerProfileResolverStub{}}).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/execution/worker-profiles", strings.NewReader(`{"profile_id":"falcon","role":"task"}`))
	req = withCanonicalWorkspace(req, "WS", "WS")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || api.creates != 1 || api.updates != 0 || api.deletes != 0 {
		t.Fatalf("status/body/mutation calls = %d/%s/%d,%d,%d", rec.Code, rec.Body.String(), api.creates, api.updates, api.deletes)
	}
}

func TestWorkerProfileRoutesUseCanonicalWorkspaceAndFailClosedWithoutResolution(t *testing.T) {
	body := `{"profile_id":"falcon","role":"task"}`

	t.Run("alias resolves to canonical workspace", func(t *testing.T) {
		api := &workerProfileAPIStub{}
		resolver := &workerProfileResolverStub{}
		mux := http.NewServeMux()
		New(Config{WorkerProfiles: api, Authority: resolver}).Register(mux)
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/workspaces/ALIAS/execution/worker-profiles",
			strings.NewReader(body),
		)
		req = withCanonicalWorkspace(req, "ALIAS", "CANONICAL")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
		}
		if api.create.WorkspaceKey != "CANONICAL" || resolver.workspace != "CANONICAL" {
			t.Fatalf("command/authority workspaces = %q/%q", api.create.WorkspaceKey, resolver.workspace)
		}
	})

	t.Run("missing canonical workspace fails closed", func(t *testing.T) {
		api := &workerProfileAPIStub{}
		resolver := &workerProfileResolverStub{}
		mux := http.NewServeMux()
		New(Config{WorkerProfiles: api, Authority: resolver}).Register(mux)
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/workspaces/WS/execution/worker-profiles",
			strings.NewReader(body),
		)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
		}
		if api.creates != 0 || resolver.workspace != "" {
			t.Fatalf("capability/authority invoked = %d/%q", api.creates, resolver.workspace)
		}
	})
}

func withCanonicalWorkspace(request *http.Request, requested, canonical string) *http.Request {
	ref := middleware.WorkspaceRef{RequestedID: requested, CanonicalID: canonical}
	return request.WithContext(middleware.WithWorkspaceRef(request.Context(), ref))
}
