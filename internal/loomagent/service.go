package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
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
	// Interrupt stops a's running turn; nil uses the current session's own
	// Interrupt, the same call on every harness.
	Interrupt func(ctx context.Context, a loomstore.Agent) error
	// Purge removes exactly the native sessions a owns (R29); nil purges each
	// through its recorded harness. A failure leaves the Delete pending for
	// Reconcile.
	Purge func(ctx context.Context, a loomstore.Agent, owned []loomstore.NativeSession) error
	// Harnesses are the wired harness runtimes by name.
	Harnesses map[string]loomharness.Harness
	// Launch returns a's opaque launch input on harness; nil launches with none.
	Launch func(ctx context.Context, a loomstore.Agent, harness string) (loomharness.Launch, error)
	// Retire runs once a is archived or deleted, to remove what Launch left
	// at rest (its bridge settings); the next Resume after an Unarchive
	// launches it again. It must be safe to repeat; nil does nothing.
	Retire func(ctx context.Context, a loomstore.Agent) error
	// CatalogWarmUp is how long after this service first lists a harness's
	// models a create naming a model the catalog lacks re-fetches it: a
	// harness that just started may list only some providers (MC1). Zero
	// refuses the missing model at once.
	CatalogWarmUp time.Duration
	// WorkspaceID is the workspace this service creates agents in.
	WorkspaceID string
	// Presets defaults to BuiltinPresets.
	Presets Presets
	// DefaultBackend reads the workspace default harness and model
	// (/config/backend); nil has none.
	DefaultBackend func(ctx context.Context) (Backend, error)
	// Bridge returns the capabilities the host bridge currently registers for
	// a preset. It errors when the preset needs bridge wiring that is absent,
	// or the bridge is down; the agent then does not launch. nil registers
	// none. Loom never takes capabilities from a request or a stored row.
	Bridge func(ctx context.Context, p Preset) (BridgeCaps, error)
	// InputKey derives the native input key the dispatcher prompts with from
	// the AgentID and the Send's RequestID, per harness (design v2 §4.9); the
	// same message always gets the same key, so HasInput can find it after a
	// crash. nil uses OpenCode's msg_ form.
	InputKey func(harness, agentID, requestID string) string
	// RecoverFirst holds every write and the dispatcher until RunDispatcher's
	// first Reconcile of each wired harness has finished (a serve start,
	// design v2 §4.14).
	RecoverFirst bool
}

// Backend is a workspace default harness and model.
type Backend struct{ Harness, Model string }

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
	harnesses   map[string]loomharness.Harness
	launch      func(context.Context, loomstore.Agent, string) (loomharness.Launch, error)
	retire      func(context.Context, loomstore.Agent) error
	workspaceID string
	presets     Presets
	backend     func(context.Context) (Backend, error)
	bridge      func(context.Context, Preset) (BridgeCaps, error)
	inputKey    func(harness, agentID, requestID string) string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	asks  map[string]map[string]Ask // open asks by agent and ask ID, under mu
	// resumed holds, by harness, the sessions this process opened or
	// resumed, under mu: Reconcile resumes each live session once, so its
	// policy is installed here, and again after its harness restarts.
	resumed map[string]map[loomharness.NativeRef]bool
	// ready closes when start-up recovery is done (nil: no gate); recovered closes it.
	ready     chan struct{}
	recovered func()
	// owed is set when a task_completed record could not be saved; the
	// dispatcher retries the sweep while it is set.
	owed atomic.Bool
	// catalogWait bounds how long a create waits for a harness's model
	// catalog to load after it boots, polling every catalogPoll (MC1).
	catalogWait, catalogPoll time.Duration
	catalogWarmUp            time.Duration
	listed                   map[string]time.Time // by harness, the first successful catalog listing, under mu
	// loops are the running background loops Drain waits on, in the order
	// they started, under mu.
	loops []*loop
	// stoppedWork sums the items stopped loops handled after their last
	// Drain answer, under mu.
	stoppedWork int
	// tick is the dispatcher's completion-retry clock and after RunFeed's
	// backoff timer: time's own, or a test's.
	tick  ticker
	after func(time.Duration) <-chan time.Time
}

// New returns a Service for cfg.
func New(cfg ServiceConfig) *Service {
	s := &Service{Bus: NewBus(), store: cfg.Store, events: cfg.Events, workspace: cfg.Workspace,
		resolveRepo: cfg.ResolveRepo, prepare: cfg.PrepareWorktree, target: cfg.Target,
		interrupt: cfg.Interrupt, purge: cfg.Purge, harnesses: cfg.Harnesses, launch: cfg.Launch,
		retire: cfg.Retire, workspaceID: cfg.WorkspaceID, presets: cfg.Presets, backend: cfg.DefaultBackend, bridge: cfg.Bridge,
		inputKey: cfg.InputKey, catalogWait: 15 * time.Second, catalogPoll: 250 * time.Millisecond,
		catalogWarmUp: cfg.CatalogWarmUp, listed: map[string]time.Time{}, tick: realTicker, after: time.After,
		locks: map[string]*sync.Mutex{}, asks: map[string]map[string]Ask{}, resumed: map[string]map[loomharness.NativeRef]bool{}}
	if cfg.RecoverFirst {
		s.ready = make(chan struct{})
		s.recovered = sync.OnceFunc(func() { close(s.ready) })
	}
	if s.presets == nil {
		s.presets = BuiltinPresets{}
	}
	if s.interrupt == nil {
		s.interrupt = s.sessionInterrupt
	}
	if s.purge == nil {
		s.purge = s.purgeOwned
	}
	if s.backend == nil {
		s.backend = func(context.Context) (Backend, error) { return Backend{}, nil }
	}
	if s.bridge == nil {
		s.bridge = noBridge
	}
	if s.events == nil {
		s.events = NewEventLog(cfg.Store)
	}
	if s.inputKey == nil {
		s.inputKey = defaultInputKey
	}
	if s.target == "" {
		s.target = TargetLocal
	}
	if s.resolveRepo == nil {
		s.resolveRepo = func(_ context.Context, _ Target, repo string) (string, error) { return repo, nil }
	}
	if s.launch == nil {
		s.launch = func(context.Context, loomstore.Agent, string) (loomharness.Launch, error) {
			return loomharness.Launch{}, nil
		}
	}
	if s.prepare == nil {
		s.prepare = func(context.Context, Target, loomstore.Agent) error { return nil }
	}
	return s
}

// gateHeld runs when a write starts waiting on start-up recovery; tests
// use it to know the write is held.
var gateHeld = func() {}

// waitReady holds a write until start-up recovery is done.
func (s *Service) waitReady(ctx context.Context) error {
	if s.ready == nil {
		return nil
	}
	select {
	case <-s.ready:
		return nil
	default:
		gateHeld() // recovery is still running: this write waits
	}
	select {
	case <-s.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// lockReady waits for start-up recovery, then takes agentID's lock. If ctx
// ends first it still locks, and the caller's next store read fails with
// ctx's error.
func (s *Service) lockReady(ctx context.Context, agentID string) func() {
	_ = s.waitReady(ctx)
	return s.lock(agentID)
}

// markResumed records that this process installed ref's policy on harness.
func (s *Service) markResumed(harness string, ref loomharness.NativeRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resumed[harness] == nil {
		s.resumed[harness] = map[loomharness.NativeRef]bool{}
	}
	s.resumed[harness][ref] = true
}

// lock takes agentID's lock, which orders that agent's writes, and returns its unlock.
func (s *Service) lock(agentID string) func() {
	l := s.agentLock(agentID)
	l.Lock()
	return l.Unlock
}

// agentLock returns agentID's lock.
func (s *Service) agentLock(agentID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[agentID]
	if !ok {
		l = &sync.Mutex{}
		s.locks[agentID] = l
	}
	return l
}

// repoPath resolves repo for the service's target.
func (s *Service) repoPath(ctx context.Context, repo string) (string, error) {
	return s.resolveRepo(ctx, s.target, repo)
}

// setState moves a to `to` by compare-and-set on a's state columns and
// revision, saving the change's events in the same transaction under the
// event lane, then publishes them (EventLog.CommitState). It returns
// loomstore.ErrStateChanged when another writer moved a first; that writer
// alone publishes. On any error nothing is saved or published.
func (s *Service) setState(ctx context.Context, a loomstore.Agent, to loomstore.AgentState) (loomstore.Agent, error) {
	from := a.StateOf()
	if to.State != from.State && !slices.Contains(transitions[from.State], to.State) {
		return a, fmt.Errorf("loomagent: invalid state change %s -> %s", from.State, to.State)
	}
	before := a
	a.State, a.StateReason, a.WaitingOn, a.Outcome = to.State, to.StateReason, to.WaitingOn, to.Outcome
	a.AttentionReason, a.RunningTurnID, a.Attempt = to.AttentionReason, to.RunningTurn, to.Attempt
	a.Revision++
	out := changeEvents(before, a)
	rows, err := eventRows(out)
	if err != nil {
		return before, err
	}
	if _, err := s.events.CommitState(ctx, a.AgentID, from, to, before.Revision, rows, s.busPublish(out)); err != nil {
		return before, err
	}
	if completed(a) && !completed(before) { // a child's attempt ended: tell its parent (§10.3)
		s.tryRecordCompletion(ctx, a) // the change is committed; a failed record is retried
	}
	return a, nil
}

// eventRows are out as rows to save; each keeps its EventID ("" lets the
// write name it).
func eventRows(out []Event) ([]loomstore.Event, error) {
	rows := make([]loomstore.Event, len(out))
	for i, e := range out {
		b, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		rows[i] = loomstore.Event{AgentID: e.AgentID, EventID: e.EventID, Kind: e.Type, TurnID: e.TurnID, Payload: b}
	}
	return rows, nil
}

// busPublish publishes out, whose rows were saved as saved, on the Bus too,
// in commit order. A write that saved nothing (a retry) publishes nothing.
func (s *Service) busPublish(out []Event) func(saved []loomstore.Event) {
	return func(saved []loomstore.Event) {
		for i, row := range saved[:min(len(saved), len(out))] { // out's rows come first
			e := out[i]
			e.EventID = row.EventID
			s.Bus.publish(e)
		}
	}
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

// publishChange saves, then publishes, the events of a's committed change.
func (s *Service) publishChange(ctx context.Context, before, after loomstore.Agent) error {
	for _, c := range changeEvents(before, after) {
		if err := s.emit(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// changeEvents are the events of a's change from before to after.
func changeEvents(before, after loomstore.Agent) []Event {
	e, out := Event{AgentID: after.AgentID, Time: time.Now()}, []Event{}
	if before.State != after.State {
		c := e
		c.Type, c.From, c.To, c.Reason = EventStateChanged, before.State, after.State, deref(after.StateReason)
		out = append(out, c)
	}
	if deref(before.AttentionReason) != deref(after.AttentionReason) {
		c := e
		c.Type, c.Reason = EventAttentionRaised, deref(after.AttentionReason)
		if after.AttentionReason == nil {
			c.Type, c.Reason = EventAttentionCleared, deref(before.AttentionReason)
		}
		out = append(out, c)
	}
	if after.Mode == "persistent" && after.State == StateIdle &&
		(before.State == StateActive || before.State == StateWaiting) {
		c := e
		c.Type, c.TurnID = EventIdle, deref(before.RunningTurnID)
		out = append(out, c)
	}
	if r := settledReason(after); r != "" && settledReason(before) == "" {
		c := e
		c.Type, c.Reason, c.Outcome, c.Attempt = EventSettled, r, deref(after.Outcome), after.Attempt
		out = append(out, c)
	}
	return out
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

// noBridge is the Bridge when none is wired: a preset with bridge tools fails
// closed (R-G); one without needs no registration.
func noBridge(_ context.Context, p Preset) (BridgeCaps, error) {
	if len(p.Tools) > 0 {
		return BridgeCaps{}, errors.New("no bridge is wired for its tools")
	}
	return BridgeCaps{}, nil
}
