package memstore_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store/storetest"
)

func TestMemstoreRoleCASConformance(t *testing.T) {
	storetest.RunRoleCASConformance(t, func(testing.TB) *storetest.RoleCASHarness {
		return &storetest.RoleCASHarness{Workspace: "WS", Store: memstore.New()}
	})
}
