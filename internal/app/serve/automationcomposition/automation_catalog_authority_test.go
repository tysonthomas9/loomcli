package automationcomposition

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestAutomationCatalogAuthorityProviderIssuesOnlyEffectiveVersionAuthority(t *testing.T) {
	provider := newAutomationCatalogAuthorityProvider(
		testEffectiveVersionAuthority(authority.NewIssuer()),
	)
	value, err := provider.AuthorityForEffectiveVersion(context.Background(), "TEST", "automation admission target")
	if err != nil {
		t.Fatalf("AuthorityForEffectiveVersion: %v", err)
	}
	if value.Workspace() != "TEST" || value.Action() != workflowcatalog.ActionResolveEffectiveVersion || value.Subject() != "automation" || value.Reason() != "automation admission target" {
		t.Fatalf("authority = workspace:%q action:%q subject:%q reason:%q", value.Workspace(), value.Action(), value.Subject(), value.Reason())
	}
}

func TestAutomationCatalogAuthorityProviderFailsClosed(t *testing.T) {
	if got := newAutomationCatalogAuthorityProvider(nil); got != nil {
		t.Fatalf("nil catalog provider = %#v, want nil", got)
	}
	provider := &automationCatalogAuthorityProvider{}
	if _, err := provider.AuthorityForEffectiveVersion(context.Background(), "TEST", "reason"); !errors.Is(err, automation.ErrUnavailable) {
		t.Fatalf("nil capability error = %v, want %v", err, automation.ErrUnavailable)
	}

	provider = &automationCatalogAuthorityProvider{
		issue: testEffectiveVersionAuthority(authority.NewIssuer()),
	}
	for _, test := range []struct {
		workspace string
		reason    string
	}{
		{workspace: "", reason: "reason"},
		{workspace: "TEST", reason: ""},
	} {
		if _, err := provider.AuthorityForEffectiveVersion(context.Background(), test.workspace, test.reason); !errors.Is(err, authority.ErrInvalidScope) {
			t.Fatalf("scope (%q,%q) error = %v, want %v", test.workspace, test.reason, err, authority.ErrInvalidScope)
		}
	}
}
