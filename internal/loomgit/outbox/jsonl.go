package outbox

import (
	"context"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

type JSONLStore interface {
	Store
	PendingJSONLEvents(context.Context) ([]loomgit.OutboxEvent, error)
	MarkJSONLEmitted(context.Context, int64) error
}

func EmitJSONL(ctx context.Context, store JSONLStore, bus *events.Bus, event loomgit.OutboxEvent) error {
	if event.JSONLEmitted {
		return nil
	}
	if err := bus.Emit(events.Event{Type: events.EventType(event.Kind),
		EventID: "loomgit:" + strconv.FormatInt(event.ID, 10), Data: event.Payload}); err != nil {
		return err
	}
	if err := bus.Flush(); err != nil {
		return err
	}
	return store.MarkJSONLEmitted(ctx, event.ID)
}

// EmitPending writes recovery events to JSONL without acknowledging outbox rows.
// The UI dispatcher still owns SSE delivery and the final acknowledgement.
func EmitPending(ctx context.Context, store JSONLStore, bus *events.Bus) error {
	pending, err := store.PendingJSONLEvents(ctx)
	if err != nil {
		return err
	}
	for _, event := range pending {
		if err := EmitJSONL(ctx, store, bus, event); err != nil {
			return err
		}
	}
	return nil
}
