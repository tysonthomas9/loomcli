package automationroutes

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestNewRequiresExplicitAwaitResolver(t *testing.T) {
	st := memstore.New()
	withoutResolver := New(Deps{Awaits: st.Awaits(), DriverRuns: st.DriverRuns()})
	if withoutResolver.EventAwaits != nil {
		t.Fatal("EventAwaits composed without an explicit Execution resolver")
	}

	resolver, ok := st.Awaits().(store.AtomicAwaitStore)
	if !ok {
		t.Fatalf("memstore awaits %T does not implement store.AtomicAwaitStore", st.Awaits())
	}
	withResolver := New(Deps{
		Awaits: st.Awaits(), DriverRuns: st.DriverRuns(), AwaitResolver: resolver,
	})
	if withResolver.EventAwaits == nil {
		t.Fatal("EventAwaits not composed with an explicit resolver")
	}
}
