package agentprovisioning

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

type recoveryCall struct {
	workspace string
	limit     int
}

type recoveryCommandsStub struct {
	calls []recoveryCall
	errs  map[string]error
}

func (stub *recoveryCommandsStub) Recover(_ context.Context, workspace string, limit int) (int, error) {
	stub.calls = append(stub.calls, recoveryCall{workspace: workspace, limit: limit})
	return 0, stub.errs[workspace]
}

type recoveryWorkspaceListerStub struct {
	values []string
	err    error
}

func (stub recoveryWorkspaceListerStub) ListWorkspaceKeys(context.Context) ([]string, error) {
	return stub.values, stub.err
}

func TestRuntimeRegistrationRecoversEveryCurrentWorkspaceInStableOrder(t *testing.T) {
	commands := &recoveryCommandsStub{}
	registration, err := RuntimeRegistration(commands, RuntimeConfig{
		WorkspaceLister: recoveryWorkspaceListerStub{values: []string{"ZETA", "ALPHA"}},
		Limit:           7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if registration.Component.ID() != RecoveryComponentID ||
		!registration.Policy.Immediate ||
		registration.Policy.Cadence != DefaultRecoveryCadence ||
		registration.Policy.Timeout != DefaultRecoveryTimeout {
		t.Fatalf("registration = %+v", registration)
	}
	if err := registration.Component.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	want := []recoveryCall{{workspace: "ALPHA", limit: 7}, {workspace: "ZETA", limit: 7}}
	if !reflect.DeepEqual(commands.calls, want) {
		t.Fatalf("calls = %#v, want %#v", commands.calls, want)
	}
}

func TestRecoveryComponentContinuesAcrossWorkspaceFailure(t *testing.T) {
	boom := errors.New("boom")
	commands := &recoveryCommandsStub{errs: map[string]error{"ALPHA": boom}}
	registration, err := RuntimeRegistration(commands, RuntimeConfig{
		WorkspaceLister: recoveryWorkspaceListerStub{values: []string{"ALPHA", "BETA"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = registration.Component.RunOnce(t.Context(), time.Now())
	if !errors.Is(err, boom) {
		t.Fatalf("RunOnce error = %v", err)
	}
	if len(commands.calls) != 2 {
		t.Fatalf("calls = %#v", commands.calls)
	}
}

func TestRuntimeRegistrationFailsClosedForInvalidScope(t *testing.T) {
	commands := &recoveryCommandsStub{}
	for name, config := range map[string]RuntimeConfig{
		"missing scope":   {},
		"negative limit":  {WorkspaceKey: "WS", Limit: -1},
		"oversized limit": {WorkspaceKey: "WS", Limit: MaxRecoveryLimit + 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RuntimeRegistration(commands, config); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRecoveryComponentRejectsAmbiguousWorkspaceList(t *testing.T) {
	for name, values := range map[string][]string{
		"blank":     {"WS", ""},
		"unclean":   {" WS"},
		"duplicate": {"WS", "WS"},
	} {
		t.Run(name, func(t *testing.T) {
			registration, err := RuntimeRegistration(&recoveryCommandsStub{}, RuntimeConfig{
				WorkspaceLister: recoveryWorkspaceListerStub{values: values},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := registration.Component.RunOnce(t.Context(), time.Now()); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRecoveryComponentHasStablePlatformIdentity(t *testing.T) {
	var component platformruntime.Component = (*recoveryComponent)(nil)
	_ = component
	if RecoveryComponentID != "serve-agent-provisioning-recovery" {
		t.Fatalf("component id = %q", RecoveryComponentID)
	}
}
