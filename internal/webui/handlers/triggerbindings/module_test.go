package triggerbindings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
)

// seededMux returns a mux wired to a memstore that already has a workflow
// driver ("driver-1") with a validated active version ("version-1").
func seededMux(t *testing.T) (*http.ServeMux, *memstore.Store) {
	t.Helper()
	s := memstore.New()
	ctx := context.Background()
	if _, err := s.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := s.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    workflowcatalog.DriverOwnerSystem,
		Status:       workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey:       "WS",
		VersionID:          "version-1",
		DriverID:           "driver-1",
		Version:            1,
		SourceDigest:       "sha256:source",
		BundleDigest:       "sha256:bundle",
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	automationAPI := &testAutomationAPI{store: s}
	createWorkflow, err := workflowbinding.New(&testWorkflowTargetPreparer{
		target: workflowbinding.WorkflowTarget{DriverID: "driver-1", DriverVersionID: "version-1"},
	}, automationAPI)
	if err != nil {
		t.Fatalf("new workflow binding: %v", err)
	}
	mux := http.NewServeMux()
	New(Config{
		CreateWorkflow: createWorkflow,
		Commands:       automationAPI, Queries: automationAPI, ManualDispatch: automationAPI,
		OperatorAuthority: testOperatorResolver{}, WorkspaceFromContext: func(context.Context) string { return "WS" },
		Runs: readprojection.NewBindingRunReader(s.DriverRuns()), Connectors: &testConnectorLifecycle{store: s},
		AgentIdentities: testAgentIdentityChecker{store: s},
	}).Register(mux)
	return mux, s
}

func muxWithIdentityChecker(
	t *testing.T,
	s *memstore.Store,
	checker agentsmodule.IdentityQueries,
) *http.ServeMux {
	t.Helper()
	automationAPI := &testAutomationAPI{store: s}
	createWorkflow, err := workflowbinding.New(&testWorkflowTargetPreparer{
		target: workflowbinding.WorkflowTarget{DriverID: "driver-1", DriverVersionID: "version-1"},
	}, automationAPI)
	if err != nil {
		t.Fatalf("new workflow binding: %v", err)
	}
	mux := http.NewServeMux()
	New(Config{
		CreateWorkflow: createWorkflow,
		Commands:       automationAPI, Queries: automationAPI, ManualDispatch: automationAPI,
		OperatorAuthority: testOperatorResolver{}, WorkspaceFromContext: func(context.Context) string { return "WS" },
		Runs: readprojection.NewBindingRunReader(s.DriverRuns()), Connectors: &testConnectorLifecycle{store: s},
		AgentIdentities: checker,
	}).Register(mux)
	return mux
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-operator")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

type testOperatorResolver struct{}

func (testOperatorResolver) ResolveOperatorAuthority(r *http.Request, _ string, _ authority.Action) (authority.OperatorAuthority, error) {
	if r == nil || strings.TrimSpace(r.Header.Get("Authorization")) == "" {
		return authority.OperatorAuthority{}, workflowcataloghttp.ErrUnauthenticated
	}
	return authority.OperatorAuthority{}, nil
}

type testAgentIdentityChecker struct {
	store *memstore.Store
}

func (checker testAgentIdentityChecker) GetAgent(
	ctx context.Context,
	workspace, bindingID string,
) (*agentsmodule.Agent, error) {
	if _, err := checker.store.AgentServices().Get(ctx, workspace, bindingID); err == nil {
		return &agentsmodule.Agent{WorkspaceKey: workspace, AgentID: bindingID}, nil
	} else if errors.Is(err, persistence.ErrNotFound) {
		return nil, agentsmodule.ErrNotFound
	} else {
		return nil, err
	}
}

func (checker testAgentIdentityChecker) ListAgents(context.Context, string, agentsmodule.AgentFilter) ([]*agentsmodule.Agent, error) {
	return nil, nil
}

type postCreateCollisionChecker struct {
	store *memstore.Store
	mu    sync.Mutex
	calls int
}

func (checker *postCreateCollisionChecker) GetAgent(
	ctx context.Context,
	workspace, bindingID string,
) (*agentsmodule.Agent, error) {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	checker.calls++
	if checker.calls == 2 {
		if _, err := checker.store.AgentServices().Create(ctx, agentsmodule.AgentServiceCreate{
			WorkspaceKey: workspace, ServiceID: bindingID, Name: bindingID, RoleName: "review",
			Kind: agentsmodule.AgentKindEvent, DesiredState: agentsmodule.DesiredRunning, MaxInstances: 1,
		}); err != nil {
			return nil, fmt.Errorf("insert concurrent agent fixture: %w", err)
		}
	}
	return testAgentIdentityChecker{store: checker.store}.GetAgent(ctx, workspace, bindingID)
}

func (checker *postCreateCollisionChecker) ListAgents(context.Context, string, agentsmodule.AgentFilter) ([]*agentsmodule.Agent, error) {
	return nil, nil
}

func (checker *postCreateCollisionChecker) callCount() int {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	return checker.calls
}

type testWorkflowTargetPreparer struct {
	target    workflowbinding.WorkflowTarget
	err       error
	calls     int
	workspace string
	workflow  string
	prepare   func(context.Context, string, string) error
}

func (preparer *testWorkflowTargetPreparer) PrepareWorkflowTarget(
	ctx context.Context,
	workspace, workflow string,
) (workflowbinding.WorkflowTarget, error) {
	preparer.calls++
	preparer.workspace = workspace
	preparer.workflow = workflow
	if preparer.prepare != nil {
		if err := preparer.prepare(ctx, workspace, workflow); err != nil {
			return workflowbinding.WorkflowTarget{}, err
		}
	}
	return preparer.target, preparer.err
}

// testAutomationAPI is a test-only adapter that keeps the pre-migration HTTP
// behavior fixtures intact while the production module is verified against
// Automation's public interfaces. Direct store writes are intentionally
// confined to test setup, never the HTTP adapter.
type testAutomationAPI struct {
	store *memstore.Store
	mu    sync.Mutex
	runs  int
}

func (a *testAutomationAPI) CreateBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.CreateBindingCommand) (*automation.Binding, error) {
	definition := command.Definition
	versionID := strings.TrimSpace(definition.DriverVersionID)
	if versionID == "" {
		versionID = "version-1"
	}
	created, err := a.store.TriggerBindings().Create(ctx, automation.TriggerBindingCreate{
		WorkspaceKey: command.WorkspaceKey, BindingID: definition.BindingID, Name: definition.Name,
		SourceKind: definition.SourceKind, SourceRef: definition.SourceRef, SourceConfigRef: definition.SourceConfigRef,
		RouteKey: definition.RouteKey, EventTypePatterns: definition.EventTypePatterns,
		DriverID: definition.DriverID, DriverVersionID: versionID, TargetEntrypoint: definition.TargetEntrypoint,
		ConcurrencyPolicy:  definition.ConcurrencyPolicy,
		SubjectKeyTemplate: definition.SubjectKeyTemplate, ActorFilter: definition.ActorFilter,
		RetryMaxAttempts: definition.RetryMaxAttempts, RetryBackoffSeconds: definition.RetryBackoffSeconds,
		Schedule: definition.Schedule, ScheduleTimezone: definition.ScheduleTimezone, Enabled: definition.Enabled,
	})
	if err != nil {
		return nil, mapTestAutomationError(err)
	}
	return automationBinding(created), nil
}

func (a *testAutomationAPI) UpdateBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.UpdateBindingCommand) (*automation.Binding, error) {
	existing, err := a.store.TriggerBindings().Get(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return nil, mapTestAutomationError(err)
	}
	if strings.TrimSpace(existing.TargetAgentServiceID) != "" {
		return nil, automation.ErrManagedBinding
	}
	patch := automation.TriggerBindingUpdate{
		Name: command.Patch.Name, Schedule: command.Patch.Schedule, ScheduleTimezone: command.Patch.ScheduleTimezone,
		SourceConfigRef:     command.Patch.SourceConfigRef,
		EventTypePatterns:   command.Patch.EventTypePatterns,
		ConcurrencyPolicy:   command.Patch.ConcurrencyPolicy,
		SubjectKeyTemplate:  command.Patch.SubjectKeyTemplate,
		RetryMaxAttempts:    command.Patch.RetryMaxAttempts,
		RetryBackoffSeconds: command.Patch.RetryBackoffSeconds,
	}
	if command.Patch.ClearActorFilter {
		patch.ActorFilter = &automation.ActorFilter{}
	} else {
		patch.ActorFilter = command.Patch.ActorFilter
	}
	updated, err := a.store.TriggerBindings().Update(ctx, command.WorkspaceKey, command.BindingID, patch)
	if err != nil {
		return nil, mapTestAutomationError(err)
	}
	return automationBinding(updated), nil
}

func (a *testAutomationAPI) EnableBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.BindingCommand) (*automation.Binding, error) {
	return a.setEnabled(ctx, command, true)
}

func (a *testAutomationAPI) DisableBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.BindingCommand) (*automation.Binding, error) {
	return a.setEnabled(ctx, command, false)
}

func (a *testAutomationAPI) setEnabled(ctx context.Context, command automation.BindingCommand, enabled bool) (*automation.Binding, error) {
	existing, err := a.store.TriggerBindings().Get(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return nil, mapTestAutomationError(err)
	}
	if strings.TrimSpace(existing.TargetAgentServiceID) != "" {
		return nil, automation.ErrManagedBinding
	}
	updated, err := a.store.TriggerBindings().Update(ctx, command.WorkspaceKey, command.BindingID, automation.TriggerBindingUpdate{Enabled: &enabled})
	if err != nil {
		return nil, mapTestAutomationError(err)
	}
	return automationBinding(updated), nil
}

func (a *testAutomationAPI) DeleteBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.BindingCommand) error {
	existing, err := a.store.TriggerBindings().Get(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return mapTestAutomationError(err)
	}
	if strings.TrimSpace(existing.TargetAgentServiceID) != "" {
		return automation.ErrManagedBinding
	}
	if existing.Enabled {
		return automation.ErrBindingEnabled
	}
	return mapTestAutomationError(a.store.TriggerBindings().Delete(ctx, command.WorkspaceKey, command.BindingID))
}

func (a *testAutomationAPI) GetBinding(ctx context.Context, workspace, bindingID string) (*automation.Binding, error) {
	binding, err := a.store.TriggerBindings().Get(ctx, workspace, bindingID)
	if err != nil {
		return nil, mapTestAutomationError(err)
	}
	return automationBinding(binding), nil
}

func (a *testAutomationAPI) ListBindings(ctx context.Context, workspace string, filter automation.BindingFilter) ([]*automation.Binding, error) {
	bindings, err := a.store.TriggerBindings().List(ctx, workspace, automation.TriggerBindingFilter(filter))
	if err != nil {
		return nil, mapTestAutomationError(err)
	}
	out := make([]*automation.Binding, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, automationBinding(binding))
	}
	return out, nil
}

func (a *testAutomationAPI) DispatchBinding(ctx context.Context, _ authority.OperatorAuthority, command automation.DispatchBindingCommand) (*automation.DispatchBindingResult, error) {
	binding, err := a.store.TriggerBindings().Get(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return nil, mapTestAutomationError(err)
	}
	a.mu.Lock()
	a.runs++
	runID := fmt.Sprintf("manual-run-%d", a.runs)
	a.mu.Unlock()
	run, err := a.store.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey: command.WorkspaceKey, RunID: runID, DriverID: binding.DriverID,
		DriverVersionID: binding.DriverVersionID, TriggerBindingID: binding.BindingID,
		SourceKind: "binding-run", SourceRef: firstNonEmpty(binding.RouteKey, binding.BindingID),
		IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return nil, mapTestAutomationError(err)
	}
	snapshot, err := json.Marshal(run)
	if err != nil {
		return nil, err
	}
	return &automation.DispatchBindingResult{BindingID: binding.BindingID, RunID: run.RunID, RunSnapshot: snapshot}, nil
}

func automationBinding(binding *automation.Binding) *automation.Binding {
	if binding == nil {
		return nil
	}
	return &automation.Binding{
		WorkspaceKey: binding.WorkspaceKey, BindingID: binding.BindingID, Name: binding.Name,
		SourceKind: binding.SourceKind, SourceRef: binding.SourceRef, SourceConfigRef: binding.SourceConfigRef,
		RouteKey: binding.RouteKey, EventTypePatterns: append([]string(nil), binding.EventTypePatterns...),
		DriverID: binding.DriverID, DriverVersionID: binding.DriverVersionID,
		TargetEntrypoint: binding.TargetEntrypoint, TargetAgentServiceID: binding.TargetAgentServiceID,
		ConcurrencyPolicy:  binding.ConcurrencyPolicy,
		SubjectKeyTemplate: binding.SubjectKeyTemplate, ActorFilter: binding.ActorFilter,
		RetryMaxAttempts: binding.RetryMaxAttempts, RetryBackoffSeconds: binding.RetryBackoffSeconds,
		Schedule: binding.Schedule, ScheduleTimezone: binding.ScheduleTimezone, Enabled: binding.Enabled,
		CreatedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
	}
}

func mapTestAutomationError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, persistence.ErrNotFound):
		return errors.Join(automation.ErrNotFound, err)
	case errors.Is(err, persistence.ErrInvalid):
		return errors.Join(automation.ErrInvalid, err)
	case errors.Is(err, persistence.ErrConflict), errors.Is(err, persistence.ErrAlreadyExists):
		return errors.Join(automation.ErrConflict, err)
	default:
		return err
	}
}

type testConnectorLifecycle struct {
	store *memstore.Store
}

func (c *testConnectorLifecycle) RevokeBindingGrants(ctx context.Context, command connectorsmodule.BindingGrantCleanupCommand) (int, error) {
	grants, err := c.store.Connectors().ListGrantRecordsByBinding(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return 0, err
	}
	revoked := 0
	for _, grant := range grants {
		if grant == nil {
			continue
		}
		if err := c.store.Connectors().RevokeGrantRecord(ctx, command.WorkspaceKey, grant.GrantID); err != nil {
			if errors.Is(err, connectorsmodule.ErrGrantRevoked) {
				continue
			}
			return revoked, err
		}
		revoked++
	}
	return revoked, nil
}

func TestCreateBinding_CreatesThenDisables(t *testing.T) {
	mux, _ := seededMux(t)

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"epics.runs.create","source_kind":"http","name":"epic-runner-binding","enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var binding automation.Binding
	if err := json.Unmarshal(rec.Body.Bytes(), &binding); err != nil {
		t.Fatalf("decode binding: %v", err)
	}
	if binding.RouteKey != "epics.runs.create" || binding.DriverID != "driver-1" || !binding.Enabled {
		t.Fatalf("unexpected binding: %+v", binding)
	}

	// Disabling flips Enabled without a request body.
	rec2 := do(t, mux, http.MethodPost,
		"/api/workspaces/WS/trigger-bindings/"+binding.BindingID+"/disable", "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
	var disabled automation.Binding
	if err := json.Unmarshal(rec2.Body.Bytes(), &disabled); err != nil {
		t.Fatalf("decode disabled: %v", err)
	}
	if disabled.Enabled {
		t.Fatalf("binding should be disabled after /disable")
	}
}

func TestCreateBinding_RejectsCanonicalAgentIdentifier(t *testing.T) {
	mux, st := seededMux(t)
	ctx := context.Background()
	if _, err := st.Roles().Create(ctx, agentsmodule.RoleRecordCreate{
		WorkspaceKey: "WS",
		Name:         "task",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, agentsmodule.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: "s3-local-review", Name: "s3-local-review", RoleName: "task",
		Kind: agentsmodule.AgentKindEvent, DesiredState: agentsmodule.DesiredRunning, MaxInstances: 1,
	}); err != nil {
		t.Fatalf("create canonical agent: %v", err)
	}

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/10 * * * *","binding_id":"s3-local-review","enabled":false}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already used by a durable agent record") {
		t.Fatalf("create status = %d body=%s, want clean canonical-agent 409", rec.Code, rec.Body.String())
	}
	if _, err := st.TriggerBindings().Get(ctx, "WS", "s3-local-review"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("binding after rejected create err = %v, want not found", err)
	}
}

func TestCreateBinding_RejectsArchivedAgentRecordIdentifier(t *testing.T) {
	mux, st := seededMux(t)
	ctx := context.Background()
	if _, err := st.Roles().Create(ctx, agentsmodule.RoleRecordCreate{
		WorkspaceKey: "WS",
		Name:         "review",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, agentsmodule.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "s2-review-loop",
		Name:         "Review loop",
		Kind:         agentsmodule.AgentKindEvent,
		DesiredState: agentsmodule.DesiredPaused,
		RoleName:     "review",
	}); err != nil {
		t.Fatalf("create agent record: %v", err)
	}
	if err := st.AgentServices().Delete(ctx, "WS", "s2-review-loop"); err != nil {
		t.Fatalf("archive agent record: %v", err)
	}

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/10 * * * *","binding_id":"s2-review-loop","enabled":false}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already used by a durable agent record") {
		t.Fatalf("create status = %d body=%s, want clean durable-record 409", rec.Code, rec.Body.String())
	}
	if _, err := st.TriggerBindings().Get(ctx, "WS", "s2-review-loop"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("binding after rejected create err = %v, want not found", err)
	}
}

func TestCreateAndPatchBindingPreserveRouterV2Fields(t *testing.T) {
	mux, _ := seededMux(t)
	created := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", `{
		"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"internal",
		"binding_id":"router-v2","event_type_patterns":["internal.task.ready"],
		"subject_key_template":"{{subject_ref}}","concurrency_policy":"queue",
		"actor_filter":{"exclude_actor_kinds":["workflow"]},
		"retry_max_attempts":7,"retry_backoff_seconds":33,"enabled":true
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", created.Code, created.Body.String())
	}
	var binding automation.Binding
	if err := json.Unmarshal(created.Body.Bytes(), &binding); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if binding.SubjectKeyTemplate != "{{subject_ref}}" || binding.ConcurrencyPolicy != automation.ConcurrencyQueue ||
		binding.ActorFilter == nil || len(binding.ActorFilter.ExcludeActorKinds) != 1 ||
		binding.RetryMaxAttempts != 7 || binding.RetryBackoffSeconds != 33 ||
		len(binding.EventTypePatterns) != 1 || binding.EventTypePatterns[0] != "internal.task.ready" {
		t.Fatalf("created Router v2 fields = %+v", binding)
	}

	updated := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/router-v2", `{
		"subject_key_template":"{{event_type}}","concurrency_policy":"forbid",
		"actor_filter":{"exclude_actor_kinds":["external"]},
		"retry_max_attempts":9,"retry_backoff_seconds":45,
		"event_type_patterns":["internal.run.finished"]
	}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("patch status = %d; body=%s", updated.Code, updated.Body.String())
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &binding); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if binding.SubjectKeyTemplate != "{{event_type}}" || binding.ConcurrencyPolicy != automation.ConcurrencyForbid ||
		binding.ActorFilter == nil || len(binding.ActorFilter.ExcludeActorKinds) != 1 || binding.ActorFilter.ExcludeActorKinds[0] != "external" ||
		binding.RetryMaxAttempts != 9 || binding.RetryBackoffSeconds != 45 ||
		len(binding.EventTypePatterns) != 1 || binding.EventTypePatterns[0] != "internal.run.finished" {
		t.Fatalf("updated Router v2 fields = %+v", binding)
	}
}

func TestSetEnabledRejectsAgentManagedBinding(t *testing.T) {
	mux, st := seededMux(t)
	ctx := context.Background()
	if _, err := st.Roles().Create(ctx, agentsmodule.RoleRecordCreate{WorkspaceKey: "WS", Name: "docs-assistant"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, agentsmodule.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: "agt-docs", Name: "Docs",
		Kind: agentsmodule.AgentKindEvent, DesiredState: agentsmodule.DesiredRunning, RoleName: "docs-assistant",
	}); err != nil {
		t.Fatalf("create agent service: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, automation.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "agt-docs-1", Name: "Docs",
		SourceKind: automation.InternalSourceKind, DriverID: "driver-1", DriverVersionID: "version-1",
		TargetAgentServiceID: "agt-docs", Enabled: false,
	}); err != nil {
		t.Fatalf("create attached binding: %v", err)
	}

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings/agt-docs-1/enable", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("enable status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "managed by agent agt-docs") {
		t.Fatalf("enable body = %s, want managed-by-agent message", rec.Body.String())
	}
	binding, err := st.TriggerBindings().Get(ctx, "WS", "agt-docs-1")
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.Enabled {
		t.Fatalf("binding enabled despite guard: %+v", binding)
	}
}

// TestCreateBinding_IsIdempotent pins the ensure contract the create-agent
// gallery relies on: re-activating the same template (same binding_id) returns
// 200 with the existing binding rather than a 409 that would fail activation
// before it reaches the connector/grant steps.
func TestCreateBinding_IsIdempotent(t *testing.T) {
	mux, _ := seededMux(t)
	body := `{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"epics.runs.create","source_kind":"http","binding_id":"b-fixed","enabled":true}`

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	rec2 := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("re-create status = %d, want 200 ensure; body=%s", rec2.Code, rec2.Body.String())
	}
	var binding automation.Binding
	if err := json.Unmarshal(rec2.Body.Bytes(), &binding); err != nil {
		t.Fatalf("decode ensure binding: %v", err)
	}
	if binding.BindingID != "b-fixed" {
		t.Fatalf("ensure returned binding_id = %q, want b-fixed", binding.BindingID)
	}
}

func TestCreateBinding_IdempotentEnsureRechecksAgentIdentity(t *testing.T) {
	mux, st := seededMux(t)
	const body = `{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/10 * * * *","binding_id":"ensure-collision","enabled":true}`
	if rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if _, err := st.Roles().Create(t.Context(), agentsmodule.RoleRecordCreate{WorkspaceKey: "WS", Name: "review"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.AgentServices().Create(t.Context(), agentsmodule.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: "ensure-collision", Name: "ensure-collision", RoleName: "review",
		Kind: agentsmodule.AgentKindEvent, DesiredState: agentsmodule.DesiredRunning, MaxInstances: 1,
	}); err != nil {
		t.Fatalf("create colliding agent fixture: %v", err)
	}

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already used by a durable agent record") {
		t.Fatalf("ensure status = %d body=%s, want clean canonical-agent 409", rec.Code, rec.Body.String())
	}
}

// This deterministically inserts the canonical Agent after the preflight
// check but before the post-create check. The handler must catch that
// interleaving and remove the newly-created enabled binding through
// Automation's fenced disable/delete commands.
func TestCreateBinding_PostCreateCollisionRollsBackBinding(t *testing.T) {
	_, st := seededMux(t)
	if _, err := st.Roles().Create(t.Context(), agentsmodule.RoleRecordCreate{WorkspaceKey: "WS", Name: "review"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	checker := &postCreateCollisionChecker{store: st}
	mux := muxWithIdentityChecker(t, st, checker)
	rec := do(
		t,
		mux,
		http.MethodPost,
		"/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/10 * * * *","binding_id":"raced-identity","enabled":true}`,
	)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already used by a durable agent record") {
		t.Fatalf("create status = %d body=%s, want post-create identity 409", rec.Code, rec.Body.String())
	}
	if checker.callCount() != 2 {
		t.Fatalf("identity checks = %d, want pre- and post-create checks", checker.callCount())
	}
	if _, err := st.TriggerBindings().Get(t.Context(), "WS", "raced-identity"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("binding after compensated race err = %v, want not found", err)
	}
	if _, err := st.AgentServices().Get(t.Context(), "WS", "raced-identity"); err != nil {
		t.Fatalf("colliding agent should remain authoritative: %v", err)
	}
}

func TestCreateBinding_EnsureRejectsImmutableIdentityMismatch(t *testing.T) {
	const original = `{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"epics.runs.create","source_kind":"http","binding_id":"b-fixed","entrypoint":"run","enabled":true}`
	tests := []struct {
		name string
		body string
	}{
		{
			name: "driver",
			body: `{"driver_id":"driver-2","driver_version_id":"version-1","route_key":"epics.runs.create","source_kind":"http","binding_id":"b-fixed","entrypoint":"run","enabled":true}`,
		},
		{
			name: "driver version",
			body: `{"driver_id":"driver-1","driver_version_id":"version-2","route_key":"epics.runs.create","source_kind":"http","binding_id":"b-fixed","entrypoint":"run","enabled":true}`,
		},
		{
			name: "unspecified driver version",
			body: `{"driver_id":"driver-1","route_key":"epics.runs.create","source_kind":"http","binding_id":"b-fixed","entrypoint":"run","enabled":true}`,
		},
		{
			name: "source kind",
			body: `{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"epics.runs.create","source_kind":"internal","binding_id":"b-fixed","entrypoint":"run","enabled":true}`,
		},
		{
			name: "source route",
			body: `{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"epics.runs.other","source_kind":"http","binding_id":"b-fixed","entrypoint":"run","enabled":true}`,
		},
		{
			name: "source event patterns",
			body: `{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"epics.runs.create","source_kind":"http","binding_id":"b-fixed","entrypoint":"run","event_type_patterns":["internal.task.ready"],"enabled":true}`,
		},
		{
			name: "entrypoint",
			body: `{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"epics.runs.create","source_kind":"http","binding_id":"b-fixed","entrypoint":"other","enabled":true}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mux, st := seededMux(t)
			if rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", original); rec.Code != http.StatusCreated {
				t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
			}

			rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", test.body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("ensure status = %d, want 409; body=%s", rec.Code, rec.Body.String())
			}
			binding, err := st.TriggerBindings().Get(t.Context(), "WS", "b-fixed")
			if err != nil {
				t.Fatalf("get binding: %v", err)
			}
			if binding.DriverID != "driver-1" || binding.DriverVersionID != "version-1" ||
				binding.SourceKind != "http" || binding.RouteKey != "epics.runs.create" ||
				binding.TargetEntrypoint != "run" {
				t.Fatalf("binding changed after rejected ensure: %+v", binding)
			}
		})
	}
}

func TestCreateBinding_EnsureResolvesWorkflowBeforeReusingBinding(t *testing.T) {
	initialMux, st := seededMux(t)
	const initial = `{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/10 * * * *","binding_id":"stable-review","enabled":true}`
	if rec := do(t, initialMux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", initial); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	preparer := &testWorkflowTargetPreparer{
		target: workflowbinding.WorkflowTarget{DriverID: "different-review", DriverVersionID: "different-review-v1"},
	}
	automationAPI := &testAutomationAPI{store: st}
	createWorkflow, err := workflowbinding.New(preparer, automationAPI)
	if err != nil {
		t.Fatalf("new workflow binding: %v", err)
	}
	mux := http.NewServeMux()
	New(Config{
		CreateWorkflow: createWorkflow,
		Commands:       automationAPI, Queries: automationAPI, ManualDispatch: automationAPI,
		OperatorAuthority: testOperatorResolver{}, WorkspaceFromContext: func(context.Context) string { return "WS" },
		Runs: readprojection.NewBindingRunReader(st.DriverRuns()), Connectors: &testConnectorLifecycle{store: st},
		AgentIdentities: testAgentIdentityChecker{store: st},
	}).Register(mux)

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"workflow":"different-review-agent","source_kind":"cron","schedule":"*/10 * * * *","binding_id":"stable-review","enabled":true}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("workflow ensure status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if preparer.calls != 1 || preparer.workflow != "different-review-agent" {
		t.Fatalf("preparer = %+v", preparer)
	}
}

// A gallery/scheduled-workflow activation starts from a workspace with no
// builtin Driver rows. The application workflow prepares that target before
// Automation creates the binding; a repeated browser ensure resolves the
// workflow again to prove immutable identity, then returns the same response
// shape with 200 without writing another binding or driver.
func TestCreateBinding_WorkflowTargetFreshStoreReturns201Then200(t *testing.T) {
	st := memstore.New()
	if _, err := st.Workspaces().Create(t.Context(), workspaceowner.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	automationAPI := &testAutomationAPI{store: st}
	materializations := 0
	preparer := &testWorkflowTargetPreparer{
		target: workflowbinding.WorkflowTarget{DriverID: "builtin-review", DriverVersionID: "builtin-review-v1"},
		prepare: func(ctx context.Context, workspace, workflow string) error {
			if workspace != "WS" || workflow != "github-review-agent" {
				return fmt.Errorf("unexpected target %s/%s", workspace, workflow)
			}
			if materializations > 0 {
				return nil
			}
			if _, err := st.Drivers().Create(ctx, workflowcatalog.DriverCreate{
				WorkspaceKey: workspace, DriverID: "builtin-review", Name: workflow,
				OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
			}); err != nil {
				return err
			}
			_, err := st.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
				WorkspaceKey: workspace, VersionID: "builtin-review-v1", DriverID: "builtin-review",
				Version: 1, SourceDigest: "sha256:builtin", BundleDigest: "sha256:bundle",
				ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
				AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
			})
			if err == nil {
				if _, err = st.ApproveDriverVersionForTest(ctx, workspace, "builtin-review", "builtin-review-v1"); err == nil {
					_, err = st.ActivateDriverVersionForTest(ctx, workspace, "builtin-review", "builtin-review-v1")
				}
				if err == nil {
					materializations++
				}
			}
			return err
		},
	}
	createWorkflow, err := workflowbinding.New(preparer, automationAPI)
	if err != nil {
		t.Fatalf("new workflow binding: %v", err)
	}
	mux := http.NewServeMux()
	New(Config{
		CreateWorkflow: createWorkflow,
		Commands:       automationAPI, Queries: automationAPI, ManualDispatch: automationAPI,
		OperatorAuthority: testOperatorResolver{}, WorkspaceFromContext: func(context.Context) string { return "WS" },
		Runs: readprojection.NewBindingRunReader(st.DriverRuns()), Connectors: &testConnectorLifecycle{store: st},
		AgentIdentities: testAgentIdentityChecker{store: st},
	}).Register(mux)
	body := `{"workflow":"github-review-agent","source_kind":"cron","schedule":"*/10 * * * *","binding_id":"fresh-review","enabled":true}`

	first := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("fresh workflow create status = %d, want 201; body=%s", first.Code, first.Body.String())
	}
	second := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body)
	if second.Code != http.StatusOK {
		t.Fatalf("workflow ensure status = %d, want 200; body=%s", second.Code, second.Body.String())
	}
	if preparer.calls != 2 || materializations != 1 {
		t.Fatalf("preparer calls = %d, materializations = %d, want 2/1", preparer.calls, materializations)
	}

	var created, ensured automation.Binding
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created binding: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &ensured); err != nil {
		t.Fatalf("decode ensured binding: %v", err)
	}
	if created.BindingID != "fresh-review" || created.DriverID != "builtin-review" || created.DriverVersionID != "builtin-review-v1" ||
		ensured.BindingID != created.BindingID || ensured.DriverID != created.DriverID || ensured.DriverVersionID != created.DriverVersionID {
		t.Fatalf("created=%+v ensured=%+v", created, ensured)
	}
}

func TestCreateBinding_CreateOnlyPreservesCLIDuplicateConflict(t *testing.T) {
	mux, _ := seededMux(t)
	body := `{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"epics.runs.create","source_kind":"http","binding_id":"b-strict","enabled":true}`
	if rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings?create_only=true", body); rec.Code != http.StatusCreated {
		t.Fatalf("first strict create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings?create_only=true", body); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate strict create status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateBinding_CronDerivesRouteKey pins the Phase-A fix: a cron binding
// needs no route_key — it is derived from the unique binding_id — so two
// scheduled workflows coexist in one workspace instead of colliding on a shared
// hand-picked route string.
func TestCreateBinding_CronDerivesRouteKey(t *testing.T) {
	mux, _ := seededMux(t)
	base := `"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/10 * * * *","enabled":true`

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{`+base+`,"binding_id":"s1-bug-fix"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("cron create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var b automation.Binding
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode binding: %v", err)
	}
	if b.RouteKey != "cron:s1-bug-fix" {
		t.Fatalf("derived route_key = %q, want cron:s1-bug-fix", b.RouteKey)
	}

	// A second scheduled workflow (different binding_id, no route_key) must
	// coexist — distinct derived routes, no 409 on the shared route.
	rec2 := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{`+base+`,"binding_id":"s2-review-loop"}`)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second cron create status = %d, want 201 (coexist); body=%s", rec2.Code, rec2.Body.String())
	}

	// Neither binding_id nor route_key is a 400 (nothing to address the binding).
	rec3 := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", `{`+base+`}`)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("cron without binding_id status = %d, want 400; body=%s", rec3.Code, rec3.Body.String())
	}
}

// TestCreateBinding_InternalDerivesRouteKey pins the WS2a fix: an internal-event
// binding needs no route_key — like cron it derives a unique 1:1 address from its
// binding_id — so several prompt-agent bindings can pattern-match the SAME event
// route (internal.task.ready) via event_type_patterns without colliding on the
// exact-owner route slot.
func TestCreateBinding_InternalDerivesRouteKey(t *testing.T) {
	mux, _ := seededMux(t)
	base := `"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"internal","event_type_patterns":["internal.task.ready"],"enabled":true`

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{`+base+`,"binding_id":"ts-planner","run_input":{"roleName":"plan","backend":"codex"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("internal create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var b automation.Binding
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode binding: %v", err)
	}
	if b.RouteKey != "internal:ts-planner" {
		t.Fatalf("derived route_key = %q, want internal:ts-planner", b.RouteKey)
	}
	if len(b.EventTypePatterns) != 1 || b.EventTypePatterns[0] != "internal.task.ready" {
		t.Fatalf("event_type_patterns = %v, want [internal.task.ready]", b.EventTypePatterns)
	}

	// A second prompt-agent binding on the SAME event route (different binding_id,
	// no route_key) must coexist — distinct derived routes, no 409 on the shared
	// event pattern.
	rec2 := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{`+base+`,"binding_id":"ts-coder","run_input":{"roleName":"task","backend":"codex"}}`)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second internal create status = %d, want 201 (coexist); body=%s", rec2.Code, rec2.Body.String())
	}

	// Neither binding_id nor route_key is a 400 (nothing to address the binding).
	rec3 := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", `{`+base+`}`)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("internal without binding_id/route_key status = %d, want 400; body=%s", rec3.Code, rec3.Body.String())
	}

	// An explicit route_key still wins (exact-owner opt-in), unchanged.
	rec4 := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{`+base+`,"binding_id":"ts-exact","route_key":"internal.task.ready"}`)
	if rec4.Code != http.StatusCreated {
		t.Fatalf("internal explicit-route create status = %d, want 201; body=%s", rec4.Code, rec4.Body.String())
	}
	var b4 automation.Binding
	if err := json.Unmarshal(rec4.Body.Bytes(), &b4); err != nil {
		t.Fatalf("decode binding: %v", err)
	}
	if b4.RouteKey != "internal.task.ready" {
		t.Fatalf("explicit route_key = %q, want internal.task.ready (not overridden by derivation)", b4.RouteKey)
	}
}

// TestCreateBinding_RunInputStoredOnSourceConfigRef pins ITEM D's plumbing: a
// prompt-agent binding created with a run_input object stores it on the binding's
// source_config_ref, where the dispatch source (CronScheduler) merges it into the
// fired run payload.
func TestCreateBinding_RunInputStoredOnSourceConfigRef(t *testing.T) {
	mux, _ := seededMux(t)
	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/10 * * * *","binding_id":"docs-agent","enabled":true,"run_input":{"roleName":"docs-assistant","backend":"codex"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var b automation.Binding
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode binding: %v", err)
	}
	var runInput map[string]string
	if err := json.Unmarshal([]byte(b.SourceConfigRef), &runInput); err != nil {
		t.Fatalf("source_config_ref %q is not the run-input JSON: %v", b.SourceConfigRef, err)
	}
	if runInput["roleName"] != "docs-assistant" || runInput["backend"] != "codex" {
		t.Fatalf("run-input round-trip = %v, want roleName+backend", runInput)
	}
}

// TestListBindings_NextFireAt pins the Phase-1 computed field: an enabled cron
// binding carries a future next_fire_at, while disabled cron and non-cron
// bindings omit it.
func TestListBindings_NextFireAt(t *testing.T) {
	mux, _ := seededMux(t)

	create := func(body string) {
		t.Helper()
		if rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body); rec.Code != http.StatusCreated {
			t.Fatalf("create status = %d; body=%s", rec.Code, rec.Body.String())
		}
	}
	create(`{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/5 * * * *","binding_id":"cron-enabled","enabled":true}`)
	create(`{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/5 * * * *","binding_id":"cron-disabled","enabled":false}`)
	create(`{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"github.pr.opened","source_kind":"http","binding_id":"http-enabled","enabled":true}`)

	rec := do(t, mux, http.MethodGet, "/api/workspaces/WS/trigger-bindings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Bindings []struct {
			BindingID  string     `json:"binding_id"`
			NextFireAt *time.Time `json:"next_fire_at"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	byID := map[string]*time.Time{}
	for _, b := range resp.Bindings {
		byID[b.BindingID] = b.NextFireAt
	}
	if len(byID) != 3 {
		t.Fatalf("listed bindings = %d, want 3 (%+v)", len(byID), resp.Bindings)
	}
	if next := byID["cron-enabled"]; next == nil || !next.After(time.Now()) {
		t.Fatalf("cron-enabled next_fire_at = %v, want a future instant", next)
	}
	if next := byID["cron-disabled"]; next != nil {
		t.Fatalf("cron-disabled next_fire_at = %v, want absent", next)
	}
	if next := byID["http-enabled"]; next != nil {
		t.Fatalf("http-enabled next_fire_at = %v, want absent", next)
	}
}

func TestCreateBinding_RequiresRouteKey(t *testing.T) {
	mux, _ := seededMux(t)
	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateBinding_GithubDoesNotCarryConnectorSecret(t *testing.T) {
	mux, _ := seededMux(t)
	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"workflow":"github-review-agent","route_key":"github.pull_request.opened","source_kind":"github","enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

func TestListBindingRuns_FiltersToBindingRuns(t *testing.T) {
	mux, st := seededMux(t)
	ctx := context.Background()
	seedBindingRecord(t, st, "agent-a")
	seedBindingRecord(t, st, "agent-b")
	seedDriverRun(t, st, "run-agent-a-1", "agent-a")
	seedDriverRun(t, st, "run-agent-b-1", "agent-b")
	seedDriverRun(t, st, "run-bare-1", "")
	seedDriverRun(t, st, "run-agent-a-2", "agent-a")

	rec := do(t, mux, http.MethodGet, "/api/workspaces/WS/trigger-bindings/agent-a/runs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list binding runs status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		BindingID string                      `json:"binding_id"`
		Runs      []execution.DriverRunRecord `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode binding runs: %v", err)
	}
	// Envelope is deliberately NOT driver-rooted (agent identity design §4.3):
	// binding_id + runs only; driver identity is per-run provenance.
	if out.BindingID != "agent-a" {
		t.Fatalf("metadata = %+v, want binding agent-a", out)
	}
	if len(out.Runs) != 2 {
		t.Fatalf("runs = %d (%+v), want only agent-a's 2 runs", len(out.Runs), out.Runs)
	}
	for _, run := range out.Runs {
		if run.TriggerBindingID != "agent-a" {
			t.Fatalf("run %s trigger_binding_id = %q, want agent-a", run.RunID, run.TriggerBindingID)
		}
	}
	if _, err := st.DriverRuns().Get(ctx, "WS", "run-bare-1"); err != nil {
		t.Fatalf("bare run setup vanished: %v", err)
	}
}

func TestListBindingRuns_UnknownBinding404(t *testing.T) {
	mux, _ := seededMux(t)
	rec := do(t, mux, http.MethodGet, "/api/workspaces/WS/trigger-bindings/missing/runs", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing binding status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestListBindingRuns_LimitHonored(t *testing.T) {
	mux, st := seededMux(t)
	seedBindingRecord(t, st, "agent-limited")
	seedDriverRun(t, st, "run-limited-1", "agent-limited")
	seedDriverRun(t, st, "run-limited-2", "agent-limited")
	seedDriverRun(t, st, "run-limited-3", "agent-limited")

	rec := do(t, mux, http.MethodGet, "/api/workspaces/WS/trigger-bindings/agent-limited/runs?limit=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("limited list status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Runs []execution.DriverRunRecord `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode limited runs: %v", err)
	}
	if len(out.Runs) != 2 {
		t.Fatalf("runs = %d (%+v), want limit 2", len(out.Runs), out.Runs)
	}
}

func seedBindingRecord(t *testing.T, st *memstore.Store, bindingID string) {
	t.Helper()
	if _, err := st.TriggerBindings().Create(context.Background(), automation.TriggerBindingCreate{
		WorkspaceKey:     "WS",
		BindingID:        bindingID,
		Name:             bindingID,
		SourceKind:       automation.CronSourceKind,
		RouteKey:         "cron:" + bindingID,
		DriverID:         "driver-1",
		DriverVersionID:  "version-1",
		Enabled:          true,
		Schedule:         "*/10 * * * *",
		ScheduleTimezone: "UTC",
	}); err != nil {
		t.Fatalf("seed binding %s: %v", bindingID, err)
	}
}

func seedDriverRun(t *testing.T, st *memstore.Store, runID, bindingID string) {
	t.Helper()
	if _, err := st.DriverRuns().Create(context.Background(), execution.DriverRunCreate{
		WorkspaceKey:     "WS",
		RunID:            runID,
		DriverID:         "driver-1",
		DriverVersionID:  "version-1",
		TriggerBindingID: bindingID,
		SourceKind:       "test",
		SourceRef:        "test:" + runID,
		IdempotencyKey:   "",
		Payload:          nil,
	}); err != nil {
		t.Fatalf("seed run %s: %v", runID, err)
	}
}

// --- Phase 3: PATCH / DELETE / failure health ---

// createCronBinding is a test helper that creates an enabled cron binding under
// driver-1 and returns its id.
func createCronBinding(t *testing.T, mux *http.ServeMux, bindingID, schedule string) {
	t.Helper()
	body := `{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"` +
		schedule + `","binding_id":"` + bindingID + `","name":"` + bindingID + `","enabled":true}`
	if rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body); rec.Code != http.StatusCreated {
		t.Fatalf("create cron binding %s: status = %d; body=%s", bindingID, rec.Code, rec.Body.String())
	}
}

// TestPatchBinding_RenameAndReschedule pins the PATCH happy path: name +
// schedule change apply, and next_fire_at is recomputed from the new schedule.
func TestPatchBinding_RenameAndReschedule(t *testing.T) {
	mux, _ := seededMux(t)
	createCronBinding(t, mux, "s1", "*/10 * * * *")

	rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/s1",
		`{"name":"renamed","schedule":"0 9 * * *","schedule_timezone":"UTC"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Name       string     `json:"name"`
		Schedule   string     `json:"schedule"`
		NextFireAt *time.Time `json:"next_fire_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Name != "renamed" || out.Schedule != "0 9 * * *" {
		t.Fatalf("patch did not apply: %+v", out)
	}
	if out.NextFireAt == nil || !out.NextFireAt.After(time.Now()) {
		t.Fatalf("next_fire_at not recomputed to a future instant: %v", out.NextFireAt)
	}
}

func TestPatchBinding_ReconcilesRunInput(t *testing.T) {
	mux, st := seededMux(t)
	createCronBinding(t, mux, "s1", "*/10 * * * *")

	rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/s1",
		`{"run_input":{"targetRepo":"alpha","githubRepo":"acme/alpha"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	binding, err := st.TriggerBindings().Get(t.Context(), "WS", "s1")
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	var runInput map[string]string
	if err := json.Unmarshal([]byte(binding.SourceConfigRef), &runInput); err != nil {
		t.Fatalf("decode source_config_ref %q: %v", binding.SourceConfigRef, err)
	}
	if runInput["targetRepo"] != "alpha" || runInput["githubRepo"] != "acme/alpha" {
		t.Fatalf("run input = %#v, want reconciled repo fields", runInput)
	}
}

func TestPatchBinding_RejectsNonObjectRunInput(t *testing.T) {
	mux, _ := seededMux(t)
	createCronBinding(t, mux, "s1", "*/10 * * * *")

	for _, body := range []string{
		`{"run_input":"alpha"}`,
		`{"run_input":["alpha"]}`,
		`{"run_input":null}`,
	} {
		rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/s1", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("patch %s status = %d, want 400; body=%s", body, rec.Code, rec.Body.String())
		}
	}
}

// TestPatchBinding_ScheduleOnNonCron400 rejects a schedule change on a non-cron
// binding: an http binding fires by route, not schedule.
func TestPatchBinding_ScheduleOnNonCron400(t *testing.T) {
	mux, _ := seededMux(t)
	if rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"github.pr.opened","source_kind":"http","binding_id":"h1","enabled":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("create http binding: %d; body=%s", rec.Code, rec.Body.String())
	}
	rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/h1", `{"schedule":"*/5 * * * *"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch schedule on http binding status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPatchBinding_InvalidSchedule400 rejects a malformed cron expression.
func TestPatchBinding_InvalidSchedule400(t *testing.T) {
	mux, _ := seededMux(t)
	createCronBinding(t, mux, "s1", "*/10 * * * *")
	rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/s1", `{"schedule":"not a cron"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch invalid schedule status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPatchBinding_Errors covers the not-found and empty-patch guards.
func TestPatchBinding_Errors(t *testing.T) {
	mux, _ := seededMux(t)
	createCronBinding(t, mux, "s1", "*/10 * * * *")

	if rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/missing", `{"name":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("patch missing binding status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/s1", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestDeleteBinding_GoneAndGrantsRevoked pins Decision 6: deleting a binding
// removes it AND revokes its connector grants (no orphaned credentials).
func TestDeleteBinding_GoneAndGrantsRevoked(t *testing.T) {
	mux, st := seededMux(t)
	ctx := context.Background()
	createCronBinding(t, mux, "s2", "*/10 * * * *")

	// Seed two active grants for the binding (memstore grants need no connector FK).
	for i, action := range []string{"github.pull_request.read", "github.compare.read"} {
		if _, err := st.Connectors().CreateManagementGrant(ctx, connectorsmodule.CreateGrantMutation{
			WorkspaceKey:    "WS",
			GrantID:         "grant-" + string(rune('a'+i)),
			ConnectorID:     "github",
			BindingID:       "s2",
			Action:          action,
			ResourcePattern: "repo:o/r",
		}); err != nil {
			t.Fatalf("seed grant %d: %v", i, err)
		}
	}

	rec := do(t, mux, http.MethodDelete, "/api/workspaces/WS/trigger-bindings/s2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Deleted       bool `json:"deleted"`
		GrantsRevoked int  `json:"grants_revoked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode delete: %v", err)
	}
	if !out.Deleted || out.GrantsRevoked != 2 {
		t.Fatalf("unexpected delete result: %+v", out)
	}
	// Binding is gone.
	if _, err := st.TriggerBindings().Get(ctx, "WS", "s2"); err == nil {
		t.Fatalf("binding s2 still present after delete")
	}
	// Grants are revoked (ListByBinding excludes revoked grants).
	grants, err := st.Connectors().ListGrantRecordsByBinding(ctx, "WS", "s2")
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected 0 active grants after delete, got %d", len(grants))
	}
	// A lost 200 response is safe to retry after the binding is already gone.
	rec = do(t, mux, http.MethodDelete, "/api/workspaces/WS/trigger-bindings/s2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("retry delete status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode retry delete: %v", err)
	}
	if !out.Deleted || out.GrantsRevoked != 0 {
		t.Fatalf("unexpected retry delete result: %+v", out)
	}
}

// TestDeleteBinding_MissingConverges pins idempotent DELETE semantics even
// when the caller cannot distinguish an original miss from a lost response.
func TestDeleteBinding_MissingConverges(t *testing.T) {
	mux, st := seededMux(t)
	if _, err := st.Connectors().CreateManagementGrant(t.Context(), connectorsmodule.CreateGrantMutation{
		WorkspaceKey: "WS", GrantID: "orphan-grant", ConnectorID: "github", BindingID: "missing",
		Action: "github.pull_request.read", ResourcePattern: "repo:o/r",
	}); err != nil {
		t.Fatalf("seed orphan grant: %v", err)
	}
	rec := do(t, mux, http.MethodDelete, "/api/workspaces/WS/trigger-bindings/missing", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete missing status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out DeleteBindingResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode missing delete: %v", err)
	}
	if !out.Deleted || out.GrantsRevoked != 1 {
		t.Fatalf("missing delete result = %+v, want deleted with orphan revoked", out)
	}
}

// TestListBindings_FailureHealth pins Decision 7 inputs: consecutive_failures
// counts failed runs from newest until a clean run, skipping in-flight runs,
// and last_run_status is the newest run's status. Health is computed from the
// runs stamped with the binding's id (trigger-dispatch provenance), NOT from
// every run of the binding's driver — a second binding sharing driver-1 must
// not absorb s1's failures.
func TestListBindings_FailureHealth(t *testing.T) {
	mux, st := seededMux(t)
	ctx := context.Background()
	createCronBinding(t, mux, "s1", "*/10 * * * *")
	createCronBinding(t, mux, "s2-shares-driver", "*/20 * * * *")

	// Claim order stamps strictly increasing StartedAt, so newest-first is
	// D(running) > C(failed) > B(failed) > A(completed).
	seed := func(runID, bindingID string, status execution.DriverRunStatus, finish bool) {
		t.Helper()
		if _, err := st.DriverRuns().Create(ctx, execution.DriverRunCreate{
			WorkspaceKey: "WS", RunID: runID, DriverID: "driver-1", DriverVersionID: "version-1",
			TriggerBindingID: bindingID,
		}); err != nil {
			t.Fatalf("create run %s: %v", runID, err)
		}
		run, err := st.DriverRuns().Claim(ctx, "WS", runID, "node-1", "lease-"+runID)
		if err != nil {
			t.Fatalf("claim run %s: %v", runID, err)
		}
		if !finish {
			return
		}
		if _, err := st.DriverRuns().Finish(ctx, "WS", runID, execution.DriverRunFinish{
			NodeID: run.NodeID, LeaseID: run.LeaseID, FencingToken: run.FencingToken, Status: status,
		}); err != nil {
			t.Fatalf("finish run %s: %v", runID, err)
		}
	}
	seed("A", "s1", execution.DriverRunCompleted, true)
	seed("B", "s1", execution.DriverRunFailed, true)
	seed("C", "s1", execution.DriverRunFailed, true)
	seed("D", "s1", execution.DriverRunRunning, false) // in-flight, must be skipped

	rec := do(t, mux, http.MethodGet, "/api/workspaces/WS/trigger-bindings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Bindings []struct {
			BindingID           string `json:"binding_id"`
			LastRunStatus       string `json:"last_run_status"`
			ConsecutiveFailures int    `json:"consecutive_failures"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var foundS1, foundS2 bool
	for _, b := range resp.Bindings {
		switch b.BindingID {
		case "s1":
			foundS1 = true
			if b.ConsecutiveFailures != 2 {
				t.Fatalf("consecutive_failures = %d, want 2 (D running skipped, C+B failed, A completed breaks)", b.ConsecutiveFailures)
			}
			if b.LastRunStatus != string(execution.DriverRunRunning) {
				t.Fatalf("last_run_status = %q, want running", b.LastRunStatus)
			}
		case "s2-shares-driver":
			foundS2 = true
			// Shares driver-1 but owns none of the seeded runs: health must not
			// bleed across bindings that share a driver.
			if b.LastRunStatus != "" || b.ConsecutiveFailures != 0 {
				t.Fatalf("s2-shares-driver health = (%q, %d), want empty — driver-mates must not bleed metrics", b.LastRunStatus, b.ConsecutiveFailures)
			}
		}
	}
	if !foundS1 || !foundS2 {
		t.Fatalf("bindings missing from list response: s1=%v s2-shares-driver=%v", foundS1, foundS2)
	}
}
