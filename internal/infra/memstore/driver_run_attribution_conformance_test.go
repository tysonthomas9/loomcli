package memstore_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store/storetest"
)

func TestMemstoreDriverRunAttributionConformance(t *testing.T) {
	storetest.RunDriverRunAttributionConformance(t, func(testing.TB) *storetest.DriverRunAttributionHarness {
		return &storetest.DriverRunAttributionHarness{Workspace: "WS", Store: memstore.New()}
	})
}
