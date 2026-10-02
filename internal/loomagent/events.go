package loomagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// EventTurnCompleted is the saved Loom event for a native turn.completed.
const EventTurnCompleted = "agent.turn_completed"

// replayCap bounds the memory one replay holds: the bytes of the history's
// rows plus its open asks. A session's saved history is its text, so a
// long session is a few MiB; 256 MiB is far beyond any real one yet stops a
// runaway history before it exhausts the process. A history over the cap is
// not saved at all and its agent shows Attention history_too_large.
var replayCap = 256 << 20

// AttentionHistoryTooLarge: the agent's native history is over replayCap,
// so it could not be replayed.
const AttentionHistoryTooLarge = "history_too_large"

// RunFeed waits feedRetry before it reopens the feed, doubling up to
// feedRetryMax while reads keep failing; a read that ingested events resets it.
var feedRetry, feedRetryMax = 200 * time.Millisecond, 30 * time.Second

// errFeedClosed is a feed that ended while ctx was still live.
var errFeedClosed = errors.New("loomagent: the harness feed closed")

// forgetResumed drops harness's resumed sessions, so the next Reconcile
// resumes them again; RunFeed calls it when the feed ended or the harness
// was unavailable, as after a harness restart, but not on a feed.gap.
func (s *Service) forgetResumed(harness string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.resumed, harness)
}

// RunFeed ingests the named harness's live feed until ctx ends (§5.2): each
// completed native event of an agent's session is saved in agent_events and
// then applied to its slots and turn. After every (re)connect and on each
// feed.gap it runs Reconcile, which backfills every live agent's session
// from its native history, so events the feed missed, or that came while
// Loom was down, are saved once (stable EventIDs) and never invented from
// the live bus. A failed save or history read, or a closed feed, reopens the
// feed after a backoff; a replay is all or nothing, so the backfill after
// the reconnect replays a failed one whole. A warning is logged when the
// failure changes, not on every retry.
func (s *Service) RunFeed(ctx context.Context, harness string) {
	wait, last := feedRetry, ""
	for h := s.harnesses[harness]; h != nil && ctx.Err() == nil; {
		read, err := s.readFeed(ctx, harness, h)
		if errors.Is(err, errFeedClosed) || errors.Is(err, loomharness.ErrUnavailable) {
			s.forgetResumed(harness) // the harness may have restarted: Resume its sessions again
		}
		if ctx.Err() != nil {
			return
		}
		if read {
			wait, last = feedRetry, ""
		}
		if err.Error() != last {
			last = err.Error()
			slog.Warn("loomagent: event ingestion stopped; reopening the feed to backfill", "harness", harness, "error", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		wait = min(2*wait, feedRetryMax)
	}
}

// readFeed backfills, then ingests h's feed until it ends or a save fails;
// it always returns an error, and read reports whether it ingested a live
// native event of an owned session (a feed.gap or another session's event
// is not one).
func (s *Service) readFeed(ctx context.Context, harness string, h loomharness.Harness) (read bool, err error) {
	f, err := h.Feed(ctx)
	if err != nil {
		return false, errors.Join(err, s.harnessAttention(ctx, harness, true))
	}
	defer func() { _ = f.Close() }()
	gap := loomstore.Event{Kind: KindFeedGap, Payload: json.RawMessage(strconv.Quote(harness))}
	s.events.Notify(gap)
	if err := s.Reconcile(ctx, harness); err != nil { // after subscribing, so nothing falls between
		return false, err
	}
	if err := s.harnessAttention(ctx, harness, false); err != nil {
		return false, err
	}
	for e := range f.Events() {
		ok := false
		if e.Type == loomharness.EventFeedGap {
			s.events.Notify(gap)
			err = s.Reconcile(ctx, harness)
		} else {
			ok, err = s.ingest(ctx, harness, e)
		}
		if err != nil {
			return read, err
		}
		read = read || ok
	}
	return read, errFeedClosed
}

// backfill replays the native history of every live agent's current session.
// A session the harness no longer has shows Attention session_missing, and
// one whose history is over replayCap history_too_large; both are skipped.
// Any other failure skips only that agent: the others are still replayed,
// and failed names the agents whose replay failed, with err joining why.
func (s *Service) backfill(ctx context.Context, harness string) (failed map[string]bool, err error) {
	agents, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: s.workspaceID, Harness: harness})
	if err != nil {
		return nil, err
	}
	failed = map[string]bool{}
	for _, a := range agents {
		if a.HarnessSessionID == nil {
			continue
		}
		rerr := s.replay(ctx, harness, a)
		switch {
		case errors.Is(rerr, errHistoryTooLarge):
			slog.Warn("loomagent: native history not replayed", "agent", a.AgentID, "error", rerr)
			rerr = s.flag(ctx, a.AgentID, AttentionHistoryTooLarge)
		case errors.Is(rerr, loomharness.ErrSessionNotFound):
			rerr = s.flag(ctx, a.AgentID, AttentionSessionMissing)
		}
		if rerr != nil {
			failed[a.AgentID] = true
			err = errors.Join(err, fmt.Errorf("%s: %w", a.AgentID, rerr))
		}
	}
	return failed, err
}

var errHistoryTooLarge = errors.New("loomagent: the native history is over the replay cap")

// flag shows Attention reason on agentID unless it already shows one.
func (s *Service) flag(ctx context.Context, agentID, reason string) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	if err != nil || a.AttentionReason != nil {
		return err
	}
	_, err = s.raiseAttention(ctx, a, reason)
	return err
}

// replay saves a's whole current native history all or nothing. It first
// reads every page, holding no lock or transaction, into memory bounded by
// replayCap (rows plus the fold); then one short transaction saves them
// all, so a failed read or write saves, publishes and changes nothing. Only
// after the commit are the new rows published and the history's net effect
// (the fold) applied to a under its lock. A crash between the commit and
// the apply is redone by the next replay: its rows are already saved once,
// and the fold, read again from the history, is applied as before.
func (s *Service) replay(ctx context.Context, harness string, a loomstore.Agent) error {
	ref := loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: *a.HarnessSessionID}
	sess := s.harnesses[harness].Session(ref)
	f := fold{running: deref(a.RunningTurnID), asks: map[string]*Ask{}}
	var rows []loomstore.Event
	size := 0
	for after := ""; ; {
		page, err := sess.Messages(ctx, after, 100)
		if err != nil {
			return err
		}
		for _, e := range page.Events {
			kind, ok := savedKinds[e.Type]
			if !ok || e.Session != ref {
				continue // a delta is live only: a subscriber had it, or missed it with the gap
			}
			if e, err = s.withText(ctx, a.AgentID, e); err != nil {
				return err
			}
			r := nativeRow(a.AgentID, kind, e)
			size += len(r.EventID) + len(r.Kind) + len(r.TurnID) + len(r.Payload) + f.add(e)
			if size > replayCap {
				return fmt.Errorf("%w of %d bytes", errHistoryTooLarge, replayCap)
			}
			rows = append(rows, r)
		}
		if after = page.Next; after == "" {
			break
		}
	}
	if err := s.events.AppendAll(ctx, a.AgentID, rows); err != nil {
		return err
	}
	return s.applyFold(ctx, a.AgentID, f)
}

// fold is the net effect of a replayed history on its agent, which had
// running as its running turn (an input key until turn.started names it).
type fold struct {
	running   string
	turn      string // the turn a turn.started named for running's input
	delivered bool   // running's input was delivered
	ended     *loomharness.Event
	asks      map[string]*Ask // the history's open asks
}

// add folds in e and returns the bytes it added to the fold.
func (f *fold) add(e loomharness.Event) int {
	switch e.Type {
	case loomharness.EventMessageDelivered:
		f.delivered = f.delivered || (f.running != "" && e.InputKey == f.running)
	case loomharness.EventTurnStarted:
		if f.running != "" && e.InputKey == f.running && e.TurnID != "" {
			f.turn = e.TurnID
		}
	case loomharness.EventTurnCompleted:
		if e.TurnID != "" && (e.TurnID == f.turn || e.TurnID == f.running) {
			f.ended = &e
			return len(e.TurnID) + len(e.StopReason)
		}
	case loomharness.EventAskOpened:
		ask := askOf(e)
		f.asks[e.AskID] = &ask
		return 2*len(ask.ID) + len(ask.Type) + len(ask.About) + len(ask.TurnID)
	case loomharness.EventAskResolved, loomharness.EventAskLost:
		delete(f.asks, e.AskID)
	}
	return 0
}

// applyFold applies a committed replay's net effect to agentID: a
// history_too_large or session_missing Attention clears; the history's open asks are opened;
// an ask open before that the history no longer has is saved as ask.lost
// (or just removed if its log shows it answered); then the delivery, turn
// start and turn end are applied as the live events would be (each a no-op
// once applied), and the turn end saves ask.lost for its asks still open.
func (s *Service) applyFold(ctx context.Context, agentID string, f fold) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	if isCode(err, CodeAgentNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if r := deref(a.AttentionReason); r == AttentionHistoryTooLarge || r == AttentionSessionMissing {
		if a, err = s.clearAttention(ctx, a); err != nil {
			return err
		}
	}
	for _, ask := range f.asks {
		s.setAsk(agentID, *ask, true)
	}
	if err := s.loseOpen(ctx, a, f.asks); err != nil {
		return err
	}
	if f.delivered {
		if err := s.delivered(ctx, a, f.running); err != nil {
			return err
		}
	}
	if f.turn != "" {
		if err := s.turnStarted(ctx, a, loomharness.Event{TurnID: f.turn, InputKey: f.running}); err != nil {
			return err
		}
		if a, err = s.live(ctx, agentID); err != nil {
			return err
		}
	}
	if f.ended != nil {
		return s.turnCompleted(ctx, a, *f.ended)
	}
	return s.syncWaiting(ctx, a)
}

// harnessAttention raises Attention harness_unavailable on each live agent
// of harness (raise), or clears it and retries their waiting messages. Only
// that harness's agents are touched; one already showing another Attention
// keeps it.
func (s *Service) harnessAttention(ctx context.Context, harness string, raise bool) error {
	agents, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: s.workspaceID, Harness: harness})
	if err != nil {
		return err
	}
	for _, a := range agents {
		if raise == (a.AttentionReason == nil) && (raise || deref(a.AttentionReason) == AttentionHarnessUnavailable) {
			err = errors.Join(err, s.flipAttention(ctx, a.AgentID, raise))
		}
	}
	return err
}

func (s *Service) flipAttention(ctx context.Context, agentID string, raise bool) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	if err != nil || (a.AttentionReason == nil) != raise {
		return err
	}
	if raise {
		_, err = s.raiseAttention(ctx, a, AttentionHarnessUnavailable)
		return err
	}
	if deref(a.AttentionReason) != AttentionHarnessUnavailable {
		return nil
	}
	if a, err = s.clearAttention(ctx, a); err != nil {
		return err
	}
	_, err = s.wake(ctx, a)
	return err
}

// ingest saves one live native event of an owned session, then applies it.
// A repeat of an event a replay already saved saves nothing new, and
// HarnessEvent ignores events of a turn that is not running. ok reports
// that e belonged to a live agent's session and was handled.
func (s *Service) ingest(ctx context.Context, harness string, e loomharness.Event) (ok bool, err error) {
	id, err := s.store.NativeSessionOwner(ctx, harness, e.Session.Root, e.Session.NativeID)
	if errors.Is(err, loomstore.ErrNotFound) {
		return false, nil // not an agent's session
	}
	if err != nil {
		return false, err
	}
	if _, err := s.live(ctx, id); isCode(err, CodeAgentNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if e, err = s.withText(ctx, id, e); err != nil {
		return false, err
	}
	if kind, ok := savedKinds[e.Type]; ok {
		if _, err := s.events.Append(ctx, nativeRow(id, kind, e)); err != nil {
			return false, err
		}
	} else if e.Type == loomharness.EventDelta {
		s.events.Notify(nativeRow(id, KindDelta, e))
	}
	return true, s.HarnessEvent(ctx, id, e)
}

// withText sets a message.delivered event's Text to the text Loom handed
// over with its input key, kept on the Send's receipt, so every harness
// carries it (OpenCode's and Claude's deliveries name only the key) however
// the sender's slot moved on since. A key Loom has no record of (a legacy
// receipt) keeps the harness's text, if any.
func (s *Service) withText(ctx context.Context, agentID string, e loomharness.Event) (loomharness.Event, error) {
	if e.Type != loomharness.EventMessageDelivered || e.InputKey == "" {
		return e, nil
	}
	text, ok, err := s.store.HandedText(ctx, agentID, e.InputKey)
	if ok {
		e.Text = text
	}
	return e, err
}

// savedKinds maps the completed native events Phase 1 saves to their Loom
// kind. Deltas and item starts stay live only.
var savedKinds = map[loomharness.EventType]string{
	loomharness.EventMessageDelivered: string(loomharness.EventMessageDelivered),
	loomharness.EventTurnStarted:      string(loomharness.EventTurnStarted),
	loomharness.EventItemCompleted:    string(loomharness.EventItemCompleted),
	loomharness.EventUsage:            string(loomharness.EventUsage),
	loomharness.EventTurnCompleted:    EventTurnCompleted,
	loomharness.EventAskOpened:        string(loomharness.EventAskOpened),
	loomharness.EventAskResolved:      string(loomharness.EventAskResolved),
	loomharness.EventAskLost:          string(loomharness.EventAskLost),
	loomharness.EventTurnResumed:      string(loomharness.EventTurnResumed),
	loomharness.EventSubagentStarted:  string(loomharness.EventSubagentStarted),
}

// nativeRow is e as a saved row. Its EventID uses only ids the live feed and
// a catch-up read share (the port contract): the session's Root and NativeID
// and the event's own id: InputKey for a delivery, AskID for an ask, TurnID
// for a turn start or end, else ItemID (a usage its step's id, a turn.resumed
// its resume's id), else the native Seq. TurnID is never part of an item's id.
func nativeRow(agentID, kind string, e loomharness.Event) loomstore.Event {
	key := e.ItemID
	switch e.Type {
	case loomharness.EventMessageDelivered:
		key = e.InputKey
	case loomharness.EventAskOpened, loomharness.EventAskResolved, loomharness.EventAskLost:
		key = e.AskID
	case loomharness.EventTurnStarted, loomharness.EventTurnCompleted:
		key = e.TurnID
	}
	if key == "" {
		key = "seq:" + strconv.FormatInt(e.Seq, 10)
	}
	b, _ := json.Marshal(struct {
		Session    string `json:"session"`
		ItemID     string `json:"itemId,omitempty"`
		ItemKind   string `json:"itemKind,omitempty"`
		InputKey   string `json:"inputKey,omitempty"`
		AskID      string `json:"askId,omitempty"`
		Text       string `json:"text,omitempty"`
		StopReason string `json:"stopReason,omitempty"`
	}{e.Session.NativeID, e.ItemID, e.ItemKind, e.InputKey, e.AskID, e.Text, e.StopReason})
	return loomstore.Event{AgentID: agentID, Kind: kind, TurnID: e.TurnID, Payload: b,
		EventID: kind + ":" + e.Session.Root + ":" + e.Session.NativeID + ":" + key}
}

// emit saves a Loom event in agent_events, then publishes it on the Bus (the
// Bus is delivery only). Its EventID is a hash of the event itself, so the
// same event saved again is one row, and the Bus copy names its row.
func (s *Service) emit(ctx context.Context, e Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	e.EventID = e.Type + ":" + hex.EncodeToString(sum[:16])
	if _, err := s.events.Append(ctx, loomstore.Event{AgentID: e.AgentID, EventID: e.EventID,
		Kind: e.Type, TurnID: e.TurnID, Payload: b}); err != nil {
		return err
	}
	s.Bus.publish(e)
	return nil
}

func isCode(err error, c Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == c
}
