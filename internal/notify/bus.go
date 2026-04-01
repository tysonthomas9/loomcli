package notify

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultBufferSize is the default per-subscriber channel buffer size.
const DefaultBufferSize = 64

// Event is the envelope for all bus events.
type Event struct {
	Topic       string    // Dot-delimited event topic (e.g., "issue.created")
	WorkspaceID string    // Workspace this event belongs to; empty = system-wide
	Payload     any       // Typed event data; consumers type-assert
	Timestamp   time.Time // When the event was created; auto-set if zero on Publish
}

// Publisher is the producer-facing interface for publishing events.
type Publisher interface {
	Publish(Event)
}

// NopPublisher implements Publisher and discards all events.
type NopPublisher struct{}

// Publish is a no-op.
func (NopPublisher) Publish(Event) {}

// Compile-time interface checks.
var (
	_ Publisher = (*Bus)(nil)
	_ Publisher = NopPublisher{}
)

// Option configures a Bus.
type Option func(*busConfig)

type busConfig struct {
	bufferSize int
}

// WithBufferSize sets the default per-subscriber buffer size.
func WithBufferSize(n int) Option {
	return func(c *busConfig) {
		c.bufferSize = n
	}
}

// Subscription represents a registered subscriber to the bus.
// Use Events() to receive events and Close() to unregister.
type Subscription struct {
	sub  *subscriber
	bus  *Bus
	once sync.Once
}

// Events returns the read-only channel for receiving events.
func (s *Subscription) Events() <-chan Event {
	return s.sub.ch
}

// Close unregisters the subscriber from the bus and closes the channel.
// Safe to call multiple times.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.bus.removeSub(s.sub)
		// Drain and close the channel.
		for range s.sub.ch {
		}
	})
}

// Dropped returns the number of events dropped due to buffer overflow.
func (s *Subscription) Dropped() int64 {
	return s.sub.dropped.Load()
}

// subscriber is the internal state for a registered subscription.
type subscriber struct {
	ch          chan Event
	workspaceID string
	topics      []string
	dropped     atomic.Int64
}

// Bus is an in-process pub/sub hub with workspace-scoped subscriptions.
// It implements the Publisher interface.
type Bus struct {
	mu         sync.RWMutex
	subs       []*subscriber
	bufferSize int
	closed     bool
	closeOnce  sync.Once
}

// New creates a new Bus ready to use. No background goroutines are started.
func New(opts ...Option) *Bus {
	cfg := busConfig{bufferSize: DefaultBufferSize}
	for _, o := range opts {
		o(&cfg)
	}
	return &Bus{
		bufferSize: clampBuffer(cfg.bufferSize),
	}
}

// Publish delivers an event to all matching subscribers. Non-blocking: if a
// subscriber's buffer is full, the event is dropped for that subscriber.
// After Close, Publish is a no-op.
func (b *Bus) Publish(e Event) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	for _, sub := range b.subs {
		if !matches(sub, e) {
			continue
		}
		select {
		case sub.ch <- e:
		default:
			sub.dropped.Add(1)
		}
	}
}

// Subscribe creates a subscription scoped to the given workspace and topic
// prefixes. An empty workspaceID subscribes to events from all workspaces.
// No topics means all topics. Returns nil after the bus is closed.
func (b *Bus) Subscribe(workspaceID string, topics ...string) *Subscription {
	return b.subscribeWithBuffer(b.bufferSize, workspaceID, topics)
}

// SubscribeWithBuffer creates a subscription with a custom buffer size.
func (b *Bus) SubscribeWithBuffer(bufferSize int, workspaceID string, topics ...string) *Subscription {
	return b.subscribeWithBuffer(clampBuffer(bufferSize), workspaceID, topics)
}

func (b *Bus) subscribeWithBuffer(bufSize int, workspaceID string, topics []string) *Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}

	sub := &subscriber{
		ch:          make(chan Event, bufSize),
		workspaceID: workspaceID,
		topics:      topics,
	}
	b.subs = append(b.subs, sub)
	return &Subscription{sub: sub, bus: b}
}

// Close shuts down the bus. All subscriber channels are closed.
// After Close, Publish is a no-op and Subscribe returns nil.
// Safe to call multiple times.
func (b *Bus) Close() {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		b.closed = true
		for _, sub := range b.subs {
			close(sub.ch)
		}
		b.subs = nil
	})
}

// SubscriberCount returns the number of active subscribers.
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// TotalDropped returns the sum of all dropped events across active subscribers.
func (b *Bus) TotalDropped() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var total int64
	for _, sub := range b.subs {
		total += sub.dropped.Load()
	}
	return total
}

// removeSub removes a subscriber from the bus and closes its channel.
func (b *Bus) removeSub(sub *subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for i, s := range b.subs {
		if s == sub {
			last := len(b.subs) - 1
			b.subs[i] = b.subs[last]
			b.subs[last] = nil // allow GC
			b.subs = b.subs[:last]
			close(sub.ch)
			return
		}
	}
}

// matches returns true if the event should be delivered to the subscriber.
func matches(sub *subscriber, e Event) bool {
	// Workspace filter.
	if sub.workspaceID != "" && sub.workspaceID != e.WorkspaceID {
		return false
	}
	// Topic filter.
	if len(sub.topics) == 0 {
		return true
	}
	for _, prefix := range sub.topics {
		if e.Topic == prefix || strings.HasPrefix(e.Topic, prefix+".") {
			return true
		}
	}
	return false
}

// clampBuffer ensures a minimum buffer size of 1.
func clampBuffer(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
