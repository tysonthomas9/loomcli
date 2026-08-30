package placement

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// ProviderRegistry maps a runtime provider onto the adapter that owns its
// sandboxes.
//
// It replaces the broker's single Provider field. A sandbox id is only unique
// WITHIN a provider, so once more than one provider is registered, every
// provider call must be routed by the owning node's RuntimeProvider. Routing a
// call to the wrong adapter would, at best, fail; at worst it would act on an
// unrelated sandbox that happens to share an id.
type ProviderRegistry map[domain.RuntimeProvider]Provider

// providerHandle binds an adapter to the runtime provider that owns it.
//
// Passing a bare Provider was not enough: the compiler forced callers to supply
// SOME adapter, but nothing stopped them pairing provider A's adapter with
// provider B's sandbox id -- and a sandbox id is unique only within a provider.
// Carrying the kind alongside the adapter lets destructive paths (orphan
// reaping especially) verify ownership instead of assuming it.
type providerHandle struct {
	kind    domain.RuntimeProvider
	adapter Provider
}

// clone returns an independent copy so post-construction mutation of the
// caller's map cannot change routing.
func (r ProviderRegistry) clone() ProviderRegistry {
	out := make(ProviderRegistry, len(r))
	for kind, p := range r {
		out[kind] = p
	}
	return out
}

// validateProviderRegistry rejects a registry that could route silently wrong:
// an empty registry, an entry under the empty runtime provider, or a nil
// adapter. Each would surface later as a confusing nil dereference or a
// fail-open resolution rather than a clear construction error.
func validateProviderRegistry(reg ProviderRegistry) error {
	if len(reg) == 0 {
		return fmt.Errorf("at least one sandbox provider required: %w", domain.ErrInvalid)
	}
	for kind, p := range reg {
		if kind == "" {
			return fmt.Errorf("sandbox provider registered under empty runtime provider: %w", domain.ErrInvalid)
		}
		if isNilProvider(p) {
			return fmt.Errorf("nil sandbox provider registered for %q: %w", kind, domain.ErrInvalid)
		}
	}
	return nil
}

// providerFor resolves the adapter for a runtime provider, FAIL-CLOSED.
//
// An unregistered provider is an error, never a fallback to the "default" or
// "only" provider. Silently falling back is how a placement gets created on one
// provider and released against another -- which severs the record of a live,
// billing sandbox. The zero value ("") is equally an error: an unstamped node
// must not be silently treated as Daytona.
func (b *Broker) providerFor(kind domain.RuntimeProvider) (providerHandle, error) {
	if kind == "" {
		return providerHandle{}, fmt.Errorf("runtime provider not set on placement: %w", domain.ErrInvalid)
	}
	p, ok := b.providers[kind]
	if !ok || isNilProvider(p) {
		return providerHandle{}, fmt.Errorf("no sandbox provider registered for runtime provider %q: %w", kind, domain.ErrInvalid)
	}
	return providerHandle{kind: kind, adapter: p}, nil
}

// isNilProvider catches a typed nil pointer stored in a non-nil interface --
// `p == nil` is false for those, so it would pass construction and panic on
// first use.
func isNilProvider(p Provider) bool {
	if p == nil {
		return true
	}
	v := reflect.ValueOf(p)
	switch v.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}

// providerForNode resolves the adapter that owns a node's sandbox. Every
// provider call reachable from a placement record must go through this, so the
// node's stamped provider -- not ambient configuration -- decides the routing.
func (b *Broker) providerForNode(node *domain.Node) (providerHandle, error) {
	if node == nil {
		return providerHandle{}, fmt.Errorf("node required to resolve sandbox provider: %w", domain.ErrInvalid)
	}
	p, err := b.providerFor(node.RuntimeProvider)
	if err != nil {
		return providerHandle{}, fmt.Errorf("placement %q: %w", node.NodeID, err)
	}
	return p, nil
}

// countsTowardQuota is the SINGLE predicate deciding whether a placement
// consumes the live budget. Admission (accountReserved) and reporting
// (List.LiveReserved) must agree: while they disagreed -- admission hardcoded
// Daytona, listing counted any registered provider -- a second provider's
// reservations would have been reported but not charged, and its requests
// checked against another provider's consumption.
//
// This is still ONE GLOBAL budget. Making it provider-keyed so one provider
// cannot consume another's quota is a separate change, and must land together
// with MaxLive and LiveReserved provider-keying before a second provider is
// registered in production.
func (b *Broker) countsTowardQuota(node *domain.Node) bool {
	if node == nil || node.Placement == nil {
		return false
	}
	_, err := b.providerFor(node.RuntimeProvider)
	return err == nil
}

// registeredProviders lists the runtime providers this broker can act on, so
// account-wide sweeps (orphan reaping) can iterate every provider instead of
// silently covering only one.
func (b *Broker) registeredProviders() []domain.RuntimeProvider {
	kinds := make([]domain.RuntimeProvider, 0, len(b.providers))
	for kind, p := range b.providers {
		if !isNilProvider(p) {
			kinds = append(kinds, kind)
		}
	}
	// Sorted: map order is random, and an orphan sweep that visits providers
	// in a different order each run makes reaper behavior untestable.
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}
