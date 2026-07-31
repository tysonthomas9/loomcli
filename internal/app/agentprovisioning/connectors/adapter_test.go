package connectors

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	moduleconnectors "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type grantCommandsFake struct {
	command agentprovisioning.EnsureGrantCommand
	err     error
	calls   int
}

func (fake *grantCommandsFake) EnsureGrant(
	_ context.Context,
	command agentprovisioning.EnsureGrantCommand,
) error {
	fake.calls++
	fake.command = command
	return fake.err
}

type authorityProviderFake struct {
	issuer    *authority.Issuer
	workspace string
	reason    string
	err       error
	calls     int
}

func (fake *authorityProviderFake) AuthorityForGrant(
	_ context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	fake.calls++
	fake.workspace, fake.reason = workspace, reason
	if fake.err != nil {
		return authority.SystemAuthority{}, fake.err
	}
	principal, err := fake.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   "serve-agent-provisioning-recovery",
		Class:     authority.ClassSystem,
		Workspace: workspace,
		Actions:   []authority.Action{moduleconnectors.ActionEnsureGrant},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return fake.issuer.IssueSystem(
		principal,
		workspace,
		moduleconnectors.ActionEnsureGrant,
		reason,
	)
}

func TestAdapterRequestsFreshAuthorityAndMapsExactGrant(t *testing.T) {
	issuer := authority.NewIssuer()
	grants := &grantCommandsFake{}
	provider := &authorityProviderFake{issuer: issuer}
	adapter, err := New(grants, provider)
	if err != nil {
		t.Fatal(err)
	}
	command := validCommand()

	if err := adapter.EnsureGrant(t.Context(), command); err != nil {
		t.Fatalf("EnsureGrant: %v", err)
	}
	if err := adapter.EnsureGrant(t.Context(), command); err != nil {
		t.Fatalf("EnsureGrant replay: %v", err)
	}
	if grants.calls != 2 || provider.calls != 2 {
		t.Fatalf("calls grants=%d authority=%d, want two each", grants.calls, provider.calls)
	}
	if provider.workspace != command.WorkspaceKey ||
		provider.reason != "AgentProvisioning "+command.CommandID {
		t.Fatalf("authority request workspace=%q reason=%q", provider.workspace, provider.reason)
	}
	if grants.command != command {
		t.Fatalf("Connectors command = %#v", grants.command)
	}
}

func TestAdapterMapsConnectorErrorsToProvisioningVocabulary(t *testing.T) {
	tests := []struct {
		name string
		in   error
		want error
	}{
		{name: "invalid", in: agentprovisioning.ErrInvalid, want: agentprovisioning.ErrInvalid},
		{name: "not found", in: agentprovisioning.ErrNotFound, want: agentprovisioning.ErrNotFound},
		{name: "collision", in: agentprovisioning.ErrConflict, want: agentprovisioning.ErrConflict},
		{name: "concurrent write", in: agentprovisioning.ErrConcurrentWrite, want: agentprovisioning.ErrConcurrentWrite},
		{name: "unavailable", in: agentprovisioning.ErrUnavailable, want: agentprovisioning.ErrUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			grants := &grantCommandsFake{err: test.in}
			adapter, err := New(
				grants,
				&authorityProviderFake{issuer: authority.NewIssuer()},
			)
			if err != nil {
				t.Fatal(err)
			}
			err = adapter.EnsureGrant(t.Context(), validCommand())
			if !errors.Is(err, test.want) || !errors.Is(err, test.in) {
				t.Fatalf("EnsureGrant error = %v, want %v and original", err, test.want)
			}
		})
	}
}

func TestAdapterFailsClosedWhenAuthorityCannotBeIssued(t *testing.T) {
	authErr := errors.New("issuer unavailable")
	grants := &grantCommandsFake{}
	provider := &authorityProviderFake{issuer: authority.NewIssuer(), err: authErr}
	adapter, err := New(grants, provider)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.EnsureGrant(t.Context(), validCommand())
	if !errors.Is(err, agentprovisioning.ErrUnavailable) || !errors.Is(err, authErr) {
		t.Fatalf("EnsureGrant error = %v", err)
	}
	if grants.calls != 0 {
		t.Fatalf("grant command calls = %d, want zero", grants.calls)
	}
}

func TestNewRejectsIncompleteComposition(t *testing.T) {
	grants := &grantCommandsFake{}
	provider := &authorityProviderFake{issuer: authority.NewIssuer()}
	tests := []struct {
		name     string
		grants   agentprovisioning.GrantOperations
		provider AuthorityProvider
	}{
		{name: "grants", provider: provider},
		{name: "provider", grants: grants},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.grants, test.provider)
			if !errors.Is(err, agentprovisioning.ErrUnavailable) {
				t.Fatalf("New error = %v, want unavailable", err)
			}
		})
	}
}

func TestAdapterRejectsNonCanonicalCommandIDBeforeAuthority(t *testing.T) {
	for _, commandID := range []string{"", " command-1", "command-1\nforged"} {
		t.Run(commandID, func(t *testing.T) {
			grants := &grantCommandsFake{}
			provider := &authorityProviderFake{issuer: authority.NewIssuer()}
			adapter, err := New(grants, provider)
			if err != nil {
				t.Fatal(err)
			}
			command := validCommand()
			command.CommandID = commandID
			err = adapter.EnsureGrant(t.Context(), command)
			if !errors.Is(err, agentprovisioning.ErrInvalid) {
				t.Fatalf("EnsureGrant error = %v, want invalid", err)
			}
			if provider.calls != 0 || grants.calls != 0 {
				t.Fatalf("calls provider=%d grants=%d, want zero", provider.calls, grants.calls)
			}
		})
	}
}

func validCommand() agentprovisioning.EnsureGrantCommand {
	return agentprovisioning.EnsureGrantCommand{
		CommandID: "provision-1:grant:grant-read", WorkspaceKey: "WS",
		ProvisioningID:           "provision-1",
		ProvisioningGenerationID: "0123456789abcdef0123456789abcdef",
		BindingID:                "binding-docs",
		Grant: agentprovisioning.GrantSpec{
			GrantID: "grant-read", ConnectorID: "github-main",
			Action: "pull_request.read", ResourcePattern: "repo:acme/docs",
		},
	}
}
