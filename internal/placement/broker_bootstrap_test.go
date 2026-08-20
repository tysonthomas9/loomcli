package placement

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func newBootstrapBroker(t *testing.T, provider *fakeProvider, enabled bool, baseURL string) *Broker {
	t.Helper()
	broker, err := NewBroker(Config{
		Store:                memstore.New(),
		Provider:             provider,
		TokenKey:             testTokenKey,
		DeploymentID:         testDeploymentID,
		LeadAPIBaseURL:       baseURL,
		LeadBootstrapEnabled: enabled,
	})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	return broker
}

func TestBrokerLeadBootstrapBinarySpec(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		baseURL string
		wantURL string // "" means the spec must be nil
	}{
		{name: "enabled with base url", enabled: true, baseURL: "https://serve.example.com", wantURL: "https://serve.example.com/api/lead/bootstrap/loom"},
		{name: "enabled trims trailing slash", enabled: true, baseURL: "https://serve.example.com/", wantURL: "https://serve.example.com/api/lead/bootstrap/loom"},
		{name: "enabled but no base url disables", enabled: true, baseURL: "", wantURL: ""},
		{name: "disabled with base url", enabled: false, baseURL: "https://serve.example.com", wantURL: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := newBootstrapBroker(t, &fakeProvider{}, tc.enabled, tc.baseURL).leadBootstrapBinarySpec()
			if tc.wantURL == "" {
				if spec != nil {
					t.Fatalf("spec = %#v, want nil", spec)
				}
				return
			}
			if spec == nil {
				t.Fatal("spec = nil, want populated bootstrap spec")
			}
			if spec.URL != tc.wantURL {
				t.Fatalf("URL = %q, want %q", spec.URL, tc.wantURL)
			}
			if spec.Dest != "/usr/local/bin/loom" || spec.Mode != "0755" {
				t.Fatalf("dest/mode = %q/%q, want /usr/local/bin/loom and 0755", spec.Dest, spec.Mode)
			}
		})
	}
}

// Enabling bootstrap alone must make prep run and carry the spec, even with no
// repo/prompt/seed work -- the download is prep work by itself.
func TestProvisionBootstrapBinaryReachesPrep(t *testing.T) {
	ctx := context.Background()
	provider := &fakeProvider{}
	broker := newBootstrapBroker(t, provider, true, "https://serve.example.com")

	if _, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4)); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got := provider.prepCallCount(); got != 1 {
		t.Fatalf("prep calls = %d, want 1 for bootstrap-only prep", got)
	}
	spec := provider.prepCall(t, 0).BootstrapBinary
	if spec == nil {
		t.Fatal("prep BootstrapBinary = nil, want the bootstrap spec")
	}
	if spec.URL != "https://serve.example.com/api/lead/bootstrap/loom" || spec.Dest != "/usr/local/bin/loom" || spec.Mode != "0755" {
		t.Fatalf("prep BootstrapBinary = %#v, want served-binary install", spec)
	}
}

// The no-config path must leave prep untouched: without repo/prompt/seed work a
// bootstrap-disabled broker runs no prep at all (byte-identical to today).
func TestProvisionWithoutBootstrapLeavesPrepNil(t *testing.T) {
	ctx := context.Background()
	provider := &fakeProvider{}
	broker := newBootstrapBroker(t, provider, false, "https://serve.example.com")

	if _, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4)); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got := provider.prepCallCount(); got != 0 {
		t.Fatalf("prep calls = %d, want 0 when bootstrap disabled and no other prep work", got)
	}
}
