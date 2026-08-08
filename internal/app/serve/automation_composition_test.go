package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	automationfleetdb "github.com/tysonthomas9/loomcli/internal/modules/automation/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type automationExecutionStub struct{}

func (automationExecutionStub) EmissionContext(context.Context, authority.ExecutionAuthority) (*automation.ExecutionEmissionContext, error) {
	return nil, automation.ErrUnavailable
}

func (automationExecutionStub) Dispatch(context.Context, automation.ExecutionDispatchRequest) (*automation.ExecutionDispatchResult, error) {
	return nil, automation.ErrUnavailable
}

type automationCronStub struct{}

func (automationCronStub) ClaimDueCron(context.Context, automation.CronClaim) ([]automation.CronOccurrence, error) {
	return nil, nil
}

func (automationCronStub) CompleteCron(context.Context, automation.CronCompletion) error { return nil }

type automationAwaitNotifierStub struct{}

func (automationAwaitNotifierStub) NotifyAwaitEvent(context.Context, automation.AwaitEventNotification) error {
	return nil
}

type automationWorkspaceListerStub struct{}

func (automationWorkspaceListerStub) ListWorkspaceKeys(context.Context) ([]string, error) {
	return []string{"TEST"}, nil
}

type automationVerifierStub struct{}

func (automationVerifierStub) Verify(context.Context, webhookingestion.VerificationRequest) error {
	return nil
}

type automationWorkflowTargetPreparerStub struct{}

func (automationWorkflowTargetPreparerStub) PrepareWorkflowTarget(context.Context, string, string) (workflowbinding.WorkflowTarget, error) {
	return workflowbinding.WorkflowTarget{DriverID: "driver-1", DriverVersionID: "version-1"}, nil
}

type automationOperatorResolverStub struct{}

func (automationOperatorResolverStub) ResolveOperatorAuthority(*http.Request, string, authority.Action) (authority.OperatorAuthority, error) {
	return authority.OperatorAuthority{}, authority.ErrAdmissionDenied
}

type automationModuleTransportStub struct {
	automationfleetdb.Transport
}

type automationApprovalStoreStub struct{}

func (automationApprovalStoreStub) AppendTriggerEvent(
	_ context.Context,
	event *automation.Event,
) (*automation.Event, error) {
	if event == nil {
		return nil, automation.ErrInvalid
	}
	result := *event
	return &result, nil
}

func TestComposeAutomationCapabilityDisabledConstructsNothing(t *testing.T) {
	capability, err := composeAutomationCapability(automationCapabilityConfig{}, automationCapabilityDependencies{})
	if err != nil || capability != nil {
		t.Fatalf("disabled capability = %#v, err=%v", capability, err)
	}
}

func TestComposeAutomationCapabilityExposesOnlyNarrowPortsAndTwoComponents(t *testing.T) {
	adapter, err := automationfleetdb.New(&automationModuleTransportStub{})
	if err != nil {
		t.Fatalf("new Automation adapter: %v", err)
	}
	issuer := authority.NewIssuer()
	catalog := catalogOwner{
		effectiveVersions: workflowcatalog.New(nil, nil, nil), issuer: issuer,
		effectiveVersionAuthority: testEffectiveVersionAuthority(issuer),
		operatorResolver:          automationOperatorResolverStub{},
	}
	capability, err := composeAutomationCapability(automationCapabilityConfig{
		enabled: true, workspaceKey: "TEST", catalog: catalog,
	}, automationCapabilityDependencies{
		bindings: adapter, unmanagedBindings: adapter, managedBindings: adapter,
		matcher: adapter, events: adapter, deliveries: adapter,
		admissions: adapter, approvalEvents: automationApprovalStoreStub{},
		execution: automationExecutionStub{}, cron: automationCronStub{},
		retries: adapter, awaits: automationAwaitNotifierStub{},
		workspaces: automationWorkspaceListerStub{}, webhookVerifier: automationVerifierStub{},
		workflowTargets: automationWorkflowTargetPreparerStub{},
	})
	if err != nil {
		t.Fatalf("composeAutomationCapability: %v", err)
	}
	if capability.BindingOperations() == nil || capability.AuditQueries() == nil ||
		capability.WebhookWorkflow() == nil || capability.WorkflowEventing() == nil ||
		capability.WorkflowBinding() == nil ||
		capability.IssueJournalEmitter() == nil || capability.ApprovalJournal() == nil ||
		capability.ApprovalAuthorityProvider() == nil || capability.RunOutcomePublisher() == nil ||
		capability.OperatorAuthorityResolver() == nil {
		t.Fatalf("capability has missing narrow port: %#v", capability)
	}
	registrations := capability.RuntimeRegistrations()
	if len(registrations) != 2 || registrations[0].Component.ID() != automation.CronSchedulerComponentID ||
		registrations[1].Component.ID() != automation.DeliverySweeperComponentID {
		t.Fatalf("runtime registrations = %#v", registrations)
	}
	registrations[0] = registrations[1]
	if got := capability.RuntimeRegistrations()[0].Component.ID(); got != automation.CronSchedulerComponentID {
		t.Fatalf("caller mutated registrations: first id = %q", got)
	}
	if (*AutomationCapability)(nil).BindingOperations() != nil ||
		(*AutomationCapability)(nil).WorkflowBinding() != nil ||
		(*AutomationCapability)(nil).ApprovalJournal() != nil ||
		(*AutomationCapability)(nil).ApprovalAuthorityProvider() != nil ||
		(*AutomationCapability)(nil).RunOutcomePublisher() != nil ||
		(*AutomationCapability)(nil).RuntimeRegistrations() != nil {
		t.Fatal("nil capability accessors did not fail closed")
	}

	// Exercise the composed webhook workflow rather than the policy package
	// directly. ErrInvalid (instead of ErrUnavailable or a transport call)
	// proves serve injected the real canonical policy into Automation and that
	// it rejected the reserved lifecycle tuple before persistence.
	_, err = capability.WebhookWorkflow().Ingest(t.Context(), webhookingestion.IngestRequest{
		WorkspaceKey: "TEST", SourceKind: "github", SourceRef: "repo:loom",
		RouteKey: "github.run.finished", SourceEventID: "run-finished:child:completed",
		EventType: execution.RunFinishedEventType, SubjectRef: "child", ActorRef: "system",
		Payload: json.RawMessage(`{}`), PresentedSignature: "verified-by-test-stub",
	})
	if !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("composed run.finished spoof admission error = %v, want Automation invalid provenance", err)
	}
}

func TestComposeAutomationCapabilityRequiresEveryProductionPort(t *testing.T) {
	adapter, err := automationfleetdb.New(&automationModuleTransportStub{})
	if err != nil {
		t.Fatalf("new Automation adapter: %v", err)
	}
	issuer := authority.NewIssuer()
	catalog := catalogOwner{
		effectiveVersions: workflowcatalog.New(nil, nil, nil), issuer: issuer,
		effectiveVersionAuthority: testEffectiveVersionAuthority(issuer),
		operatorResolver:          automationOperatorResolverStub{},
	}
	valid := automationCapabilityDependencies{
		bindings: adapter, unmanagedBindings: adapter, managedBindings: adapter,
		matcher: adapter, events: adapter, deliveries: adapter,
		admissions: adapter, approvalEvents: automationApprovalStoreStub{},
		execution: automationExecutionStub{}, cron: automationCronStub{},
		retries: adapter, awaits: automationAwaitNotifierStub{},
		workspaces: automationWorkspaceListerStub{}, webhookVerifier: automationVerifierStub{},
		workflowTargets: automationWorkflowTargetPreparerStub{},
	}
	tests := []struct {
		name   string
		mutate func(*automationCapabilityDependencies)
	}{
		{"bindings", func(value *automationCapabilityDependencies) { value.bindings = nil }},
		{"unmanaged bindings", func(value *automationCapabilityDependencies) { value.unmanagedBindings = nil }},
		{"managed bindings", func(value *automationCapabilityDependencies) { value.managedBindings = nil }},
		{"matcher", func(value *automationCapabilityDependencies) { value.matcher = nil }},
		{"events", func(value *automationCapabilityDependencies) { value.events = nil }},
		{"deliveries", func(value *automationCapabilityDependencies) { value.deliveries = nil }},
		{"admissions", func(value *automationCapabilityDependencies) { value.admissions = nil }},
		{"approval events", func(value *automationCapabilityDependencies) { value.approvalEvents = nil }},
		{"execution", func(value *automationCapabilityDependencies) { value.execution = nil }},
		{"cron", func(value *automationCapabilityDependencies) { value.cron = nil }},
		{"retries", func(value *automationCapabilityDependencies) { value.retries = nil }},
		{"awaits", func(value *automationCapabilityDependencies) { value.awaits = nil }},
		{"workspaces", func(value *automationCapabilityDependencies) { value.workspaces = nil }},
		{"webhook verifier", func(value *automationCapabilityDependencies) { value.webhookVerifier = nil }},
		{"workflow targets", func(value *automationCapabilityDependencies) { value.workflowTargets = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := valid
			test.mutate(&dependencies)
			capability, err := composeAutomationCapability(automationCapabilityConfig{
				enabled: true, catalog: catalog,
			}, dependencies)
			if capability != nil || !errors.Is(err, automation.ErrUnavailable) {
				t.Fatalf("capability = %#v, error = %v, want unavailable", capability, err)
			}
		})
	}
	// A fixed workspace is a valid alternative to the dynamic lister.
	withoutLister := valid
	withoutLister.workspaces = nil
	if _, err := composeAutomationCapability(automationCapabilityConfig{
		enabled: true, workspaceKey: "TEST", catalog: catalog,
	}, withoutLister); err != nil {
		t.Fatalf("fixed-workspace composition: %v", err)
	}
}

func TestAutomationRuntimeAuthorityExpiresWithServerClock(t *testing.T) {
	issuer := authority.NewIssuer()
	now := time.Now()
	provider := &automationRuntimeAuthorityProvider{issuer: issuer, now: func() time.Time { return now }}
	value, err := provider.AuthorityForAutomationRuntime(context.Background(), automation.CronSchedulerComponentID, "TEST", automation.ActionSweepCron)
	if err != nil {
		t.Fatalf("AuthorityForAutomationRuntime: %v", err)
	}
	if !value.ExpiresAt().Equal(now.Add(time.Minute)) {
		t.Fatalf("expiry = %s, want %s", value.ExpiresAt(), now.Add(time.Minute))
	}
}

func testEffectiveVersionAuthority(issuer *authority.Issuer) EffectiveVersionAuthority {
	return func(
		_ context.Context,
		workspace,
		reason string,
	) (authority.SystemAuthority, error) {
		principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
			Subject: "automation", Class: authority.ClassSystem,
			Workspace: workspace,
			Actions:   []authority.Action{workflowcatalog.ActionResolveEffectiveVersion},
			ExpiresAt: time.Now().Add(time.Minute),
		})
		if err != nil {
			return authority.SystemAuthority{}, err
		}
		return issuer.IssueSystem(
			principal,
			workspace,
			workflowcatalog.ActionResolveEffectiveVersion,
			reason,
		)
	}
}
