package fleetdb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestAgentManagementUsesDelegatedAuditHeaderAndHeaderOnlyLeaseCredential(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	calls := 0
	httpClient := newWorkspaceHTTPClient(func(response http.ResponseWriter, request *http.Request) {
		calls++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got := request.Header.Get(FleetDelegatedActorHeader); got != "operator:alice" {
			t.Fatalf("call %d delegated actor = %q", calls, got)
		}
		var auditProbe map[string]any
		if len(body) > 0 {
			if err := json.Unmarshal(body, &auditProbe); err != nil {
				t.Fatal(err)
			}
		}
		for _, field := range []string{"created_by", "archived_by", "changed_by", "delegated_actor"} {
			if _, ok := auditProbe[field]; ok {
				t.Fatalf("call %d leaked audit field %q into body: %s", calls, field, body)
			}
		}
		switch calls {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != "/api/v1/WS/agent-services" {
				t.Fatalf("create request = %s %s", request.Method, request.URL.Path)
			}
			var decoded map[string]any
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded["service_id"] != "agent-1" || decoded["role_name"] != "docs" {
				t.Fatalf("create body = %#v", decoded)
			}
			if _, ok := decoded["created_by"]; ok {
				t.Fatalf("create body accepted created_by: %#v", decoded)
			}
			writeAgentManagementJSON(t, response, domain.AgentService{
				WorkspaceKey: "WS", ServiceID: "agent-1", Name: "Docs",
				Kind: domain.AgentServiceKindEvent, RoleName: "docs",
				DesiredState: domain.AgentServiceDesiredRunning, MaxInstances: 1,
				CreatedBy: "operator:alice", CreatedAt: now, UpdatedAt: now,
			})
		case 2:
			if request.URL.Path != "/api/v1/WS/agent-services/agent-1/desired-state/owned" {
				t.Fatalf("owned desired-state path = %s", request.URL.Path)
			}
			if got := request.Header.Get(AgentOwnershipLeaseTokenHeader); got != "raw-lease-token" {
				t.Fatalf("owned desired-state token header = %q", got)
			}
			if strings.Contains(string(body), "raw-lease-token") {
				t.Fatalf("owned desired-state token entered JSON: %s", body)
			}
			writeAgentManagementJSON(t, response, domain.AgentService{
				WorkspaceKey: "WS", ServiceID: "agent-1", Name: "Docs",
				Kind: domain.AgentServiceKindEvent, RoleName: "docs",
				DesiredState: domain.AgentServiceDesiredPaused, MaxInstances: 1,
				CreatedBy: "operator:alice", CreatedAt: now, UpdatedAt: now.Add(time.Second),
			})
		case 3:
			if request.URL.Path != "/api/v1/WS/agent-ownership-leases/agent-1/acquire" {
				t.Fatalf("acquire path = %s", request.URL.Path)
			}
			writeAgentManagementJSON(t, response, map[string]any{
				"lease": domain.AgentOwnershipLease{
					WorkspaceKey: "WS", AgentID: "agent-1", LeaseID: "lease-1",
					OwnerID: "operator:alice", RuntimeProvider: domain.RuntimeProviderLocal,
					NodeID: "node-1", FencingToken: 7, Status: domain.AgentLeaseActive,
					ExpiresAt: now.Add(time.Minute), LastHeartbeat: now,
					CreatedAt: now, UpdatedAt: now,
				},
				"token": "raw-lease-token",
			})
		case 4, 5:
			wantSuffix := "/heartbeat"
			if calls == 5 {
				wantSuffix = "/release"
			}
			if request.URL.Path != "/api/v1/WS/agent-ownership-leases/agent-1"+wantSuffix {
				t.Fatalf("ownership command path = %s", request.URL.Path)
			}
			if got := request.Header.Get(AgentOwnershipLeaseTokenHeader); got != "raw-lease-token" {
				t.Fatalf("ownership token header = %q", got)
			}
			if strings.Contains(string(body), "raw-lease-token") {
				t.Fatalf("ownership token entered JSON: %s", body)
			}
			status := domain.AgentLeaseActive
			if calls == 5 {
				status = domain.AgentLeaseReleased
			}
			writeAgentManagementJSON(t, response, domain.AgentOwnershipLease{
				WorkspaceKey: "WS", AgentID: "agent-1", LeaseID: "lease-1",
				OwnerID: "operator:alice", RuntimeProvider: domain.RuntimeProviderLocal,
				NodeID: "node-1", FencingToken: 7, Status: status,
				ExpiresAt: now.Add(time.Minute), LastHeartbeat: now,
				CreatedAt: now, UpdatedAt: now,
			})
		default:
			t.Fatalf("unexpected call %d", calls)
		}
	})
	client, err := New(Config{BaseURL: "http://fleet.invalid", APIKey: "service-key", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	transport := client.AgentManagement()
	if _, err := transport.CreateAgentService(t.Context(), AgentServiceCreateInput{
		WorkspaceKey: "WS", ServiceID: "agent-1", Name: "Docs",
		Kind: domain.AgentServiceKindEvent, DesiredState: domain.AgentServiceDesiredRunning,
		RoleName: "docs", MaxInstances: 1, DelegatedActor: "operator:alice",
	}); err != nil {
		t.Fatal(err)
	}
	proof := AgentOwnershipProof{
		WorkspaceKey: "WS", AgentID: "agent-1", LeaseID: "lease-1",
		LeaseToken: "raw-lease-token", OwnerID: "operator:alice",
		RuntimeProvider: domain.RuntimeProviderLocal, NodeID: "node-1", FencingToken: 7,
	}
	if _, err := transport.SetAgentServiceDesiredStateOwned(t.Context(), AgentServiceOwnedDesiredStateInput{
		Proof: proof, ExpectedState: domain.AgentServiceDesiredRunning,
		DesiredState: domain.AgentServiceDesiredPaused, ExpectedUpdatedAt: now,
		IdempotencyKey: "desired-1", DelegatedActor: "operator:alice",
	}); err != nil {
		t.Fatal(err)
	}
	grant, err := transport.AcquireAgentOwnership(t.Context(), AgentOwnershipAcquireInput{
		WorkspaceKey: "WS", AgentID: "agent-1", LeaseID: "lease-1",
		OwnerID: "operator:alice", RuntimeProvider: domain.RuntimeProviderLocal,
		NodeID: "node-1", TTLSeconds: 30, DelegatedActor: "operator:alice",
	})
	if err != nil || grant == nil || grant.Token != "raw-lease-token" ||
		grant.Lease == nil || grant.Lease.FencingToken != 7 {
		t.Fatalf("acquire = %#v, %v", grant, err)
	}
	if _, err := transport.RenewAgentOwnership(t.Context(), AgentOwnershipRenewInput{
		Proof: proof, TTLSeconds: 30, DelegatedActor: "operator:alice",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.ReleaseAgentOwnership(t.Context(), AgentOwnershipReleaseInput{
		Proof: proof, DelegatedActor: "operator:alice",
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 5 {
		t.Fatalf("calls = %d, want 5", calls)
	}
}

func TestAgentManagementRejectsInvalidDelegationBeforeTransport(t *testing.T) {
	client, err := New(Config{
		BaseURL: "http://fleet.invalid",
		HTTPClient: newWorkspaceHTTPClient(func(http.ResponseWriter, *http.Request) {
			t.Fatal("transport must not be called")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"", " actor ", "actor\nforged"} {
		_, err := client.AgentManagement().CreateAgentService(t.Context(), AgentServiceCreateInput{
			WorkspaceKey: "WS", ServiceID: "agent-1", DelegatedActor: actor,
		})
		if !errors.Is(err, ErrAgentManagementInvalidDelegatedActor) {
			t.Fatalf("actor %q error = %v", actor, err)
		}
	}
	_, err = client.AgentManagement().ReleaseAgentOwnership(t.Context(), AgentOwnershipReleaseInput{
		Proof: AgentOwnershipProof{
			WorkspaceKey: "WS", AgentID: "agent-1", LeaseID: "lease-1",
			OwnerID: "owner-1", RuntimeProvider: domain.RuntimeProviderLocal,
			NodeID: "node-1", FencingToken: 1,
		},
		DelegatedActor: "owner-1",
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing token error = %v", err)
	}
}

func TestAgentManagementLifecycleUsesSingleFleetCommand(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 123456000, time.UTC)
	generationID := "0123456789abcdef0123456789abcdef"
	httpClient := newWorkspaceHTTPClient(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/api/v1/WS/agent-services/agent-1/lifecycle" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get(FleetDelegatedActorHeader); got != "operator:alice" {
			t.Fatalf("delegated actor = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["action"] != "delete" ||
			body["idempotency_key"] != "lifecycle-1" ||
			body["expected_generation_id"] != generationID {
			t.Fatalf("lifecycle body = %#v", body)
		}
		deletedAt := now.Add(time.Microsecond)
		writeAgentManagementJSON(t, response, AgentServiceLifecycleResult{
			WorkspaceKey: "WS", ServiceID: "agent-1",
			IdempotencyKey: "lifecycle-1", Action: "delete",
			Agent: &domain.AgentService{
				WorkspaceKey: "WS", ServiceID: "agent-1", Name: "Docs",
				GenerationID: generationID,
				Kind:         domain.AgentServiceKindEvent, RoleName: "docs",
				DesiredState: domain.AgentServiceDesiredStopped, MaxInstances: 1,
				DeletedAt: &deletedAt, CreatedAt: now, UpdatedAt: deletedAt,
			},
			BindingIDs: []string{"binding-1"}, GrantIDs: []string{"grant-1"},
			CommittedAt: deletedAt,
		})
	})
	client, err := New(Config{
		BaseURL: "http://fleet.invalid", APIKey: "service-key", HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.AgentManagement().ApplyAgentServiceLifecycle(
		t.Context(),
		AgentServiceLifecycleInput{
			WorkspaceKey: "WS", ServiceID: "agent-1", Action: "delete",
			ExpectedUpdatedAt: now, IdempotencyKey: "lifecycle-1",
			ExpectedGenerationID: generationID,
			DelegatedActor:       "operator:alice",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Agent == nil ||
		result.Agent.GenerationID != generationID ||
		len(result.BindingIDs) != 1 ||
		len(result.GrantIDs) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestAgentManagementRoleMutationsUseAtomicFleetCommands(t *testing.T) {
	now := time.Date(2026, 7, 30, 14, 0, 0, 123456000, time.UTC)
	calls := 0
	httpClient := newWorkspaceHTTPClient(func(response http.ResponseWriter, request *http.Request) {
		calls++
		if got := request.Header.Get(FleetDelegatedActorHeader); got != "operator:alice" {
			t.Fatalf("call %d delegated actor = %q", calls, got)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch calls {
		case 1:
			if request.Method != http.MethodPatch ||
				request.URL.Path != "/api/v1/WS/roles/docs/definition" {
				t.Fatalf("update request = %s %s", request.Method, request.URL.Path)
			}
			if body["expected_updated_at"] != now.Format(time.RFC3339Nano) {
				t.Fatalf("update body = %#v", body)
			}
			patch, ok := body["patch"].(map[string]any)
			if !ok || patch["description"] != "new docs role" ||
				patch["clear_max_priority"] != true {
				t.Fatalf("update patch = %#v", body["patch"])
			}
			writeAgentManagementJSON(t, response, domain.Role{
				WorkspaceKey: "WS", Name: "docs", Description: "new docs role",
				CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(time.Microsecond),
			})
		case 2:
			if request.Method != http.MethodPost ||
				request.URL.Path != "/api/v1/WS/roles/docs/delete" {
				t.Fatalf("delete request = %s %s", request.Method, request.URL.Path)
			}
			if body["expected_updated_at"] != now.Format(time.RFC3339Nano) {
				t.Fatalf("delete body = %#v", body)
			}
			response.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected call %d", calls)
		}
	})
	client, err := New(Config{
		BaseURL: "http://fleet.invalid", APIKey: "service-key", HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	description := "new docs role"
	var clearedPriority *int
	updated, err := client.AgentManagement().UpdateAgentRole(t.Context(), AgentRoleUpdateInput{
		WorkspaceKey: "WS", RoleName: "docs", ExpectedUpdatedAt: now,
		Patch: AgentRolePatch{
			Description: &description,
			MaxPriority: &clearedPriority,
		},
		DelegatedActor: "operator:alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil || updated.Description != description {
		t.Fatalf("updated role = %#v", updated)
	}
	if err := client.AgentManagement().DeleteAgentRole(t.Context(), AgentRoleDeleteInput{
		WorkspaceKey: "WS", RoleName: "docs", ExpectedUpdatedAt: now,
		DelegatedActor: "operator:alice",
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want exactly two command requests", calls)
	}
}

func TestAgentManagementUnsupportedIdentityFieldsFailClosedWithoutLegacyIO(t *testing.T) {
	calls := 0
	client, err := New(Config{
		BaseURL: "http://fleet.invalid",
		HTTPClient: newWorkspaceHTTPClient(func(http.ResponseWriter, *http.Request) {
			calls++
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := "legacy-profile"
	_, err = client.AgentManagement().UpdateAgentServiceIdentity(
		t.Context(),
		AgentServiceUpdateInput{
			WorkspaceKey: "WS", ServiceID: "agent-1",
			ExpectedUpdatedAt: time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC),
			Patch:             AgentServiceIdentityPatch{ProfileName: &profile},
			DelegatedActor:    "operator:alice",
		},
	)
	if !errors.Is(err, ErrAgentServiceUnsupportedIdentityPatch) ||
		!errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unsupported patch error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("transport calls = %d, want 0", calls)
	}
}

func TestAgentManagementRoleRevisionConflictIsTypedWithoutPreflightRead(t *testing.T) {
	calls := 0
	httpClient := newWorkspaceHTTPClient(func(response http.ResponseWriter, request *http.Request) {
		calls++
		if request.Method != http.MethodPatch ||
			request.URL.Path != "/api/v1/WS/roles/docs/definition" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusConflict)
		_, _ = response.Write([]byte(`{"error":{"code":"agent_role_revision_conflict","message":"stale role"}}`))
	})
	client, err := New(Config{
		BaseURL: "http://fleet.invalid", APIKey: "service-key", HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	description := "new"
	_, err = client.AgentManagement().UpdateAgentRole(t.Context(), AgentRoleUpdateInput{
		WorkspaceKey: "WS", RoleName: "docs",
		ExpectedUpdatedAt: time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC),
		Patch:             AgentRolePatch{Description: &description}, DelegatedActor: "operator:alice",
	})
	if !errors.Is(err, ErrAgentRoleRevisionConflict) {
		t.Fatalf("error = %v, want ErrAgentRoleRevisionConflict", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want one atomic command and no GET", calls)
	}
}

func writeAgentManagementJSON(t *testing.T, response http.ResponseWriter, value any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Fatal(err)
	}
}
