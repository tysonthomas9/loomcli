package cmdstore

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestTracedStore_UnwrapReachesFleetDBStacks guards stack store selection
// through the tracing layer: OpenStore wraps the fleet-db client, and without
// Unwrap stackstore.ForStore would silently fall back to the local store.
func TestTracedStore_UnwrapReachesFleetDBStacks(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(stackstore.EnvStackStore, "")

	client, err := fleetdb.New(fleetdb.Config{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("fleetdb.New: %v", err)
	}
	wrapped := WrapStoreWithTracing(client)

	u, ok := wrapped.(stackstore.Unwrapper)
	if !ok {
		t.Fatalf("traced store %T does not implement stackstore.Unwrapper", wrapped)
	}
	if got := u.Unwrap(); got != store.Store(client) {
		t.Fatalf("Unwrap() = %T %p, want the wrapped client %p", got, got, client)
	}
	if _, ok := wrapped.(fleetdb.StackProvider); ok {
		t.Fatal("traced store unexpectedly exposes Stacks() directly; this test relies on Unwrap")
	}

	ss, err := stackstore.ForStore(wrapped)
	if err != nil {
		t.Fatalf("ForStore: %v", err)
	}
	if _, ok := ss.(*stackstore.FleetDBStore); !ok {
		t.Fatalf("ForStore(traced fleet-db) = %T, want *stackstore.FleetDBStore", ss)
	}

	t.Setenv(stackstore.EnvStackStore, "fleetdb")
	if _, err := stackstore.ForStore(wrapped); err != nil {
		t.Fatalf("ForStore with LOOM_STACK_STORE=fleetdb through tracing: %v", err)
	}
}

func TestTracedStore_UnwrapNonFleetDBFallsBackLocal(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(stackstore.EnvStackStore, "")

	inner := memstore.New()
	wrapped := WrapStoreWithTracing(inner)
	if got := wrapped.(stackstore.Unwrapper).Unwrap(); got != store.Store(inner) {
		t.Fatalf("Unwrap() = %T, want the memstore", got)
	}
	ss, err := stackstore.ForStore(wrapped)
	if err != nil {
		t.Fatalf("ForStore: %v", err)
	}
	if _, ok := ss.(*stackstore.LocalStore); !ok {
		t.Fatalf("ForStore(traced memstore) = %T, want *stackstore.LocalStore", ss)
	}
}
