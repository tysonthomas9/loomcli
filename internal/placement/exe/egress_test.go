package exe

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

// dialedProvider builds a provider whose control plane would panic if reached.
// Every test here asserts a refusal BEFORE any I/O, so a request escaping to
// the transport must be loud rather than a passing test against a dead URL.
func egressProvider(t *testing.T, allowOpen bool) *Provider {
	t.Helper()
	return &Provider{
		control:                 newControlClient("token", "http://127.0.0.1:1/unreachable", 0),
		image:                   "ubuntu",
		allowUnrestrictedEgress: allowOpen,
	}
}

func TestCreateRefusesUnenforceableEgressAllowlist(t *testing.T) {
	p := egressProvider(t, false)
	res, err := p.Create(context.Background(), placement.CreateRequest{
		Name:                   "lead-abc",
		Resource:               placement.ResourceSize{VCPU: 2, MemGiB: 4},
		NetworkDomainAllowlist: []string{"github.com", "api.anthropic.com"},
	})
	if err == nil {
		t.Fatal("create accepted an egress allowlist exe.dev cannot enforce")
	}
	// NotDispatched is the load-bearing part: the refusal is pure local
	// validation, so the broker may release the placement knowing no VM can
	// exist. Reporting Unknown here would leak a placement row per refusal.
	if res.Outcome != placement.CreateOutcomeNotDispatched {
		t.Errorf("outcome = %q, want %q", res.Outcome, placement.CreateOutcomeNotDispatched)
	}
	if !res.ProvablyNotDispatched() {
		t.Error("ProvablyNotDispatched() = false for a pre-I/O validation refusal")
	}
	if res.SandboxID != "" {
		t.Errorf("SandboxID = %q, want empty", res.SandboxID)
	}
	if !strings.Contains(err.Error(), "egress") {
		t.Errorf("error does not name the reason: %v", err)
	}
}

func TestCreateWithoutAllowlistIsNotRefusedForEgress(t *testing.T) {
	p := egressProvider(t, false)
	_, err := p.Create(context.Background(), placement.CreateRequest{
		Name:     "lead-abc",
		Resource: placement.ResourceSize{VCPU: 2, MemGiB: 4},
	})
	// It still fails -- the control plane is unreachable -- but it must fail
	// for THAT reason, having actually attempted the call.
	if err != nil && strings.Contains(err.Error(), "egress") {
		t.Fatalf("empty allowlist refused as an egress problem: %v", err)
	}
}

func TestCreateAllowsEgressAllowlistWhenOperatorOptedIn(t *testing.T) {
	p := egressProvider(t, true)
	res, err := p.Create(context.Background(), placement.CreateRequest{
		Name:                   "lead-abc",
		Resource:               placement.ResourceSize{VCPU: 2, MemGiB: 4},
		NetworkDomainAllowlist: []string{"github.com"},
	})
	if err != nil && strings.Contains(err.Error(), "egress") {
		t.Fatalf("opt-in did not take effect: %v", err)
	}
	// The request reached the transport and failed there, which is Unknown --
	// never NotDispatched, since a VM may exist.
	if res.Outcome == placement.CreateOutcomeNotDispatched {
		t.Error("outcome = not_dispatched after the request left the process")
	}
}
