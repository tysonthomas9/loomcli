package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/webui/operatorid"
)

// fakeActorBackend implements just enough of backend.IssueBackend for the
// check: the embedded nil interface satisfies the type, and only the probe
// methods are ever called.
type fakeActorBackend struct {
	backend.IssueBackend
	err       error
	gotActor  string
	workspace string
}

func (f *fakeActorBackend) CheckActorAccess(_ context.Context, actor string) error {
	f.gotActor = actor
	return f.err
}

func (f *fakeActorBackend) Workspace() string { return f.workspace }

// plainBackend has no actor-probe capability (any non-fleet backend).
type plainBackend struct{ backend.IssueBackend }

func TestCheckOperatorActorRole(t *testing.T) {
	forbidden := backend.ErrUnavailable("CheckActorAccess",
		`actor "operator@local" is not authorized in workspace "PUPPET" (fleet-db returned: workspace access denied)`, nil)
	unreachable := backend.ErrUnavailable("CheckActorAccess", "fleet server unreachable: dial tcp: refused", nil)

	tests := []struct {
		name       string
		deps       *cli.Deps
		apiKey     string
		wantStatus CheckStatus
		wantSkip   bool
		wantIn     string
	}{
		{name: "no deps", deps: nil, wantSkip: true},
		{name: "no issue backend", deps: &cli.Deps{}, wantSkip: true},
		{
			name:     "backend cannot probe actors",
			deps:     &cli.Deps{IssueBackend: &plainBackend{}},
			wantSkip: true,
		},
		{
			name:     "api key configured means X-Actor is ignored",
			deps:     &cli.Deps{IssueBackend: &fakeActorBackend{workspace: "PUPPET"}},
			apiKey:   "secret",
			wantSkip: true,
		},
		{
			name:       "authorized",
			deps:       &cli.Deps{IssueBackend: &fakeActorBackend{workspace: "PUPPET"}},
			wantStatus: StatusPass,
			wantIn:     "PUPPET",
		},
		{
			name:       "role-less operator actor",
			deps:       &cli.Deps{IssueBackend: &fakeActorBackend{workspace: "PUPPET", err: forbidden}},
			wantStatus: StatusWarn,
			wantIn:     "has no role in fleet-db workspace",
		},
		{
			name:     "transport failure is left to the reachability check",
			deps:     &cli.Deps{IssueBackend: &fakeActorBackend{workspace: "PUPPET", err: unreachable}},
			wantSkip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(bootstrap.EnvFleetDBAPIKey, tt.apiKey)
			t.Setenv(operatorid.EnvOperatorActor, "")

			got := checkOperatorActorRole(tt.deps)

			if tt.wantSkip {
				if got.Name != "" {
					t.Fatalf("check reported %+v, want skip", got)
				}
				return
			}
			if got.Name != "operator_actor_role" {
				t.Fatalf("Name = %q, want operator_actor_role", got.Name)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %v, want %v", got.Status, tt.wantStatus)
			}
			if !strings.Contains(got.Summary, tt.wantIn) {
				t.Errorf("Summary = %q, want it to contain %q", got.Summary, tt.wantIn)
			}
			if tt.wantStatus == StatusWarn {
				if !strings.Contains(got.Detail, "fleet-db:acl:global-roles:") {
					t.Errorf("Detail = %q, want the redis remediation", got.Detail)
				}
				if !strings.Contains(got.Detail, "fall back to the process actor") {
					t.Errorf("Detail = %q, want it to say writes still succeed", got.Detail)
				}
			}
		})
	}
}

func TestCheckOperatorActorRole_ProbesTheConfiguredOperatorActor(t *testing.T) {
	t.Setenv(bootstrap.EnvFleetDBAPIKey, "")
	t.Setenv(operatorid.EnvOperatorActor, "alice@example.com")

	be := &fakeActorBackend{workspace: "PUPPET"}
	if got := checkOperatorActorRole(&cli.Deps{IssueBackend: be}); got.Status != StatusPass {
		t.Fatalf("Status = %v, want pass", got.Status)
	}
	if be.gotActor != "alice@example.com" {
		t.Errorf("probed actor = %q, want the configured operator actor", be.gotActor)
	}
}

func TestIsActorForbidden(t *testing.T) {
	if isActorForbidden(errors.New("plain error")) {
		t.Error("a non-backend error was treated as a denial")
	}
	if isActorForbidden(backend.ErrUnavailable("op", "fleet server unreachable", nil)) {
		t.Error("a transport failure was treated as a denial")
	}
	if !isActorForbidden(backend.ErrUnavailable("op", `actor "x" is not authorized in workspace "y"`, nil)) {
		t.Error("the authorization verdict was not recognized")
	}
}
