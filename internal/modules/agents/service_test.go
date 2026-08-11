package agents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type fakePorts struct {
	getAgent         func(context.Context, string, string) (*Agent, error)
	listAgents       func(context.Context, string, AgentFilter) ([]*Agent, error)
	getRole          func(context.Context, string, string) (*RoleReference, error)
	getFullRole      func(context.Context, string, string) (*Role, error)
	listRoles        func(context.Context, string) ([]*Role, error)
	createRole       func(context.Context, string, RoleDefinition) (*Role, error)
	updateRole       func(context.Context, UpdateRoleMutation) (*Role, error)
	deleteRole       func(context.Context, DeleteRoleMutation) error
	createAgent      func(context.Context, CreateAgentMutation) (*Agent, error)
	updateAgent      func(context.Context, UpdateAgentMutation) (*Agent, error)
	archiveAgent     func(context.Context, ArchiveAgentMutation) (*Agent, error)
	setDesired       func(context.Context, DesiredStateMutation) (*Agent, error)
	setDesiredOwned  func(context.Context, OwnedDesiredStateMutation) (*Agent, error)
	acquireOwnership func(context.Context, AcquireOwnershipMutation) (*OwnershipGrant, error)
	getOwnership     func(context.Context, string, string) (*AgentOwnershipLease, error)
	listOwnership    func(context.Context, string, OwnershipFilter) ([]*AgentOwnershipLease, error)
	renewOwnership   func(context.Context, RenewOwnershipMutation) (*AgentOwnershipLease, error)
	releaseOwnership func(context.Context, OwnershipProof) (*AgentOwnershipLease, error)
}

func (*fakePorts) ApplyLifecycle(context.Context, ApplyLifecycleMutation) (*LifecycleResult, error) {
	return nil, nil
}

func (*fakePorts) ListAgentBindingStates(context.Context, string, string) ([]bool, error) {
	return nil, nil
}

func (f *fakePorts) GetAgent(ctx context.Context, workspace, agentID string) (*Agent, error) {
	return f.getAgent(ctx, workspace, agentID)
}

func (f *fakePorts) ListAgents(ctx context.Context, workspace string, filter AgentFilter) ([]*Agent, error) {
	return f.listAgents(ctx, workspace, filter)
}

func (f *fakePorts) GetRoleReference(ctx context.Context, workspace, roleName string) (*RoleReference, error) {
	return f.getRole(ctx, workspace, roleName)
}

func (f *fakePorts) GetRole(ctx context.Context, workspace, roleName string) (*Role, error) {
	return f.getFullRole(ctx, workspace, roleName)
}

func (f *fakePorts) CreateRole(ctx context.Context, workspace string, role RoleDefinition) (*Role, error) {
	return f.createRole(ctx, workspace, role)
}

func (f *fakePorts) ListRoles(ctx context.Context, workspace string) ([]*Role, error) {
	return f.listRoles(ctx, workspace)
}

func (f *fakePorts) UpdateRole(ctx context.Context, mutation UpdateRoleMutation) (*Role, error) {
	return f.updateRole(ctx, mutation)
}

func (f *fakePorts) DeleteRole(ctx context.Context, mutation DeleteRoleMutation) error {
	return f.deleteRole(ctx, mutation)
}

func (f *fakePorts) CreateAgent(ctx context.Context, mutation CreateAgentMutation) (*Agent, error) {
	return f.createAgent(ctx, mutation)
}

func (f *fakePorts) UpdateAgent(ctx context.Context, mutation UpdateAgentMutation) (*Agent, error) {
	return f.updateAgent(ctx, mutation)
}

func (f *fakePorts) ArchiveAgent(ctx context.Context, mutation ArchiveAgentMutation) (*Agent, error) {
	return f.archiveAgent(ctx, mutation)
}

func (f *fakePorts) SetDesiredState(ctx context.Context, mutation DesiredStateMutation) (*Agent, error) {
	return f.setDesired(ctx, mutation)
}

func (f *fakePorts) SetDesiredStateOwned(ctx context.Context, mutation OwnedDesiredStateMutation) (*Agent, error) {
	return f.setDesiredOwned(ctx, mutation)
}

func (f *fakePorts) AcquireOwnership(ctx context.Context, mutation AcquireOwnershipMutation) (*OwnershipGrant, error) {
	return f.acquireOwnership(ctx, mutation)
}

func (f *fakePorts) GetOwnership(ctx context.Context, workspace, agentID string) (*AgentOwnershipLease, error) {
	return f.getOwnership(ctx, workspace, agentID)
}

func (f *fakePorts) ListOwnership(ctx context.Context, workspace string, filter OwnershipFilter) ([]*AgentOwnershipLease, error) {
	return f.listOwnership(ctx, workspace, filter)
}

func (f *fakePorts) RenewOwnership(ctx context.Context, mutation RenewOwnershipMutation) (*AgentOwnershipLease, error) {
	return f.renewOwnership(ctx, mutation)
}

func (f *fakePorts) ReleaseOwnership(ctx context.Context, proof OwnershipProof) (*AgentOwnershipLease, error) {
	return f.releaseOwnership(ctx, proof)
}

func TestNewWithLifecycleRequiresCompleteAgentsBoundary(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	ports := &fakePorts{}
	if _, err := NewWithLifecycle(ports, ports, ports, ports, ports, ports, ports, ports, admission); err != nil {
		t.Fatalf("NewWithLifecycle() with all ports: %v", err)
	}
	if _, err := NewWithLifecycle(ports, nil, ports, ports, ports, ports, ports, ports, admission); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("NewWithLifecycle() missing Role port error = %v, want unavailable", err)
	}
	if _, err := NewWithLifecycle(ports, ports, ports, ports, ports, ports, ports, ports, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("NewWithLifecycle() missing admission error = %v, want unavailable", err)
	}
}

func TestRoleCommandsRequireOperatorAuthorityAndPreserveRevisionCAS(t *testing.T) {
	const workspace = "WS"
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)

	createdRole := &Role{
		WorkspaceKey: workspace, Name: "docs", Kind: "worker",
		Description: "Documentation", CreatedAt: now, UpdatedAt: now,
	}
	ports.createRole = func(_ context.Context, gotWorkspace string, definition RoleDefinition) (*Role, error) {
		if gotWorkspace != workspace || definition.Name != "docs" || definition.Description != "Documentation" {
			t.Fatalf("create role input = %q/%+v", gotWorkspace, definition)
		}
		return createdRole, nil
	}
	createAuth := issueOperator(t, issuer, workspace, "operator-a", ActionCreateRole)
	created, err := service.CreateRole(t.Context(), createAuth, CreateRoleCommand{
		WorkspaceKey: workspace,
		Role:         RoleDefinition{Name: "docs", Kind: "worker", Description: "Documentation"},
	})
	if err != nil {
		t.Fatal(err)
	}
	created.Description = "caller mutation"
	if createdRole.Description != "Documentation" {
		t.Fatal("CreateRole leaked durable Role pointer")
	}

	description := "Updated documentation"
	var updateMutation UpdateRoleMutation
	ports.updateRole = func(_ context.Context, mutation UpdateRoleMutation) (*Role, error) {
		updateMutation = mutation
		result := *createdRole
		result.Description = description
		result.UpdatedAt = now.Add(time.Second)
		return &result, nil
	}
	updateAuth := issueOperator(t, issuer, workspace, "operator-a", ActionUpdateRole)
	updated, err := service.UpdateRole(t.Context(), updateAuth, UpdateRoleCommand{
		WorkspaceKey: workspace, RoleName: "docs", ExpectedUpdatedAt: now,
		Patch: RolePatch{Description: &description},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updateMutation.WorkspaceKey != workspace || updateMutation.RoleName != "docs" ||
		!updateMutation.ExpectedUpdatedAt.Equal(now) || updateMutation.UpdatedBy != "operator-a" ||
		updateMutation.Patch.Description == nil || *updateMutation.Patch.Description != description {
		t.Fatalf("update role mutation = %+v", updateMutation)
	}
	if updated.Description != description || !updated.UpdatedAt.After(now) {
		t.Fatalf("updated role = %+v", updated)
	}

	var deleteMutation DeleteRoleMutation
	ports.deleteRole = func(_ context.Context, mutation DeleteRoleMutation) error {
		deleteMutation = mutation
		return nil
	}
	deleteAuth := issueOperator(t, issuer, workspace, "operator-a", ActionDeleteRole)
	if err := service.DeleteRole(t.Context(), deleteAuth, DeleteRoleCommand{
		WorkspaceKey: workspace, RoleName: "docs", ExpectedUpdatedAt: updated.UpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if deleteMutation.WorkspaceKey != workspace || deleteMutation.RoleName != "docs" ||
		!deleteMutation.ExpectedUpdatedAt.Equal(updated.UpdatedAt) || deleteMutation.DeletedBy != "operator-a" {
		t.Fatalf("delete role mutation = %+v", deleteMutation)
	}

	wrongAuth := issueOperator(t, issuer, workspace, "operator-a", ActionCreateAgent)
	if _, err := service.CreateRole(t.Context(), wrongAuth, CreateRoleCommand{
		WorkspaceKey: workspace, Role: RoleDefinition{Name: "other"},
	}); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-action CreateRole error = %v, want admission denied", err)
	}
}

func TestRoleQueriesValidateAndClonePersistenceResults(t *testing.T) {
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, _ := newTestService(t, ports)
	persisted := &Role{WorkspaceKey: "WS", Name: "docs", CreatedAt: now, UpdatedAt: now}
	ports.getFullRole = func(context.Context, string, string) (*Role, error) {
		return persisted, nil
	}
	ports.listRoles = func(context.Context, string) ([]*Role, error) {
		return []*Role{persisted}, nil
	}

	got, err := service.GetRole(t.Context(), "WS", "docs")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := service.ListRoles(t.Context(), "WS")
	if err != nil {
		t.Fatal(err)
	}
	got.Name = "mutated"
	listed[0].Name = "also-mutated"
	if persisted.Name != "docs" {
		t.Fatal("Role query leaked durable pointer")
	}
}

func TestCreateAgentOwnsIdentityAndValidatesRoleReference(t *testing.T) {
	const workspace = "WS"
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueOperator(t, issuer, workspace, "operator-a", ActionCreateAgent)

	var gotRole string
	ports.getRole = func(_ context.Context, gotWorkspace, roleName string) (*RoleReference, error) {
		if gotWorkspace != workspace {
			t.Fatalf("role workspace = %q", gotWorkspace)
		}
		gotRole = roleName
		return &RoleReference{WorkspaceKey: workspace, RoleName: roleName}, nil
	}
	var gotMutation CreateAgentMutation
	var persisted *Agent
	ports.createAgent = func(_ context.Context, mutation CreateAgentMutation) (*Agent, error) {
		gotMutation = mutation
		persisted = &Agent{
			WorkspaceKey: mutation.WorkspaceKey, AgentID: mutation.AgentID,
			GenerationID: "00112233445566778899aabbccddeeff",
			Name:         mutation.Name, Kind: mutation.Kind, Behavior: mutation.Behavior,
			DesiredState: mutation.DesiredState, PlacementPolicy: mutation.PlacementPolicy,
			MaxInstances: mutation.MaxInstances, RestartPolicy: mutation.RestartPolicy,
			BudgetPolicy: mutation.BudgetPolicy, CreatedBy: mutation.CreatedBy,
			CreatedAt: now, UpdatedAt: now,
		}
		return persisted, nil
	}

	got, err := service.CreateAgent(t.Context(), auth, CreateAgentCommand{
		WorkspaceKey: workspace, AgentID: "agt-docs", Name: "Docs review",
		Kind: AgentKindEvent, Behavior: BehaviorReference{RoleName: "docs"},
		DesiredState: DesiredRunning, PlacementPolicy: "local",
		RestartPolicy: "always", BudgetPolicy: "daily:10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotRole != "docs" {
		t.Fatalf("role preflight = %q", gotRole)
	}
	if gotMutation.CreatedBy != "operator-a" || gotMutation.MaxInstances != 1 {
		t.Fatalf("create mutation = %#v", gotMutation)
	}
	got.Name = "mutated by caller"
	if persisted.Name != "Docs review" {
		t.Fatal("service leaked durable Agent pointer")
	}
}

func TestCreateAgentRejectsAmbiguousBehaviorBeforePersistence(t *testing.T) {
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueOperator(t, issuer, "WS", "operator-a", ActionCreateAgent)
	called := false
	ports.createAgent = func(context.Context, CreateAgentMutation) (*Agent, error) {
		called = true
		return nil, nil
	}

	_, err := service.CreateAgent(t.Context(), auth, CreateAgentCommand{
		WorkspaceKey: "WS", AgentID: "agt-docs", Name: "Docs",
		Kind: AgentKindEvent, DesiredState: DesiredRunning, MaxInstances: 1,
		Behavior: BehaviorReference{
			RoleName: "docs", DriverID: "driver", DriverVersionID: "version",
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateAgent() error = %v, want invalid", err)
	}
	if called {
		t.Fatal("invalid behavior reached durable persistence")
	}
}

func TestUpdateAgentDerivesTrustedAuditActor(t *testing.T) {
	const workspace = "WS"
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueOperator(t, issuer, workspace, "operator-a", ActionUpdateAgent)
	name := "Updated docs"
	var mutation UpdateAgentMutation
	ports.updateAgent = func(_ context.Context, got UpdateAgentMutation) (*Agent, error) {
		mutation = got
		result := testAgent(now, DesiredRunning)
		result.Name = name
		result.UpdatedAt = now.Add(time.Second)
		return result, nil
	}

	result, err := service.UpdateAgent(t.Context(), auth, UpdateAgentCommand{
		WorkspaceKey: workspace, AgentID: "agt-docs", ExpectedUpdatedAt: now,
		Patch: AgentPatch{Name: &name},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mutation.WorkspaceKey != workspace || mutation.AgentID != "agt-docs" ||
		!mutation.ExpectedUpdatedAt.Equal(now) || mutation.UpdatedBy != "operator-a" ||
		mutation.Patch.Name == nil || *mutation.Patch.Name != name {
		t.Fatalf("update mutation = %#v", mutation)
	}
	if result.Name != name {
		t.Fatalf("updated name = %q", result.Name)
	}
}

func TestSetDesiredStateUsesExplicitIntentCAS(t *testing.T) {
	const workspace = "WS"
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueOperator(t, issuer, workspace, "operator-a", ActionSetDesiredState)
	var got DesiredStateMutation
	ports.setDesired = func(_ context.Context, mutation DesiredStateMutation) (*Agent, error) {
		got = mutation
		result := testAgent(now, DesiredPaused)
		result.UpdatedAt = now.Add(time.Second)
		return result, nil
	}

	result, err := service.SetDesiredState(t.Context(), auth, SetDesiredStateCommand{
		WorkspaceKey: workspace, AgentID: "agt-docs",
		ExpectedState: DesiredRunning, DesiredState: DesiredPaused,
		ExpectedUpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkspaceKey != workspace || got.AgentID != "agt-docs" ||
		got.ExpectedState != DesiredRunning || got.DesiredState != DesiredPaused ||
		!got.ExpectedUpdatedAt.Equal(now) || got.ChangedBy != "operator-a" {
		t.Fatalf("desired-state mutation = %#v", got)
	}
	if result.DesiredState != DesiredPaused {
		t.Fatalf("desired state = %q", result.DesiredState)
	}
}

func TestSetDesiredStateOwnedCarriesExactOwnershipFence(t *testing.T) {
	const workspace = "WS"
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueSystem(t, issuer, workspace, "agent-controller-a", ActionSetDesiredStateOwned)
	proof := validProof()
	var got OwnedDesiredStateMutation
	ports.setDesiredOwned = func(_ context.Context, mutation OwnedDesiredStateMutation) (*Agent, error) {
		got = mutation
		return testAgent(now, DesiredPaused), nil
	}

	result, err := service.SetDesiredStateOwned(t.Context(), auth, proof, SetDesiredStateOwnedCommand{
		ExpectedState: DesiredRunning, DesiredState: DesiredPaused,
		ExpectedUpdatedAt: now, IdempotencyKey: "reconcile-agt-docs-7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Ownership != proof || got.ExpectedState != DesiredRunning ||
		got.DesiredState != DesiredPaused || !got.ExpectedUpdatedAt.Equal(now) ||
		got.IdempotencyKey != "reconcile-agt-docs-7" {
		t.Fatalf("owned mutation = %#v", got)
	}
	if result.DesiredState != DesiredPaused {
		t.Fatalf("desired state = %q", result.DesiredState)
	}
}

func TestSetDesiredStateOwnedRejectsAuthorityConversionAndStaleFence(t *testing.T) {
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueSystem(t, issuer, "WS", "different-controller", ActionSetDesiredStateOwned)
	expectedUpdatedAt := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	called := false
	ports.setDesiredOwned = func(context.Context, OwnedDesiredStateMutation) (*Agent, error) {
		called = true
		return nil, nil
	}

	_, err := service.SetDesiredStateOwned(t.Context(), auth, validProof(), SetDesiredStateOwnedCommand{
		ExpectedState: DesiredRunning, DesiredState: DesiredPaused,
		ExpectedUpdatedAt: expectedUpdatedAt, IdempotencyKey: "attempt-1",
	})
	if !errors.Is(err, ErrNotOwner) {
		t.Fatalf("wrong owner error = %v, want not owner", err)
	}
	if called {
		t.Fatal("wrong-owner authority reached durable command")
	}

	auth = issueSystem(t, issuer, "WS", "agent-controller-a", ActionSetDesiredStateOwned)
	ports.setDesiredOwned = func(context.Context, OwnedDesiredStateMutation) (*Agent, error) {
		return nil, ErrNotOwner
	}
	_, err = service.SetDesiredStateOwned(t.Context(), auth, validProof(), SetDesiredStateOwnedCommand{
		ExpectedState: DesiredRunning, DesiredState: DesiredPaused,
		ExpectedUpdatedAt: expectedUpdatedAt, IdempotencyKey: "attempt-2",
	})
	if !errors.Is(err, ErrNotOwner) {
		t.Fatalf("stale fence error = %v, want not owner", err)
	}
}

func TestSetDesiredStateOwnedRejectsDivergentDurableResult(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueSystem(t, issuer, "WS", "agent-controller-a", ActionSetDesiredStateOwned)
	ports.setDesiredOwned = func(context.Context, OwnedDesiredStateMutation) (*Agent, error) {
		return testAgent(now, DesiredRunning), nil
	}

	_, err := service.SetDesiredStateOwned(t.Context(), auth, validProof(), SetDesiredStateOwnedCommand{
		ExpectedState: DesiredRunning, DesiredState: DesiredPaused,
		ExpectedUpdatedAt: now, IdempotencyKey: "attempt-1",
	})
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("divergent result error = %v, want invalid persisted state", err)
	}
}

func TestOwnershipLifecycleDerivesOwnerAndKeepsTokenOpaque(t *testing.T) {
	const workspace = "WS"
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	acquireAuth := issueSystem(t, issuer, workspace, "agent-controller-a", ActionAcquireOwnership)
	var acquired AcquireOwnershipMutation
	ports.acquireOwnership = func(_ context.Context, mutation AcquireOwnershipMutation) (*OwnershipGrant, error) {
		acquired = mutation
		return &OwnershipGrant{
			Lease:      testLease(now, OwnershipActive),
			LeaseToken: "raw-secret-token",
		}, nil
	}

	grant, err := service.AcquireOwnership(t.Context(), acquireAuth, AcquireOwnershipCommand{
		WorkspaceKey: workspace, AgentID: "agt-docs", LeaseID: "lease-7",
		RuntimeProvider: RuntimeProviderLocal, NodeID: "node-a", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if acquired.OwnerID != "agent-controller-a" || acquired.TTL != time.Minute {
		t.Fatalf("acquire mutation = %#v", acquired)
	}
	wire, err := json.Marshal(grant)
	if err != nil {
		t.Fatal(err)
	}
	if string(wire) == "" || strings.Contains(string(wire), "raw-secret-token") {
		t.Fatalf("ownership grant leaked token: %s", wire)
	}
	proofWire, err := json.Marshal(validProof())
	if err != nil {
		t.Fatal(err)
	}
	if string(proofWire) != "{}" {
		t.Fatalf("ownership proof crossed JSON boundary: %s", proofWire)
	}

	proof := validProof()
	renewAuth := issueSystem(t, issuer, workspace, "agent-controller-a", ActionRenewOwnership)
	ports.renewOwnership = func(_ context.Context, mutation RenewOwnershipMutation) (*AgentOwnershipLease, error) {
		if mutation.Ownership != proof || mutation.TTL != 2*time.Minute {
			t.Fatalf("renew mutation = %#v", mutation)
		}
		return testLease(now.Add(time.Minute), OwnershipActive), nil
	}
	if _, err := service.RenewOwnership(t.Context(), renewAuth, proof, 2*time.Minute); err != nil {
		t.Fatal(err)
	}

	releaseAuth := issueSystem(t, issuer, workspace, "agent-controller-a", ActionReleaseOwnership)
	ports.releaseOwnership = func(_ context.Context, got OwnershipProof) (*AgentOwnershipLease, error) {
		if got != proof {
			t.Fatalf("release proof = %#v", got)
		}
		return testLease(now.Add(2*time.Minute), OwnershipReleased), nil
	}
	if _, err := service.ReleaseOwnership(t.Context(), releaseAuth, proof); err != nil {
		t.Fatal(err)
	}
}

func TestListAgentsRejectsRecordsOutsideFilter(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, _ := newTestService(t, ports)
	ports.listAgents = func(context.Context, string, AgentFilter) ([]*Agent, error) {
		return []*Agent{testAgent(now, DesiredRunning)}, nil
	}
	_, err := service.ListAgents(t.Context(), "WS", AgentFilter{DesiredState: DesiredPaused})
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("ListAgents() error = %v, want invalid persisted state", err)
	}
}

func newTestService(t *testing.T, ports *fakePorts) (*Service, *authority.Issuer) {
	t.Helper()
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewWithLifecycle(ports, ports, ports, ports, ports, ports, ports, ports, admission)
	if err != nil {
		t.Fatal(err)
	}
	return service, issuer
}

func issueOperator(
	t *testing.T,
	issuer *authority.Issuer,
	workspace, subject string,
	action authority.Action,
) authority.OperatorAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: subject, Class: authority.ClassOperator, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := issuer.IssueOperator(principal, workspace, action)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func issueSystem(
	t *testing.T,
	issuer *authority.Issuer,
	workspace, subject string,
	action authority.Action,
) authority.SystemAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: subject, Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := issuer.IssueSystem(principal, workspace, action, "agents test")
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func validProof() OwnershipProof {
	return OwnershipProof{
		WorkspaceKey: "WS", AgentID: "agt-docs", LeaseID: "lease-7",
		LeaseToken: "raw-secret-token", OwnerID: "agent-controller-a",
		RuntimeProvider: RuntimeProviderLocal, NodeID: "node-a", FencingToken: 7,
	}
}

func testAgent(now time.Time, state DesiredState) *Agent {
	return &Agent{
		WorkspaceKey: "WS", AgentID: "agt-docs", Name: "Docs review",
		GenerationID: "00112233445566778899aabbccddeeff",
		Kind:         AgentKindEvent, Behavior: BehaviorReference{RoleName: "docs"},
		DesiredState: state, MaxInstances: 1, CreatedBy: "operator-a",
		CreatedAt: now, UpdatedAt: now,
	}
}

func testLease(now time.Time, status OwnershipStatus) *AgentOwnershipLease {
	return &AgentOwnershipLease{
		WorkspaceKey: "WS", AgentID: "agt-docs", LeaseID: "lease-7",
		OwnerID: "agent-controller-a", RuntimeProvider: RuntimeProviderLocal,
		NodeID: "node-a", FencingToken: 7, Status: status,
		ExpiresAt: now.Add(5 * time.Minute), LastHeartbeat: now,
		CreatedAt: now, UpdatedAt: now,
	}
}
