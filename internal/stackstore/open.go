package stackstore

import (
	"fmt"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// EnvStackStore overrides stack store selection. "local" forces the
// machine-local LocalStore (offline use); "fleetdb" requires fleet-db; empty
// selects fleet-db whenever the loom store reaches it.
const EnvStackStore = "LOOM_STACK_STORE"

// Unwrapper is implemented by store.Store decorators (e.g. tracing) so ForStore
// can reach the fleet-db client underneath.
type Unwrapper interface {
	Unwrap() store.Store
}

// ForStore returns the stack store to use alongside the loom store s:
//
//   - LOOM_STACK_STORE=local → LocalStore (explicit local/offline fallback);
//   - s (or a store it decorates) reaches fleet-db → FleetDBStore, the
//     canonical stack state in a FleetDB workspace;
//   - otherwise (no store, or an in-memory one) → LocalStore, unless
//     LOOM_STACK_STORE=fleetdb demands fleet-db, which is an error.
func ForStore(s store.Store) (Store, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(EnvStackStore)))
	switch mode {
	case "", "fleetdb", "local":
	default:
		return nil, fmt.Errorf("stackstore: %s=%q is invalid (want \"local\" or \"fleetdb\")", EnvStackStore, mode)
	}
	if mode == "local" {
		return Default()
	}
	if p := stackProvider(s); p != nil {
		return NewFleetDB(p.Stacks()), nil
	}
	if mode == "fleetdb" {
		return nil, fmt.Errorf("stackstore: %s=fleetdb but the loom store is not backed by fleet-db", EnvStackStore)
	}
	return Default()
}

// stackProvider finds the fleet-db stack API under any decorators of s.
func stackProvider(s store.Store) fleetdb.StackProvider {
	for depth := 0; s != nil && depth < 8; depth++ {
		if p, ok := s.(fleetdb.StackProvider); ok {
			return p
		}
		u, ok := s.(Unwrapper)
		if !ok {
			return nil
		}
		s = u.Unwrap()
	}
	return nil
}

// LocalRequested reports whether LOOM_STACK_STORE=local forces LocalStore, so
// callers can skip opening a fleet-db handle they would not use.
func LocalRequested() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(EnvStackStore)), "local")
}
