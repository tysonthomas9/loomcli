// Package loomagent is the Agent API service inside `loom serve`.
package loomagent

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// ErrSlowSubscriber ends a Subscription whose live buffer filled. The client
// reconnects from the last Seq it received.
var ErrSlowSubscriber = errors.New("loomagent: subscriber too slow; reconnect from the last seq")

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
	return got, nil
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
// the committed rows after its cursor, then live rows.
type Subscription struct {
	C      <-chan loomstore.Event // closed when the subscription ends; then read Err
	out    chan loomstore.Event
	live   chan loomstore.Event
	agents map[string]bool  // fixed at Subscribe; read by Append
	cursor map[string]int64 // last seq delivered per agent; owned by run
	err    error
}

// Err reports why C closed: the context's error or ErrSlowSubscriber.
func (s *Subscription) Err() error { return s.err }

// Subscribe follows the agents in cursors, each from its cursor seq (0 for
// all history, LiveOnly for new events only). It registers for live rows
// before reading history, so every row is either replayed or live; rows
// already delivered are dropped by seq.
func (l *EventLog) Subscribe(ctx context.Context, cursors map[string]int64) (*Subscription, error) {
	s := &Subscription{out: make(chan loomstore.Event), live: make(chan loomstore.Event, subscriberBuffer),
		agents: map[string]bool{}, cursor: map[string]int64{}}
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

// send delivers e unless its seq was already delivered for that agent.
func (s *Subscription) send(ctx context.Context, e loomstore.Event) error {
	if e.Seq <= s.cursor[e.AgentID] {
		return nil
	}
	select {
	case s.out <- e:
		s.cursor[e.AgentID] = e.Seq
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
