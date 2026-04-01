package notify

import (
	"sync"
	"testing"
	"time"
)

// Compile-time interface checks.
var (
	_ Publisher = (*Bus)(nil)
	_ Publisher = NopPublisher{}
)

func recv(ch <-chan Event, timeout time.Duration) (Event, bool) {
	select {
	case e, ok := <-ch:
		return e, ok
	case <-time.After(timeout):
		return Event{}, false
	}
}

const shortWait = 50 * time.Millisecond

// --- Basic Publish/Subscribe ---

func TestPublishSubscribeBasic(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("")
	defer sub.Close()

	now := time.Now()
	bus.Publish(Event{
		Topic:       "test.event",
		WorkspaceID: "ws-1",
		Payload:     "hello",
		Timestamp:   now,
	})

	e, ok := recv(sub.Events(), shortWait)
	if !ok {
		t.Fatal("expected to receive event")
	}
	if e.Topic != "test.event" {
		t.Errorf("topic = %q, want %q", e.Topic, "test.event")
	}
	if e.WorkspaceID != "ws-1" {
		t.Errorf("workspace = %q, want %q", e.WorkspaceID, "ws-1")
	}
	if e.Payload != "hello" {
		t.Errorf("payload = %v, want %q", e.Payload, "hello")
	}
	if !e.Timestamp.Equal(now) {
		t.Errorf("timestamp = %v, want %v", e.Timestamp, now)
	}
}

func TestPublishAutoSetsTimestamp(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("")
	defer sub.Close()

	before := time.Now()
	bus.Publish(Event{Topic: "t"})

	e, ok := recv(sub.Events(), shortWait)
	if !ok {
		t.Fatal("expected event")
	}
	if e.Timestamp.Before(before) {
		t.Errorf("auto-set timestamp %v is before publish time %v", e.Timestamp, before)
	}
}

func TestPublishPreservesNonZeroTimestamp(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("")
	defer sub.Close()

	fixed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	bus.Publish(Event{Topic: "t", Timestamp: fixed})

	e, ok := recv(sub.Events(), shortWait)
	if !ok {
		t.Fatal("expected event")
	}
	if !e.Timestamp.Equal(fixed) {
		t.Errorf("timestamp = %v, want %v", e.Timestamp, fixed)
	}
}

// --- Workspace Scoping ---

func TestWorkspaceScopedReceivesMatching(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("ws-1")
	defer sub.Close()

	bus.Publish(Event{Topic: "t", WorkspaceID: "ws-1"})

	_, ok := recv(sub.Events(), shortWait)
	if !ok {
		t.Fatal("expected to receive event for matching workspace")
	}
}

func TestWorkspaceScopedRejectsNonMatching(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("ws-1")
	defer sub.Close()

	bus.Publish(Event{Topic: "t", WorkspaceID: "ws-2"})

	_, ok := recv(sub.Events(), shortWait)
	if ok {
		t.Fatal("should not receive event for different workspace")
	}
}

func TestGlobalSubscriberReceivesAll(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("")
	defer sub.Close()

	bus.Publish(Event{Topic: "t", WorkspaceID: "ws-1"})
	bus.Publish(Event{Topic: "t", WorkspaceID: "ws-2"})
	bus.Publish(Event{Topic: "t", WorkspaceID: ""})

	for i := 0; i < 3; i++ {
		_, ok := recv(sub.Events(), shortWait)
		if !ok {
			t.Fatalf("expected event %d", i)
		}
	}
}

func TestWorkspaceScopedRejectsEmptyWorkspaceEvent(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("ws-1")
	defer sub.Close()

	bus.Publish(Event{Topic: "t", WorkspaceID: ""})

	_, ok := recv(sub.Events(), shortWait)
	if ok {
		t.Fatal("scoped subscriber should not receive system-wide event")
	}
}

func TestEmptyWorkspaceEventOnlyGlobal(t *testing.T) {
	bus := New()
	defer bus.Close()

	global := bus.Subscribe("")
	defer global.Close()
	scoped := bus.Subscribe("ws-1")
	defer scoped.Close()

	bus.Publish(Event{Topic: "t", WorkspaceID: ""})

	_, ok := recv(global.Events(), shortWait)
	if !ok {
		t.Fatal("global subscriber should receive system-wide event")
	}

	_, ok = recv(scoped.Events(), shortWait)
	if ok {
		t.Fatal("scoped subscriber should not receive system-wide event")
	}
}

// --- Topic Filtering ---

func TestTopicExactMatch(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("", "issue")
	defer sub.Close()

	bus.Publish(Event{Topic: "issue", WorkspaceID: "ws-1"})

	_, ok := recv(sub.Events(), shortWait)
	if !ok {
		t.Fatal("expected exact topic match")
	}
}

func TestTopicPrefixMatch(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("", "issue")
	defer sub.Close()

	bus.Publish(Event{Topic: "issue.created", WorkspaceID: "ws-1"})

	_, ok := recv(sub.Events(), shortWait)
	if !ok {
		t.Fatal("expected prefix topic match")
	}
}

func TestTopicNoFalsePrefix(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("", "issue")
	defer sub.Close()

	bus.Publish(Event{Topic: "issues", WorkspaceID: "ws-1"})

	_, ok := recv(sub.Events(), shortWait)
	if ok {
		t.Fatal("'issues' should not match topic filter 'issue'")
	}
}

func TestTopicWrongPrefix(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("", "issue")
	defer sub.Close()

	bus.Publish(Event{Topic: "session.created", WorkspaceID: "ws-1"})

	_, ok := recv(sub.Events(), shortWait)
	if ok {
		t.Fatal("session.created should not match issue filter")
	}
}

func TestNoTopicFilterReceivesAll(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("")
	defer sub.Close()

	bus.Publish(Event{Topic: "issue.created"})
	bus.Publish(Event{Topic: "session.started"})

	for i := 0; i < 2; i++ {
		_, ok := recv(sub.Events(), shortWait)
		if !ok {
			t.Fatalf("expected event %d with no topic filter", i)
		}
	}
}

func TestMultipleTopicFilters(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("", "issue", "session")
	defer sub.Close()

	bus.Publish(Event{Topic: "issue.created"})
	bus.Publish(Event{Topic: "session.started"})
	bus.Publish(Event{Topic: "terminal.changed"})

	// Should receive issue and session events.
	for i := 0; i < 2; i++ {
		_, ok := recv(sub.Events(), shortWait)
		if !ok {
			t.Fatalf("expected event %d", i)
		}
	}
	// Should NOT receive terminal event.
	_, ok := recv(sub.Events(), shortWait)
	if ok {
		t.Fatal("should not receive terminal event")
	}
}

func TestSubTopicExactNotParent(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("", "issue.created")
	defer sub.Close()

	bus.Publish(Event{Topic: "issue.created"})
	bus.Publish(Event{Topic: "issue.updated"})
	bus.Publish(Event{Topic: "issue"})

	_, ok := recv(sub.Events(), shortWait)
	if !ok {
		t.Fatal("expected issue.created")
	}

	_, ok = recv(sub.Events(), shortWait)
	if ok {
		t.Fatal("issue.updated should not match issue.created filter")
	}

	_, ok = recv(sub.Events(), shortWait)
	if ok {
		t.Fatal("bare 'issue' should not match 'issue.created' filter")
	}
}

// --- Combined Workspace + Topic ---

func TestCombinedWorkspaceAndTopic(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("ws-1", "issue")
	defer sub.Close()

	bus.Publish(Event{Topic: "issue.created", WorkspaceID: "ws-1"})
	_, ok := recv(sub.Events(), shortWait)
	if !ok {
		t.Fatal("expected matching workspace+topic event")
	}

	bus.Publish(Event{Topic: "issue.created", WorkspaceID: "ws-2"})
	_, ok = recv(sub.Events(), shortWait)
	if ok {
		t.Fatal("wrong workspace should not match")
	}

	bus.Publish(Event{Topic: "session.started", WorkspaceID: "ws-1"})
	_, ok = recv(sub.Events(), shortWait)
	if ok {
		t.Fatal("wrong topic should not match")
	}
}

// --- Fan-out ---

func TestFanOutToMultipleSubscribers(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub1 := bus.Subscribe("")
	defer sub1.Close()
	sub2 := bus.Subscribe("")
	defer sub2.Close()

	bus.Publish(Event{Topic: "t"})

	_, ok1 := recv(sub1.Events(), shortWait)
	_, ok2 := recv(sub2.Events(), shortWait)
	if !ok1 || !ok2 {
		t.Fatal("both subscribers should receive the event")
	}
}

func TestFanOutWorkspaceIsolation(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub1 := bus.Subscribe("ws-1")
	defer sub1.Close()
	sub2 := bus.Subscribe("ws-2")
	defer sub2.Close()

	bus.Publish(Event{Topic: "t", WorkspaceID: "ws-1"})

	_, ok1 := recv(sub1.Events(), shortWait)
	if !ok1 {
		t.Fatal("ws-1 subscriber should receive ws-1 event")
	}
	_, ok2 := recv(sub2.Events(), shortWait)
	if ok2 {
		t.Fatal("ws-2 subscriber should not receive ws-1 event")
	}
}

func TestGlobalAndScopedBothReceive(t *testing.T) {
	bus := New()
	defer bus.Close()

	global := bus.Subscribe("")
	defer global.Close()
	scoped := bus.Subscribe("ws-1")
	defer scoped.Close()

	bus.Publish(Event{Topic: "t", WorkspaceID: "ws-1"})

	_, ok1 := recv(global.Events(), shortWait)
	_, ok2 := recv(scoped.Events(), shortWait)
	if !ok1 || !ok2 {
		t.Fatal("both global and scoped should receive matching event")
	}
}

// --- Backpressure ---

func TestBackpressureDropsEvent(t *testing.T) {
	bus := New(WithBufferSize(2))
	defer bus.Close()

	sub := bus.Subscribe("")
	defer sub.Close()

	// Fill the buffer.
	bus.Publish(Event{Topic: "t1"})
	bus.Publish(Event{Topic: "t2"})
	// This should be dropped.
	bus.Publish(Event{Topic: "t3"})

	if sub.Dropped() != 1 {
		t.Errorf("dropped = %d, want 1", sub.Dropped())
	}
}

func TestBackpressureDoesNotAffectOthers(t *testing.T) {
	bus := New()
	defer bus.Close()

	slow := bus.SubscribeWithBuffer(1, "")
	defer slow.Close()
	fast := bus.Subscribe("")
	defer fast.Close()

	// Fill slow subscriber's buffer.
	bus.Publish(Event{Topic: "t1"})
	// This will be dropped for slow, but fast has room.
	bus.Publish(Event{Topic: "t2"})

	// Drain fast.
	_, ok := recv(fast.Events(), shortWait)
	if !ok {
		t.Fatal("fast subscriber should receive first event")
	}
	_, ok = recv(fast.Events(), shortWait)
	if !ok {
		t.Fatal("fast subscriber should receive second event")
	}

	if slow.Dropped() == 0 {
		// slow had buffer=1, received 2 events, should have dropped at least 1
		t.Fatal("slow subscriber should have dropped events")
	}
}

func TestTotalDropped(t *testing.T) {
	bus := New(WithBufferSize(1))
	defer bus.Close()

	sub1 := bus.Subscribe("")
	defer sub1.Close()
	sub2 := bus.Subscribe("")
	defer sub2.Close()

	// Fill both buffers.
	bus.Publish(Event{Topic: "t1"})
	// Both full, this should be dropped for both.
	bus.Publish(Event{Topic: "t2"})

	if bus.TotalDropped() != 2 {
		t.Errorf("total dropped = %d, want 2", bus.TotalDropped())
	}
}

// --- Subscription Lifecycle ---

func TestSubscriptionCloseClosesChannel(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("")
	sub.Close()

	// Channel should be closed: receive returns zero value + false.
	_, ok := <-sub.Events()
	if ok {
		t.Fatal("channel should be closed after Close()")
	}
}

func TestSubscriptionDoubleCloseNoPanic(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("")
	sub.Close()
	sub.Close() // Should not panic.
}

func TestSubscriptionCloseStopsDelivery(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("")
	sub.Close()

	bus.Publish(Event{Topic: "t"})

	// The channel is closed so we should get zero value, not a real event.
	e, ok := <-sub.Events()
	if ok {
		t.Fatalf("should not receive event after close, got %+v", e)
	}
}

func TestSubscriberCountDecrementsOnClose(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub1 := bus.Subscribe("")
	sub2 := bus.Subscribe("")

	if bus.SubscriberCount() != 2 {
		t.Fatalf("count = %d, want 2", bus.SubscriberCount())
	}

	sub1.Close()
	if bus.SubscriberCount() != 1 {
		t.Fatalf("count = %d, want 1", bus.SubscriberCount())
	}

	sub2.Close()
	if bus.SubscriberCount() != 0 {
		t.Fatalf("count = %d, want 0", bus.SubscriberCount())
	}
}

// --- Bus Lifecycle ---

func TestBusClosePublishIsNoop(t *testing.T) {
	bus := New()
	sub := bus.Subscribe("")
	bus.Close()

	bus.Publish(Event{Topic: "t"}) // Should not panic.

	_, ok := <-sub.Events()
	if ok {
		t.Fatal("should not receive events after bus close")
	}
}

func TestBusCloseSubscribeReturnsNil(t *testing.T) {
	bus := New()
	bus.Close()

	sub := bus.Subscribe("")
	if sub != nil {
		t.Fatal("Subscribe after Close should return nil")
	}
}

func TestBusCloseClosesAllSubscriptions(t *testing.T) {
	bus := New()

	sub1 := bus.Subscribe("")
	sub2 := bus.Subscribe("ws-1")

	bus.Close()

	_, ok1 := <-sub1.Events()
	_, ok2 := <-sub2.Events()
	if ok1 || ok2 {
		t.Fatal("all subscription channels should be closed after bus close")
	}
}

func TestBusDoubleCloseNoPanic(t *testing.T) {
	bus := New()
	bus.Close()
	bus.Close() // Should not panic.
}

// --- Configuration ---

func TestWithBufferSize(t *testing.T) {
	bus := New(WithBufferSize(128))
	defer bus.Close()

	sub := bus.Subscribe("")
	defer sub.Close()

	// Fill 128 events without blocking.
	for i := 0; i < 128; i++ {
		bus.Publish(Event{Topic: "t"})
	}
	if sub.Dropped() != 0 {
		t.Errorf("dropped = %d with buffer 128, want 0", sub.Dropped())
	}
	// 129th should be dropped.
	bus.Publish(Event{Topic: "t"})
	if sub.Dropped() != 1 {
		t.Errorf("dropped = %d, want 1", sub.Dropped())
	}
}

func TestWithBufferSizeZeroClamped(t *testing.T) {
	bus := New(WithBufferSize(0))
	defer bus.Close()

	sub := bus.Subscribe("")
	defer sub.Close()

	bus.Publish(Event{Topic: "t"})
	_, ok := recv(sub.Events(), shortWait)
	if !ok {
		t.Fatal("should receive event even with buffer size clamped from 0 to 1")
	}
}

func TestWithBufferSizeNegativeClamped(t *testing.T) {
	bus := New(WithBufferSize(-5))
	defer bus.Close()

	sub := bus.Subscribe("")
	defer sub.Close()

	bus.Publish(Event{Topic: "t"})
	_, ok := recv(sub.Events(), shortWait)
	if !ok {
		t.Fatal("should receive event even with negative buffer size clamped to 1")
	}
}

func TestSubscribeWithBufferOverride(t *testing.T) {
	bus := New(WithBufferSize(2))
	defer bus.Close()

	sub := bus.SubscribeWithBuffer(256, "ws-1")
	defer sub.Close()

	for i := 0; i < 256; i++ {
		bus.Publish(Event{Topic: "t", WorkspaceID: "ws-1"})
	}
	if sub.Dropped() != 0 {
		t.Errorf("dropped = %d with buffer 256, want 0", sub.Dropped())
	}
}

// --- Concurrency ---

func TestConcurrentPublish(t *testing.T) {
	bus := New()
	defer bus.Close()

	sub := bus.Subscribe("")
	defer sub.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				bus.Publish(Event{Topic: "t"})
			}
		}()
	}
	wg.Wait()

	// Drain and count. Some may be dropped if buffer fills.
	received := 0
	for {
		_, ok := recv(sub.Events(), shortWait)
		if !ok {
			break
		}
		received++
	}
	total := received + int(sub.Dropped())
	if total != 1000 {
		t.Errorf("received+dropped = %d, want 1000", total)
	}
}

func TestConcurrentSubscribeClose(t *testing.T) {
	bus := New()
	defer bus.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				sub := bus.Subscribe("")
				if sub != nil {
					sub.Close()
				}
			}
		}()
	}
	wg.Wait()

	if bus.SubscriberCount() != 0 {
		t.Errorf("subscriber count = %d, want 0", bus.SubscriberCount())
	}
}

func TestConcurrentPublishSubscribeClose(t *testing.T) {
	bus := New()

	var wg sync.WaitGroup

	// Publishers.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				bus.Publish(Event{Topic: "t", WorkspaceID: "ws-1"})
			}
		}()
	}

	// Subscribe/close cyclers.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				sub := bus.Subscribe("ws-1")
				if sub != nil {
					sub.Close()
				}
			}
		}()
	}

	wg.Wait()
	bus.Close()
}

// --- NopPublisher ---

func TestNopPublisherDoesNotPanic(t *testing.T) {
	var p NopPublisher
	p.Publish(Event{Topic: "t", Payload: "data"})
	p.Publish(Event{})
}
