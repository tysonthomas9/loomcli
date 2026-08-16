package agentmodules

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func TestNewRequiresExplicitAwaitResolver(t *testing.T) {
	st := memstore.New()
	withoutResolver := newAutomationRouteModules(automationRouteDeps{Awaits: st.Awaits(), DriverRuns: st.DriverRuns()})
	if withoutResolver.EventAwaits != nil {
		t.Fatal("EventAwaits composed without an explicit Execution resolver")
	}

	resolver, ok := st.Awaits().(execution.AtomicAwaitStore)
	if !ok {
		t.Fatalf("memstore awaits %T does not implement execution.AtomicAwaitStore", st.Awaits())
	}
	withResolver := newAutomationRouteModules(automationRouteDeps{
		Awaits: st.Awaits(), DriverRuns: st.DriverRuns(), AwaitResolver: resolver,
	})
	if withResolver.EventAwaits == nil {
		t.Fatal("EventAwaits not composed with an explicit resolver")
	}
}
