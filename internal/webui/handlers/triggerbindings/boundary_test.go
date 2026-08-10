package triggerbindings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type operatorResolverFunc func(*http.Request, string, authority.Action) (authority.OperatorAuthority, error)

func (f operatorResolverFunc) ResolveOperatorAuthority(r *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
	return f(r, workspace, action)
}

type stubBindingAPI struct {
	create   func(context.Context, authority.OperatorAuthority, automation.CreateBindingCommand) (*automation.Binding, error)
	update   func(context.Context, authority.OperatorAuthority, automation.UpdateBindingCommand) (*automation.Binding, error)
	enable   func(context.Context, authority.OperatorAuthority, automation.BindingCommand) (*automation.Binding, error)
	disable  func(context.Context, authority.OperatorAuthority, automation.BindingCommand) (*automation.Binding, error)
	delete   func(context.Context, authority.OperatorAuthority, automation.BindingCommand) error
	get      func(context.Context, string, string) (*automation.Binding, error)
	list     func(context.Context, string, automation.BindingFilter) ([]*automation.Binding, error)
	dispatch func(context.Context, authority.OperatorAuthority, automation.DispatchBindingCommand) (*automation.DispatchBindingResult, error)
}

func (s *stubBindingAPI) CreateBinding(ctx context.Context, auth authority.OperatorAuthority, command automation.CreateBindingCommand) (*automation.Binding, error) {
	if s.create == nil {
		return nil, automation.ErrUnavailable
	}
	return s.create(ctx, auth, command)
}

func (s *stubBindingAPI) UpdateBinding(ctx context.Context, auth authority.OperatorAuthority, command automation.UpdateBindingCommand) (*automation.Binding, error) {
	if s.update == nil {
		return nil, automation.ErrUnavailable
	}
	return s.update(ctx, auth, command)
}

func (s *stubBindingAPI) EnableBinding(ctx context.Context, auth authority.OperatorAuthority, command automation.BindingCommand) (*automation.Binding, error) {
	if s.enable == nil {
		return nil, automation.ErrUnavailable
	}
	return s.enable(ctx, auth, command)
}

func (s *stubBindingAPI) DisableBinding(ctx context.Context, auth authority.OperatorAuthority, command automation.BindingCommand) (*automation.Binding, error) {
	if s.disable == nil {
		return nil, automation.ErrUnavailable
	}
	return s.disable(ctx, auth, command)
}

func (s *stubBindingAPI) DeleteBinding(ctx context.Context, auth authority.OperatorAuthority, command automation.BindingCommand) error {
	if s.delete == nil {
		return automation.ErrUnavailable
	}
	return s.delete(ctx, auth, command)
}

func (s *stubBindingAPI) GetBinding(ctx context.Context, workspace, bindingID string) (*automation.Binding, error) {
	if s.get == nil {
		return nil, automation.ErrUnavailable
	}
	return s.get(ctx, workspace, bindingID)
}

func (s *stubBindingAPI) ListBindings(ctx context.Context, workspace string, filter automation.BindingFilter) ([]*automation.Binding, error) {
	if s.list == nil {
		return nil, automation.ErrUnavailable
	}
	return s.list(ctx, workspace, filter)
}

func (s *stubBindingAPI) DispatchBinding(ctx context.Context, auth authority.OperatorAuthority, command automation.DispatchBindingCommand) (*automation.DispatchBindingResult, error) {
	if s.dispatch == nil {
		return nil, automation.ErrUnavailable
	}
	return s.dispatch(ctx, auth, command)
}

type connectorCompatibilityStub struct {
	configure func(context.Context, string, string, string, string) error
	revoke    func(context.Context, string, string) (int, error)
}

type allowUnattachedBindingIdentity struct{}

func (allowUnattachedBindingIdentity) CheckUnattachedBindingID(context.Context, string, string) error {
	return nil
}

type unexpectedWorkflowTargetPreparer struct{}

func (unexpectedWorkflowTargetPreparer) PrepareWorkflowTarget(context.Context, string, string) (workflowbinding.WorkflowTarget, error) {
	panic("explicit driver request unexpectedly prepared a workflow target")
}

func (s *connectorCompatibilityStub) ConfigureBindingSecret(ctx context.Context, workspace, bindingID, sourceKind, secret string) error {
	if s.configure == nil {
		return nil
	}
	return s.configure(ctx, workspace, bindingID, sourceKind, secret)
}

func (s *connectorCompatibilityStub) RevokeBindingGrants(ctx context.Context, workspace, bindingID string) (int, error) {
	if s.revoke == nil {
		return 0, nil
	}
	return s.revoke(ctx, workspace, bindingID)
}

func registerBoundaryModule(api *stubBindingAPI, resolver workflowcataloghttp.OperatorAuthorityResolver, connectors ConnectorCompatibility) *http.ServeMux {
	createWorkflow, err := workflowbinding.New(unexpectedWorkflowTargetPreparer{}, api)
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	New(Config{
		CreateWorkflow: createWorkflow,
		Commands:       api, Queries: api, ManualDispatch: api, OperatorAuthority: resolver,
		WorkspaceFromContext: func(context.Context) string { return "CANONICAL" }, Connectors: connectors,
		AgentIdentities: allowUnattachedBindingIdentity{},
	}).Register(mux)
	return mux
}

func TestRunBindingReturnsCommittedSnapshotWithoutRunReRead(t *testing.T) {
	const snapshot = `{"workspace_key":"CANONICAL","run_id":"run-committed","driver_id":"driver-1","driver_version_id":"version-1","status":"queued","future_field":"preserved"}`
	api := &stubBindingAPI{dispatch: func(_ context.Context, _ authority.OperatorAuthority, command automation.DispatchBindingCommand) (*automation.DispatchBindingResult, error) {
		if command.WorkspaceKey != "CANONICAL" || command.BindingID != "binding-1" || command.IdempotencyKey != "manual-key" {
			t.Fatalf("dispatch command = %+v", command)
		}
		return &automation.DispatchBindingResult{
			BindingID: command.BindingID, RunID: "run-committed", RunSnapshot: json.RawMessage(snapshot), Replayed: true,
		}, nil
	}}
	resolver := operatorResolverFunc(func(_ *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
		if workspace != "CANONICAL" || action != automation.ActionDispatchBinding {
			t.Fatalf("authority scope = %q/%q", workspace, action)
		}
		return authority.OperatorAuthority{}, nil
	})
	// registerBoundaryModule deliberately provides no RunQueries. A successful
	// response therefore proves the handler did not re-read mutable run state.
	mux := registerBoundaryModule(api, resolver, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/alias/trigger-bindings/binding-1/run", nil)
	request.Header.Set("Idempotency-Key", "manual-key")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"future_field":"preserved"`) ||
		!strings.Contains(response.Body.String(), `"run_id":"run-committed"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMutationAuthorityFailuresStopBeforeAutomation(t *testing.T) {
	for _, test := range []struct {
		name       string
		resolveErr error
		wantStatus int
	}{
		{name: "unauthenticated", resolveErr: workflowcataloghttp.ErrUnauthenticated, wantStatus: http.StatusUnauthorized},
		{name: "wrong workspace", resolveErr: authority.ErrWorkspaceMismatch, wantStatus: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			api := &stubBindingAPI{create: func(context.Context, authority.OperatorAuthority, automation.CreateBindingCommand) (*automation.Binding, error) {
				called = true
				return nil, errors.New("must not be called")
			}}
			resolver := operatorResolverFunc(func(_ *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
				if workspace != "CANONICAL" || action != automation.ActionCreateBinding {
					t.Fatalf("authority scope = (%q, %q)", workspace, action)
				}
				return authority.OperatorAuthority{}, test.resolveErr
			})
			mux := registerBoundaryModule(api, resolver, nil)
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/alias/trigger-bindings", strings.NewReader(`{}`))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if called {
				t.Fatal("Automation create was invoked after authority denial")
			}
		})
	}
}

func TestCreateKeepsSecretOutOfAutomation(t *testing.T) {
	const secret = "connector-only-secret"
	var commandJSON []byte
	api := &stubBindingAPI{
		get: func(context.Context, string, string) (*automation.Binding, error) {
			return nil, automation.ErrNotFound
		},
		create: func(_ context.Context, _ authority.OperatorAuthority, command automation.CreateBindingCommand) (*automation.Binding, error) {
			commandJSON, _ = json.Marshal(command)
			return &automation.Binding{
				WorkspaceKey: command.WorkspaceKey, BindingID: command.Definition.BindingID,
				Name: command.Definition.Name, SourceKind: command.Definition.SourceKind,
				RouteKey: command.Definition.RouteKey, DriverID: "driver-1", DriverVersionID: "version-1",
				Enabled: command.Definition.Enabled,
			}, nil
		},
	}
	var connectorSecret string
	connectors := &connectorCompatibilityStub{configure: func(_ context.Context, workspace, bindingID, sourceKind, got string) error {
		if workspace != "CANONICAL" || bindingID != "github-binding" || sourceKind != "github" {
			t.Fatalf("connector scope = %q/%q/%q", workspace, bindingID, sourceKind)
		}
		connectorSecret = got
		return nil
	}}
	resolver := operatorResolverFunc(func(*http.Request, string, authority.Action) (authority.OperatorAuthority, error) {
		return authority.OperatorAuthority{}, nil
	})
	mux := registerBoundaryModule(api, resolver, connectors)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/alias/trigger-bindings", strings.NewReader(
		`{"driver_id":"driver-1","binding_id":"github-binding","route_key":"github.pull_request.opened","source_kind":"github","secret":"`+secret+`","enabled":true}`,
	))
	req.Header.Set("Authorization", "Bearer operator")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", response.Code, response.Body.String())
	}
	if connectorSecret != secret {
		t.Fatalf("connector secret = %q, want supplied secret", connectorSecret)
	}
	if strings.Contains(string(commandJSON), secret) || strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), "webhook_secret") {
		t.Fatalf("secret crossed Automation or response boundary: command=%s response=%s", commandJSON, response.Body.String())
	}
}

func TestDeleteDisablesRevokesThenDeletesAndCanResume(t *testing.T) {
	var calls []string
	revokeAttempts := 0
	api := &stubBindingAPI{
		get: func(context.Context, string, string) (*automation.Binding, error) {
			calls = append(calls, "get")
			return &automation.Binding{WorkspaceKey: "CANONICAL", BindingID: "binding-a", Enabled: true}, nil
		},
		disable: func(context.Context, authority.OperatorAuthority, automation.BindingCommand) (*automation.Binding, error) {
			calls = append(calls, "disable")
			return &automation.Binding{WorkspaceKey: "CANONICAL", BindingID: "binding-a", Enabled: false}, nil
		},
		delete: func(context.Context, authority.OperatorAuthority, automation.BindingCommand) error {
			calls = append(calls, "delete")
			return nil
		},
	}
	connectors := &connectorCompatibilityStub{revoke: func(context.Context, string, string) (int, error) {
		calls = append(calls, "revoke")
		revokeAttempts++
		if revokeAttempts == 1 {
			return 1, errors.New("transient connector failure")
		}
		return 2, nil
	}}
	resolver := operatorResolverFunc(func(_ *http.Request, _ string, action authority.Action) (authority.OperatorAuthority, error) {
		calls = append(calls, "auth:"+string(action))
		return authority.OperatorAuthority{}, nil
	})
	mux := registerBoundaryModule(api, resolver, connectors)
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/alias/trigger-bindings/binding-a", nil)
		req.Header.Set("Authorization", "Bearer operator")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		return response
	}
	first := request()
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500; body=%s", first.Code, first.Body.String())
	}
	wantFirst := []string{
		"auth:" + string(automation.ActionDisableBinding), "auth:" + string(automation.ActionDeleteBinding),
		"get", "disable", "revoke",
	}
	if strings.Join(calls, ",") != strings.Join(wantFirst, ",") {
		t.Fatalf("first calls = %v, want %v", calls, wantFirst)
	}
	calls = nil
	second := request()
	if second.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200; body=%s", second.Code, second.Body.String())
	}
	wantSecond := append(append([]string(nil), wantFirst[:4]...), "revoke", "delete")
	if strings.Join(calls, ",") != strings.Join(wantSecond, ",") {
		t.Fatalf("retry calls = %v, want %v", calls, wantSecond)
	}
}

func TestHandlerHasNoDirectBindingWriteOrDriverRunFallback(t *testing.T) {
	source, err := os.ReadFile("module.go")
	if err != nil {
		t.Fatalf("read module source: %v", err)
	}
	text := string(source)
	for _, forbidden := range []string{
		".TriggerBindings().Create", ".TriggerBindings().Update", ".TriggerBindings().Delete",
		"driver.CreateDriverRun", "internal/driver", "internal/workflows",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("module contains forbidden direct fallback %q", forbidden)
		}
	}
}
