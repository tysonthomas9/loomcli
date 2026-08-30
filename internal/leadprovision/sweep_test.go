package leadprovision

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// A sandbox-placed provider must be provisioned, whichever one it is. A
// Daytona equality here silently treats an exe lead as local: never
// provisioned, and nothing reports why.
func TestNeedsSandboxLeadProvisionCoversEverySandboxProvider(t *testing.T) {
	for _, kind := range []domain.RuntimeProvider{domain.RuntimeProviderDaytona, domain.RuntimeProviderExe} {
		target := provisionTarget{
			agent: &domain.Agent{Name: "nova", RoleName: "lead", RuntimeProvider: kind},
			role:  &domain.Role{Name: "lead", Kind: domain.RoleKindInteractive},
		}
		if !target.needsSandboxLeadProvision() {
			t.Errorf("provider %q: needsSandboxLeadProvision() = false, want true", kind)
		}
	}
	local := provisionTarget{
		agent: &domain.Agent{Name: "nova", RoleName: "lead", RuntimeProvider: domain.RuntimeProviderLocal},
		role:  &domain.Role{Name: "lead", Kind: domain.RoleKindInteractive},
	}
	if local.needsSandboxLeadProvision() {
		t.Error("a local lead must not be sandbox-provisioned")
	}
}
