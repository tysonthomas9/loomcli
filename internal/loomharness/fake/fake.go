// Package fake is a scripted, in-memory loomharness.Harness for tests. It
// calls no external service. Tests script each agent's turns (deltas, asks,
// crashes, event gaps and how delivery looks after a crash) by OpenSpec.Key,
// then drive the port and read the Feed and Messages.
//
// Turns run synchronously inside Prompt and Reply, so events are already on
// the Feed when those calls return.
package fake

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// Step is one scripted step of a turn. Set exactly one of Delta, Ask, Crash or Usage.
type Step struct {
	Delta    string             // emits a delta on the turn's message item
	Ask      string             // opens an ask with this ID; the turn waits for Reply
	Question bool               // the Ask is a question, not an approval
	Crash    bool               // the harness process dies here, mid-turn
	Gap      bool               // the live Feed misses this step's event (it gets feed.gap); Messages still has it
	Usage    *loomharness.Usage // emits a usage event with these counts
	Tool     *loomharness.Tool  // emits a tool call's item.started (name and input) and item.completed
}

// Turn is one scripted turn.
type Turn struct {
	Steps []Step
	// Delivery is how the turn's input fares. "" or LandedFound: it lands and
	// the steps run. LandedNotFound: the harness dies before the input lands.
	// LandedUnknown: the harness dies and its history cannot tell afterwards.
	Delivery loomharness.Landed
	// ResumeContinues: after a crash, Resume emits turn.resumed and runs the
	// remaining steps (Claude's resume_reason, codex's continued turn).
	// Otherwise Resume ends the turn as cancelled and loses its open ask.
	ResumeContinues bool
}

// feedBuffer bounds each Feed; a test that lets it fill is broken.
const feedBuffer = 1024

// Harness is the fake. Use New.
type Harness struct {
	mu       sync.Mutex
	down     bool
	nextID   int
	byKey    map[string]loomharness.NativeRef
	sessions map[loomharness.NativeRef]*session
	scripts  map[string][]Turn
	feeds    map[*feed]bool
	install  error // set by FailInstall: Resume fails before installing rules
	open     error // set by FailOpen: Open fails, leaving its session if leave
	leave    bool
	purge    error // set by FailPurge: Purge fails and removes nothing
}

type session struct {
	ref           loomharness.NativeRef
	key           string
	model, dir    string
	opts          []loomharness.Option
	turnModels    []Selection // the model and options each turn started with
	seq           int64
	turns         int
	history       []loomharness.Event
	inputs        map[string]loomharness.Landed
	turn          Turn
	turnID        string
	step          int
	running       bool
	ask           string
	crashed       bool // a turn was running when the harness died; Resume first
	closed        bool // Close stopped the runtime; history stays, Resume reopens
	lastInterrupt bool
	rules         []loomharness.PermissionRule   // the installed policy
	turnRules     [][]loomharness.PermissionRule // the policy each run of a turn used
}

var (
	_ loomharness.Harness = (*Harness)(nil)
	_ loomharness.Session = (*sessionHandle)(nil)
)

// New returns a running fake harness.
func New() *Harness {
	return &Harness{
		byKey:    map[string]loomharness.NativeRef{},
		sessions: map[loomharness.NativeRef]*session{},
		scripts:  map[string][]Turn{},
		feeds:    map[*feed]bool{},
	}
}

// Script queues turns for the agent opened with key. Each Prompt takes the
// next one; with none queued a turn emits one "ok" delta.
func (h *Harness) Script(key string, turns ...Turn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scripts[key] = append(h.scripts[key], turns...)
}

func (h *Harness) Name() string { return "fake" }

// Models offers fake-model, whose effort is low, medium (the default) or high.
func (h *Harness) Models(context.Context) ([]loomharness.Model, error) {
	return []loomharness.Model{{ID: "fake-model", Name: "Fake model", Provider: "fake", ProviderName: "Fake",
		ContextLimit: 1000, Input: []string{"text"}, Default: true,
		Options: []loomharness.OptionDescriptor{{ID: loomharness.OptionEffort, Label: "Reasoning", Type: loomharness.OptionSelect,
			Current: "medium", Choices: []loomharness.OptionChoice{{ID: "low", Label: "Low"},
				{ID: "medium", Label: "Medium", Default: true}, {ID: "high", Label: "High"}}}}}}, nil
}

// Selection is the model and options a turn started with.
type Selection struct {
	Model   string
	Options []loomharness.Option
}

// Turns returns the model and options each turn on ref started with, oldest first.
func (h *Harness) Turns(ref loomharness.NativeRef) []Selection {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[ref]; ok {
		return slices.Clone(s.turnModels)
	}
	return nil
}

func (h *Harness) Health(context.Context) (loomharness.Health, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return loomharness.Health{OK: !h.down, Version: loomharness.VersionCheck{Harness: "fake"}}, nil
}

func (h *Harness) Open(_ context.Context, spec loomharness.OpenSpec) (loomharness.NativeRef, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.down {
		return loomharness.NativeRef{}, loomharness.ErrUnavailable
	}
	if h.open != nil && !h.leave {
		return loomharness.NativeRef{}, h.open
	}
	if ref, ok := h.byKey[spec.Key]; ok {
		if s, ok := h.sessions[ref]; ok && h.open == nil { // a repeat installs the current rules
			s.rules = slices.Clone(spec.Rules)
		}
		return ref, nil
	}
	h.nextID++
	ref := loomharness.NativeRef{Root: spec.Launch.Root, NativeID: "fake_ses_" + strconv.Itoa(h.nextID)}
	h.byKey[spec.Key] = ref
	h.sessions[ref] = &session{ref: ref, key: spec.Key, model: spec.Model, dir: spec.Dir, inputs: map[string]loomharness.Landed{},
		rules: slices.Clone(spec.Rules)}
	return ref, h.open
}

// FailOpen makes every Open fail with err. With leave, Open still creates
// (or finds) the session and returns its ref with err, as an Open that could
// not remove what it created; without, it creates nothing. nil restores Open.
func (h *Harness) FailOpen(err error, leave bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.open, h.leave = err, leave
}

// FailPurge makes every Purge fail with err and remove nothing; nil restores it.
func (h *Harness) FailPurge(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.purge = err
}

// FailInstall makes every Resume fail to install its rules with err; nil
// restores installs.
func (h *Harness) FailInstall(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.install = err
}

// Rules returns the policy installed on ref, and the policy each run of a
// turn on ref used, oldest first.
func (h *Harness) Rules(ref loomharness.NativeRef) (installed []loomharness.PermissionRule, turns [][]loomharness.PermissionRule) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[ref]; ok {
		return slices.Clone(s.rules), slices.Clone(s.turnRules)
	}
	return nil, nil
}

func (h *Harness) Session(ref loomharness.NativeRef) loomharness.Session {
	return &sessionHandle{h: h, ref: ref}
}

// Feed returns a live feed. It ends when the harness crashes or restarts, or ctx ends.
func (h *Harness) Feed(ctx context.Context) (loomharness.Feed, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.down {
		return nil, loomharness.ErrUnavailable
	}
	f := &feed{h: h, ch: make(chan loomharness.Event, feedBuffer)}
	h.feeds[f] = true
	context.AfterFunc(ctx, func() { _ = f.Close() })
	return f, nil
}

// Purge deletes exactly the given refs.
func (h *Harness) Purge(_ context.Context, owned []loomharness.NativeRef) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.purge != nil {
		return h.purge
	}
	for _, ref := range owned {
		if s, ok := h.sessions[ref]; ok {
			delete(h.byKey, s.key)
			delete(h.sessions, ref)
		}
	}
	return nil
}

// Restart stops and starts the harness; running turns die as in a crash.
func (h *Harness) Restart(context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.crash()
	h.down = false
	return nil
}

// Crash kills the harness now, as a Crash step does. Calls fail with
// ErrUnavailable until Restart.
func (h *Harness) Crash() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.crash()
}

func (h *Harness) crash() {
	h.down = true
	for _, s := range h.sessions {
		if s.running {
			s.crashed = true
		}
	}
	for f := range h.feeds {
		f.closeLocked()
	}
}

// emit records e in the session's history and sends it to live feeds,
// or sends feed.gap instead when live is false.
func (h *Harness) emit(s *session, e loomharness.Event, live bool) {
	s.seq++
	e.Session, e.Seq, e.Time, e.TurnID = s.ref, s.seq, time.Now(), s.turnID
	if e.ItemID == "" {
		e.ItemID = s.ref.NativeID + "/" + strconv.FormatInt(s.seq, 10)
	}
	s.history = append(s.history, e)
	if !live {
		e = loomharness.Event{Type: loomharness.EventFeedGap, Session: s.ref, Seq: s.seq - 1, Time: e.Time}
	}
	for f := range h.feeds {
		select {
		case f.ch <- e:
		default:
			panic("fake: feed buffer full; read the Feed")
		}
	}
}

// run plays the current turn's steps until it ends, waits on an ask or crashes.
func (h *Harness) run(s *session) {
	s.turnRules = append(s.turnRules, slices.Clone(s.rules))
	for s.step < len(s.turn.Steps) {
		st := s.turn.Steps[s.step]
		s.step++
		switch {
		case st.Crash:
			h.crash()
			return
		case st.Ask != "":
			s.ask = st.Ask
			kind := "approval"
			if st.Question {
				kind = "question"
			}
			h.emit(s, loomharness.Event{Type: loomharness.EventAskOpened, AskID: st.Ask, ItemKind: kind}, !st.Gap)
			return
		case st.Usage != nil:
			h.emit(s, loomharness.Event{Type: loomharness.EventUsage, Usage: *st.Usage}, !st.Gap)
		case st.Tool != nil:
			id := s.turnID + "/tool/" + strconv.Itoa(s.step)
			started := loomharness.Tool{Name: st.Tool.Name, Input: st.Tool.Input}
			done := *st.Tool
			h.emit(s, loomharness.Event{Type: loomharness.EventItemStarted, ItemID: id, ItemKind: "tool", Tool: &started}, !st.Gap)
			h.emit(s, loomharness.Event{Type: loomharness.EventItemCompleted, ItemID: id, ItemKind: "tool", Tool: &done}, !st.Gap)
		default:
			h.emit(s, loomharness.Event{Type: loomharness.EventDelta, ItemID: s.turnID + "/msg", ItemKind: "message", Text: st.Delta}, !st.Gap)
		}
	}
	h.endTurn(s, "completed")
}

func (h *Harness) endTurn(s *session, reason string) {
	if s.ask != "" {
		h.emit(s, loomharness.Event{Type: loomharness.EventAskLost, AskID: s.ask}, true)
		s.ask = ""
	}
	s.running, s.crashed = false, false
	s.lastInterrupt = reason == "cancelled"
	h.emit(s, loomharness.Event{Type: loomharness.EventTurnCompleted, StopReason: reason}, true)
}

// lookup returns the live session for ref, under h.mu.
func (h *Harness) lookup(ref loomharness.NativeRef) (*session, error) {
	if h.down {
		return nil, loomharness.ErrUnavailable
	}
	s, ok := h.sessions[ref]
	if !ok {
		return nil, fmt.Errorf("fake %s: %w", ref.NativeID, loomharness.ErrSessionNotFound)
	}
	return s, nil
}

type feed struct {
	h      *Harness
	ch     chan loomharness.Event
	closed bool
}

func (f *feed) Events() <-chan loomharness.Event { return f.ch }

func (f *feed) Close() error {
	f.h.mu.Lock()
	defer f.h.mu.Unlock()
	f.closeLocked()
	return nil
}

func (f *feed) closeLocked() {
	if !f.closed {
		f.closed = true
		close(f.ch)
		delete(f.h.feeds, f)
	}
}

type sessionHandle struct {
	h   *Harness
	ref loomharness.NativeRef
}

// Resume recovers the session under the same root and reopens a closed one.
// It installs rules first; with FailInstall set it fails and nothing changes
// or runs. A turn cut off by a crash or Close loses its open ask (R20), then
// either continues (Turn.ResumeContinues) under the new rules or ends cancelled.
func (x *sessionHandle) Resume(_ context.Context, l loomharness.Launch, rules []loomharness.PermissionRule) (loomharness.NativeRef, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	s, err := x.h.lookup(x.ref)
	if err != nil {
		return loomharness.NativeRef{}, err
	}
	if l.Root != s.ref.Root {
		return loomharness.NativeRef{}, fmt.Errorf("fake %s not under root %q: %w", s.ref.NativeID, l.Root, loomharness.ErrSessionNotFound)
	}
	if x.h.install != nil {
		return loomharness.NativeRef{}, fmt.Errorf("fake %s: install permissions: %w", s.ref.NativeID, x.h.install)
	}
	s.rules = slices.Clone(rules)
	s.closed = false
	if s.crashed {
		s.crashed = false
		if s.turn.ResumeContinues {
			if s.ask != "" {
				x.h.emit(s, loomharness.Event{Type: loomharness.EventAskLost, AskID: s.ask}, true)
				s.ask = ""
			}
			x.h.emit(s, loomharness.Event{Type: loomharness.EventTurnResumed}, true)
			x.h.run(s)
		} else {
			x.h.endTurn(s, "cancelled")
		}
	}
	return s.ref, nil
}

func (x *sessionHandle) Prompt(_ context.Context, in loomharness.Input) error {
	h := x.h
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := h.lookup(x.ref)
	if err != nil {
		return err
	}
	if err := s.open(); err != nil {
		return err
	}
	if s.crashed {
		return fmt.Errorf("fake %s: Resume after the crash first: %w", s.ref.NativeID, loomharness.ErrBusy)
	}
	if s.running {
		return loomharness.ErrBusy
	}
	t := Turn{Steps: []Step{{Delta: "ok"}}}
	if q := h.scripts[s.key]; len(q) > 0 {
		t, h.scripts[s.key] = q[0], q[1:]
	}
	if t.Delivery == loomharness.LandedNotFound || t.Delivery == loomharness.LandedUnknown {
		if t.Delivery == loomharness.LandedUnknown {
			s.inputs[in.Key] = loomharness.LandedUnknown
		}
		h.crash()
		return nil
	}
	s.turns++
	s.turnModels = append(s.turnModels, Selection{s.model, slices.Clone(s.opts)})
	s.turn, s.turnID, s.step = t, s.ref.NativeID+"/turn_"+strconv.Itoa(s.turns), 0
	s.running, s.lastInterrupt = true, false
	s.inputs[in.Key] = loomharness.LandedFound
	h.emit(s, loomharness.Event{Type: loomharness.EventMessageDelivered, InputKey: in.Key, Text: in.Text}, true)
	h.emit(s, loomharness.Event{Type: loomharness.EventTurnStarted, InputKey: in.Key}, true)
	h.run(s)
	return nil
}

func (x *sessionHandle) Interrupt(context.Context) (bool, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	s, err := x.h.lookup(x.ref)
	if err == nil {
		err = s.open()
	}
	if err != nil || !s.running || s.crashed {
		return false, err
	}
	x.h.endTurn(s, "cancelled")
	return true, nil
}

func (x *sessionHandle) Reply(_ context.Context, askID string, r loomharness.Reply) error {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	s, err := x.h.lookup(x.ref)
	if err != nil {
		return err
	}
	if err := s.open(); err != nil {
		return err
	}
	if s.crashed || s.ask == "" || s.ask != askID {
		return fmt.Errorf("fake %s: no open ask %q", s.ref.NativeID, askID)
	}
	s.ask = ""
	x.h.emit(s, loomharness.Event{Type: loomharness.EventAskResolved, AskID: askID, Text: r.Answer}, true)
	x.h.run(s)
	return nil
}

// HasInput is found once an input landed, unknown when the scripted crash
// hid it, and not_found otherwise.
func (x *sessionHandle) HasInput(_ context.Context, key string) (loomharness.Landed, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	s, err := x.h.lookup(x.ref)
	if err != nil {
		return "", err
	}
	if l, ok := s.inputs[key]; ok {
		return l, nil
	}
	return loomharness.LandedNotFound, nil
}

// Messages pages the session history; the cursor is the last Seq read.
func (x *sessionHandle) Messages(_ context.Context, after string, limit int) (loomharness.MessagePage, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	s, err := x.h.lookup(x.ref)
	if err != nil {
		return loomharness.MessagePage{}, err
	}
	var from int64
	if after != "" {
		if from, err = strconv.ParseInt(after, 10, 64); err != nil {
			return loomharness.MessagePage{}, fmt.Errorf("fake: bad cursor %q: %w", after, err)
		}
	}
	var page loomharness.MessagePage
	for _, e := range s.history {
		if e.Seq <= from {
			continue
		}
		if limit > 0 && len(page.Events) == limit {
			page.Next = strconv.FormatInt(page.Events[limit-1].Seq, 10)
			break
		}
		page.Events = append(page.Events, e)
	}
	return page, nil
}

func (x *sessionHandle) Status(context.Context) (loomharness.Status, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	s, err := x.h.lookup(x.ref)
	if err != nil {
		return loomharness.Status{}, err
	}
	st := loomharness.Status{Running: s.running && !s.crashed, LastTurnInterrupt: s.lastInterrupt}
	if st.Running {
		st.TurnID = s.turnID
	}
	return st, nil
}

func (x *sessionHandle) SetModel(_ context.Context, model string, opts []loomharness.Option) error {
	return x.idle(func(s *session) { s.model, s.opts = model, slices.Clone(opts) }, false)
}

func (x *sessionHandle) Move(_ context.Context, dir string) error {
	return x.idle(func(s *session) { s.dir = dir }, true)
}

// Unload frees an idle session's runtime as Close does; Resume reopens it.
func (x *sessionHandle) Unload(context.Context) error {
	return x.idle(func(s *session) { s.closed = true }, true)
}

// Close stops the session's runtime and keeps its history. A running turn
// dies as in a crash. Until Resume, only HasInput, Messages and Status work.
func (x *sessionHandle) Close(context.Context) error {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	s, err := x.h.lookup(x.ref)
	if err != nil {
		return err
	}
	if s.running {
		s.crashed = true
	}
	s.closed = true
	return nil
}

// open refuses calls that need the session's runtime after Close.
func (s *session) open() error {
	if s.closed {
		return fmt.Errorf("fake %s closed; Resume to reopen: %w", s.ref.NativeID, loomharness.ErrUnavailable)
	}
	return nil
}

// idle applies fn to an open session, refusing with ErrBusy mid-turn when needIdle.
func (x *sessionHandle) idle(fn func(*session), needIdle bool) error {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	s, err := x.h.lookup(x.ref)
	if err == nil {
		err = s.open()
	}
	if err != nil {
		return err
	}
	if needIdle && (s.running || s.crashed) {
		return loomharness.ErrBusy
	}
	fn(s)
	return nil
}
