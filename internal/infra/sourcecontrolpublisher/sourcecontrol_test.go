package stackpublish

import (
	"testing"
	"time"

	stackstore "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolstackstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

func mustStackLifecycle(t *testing.T, store stackstore.Store) sourcecontrol.StackLifecycle {
	t.Helper()
	adapter, err := stackstore.NewAdapter(store)
	if err != nil {
		t.Fatalf("compose stack adapter: %v", err)
	}
	service, err := sourcecontrol.NewStackLifecycle(adapter, time.Now)
	if err != nil {
		t.Fatalf("compose stack lifecycle: %v", err)
	}
	return service
}
