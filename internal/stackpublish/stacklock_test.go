package stackpublish

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/stacklock"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

func TestAllReconcilerEntriesShareStackLock(t *testing.T) {
	ctx := context.Background()
	const ws = "lock-entry-test"
	id := sl.StackID("epic:lock-entry-test")
	r := &Reconciler{}
	err := stacklock.With(ctx, ws, string(id), func(context.Context) error {
		entries := []struct {
			name string
			run  func() error
		}{
			{"publish", func() error { _, err := r.Publish(ctx, ws, id, "", Options{}); return err }},
			{"origin", func() error { _, err := r.PublishFromOrigin(ctx, ws, id, "", "", Options{}); return err }},
			{"restack", func() error { _, err := r.Restack(ctx, ws, id, "", nil); return err }},
		}
		for _, entry := range entries {
			if err := entry.run(); !errors.Is(err, loomgit.NewError(loomgit.StackLocked, "", nil)) {
				t.Errorf("%s: error = %v, want stack_locked", entry.name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
