package outbox

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

type Store interface {
	PendingEvents(context.Context) ([]loomgit.OutboxEvent, error)
	MarkDelivered(context.Context, int64) error
	Close() error
}

type Emitter interface {
	Emit(context.Context, loomgit.OutboxEvent) error
}

type EmitFunc func(context.Context, loomgit.OutboxEvent) error

func (emit EmitFunc) Emit(ctx context.Context, event loomgit.OutboxEvent) error {
	return emit(ctx, event)
}

// Dispatch leaves an event pending until its sink accepts it. The sink must
// deduplicate by event ID if it can succeed immediately before a crash.
func Dispatch(ctx context.Context, store Store, sink Emitter) error {
	events, err := store.PendingEvents(ctx)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := sink.Emit(ctx, event); err != nil {
			return err
		}
		if err := store.MarkDelivered(ctx, event.ID); err != nil {
			return err
		}
	}
	return nil
}
