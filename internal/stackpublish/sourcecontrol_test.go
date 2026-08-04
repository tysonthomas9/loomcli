package stackpublish

import (
	"testing"
	"time"

	stackstoreadapter "github.com/tysonthomas9/loomcli/internal/infra/stackstoreadapter"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

func mustStackLifecycle(t *testing.T, store stackstore.Store) sourcecontrol.StackLifecycle {
	t.Helper()
	adapter, err := stackstoreadapter.New(store)
	if err != nil {
		t.Fatalf("compose stack adapter: %v", err)
	}
	service, err := sourcecontrol.NewStackLifecycle(adapter, time.Now)
	if err != nil {
		t.Fatalf("compose stack lifecycle: %v", err)
	}
	return service
}
