package serve

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type agentProvisioningProgressStub struct{}

func (agentProvisioningProgressStub) Begin(
	context.Context,
	agentprovisioning.Spec,
	string,
) (*agentprovisioning.Record, error) {
	return nil, agentprovisioning.ErrUnavailable
}

func (agentProvisioningProgressStub) Get(
	context.Context,
	string,
	string,
) (*agentprovisioning.Record, error) {
	return nil, agentprovisioning.ErrUnavailable
}

func (agentProvisioningProgressStub) Save(
	context.Context,
	*agentprovisioning.Record,
	int64,
) (*agentprovisioning.Record, error) {
	return nil, agentprovisioning.ErrUnavailable
}

func (agentProvisioningProgressStub) ListPending(
	context.Context,
	string,
	int,
) ([]*agentprovisioning.Record, error) {
	return []*agentprovisioning.Record{}, nil
}

func (agentProvisioningProgressStub) EnsureRole(
	context.Context,
	agentprovisioning.EnsureRoleCommand,
) error {
	return agentprovisioning.ErrUnavailable
}

func (agentProvisioningProgressStub) EnsureAgent(
	context.Context,
	agentprovisioning.EnsureAgentCommand,
) error {
	return agentprovisioning.ErrUnavailable
}

func (agentProvisioningProgressStub) EnsureBinding(
	context.Context,
	agentprovisioning.EnsureBindingCommand,
) error {
	return agentprovisioning.ErrUnavailable
}

func (agentProvisioningProgressStub) EnsureGrant(
	context.Context,
	agentprovisioning.EnsureGrantCommand,
) error {
	return agentprovisioning.ErrUnavailable
}

func TestAgentProvisioningCompositionPublishesOnlyCommandsAuthorityAndRecovery(t *testing.T) {
	now := time.Now()
	agentsIssuer := fixedAgentProvisioningAuthorityIssuer(t, now)
	automationIssuer := fixedAgentProvisioningAuthorityIssuer(t, now)
	connectorsIssuer := fixedAgentProvisioningAuthorityIssuer(t, now)
	resolver, err := NewLocalOpenOperatorResolver(
		agentsIssuer,
		agentprovisioning.ActionBeginProvisioning,
	)
	if err != nil {
		t.Fatal(err)
	}
	capability, err := composeAgentProvisioningCapability(agentProvisioningDependencies{
		progress:     agentProvisioningProgressStub{},
		agentsIssuer: agentsIssuer, automationIssuer: automationIssuer,
		connectorsIssuer: connectorsIssuer, operatorResolver: resolver,
		workspaceKey: "WS", now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if capability.AgentProvisioningCommands() == nil ||
		capability.OperatorAuthorityResolver() == nil {
		t.Fatal("composition omitted request commands or authority resolver")
	}
	registrations := capability.RuntimeRegistrations()
	if len(registrations) != 1 ||
		registrations[0].Component.ID() != agentprovisioning.RecoveryComponentID ||
		!registrations[0].Policy.Immediate {
		t.Fatalf("runtime registrations = %#v", registrations)
	}
	registrations[0].Component = nil
	if got := capability.RuntimeRegistrations(); len(got) != 1 ||
		got[0].Component.ID() != agentprovisioning.RecoveryComponentID {
		t.Fatal("runtime registration accessor leaked its backing slice")
	}

	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"http://loom.invalid/api/workspaces/WS/agents",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := capability.OperatorAuthorityResolver().ResolveOperatorAuthority(
		request,
		"WS",
		agentprovisioning.ActionBeginProvisioning,
	)
	if err != nil {
		t.Fatal(err)
	}
	if auth.Subject() != LocalOpenOperatorSubject ||
		auth.Action() != agentprovisioning.ActionBeginProvisioning {
		t.Fatalf("begin authority = subject:%q action:%q", auth.Subject(), auth.Action())
	}

	var nilCapability *AgentProvisioningCapability
	if nilCapability.AgentProvisioningCommands() != nil ||
		nilCapability.OperatorAuthorityResolver() != nil ||
		nilCapability.RuntimeRegistrations() != nil {
		t.Fatal("nil AgentProvisioning capability exposed dependencies")
	}
}

func TestAgentProvisioningAuthoritiesAreActionAndIssuerScoped(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	agentsIssuer := fixedAgentProvisioningAuthorityIssuer(t, now)
	automationIssuer := fixedAgentProvisioningAuthorityIssuer(t, now)
	connectorsIssuer := fixedAgentProvisioningAuthorityIssuer(t, now)
	provider := &agentProvisioningAuthorityProvider{
		agentsIssuer: agentsIssuer, automationIssuer: automationIssuer,
		connectorsIssuer: connectorsIssuer, now: func() time.Time { return now },
	}
	agentsAdmission, err := agentsIssuer.NewAdmission(agents.OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	automationAdmission, err := automationIssuer.NewAdmission(
		authority.Allow(automation.ActionEnsureManagedBinding, authority.ClassSystem),
	)
	if err != nil {
		t.Fatal(err)
	}
	connectorsAdmission, err := connectorsIssuer.NewAdmission(connectors.OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}

	roleAuth, err := provider.AuthorityForRole(t.Context(), "WS", "AgentProvisioning provision-1:role")
	if err != nil {
		t.Fatal(err)
	}
	agentAuth, err := provider.AuthorityForAgent(t.Context(), "WS", "AgentProvisioning provision-1:agent")
	if err != nil {
		t.Fatal(err)
	}
	bindingAuth, err := provider.AuthorityForBinding(t.Context(), "WS", "AgentProvisioning provision-1:binding")
	if err != nil {
		t.Fatal(err)
	}
	grantAuth, err := provider.AuthorityForGrant(t.Context(), "WS", "AgentProvisioning provision-1:grant:read")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		auth      authority.SystemAuthority
		action    authority.Action
		admission *authority.Admission
	}{
		{"role", roleAuth, agents.ActionEnsureManagedRole, agentsAdmission},
		{"agent", agentAuth, agents.ActionEnsureManagedAgent, agentsAdmission},
		{"binding", bindingAuth, automation.ActionEnsureManagedBinding, automationAdmission},
		{"grant", grantAuth, connectors.ActionEnsureGrant, connectorsAdmission},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.auth.Action() != test.action ||
				test.auth.Subject() != string(agentprovisioning.RecoveryComponentID) ||
				test.auth.Workspace() != "WS" {
				t.Fatalf(
					"authority = subject:%q workspace:%q action:%q",
					test.auth.Subject(),
					test.auth.Workspace(),
					test.auth.Action(),
				)
			}
			if err := test.admission.RequireSystem(test.action, "WS", test.auth); err != nil {
				t.Fatalf("owner admission rejected authority: %v", err)
			}
		})
	}
	if err := automationAdmission.RequireSystem(
		automation.ActionEnsureManagedBinding,
		"WS",
		roleAuth,
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("cross-issuer authority = %v, want admission denied", err)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := provider.AuthorityForGrant(
		cancelled,
		"WS",
		"AgentProvisioning provision-1:grant:read",
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled authority error = %v", err)
	}
	if _, err := provider.AuthorityForRole(
		t.Context(),
		"",
		"AgentProvisioning provision-1:role",
	); !errors.Is(err, authority.ErrInvalidScope) {
		t.Fatalf("empty scope authority error = %v", err)
	}
}

func TestAgentProvisioningCompositionFailsClosedWhenRequiredDependencyIsMissing(t *testing.T) {
	issuer := authority.NewIssuer()
	resolver, err := NewLocalOpenOperatorResolver(
		issuer,
		agentprovisioning.ActionBeginProvisioning,
	)
	if err != nil {
		t.Fatal(err)
	}
	base := agentProvisioningDependencies{
		progress:     agentProvisioningProgressStub{},
		agentsIssuer: issuer, automationIssuer: issuer, connectorsIssuer: issuer,
		operatorResolver: resolver, workspaceKey: "WS", now: time.Now,
	}
	tests := []struct {
		name   string
		mutate func(*agentProvisioningDependencies)
	}{
		{"progress", func(value *agentProvisioningDependencies) { value.progress = nil }},
		{"agents issuer", func(value *agentProvisioningDependencies) { value.agentsIssuer = nil }},
		{"automation issuer", func(value *agentProvisioningDependencies) { value.automationIssuer = nil }},
		{"connectors issuer", func(value *agentProvisioningDependencies) { value.connectorsIssuer = nil }},
		{"operator resolver", func(value *agentProvisioningDependencies) { value.operatorResolver = nil }},
		{"clock", func(value *agentProvisioningDependencies) { value.now = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := base
			test.mutate(&dependencies)
			capability, err := composeAgentProvisioningCapability(dependencies)
			if capability != nil || !errors.Is(err, agentprovisioning.ErrUnavailable) {
				t.Fatalf("composition = %#v, %v", capability, err)
			}
		})
	}
}

func fixedAgentProvisioningAuthorityIssuer(t *testing.T, now time.Time) *authority.Issuer {
	t.Helper()
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return issuer
}
