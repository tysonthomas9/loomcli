package outbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/loomgittest"
	"github.com/tysonthomas9/loomcli/internal/loomgit/outbox"
)

type interruptAfterSend struct {
	*loomgittest.Store
	fail bool
}

func (store *interruptAfterSend) MarkDelivered(ctx context.Context, id int64) error {
	if store.fail {
		store.fail = false
		return errors.New("process stopped before delivery receipt")
	}
	return store.Store.MarkDelivered(ctx, id)
}

func TestDispatchRetriesFailureAndDeliversOnce(t *testing.T) {
	ctx := context.Background()
	store := loomgittest.NewStore()
	entry, _, err := store.Begin(ctx, "apply-1", "apply")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(ctx, entry, "done", nil, []loomgit.OutboxEvent{{Kind: "git.integrated", Payload: []byte(`{"change_id":"C"}`)}}); err != nil {
		t.Fatal(err)
	}
	var calls int
	failed := outbox.EmitFunc(func(context.Context, loomgit.OutboxEvent) error {
		calls++
		return errors.New("sink unavailable")
	})
	if err := outbox.Dispatch(ctx, store, failed); err == nil {
		t.Fatal("failed sink accepted event")
	}
	if pending, err := store.PendingEvents(ctx); err != nil || len(pending) != 1 {
		t.Fatalf("event was lost: %+v, %v", pending, err)
	}
	sent := outbox.EmitFunc(func(_ context.Context, event loomgit.OutboxEvent) error {
		calls++
		if event.ID == 0 || event.Kind != "git.integrated" {
			t.Errorf("event = %+v", event)
		}
		return nil
	})
	if err := outbox.Dispatch(ctx, store, sent); err != nil {
		t.Fatal(err)
	}
	if err := outbox.Dispatch(ctx, store, sent); err != nil || calls != 2 {
		t.Fatalf("replayed delivered event: calls=%d err=%v", calls, err)
	}
}

func TestDispatchResendsStableIDAfterSendBeforeReceipt(t *testing.T) {
	ctx := context.Background()
	store := &interruptAfterSend{Store: loomgittest.NewStore(), fail: true}
	entry, _, err := store.Begin(ctx, "apply-1", "apply")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(ctx, entry, "done", nil, []loomgit.OutboxEvent{{Kind: "git.integrated"}}); err != nil {
		t.Fatal(err)
	}
	var seen []int64
	visible := make(map[int64]bool)
	sink := outbox.EmitFunc(func(_ context.Context, event loomgit.OutboxEvent) error {
		seen = append(seen, event.ID)
		visible[event.ID] = true
		return nil
	})
	if err := outbox.Dispatch(ctx, store, sink); err == nil {
		t.Fatal("missing interrupted receipt failure")
	}
	if err := outbox.Dispatch(ctx, store, sink); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != seen[1] || len(visible) != 1 {
		t.Fatalf("unstable replay: deliveries=%v visible=%v", seen, visible)
	}
}
