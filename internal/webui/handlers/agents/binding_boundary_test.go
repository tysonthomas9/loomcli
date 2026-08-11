package agents

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/domain"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type boundaryOperatorResolverFunc func(*http.Request, string, authority.Action) (authority.OperatorAuthority, error)

func (f boundaryOperatorResolverFunc) ResolveOperatorAuthority(r *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
	return f(r, workspace, action)
}

func TestPromptAgentCreateRequiresManagedBindingAuthorityBeforeMutation(t *testing.T) {
	st := newAgentRecordStore(t)
	module := newTestAgentsModule(nil, st, nil, agentRecordTestWS)
	mux := http.NewServeMux()
	module.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/agents", strings.NewReader(
		`{"kind":"prompt","name":"Docs","behavior":{"role_name":"docs"},"trigger":{"source_kind":"internal"}}`,
	))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", response.Code, response.Body.String())
	}
	if _, err := st.Roles().Get(context.Background(), agentRecordTestWS, "docs"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unauthorized create mutated role: %v", err)
	}
	records, err := st.AgentServices().List(context.Background(), agentRecordTestWS, store.AgentServiceFilter{})
	if err != nil || len(records) != 0 {
		t.Fatalf("unauthorized create records = %+v err=%v, want none", records, err)
	}
}

func TestLegacyBindingPatchIsNotAnAgentRoute(t *testing.T) {
	st := newAgentRecordStore(t)
	if _, err := st.TriggerBindings().Create(context.Background(), store.TriggerBindingCreate{
		WorkspaceKey: agentRecordTestWS, BindingID: "legacy", Name: "Legacy", SourceKind: store.InternalSourceKind,
		DriverID: "prompt-agent", DriverVersionID: promptAgentDriverTestVersion, Enabled: true,
	}); err != nil {
		t.Fatalf("seed legacy binding: %v", err)
	}
	bindings := &testBindingOperations{store: st}
	module := New(Config{
		Store: st, Bindings: bindings, AgentRecords: &testAgentRecordAPI{store: st},
		AgentRecordAuthority: testOperatorAuthorityResolver{},
		OperatorAuthority: boundaryOperatorResolverFunc(func(_ *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
			t.Fatalf("retired legacy binding route requested authority for %q/%q", workspace, action)
			return authority.OperatorAuthority{}, nil
		}),
		WorkspaceFromContext: func(context.Context) string { return agentRecordTestWS },
		BindingGrants:        testBindingGrantCleanup{grants: st.Connectors()},
	})
	mux := http.NewServeMux()
	module.Register(mux)
	request := httptest.NewRequest(http.MethodPatch, "/api/workspaces/alias/agents/legacy", strings.NewReader(`{"name":"Renamed"}`))
	request.Header.Set("Authorization", "Bearer wrong-workspace")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", response.Code, response.Body.String())
	}
	binding, err := st.TriggerBindings().Get(context.Background(), agentRecordTestWS, "legacy")
	if err != nil || binding.Name != "Legacy" {
		t.Fatalf("wrong-workspace patch mutated binding: %+v err=%v", binding, err)
	}
}

func TestManagedBindingLifecycleUsesExactActions(t *testing.T) {
	st := newAgentRecordStore(t)
	seedPromptAgentRole(t, st, "docs")
	bindings := &testBindingOperations{store: st}
	var actions []authority.Action
	provisioning := newTestAgentProvisioning(st, bindings)
	provisioning.onResolve = func(workspace string, action authority.Action) {
		if workspace != agentRecordTestWS {
			t.Fatalf(
				"provisioning authority workspace = %q, want %q",
				workspace,
				agentRecordTestWS,
			)
		}
		actions = append(actions, action)
	}
	resolver := boundaryOperatorResolverFunc(func(_ *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
		if workspace != agentRecordTestWS {
			t.Fatalf("authority workspace = %q, want %q", workspace, agentRecordTestWS)
		}
		actions = append(actions, action)
		return authority.OperatorAuthority{}, nil
	})
	module := New(Config{
		Store: st, Bindings: bindings,
		OperatorAuthority: resolver,
		AgentRecords:      &testAgentRecordAPI{store: st}, AgentRecordAuthority: resolver,
		Provisioning:          provisioning,
		ProvisioningAuthority: provisioning,
		PrepareWorkflowTarget: testWorkflowTargetPreparation(st),
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
		`{"kind":"prompt","name":"Docs","behavior":{"role_name":"docs"},"trigger":{"source_kind":"internal"}}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", created.Code, created.Body.String())
	}
	var createdDTO agentRecordDTO
	decodeJSON(t, created.Body.Bytes(), &createdDTO)
	if createdDTO.ID == "" {
		t.Fatal("created agent has no id")
	}
	disabled := request(http.MethodPost, "/api/workspaces/WS/agents/"+createdDTO.ID+"/disable", "")
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable status = %d; body=%s", disabled.Code, disabled.Body.String())
	}
	deleted := request(http.MethodDelete, "/api/workspaces/WS/agents/"+createdDTO.ID, "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body=%s", deleted.Code, deleted.Body.String())
	}
	want := []authority.Action{
		agentprovisioning.ActionBeginProvisioning,
		agentsmodule.ActionApplyLifecycle,
		agentsmodule.ActionApplyLifecycle,
	}
	if len(actions) != len(want) {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
	for index := range want {
		if actions[index] != want[index] {
			t.Fatalf("actions = %v, want %v", actions, want)
		}
	}
}

func TestManagedBindingDeleteResumesAfterEveryDurableStepFailure(t *testing.T) {
	for _, failStep := range []string{"disable", "revoke", "delete"} {
		t.Run(failStep, func(t *testing.T) {
			state := &managedDeleteFaultState{failStep: failStep}
			module := &Module{
				bindings:      &managedDeleteFaultBindings{state: state},
				bindingGrants: &managedDeleteFaultGrants{state: state},
			}
			command := func() (bindingDeletionResult, error) {
				return module.deleteManagedBinding(
					context.Background(), agentRecordTestWS, "binding-1", "agent-1",
					authority.OperatorAuthority{}, authority.OperatorAuthority{},
				)
			}

			first, err := command()
			if err == nil || first.Deleted {
				t.Fatalf("first delete = %+v err=%v, want injected failure before completion", first, err)
			}
			if failStep != "disable" && !state.disabled {
				t.Fatalf("failure at %s lost committed disable", failStep)
			}
			if failStep == "delete" && !state.revoked {
				t.Fatal("delete failure lost committed grant revocation")
			}
			if state.deleted {
				t.Fatalf("failure at %s deleted binding prematurely", failStep)
			}

			second, err := command()
			if err != nil || !second.Deleted || !state.disabled || !state.revoked || !state.deleted {
				t.Fatalf("retry delete = %+v err=%v state=%+v, want converged deletion", second, err, state)
			}
			wantCalls := map[string][]string{
				"disable": {"disable", "disable", "revoke", "delete"},
				"revoke":  {"disable", "revoke", "disable", "revoke", "delete"},
				"delete":  {"disable", "revoke", "delete", "disable", "revoke", "delete"},
			}[failStep]
			if strings.Join(state.calls, ",") != strings.Join(wantCalls, ",") {
				t.Fatalf("calls = %v, want %v", state.calls, wantCalls)
			}
		})
	}
}

func TestManagedAgentDeleteResumesAfterParkOrArchiveFailure(t *testing.T) {
	for _, failStep := range []string{"park", "archive"} {
		t.Run(failStep, func(t *testing.T) {
			base := newAgentRecordStore(t)
			created := createPromptAgentForTest(t, newAgentsMux(base))
			bindingID := created.Bindings[0].BindingID
			if _, err := base.Connectors().CreateManagementGrant(context.Background(), connectorsmodule.CreateGrantMutation{
				WorkspaceKey: agentRecordTestWS, GrantID: "grant-1", ConnectorID: "github",
				BindingID: bindingID, Action: "github.comment", ResourcePattern: "repo:o/r",
			}); err != nil {
				t.Fatalf("create grant: %v", err)
			}

			services := &faultAgentServiceStore{AgentServiceStore: base.AgentServices(), failStep: failStep}
			faultStore := &faultAgentRecordStore{Store: base, services: services}
			module := New(Config{
				Store: faultStore, Bindings: &testBindingOperations{store: faultStore},
				OperatorAuthority:    testOperatorAuthorityResolver{},
				AgentRecords:         &testAgentRecordAPI{store: faultStore},
				AgentRecordAuthority: testOperatorAuthorityResolver{},
				WorkspaceFromContext: func(context.Context) string { return agentRecordTestWS },
				BindingGrants:        testBindingGrantCleanup{grants: faultStore.Connectors()},
			})
			mux := http.NewServeMux()
			module.Register(mux)
			request := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/WS/agents/"+created.ID, nil)
				req.Header.Set("Authorization", "Bearer operator")
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, req)
				return response
			}

			first := request()
			if first.Code != http.StatusInternalServerError {
				t.Fatalf("first delete status = %d, want 500; body=%s", first.Code, first.Body.String())
			}
			if _, err := base.TriggerBindings().Get(context.Background(), agentRecordTestWS, bindingID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("binding after %s failure err=%v, want already deleted", failStep, err)
			}
			grants, err := base.Connectors().ListGrantRecordsByBinding(context.Background(), agentRecordTestWS, bindingID)
			if err != nil || len(grants) != 0 {
				t.Fatalf("grants after %s failure = %+v err=%v, want revoked", failStep, grants, err)
			}
			record, err := base.AgentServices().Get(context.Background(), agentRecordTestWS, created.ID)
			if err != nil || record.DeletedAt != nil {
				t.Fatalf("record after %s failure = %+v err=%v, want retryable unarchived record", failStep, record, err)
			}
			if failStep == "archive" && record.DesiredState != domain.AgentServiceDesiredStopped {
				t.Fatalf("record after archive failure desired_state=%q, want stopped", record.DesiredState)
			}

			second := request()
			if second.Code != http.StatusOK {
				t.Fatalf("retry delete status = %d, want 200; body=%s", second.Code, second.Body.String())
			}
			record, err = base.AgentServices().Get(context.Background(), agentRecordTestWS, created.ID)
			if err != nil || record.DesiredState != domain.AgentServiceDesiredStopped || record.DeletedAt == nil {
				t.Fatalf("record after retry = %+v err=%v, want stopped and archived", record, err)
			}
		})
	}
}

type managedDeleteFaultState struct {
	failStep string
	failed   bool
	disabled bool
	revoked  bool
	deleted  bool
	calls    []string
}

type managedDeleteFaultBindings struct {
	automation.BindingOperations
	state *managedDeleteFaultState
}

func (bindings *managedDeleteFaultBindings) DisableManagedBinding(
	_ context.Context,
	_ authority.OperatorAuthority,
	_ automation.ManagedBindingCommand,
) (*automation.Binding, error) {
	bindings.state.calls = append(bindings.state.calls, "disable")
	if bindings.state.failStep == "disable" && !bindings.state.failed {
		bindings.state.failed = true
		return nil, errors.New("injected managed disable failure")
	}
	bindings.state.disabled = true
	return &automation.Binding{WorkspaceKey: agentRecordTestWS, BindingID: "binding-1", Enabled: false}, nil
}

func (bindings *managedDeleteFaultBindings) DeleteManagedBinding(
	_ context.Context,
	_ authority.OperatorAuthority,
	_ automation.ManagedBindingCommand,
) error {
	bindings.state.calls = append(bindings.state.calls, "delete")
	if bindings.state.failStep == "delete" && !bindings.state.failed {
		bindings.state.failed = true
		return errors.New("injected managed delete failure")
	}
	if !bindings.state.disabled || !bindings.state.revoked {
		return errors.New("managed delete ran before disable and grant revocation")
	}
	bindings.state.deleted = true
	return nil
}

type managedDeleteFaultGrants struct{ state *managedDeleteFaultState }

func (grants *managedDeleteFaultGrants) RevokeBindingGrants(context.Context, string, string) (int, error) {
	grants.state.calls = append(grants.state.calls, "revoke")
	if grants.state.failStep == "revoke" && !grants.state.failed {
		grants.state.failed = true
		return 0, errors.New("injected managed grant revocation failure")
	}
	if !grants.state.disabled {
		return 0, errors.New("grant revocation ran before managed disable")
	}
	if grants.state.revoked {
		return 0, nil
	}
	grants.state.revoked = true
	return 1, nil
}

type faultAgentRecordStore struct {
	store.Store
	services store.AgentServiceStore
}

func (faults *faultAgentRecordStore) AgentServices() store.AgentServiceStore { return faults.services }

type faultAgentServiceStore struct {
	store.AgentServiceStore
	failStep string
	failed   bool
}

func (faults *faultAgentServiceStore) Update(
	ctx context.Context,
	workspace, serviceID string,
	patch store.AgentServiceUpdate,
) (*domain.AgentService, error) {
	if faults.failStep == "park" && !faults.failed {
		faults.failed = true
		return nil, errors.New("injected agent park failure")
	}
	return faults.AgentServiceStore.Update(ctx, workspace, serviceID, patch)
}

func (faults *faultAgentServiceStore) Delete(ctx context.Context, workspace, serviceID string) error {
	if faults.failStep == "archive" && !faults.failed {
		faults.failed = true
		return errors.New("injected agent archive failure")
	}
	return faults.AgentServiceStore.Delete(ctx, workspace, serviceID)
}

func TestAgentsProductionSourcesHaveNoDirectTriggerBindingStoreAccess(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob production sources: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(source)
		for _, forbidden := range []string{"TriggerBindings()", "DeleteBindingAndRevokeGrants", "driver.CreateDriverRun"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains forbidden trigger-binding fallback %q", file, forbidden)
			}
		}
	}
}
