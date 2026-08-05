package interaction

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

type runtimeInteractionAPIStub struct {
	API
	calls []runtimeReconcileCall
	errs  map[string]error
}

type runtimeReconcileCall struct {
	workspace string
	now       time.Time
}

func (stub *runtimeInteractionAPIStub) ReconcileSessions(
	_ context.Context,
	_ authority.SystemAuthority,
	workspace string,
	now time.Time,
) (int, error) {
	stub.calls = append(stub.calls, runtimeReconcileCall{workspace: workspace, now: now})
	return 0, stub.errs[workspace]
}

type runtimeAuthorityCall struct {
	component platformruntime.ComponentID
	workspace string
	action    authority.Action
}

type runtimeAuthorityProviderStub struct {
	auth  authority.SystemAuthority
	calls []runtimeAuthorityCall
	errs  map[string]error
}

func (stub *runtimeAuthorityProviderStub) AuthorityForInteractionRuntime(
	_ context.Context,
	component platformruntime.ComponentID,
	workspace string,
	action authority.Action,
) (authority.SystemAuthority, error) {
	stub.calls = append(stub.calls, runtimeAuthorityCall{
		component: component,
		workspace: workspace,
		action:    action,
	})
	return stub.auth, stub.errs[workspace]
}

type runtimeWorkspaceListerStub struct {
	values []string
	err    error
}

func (stub runtimeWorkspaceListerStub) ListWorkspaceKeys(context.Context) ([]string, error) {
	return stub.values, stub.err
}

func TestSessionRecoveryRuntimeUsesExactAuthorityForEveryWorkspace(t *testing.T) {
	api := &runtimeInteractionAPIStub{}
	provider := &runtimeAuthorityProviderStub{}
	registration, err := RuntimeRegistration(api, provider, RuntimeConfig{
		WorkspaceLister: runtimeWorkspaceListerStub{values: []string{"ZETA", "ALPHA"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	if err := registration.Component.RunOnce(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if registration.Component.ID() != SessionRecoveryComponentID ||
		!registration.Policy.Immediate ||
		registration.Policy.Cadence != SessionRecoveryCadence ||
		registration.Policy.Timeout != SessionRecoveryTimeout {
		t.Fatalf("registration = %+v", registration)
	}
	wantAuthority := []runtimeAuthorityCall{
		{component: SessionRecoveryComponentID, workspace: "ALPHA", action: ActionReconcileSessions},
		{component: SessionRecoveryComponentID, workspace: "ZETA", action: ActionReconcileSessions},
	}
	if !reflect.DeepEqual(provider.calls, wantAuthority) {
		t.Fatalf("authority calls = %#v, want %#v", provider.calls, wantAuthority)
	}
	wantReconcile := []runtimeReconcileCall{
		{workspace: "ALPHA", now: now},
		{workspace: "ZETA", now: now},
	}
	if !reflect.DeepEqual(api.calls, wantReconcile) {
		t.Fatalf("reconcile calls = %#v, want %#v", api.calls, wantReconcile)
	}
}

func TestSessionRecoveryRuntimeContinuesAfterAuthorityAndReconcileFailures(t *testing.T) {
	authErr := errors.New("authority")
	reconcileErr := errors.New("reconcile")
	api := &runtimeInteractionAPIStub{errs: map[string]error{"BETA": reconcileErr}}
	provider := &runtimeAuthorityProviderStub{errs: map[string]error{"ALPHA": authErr}}
	registration, err := RuntimeRegistration(api, provider, RuntimeConfig{
		WorkspaceLister: runtimeWorkspaceListerStub{values: []string{"ALPHA", "BETA", "GAMMA"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = registration.Component.RunOnce(t.Context(), time.Now())
	if !errors.Is(err, authErr) || !errors.Is(err, reconcileErr) {
		t.Fatalf("RunOnce error = %v", err)
	}
	if len(provider.calls) != 3 || len(api.calls) != 2 {
		t.Fatalf("authority calls = %d, reconcile calls = %d", len(provider.calls), len(api.calls))
	}
}

func TestSessionRecoveryRuntimeRejectsInvalidCompositionAndWorkspaceState(t *testing.T) {
	api := &runtimeInteractionAPIStub{}
	provider := &runtimeAuthorityProviderStub{}
	if _, err := RuntimeRegistration(nil, provider, RuntimeConfig{WorkspaceKey: "WS"}); err == nil {
		t.Fatal("expected nil API error")
	}
	if _, err := RuntimeRegistration(api, nil, RuntimeConfig{WorkspaceKey: "WS"}); err == nil {
		t.Fatal("expected nil authority provider error")
	}
	if _, err := RuntimeRegistration(api, provider, RuntimeConfig{}); err == nil {
		t.Fatal("expected missing scope error")
	}
	for name, values := range map[string][]string{
		"blank":     {"WS", ""},
		"unclean":   {" WS"},
		"duplicate": {"WS", "WS"},
	} {
		t.Run(name, func(t *testing.T) {
			registration, err := RuntimeRegistration(api, provider, RuntimeConfig{
				WorkspaceLister: runtimeWorkspaceListerStub{values: values},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := registration.Component.RunOnce(t.Context(), time.Now()); err == nil {
				t.Fatal("expected persisted workspace error")
			}
		})
	}
}
