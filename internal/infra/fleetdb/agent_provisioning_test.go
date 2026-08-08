package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

type agentProvisioningRoundTripFunc func(*http.Request) (*http.Response, error)

func (function agentProvisioningRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type agentProvisioningStepGuardInput struct {
	ExpectedProvisioningGenerationID string `json:"expected_provisioning_generation_id"`
}

func TestAgentProvisioningTransportUsesServerOwnedIntentAndProgressRoutes(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	record := AgentProvisioningRecord{
		ProvisioningID:           "docs/review",
		ProvisioningGenerationID: "0123456789abcdef0123456789abcdef",
		WorkspaceKey:             "space/name",
		RequestedBy:              "loom-service", SpecFingerprint: "sha256:" + strings.Repeat("a", 64),
		Spec: AgentProvisioningSpec{
			ProvisioningID: "docs/review", WorkspaceKey: "space/name", RequestedBy: "loom-service",
			Role:    AgentProvisioningRoleSpec{Name: "docs"},
			Agent:   AgentProvisioningAgentSpec{AgentID: "agent-1", RoleName: "docs"},
			Binding: AgentProvisioningBindingSpec{BindingID: "binding-1"},
		},
		State: "pending", UnusedRolePolicy: "retain", Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		switch call {
		case 1:
			if request.Method != http.MethodPost ||
				request.URL.EscapedPath() != "/api/v1/space%2Fname/agent-provisioning" {
				t.Fatalf("begin request = %s %s", request.Method, request.URL.EscapedPath())
			}
			if got := request.Header.Get(FleetDelegatedActorHeader); got != "operator:docs" {
				t.Fatalf("delegated requester = %q", got)
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{
				"workspace_key", "requested_by", "spec_fingerprint", "state",
				"version", "created_at", "updated_at", "unused_role_policy",
				"provisioning_generation_id", "workspace_incarnation_id",
			} {
				if _, exists := body[forbidden]; exists {
					t.Errorf("begin body contains server-owned %q", forbidden)
				}
			}
			if string(body["provisioning_id"]) != `"docs/review"` {
				t.Errorf("begin body = %s", body["provisioning_id"])
			}
			_ = json.NewEncoder(w).Encode(record)
		case 2:
			if request.Method != http.MethodGet ||
				request.URL.EscapedPath() != "/api/v1/space%2Fname/agent-provisioning/docs%2Freview" {
				t.Fatalf("get request = %s %s", request.Method, request.URL.EscapedPath())
			}
			_ = json.NewEncoder(w).Encode(record)
		case 3:
			if request.Method != http.MethodGet ||
				request.URL.EscapedPath() != "/api/v1/space%2Fname/agent-provisioning/pending" ||
				request.URL.Query().Get("limit") != "7" {
				t.Fatalf("pending request = %s %s?%s", request.Method, request.URL.EscapedPath(), request.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"agent_provisioning": []*AgentProvisioningRecord{&record},
				"count":              1,
			})
		case 4:
			if request.Method != http.MethodPost ||
				request.URL.EscapedPath() != "/api/v1/space%2Fname/agent-provisioning/docs%2Freview/progress" {
				t.Fatalf("progress request = %s %s", request.Method, request.URL.EscapedPath())
			}
			var body AgentProvisioningProgressInput
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.ExpectedProvisioningGenerationID != record.ProvisioningGenerationID ||
				body.ExpectedVersion != 1 || body.State != "running" ||
				len(body.CompletedSteps) != 1 || body.CompletedSteps[0] != "role" {
				t.Errorf("progress body = %+v", body)
			}
			next := record
			next.State = "running"
			next.CompletedSteps = []string{"role"}
			next.Version = 2
			_ = json.NewEncoder(w).Encode(next)
		case 5, 6, 7, 8:
			wantOperation := map[int]string{
				5: "ensure-role",
				6: "ensure-agent-service",
				7: "ensure-trigger-binding",
				8: "ensure-connector-grant/grant%2Fread",
			}[int(call)]
			if request.Method != http.MethodPost ||
				request.URL.EscapedPath() != "/api/v1/space%2Fname/agent-provisioning/docs%2Freview/"+wantOperation {
				t.Fatalf("guarded step request = %s %s", request.Method, request.URL.EscapedPath())
			}
			var body agentProvisioningStepGuardInput
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.ExpectedProvisioningGenerationID != record.ProvisioningGenerationID {
				t.Errorf("guarded step body = %+v", body)
			}
			switch call {
			case 5:
				_ = json.NewEncoder(w).Encode(domain.Role{
					WorkspaceKey: "space/name", Name: "docs",
				})
			case 6:
				_ = json.NewEncoder(w).Encode(domain.AgentService{
					WorkspaceKey: "space/name", ServiceID: "agent-1",
				})
			case 7:
				_ = json.NewEncoder(w).Encode(automation.Binding{
					WorkspaceKey: "space/name", BindingID: "binding-1",
				})
			case 8:
				_ = json.NewEncoder(w).Encode(connectorGrantWire{
					WorkspaceKey: "space/name", GrantID: "grant/read",
				})
			}
		default:
			t.Fatalf("unexpected request %d: %s %s", call, request.Method, request.URL.String())
		}
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	transport := client.AgentProvisioning()
	if transport == nil || transport != client.AgentProvisioning() {
		t.Fatal("AgentProvisioning did not return the stable shared transport")
	}
	input := AgentProvisioningBeginInput{
		ProvisioningID: "docs/review",
		Role:           AgentProvisioningRoleSpec{Name: "docs"},
		Agent:          AgentProvisioningAgentSpec{AgentID: "agent-1", RoleName: "docs"},
		Binding:        AgentProvisioningBindingSpec{BindingID: "binding-1"},
		DelegatedActor: "operator:docs",
	}
	begun, err := transport.BeginAgentProvisioning(t.Context(), "space/name", input)
	if err != nil || begun.RequestedBy != "loom-service" {
		t.Fatalf("BeginAgentProvisioning = %+v, %v", begun, err)
	}
	if _, err := transport.GetAgentProvisioning(t.Context(), "space/name", "docs/review"); err != nil {
		t.Fatal(err)
	}
	pending, err := transport.ListPendingAgentProvisioning(t.Context(), "space/name", 7)
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPendingAgentProvisioning = %+v, %v", pending, err)
	}
	saved, err := transport.SaveAgentProvisioningProgress(
		t.Context(), "space/name", "docs/review",
		AgentProvisioningProgressInput{
			ExpectedProvisioningGenerationID: record.ProvisioningGenerationID,
			ExpectedVersion:                  1,
			State:                            "running",
			CompletedSteps:                   []string{"role"},
		},
	)
	if err != nil || saved.Version != 2 {
		t.Fatalf("SaveAgentProvisioningProgress = %+v, %v", saved, err)
	}
	role, err := transport.EnsureAgentProvisioningRole(
		t.Context(),
		"space/name",
		"docs/review",
		record.ProvisioningGenerationID,
	)
	if err != nil || role.Name != "docs" {
		t.Fatalf("EnsureAgentProvisioningRole = %+v, %v", role, err)
	}
	agent, err := transport.EnsureAgentProvisioningAgentService(
		t.Context(),
		"space/name",
		"docs/review",
		record.ProvisioningGenerationID,
	)
	if err != nil || agent.ServiceID != "agent-1" {
		t.Fatalf("EnsureAgentProvisioningAgentService = %+v, %v", agent, err)
	}
	binding, err := transport.EnsureAgentProvisioningTriggerBinding(
		t.Context(),
		"space/name",
		"docs/review",
		record.ProvisioningGenerationID,
	)
	if err != nil || binding.BindingID != "binding-1" {
		t.Fatalf("EnsureAgentProvisioningTriggerBinding = %+v, %v", binding, err)
	}
	grant, err := transport.EnsureAgentProvisioningConnectorGrant(
		t.Context(),
		"space/name",
		"docs/review",
		record.ProvisioningGenerationID,
		"grant/read",
	)
	if err != nil || grant.GrantID != "grant/read" {
		t.Fatalf("EnsureAgentProvisioningConnectorGrant = %+v, %v", grant, err)
	}
}

func TestAgentProvisioningTransportRejectsInvalidDelegatedRequesterBeforeHTTP(t *testing.T) {
	client, err := New(Config{
		BaseURL: "http://fleet.invalid",
		HTTPClient: &http.Client{Transport: agentProvisioningRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid delegated requester reached HTTP")
			return nil, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AgentProvisioning().BeginAgentProvisioning(
		t.Context(),
		"WS",
		AgentProvisioningBeginInput{DelegatedActor: " operator:docs "},
	)
	if !errors.Is(err, ErrAgentManagementInvalidDelegatedActor) {
		t.Fatalf("invalid delegated requester error = %v", err)
	}
}

func TestAgentProvisioningTransportPreservesTypedFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		want   error
	}{
		{name: "not found", status: http.StatusNotFound, code: "not_found", want: ErrAgentProvisioningNotFound},
		{name: "invalid", status: http.StatusBadRequest, code: "validation_failed", want: ErrAgentProvisioningInvalid},
		{name: "intent conflict", status: http.StatusConflict, code: "conflict", want: ErrAgentProvisioningConflict},
		{name: "concurrent write", status: http.StatusConflict, code: "revision_conflict", want: ErrAgentProvisioningConcurrentWrite},
		{name: "terminal transition", status: http.StatusConflict, code: "invalid_transition", want: ErrAgentProvisioningInvalidTransition},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, `{"error":{"code":"`+test.code+`","message":"failed"}}`)
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.AgentProvisioning().SaveAgentProvisioningProgress(
				context.Background(), "WS", "provision-1",
				AgentProvisioningProgressInput{
					ExpectedProvisioningGenerationID: "0123456789abcdef0123456789abcdef",
					ExpectedVersion:                  1,
					State:                            "running",
				},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is %v", err, test.want)
			}
		})
	}
}

func TestAgentProvisioningTransportNormalizesNilPendingCollection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"count":0}`)
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	values, err := client.AgentProvisioning().ListPendingAgentProvisioning(t.Context(), "WS", 0)
	if err != nil || values == nil || len(values) != 0 {
		t.Fatalf("pending = %#v, %v", values, err)
	}
}
