package owneradapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	moduleconnectors "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type agentProvisioningGrantOperationsFake struct {
	command agentprovisioning.EnsureGrantCommand
	err     error
	calls   int
}

func (fake *agentProvisioningGrantOperationsFake) EnsureGrant(
	_ context.Context,
	command agentprovisioning.EnsureGrantCommand,
) error {
	fake.calls++
	fake.command = command
	return fake.err
}

type agentProvisioningGrantAuthorityFake struct {
	issuer    *authority.Issuer
	workspace string
	reason    string
	err       error
	calls     int
}

func (fake *agentProvisioningGrantAuthorityFake) AuthorityForGrant(
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

func TestAgentProvisioningConnectorsAdapterRequestsFreshAuthorityAndPreservesExactCommand(t *testing.T) {
	grants := &agentProvisioningGrantOperationsFake{}
	provider := &agentProvisioningGrantAuthorityFake{issuer: authority.NewIssuer()}
	adapter, err := NewConnectorsAdapter(grants, provider)
	if err != nil {
		t.Fatal(err)
	}
	command := validAgentProvisioningGrantCommand()

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

func TestAgentProvisioningConnectorsAdapterMapsFailures(t *testing.T) {
	for _, testErr := range []error{
		agentprovisioning.ErrInvalid,
		agentprovisioning.ErrNotFound,
		agentprovisioning.ErrConflict,
		agentprovisioning.ErrConcurrentWrite,
		agentprovisioning.ErrUnavailable,
	} {
		grants := &agentProvisioningGrantOperationsFake{err: testErr}
		adapter, err := NewConnectorsAdapter(
			grants,
			&agentProvisioningGrantAuthorityFake{issuer: authority.NewIssuer()},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = adapter.EnsureGrant(t.Context(), validAgentProvisioningGrantCommand())
		if !errors.Is(err, testErr) {
			t.Fatalf("EnsureGrant error = %v, want %v", err, testErr)
		}
	}
}

func TestAgentProvisioningConnectorsAdapterFailsClosed(t *testing.T) {
	authErr := errors.New("issuer unavailable")
	grants := &agentProvisioningGrantOperationsFake{}
	provider := &agentProvisioningGrantAuthorityFake{
		issuer: authority.NewIssuer(),
		err:    authErr,
	}
	adapter, err := NewConnectorsAdapter(grants, provider)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.EnsureGrant(t.Context(), validAgentProvisioningGrantCommand())
	if !errors.Is(err, agentprovisioning.ErrUnavailable) || !errors.Is(err, authErr) {
		t.Fatalf("EnsureGrant error = %v", err)
	}
	if grants.calls != 0 {
		t.Fatalf("grant command calls = %d, want zero", grants.calls)
	}

	if _, err := NewConnectorsAdapter(
		nil,
		provider,
	); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("nil grants = %v", err)
	}
	if _, err := NewConnectorsAdapter(
		grants,
		nil,
	); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("nil authority provider = %v", err)
	}
}

func TestAgentProvisioningConnectorsAdapterRejectsNonCanonicalCommandIDBeforeAuthority(t *testing.T) {
	for _, commandID := range []string{"", " command-1", "command-1\nforged"} {
		t.Run(commandID, func(t *testing.T) {
			grants := &agentProvisioningGrantOperationsFake{}
			provider := &agentProvisioningGrantAuthorityFake{issuer: authority.NewIssuer()}
			adapter, err := NewConnectorsAdapter(grants, provider)
			if err != nil {
				t.Fatal(err)
			}
			command := validAgentProvisioningGrantCommand()
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

func validAgentProvisioningGrantCommand() agentprovisioning.EnsureGrantCommand {
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
