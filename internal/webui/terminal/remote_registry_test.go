package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// TestRemoteUpstreamFactoryForFailsClosed pins the routing rule: a sandbox id
// is unique only within a provider, so an unset or unregistered provider must
// be refused rather than handed to whichever factory happens to be registered.
func TestRemoteUpstreamFactoryForFailsClosed(t *testing.T) {
	if _, err := remoteUpstreamFactoryFor("daytona"); err != nil {
		t.Fatalf("daytona should resolve: %v", err)
	}
	for _, tc := range []struct{ name, provider, wantSubstr string }{
		{"empty", "", "no runtime provider"},
		{"whitespace", "   ", "no runtime provider"},
		{"unregistered exe", "exe", "unsupported remote terminal provider"},
		{"unknown", "fly", "unsupported remote terminal provider"},
		{"case mismatch", "Daytona", "unsupported remote terminal provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			factory, err := remoteUpstreamFactoryFor(tc.provider)
			if err == nil {
				t.Fatalf("provider %q resolved to a factory, want an error", tc.provider)
			}
			if factory != nil {
				t.Fatal("a failed resolution must not return a factory")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestSupportsRemoteProviderAndRemoteProviders(t *testing.T) {
	if !SupportsRemoteProvider(domain.RuntimeProviderDaytona) {
		t.Error("daytona must be attachable")
	}
	for _, kind := range []domain.RuntimeProvider{"", domain.RuntimeProviderExe, domain.RuntimeProviderLocal, "fly"} {
		if SupportsRemoteProvider(kind) {
			t.Errorf("SupportsRemoteProvider(%q) = true, want false", kind)
		}
	}
	got := RemoteProviders()
	if len(got) != 1 || got[0] != domain.RuntimeProviderDaytona {
		t.Fatalf("RemoteProviders() = %v, want [daytona]", got)
	}
}

// TestAttachRemoteSessionRoutesToOwningFactory drives the manager's attach path
// with two registered factories and asserts the launch spec's provider -- not
// ambient configuration -- picks which one runs.
func TestAttachRemoteSessionRoutesToOwningFactory(t *testing.T) {
	const fakeKind = domain.RuntimeProvider("fake-remote")
	var daytonaCalls, fakeCalls int
	var gotSandbox string

	t.Cleanup(RegisterRemoteUpstreamFactory(domain.RuntimeProviderDaytona,
		func(context.Context, string, string) (PTYUpstream, error) {
			daytonaCalls++
			return nil, errors.New("daytona factory should not have run")
		}))
	t.Cleanup(RegisterRemoteUpstreamFactory(fakeKind,
		func(_ context.Context, sandboxID, _ string) (PTYUpstream, error) {
			fakeCalls++
			gotSandbox = sandboxID
			return nil, errors.New("stop here: routing is what is under test")
		}))

	m := &PTYManager{}
	// Both providers can legitimately hold sandbox id "sandbox-1"; only the
	// spec's provider says which platform this one lives on.
	_, err := m.attachRemoteSession(SessionKey{}, 80, 24, &tabmeta.RemoteLaunchSpec{
		Provider:     string(fakeKind),
		SandboxID:    "sandbox-1",
		PTYSessionID: "lead",
	})
	if err == nil {
		t.Fatal("expected the factory's error to surface")
	}
	if fakeCalls != 1 {
		t.Fatalf("owning factory ran %d time(s), want 1", fakeCalls)
	}
	if daytonaCalls != 0 {
		t.Fatalf("non-owning factory ran %d time(s); a sandbox id must never cross providers", daytonaCalls)
	}
	if gotSandbox != "sandbox-1" {
		t.Fatalf("factory received sandbox %q, want sandbox-1", gotSandbox)
	}
}

func TestAttachRemoteSessionRejectsUnknownProvider(t *testing.T) {
	m := &PTYManager{}
	if _, err := m.attachRemoteSession(SessionKey{}, 80, 24, &tabmeta.RemoteLaunchSpec{
		Provider: "exe", SandboxID: "sandbox-1", PTYSessionID: "lead",
	}); err == nil || !strings.Contains(err.Error(), "unsupported remote terminal provider") {
		t.Fatalf("err = %v, want unsupported-provider", err)
	}
	if _, err := m.attachRemoteSession(SessionKey{}, 80, 24, &tabmeta.RemoteLaunchSpec{
		SandboxID: "sandbox-1", PTYSessionID: "lead",
	}); err == nil || !strings.Contains(err.Error(), "no runtime provider") {
		t.Fatalf("err = %v, want missing-provider", err)
	}
}
