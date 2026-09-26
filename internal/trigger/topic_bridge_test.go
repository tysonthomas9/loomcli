package trigger_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// fakeTopicConsumer is an in-memory stand-in for the bus's durable-consume
// half. It models the parts the bridge depends on: a single drain lease, a
// cursor that only moves on ack, broker-tracked attempts, and a dead-letter
// sink.
type fakeTopicConsumer struct {
	messages []store.TopicMessage
	cursor   string // last acked cursor ("" = before the first message)
	attempts int
	token    string
	// leaseHeldByOther makes AcquireLease report contention.
	leaseHeldByOther bool
	deadLettered     []string
	acks             [][2]string // (from, position) pairs
	ensured          int
	released         int
	pullErr          error
	lastOwner        string
}

func (f *fakeTopicConsumer) EnsureSubscription(_ context.Context, _, _, _, _ string) error {
	f.ensured++
	return nil
}

func (f *fakeTopicConsumer) AcquireLease(_ context.Context, _, _, _, owner, token string, _ time.Duration) (string, bool, error) {
	f.lastOwner = owner
	if f.leaseHeldByOther {
		return "", false, nil
	}
	if token != "" {
		return token, true, nil // renew keeps the held token
	}
	f.token = "lease-1"
	return f.token, true, nil
}

func (f *fakeTopicConsumer) ReleaseLease(_ context.Context, _, _, _, _ string) error {
	f.released++
	return nil
}

func (f *fakeTopicConsumer) Pull(_ context.Context, _, _, _, token string, limit int, _ time.Duration) (*store.TopicPull, error) {
	if f.pullErr != nil {
		return nil, f.pullErr
	}
	if token == "" {
		return nil, errors.New("pull without a lease token")
	}
	pending := f.pending()
	if limit > 0 && len(pending) > limit {
		pending = pending[:limit]
	}
	out := &store.TopicPull{Messages: pending, Cursor: f.cursor, Attempts: f.attempts}
	if len(pending) > 0 {
		out.Head = pending[0].Cursor
	}
	return out, nil
}

// pending returns everything after the acked cursor, preserving order.
func (f *fakeTopicConsumer) pending() []store.TopicMessage {
	if f.cursor == "" {
		return f.messages
	}
	for i, m := range f.messages {
		if m.Cursor == f.cursor {
			return f.messages[i+1:]
		}
	}
	return f.messages
}

// SubscriptionCursor reports the stored cursor, mirroring the broker's
// "unset means 0" rule.
func (f *fakeTopicConsumer) SubscriptionCursor(_ context.Context, _, _, _ string) (string, bool, error) {
	if f.cursor == "" {
		return "0", true, nil
	}
	return f.cursor, true, nil
}

func (f *fakeTopicConsumer) Ack(_ context.Context, _, _, _, _, from, position string) error {
	stored := f.cursor
	if stored == "" {
		stored = "0"
	}
	if from != stored {
		return errors.New("ack CAS failed: cursor moved")
	}
	f.acks = append(f.acks, [2]string{from, position})
	f.cursor = position
	f.attempts = 0
	return nil
}

func (f *fakeTopicConsumer) DeadLetter(_ context.Context, _, _, _, _, expectedCursor string, _ int, _ string) error {
	pending := f.pending()
	if len(pending) == 0 || pending[0].Cursor != expectedCursor {
		return errors.New("dead-letter expected_cursor mismatch")
	}
	f.deadLettered = append(f.deadLettered, expectedCursor)
	f.cursor = expectedCursor
	f.attempts = 0
	return nil
}

func topicMsg(id, cursor, kind string, trace map[string]string) store.TopicMessage {
	return store.TopicMessage{
		ID: id, Cursor: cursor, Topic: "deploys", Kind: kind, Trace: trace,
		Payload: json.RawMessage(`{"ok":true}`),
	}
}

// newTopicBridge wires a bridge over a fresh memstore carrying a binding on the
// given route key.
func newTopicBridge(t *testing.T, consumer store.TopicConsumer, routeKey string) (*trigger.TopicBridge, *memstore.Store) {
	t.Helper()
	s := memstore.New()
	ctx := t.Context()
	if _, err := s.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "topic-bot", Name: "topic-bot",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "v1", DriverID: "topic-bot", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "b-topic", Name: "b-topic",
		SourceKind: "internal", RouteKey: routeKey, Topic: "deploys",
		DriverID: "topic-bot", DriverVersionID: "v1", TargetEntrypoint: "run",
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyAllow, Enabled: true,
	}); err != nil {
		t.Fatalf("create binding: %v", err)
	}
	return &trigger.TopicBridge{
		Source: &trigger.InternalSource{Store: s}, Consumer: consumer,
		WorkspaceKey: "WS", Topic: "deploys", Subscriber: "trigger-router",
	}, s
}

// TestTopicBridgeDispatchesAndAcks is the happy path: messages become trigger
// events, in order, and the cursor advances exactly once past the batch.
func TestTopicBridgeDispatchesAndAcks(t *testing.T) {
	consumer := &fakeTopicConsumer{messages: []store.TopicMessage{
		topicMsg("m1", "1-0", "deploy.requested", map[string]string{"actor": "alice", "env": "prod"}),
		topicMsg("m2", "2-0", "deploy.requested", nil),
	}}
	bridge, s := newTopicBridge(t, consumer, "internal.deploy.requested")

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Emitted != 2 || out.Acked != 2 || out.LeaseHeldElsewhere {
		t.Fatalf("result = %+v, want 2 emitted 2 acked", out)
	}
	if len(consumer.acks) != 1 || consumer.acks[0] != [2]string{"0", "2-0"} {
		t.Fatalf("acks = %v, want a single advance to the batch tail", consumer.acks)
	}

	evs, _ := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	if len(evs) != 2 {
		t.Fatalf("events = %d, want 2", len(evs))
	}
	// List returns newest-first, so index by source id rather than position.
	bySource := map[string]*domain.TriggerEvent{}
	for _, ev := range evs {
		bySource[ev.SourceEventID] = ev
	}
	withTrace := bySource[trigger.TopicEventIDPrefix+"deploys-m1"]
	withoutTrace := bySource[trigger.TopicEventIDPrefix+"deploys-m2"]
	if withTrace == nil || withoutTrace == nil {
		t.Fatalf("events by source id = %+v, want both messages", bySource)
	}
	// The message kind is the event type, so one topic can carry many types.
	if withTrace.EventType != "deploy.requested" {
		t.Fatalf("event type = %q, want deploy.requested", withTrace.EventType)
	}
	// The actor rides from trace, never synthesized.
	if withTrace.ActorRef != "alice" {
		t.Fatalf("actor = %q, want alice from trace", withTrace.ActorRef)
	}
	if withoutTrace.ActorRef != "" {
		t.Fatalf("actor = %q, want empty when trace carries none", withoutTrace.ActorRef)
	}
}

// TestTopicBridgeRedeliveryDedups pins the at-least-once story: replaying the
// same messages (an ack that never landed) must not produce second runs,
// because the loopback event id is derived from the broker's message id.
func TestTopicBridgeRedeliveryDedups(t *testing.T) {
	msgs := []store.TopicMessage{topicMsg("m1", "1-0", "deploy.requested", nil)}
	consumer := &fakeTopicConsumer{messages: msgs}
	bridge, s := newTopicBridge(t, consumer, "internal.deploy.requested")

	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	// Rewind the cursor: the broker redelivers what was never durably acked.
	consumer.cursor = ""
	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	evs, _ := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	if len(evs) != 1 {
		t.Fatalf("events = %d after redelivery, want 1 (deduped)", len(evs))
	}
	runs, _ := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if len(runs) != 1 {
		t.Fatalf("runs = %d after redelivery, want 1 (no duplicate work)", len(runs))
	}
}

// TestTopicBridgeLeaseContentionIsNotAnError proves a losing consumer backs off
// quietly instead of erroring or draining.
func TestTopicBridgeLeaseContentionIsNotAnError(t *testing.T) {
	consumer := &fakeTopicConsumer{
		messages:         []store.TopicMessage{topicMsg("m1", "1-0", "deploy.requested", nil)},
		leaseHeldByOther: true,
	}
	bridge, s := newTopicBridge(t, consumer, "internal.deploy.requested")

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !out.LeaseHeldElsewhere || out.Emitted != 0 {
		t.Fatalf("result = %+v, want a quiet no-op pass", out)
	}
	if evs, _ := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{}); len(evs) != 0 {
		t.Fatalf("events = %d, want 0 (the other consumer owns the drain)", len(evs))
	}
}

// TestTopicBridgeDeadLettersPoisonHead proves a message that has exhausted its
// broker-tracked attempts is retired so the subscription resumes, rather than
// blocking head-of-line forever.
func TestTopicBridgeDeadLettersPoisonHead(t *testing.T) {
	consumer := &fakeTopicConsumer{
		messages: []store.TopicMessage{
			topicMsg("poison", "1-0", "deploy.requested", nil),
			topicMsg("good", "2-0", "deploy.requested", nil),
		},
		attempts: 5, // already at the default limit
	}
	bridge, s := newTopicBridge(t, consumer, "internal.deploy.requested")

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.DeadLettered != 1 || out.Emitted != 0 {
		t.Fatalf("result = %+v, want the poison head retired and nothing dispatched", out)
	}
	if len(consumer.deadLettered) != 1 || consumer.deadLettered[0] != "1-0" {
		t.Fatalf("deadLettered = %v, want the head cursor only", consumer.deadLettered)
	}

	// The next pass makes progress on the message behind it.
	out, err = bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("result = %+v, want the following message dispatched", out)
	}
	if evs, _ := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{}); len(evs) != 1 {
		t.Fatalf("events = %d, want 1 (only the good message)", len(evs))
	}
}

// TestTopicBridgeNoBindingStillAcks proves a topic nobody binds does not stall
// the subscription: the message is acked past rather than retried forever.
func TestTopicBridgeNoBindingStillAcks(t *testing.T) {
	consumer := &fakeTopicConsumer{messages: []store.TopicMessage{
		topicMsg("m1", "1-0", "nobody.listens", nil),
	}}
	// The binding is on a different route key, so this message matches nothing.
	bridge, _ := newTopicBridge(t, consumer, "internal.deploy.requested")

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Acked != 1 {
		t.Fatalf("result = %+v, want the unmatched message acked past", out)
	}
	if consumer.cursor != "1-0" {
		t.Fatalf("cursor = %q, want it advanced past the unbound message", consumer.cursor)
	}
}

// TestTopicBridgeDefaultsAndSubject pins the projection defaults: a kindless
// message lands on topic.message and addresses its own topic.
func TestTopicBridgeDefaultsAndSubject(t *testing.T) {
	consumer := &fakeTopicConsumer{messages: []store.TopicMessage{
		topicMsg("m1", "1-0", "", nil),
	}}
	bridge, s := newTopicBridge(t, consumer, "internal."+trigger.DefaultTopicEventType)

	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	evs, _ := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	if evs[0].EventType != trigger.DefaultTopicEventType {
		t.Fatalf("event type = %q, want %q", evs[0].EventType, trigger.DefaultTopicEventType)
	}
	if evs[0].SubjectRef != trigger.TopicSubjectRefPrefix+"deploys" {
		t.Fatalf("subject = %q, want the topic namespace", evs[0].SubjectRef)
	}
}

// TestTopicBridgeCloseReleasesLease proves shutdown hands the drain over
// promptly instead of leaving peers to wait out the TTL.
func TestTopicBridgeCloseReleasesLease(t *testing.T) {
	consumer := &fakeTopicConsumer{messages: []store.TopicMessage{topicMsg("m1", "1-0", "deploy.requested", nil)}}
	bridge, _ := newTopicBridge(t, consumer, "internal.deploy.requested")

	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if err := bridge.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if consumer.released != 1 {
		t.Fatalf("released = %d, want 1", consumer.released)
	}
	// Close is idempotent — a second call has no lease to give back.
	if err := bridge.Close(t.Context()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if consumer.released != 1 {
		t.Fatalf("released = %d after a second Close, want 1", consumer.released)
	}
}
