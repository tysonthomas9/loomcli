package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
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
	replaceErr     error
}

type agentServiceCASLifecycleAPI struct {
	*testAgentRecordAPI
	bindings *agentServiceCASBindingStore
	calls    int
}

func (api *agentServiceCASLifecycleAPI) ApplyLifecycle(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command agentsmodule.ApplyLifecycleCommand,
) (*agentsmodule.LifecycleResult, error) {
	result, err := api.testAgentRecordAPI.ApplyLifecycle(ctx, auth, command)
	if err != nil {
		return nil, err
	}
	api.calls++
	if command.Action != agentsmodule.LifecycleDelete {
		return result, nil
	}
	result.BindingIDs = result.BindingIDs[:0]
	for bindingID, binding := range api.bindings.bindings {
		if binding.TargetAgentServiceID != command.AgentID {
			continue
		}
		result.BindingIDs = append(result.BindingIDs, bindingID)
		delete(api.bindings.bindings, bindingID)
	}
	return result, nil
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
	if s.replaceErr != nil {
		return nil, s.replaceErr
	}
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

type countingAgentServiceStore struct {
	agentsmodule.AgentServiceStore
	updates int
	deletes int
}

func (s *countingAgentServiceStore) Delete(ctx context.Context, workspaceKey, serviceID string) error {
	s.deletes++
	return s.AgentServiceStore.Delete(ctx, workspaceKey, serviceID)
}

func (s *countingAgentServiceStore) Update(
	ctx context.Context,
	workspaceKey, serviceID string,
	patch agentsmodule.AgentServiceUpdate,
) (*agentsmodule.AgentServiceRecord, error) {
	s.updates++
	return s.AgentServiceStore.Update(ctx, workspaceKey, serviceID, patch)
}

type storeWithCountingAgentServices struct {
	*memstore.Store
	services *countingAgentServiceStore
}

func (s *storeWithCountingAgentServices) AgentServices() agentsmodule.AgentServiceStore {
	return s.services
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
		authority.Allow(automation.ActionEnsureManagedBinding, authority.ClassSystem),
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
	countingServices := &countingAgentServiceStore{AgentServiceStore: st.AgentServices()}
	countingStore := &storeWithCountingAgentServices{Store: st, services: countingServices}
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
	provisioningPrincipal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "agent-provisioning-test", Class: authority.ClassSystem,
		Workspace: agentRecordTestWS,
		Actions:   []authority.Action{automation.ActionEnsureManagedBinding},
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	provisioningBindingAuthority, err := issuer.IssueSystem(
		provisioningPrincipal,
		agentRecordTestWS,
		automation.ActionEnsureManagedBinding,
		"handler provisioning test",
	)
	if err != nil {
		t.Fatal(err)
	}
	provisioning := newTestAgentProvisioningWithBindingAuthority(
		countingStore,
		bindings,
		provisioningBindingAuthority,
	)
	lifecycle := &agentServiceCASLifecycleAPI{
		testAgentRecordAPI: &testAgentRecordAPI{store: st},
		bindings:           persistence,
	}
	module := New(Config{
		AgentRuns: testAgentRunQueries{store: countingStore}, Bindings: bindings, OperatorAuthority: resolver,
		AgentRecords:          lifecycle,
		AgentRecordAuthority:  resolver,
		Provisioning:          provisioning,
		ProvisioningAuthority: provisioning,
		PrepareWorkflowTarget: testWorkflowTargetPreparation(countingStore),
		WorkspaceFromContext:  func(context.Context) string { return agentRecordTestWS },
		BindingGrants:         testBindingGrantCleanup{grants: st.Connectors()},
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
		`{"kind":"prompt","name":"Docs","behavior":{"role_name":"docs"},"trigger":{"source_kind":"cron","schedule":"*/10 * * * *","schedule_timezone":"UTC"}}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", created.Code, created.Body.String())
	}
	var createdDTO agentRecordDTO
	decodeJSON(t, created.Body.Bytes(), &createdDTO)
	if createdDTO.ID == "" || persistence.managedCreate != 1 || persistence.ordinaryCreate != 0 {
		t.Fatalf("create id=%q managed=%d ordinary=%d", createdDTO.ID, persistence.managedCreate, persistence.ordinaryCreate)
	}
	invalidSchedule := request(http.MethodPatch, "/api/workspaces/WS/agents/"+createdDTO.ID,
		`{"binding_id":"`+createdDTO.ID+`-1","schedule":"not a cron"}`)
	if invalidSchedule.Code != http.StatusBadRequest {
		t.Fatalf("invalid schedule status = %d; body=%s", invalidSchedule.Code, invalidSchedule.Body.String())
	}
	if persistence.managedReplace != 0 || persistence.bindings[createdDTO.ID+"-1"].Schedule != "*/10 * * * *" {
		t.Fatalf("invalid schedule mutated binding: replacements=%d binding=%+v",
			persistence.managedReplace, persistence.bindings[createdDTO.ID+"-1"])
	}
	foreignKind := request(http.MethodPatch, "/api/workspaces/WS/agents/"+createdDTO.ID,
		`{"name":"Must not persist","backend":"claude"}`)
	if foreignKind.Code != http.StatusBadRequest {
		t.Fatalf("foreign-kind patch status = %d; body=%s", foreignKind.Code, foreignKind.Body.String())
	}
	unchangedRecord, err := st.AgentServices().Get(t.Context(), agentRecordTestWS, createdDTO.ID)
	if err != nil || unchangedRecord.Name != "Docs" {
		t.Fatalf("foreign-kind patch mutated record = %+v err=%v", unchangedRecord, err)
	}
	rejectedRole := request(http.MethodPatch, "/api/workspaces/WS/agents/"+createdDTO.ID,
		`{"name":"Must not persist","behavior":{"role_name":"reviewer"}}`)
	if rejectedRole.Code != http.StatusBadRequest ||
		!strings.Contains(rejectedRole.Body.String(), "invalid request body") {
		t.Fatalf("role patch status = %d; body=%s", rejectedRole.Code, rejectedRole.Body.String())
	}
	unchangedRecord, err = st.AgentServices().Get(t.Context(), agentRecordTestWS, createdDTO.ID)
	if err != nil || unchangedRecord.Name != "Docs" || unchangedRecord.RoleName != "docs" ||
		countingServices.updates != 0 || persistence.managedReplace != 0 ||
		!strings.Contains(persistence.bindings[createdDTO.ID+"-1"].SourceConfigRef, `"roleName":"docs"`) {
		t.Fatalf("rejected role patch record=%+v updates=%d replacements=%d binding=%+v err=%v",
			unchangedRecord, countingServices.updates, persistence.managedReplace,
			persistence.bindings[createdDTO.ID+"-1"], err)
	}

	patched := request(http.MethodPatch, "/api/workspaces/WS/agents/"+createdDTO.ID, `{"name":"Daily review"}`)
	if patched.Code != http.StatusOK {
		t.Fatalf("name patch status = %d; body=%s", patched.Code, patched.Body.String())
	}
	if countingServices.updates != 0 || persistence.managedReplace != 0 {
		t.Fatalf("name patch updates=%d replacements=%d", countingServices.updates, persistence.managedReplace)
	}
	var patchedDTO agentRecordDTO
	decodeJSON(t, patched.Body.Bytes(), &patchedDTO)
	if patchedDTO.Name != "Daily review" || patchedDTO.Behavior.RoleName != "docs" {
		t.Fatalf("patched record = %+v", patchedDTO)
	}

	mixed := request(http.MethodPatch, "/api/workspaces/WS/agents/"+createdDTO.ID,
		`{"name":"Must not persist","binding_id":"`+createdDTO.ID+`-1","schedule":"0 9 * * *"}`)
	if mixed.Code != http.StatusBadRequest {
		t.Fatalf("mixed patch status = %d; body=%s", mixed.Code, mixed.Body.String())
	}
	if countingServices.updates != 0 || persistence.managedReplace != 0 {
		t.Fatalf("mixed patch updates=%d replacements=%d", countingServices.updates, persistence.managedReplace)
	}

	persistence.replaceErr = automation.ErrUnavailable
	failedSchedule := request(http.MethodPatch, "/api/workspaces/WS/agents/"+createdDTO.ID,
		`{"binding_id":"`+createdDTO.ID+`-1","schedule":"0 9 * * *"}`)
	if failedSchedule.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed schedule status = %d; body=%s", failedSchedule.Code, failedSchedule.Body.String())
	}
	if countingServices.updates != 0 || persistence.managedReplace != 0 ||
		persistence.bindings[createdDTO.ID+"-1"].Schedule != "*/10 * * * *" {
		t.Fatalf("failed schedule updates=%d replacements=%d binding=%+v",
			countingServices.updates, persistence.managedReplace, persistence.bindings[createdDTO.ID+"-1"])
	}
	persistence.replaceErr = nil

	scheduled := request(http.MethodPatch, "/api/workspaces/WS/agents/"+createdDTO.ID,
		`{"binding_id":"`+createdDTO.ID+`-1","schedule":"0 9 * * *","schedule_timezone":"America/Los_Angeles"}`)
	if scheduled.Code != http.StatusOK {
		t.Fatalf("schedule patch status = %d; body=%s", scheduled.Code, scheduled.Body.String())
	}
	if countingServices.updates != 0 || persistence.managedReplace != 1 ||
		persistence.bindings[createdDTO.ID+"-1"].Schedule != "0 9 * * *" ||
		persistence.bindings[createdDTO.ID+"-1"].ScheduleTimezone != "America/Los_Angeles" {
		t.Fatalf("schedule updates=%d replacements=%d binding=%+v",
			countingServices.updates, persistence.managedReplace, persistence.bindings[createdDTO.ID+"-1"])
	}
	deleted := request(http.MethodDelete, "/api/workspaces/WS/agents/"+createdDTO.ID, "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body=%s", deleted.Code, deleted.Body.String())
	}
	if persistence.managedReplace != 1 || persistence.managedDelete != 0 || len(persistence.bindings) != 0 ||
		persistence.ordinaryCreate != 0 || lifecycle.calls != 1 {
		t.Fatalf("lifecycle managed create/replace/delete=%d/%d/%d ordinary=%d remaining=%d",
			persistence.managedCreate, persistence.managedReplace, persistence.managedDelete, persistence.ordinaryCreate, len(persistence.bindings))
	}
	if countingServices.updates != 0 || countingServices.deletes != 0 {
		t.Fatalf(
			"unified handler used legacy AgentService mutations: updates=%d deletes=%d",
			countingServices.updates,
			countingServices.deletes,
		)
	}
}
