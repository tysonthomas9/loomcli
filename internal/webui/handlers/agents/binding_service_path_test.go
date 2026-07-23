package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type agentServiceCASBindingStore struct {
	bindings       map[string]*automation.Binding
	ordinaryCreate int
	managedCreate  int
	managedReplace int
	managedDelete  int
}

func newAgentServiceCASBindingStore() *agentServiceCASBindingStore {
	return &agentServiceCASBindingStore{bindings: make(map[string]*automation.Binding)}
}

func (s *agentServiceCASBindingStore) CreateBinding(context.Context, *automation.Binding) (*automation.Binding, error) {
	s.ordinaryCreate++
	return nil, automation.ErrManagedBinding
}

func (s *agentServiceCASBindingStore) CreateManagedBinding(_ context.Context, binding *automation.Binding) (*automation.Binding, error) {
	if binding == nil || strings.TrimSpace(binding.TargetAgentServiceID) == "" || binding.CreatedAt.IsZero() || binding.UpdatedAt.IsZero() {
		return nil, automation.ErrManagedBinding
	}
	s.managedCreate++
	s.bindings[binding.BindingID] = cloneAgentServiceTestBinding(binding)
	return cloneAgentServiceTestBinding(binding), nil
}

func (s *agentServiceCASBindingStore) GetBinding(_ context.Context, _, bindingID string) (*automation.Binding, error) {
	binding := s.bindings[bindingID]
	if binding == nil {
		return nil, automation.ErrNotFound
	}
	return cloneAgentServiceTestBinding(binding), nil
}

func (s *agentServiceCASBindingStore) ListBindings(_ context.Context, _ string, filter automation.BindingFilter) ([]*automation.Binding, error) {
	bindings := make([]*automation.Binding, 0, len(s.bindings))
	for _, binding := range s.bindings {
		if filter.TargetAgentServiceID != "" && binding.TargetAgentServiceID != filter.TargetAgentServiceID {
			continue
		}
		bindings = append(bindings, cloneAgentServiceTestBinding(binding))
	}
	return bindings, nil
}

func (s *agentServiceCASBindingStore) ReplaceManagedBinding(
	_ context.Context,
	replacement automation.ManagedBindingReplacement,
) (*automation.Binding, error) {
	current := s.bindings[replacement.Expected.BindingID]
	if current == nil || replacement.Binding == nil ||
		current.WorkspaceKey != replacement.Expected.WorkspaceKey ||
		current.TargetAgentServiceID != replacement.Expected.ExpectedTargetAgentServiceID ||
		current.RouteKey != replacement.Expected.ExpectedRouteKey ||
		!current.CreatedAt.Equal(replacement.Expected.ExpectedCreatedAt) ||
		!current.UpdatedAt.Equal(replacement.Expected.ExpectedUpdatedAt) ||
		replacement.Binding.TargetAgentServiceID != current.TargetAgentServiceID ||
		!replacement.Binding.CreatedAt.Equal(current.CreatedAt) ||
		!replacement.Binding.UpdatedAt.After(current.UpdatedAt) {
		return nil, automation.ErrManagedBinding
	}
	s.managedReplace++
	s.bindings[current.BindingID] = cloneAgentServiceTestBinding(replacement.Binding)
	return cloneAgentServiceTestBinding(replacement.Binding), nil
}

func (s *agentServiceCASBindingStore) DeleteManagedBindingIfUnchanged(
	_ context.Context,
	expected automation.ManagedBindingSnapshot,
) error {
	current := s.bindings[expected.BindingID]
	if current == nil || current.Enabled || current.WorkspaceKey != expected.WorkspaceKey ||
		current.TargetAgentServiceID != expected.ExpectedTargetAgentServiceID || current.RouteKey != expected.ExpectedRouteKey ||
		!current.CreatedAt.Equal(expected.ExpectedCreatedAt) || !current.UpdatedAt.Equal(expected.ExpectedUpdatedAt) {
		return automation.ErrManagedBinding
	}
	s.managedDelete++
	delete(s.bindings, expected.BindingID)
	return nil
}

func cloneAgentServiceTestBinding(binding *automation.Binding) *automation.Binding {
	if binding == nil {
		return nil
	}
	clone := *binding
	clone.EventTypePatterns = append([]string(nil), binding.EventTypePatterns...)
	clone.Permissions = append([]string(nil), binding.Permissions...)
	return &clone
}

type agentServiceCatalog struct{}

func (agentServiceCatalog) ResolveEffectiveVersion(
	_ context.Context,
	_ authority.SystemAuthority,
	workspace, driverRef string,
) (*workflowcatalog.EffectiveVersion, error) {
	return &workflowcatalog.EffectiveVersion{
		Driver: &workflowcatalog.Driver{
			WorkspaceKey: workspace, DriverID: driverRef, ActiveVersionID: promptAgentDriverTestVersion, Revision: 1,
		},
		Version: &workflowcatalog.DriverVersion{
			WorkspaceKey: workspace, DriverID: driverRef, VersionID: promptAgentDriverTestVersion,
			SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		},
	}, nil
}

type agentServiceCatalogAuthority struct{}

func (agentServiceCatalogAuthority) AuthorityForEffectiveVersion(context.Context, string, string) (authority.SystemAuthority, error) {
	return authority.SystemAuthority{}, nil
}

func TestAgentServiceHandlerUsesManagedCoreCASForCreateUpdateAndDelete(t *testing.T) {
	now := time.Date(2026, 7, 16, 14, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	admission, err := issuer.NewAdmission(
		authority.OperatorOnly(automation.ActionCreateManagedBinding),
		authority.OperatorOnly(automation.ActionUpdateManagedBinding),
		authority.OperatorOnly(automation.ActionDisableManagedBinding),
		authority.OperatorOnly(automation.ActionDeleteManagedBinding),
	)
	if err != nil {
		t.Fatal(err)
	}
	persistence := newAgentServiceCASBindingStore()
	bindings := automation.New(
		persistence, nil, persistence, nil, nil, nil, nil, nil,
		agentServiceCatalog{}, agentServiceCatalogAuthority{}, admission,
		automation.WithClock(func() time.Time { return now }),
	)
	st := newAgentRecordStore(t)
	seedPromptAgentRole(t, st, "docs")
	seedRole(t, st, "reviewer")
	resolver := boundaryOperatorResolverFunc(func(_ *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
		principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
			Subject: "agent-handler-test", Class: authority.ClassOperator, Workspace: workspace,
			Actions: []authority.Action{action}, ExpiresAt: now.Add(time.Hour),
		})
		if err != nil {
			return authority.OperatorAuthority{}, err
		}
		return issuer.IssueOperator(principal, workspace, action)
	})
	module := New(Config{
		Store: st, Bindings: bindings, OperatorAuthority: resolver,
		WorkspaceFromContext: func(context.Context) string { return agentRecordTestWS },
		BindingGrants:        testBindingGrantCompatibility{grants: st.ConnectorGrants()},
	})
	mux := http.NewServeMux()
	module.Register(mux)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer operator")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		return response
	}

	created := request(http.MethodPost, "/api/workspaces/WS/agents",
		`{"kind":"prompt","name":"Docs","behavior":{"role_name":"docs"},"trigger":{"source_kind":"internal"}}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", created.Code, created.Body.String())
	}
	var createdDTO agentRecordDTO
	decodeJSON(t, created.Body.Bytes(), &createdDTO)
	if createdDTO.ID == "" || persistence.managedCreate != 1 || persistence.ordinaryCreate != 0 {
		t.Fatalf("create id=%q managed=%d ordinary=%d", createdDTO.ID, persistence.managedCreate, persistence.ordinaryCreate)
	}
	patched := request(http.MethodPatch, "/api/workspaces/WS/agents/"+createdDTO.ID,
		`{"behavior":{"role_name":"reviewer"}}`)
	if patched.Code != http.StatusOK {
		t.Fatalf("patch status = %d; body=%s", patched.Code, patched.Body.String())
	}
	if persistence.managedReplace != 1 || !strings.Contains(persistence.bindings[createdDTO.ID+"-1"].SourceConfigRef, "reviewer") {
		t.Fatalf("patch replacements=%d binding=%+v", persistence.managedReplace, persistence.bindings[createdDTO.ID+"-1"])
	}
	deleted := request(http.MethodDelete, "/api/workspaces/WS/agents/"+createdDTO.ID, "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body=%s", deleted.Code, deleted.Body.String())
	}
	if persistence.managedReplace != 2 || persistence.managedDelete != 1 || len(persistence.bindings) != 0 || persistence.ordinaryCreate != 0 {
		t.Fatalf("lifecycle managed create/replace/delete=%d/%d/%d ordinary=%d remaining=%d",
			persistence.managedCreate, persistence.managedReplace, persistence.managedDelete, persistence.ordinaryCreate, len(persistence.bindings))
	}
}
