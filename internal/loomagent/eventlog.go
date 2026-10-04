// Package loomagent is the Agent API service inside `loom serve`.
package loomagent

import (
	"context"
	"math"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// ErrSlowSubscriber ends a Subscription whose live buffer filled. The client
// reconnects from the last Seq it received.
var ErrSlowSubscriber error = &Error{Code: CodeSubscriberLagged, Message: "subscriber too slow; reconnect from the last seq"}

// LiveOnly as a Subscribe cursor skips replay and starts at the current last seq.
const LiveOnly int64 = -1

const subscriberBuffer = 256

// EventLog is the one reader and writer of agent_events. Append commits a row
// before publishing it; Subscribe replays committed rows after a cursor and
// then joins live publication with no gap or duplicate; Page is the public
// history read for every harness (design v2 §4.11, §4.12).
type EventLog struct {
	store *loomstore.Store
	mu    sync.Mutex // orders commit+publish against subscriber registration
	subs  map[*Subscription]struct{}
}

// NewEventLog returns the event log over store.
func NewEventLog(store *loomstore.Store) *EventLog {
	return &EventLog{store: store, subs: map[*Subscription]struct{}{}}
}

// Append commits e, then publishes the stored row. An EventID the agent
// already has returns the stored row; subscribers never see it twice.
func (l *EventLog) Append(ctx context.Context, e loomstore.Event) (loomstore.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	got, err := l.store.AppendEvent(ctx, e)
	if err != nil {
		return got, err
	}
	l.fanout(got)
	return got, nil
}

// appendAllCrash runs between an AppendAll's commit and its publication;
// tests crash there.
var appendAllCrash = func() {}

// commitStateCrash runs between a CommitState's commit and its publication;
// tests crash there.
var commitStateCrash = func() {}

// CommitState is the ordered atomic write of agentID's state change. It takes
// the lane before the transaction begins and holds it until the fanout ends,
// so commit order is publish order: in one transaction it compares and sets
// the row from (from, rev) to `to`, bumping the revision, and saves events
// (loomstore.CommitState); only after the commit does it publish them. If any
// write or the commit fails, nothing is saved or published.
func (l *EventLog) CommitState(ctx context.Context, agentID string, from, to loomstore.AgentState, rev int64,
	events []loomstore.Event) ([]loomstore.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	saved, err := l.store.CommitState(ctx, agentID, from, to, rev, events)
	if err != nil {
		return nil, err
	}
	commitStateCrash() // committed, not yet published
	for _, e := range saved {
		l.fanout(e)
	}
	return saved, nil
}

// AppendAll appends events to agentID in one short transaction and
// publishes the new ones, in order, only after the commit: if any write or
// the commit fails, nothing is saved or published.
func (l *EventLog) AppendAll(ctx context.Context, agentID string, events []loomstore.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	before, err := l.store.AppendEvents(ctx, agentID, events)
	if err != nil {
		return err
	}
	appendAllCrash() // committed, not yet published or applied
	q := loomstore.EventQuery{AgentID: agentID, After: before}
	for {
		p, err := l.store.ListEvents(ctx, q)
		if err != nil { // saved but not all published: its subscribers reconnect from their cursors
			for s := range l.subs {
				if s.agents[agentID] {
					delete(l.subs, s)
					close(s.live)
				}
			}
			return err
		}
		for _, e := range p.Events {
			l.fanout(e)
		}
		if !p.More {
			return nil
		}
		q.After, q.Snapshot = p.Next, p.SnapshotSeq
	}
}

// fanout hands a committed event to its agent's live subscribers, under l.mu.
func (l *EventLog) fanout(got loomstore.Event) {
	for s := range l.subs {
		if !s.agents[got.AgentID] {
			continue
		}
		select {
		case s.live <- got:
		default:
			delete(l.subs, s)
			close(s.live)
		}
	}
}

// Page reads one snapshot-pinned page of an agent's committed events.
func (l *EventLog) Page(ctx context.Context, q loomstore.EventQuery) (loomstore.EventPage, error) {
	return l.store.ListEvents(ctx, q)
}

// Backfill reads a session's native transcript to the end and appends each
// event toEvent maps (completed items only; ok=false skips one). Mapped
// EventIDs come from native ids, so items the live feed already logged are
// not added again. Reconcile runs it before declaring the log caught up.
func (l *EventLog) Backfill(ctx context.Context, s loomharness.Session,
	toEvent func(loomharness.Event) (loomstore.Event, bool)) error {
	after := ""
	for {
		page, err := s.Messages(ctx, after, 100)
		if err != nil {
			return err
		}
		for _, ne := range page.Events {
			if e, ok := toEvent(ne); ok {
				if _, err := l.Append(ctx, e); err != nil {
					return err
				}
			}
		}
		if page.Next == "" {
			return nil
		}
		after = page.Next
	}
}

// Subscription delivers each subscribed agent's events in seq order: first
// the committed rows after its cursor, then live rows. A Service.Subscribe
// subscription also gets live-only notices (Seq 0): deltas if it asked for
// them, tool starts if it asked for deltas or named tool.started, and
// feed.gap; and only the saved kinds it asked for.
type Subscription struct {
	C      <-chan loomstore.Event // closed when the subscription ends; then read Err
	out    chan loomstore.Event
	live   chan loomstore.Event
	agents map[string]bool  // fixed at Subscribe; read by Append
	cursor map[string]int64 // last seq delivered per agent; owned by run
	err    error
	notes  bool            // gets Notify's live-only notices
	deltas bool            // of those, deltas too
	kinds  map[string]bool // saved kinds to deliver; empty means all
}

// Notify sends a live-only notice (Seq 0, never saved) to subscriptions that
// take them: a delta or tool start to its agent's, a feed.gap (AgentID "")
// to all.
func (l *EventLog) Notify(e loomstore.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for s := range l.subs {
		if !s.notes || (e.AgentID != "" && !s.agents[e.AgentID]) || (e.Kind == KindDelta && !s.deltas) ||
			(e.Kind == KindToolStarted && !s.deltas && !s.kinds[KindToolStarted]) {
			continue
		}
		select {
		case s.live <- e:
		default:
			delete(l.subs, s)
			close(s.live)
		}
	}
}

// Err reports why C closed: the context's error or ErrSlowSubscriber.
func (s *Subscription) Err() error { return s.err }

// Subscribe follows the agents in cursors, each from its cursor seq (0 for
// all history, LiveOnly for new events only). It registers for live rows
// before reading history, so every row is either replayed or live; rows
// already delivered are dropped by seq.
func (l *EventLog) Subscribe(ctx context.Context, cursors map[string]int64) (*Subscription, error) {
	return l.subscribe(ctx, cursors, &Subscription{})
}

func (l *EventLog) subscribe(ctx context.Context, cursors map[string]int64, s *Subscription) (*Subscription, error) {
	s.out, s.live = make(chan loomstore.Event), make(chan loomstore.Event, subscriberBuffer)
	s.agents, s.cursor = map[string]bool{}, map[string]int64{}
	s.C = s.out
	l.mu.Lock()
	for id, c := range cursors {
		if c == LiveOnly {
			p, err := l.store.ListEvents(ctx, loomstore.EventQuery{AgentID: id, After: math.MaxInt64})
			if err != nil {
				l.mu.Unlock()
				return nil, err
			}
			c = p.SnapshotSeq
		}
		s.agents[id], s.cursor[id] = true, c
	}
	l.subs[s] = struct{}{}
	l.mu.Unlock()
	go func() {
		defer close(s.out)
		defer l.drop(s)
		s.err = s.run(ctx, l)
	}()
	return s, nil
}

func (l *EventLog) drop(s *Subscription) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.subs[s]; ok {
		delete(l.subs, s)
		close(s.live)
	}
}

func (s *Subscription) run(ctx context.Context, l *EventLog) error {
	for id := range s.agents {
		q := loomstore.EventQuery{AgentID: id, After: s.cursor[id]}
		for {
			p, err := l.store.ListEvents(ctx, q)
			if err != nil {
				return err
			}
			for _, e := range p.Events {
				if err := s.send(ctx, e); err != nil {
					return err
				}
			}
			if !p.More {
				break
			}
			q.After, q.Snapshot = p.Next, p.SnapshotSeq
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-s.live:
			if !ok {
				return ErrSlowSubscriber
			}
			if err := s.send(ctx, e); err != nil {
				return err
			}
		}
	}
}

// send delivers e unless its seq was already delivered for that agent or
// its kind is filtered out. A live-only notice (Seq 0) is always sent.
func (s *Subscription) send(ctx context.Context, e loomstore.Event) error {
	if e.Seq != 0 {
		if e.Seq <= s.cursor[e.AgentID] {
			return nil
		}
		s.cursor[e.AgentID] = e.Seq
		if len(s.kinds) > 0 && !s.kinds[e.Kind] {
			return nil
		}
	}
	select {
	case s.out <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
