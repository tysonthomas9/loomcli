package store

import (
	"context"
	"encoding/json"
	"time"
)

// TopicMessage is one message on a fleet-db pub/sub topic.
//
// Cursor is the backend-assigned position and an OPAQUE round-trip token:
// clients must not parse, compare or order it, and a cursor from one backend
// (Redis "<ms>-<seq>") is meaningless on another (Postgres "<seq>-0"). "0"
// means beginning-of-stream; "$" means the tip and is follow-only.
//
// ID is the per-message identity the broker dedups publishes on, so a
// re-publish under the same id collapses onto the stored message.
type TopicMessage struct {
	ID      string
	Cursor  string
	Topic   string
	TS      time.Time
	Kind    string
	Trace   map[string]string
	Payload json.RawMessage
}

// TopicPublish is one publish request. Payload is required; ID makes the
// publish idempotent when the caller can derive a stable one.
type TopicPublish struct {
	ID      string
	Kind    string
	Trace   map[string]string
	Payload json.RawMessage
}

// TopicPull is one durable-consume read. The lease token is the capability
// that authorizes advancing the subscription's cursor — see TopicConsumer.
type TopicPull struct {
	Messages []TopicMessage
	// Head is the cursor of the message at the head of the subscription, the
	// value DeadLetter must be given as expectedCursor to skip it.
	Head string
	// Attempts is the broker-tracked delivery count for the current head,
	// bumped on each re-pull and reset on ack. It is what makes poison
	// isolation survive a consumer crash: the count lives on the broker, not
	// in the consumer's memory.
	Attempts int
	// Cursor is the ack-THROUGH position for a fully-handled batch — where the
	// cursor is going, NOT where it is. It is not the CAS guard for Ack; that
	// is SubscriptionCursor. Conflating the two is a cursor-conflict on every
	// ack.
	Cursor string
}

// TopicConsumer is the OPTIONAL store capability for fleet-db's pub/sub bus
// (detected by type assertion, like IssueJournalReader): a durable,
// single-ordered-consumer work queue over a topic.
//
// THE CONSUME CONTRACT, which callers must honor and which the bus enforces:
//
//  1. SINGLE ACTIVE CONSUMER — AcquireLease returns a token; every
//     cursor-mutating op CAS-checks it, so two consumers sharing a subscriber
//     name cannot both drain. A crashed holder's lease expires and another
//     consumer resumes from the durable cursor.
//  2. AT-LEAST-ONCE — the cursor advances only on Ack, so a crash or lease
//     takeover redelivers from the last acked position. CONSUMERS MUST BE
//     IDEMPOTENT.
//  3. HEAD-OF-LINE, NO SKIP — Ack advances only if the stored cursor still
//     equals `from` (a CAS). Handle in order, pause on failure, and ack only
//     the contiguous successful prefix.
//  4. POISON ISOLATION — when Attempts reaches the consumer's limit,
//     DeadLetter moves that one head message aside and advances by one.
//     Without it a message that always fails blocks the subscription forever.
type TopicConsumer interface {
	// EnsureSubscription creates the durable per-(topic, subscriber) cursor if
	// it does not exist. startCursor "" defaults to the beginning; "$" starts
	// at the tip, skipping everything already published.
	EnsureSubscription(ctx context.Context, ws, topic, subscriber, startCursor string) error
	// AcquireLease takes (or renews, when token is non-empty) the drain lease.
	// acquired=false means another consumer holds it — back off, do not drain.
	//
	// owner names the HOLDER (required by the broker, surfaced as lease_owner
	// on the subscriptions listing) and is distinct from subscriber, which
	// names the durable CURSOR: two processes draining one subscription share
	// a subscriber and differ by owner, which is what makes "who holds the
	// drain right now?" answerable. The token, not the owner, is the
	// capability.
	AcquireLease(ctx context.Context, ws, topic, subscriber, owner, token string, ttl time.Duration) (newToken string, acquired bool, err error)
	// ReleaseLease drops the lease so another consumer can take over promptly
	// instead of waiting out the TTL.
	ReleaseLease(ctx context.Context, ws, topic, subscriber, token string) error
	// Pull reads up to limit messages from the subscription's cursor without
	// advancing it. timeout > 0 long-polls for new messages.
	Pull(ctx context.Context, ws, topic, subscriber, token string, limit int, timeout time.Duration) (*TopicPull, error)
	// SubscriptionCursor reports the subscription's current durable cursor —
	// the CAS guard Ack needs as `from`. It is a separate read because the pull
	// response deliberately does not carry it: pull returns the ack-THROUGH
	// target for a fully-handled batch, which is where the cursor is going, not
	// where it is. found=false means the subscription has no stored cursor yet,
	// which the broker treats as "0".
	SubscriptionCursor(ctx context.Context, ws, topic, subscriber string) (cursor string, found bool, err error)
	// Ack advances the cursor from `from` to `position` (CAS on `from`).
	Ack(ctx context.Context, ws, topic, subscriber, token, from, position string) error
	// DeadLetter moves the head message (which must equal expectedCursor) to
	// the dead-letter log once it has been attempted maxAttempts times, then
	// advances past it.
	DeadLetter(ctx context.Context, ws, topic, subscriber, token, expectedCursor string, maxAttempts int, reason string) error
}

// TopicPublisher is the OPTIONAL broadcast half of the bus: append and
// non-acknowledged read. Split from TopicConsumer because they are distinct
// trust levels — publishing is privileged, tailing is read-only — and a
// caller that only needs one should not have to satisfy the other.
type TopicPublisher interface {
	// Publish appends a message and returns it with its assigned cursor.
	Publish(ctx context.Context, ws, topic string, msg TopicPublish) (*TopicMessage, error)
	// Read is a one-shot, non-acknowledged read after `from` (exclusive).
	// "$" is rejected — it is meaningful only when following.
	Read(ctx context.Context, ws, topic, from string, limit int, timeout time.Duration) ([]TopicMessage, string, bool, error)
}
