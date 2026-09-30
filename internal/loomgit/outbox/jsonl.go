package outbox

import (
	"context"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

func EmitJSONL(bus *events.Bus, event loomgit.OutboxEvent) error {
	if err := bus.Emit(events.Event{Type: events.EventType(event.Kind),
		EventID: "loomgit:" + strconv.FormatInt(event.ID, 10), Data: event.Payload}); err != nil {
		return err
	}
	return bus.Flush()
}

// EmitPending writes recovery events to JSONL without acknowledging outbox rows.
// The UI dispatcher still owns SSE delivery and the final acknowledgement.
func EmitPending(ctx context.Context, store Store, bus *events.Bus) error {
	pending, err := store.PendingEvents(ctx)
	if err != nil {
		return err
	}
	for _, event := range pending {
		if err := EmitJSONL(bus, event); err != nil {
			return err
		}
	}
	return nil
}
