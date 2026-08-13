package stackpublish

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

func mustStackLifecycle(t *testing.T, store sourcecontrol.StackLifecycleStore) StackLifecycle {
	t.Helper()
	service, err := sourcecontrol.NewStackLifecycle(store, time.Now)
	if err != nil {
		t.Fatalf("compose stack lifecycle: %v", err)
	}
	return service
}
