package fleetdb

// fleet-db pub/sub client: the broadcast half (publish/read) and the durable
// consume half (lease/pull/ack/dead-letter) of /api/v1/{ws}/topics.
//
// Served by the fleet-db backend only — memstore deliberately omits it, so the
// capability is detected by type assertion exactly like IssueJournalReader.
// It rides on triggerEventStore for the same reason that one does: the store
// handle callers already reach for when they want event transport.
//
// CURSORS ARE OPAQUE. Every cursor here round-trips verbatim. The client never
// parses, compares or synthesizes one — Redis renders "<ms>-<seq>" and Postgres
// "<seq>-0", so any client-side ordering logic would be backend-specific and
// silently wrong on the other.
//
// THE LEASE TOKEN IS A CAPABILITY, not an identifier: it travels in the
// X-Lease-Token header and the broker CAS-checks it on every cursor-mutating
// op. Losing it means losing the right to advance the cursor, which is what
// keeps two consumers from both draining one subscription.

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	_ store.TopicConsumer  = (*triggerEventStore)(nil)
	_ store.TopicPublisher = (*triggerEventStore)(nil)
)

// leaseTokenHeader carries the drain-lease capability on cursor-mutating ops.
const leaseTokenHeader = "X-Lease-Token" //nolint:gosec // G101: an HTTP header NAME, not a credential (mirrors fleet-db's waiver on the same constant)

// topicMessageWire mirrors fleet-db's MessageResponse (api/pubsub.go).
type topicMessageWire struct {
	ID      string            `json:"id"`
	Cursor  string            `json:"cursor"`
	Topic   string            `json:"topic"`
	TS      time.Time         `json:"ts"`
	Kind    string            `json:"kind,omitempty"`
	Trace   map[string]string `json:"trace,omitempty"`
	Payload json.RawMessage   `json:"payload"`
}

func (m topicMessageWire) toStore() store.TopicMessage {
	return store.TopicMessage{
		ID: m.ID, Cursor: m.Cursor, Topic: m.Topic, TS: m.TS,
		Kind: m.Kind, Trace: m.Trace, Payload: m.Payload,
	}
}

func topicMessages(wire []topicMessageWire) []store.TopicMessage {
	out := make([]store.TopicMessage, 0, len(wire))
	for _, m := range wire {
		out = append(out, m.toStore())
	}
	return out
}

func topicPath(ws, topic string, suffix ...string) string {
	p := "/api/v1/" + pathEscape(ws) + "/topics/" + pathEscape(topic)
	for _, s := range suffix {
		p += "/" + s
	}
	return p
}

func subscriptionPath(ws, topic, subscriber string, suffix ...string) string {
	p := topicPath(ws, topic, "subscriptions") + "/" + pathEscape(subscriber)
	for _, s := range suffix {
		p += "/" + s
	}
	return p
}

// Publish appends a message. A supplied ID makes the publish idempotent: the
// broker collapses a re-publish onto the stored message rather than appending
// a duplicate, which is what lets a crashed publisher retry safely.
func (s *triggerEventStore) Publish(ctx context.Context, ws, topic string, msg store.TopicPublish) (*store.TopicMessage, error) {
	body := map[string]any{"payload": msg.Payload}
	if msg.ID != "" {
		body["id"] = msg.ID
	}
	if msg.Kind != "" {
		body["kind"] = msg.Kind
	}
	if len(msg.Trace) > 0 {
		body["trace"] = msg.Trace
	}
	var resp topicMessageWire
	if err := s.client.do(ctx, "POST", topicPath(ws, topic, "messages"), body, &resp); err != nil {
		return nil, err
	}
	out := resp.toStore()
	return &out, nil
}

// Read is the non-acknowledged tail: it never advances any subscription, so
// two readers do not compete. Use it for displays and audits; use the consume
// path for work that must not be dropped.
func (s *triggerEventStore) Read(ctx context.Context, ws, topic, from string, limit int, timeout time.Duration) ([]store.TopicMessage, string, bool, error) {
	q := url.Values{}
	if from == "" {
		from = "0"
	}
	q.Set("from", from)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if timeout > 0 {
		q.Set("timeout", strconv.FormatInt(timeout.Milliseconds(), 10))
	}
	var resp struct {
		Messages []topicMessageWire `json:"messages"`
		Cursor   string             `json:"cursor"`
		HasMore  bool               `json:"has_more"`
	}
	if err := s.client.do(ctx, "GET", withQuery(topicPath(ws, topic, "messages"), q), nil, &resp); err != nil {
		return nil, "", false, err
	}
	next := resp.Cursor
	if next == "" {
		next = from
	}
	return topicMessages(resp.Messages), next, resp.HasMore, nil
}

// EnsureSubscription is idempotent, so a consumer can call it on every startup
// without branching on whether it has run before.
func (s *triggerEventStore) EnsureSubscription(ctx context.Context, ws, topic, subscriber, startCursor string) error {
	body := map[string]any{}
	if startCursor != "" {
		body["start_cursor"] = startCursor
	}
	return s.client.do(ctx, "PUT", subscriptionPath(ws, topic, subscriber), body, nil)
}

// AcquireLease takes the drain lease, or renews it when token is non-empty.
//
// acquired=false is NOT an error: it is the normal answer when another
// consumer holds the lease, and the caller must back off rather than drain.
// Conflating the two would turn routine contention into a failure.
func (s *triggerEventStore) AcquireLease(ctx context.Context, ws, topic, subscriber, owner, token string, ttl time.Duration) (string, bool, error) {
	body := map[string]any{"owner": owner}
	if ttl > 0 {
		body["ttl_seconds"] = int(ttl.Seconds())
	}
	headers := map[string]string{}
	if token != "" {
		headers[leaseTokenHeader] = token
	}
	var resp struct {
		Token    string `json:"token,omitempty"`
		Acquired bool   `json:"acquired"`
		Renewed  bool   `json:"renewed"`
	}
	if err := s.client.doWithHeaders(ctx, "POST", subscriptionPath(ws, topic, subscriber, "lease"), body, &resp, headers); err != nil {
		return "", false, err
	}
	// A renew answers {"renewed":true} with no token — the caller keeps the one
	// it already holds.
	if resp.Renewed && resp.Token == "" {
		return token, true, nil
	}
	return resp.Token, resp.Acquired, nil
}

func (s *triggerEventStore) ReleaseLease(ctx context.Context, ws, topic, subscriber, token string) error {
	headers := map[string]string{}
	if token != "" {
		headers[leaseTokenHeader] = token
	}
	return s.client.doWithHeaders(ctx, "DELETE", subscriptionPath(ws, topic, subscriber, "lease"), nil, nil, headers)
}

// Pull reads without advancing the cursor. Attempts on the response is the
// BROKER's count for the current head, not a local one, so it survives this
// consumer crashing and another taking over — that is what makes the
// dead-letter threshold meaningful rather than per-process.
func (s *triggerEventStore) Pull(ctx context.Context, ws, topic, subscriber, token string, limit int, timeout time.Duration) (*store.TopicPull, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if timeout > 0 {
		q.Set("timeout", strconv.FormatInt(timeout.Milliseconds(), 10))
	}
	var resp struct {
		Messages []topicMessageWire `json:"messages"`
		Head     string             `json:"head,omitempty"`
		Attempts int                `json:"attempts"`
		Cursor   string             `json:"cursor"`
	}
	path := withQuery(subscriptionPath(ws, topic, subscriber, "messages"), q)
	headers := map[string]string{leaseTokenHeader: token}
	if err := s.client.doWithHeaders(ctx, "GET", path, nil, &resp, headers); err != nil {
		return nil, err
	}
	return &store.TopicPull{
		Messages: topicMessages(resp.Messages),
		Head:     resp.Head,
		Attempts: resp.Attempts,
		Cursor:   resp.Cursor,
	}, nil
}

// SubscriptionCursor reads the subscription's stored cursor off the topic's
// subscriptions listing. The listing is per-topic, so this filters by
// subscriber name; a subscriber with no row has never been created and reports
// found=false rather than an error.
func (s *triggerEventStore) SubscriptionCursor(ctx context.Context, ws, topic, subscriber string) (string, bool, error) {
	var resp struct {
		Subscriptions []struct {
			Subscriber string `json:"subscriber"`
			Cursor     string `json:"cursor"`
		} `json:"subscriptions"`
	}
	if err := s.client.do(ctx, "GET", topicPath(ws, topic, "subscriptions"), nil, &resp); err != nil {
		return "", false, err
	}
	for _, sub := range resp.Subscriptions {
		if sub.Subscriber == subscriber {
			return sub.Cursor, true, nil
		}
	}
	return "", false, nil
}

// Ack advances the cursor. `from` is the CAS guard: the broker rejects the
// advance if the stored cursor has moved, so a stale consumer cannot skip
// messages a newer one already owns.
func (s *triggerEventStore) Ack(ctx context.Context, ws, topic, subscriber, token, from, position string) error {
	body := map[string]any{"from": from, "position": position}
	headers := map[string]string{leaseTokenHeader: token}
	return s.client.doWithHeaders(ctx, "POST", subscriptionPath(ws, topic, subscriber, "ack"), body, nil, headers)
}

// DeadLetter retires the head message after maxAttempts and advances by one.
// expectedCursor guards against retiring the wrong message when the head has
// moved underneath the caller.
func (s *triggerEventStore) DeadLetter(ctx context.Context, ws, topic, subscriber, token, expectedCursor string, maxAttempts int, reason string) error {
	body := map[string]any{
		"expected_cursor": expectedCursor,
		"max_attempts":    maxAttempts,
		"reason":          reason,
	}
	headers := map[string]string{leaseTokenHeader: token}
	return s.client.doWithHeaders(ctx, "POST", subscriptionPath(ws, topic, subscriber, "dead-letter-next"), body, nil, headers)
}
