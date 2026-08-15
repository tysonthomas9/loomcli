package memstore_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store/storetest"
)

func TestTriggerBindingDeleteConformance(t *testing.T) {
	storetest.RunTriggerBindingDeleteConformance(t, func(t testing.TB) *storetest.TriggerBindingDeleteHarness {
		return &storetest.TriggerBindingDeleteHarness{Workspace: "WS", Store: memstore.New()}
	})
}
