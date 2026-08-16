package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

type transportFake struct {
	agent    *AgentServiceWire
	agents   []*AgentServiceWire
	role     *RoleReferenceWire
	fullRole *RoleWire
	roles    []*RoleWire
	lease    *AgentOwnershipLeaseWire
	leases   []*AgentOwnershipLeaseWire
	err      error

	calls           []string
	workspace       string
	agentID         string
	agentFilter     AgentServiceFilterWire
	create          CreateAgentServiceWire
	createRole      agents.RoleDefinition
	updateRole      UpdateRoleWire
	deleteRole      DeleteRoleWire
	update          UpdateAgentServiceWire
	archive         ArchiveAgentServiceWire
	desired         DesiredStateWire
	ownedDesired    OwnedDesiredStateWire
	ownershipFilter OwnershipFilterWire
	acquire         AcquireOwnershipWire
	renew           RenewOwnershipWire
	release         OwnershipProofWire
	managedReviewer ManagedReviewerWire
	managedResult   *ManagedReviewerResultWire
}

func (fake *transportFake) GetAgentService(_ context.Context, workspace, agentID string) (*AgentServiceWire, error) {
	fake.calls = append(fake.calls, "get-agent")
	fake.workspace, fake.agentID = workspace, agentID
	return fake.agent, fake.err
}

func (fake *transportFake) ListAgentServices(_ context.Context, workspace string, filter AgentServiceFilterWire) ([]*AgentServiceWire, error) {
	fake.calls = append(fake.calls, "list-agents")
	fake.workspace, fake.agentFilter = workspace, filter
	return fake.agents, fake.err
}

func (fake *transportFake) GetRoleReference(_ context.Context, workspace, roleName string) (*RoleReferenceWire, error) {
	fake.calls = append(fake.calls, "get-role")
	fake.workspace, fake.agentID = workspace, roleName
	return fake.role, fake.err
}

func (fake *transportFake) GetRole(_ context.Context, workspace, roleName string) (*RoleWire, error) {
	fake.calls = append(fake.calls, "get-full-role")
	fake.workspace, fake.agentID = workspace, roleName
	return fake.fullRole, fake.err
}

func (fake *transportFake) CreateRole(
	_ context.Context,
	workspace string,
	role agents.RoleDefinition,
) (*RoleWire, error) {
	fake.calls = append(fake.calls, "create-role")
	fake.workspace, fake.createRole = workspace, role
	return fake.fullRole, fake.err
}

func (fake *transportFake) ListRoles(_ context.Context, workspace string) ([]*RoleWire, error) {
	fake.calls = append(fake.calls, "list-roles")
	fake.workspace = workspace
	return fake.roles, fake.err
}

func (fake *transportFake) UpdateRole(_ context.Context, request UpdateRoleWire) (*RoleWire, error) {
	fake.calls = append(fake.calls, "update-role")
	fake.updateRole = request
	return fake.fullRole, fake.err
}

func (fake *transportFake) DeleteRole(_ context.Context, request DeleteRoleWire) error {
	fake.calls = append(fake.calls, "delete-role")
	fake.deleteRole = request
	return fake.err
}

func (fake *transportFake) CreateAgentService(_ context.Context, request CreateAgentServiceWire) (*AgentServiceWire, error) {
	fake.calls = append(fake.calls, "create-agent")
	fake.create = request
	return fake.agent, fake.err
}

func (fake *transportFake) UpdateAgentService(_ context.Context, request UpdateAgentServiceWire) (*AgentServiceWire, error) {
	fake.calls = append(fake.calls, "update-agent")
	fake.update = request
	return fake.agent, fake.err
}

func (fake *transportFake) ArchiveAgentService(_ context.Context, request ArchiveAgentServiceWire) (*AgentServiceWire, error) {
	fake.calls = append(fake.calls, "archive-agent")
	fake.archive = request
	return fake.agent, fake.err
}

func (fake *transportFake) SetAgentServiceDesiredState(_ context.Context, request DesiredStateWire) (*AgentServiceWire, error) {
	fake.calls = append(fake.calls, "set-desired")
	fake.desired = request
	return fake.agent, fake.err
}

func (fake *transportFake) SetAgentServiceDesiredStateOwned(_ context.Context, request OwnedDesiredStateWire) (*AgentServiceWire, error) {
	fake.calls = append(fake.calls, "set-desired-owned")
	fake.ownedDesired = request
	return fake.agent, fake.err
}

func (fake *transportFake) ConvergeManagedReviewer(
	_ context.Context,
	request ManagedReviewerWire,
) (*ManagedReviewerResultWire, error) {
	fake.calls = append(fake.calls, "converge-managed-reviewer")
	fake.managedReviewer = request
	return fake.managedResult, fake.err
}

func (fake *transportFake) AcquireAgentOwnership(_ context.Context, request AcquireOwnershipWire) (*AgentOwnershipLeaseWire, error) {
	fake.calls = append(fake.calls, "acquire-ownership")
	fake.acquire = request
	return fake.lease, fake.err
}

func (fake *transportFake) GetAgentOwnership(_ context.Context, workspace, agentID string) (*AgentOwnershipLeaseWire, error) {
	fake.calls = append(fake.calls, "get-ownership")
	fake.workspace, fake.agentID = workspace, agentID
	return fake.lease, fake.err
}

func (fake *transportFake) ListAgentOwnership(_ context.Context, workspace string, filter OwnershipFilterWire) ([]*AgentOwnershipLeaseWire, error) {
	fake.calls = append(fake.calls, "list-ownership")
	fake.workspace, fake.ownershipFilter = workspace, filter
	return fake.leases, fake.err
}

func (fake *transportFake) RenewAgentOwnership(_ context.Context, request RenewOwnershipWire) (*AgentOwnershipLeaseWire, error) {
	fake.calls = append(fake.calls, "renew-ownership")
	fake.renew = request
	return fake.lease, fake.err
}

func (fake *transportFake) ReleaseAgentOwnership(_ context.Context, request OwnershipProofWire) (*AgentOwnershipLeaseWire, error) {
	fake.calls = append(fake.calls, "release-ownership")
	fake.release = request
	return fake.lease, fake.err
}

func TestNewRejectsNilTransport(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, agents.ErrUnavailable) {
		t.Fatalf("New(nil) error = %v, want unavailable", err)
	}
}

func TestAdapterMapsManagedReviewerConvergenceAsOneCommand(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	fake := &transportFake{managedResult: &ManagedReviewerResultWire{
		PresetID: "reviewer-v1", PresetRevision: 2,
		PresetFingerprint: strings.Repeat("a", 64), Changed: true,
		Role: &RoleWire{
			WorkspaceKey: "WS", Name: "pr-reviewer", Kind: agents.RoleKindInteractive,
			PromptFile: "builtin:pr-review-checkout", CreatedAt: now, UpdatedAt: now,
		},
		Agent: &AgentServiceWire{
			WorkspaceKey: "WS", ServiceID: "review-octo-repo-pr-7",
			GenerationID: "00112233445566778899aabbccddeeff",
			Name:         "review-octo-repo-pr-7", Kind: string(agents.AgentKindSupport),
			DesiredState: string(agents.DesiredRunning), RoleName: "pr-reviewer",
			MaxInstances: 1, CreatedAt: now, UpdatedAt: now,
		},
	}}
	adapter := newAdapter(t, fake)
	mutation := agents.ManagedReviewerMutation{
		WorkspaceKey: "WS", AgentID: "review-octo-repo-pr-7",
		DesiredState: agents.ManagedReviewerActive, Fingerprint: strings.Repeat("a", 64),
		ActorID: "serve-pr-reviewer-convergence",
		Preset: agents.ManagedReviewerPreset{
			PresetID: "reviewer-v1", Revision: 2,
			Role: agents.ManagedReviewerRoleDefinition{
				Name: "pr-reviewer", Kind: agents.RoleKindInteractive,
				PromptFile: "builtin:pr-review-checkout",
			},
			Agent: agents.ManagedReviewerAgentDefinition{
				Kind: agents.AgentKindSupport, DesiredState: agents.DesiredRunning,
				RoleName: "pr-reviewer", MaxInstances: 1,
			},
		},
	}
	result, err := adapter.ConvergeManagedReviewer(t.Context(), mutation)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fake.calls, []string{"converge-managed-reviewer"}) ||
		fake.managedReviewer.WorkspaceKey != mutation.WorkspaceKey ||
		fake.managedReviewer.AgentID != mutation.AgentID ||
		fake.managedReviewer.DesiredState != string(mutation.DesiredState) ||
		fake.managedReviewer.ActorID != mutation.ActorID ||
		fake.managedReviewer.Preset.Fingerprint != mutation.Fingerprint ||
		!reflect.DeepEqual(fake.managedReviewer.Preset.Role, mutation.Preset.Role) ||
		!reflect.DeepEqual(fake.managedReviewer.Preset.Agent, mutation.Preset.Agent) {
		t.Fatalf("managed reviewer wire = %+v calls=%v", fake.managedReviewer, fake.calls)
	}
	if result == nil || result.PresetRevision != 2 || !result.Changed ||
		result.Role == nil || result.Role.PromptFile != "builtin:pr-review-checkout" ||
		result.Agent == nil || result.Agent.AgentID != mutation.AgentID {
		t.Fatalf("managed reviewer result = %#v", result)
	}
}

func TestAdapterMapsAgentServiceIdentityBothDirections(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	deletedAt := now.Add(time.Hour)
	wire := fullAgentWire(now)
	wire.DeletedAt = &deletedAt
	fake := &transportFake{agent: wire, agents: []*AgentServiceWire{wire}}
	adapter := newAdapter(t, fake)

	got, err := adapter.GetAgent(t.Context(), "WS", "agt-docs")
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkspaceKey != "WS" || got.AgentID != "agt-docs" || got.Name != "Docs review" ||
		got.GenerationID != "00112233445566778899aabbccddeeff" ||
		got.Kind != agents.AgentKindEvent || got.Behavior.RoleName != "docs" ||
		got.Behavior.DriverID != "" || got.DesiredState != agents.DesiredRunning ||
		got.PlacementPolicy != "local" || got.MaxInstances != 2 ||
		got.RestartPolicy != "always" || got.BudgetPolicy != "daily:10" ||
		got.Metadata["backend"] != "codex" ||
		got.CreatedBy != "operator-a" || got.DeletedAt == nil || !got.DeletedAt.Equal(deletedAt) {
		t.Fatalf("Agent mapping = %#v", got)
	}
	*got.DeletedAt = time.Time{}
	if wire.DeletedAt.IsZero() {
		t.Fatal("Agent mapping leaked transport DeletedAt pointer")
	}
	if len(fake.calls) != 1 || fake.calls[0] != "get-agent" ||
		fake.workspace != "WS" || fake.agentID != "agt-docs" {
		t.Fatalf("Get delegation = calls %#v workspace %q agent %q", fake.calls, fake.workspace, fake.agentID)
	}

	fake.calls = nil
	values, err := adapter.ListAgents(t.Context(), "WS", agents.AgentFilter{
		Kind: agents.AgentKindEvent, DesiredState: agents.DesiredRunning,
		RoleName: "docs", IncludeDeleted: true, Limit: 7,
	})
	if err != nil || len(values) != 1 || values[0].AgentID != "agt-docs" {
		t.Fatalf("ListAgents = %#v, %v", values, err)
	}
	if fake.agentFilter != (AgentServiceFilterWire{
		Kind: "event", DesiredState: "running", RoleName: "docs",
		IncludeDeleted: true, Limit: 7,
	}) {
		t.Fatalf("Agent filter wire = %#v", fake.agentFilter)
	}

	fake.calls = nil
	created, err := adapter.CreateAgent(t.Context(), agents.CreateAgentMutation{
		WorkspaceKey: "WS", AgentID: "agt-docs", Name: "Docs review",
		Kind: agents.AgentKindEvent, Behavior: agents.BehaviorReference{RoleName: "docs"},
		DesiredState: agents.DesiredRunning, PlacementPolicy: "local", MaxInstances: 2,
		RestartPolicy: "always", BudgetPolicy: "daily:10",
		Metadata: map[string]string{"backend": "codex"}, CreatedBy: "operator-a",
	})
	if err != nil || created.AgentID != "agt-docs" {
		t.Fatalf("CreateAgent = %#v, %v", created, err)
	}
	if !reflect.DeepEqual(fake.create, CreateAgentServiceWire{
		WorkspaceKey: "WS", ServiceID: "agt-docs", Name: "Docs review",
		Kind: "event", DesiredState: "running", RoleName: "docs",
		PlacementPolicy: "local", MaxInstances: 2, RestartPolicy: "always",
		BudgetPolicy: "daily:10", Metadata: map[string]string{"backend": "codex"},
		CreatedBy: "operator-a",
	}) {
		t.Fatalf("Create AgentService wire = %#v", fake.create)
	}
}

func TestAdapterMapsBehaviorReplacementAndIdentityCAS(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	fake := &transportFake{agent: fullAgentWire(now)}
	adapter := newAdapter(t, fake)
	name, kind := "Scripted docs", agents.AgentKindAlwaysOn
	placement, maxInstances, restart, budget := "node:gpu", 3, "on-failure", "daily:20"
	behavior := agents.BehaviorReference{DriverID: "driver-docs", DriverVersionID: "drvver-7"}

	_, err := adapter.UpdateAgent(t.Context(), agents.UpdateAgentMutation{
		WorkspaceKey: "WS", AgentID: "agt-docs", ExpectedUpdatedAt: now,
		UpdatedBy: "operator-a",
		Patch: agents.AgentPatch{
			Name: &name, Kind: &kind, Behavior: &behavior, PlacementPolicy: &placement,
			MaxInstances: &maxInstances, RestartPolicy: &restart, BudgetPolicy: &budget,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := fake.update
	if got.WorkspaceKey != "WS" || got.ServiceID != "agt-docs" || !got.ExpectedUpdatedAt.Equal(now) ||
		got.UpdatedBy != "operator-a" ||
		stringValue(got.Patch.Name) != name || stringValue(got.Patch.Kind) != "always_on" ||
		stringValue(got.Patch.RoleName) != "" || stringValue(got.Patch.DriverID) != "driver-docs" ||
		stringValue(got.Patch.DriverVersionID) != "drvver-7" ||
		stringValue(got.Patch.PlacementPolicy) != placement ||
		intValue(got.Patch.MaxInstances) != maxInstances ||
		stringValue(got.Patch.RestartPolicy) != restart ||
		stringValue(got.Patch.BudgetPolicy) != budget {
		t.Fatalf("Update AgentService wire = %#v", got)
	}
	if got.Patch.RoleName == nil || got.Patch.DriverID == nil || got.Patch.DriverVersionID == nil {
		t.Fatal("behavior replacement did not explicitly clear the old reference")
	}

	archivedAt := now.Add(time.Minute)
	fake.agent.DeletedAt = &archivedAt
	_, err = adapter.ArchiveAgent(t.Context(), agents.ArchiveAgentMutation{
		WorkspaceKey: "WS", AgentID: "agt-docs",
		ExpectedUpdatedAt: now, ArchivedBy: "operator-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.archive.WorkspaceKey != "WS" || fake.archive.ServiceID != "agt-docs" ||
		!fake.archive.ExpectedUpdatedAt.Equal(now) || fake.archive.ArchivedBy != "operator-a" {
		t.Fatalf("Archive wire = %#v", fake.archive)
	}
}

func TestAdapterMapsDesiredStateCommandsAndKeepsOwnedCommandAtomic(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	fake := &transportFake{agent: fullAgentWire(now)}
	fake.agent.DesiredState = "paused"
	adapter := newAdapter(t, fake)

	_, err := adapter.SetDesiredState(t.Context(), agents.DesiredStateMutation{
		WorkspaceKey: "WS", AgentID: "agt-docs", ExpectedState: agents.DesiredRunning,
		DesiredState: agents.DesiredPaused, ExpectedUpdatedAt: now, ChangedBy: "operator-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.desired != (DesiredStateWire{
		WorkspaceKey: "WS", ServiceID: "agt-docs", ExpectedState: "running",
		DesiredState: "paused", ExpectedUpdatedAt: now, ChangedBy: "operator-a",
	}) {
		t.Fatalf("Desired-state wire = %#v", fake.desired)
	}

	fake.calls = nil
	proof := fullProof()
	_, err = adapter.SetDesiredStateOwned(t.Context(), agents.OwnedDesiredStateMutation{
		Ownership: proof, ExpectedState: agents.DesiredRunning, DesiredState: agents.DesiredPaused,
		ExpectedUpdatedAt: now, IdempotencyKey: "reconcile-7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "set-desired-owned" {
		t.Fatalf("owner-fenced command was decomposed: calls = %#v", fake.calls)
	}
	if fake.ownedDesired != (OwnedDesiredStateWire{
		WorkspaceKey: "WS", ServiceID: "agt-docs", LeaseID: "lease-7",
		LeaseToken: "raw-secret-token", OwnerID: "controller-a",
		RuntimeProvider: "local", NodeID: "node-a", FencingToken: 7,
		ExpectedState: "running", DesiredState: "paused",
		ExpectedUpdatedAt: now, IdempotencyKey: "reconcile-7",
	}) {
		t.Fatalf("Owned desired-state wire = %#v", fake.ownedDesired)
	}
	wireJSON, err := json.Marshal(fake.ownedDesired)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wireJSON), "raw-secret-token") {
		t.Fatalf("owned desired-state token crossed JSON: %s", wireJSON)
	}
}

func TestAdapterMapsRoleAndOwnershipWithoutLeakingLeaseToken(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	lease := fullLeaseWire(now)
	fake := &transportFake{
		role: &RoleReferenceWire{WorkspaceKey: "WS", Name: "docs"},
		fullRole: &RoleWire{
			WorkspaceKey: "WS", Name: "docs", Kind: "worker",
			Prompt: "Review docs.", CreatedAt: now, UpdatedAt: now,
		},
		roles: []*RoleWire{{
			WorkspaceKey: "WS", Name: "docs", Kind: "worker",
			Prompt: "Review docs.", CreatedAt: now, UpdatedAt: now,
		}},
		lease: lease, leases: []*AgentOwnershipLeaseWire{lease},
	}
	adapter := newAdapter(t, fake)

	role, err := adapter.GetRoleReference(t.Context(), "WS", "docs")
	if err != nil || role == nil || role.WorkspaceKey != "WS" || role.RoleName != "docs" {
		t.Fatalf("Role reference = %#v, %v", role, err)
	}
	fullRole, err := adapter.GetRole(t.Context(), "WS", "docs")
	if err != nil || fullRole == nil || fullRole.Prompt != "Review docs." {
		t.Fatalf("Role = %#v, %v", fullRole, err)
	}
	definition := agents.RoleDefinition{Name: "docs", Kind: "worker", Prompt: "Review docs."}
	if _, err := adapter.CreateRole(t.Context(), "WS", definition); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fake.createRole, definition) {
		t.Fatalf("CreateRole wire = %#v", fake.createRole)
	}
	roles, err := adapter.ListRoles(t.Context(), "WS")
	if err != nil || len(roles) != 1 || roles[0].Name != "docs" {
		t.Fatalf("ListRoles = %#v, %v", roles, err)
	}
	description := "Updated docs reviewer"
	pathPatterns := []string{"docs/**"}
	clearPriority := (*int)(nil)
	if _, err := adapter.UpdateRole(t.Context(), agents.UpdateRoleMutation{
		WorkspaceKey: "WS", RoleName: "docs", ExpectedUpdatedAt: now,
		Patch: agents.RolePatch{
			Description: &description, PathPatterns: &pathPatterns, MaxPriority: &clearPriority,
		},
		UpdatedBy: "operator-a",
	}); err != nil {
		t.Fatal(err)
	}
	if fake.updateRole.WorkspaceKey != "WS" || fake.updateRole.RoleName != "docs" ||
		!fake.updateRole.ExpectedUpdatedAt.Equal(now) || fake.updateRole.UpdatedBy != "operator-a" ||
		fake.updateRole.Patch.Description == nil || *fake.updateRole.Patch.Description != description ||
		fake.updateRole.Patch.PathPatterns == nil ||
		!reflect.DeepEqual(*fake.updateRole.Patch.PathPatterns, pathPatterns) ||
		fake.updateRole.Patch.MaxPriority == nil || *fake.updateRole.Patch.MaxPriority != nil {
		t.Fatalf("UpdateRole wire = %#v", fake.updateRole)
	}
	description = "mutated"
	pathPatterns[0] = "mutated/**"
	if *fake.updateRole.Patch.Description != "Updated docs reviewer" ||
		(*fake.updateRole.Patch.PathPatterns)[0] != "docs/**" {
		t.Fatalf("UpdateRole wire aliases caller-owned patch: %#v", fake.updateRole.Patch)
	}
	if err := adapter.DeleteRole(t.Context(), agents.DeleteRoleMutation{
		WorkspaceKey: "WS", RoleName: "docs", ExpectedUpdatedAt: now, DeletedBy: "operator-a",
	}); err != nil {
		t.Fatal(err)
	}
	if fake.deleteRole != (DeleteRoleWire{
		WorkspaceKey: "WS", RoleName: "docs", ExpectedUpdatedAt: now, DeletedBy: "operator-a",
	}) {
		t.Fatalf("DeleteRole wire = %#v", fake.deleteRole)
	}
	grant, err := adapter.AcquireOwnership(t.Context(), agents.AcquireOwnershipMutation{
		WorkspaceKey: "WS", AgentID: "agt-docs", LeaseID: "lease-7",
		OwnerID: "controller-a", RuntimeProvider: agents.RuntimeProviderLocal,
		NodeID: "node-a", TTL: 1500 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.acquire != (AcquireOwnershipWire{
		WorkspaceKey: "WS", AgentID: "agt-docs", LeaseID: "lease-7",
		OwnerID: "controller-a", RuntimeProvider: "local", NodeID: "node-a", TTLSeconds: 2,
	}) {
		t.Fatalf("Acquire wire = %#v", fake.acquire)
	}
	if grant == nil || grant.LeaseToken != "raw-secret-token" || grant.Lease == nil ||
		grant.Lease.AgentID != "agt-docs" || grant.Lease.FencingToken != 7 {
		t.Fatalf("Ownership grant = %#v", grant)
	}
	grantJSON, err := json.Marshal(grant)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(grantJSON), "raw-secret-token") {
		t.Fatalf("ownership grant leaked token: %s", grantJSON)
	}

	projected, err := adapter.GetOwnership(t.Context(), "WS", "agt-docs")
	if err != nil || projected == nil || projected.AgentID != "agt-docs" {
		t.Fatalf("GetOwnership = %#v, %v", projected, err)
	}
	projectedJSON, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(projectedJSON), "raw-secret-token") {
		t.Fatalf("ownership projection leaked token: %s", projectedJSON)
	}

	values, err := adapter.ListOwnership(t.Context(), "WS", agents.OwnershipFilter{
		OwnerID: "controller-a", RuntimeProvider: agents.RuntimeProviderLocal,
		NodeID: "node-a", Status: agents.OwnershipActive, Limit: 5,
	})
	if err != nil || len(values) != 1 {
		t.Fatalf("ListOwnership = %#v, %v", values, err)
	}
	if fake.ownershipFilter != (OwnershipFilterWire{
		OwnerID: "controller-a", RuntimeProvider: "local",
		NodeID: "node-a", Status: "active", Limit: 5,
	}) {
		t.Fatalf("Ownership filter wire = %#v", fake.ownershipFilter)
	}

	proof := fullProof()
	if _, err := adapter.RenewOwnership(t.Context(), agents.RenewOwnershipMutation{
		Ownership: proof, TTL: 2500 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	if fake.renew.Ownership != ownershipProofToWire(proof) || fake.renew.TTLSeconds != 3 {
		t.Fatalf("Renew wire = %#v", fake.renew)
	}
	if _, err := adapter.ReleaseOwnership(t.Context(), proof); err != nil {
		t.Fatal(err)
	}
	if fake.release != ownershipProofToWire(proof) {
		t.Fatalf("Release wire = %#v", fake.release)
	}
}

func TestAdapterMapsTransportErrorsToAgentsVocabulary(t *testing.T) {
	for _, test := range []struct {
		name string
		in   error
		want error
	}{
		{name: "not found", in: ErrTransportNotFound, want: agents.ErrNotFound},
		{name: "invalid", in: ErrTransportInvalid, want: agents.ErrInvalid},
		{name: "already exists", in: ErrTransportAlreadyExists, want: agents.ErrAlreadyExists},
		{name: "conflict", in: ErrTransportConflict, want: agents.ErrConflict},
		{name: "not owner", in: ErrTransportNotOwner, want: agents.ErrNotOwner},
		{name: "invalid transition", in: ErrTransportInvalidTransition, want: agents.ErrInvalidTransition},
		{name: "unavailable", in: ErrTransportUnavailable, want: agents.ErrUnavailable},
		{name: "unknown", in: errors.New("dial refused"), want: agents.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &transportFake{err: test.in}
			adapter := newAdapter(t, fake)
			_, err := adapter.GetAgent(t.Context(), "WS", "agt-docs")
			if !errors.Is(err, test.want) || !errors.Is(err, test.in) {
				t.Fatalf("error = %v, want %v and original", err, test.want)
			}
		})
	}
}

func newAdapter(t *testing.T, transport Transport) *Adapter {
	t.Helper()
	adapter, err := New(transport)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func fullAgentWire(now time.Time) *AgentServiceWire {
	return &AgentServiceWire{
		WorkspaceKey: "WS", ServiceID: "agt-docs", Name: "Docs review",
		GenerationID: "00112233445566778899aabbccddeeff",
		Kind:         "event", DesiredState: "running", RoleName: "docs",
		PlacementPolicy: "local", MaxInstances: 2, RestartPolicy: "always",
		BudgetPolicy: "daily:10", CreatedBy: "operator-a",
		CreatedAt: now, UpdatedAt: now,
		// These legacy/dormant AgentService fields are intentionally not
		// promoted into the canonical Agents model.
		ProfileName: "legacy-profile", ScheduleID: "legacy-schedule",
		EventSources: []string{"legacy"}, TriggerRefs: []string{"legacy"},
		LeaseID: "legacy-lease", Permissions: []string{"legacy"},
		StateRef: "legacy-state", Metadata: map[string]string{"backend": "codex"},
	}
}

func fullLeaseWire(now time.Time) *AgentOwnershipLeaseWire {
	return &AgentOwnershipLeaseWire{
		WorkspaceKey: "WS", AgentID: "agt-docs", LeaseID: "lease-7",
		OwnerID: "controller-a", RuntimeProvider: "local", NodeID: "node-a",
		Token: "raw-secret-token", FencingToken: 7, Status: "active",
		ExpiresAt: now.Add(5 * time.Minute), LastHeartbeat: now,
		CreatedAt: now, UpdatedAt: now,
	}
}

func fullProof() agents.OwnershipProof {
	return agents.OwnershipProof{
		WorkspaceKey: "WS", AgentID: "agt-docs", LeaseID: "lease-7",
		LeaseToken: "raw-secret-token", OwnerID: "controller-a",
		RuntimeProvider: agents.RuntimeProviderLocal, NodeID: "node-a", FencingToken: 7,
	}
}

func stringValue(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}

func intValue(value *int) int {
	if value == nil {
		return -1
	}
	return *value
}
