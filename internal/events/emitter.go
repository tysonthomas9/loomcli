package events

import (
	"sync"
)

// Listener is a callback invoked synchronously when an event is emitted.
type Listener func(Event)

// Emitter is the interface for emitting events.
type Emitter interface {
	Emit(Event) error
	Close() error
}

// Bus writes events to a JSONL file and notifies listeners synchronously.
type Bus struct {
	mu        sync.Mutex
	writer    *JSONLWriter
	listeners []Listener
}

// NewBus creates an event bus that writes to eventsDir with default rotation settings.
func NewBus(eventsDir string) *Bus {
	return &Bus{
		writer: NewJSONLWriter(eventsDir, defaultMaxSize, defaultMaxBackups),
	}
}

// Subscribe adds a listener that will be called on each emitted event.
func (b *Bus) Subscribe(l Listener) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners = append(b.listeners, l)
}

// Emit writes an event to the JSONL writer and notifies all listeners.
// Auto-sets Timestamp if zero.
// Note: under concurrent emits, listener notification order may differ from
// file write order. Listeners should not depend on ordering.
func (b *Bus) Emit(e Event) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = Now()
	}

	if err := b.writer.Write(e); err != nil {
		return err
	}

	// Snapshot listeners under lock to avoid holding it during callbacks
	b.mu.Lock()
	listeners := make([]Listener, len(b.listeners))
	copy(listeners, b.listeners)
	b.mu.Unlock()

	for _, l := range listeners {
		l(e)
	}
	return nil
}

// Close flushes and closes the underlying writer.
func (b *Bus) Close() error {
	return b.writer.Close()
}

// NopBus is an Emitter that discards all events. Useful for tests and when events are disabled.
type NopBus struct{}

func (NopBus) Emit(Event) error { return nil }
func (NopBus) Close() error     { return nil }
