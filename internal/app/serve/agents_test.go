package serve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type agentsRoundTripFunc func(*http.Request) (*http.Response, error)

type agentsTriggerBindingStoreStub struct {
	automation.TriggerBindingStore
}

func (*agentsTriggerBindingStoreStub) List(
	context.Context,
	string,
	automation.TriggerBindingFilter,
) ([]*automation.Binding, error) {
	return nil, nil
}

func (function agentsRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestAgentsCompositionUsesPublicAPIAndTrustedOperatorAttribution(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	calls := 0
	httpClient := &http.Client{Transport: agentsRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		recorder := newAgentsResponseRecorder()
		switch calls {
		case 1:
			if request.Method != http.MethodGet || request.URL.Path != "/api/v1/WS/roles/docs" {
				t.Fatalf("role request = %s %s", request.Method, request.URL.Path)
			}
			writeAgentsResponse(t, recorder, agents.Role{
				WorkspaceKey: "WS", Name: "docs", Kind: agents.RoleKindWorker,
				Prompt: "Review docs.", CreatedAt: now, UpdatedAt: now,
			})
		case 2:
			if request.Method != http.MethodPost || request.URL.Path != "/api/v1/WS/agent-services" {
				t.Fatalf("create request = %s %s", request.Method, request.URL.Path)
			}
			if got := request.Header.Get(infrafleetdb.FleetDelegatedActorHeader); got != LocalOpenOperatorSubject {
				t.Fatalf("delegated actor = %q", got)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "created_by") ||
				strings.Contains(string(body), LocalOpenOperatorSubject) {
				t.Fatalf("create body leaked audit actor: %s", body)
			}
			writeAgentsResponse(t, recorder, agents.AgentServiceRecord{
				WorkspaceKey: "WS", ServiceID: "agent-docs", Name: "Docs review",
				GenerationID: "00112233445566778899aabbccddeeff",
				Kind:         agents.AgentKindEvent, DesiredState: agents.DesiredRunning,
				RoleName: "docs", MaxInstances: 1, CreatedBy: LocalOpenOperatorSubject,
				CreatedAt: now, UpdatedAt: now,
			})
		default:
			t.Fatalf("unexpected Fleet call %d", calls)
		}
		return recorder.Result(), nil
	})}
	client, err := infrafleetdb.New(infrafleetdb.Config{
		BaseURL: "http://fleet.invalid", APIKey: "service-key", HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := NewAgentsCapability(AgentsConfig{
		FleetDBClient: client, TriggerBindings: &agentsTriggerBindingStoreStub{}, WorkspaceKey: "WS",
	})
	if err != nil {
		t.Fatal(err)
	}
	if capability.PRReviewerProvisioning() == nil {
		t.Fatal("Agents composition omitted the PR reviewer application workflow")
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://loom.invalid/agents", nil)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := capability.OperatorAuthorityResolver().ResolveOperatorAuthority(
		request,
		"WS",
		agents.ActionCreateAgent,
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := capability.AgentsAPI().CreateAgent(t.Context(), auth, agents.CreateAgentCommand{
		WorkspaceKey: "WS", AgentID: "agent-docs", Name: "Docs review",
		Kind: agents.AgentKindEvent, Behavior: agents.BehaviorReference{RoleName: "docs"},
		DesiredState: agents.DesiredRunning, MaxInstances: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created == nil || created.AgentID != "agent-docs" || created.CreatedBy != LocalOpenOperatorSubject {
		t.Fatalf("created = %#v", created)
	}
	if calls != 2 {
		t.Fatalf("Fleet calls = %d, want 2", calls)
	}
	if _, err := capability.OperatorAuthorityResolver().ResolveOperatorAuthority(
		request,
		"WS",
		authority.Action("workflowcatalog.approve"),
	); !errors.Is(err, authority.ErrActionNotAllowed) {
		t.Fatalf("cross-capability operator action = %v", err)
	}
}

func TestAgentsCompositionFailsClosedWithoutFleetOrExternalIdentityResolver(t *testing.T) {
	if capability, err := NewAgentsCapability(AgentsConfig{}); capability != nil ||
		!errors.Is(err, agents.ErrUnavailable) {
		t.Fatalf("nil Fleet composition = %#v, %v", capability, err)
	}
	client, err := infrafleetdb.New(infrafleetdb.Config{BaseURL: "http://fleet.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if capability, err := NewAgentsCapability(AgentsConfig{
		FleetDBClient: client, ExternalAuth: true,
	}); capability != nil || err == nil {
		t.Fatalf("missing external resolver composition = %#v, %v", capability, err)
	}
	var capability *AgentsCapability
	if capability.AgentsAPI() != nil ||
		capability.OperatorAuthorityResolver() != nil ||
		capability.PRReviewerProvisioning() != nil {
		t.Fatal("nil Agents capability exposed an API")
	}
}

type agentsResponseRecorder struct {
	header http.Header
	body   []byte
	status int
}

func newAgentsResponseRecorder() *agentsResponseRecorder {
	return &agentsResponseRecorder{header: make(http.Header), status: http.StatusOK}
}

func (recorder *agentsResponseRecorder) Header() http.Header {
	return recorder.header
}

func (recorder *agentsResponseRecorder) WriteHeader(status int) {
	recorder.status = status
}

func (recorder *agentsResponseRecorder) Write(payload []byte) (int, error) {
	recorder.body = append(recorder.body, payload...)
	return len(payload), nil
}

func (recorder *agentsResponseRecorder) Result() *http.Response {
	return &http.Response{
		StatusCode: recorder.status, Header: recorder.header,
		Body: io.NopCloser(strings.NewReader(string(recorder.body))),
	}
}

func writeAgentsResponse(t *testing.T, response http.ResponseWriter, value any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Fatal(err)
	}
}
