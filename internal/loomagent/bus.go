package loomagent

import (
	"sync"
	"time"
)

// Live event types the service publishes (design v2 §5.2).
const (
	EventStateChanged     = "agent.state_changed"
	EventIdle             = "agent.idle"
	EventSettled          = "agent.settled"
	EventAttentionRaised  = "attention.raised"
	EventAttentionCleared = "attention.cleared"
	EventArchived         = "agent.archived" // Reason: done | cancelled
	EventDeleted          = "agent.deleted"
	EventWithdrawn        = "message.withdrawn" // Reason: the sender
)

// Event is one service event. It is saved in agent_events before the Bus
// publishes it (emit); the Bus is delivery only (§4.11, §5.2).
type Event struct {
	AgentID string    `json:"agentId"`
	Type    string    `json:"type"`
	Time    time.Time `json:"time"`
	From    string    `json:"from,omitempty"`
	To      string    `json:"to,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	TurnID  string    `json:"turnId,omitempty"`  // agent.idle: the turn that ended
	Outcome string    `json:"outcome,omitempty"` // agent.settled: a single task's outcome
	Attempt int64     `json:"attempt,omitempty"`
}

const busBuffer = 256

// Bus fans live events out to subscribers. Each subscriber has a bounded
// queue; one that falls behind is closed with subscriber_lagged, and
// publishing never blocks.
type Bus struct {
	mu   sync.Mutex
	subs map[*BusSubscription]struct{}
}

// BusSubscription receives events for its agents (all agents when none were named).
type BusSubscription struct {
	C      <-chan Event // closed when the subscription ends; then read Err
	ch     chan Event
	agents map[string]bool
	err    error
}

// Err reports why C closed: nil after Unsubscribe, or a subscriber_lagged *Error.
func (s *BusSubscription) Err() error { return s.err }

// NewBus returns an empty bus.
func NewBus() *Bus { return &Bus{subs: map[*BusSubscription]struct{}{}} }

// Subscribe follows agentIDs, or every agent when none are given.
func (b *Bus) Subscribe(agentIDs ...string) *BusSubscription {
	s := &BusSubscription{ch: make(chan Event, busBuffer), agents: map[string]bool{}}
	s.C = s.ch
	for _, id := range agentIDs {
		s.agents[id] = true
	}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Unsubscribe ends s.
func (b *Bus) Unsubscribe(s *BusSubscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.end(s, nil)
}

func (b *Bus) publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		if len(s.agents) > 0 && !s.agents[e.AgentID] {
			continue
		}
		select {
		case s.ch <- e:
		default:
			b.end(s, &Error{Code: CodeSubscriberLagged, Message: "subscriber fell too far behind"})
		}
	}
}

// end closes s once; b.mu must be held.
func (b *Bus) end(s *BusSubscription, err error) {
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		s.err = err
		close(s.ch)
	}
}
