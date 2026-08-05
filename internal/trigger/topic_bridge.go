package trigger

// TopicBridge is the pub/sub sibling of IssueJournalBridge: it durably consumes
// a fleet-db topic and re-enters each message into the trigger router through
// InternalSource.Emit, stamped origin=system. That is what makes
// TriggerBinding.Topic mean something — a binding naming a topic gets a run per
// message on it.
//
// WHY DURABLE CONSUME AND NOT THE BROADCAST TAIL. The bus offers both. The tail
// (Read/follow) is best-effort and multi-reader: two serve processes tailing the
// same topic would each dispatch every message, and a restart would silently
// skip whatever arrived while down. Triggers are work, not display, so this
// takes the lease/pull/ack path: exactly one consumer drains, the cursor
// advances only on ack, and a crash redelivers rather than drops.
//
// AT-LEAST-ONCE MEETS EXACTLY-ONCE DISPATCH. Redelivery is guaranteed, so the
// bridge leans on the same idempotency the journal bridge does: the loopback
// key is internal:{ws}:topic-{topic}-{messageID}, derived from the broker's
// stable per-message id, so a redelivered message collapses onto the stored
// TriggerEvent and its fan-out legs instead of starting a second run. The ack
// is therefore an optimization of WHERE WE RESUME, not the thing that makes
// dispatch single-shot.
//
// ORDERING IS HEAD-OF-LINE. Messages are handled in cursor order and the bridge
// acks only the contiguous successful prefix: a message whose dispatch fails
// pauses the subscription rather than being skipped, so a topic modelling a
// sequential handoff cannot process step 3 after step 2 failed. A message that
// keeps failing is retired to the dead-letter log after MaxAttempts (broker-
// tracked, so the count survives a consumer swap) and the subscription resumes.
//
// SELF-TRIGGER. Like the journal bridge, this NEVER filters actors: a message
// published by a workflow run re-enters faithfully. A binding whose driver
// publishes to the topic it consumes will loop at hop_depth=0, which the
// structural hop cap does not stop. The guard is the binding's
// domain.TriggerActorFilter, which is per-binding policy, not transport.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TopicEventIDPrefix anchors the deterministic loopback event id derived from a
// topic message: "topic-{topic}-{messageID}". Exported so audit tooling can
// reconstruct the idempotency key a message re-entered under.
const TopicEventIDPrefix = "topic-"

// TopicSubjectRefPrefix scopes the loopback subject ref to the topic namespace.
// A message with no better subject addresses its topic, so a binding's default
// subject key still groups per topic rather than collapsing to empty.
const TopicSubjectRefPrefix = "topic:"

// DefaultTopicEventType is the event type a message re-enters under when it
// carries no kind, giving the route key internal.topic.message.
const DefaultTopicEventType = "topic.message"

// Defaults for the consume loop. The lease TTL is clamped to [5s, 5m] by the
// service; 30s is long enough to survive a slow dispatch batch and short enough
// that a crashed holder is replaced promptly.
const (
	DefaultTopicLeaseTTL    = 30 * time.Second
	DefaultTopicPullLimit   = 50
	DefaultTopicMaxAttempts = 5
)

// TopicBridge consumes one topic per RunOnce pass. One bridge per topic keeps
// the lease, cursor and dead-letter policy per-topic, which is how the bus
// models them.
type TopicBridge struct {
	// Source is the loopback ingress every message re-enters through.
	Source *InternalSource
	// Consumer is the durable-consume capability (fleet-db only).
	Consumer store.TopicConsumer
	// WorkspaceKey and Topic scope the subscription. Both required.
	WorkspaceKey string
	Topic        string
	// Subscriber names the durable cursor. Two serve processes sharing it
	// contend for one lease (only one drains); giving them different names
	// makes them independent consumers that EACH see every message.
	Subscriber string
	// Owner identifies THIS holder of the drain lease, surfaced as lease_owner
	// on the subscriptions listing so "which process is draining?" has an
	// answer during an incident. Distinct from Subscriber, which names the
	// shared durable cursor. Defaults to host/pid, which distinguishes peers
	// without any configuration.
	Owner string
	// StartCursor seeds a new subscription: "" = beginning, "$" = tip. Ignored
	// once the subscription exists.
	//
	// "$" is the safe default for switching the bridge on against a topic with
	// history — the same reasoning as the journal bridge's bootstrap
	// fast-forward, since replaying a backlog of messages would dispatch a run
	// for each.
	StartCursor string
	// LeaseTTL, PullLimit and MaxAttempts default to the constants above.
	LeaseTTL    time.Duration
	PullLimit   int
	MaxAttempts int
	// Logger receives lease-contention and dead-letter records.
	Logger *slog.Logger

	// leaseToken is the capability held between passes; renewed on each pass.
	leaseToken string
}

// TopicSweepResult summarizes one pass.
type TopicSweepResult struct {
	// Emitted counts messages re-entered into the router (dispatched or
	// dispatch-deduped).
	Emitted int
	// Acked counts messages whose cursor advance was durable.
	Acked int
	// DeadLettered counts poison messages retired this pass.
	DeadLettered int
	// LeaseHeldElsewhere reports that another consumer holds the lease, so this
	// pass drained nothing. Not an error.
	LeaseHeldElsewhere bool
}

// RunOnce performs one consume pass: ensure the subscription, take the lease,
// pull a batch, dispatch in order, ack the contiguous successful prefix.
//
// It deliberately does NOT release the lease on the happy path — holding it
// across passes is what keeps one consumer draining steadily instead of the
// subscription ping-ponging between processes every tick. Release is for
// shutdown (Close).
func (b *TopicBridge) RunOnce(ctx context.Context) (*TopicSweepResult, error) {
	if b == nil || b.Source == nil || b.Consumer == nil {
		return nil, fmt.Errorf("topic bridge: source and consumer are required: %w", domain.ErrInvalid)
	}
	if strings.TrimSpace(b.WorkspaceKey) == "" || strings.TrimSpace(b.Topic) == "" || strings.TrimSpace(b.Subscriber) == "" {
		return nil, fmt.Errorf("topic bridge: workspace, topic and subscriber are required: %w", domain.ErrInvalid)
	}
	out := &TopicSweepResult{}

	if err := b.Consumer.EnsureSubscription(ctx, b.WorkspaceKey, b.Topic, b.Subscriber, b.StartCursor); err != nil {
		return out, fmt.Errorf("ensure subscription %q on %q: %w", b.Subscriber, b.Topic, err)
	}
	token, acquired, err := b.Consumer.AcquireLease(ctx, b.WorkspaceKey, b.Topic, b.Subscriber, b.owner(), b.leaseToken, b.leaseTTL())
	if err != nil {
		return out, fmt.Errorf("acquire lease on %q: %w", b.Topic, err)
	}
	if !acquired {
		// Routine contention, not a failure: another consumer owns the drain.
		b.leaseToken = ""
		out.LeaseHeldElsewhere = true
		b.logger().Debug("topic bridge: lease held elsewhere, skipping pass",
			"workspace", b.WorkspaceKey, "topic", b.Topic, "subscriber", b.Subscriber)
		return out, nil
	}
	b.leaseToken = token

	// The ack CAS guard is the subscription's CURRENT cursor, which the pull
	// response does not carry (its Cursor is the ack-through target). Read it
	// under the lease we now hold, so it cannot move underneath this pass.
	from, _, err := b.Consumer.SubscriptionCursor(ctx, b.WorkspaceKey, b.Topic, b.Subscriber)
	if err != nil {
		return out, fmt.Errorf("read cursor for %q: %w", b.Topic, err)
	}
	if from == "" {
		// The broker treats an unset cursor as "0"; send the same so the CAS
		// compares equal on a subscription that has never acked.
		from = "0"
	}

	pull, err := b.Consumer.Pull(ctx, b.WorkspaceKey, b.Topic, b.Subscriber, b.leaseToken, b.pullLimit(), 0)
	if err != nil {
		return out, fmt.Errorf("pull %q: %w", b.Topic, err)
	}
	if pull == nil || len(pull.Messages) == 0 {
		return out, nil
	}

	// Poison isolation BEFORE dispatching: if the head has already been
	// attempted to the limit, retire it and let the next pass resume. Checking
	// first means a message that panics the dispatch path still gets retired
	// rather than being retried forever.
	if b.MaxAttemptsReached(pull) {
		reason := fmt.Sprintf("dispatch failed %d times", pull.Attempts)
		if err := b.Consumer.DeadLetter(ctx, b.WorkspaceKey, b.Topic, b.Subscriber, b.leaseToken, pull.Head, b.maxAttempts(), reason); err != nil {
			return out, fmt.Errorf("dead-letter head of %q: %w", b.Topic, err)
		}
		out.DeadLettered++
		b.logger().Warn("topic bridge: message retired to dead-letter log",
			"workspace", b.WorkspaceKey, "topic", b.Topic, "subscriber", b.Subscriber,
			"cursor", pull.Head, "attempts", pull.Attempts)
		return out, nil
	}

	// Dispatch in order, remembering the last message whose dispatch succeeded.
	// A failure stops the batch: acking past it would skip it permanently.
	ackTo := ""
	var dispatchErr error
	for _, msg := range pull.Messages {
		if ctx.Err() != nil {
			dispatchErr = ctx.Err()
			break
		}
		if err := b.emit(ctx, msg); err != nil {
			dispatchErr = err
			break
		}
		out.Emitted++
		ackTo = msg.Cursor
	}

	if ackTo != "" {
		if err := b.Consumer.Ack(ctx, b.WorkspaceKey, b.Topic, b.Subscriber, b.leaseToken, from, ackTo); err != nil {
			// The prefix was dispatched but the cursor did not move: those
			// messages redeliver next pass and dedup on their event ids.
			return out, errors.Join(dispatchErr, fmt.Errorf("ack %q through %q: %w", b.Topic, ackTo, err))
		}
		out.Acked = out.Emitted
	}
	return out, dispatchErr
}

// MaxAttemptsReached reports whether the pull's head has exhausted its delivery
// budget. Exported so a caller can surface the same judgment in diagnostics.
func (b *TopicBridge) MaxAttemptsReached(pull *store.TopicPull) bool {
	return pull != nil && pull.Head != "" && pull.Attempts >= b.maxAttempts()
}

// Close releases the drain lease so a peer can take over immediately rather
// than waiting out the TTL. Safe to call without a held lease.
func (b *TopicBridge) Close(ctx context.Context) error {
	if b == nil || b.Consumer == nil || b.leaseToken == "" {
		return nil
	}
	token := b.leaseToken
	b.leaseToken = ""
	return b.Consumer.ReleaseLease(ctx, b.WorkspaceKey, b.Topic, b.Subscriber, token)
}

// emit re-enters one message into the router. A no-listener dispatch
// (domain.ErrNotFound — no binding on this topic's route key) is NOT a failure:
// the message is still acked, because a topic with no binding must not stall
// the subscription forever.
func (b *TopicBridge) emit(ctx context.Context, msg store.TopicMessage) error {
	_, err := b.Source.Emit(ctx, b.WorkspaceKey, b.toInternalEvent(msg))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		b.logger().Debug("topic bridge: no binding for message, acking past it",
			"workspace", b.WorkspaceKey, "topic", b.Topic, "message_id", msg.ID)
		return nil
	default:
		return fmt.Errorf("emit topic message %q on %q: %w", msg.ID, b.Topic, err)
	}
}

// toInternalEvent projects a message onto the loopback event.
//
// EventType comes from the message's Kind, so one topic can carry several event
// types and bindings discriminate with the ordinary pattern glob
// (internal.deploy.* and so on) rather than the bridge inventing a routing
// language. A message with no kind lands on DefaultTopicEventType.
//
// The actor rides through verbatim from trace["actor"] when present — the
// bridge does not synthesize provenance it was not given, because a fabricated
// actor would defeat the binding actor filters that are the self-trigger guard.
func (b *TopicBridge) toInternalEvent(msg store.TopicMessage) InternalEvent {
	eventType := strings.ToLower(strings.TrimSpace(msg.Kind))
	if eventType == "" {
		eventType = DefaultTopicEventType
	}
	subject := strings.TrimSpace(msg.Trace["subject"])
	if subject == "" {
		subject = TopicSubjectRefPrefix + b.Topic
	}
	return InternalEvent{
		EventID:      TopicEventIDPrefix + b.Topic + "-" + msg.ID,
		EventType:    eventType,
		Origin:       domain.TriggerEventOriginSystem,
		ActorRef:     strings.TrimSpace(msg.Trace["actor"]),
		SubjectRef:   subject,
		Payload:      json.RawMessage(msg.Payload),
		SubjectAttrs: topicSubjectAttrs(msg),
	}
}

// topicSubjectAttrs promotes the message's trace map into the adapter-enriched
// attrs lane, so a binding can scope its subject key with {{attrs.<name>}}.
// Trace is already a string map, so it needs no scalar projection — unlike an
// issue snapshot, whose arrays cannot be templated.
func topicSubjectAttrs(msg store.TopicMessage) map[string]string {
	if len(msg.Trace) == 0 {
		return nil
	}
	attrs := make(map[string]string, len(msg.Trace)+1)
	for k, v := range msg.Trace {
		attrs[k] = v
	}
	attrs["topic"] = msg.Topic
	return attrs
}

// owner returns the configured holder identity, defaulting to host:pid so two
// serve processes sharing a subscriber are distinguishable in lease diagnostics
// without either being configured.
//
// The broker validates owner against ^[A-Za-z0-9._:@+-]+$ and REJECTS the
// lease otherwise, so the identity is sanitized rather than passed through: a
// hostname is environment-supplied and can carry characters outside that set
// (a "/" separator alone is enough to fail), and a rejected lease would take
// the whole consumer down for a cosmetic field.
func (b *TopicBridge) owner() string {
	if o := sanitizeOwner(b.Owner); o != "" {
		return o
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	if host = sanitizeOwner(host); host == "" {
		host = "unknown"
	}
	return host + ":" + strconv.Itoa(os.Getpid())
}

// sanitizeOwner maps anything outside the broker's owner charset to "-" so a
// hostile hostname degrades to a usable identity instead of a failed lease.
func sanitizeOwner(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == ':', r == '@', r == '+', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

func (b *TopicBridge) leaseTTL() time.Duration {
	if b.LeaseTTL > 0 {
		return b.LeaseTTL
	}
	return DefaultTopicLeaseTTL
}

func (b *TopicBridge) pullLimit() int {
	if b.PullLimit > 0 {
		return b.PullLimit
	}
	return DefaultTopicPullLimit
}

func (b *TopicBridge) maxAttempts() int {
	if b.MaxAttempts > 0 {
		return b.MaxAttempts
	}
	return DefaultTopicMaxAttempts
}

func (b *TopicBridge) logger() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}
