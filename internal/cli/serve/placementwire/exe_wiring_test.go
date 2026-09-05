package placementwire

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/placement"
	"github.com/tysonthomas9/loomcli/internal/placement/daytona"
	webuiterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// writeTestSSHKey writes an unencrypted ed25519 key, which is what the exe
// provider requires (it refuses encrypted keys, since serve has nobody to
// prompt for a passphrase).
func writeTestSSHKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}

func clearExeEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		envLoomExeToken, envLoomExeSSHKey, envLoomExeHostKeys,
		envLoomExeImage, envLoomExeControlURL, envLoomExeAllowOpenEgress,
	} {
		t.Setenv(key, "")
	}
}

// stubPlacementProvider is a do-nothing placement.Provider. These tests only
// care about registry SHAPE -- which kinds map to a non-nil adapter -- so no
// method is ever called.
type stubPlacementProvider struct{ placement.Provider }

// TestBuildExeProviderUnconfiguredIsNil pins the default: a deployment that has
// not configured exe gets no exe provider, and therefore no exe placements.
func TestBuildExeProviderUnconfiguredIsNil(t *testing.T) {
	clearExeEnv(t)
	if p := buildExeProvider(); p != nil {
		t.Fatalf("buildExeProvider() = %v, want nil when unconfigured", p)
	}
}

// TestBuildExeProviderPartialConfigIsNil pins fail-closed on half-wiring.
// Returning a provider here would register it, and the failure would surface at
// provision time -- after a placement row exists and a caller is waiting.
func TestBuildExeProviderPartialConfigIsNil(t *testing.T) {
	keyPath := writeTestSSHKey(t)
	cases := map[string]map[string]string{
		"token only":            {envLoomExeToken: "t"},
		"token and key, no pin": {envLoomExeToken: "t", envLoomExeSSHKey: keyPath},
		"no token": {
			envLoomExeSSHKey:   keyPath,
			envLoomExeHostKeys: filepath.Join(t.TempDir(), "known"),
		},
		"key path does not exist": {
			envLoomExeToken:    "t",
			envLoomExeSSHKey:   filepath.Join(t.TempDir(), "absent"),
			envLoomExeHostKeys: filepath.Join(t.TempDir(), "known"),
		},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			clearExeEnv(t)
			for k, v := range env {
				t.Setenv(k, v)
			}
			if p := buildExeProvider(); p != nil {
				t.Fatalf("buildExeProvider() = %v, want nil for partial config", p)
			}
		})
	}
}

func configureExe(t *testing.T) {
	t.Helper()
	clearExeEnv(t)
	t.Setenv(envLoomExeToken, "test-token")
	t.Setenv(envLoomExeSSHKey, writeTestSSHKey(t))
	t.Setenv(envLoomExeHostKeys, filepath.Join(t.TempDir(), "known_hosts"))
}

func TestBuildExeProviderFullyConfigured(t *testing.T) {
	configureExe(t)
	p := buildExeProvider()
	if p == nil {
		t.Fatal("buildExeProvider() = nil for a complete configuration")
	}
	// exe.dev cannot stop or start a VM, so the broker must never try to park
	// one. A provider that claimed parking would have every release attempt
	// fail against an API that has no such call.
	if p.SupportsParking() {
		t.Error("SupportsParking() = true, but exe.dev has no stop/start")
	}
}

// TestBuildSupportsExeWithoutDaytona proves the provider registry is the
// construction seam, not a Daytona-owned feature with exe nested beneath it.
// An operator who configures only exe.dev must get a usable placement broker.
func TestBuildSupportsExeWithoutDaytona(t *testing.T) {
	configureExe(t)
	t.Setenv(daytona.APIKeyEnv, "")
	t.Setenv("LOOM_DEPLOYMENT_ID", "test-deployment")

	broker, providers := Build(memstore.New(), []byte("0123456789abcdef0123456789abcdef"))
	if broker == nil {
		t.Fatal("Build() broker = nil for complete exe-only configuration")
	}
	if providers[domain.RuntimeProviderExe] == nil {
		t.Fatalf("Build() providers = %#v, want registered exe provider", providers)
	}
	if _, ok := providers[domain.RuntimeProviderDaytona]; ok {
		t.Fatalf("Build() providers = %#v, Daytona registered without credentials", providers)
	}
}

func TestBuildFailureDoesNotRegisterExeTerminalAttach(t *testing.T) {
	configureExe(t)
	t.Setenv(daytona.APIKeyEnv, "")
	t.Setenv("LOOM_DEPLOYMENT_ID", "")
	restoreExisting := webuiterminal.RegisterRemoteUpstreamFactory(domain.RuntimeProviderExe, nil)
	t.Cleanup(restoreExisting)

	broker, providers := Build(memstore.New(), []byte("0123456789abcdef0123456789abcdef"))
	if broker != nil || providers != nil {
		t.Fatalf("Build() = (%#v, %#v), want disabled broker for missing deployment id", broker, providers)
	}
	if webuiterminal.SupportsRemoteProvider(domain.RuntimeProviderExe) {
		t.Fatal("failed Build left exe terminal attach registered")
	}
}

// TestRegisterExeTerminalAttachMakesExeAttachable is the check that "registered
// as a provider" and "openable in the UI" cannot drift apart. Before the
// registry existed the attach path switched on a literal "daytona", so a second
// provider provisioned fine and then could not be opened.
func TestRegisterExeTerminalAttachMakesExeAttachable(t *testing.T) {
	// Establish the precondition locally rather than depending on no earlier
	// test having exercised Build, which registers configured providers.
	restoreExisting := webuiterminal.RegisterRemoteUpstreamFactory(domain.RuntimeProviderExe, nil)
	t.Cleanup(restoreExisting)
	if webuiterminal.SupportsRemoteProvider(domain.RuntimeProviderExe) {
		t.Fatal("exe was already attachable before registration; test cannot prove anything")
	}
	configureExe(t)
	provider := buildExeProvider()
	if provider == nil {
		t.Fatal("buildExeProvider() = nil")
	}
	unregister := webuiterminal.RegisterRemoteUpstreamFactory(domain.RuntimeProviderExe, exeUpstreamFactory(provider))
	defer unregister()

	if !webuiterminal.SupportsRemoteProvider(domain.RuntimeProviderExe) {
		t.Error("exe is not attachable after registering its factory")
	}
	if !webuiterminal.SupportsRemoteProvider(domain.RuntimeProviderDaytona) {
		t.Error("registering exe unregistered daytona")
	}
}

// TestExeFactoryReturnsAUsableNilOnError pins the typed-nil trap. AttachPTY
// returns a concrete *exe.PTYAttachment; handing that back directly on the
// error path produces a NON-nil interface wrapping a nil pointer, and every
// "if upstream != nil" guard downstream passes before dereferencing it.
func TestExeFactoryReturnsAUsableNilOnError(t *testing.T) {
	configureExe(t)
	provider := buildExeProvider()
	if provider == nil {
		t.Fatal("buildExeProvider() = nil")
	}
	// An identifier the allowlist rejects fails before any dial, so this
	// exercises the error path without touching the network.
	upstream, err := exeUpstreamFactory(provider)(context.Background(), "not a valid vm name", "lead")
	if err == nil {
		t.Fatal("factory accepted an invalid sandbox id")
	}
	if upstream != nil {
		t.Errorf("factory returned a non-nil upstream alongside an error: %#v", upstream)
	}
}

// TestReviveRegistryMirrorsPlacementRegistry pins that a provider cannot be
// provisionable but not revivable. Written out separately, the two registries
// drift, and the symptom is leads that survive a serve restart as permanently
// unattachable -- which reads as a lead bug, not a missing wiring line.
func TestReviveRegistryMirrorsPlacementRegistry(t *testing.T) {
	configureExe(t)
	exeProvider := buildExeProvider()
	if exeProvider == nil {
		t.Fatal("buildExeProvider() = nil")
	}
	placementRegistry := placement.ProviderRegistry{
		domain.RuntimeProviderDaytona: stubPlacementProvider{},
		domain.RuntimeProviderExe:     exeProvider,
	}
	revive := reviveRegistryFor(placementRegistry)
	for kind := range placementRegistry {
		if revive[kind] == nil {
			t.Errorf("provider %q can provision but cannot be revived", kind)
		}
	}
	if len(revive) != len(placementRegistry) {
		t.Errorf("revive registry has %d entries, placement registry has %d", len(revive), len(placementRegistry))
	}
}

// TestReviveRegistryDropsNilEntries pins that a nil adapter never becomes a
// revive entry -- calling one panics inside the request goroutine.
func TestReviveRegistryDropsNilEntries(t *testing.T) {
	revive := reviveRegistryFor(placement.ProviderRegistry{
		domain.RuntimeProviderDaytona: stubPlacementProvider{},
		domain.RuntimeProviderExe:     nil,
	})
	if _, ok := revive[domain.RuntimeProviderExe]; ok {
		t.Error("a nil provider became a revive registry entry")
	}
	if len(revive) != 1 {
		t.Errorf("revive registry = %d entries, want 1", len(revive))
	}
}

func TestRegisteredProviderNamesIsSorted(t *testing.T) {
	names := registeredProviderNames(placement.ProviderRegistry{
		domain.RuntimeProviderExe:     stubPlacementProvider{},
		domain.RuntimeProviderDaytona: stubPlacementProvider{},
	})
	want := []string{"daytona", "exe"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
}
