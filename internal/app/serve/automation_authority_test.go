package serve

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

func TestAutomationRuntimeAuthorityProviderAllowsOnlyRegisteredPairs(t *testing.T) {
	issuer := authority.NewIssuer()
	provider := &automationRuntimeAuthorityProvider{issuer: issuer, now: time.Now}
	tests := []struct {
		component platformruntime.ComponentID
		action    authority.Action
		subject   string
	}{
		{automation.CronSchedulerComponentID, automation.ActionSweepCron, "system:cron"},
		{automation.DeliverySweeperComponentID, automation.ActionRetryDeliveries, "system:delivery-retry"},
	}
	for _, test := range tests {
		value, err := provider.AuthorityForAutomationRuntime(context.Background(), test.component, "TEST", test.action)
		if err != nil {
			t.Fatalf("AuthorityForAutomationRuntime(%s): %v", test.component, err)
		}
		if value.Subject() != test.subject || value.Workspace() != "TEST" || value.Action() != test.action || value.Reason() == "" {
			t.Fatalf("authority = subject:%q workspace:%q action:%q reason:%q", value.Subject(), value.Workspace(), value.Action(), value.Reason())
		}
	}

	for _, test := range []struct {
		component platformruntime.ComponentID
		action    authority.Action
	}{
		{automation.CronSchedulerComponentID, automation.ActionRetryDeliveries},
		{automation.DeliverySweeperComponentID, automation.ActionSweepCron},
		{"unknown", automation.ActionSweepCron},
	} {
		if _, err := provider.AuthorityForAutomationRuntime(context.Background(), test.component, "TEST", test.action); !errors.Is(err, errUnregisteredAutomationRuntimeAction) {
			t.Fatalf("unregistered pair (%q,%q) error = %v", test.component, test.action, err)
		}
	}
}

func TestAutomationAuthorityProvidersFailClosed(t *testing.T) {
	if got := newAutomationRuntimeAuthorityProvider(nil); got != nil {
		t.Fatalf("nil runtime provider = %#v, want nil", got)
	}
	if got := newAutomationWebhookAuthorityProvider(nil); got != nil {
		t.Fatalf("nil webhook provider = %#v, want nil", got)
	}
	if got := newAutomationExecutionAuthorityProvider(nil); got != nil {
		t.Fatalf("nil execution provider = %#v, want nil", got)
	}
	if got := newAutomationSystemAuthorityProvider(nil); got != nil {
		t.Fatalf("nil system event provider = %#v, want nil", got)
	}

	runtimeProvider := &automationRuntimeAuthorityProvider{}
	if _, err := runtimeProvider.AuthorityForAutomationRuntime(context.Background(), automation.CronSchedulerComponentID, "TEST", automation.ActionSweepCron); !errors.Is(err, automation.ErrUnavailable) {
		t.Fatalf("nil runtime issuer error = %v, want %v", err, automation.ErrUnavailable)
	}
	webhookProvider := &automationWebhookAuthorityProvider{}
	if _, err := webhookProvider.AuthorityForVerifiedWebhook(context.Background(), webhookingestion.AuthorityRequest{WorkspaceKey: "TEST", SourceKind: "github", RouteKey: "github.push"}); !errors.Is(err, automation.ErrUnavailable) {
		t.Fatalf("nil webhook issuer error = %v, want %v", err, automation.ErrUnavailable)
	}
	executionProvider := &automationExecutionAuthorityProvider{}
	if _, err := executionProvider.AuthorityForVerifiedRun(context.Background(), workfloweventing.VerifiedRun{}); !errors.Is(err, automation.ErrUnavailable) {
		t.Fatalf("nil execution issuer error = %v, want %v", err, automation.ErrUnavailable)
	}
	systemProvider := &automationSystemAuthorityProvider{}
	if _, err := systemProvider.AuthorityForVerifiedSource(context.Background(), systemeventing.VerifiedSource{}); !errors.Is(err, automation.ErrUnavailable) {
		t.Fatalf("nil system event issuer error = %v, want %v", err, automation.ErrUnavailable)
	}
}

func TestAutomationSystemAuthorityProviderAllowsOnlyRegisteredSources(t *testing.T) {
	issuer := authority.NewIssuer()
	provider := &automationSystemAuthorityProvider{issuer: issuer, now: time.Now}
	value, err := provider.AuthorityForVerifiedSource(context.Background(), systemeventing.VerifiedSource{
		ComponentID:  systemeventing.IssueJournalBridgeComponentID,
		WorkspaceKey: "TEST",
		ActorRef:     "driver-run:parent",
	})
	if err != nil {
		t.Fatalf("AuthorityForVerifiedSource: %v", err)
	}
	if value.Subject() != "driver-run:parent" || value.Workspace() != "TEST" ||
		value.Action() != automation.ActionAdmitEvent || value.Reason() == "" {
		t.Fatalf("authority = subject:%q workspace:%q action:%q reason:%q", value.Subject(), value.Workspace(), value.Action(), value.Reason())
	}
	outcome, err := provider.AuthorityForVerifiedSource(context.Background(), systemeventing.VerifiedSource{
		ComponentID: systemeventing.DriverRunOutcomeComponentID, WorkspaceKey: "TEST", ActorRef: driver.RunFinishedActor,
	})
	if err != nil {
		t.Fatalf("driver outcome AuthorityForVerifiedSource: %v", err)
	}
	if outcome.Subject() != driver.RunFinishedActor || outcome.Workspace() != "TEST" ||
		outcome.Action() != automation.ActionAdmitEvent || outcome.Reason() == "" {
		t.Fatalf("driver outcome authority = subject:%q workspace:%q action:%q reason:%q", outcome.Subject(), outcome.Workspace(), outcome.Action(), outcome.Reason())
	}
	for _, source := range []systemeventing.VerifiedSource{
		{ComponentID: "unknown", WorkspaceKey: "TEST"},
		{ComponentID: systemeventing.IssueJournalBridgeComponentID, WorkspaceKey: " TEST "},
		{ComponentID: systemeventing.DriverRunOutcomeComponentID, WorkspaceKey: "TEST", ActorRef: "system:forged"},
	} {
		if _, err := provider.AuthorityForVerifiedSource(context.Background(), source); !errors.Is(err, authority.ErrInvalidScope) {
			t.Fatalf("invalid source %+v error = %v, want %v", source, err, authority.ErrInvalidScope)
		}
	}
}

func TestAutomationExecutionAuthorityProviderBindsVerifiedFencedRun(t *testing.T) {
	issuer := authority.NewIssuer()
	provider := &automationExecutionAuthorityProvider{issuer: issuer, now: time.Now}
	parent := workfloweventing.VerifiedRun{
		WorkspaceKey: "TEST", RunID: "run-1", Status: "running",
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 9,
	}
	value, err := provider.AuthorityForVerifiedRun(context.Background(), parent)
	if err != nil {
		t.Fatalf("AuthorityForVerifiedRun: %v", err)
	}
	if value.Subject() != parent.RunID || value.Workspace() != parent.WorkspaceKey || value.Action() != automation.ActionAdmitEvent ||
		value.ResourceKind() != authority.ExecutionResourceDriverRun || value.ResourceID() != parent.RunID ||
		value.NodeID() != parent.NodeID || value.LeaseID() != parent.LeaseID || value.FencingToken() != parent.FencingToken {
		t.Fatalf("authority = subject:%q workspace:%q action:%q", value.Subject(), value.Workspace(), value.Action())
	}

	invalid := []workfloweventing.VerifiedRun{
		{WorkspaceKey: "TEST", RunID: "run-1", Status: "queued", NodeID: "node", LeaseID: "lease", FencingToken: 1},
		{WorkspaceKey: "TEST", RunID: "run-1", Status: "running", LeaseID: "lease", FencingToken: 1},
		{WorkspaceKey: "TEST", RunID: "run-1", Status: "running", NodeID: "node", FencingToken: 1},
		{WorkspaceKey: "TEST", RunID: "run-1", Status: "running", NodeID: "node", LeaseID: "lease"},
		{WorkspaceKey: "TEST", RunID: "run-1", Status: "running", NodeID: " node", LeaseID: "lease", FencingToken: 1},
		{WorkspaceKey: "TEST", RunID: "run-1", Status: "running", NodeID: "node", LeaseID: "lease ", FencingToken: 1},
		{WorkspaceKey: " TEST ", RunID: "run-1", Status: "running", NodeID: "node", LeaseID: "lease", FencingToken: 1},
	}
	for _, candidate := range invalid {
		if _, err := provider.AuthorityForVerifiedRun(context.Background(), candidate); !errors.Is(err, authority.ErrInvalidScope) {
			t.Fatalf("invalid parent %+v error = %v, want %v", candidate, err, authority.ErrInvalidScope)
		}
	}
}

func TestAutomationOperationRulesDefaultDenyEveryWrongClass(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(automationOperationRules()...)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	now := time.Now()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "operator", Class: authority.ClassOperator, Workspace: "TEST",
		Actions: []authority.Action{automation.ActionCreateBinding, automation.ActionAdmitEvent}, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("DeriveVerifiedPrincipal: %v", err)
	}
	operator, err := issuer.IssueOperator(principal, "TEST", automation.ActionCreateBinding)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}
	if err := admission.RequireOperator(automation.ActionCreateBinding, "TEST", operator); err != nil {
		t.Fatalf("operator create admission: %v", err)
	}
	wrongAction, err := issuer.IssueOperator(principal, "TEST", automation.ActionAdmitEvent)
	if err != nil {
		t.Fatalf("IssueOperator admit-event: %v", err)
	}
	if err := admission.Admit(automation.ActionAdmitEvent, "TEST", wrongAction); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("operator event admission error = %v, want denial", err)
	}
	if got, want := automationOperatorActions(), []authority.Action{
		automation.ActionCreateBinding, automation.ActionUpdateBinding, automation.ActionEnableBinding,
		automation.ActionDisableBinding, automation.ActionDeleteBinding,
		automation.ActionCreateManagedBinding, automation.ActionUpdateManagedBinding,
		automation.ActionEnableManagedBinding, automation.ActionDisableManagedBinding,
		automation.ActionDeleteManagedBinding, automation.ActionDispatchBinding,
	}; !slices.Equal(got, want) {
		t.Fatalf("operator actions = %v, want %v", got, want)
	}
}

func TestAutomationApprovalAuthorityBindsVerifiedSessionActor(t *testing.T) {
	issuer := authority.NewIssuer()
	now := time.Now()
	provider := &automationApprovalAuthorityProvider{issuer: issuer, now: func() time.Time { return now }}
	value, err := provider.AuthorityForVerifiedSession(t.Context(), "TEST", "reviewer@example.test")
	if err != nil {
		t.Fatalf("AuthorityForVerifiedSession: %v", err)
	}
	if value.Subject() != "reviewer@example.test" || value.Workspace() != "TEST" ||
		value.Action() != automation.ActionJournalApproval || !value.ExpiresAt().Equal(now.Add(time.Minute)) {
		t.Fatalf("approval authority = subject:%q workspace:%q action:%q expiry:%s",
			value.Subject(), value.Workspace(), value.Action(), value.ExpiresAt())
	}
	for _, candidate := range [][2]string{{"", "reviewer@example.test"}, {" TEST ", "reviewer@example.test"}, {"TEST", ""}, {"TEST", " reviewer@example.test "}} {
		if _, err := provider.AuthorityForVerifiedSession(t.Context(), candidate[0], candidate[1]); !errors.Is(err, authority.ErrInvalidScope) {
			t.Fatalf("invalid approval source %q/%q error = %v", candidate[0], candidate[1], err)
		}
	}
}

func TestAutomationWebhookAuthorityProviderBindsVerifiedSource(t *testing.T) {
	issuer := authority.NewIssuer()
	provider := &automationWebhookAuthorityProvider{issuer: issuer, now: time.Now}
	value, err := provider.AuthorityForVerifiedWebhook(context.Background(), webhookingestion.AuthorityRequest{
		WorkspaceKey: "TEST", SourceKind: "GitHub", RouteKey: "github.pull_request.opened",
	})
	if err != nil {
		t.Fatalf("AuthorityForVerifiedWebhook: %v", err)
	}
	if value.Subject() != "webhook:github:github.pull_request.opened" || value.Workspace() != "TEST" || value.Action() != automation.ActionAdmitEvent {
		t.Fatalf("authority = subject:%q workspace:%q action:%q", value.Subject(), value.Workspace(), value.Action())
	}

	for _, request := range []webhookingestion.AuthorityRequest{
		{WorkspaceKey: "", SourceKind: "github", RouteKey: "github.push"},
		{WorkspaceKey: "TEST", SourceKind: automation.SourceKindCron, RouteKey: "cron:b"},
		{WorkspaceKey: "TEST", SourceKind: automation.SourceKindInternal, RouteKey: "internal.task.ready"},
	} {
		if _, err := provider.AuthorityForVerifiedWebhook(context.Background(), request); !errors.Is(err, authority.ErrInvalidScope) {
			t.Fatalf("invalid request %+v error = %v, want %v", request, err, authority.ErrInvalidScope)
		}
	}
}
