package terminal

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// RemoteUpstreamFactory opens a PTY upstream against a sandbox owned by one
// runtime provider.
type RemoteUpstreamFactory func(ctx context.Context, sandboxID, ptySessionID string) (PTYUpstream, error)

// remoteUpstreamFactories maps a runtime provider onto the factory that can
// attach to ITS sandboxes.
//
// The attach path used to switch on a literal "daytona" and the launch spec
// stamped that literal regardless of which provider actually held the sandbox.
// A sandbox id is unique only within a provider, so pairing an id with the
// wrong factory hands it to the wrong platform's SDK -- at best a confusing
// not-found, at worst attaching to an unrelated sandbox that shares the id.
// remoteUpstreamMu guards the registry. Registration happens during wiring
// while attach happens on request goroutines, so the map is not read-only.
var remoteUpstreamMu sync.RWMutex

var remoteUpstreamFactories = map[domain.RuntimeProvider]RemoteUpstreamFactory{
	domain.RuntimeProviderDaytona: func(ctx context.Context, sandboxID, ptySessionID string) (PTYUpstream, error) {
		return newDaytonaPTYUpstreamForManager(ctx, sandboxID, ptySessionID, DaytonaPTYConfig{})
	},
}

// RegisterRemoteUpstreamFactory wires a runtime provider's attach path. This
// is how a provider becomes attachable: one registration, rather than a new
// case in a switch plus a matching equality check wherever leads are filtered.
// Returns a function that removes the registration again.
func RegisterRemoteUpstreamFactory(kind domain.RuntimeProvider, factory RemoteUpstreamFactory) func() {
	remoteUpstreamMu.Lock()
	defer remoteUpstreamMu.Unlock()
	previous, had := remoteUpstreamFactories[kind]
	remoteUpstreamFactories[kind] = factory
	return func() {
		remoteUpstreamMu.Lock()
		defer remoteUpstreamMu.Unlock()
		if had {
			remoteUpstreamFactories[kind] = previous
			return
		}
		delete(remoteUpstreamFactories, kind)
	}
}

// remoteUpstreamFactoryFor resolves the factory owning a sandbox, FAIL-CLOSED.
// An unset or unregistered provider is an error, never a fallback to the only
// registered factory.
func remoteUpstreamFactoryFor(provider string) (RemoteUpstreamFactory, error) {
	kind := domain.RuntimeProvider(strings.TrimSpace(provider))
	if kind == "" {
		return nil, fmt.Errorf("remote terminal launch spec has no runtime provider")
	}
	remoteUpstreamMu.RLock()
	defer remoteUpstreamMu.RUnlock()
	factory, ok := remoteUpstreamFactories[kind]
	if !ok || factory == nil {
		return nil, fmt.Errorf("unsupported remote terminal provider %q", provider)
	}
	return factory, nil
}

// SupportsRemoteProvider reports whether a runtime provider's sandboxes can be
// attached to. Callers gate on this instead of hardcoding a provider name, so
// registering a provider enables its attach path in one place.
func SupportsRemoteProvider(kind domain.RuntimeProvider) bool {
	_, err := remoteUpstreamFactoryFor(string(kind))
	return err == nil
}

// RemoteProviders lists the attachable runtime providers, sorted so callers
// that iterate it behave deterministically.
func RemoteProviders() []domain.RuntimeProvider {
	remoteUpstreamMu.RLock()
	defer remoteUpstreamMu.RUnlock()
	kinds := make([]domain.RuntimeProvider, 0, len(remoteUpstreamFactories))
	for kind, factory := range remoteUpstreamFactories {
		if factory != nil {
			kinds = append(kinds, kind)
		}
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}
