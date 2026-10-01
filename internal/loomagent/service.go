package loomagent

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// Agent states (design v2 §5.1).
const (
	StateCreating = "creating"
	StateIdle     = "idle"
	StateActive   = "active"
	StateWaiting  = "waiting"
	StateStopping = "stopping"
	StateFinished = "finished"
	StateArchived = "archived"
)

// transitions is the one state machine: the states each state may move to.
// A change that keeps the state (Attention, WaitingOn, turn id) is always allowed.
var transitions = map[string][]string{
	StateCreating: {StateIdle, StateActive, StateFinished, StateStopping},
	StateIdle:     {StateActive, StateStopping},
	StateActive:   {StateIdle, StateWaiting, StateFinished, StateStopping},
	StateWaiting:  {StateActive, StateIdle, StateFinished, StateStopping},
	StateStopping: {StateArchived},
	StateFinished: {StateActive, StateStopping},
	StateArchived: {StateIdle, StateFinished},
}

// Target is the internal environment an agent runs in. Phase 1 supplies only
// local; state transitions never read host-global paths.
type Target string

// TargetLocal is this host.
const TargetLocal Target = "local"

// ResolveRepo maps a repo to its clone for target.
type ResolveRepo func(ctx context.Context, target Target, repo string) (string, error)

// PrepareWorktree stages workspace and role-scoped skills into a's working
// copy for target. The service runs it before every hand-over.
type PrepareWorktree func(ctx context.Context, target Target, a loomstore.Agent) error

// ServiceConfig wires a Service. Target defaults to TargetLocal, ResolveRepo to the
// repo itself, and PrepareWorktree to staging nothing.
type ServiceConfig struct {
	Store           *loomstore.Store
	Events          *EventLog
	Workspace       Workspace
	ResolveRepo     ResolveRepo
	PrepareWorktree PrepareWorktree
	Target          Target
	// Interrupt stops a's running turn; nil when no harness is wired.
	Interrupt func(ctx context.Context, a loomstore.Agent) error
	// Purge removes exactly the native sessions a owns (2.1c's R29 hook); nil
	// until 2.1c wires it. A failure leaves the Delete pending for Reconcile.
	Purge func(ctx context.Context, a loomstore.Agent, owned []loomstore.NativeSession) error
}

// Service is the Agent API service: it owns agent state changes and their
// live events.
type Service struct {
	Bus         *Bus
	store       *loomstore.Store
	events      *EventLog
	workspace   Workspace
	resolveRepo ResolveRepo
	prepare     PrepareWorktree
	target      Target
	interrupt   func(context.Context, loomstore.Agent) error
	purge       func(context.Context, loomstore.Agent, []loomstore.NativeSession) error

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New returns a Service for cfg.
func New(cfg ServiceConfig) *Service {
	s := &Service{Bus: NewBus(), store: cfg.Store, events: cfg.Events, workspace: cfg.Workspace,
		resolveRepo: cfg.ResolveRepo, prepare: cfg.PrepareWorktree, target: cfg.Target,
		interrupt: cfg.Interrupt, purge: cfg.Purge, locks: map[string]*sync.Mutex{}}
	if s.target == "" {
		s.target = TargetLocal
	}
	if s.resolveRepo == nil {
		s.resolveRepo = func(_ context.Context, _ Target, repo string) (string, error) { return repo, nil }
	}
	if s.prepare == nil {
		s.prepare = func(context.Context, Target, loomstore.Agent) error { return nil }
	}
	return s
}

// lock takes agentID's lock, which orders that agent's writes, and returns its unlock.
func (s *Service) lock(agentID string) func() {
	s.mu.Lock()
	l, ok := s.locks[agentID]
	if !ok {
		l = &sync.Mutex{}
		s.locks[agentID] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// repoPath resolves repo for the service's target.
func (s *Service) repoPath(ctx context.Context, repo string) (string, error) {
	return s.resolveRepo(ctx, s.target, repo)
}

// setState moves a to `to` by compare-and-set on a's state columns, then,
// after the commit, publishes the change. It returns loomstore.ErrStateChanged
// when another writer moved a first; that writer alone publishes.
func (s *Service) setState(ctx context.Context, a loomstore.Agent, to loomstore.AgentState) (loomstore.Agent, error) {
	from := a.StateOf()
	if to.State != from.State && !slices.Contains(transitions[from.State], to.State) {
		return a, fmt.Errorf("loomagent: invalid state change %s -> %s", from.State, to.State)
	}
	if err := s.store.CompareAndSetState(ctx, a.AgentID, from, to); err != nil {
		return a, err
	}
	before := a
	a.State, a.StateReason, a.WaitingOn, a.Outcome = to.State, to.StateReason, to.WaitingOn, to.Outcome
	a.AttentionReason, a.RunningTurnID, a.Attempt = to.AttentionReason, to.RunningTurn, to.Attempt
	s.publishChange(before, a)
	return a, nil
}

// raiseAttention sets Attention{reason} beside a's state.
func (s *Service) raiseAttention(ctx context.Context, a loomstore.Agent, reason string) (loomstore.Agent, error) {
	to := a.StateOf()
	to.AttentionReason = &reason
	return s.setState(ctx, a, to)
}

// clearAttention clears a's Attention.
func (s *Service) clearAttention(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	to := a.StateOf()
	to.AttentionReason = nil
	return s.setState(ctx, a, to)
}

// handOver stages skills with PrepareWorktree, then hands over a's next
// waiting slot. Nothing is handed over when staging fails.
func (s *Service) handOver(ctx context.Context, a loomstore.Agent, nativeKey func(loomstore.Slot) string) (loomstore.Slot, error) {
	if err := s.prepare(ctx, s.target, a); err != nil {
		return loomstore.Slot{}, err
	}
	return s.store.HandNext(ctx, a.AgentID, nativeKey)
}

func (s *Service) publishChange(before, after loomstore.Agent) {
	e := Event{AgentID: after.AgentID, Time: time.Now()}
	if before.State != after.State {
		c := e
		c.Type, c.From, c.To, c.Reason = EventStateChanged, before.State, after.State, deref(after.StateReason)
		s.Bus.publish(c)
	}
	if deref(before.AttentionReason) != deref(after.AttentionReason) {
		c := e
		c.Type, c.Reason = EventAttentionRaised, deref(after.AttentionReason)
		if after.AttentionReason == nil {
			c.Type, c.Reason = EventAttentionCleared, deref(before.AttentionReason)
		}
		s.Bus.publish(c)
	}
	if after.Mode == "persistent" && after.State == StateIdle &&
		(before.State == StateActive || before.State == StateWaiting) {
		c := e
		c.Type, c.TurnID = EventIdle, deref(before.RunningTurnID)
		s.Bus.publish(c)
	}
	if r := settledReason(after); r != "" && settledReason(before) == "" {
		c := e
		c.Type, c.Reason, c.Outcome, c.Attempt = EventSettled, r, deref(after.Outcome), after.Attempt
		s.Bus.publish(c)
	}
}

// settledReason is why a is settled (§5.1), or "" when it is not.
func settledReason(a loomstore.Agent) string {
	switch {
	case a.DeletedAt != nil:
		return "deleted"
	case a.State == StateArchived:
		return "archived"
	case a.State == StateFinished:
		return "finished"
	case a.AttentionReason != nil:
		return "attention"
	}
	return ""
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
