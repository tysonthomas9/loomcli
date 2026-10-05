package loomagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// switchEnv is one lead on harness "fa" with a recorded session, and a second
// harness "fb".
type switchEnv struct {
	s          *Service
	fa, fb     *fake.Harness
	old        loomharness.NativeRef
	interrupts int
}

func newSwitchEnv(t *testing.T, state string) *switchEnv {
	t.Helper()
	ctx := context.Background()
	e := &switchEnv{fa: fake.New(), fb: fake.New()}
	harnesses := map[string]loomharness.Harness{"fa": e.fa, "fb": e.fb}
	if _, err := e.fb.Open(ctx, loomharness.OpenSpec{Key: "other"}); err != nil { // fb ids differ from fa's
		t.Fatal(err)
	}
	var err error
	if e.old, err = e.fa.Open(ctx, loomharness.OpenSpec{Key: "a1", Launch: loomharness.Launch{Root: "/root/fa"}}); err != nil {
		t.Fatal(err)
	}
	a := svcAgent("a1", "persistent", state)
	a.Harness, a.HarnessSessionID, a.Model = "fa", &e.old.NativeID, sp("fake-model")
	e.s = newService(t, ServiceConfig{
		Harnesses: harnesses,
		Launch: func(_ context.Context, _ loomstore.Agent, h string) (loomharness.Launch, error) {
			return loomharness.Launch{Root: "/root/" + h}, nil
		},
		Interrupt: func(ctx context.Context, a loomstore.Agent) error {
			e.interrupts++
			sess, _, err := e.s.current(ctx, a)
			if err != nil {
				return err
			}
			_, err = sess.Interrupt(ctx)
			return err
		},
	}, a)
	if err := e.s.store.RecordNativeSession(ctx, loomstore.NativeSession{AgentID: "a1", Harness: "fa",
		NativeRoot: e.old.Root, NativeID: e.old.NativeID}); err != nil {
		t.Fatal(err)
	}
	return e
}

// startTurn runs a turn on the old session that stops on an ask.
func (e *switchEnv) startTurn(t *testing.T) {
	t.Helper()
	e.fa.Script("a1", fake.Turn{Steps: []fake.Step{{Delta: "partial"}, {Ask: "ask1"}}})
	if err := e.fa.Session(e.old).Prompt(context.Background(), loomharness.Input{Key: "k0", Text: "go"}); err != nil {
		t.Fatal(err)
	}
	e.appendEv(t, "item:partial", "item.completed")
	if e.s.get(t, "a1").State == StateWaiting {
		e.s.setAsk("a1", Ask{ID: "ask1", Type: "approval", TurnID: "turn1"}, true)
	}
}

func (e *switchEnv) appendEv(t *testing.T, id, kind string) {
	t.Helper()
	if _, err := e.s.events.Append(context.Background(), loomstore.Event{AgentID: "a1", EventID: id, Kind: kind,
		Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

// dispatcher stands in for 1.6's slot dispatcher: on agent.idle it takes the
// agent lock and hands the next waiting slot to the current session. It
// reports the native session id each slot was prompted on.
func (e *switchEnv) dispatcher(t *testing.T) <-chan string {
	t.Helper()
	sub := e.s.Bus.Subscribe("a1")
	t.Cleanup(func() { e.s.Bus.Unsubscribe(sub) })
	out := make(chan string, 8)
	go func() {
		for ev := range sub.C {
			if ev.Type != EventIdle {
				continue
			}
			func() {
				ctx := context.Background()
				defer e.s.lock("a1")()
				a, err := e.s.store.GetAgent(ctx, "a1")
				if err != nil {
					return
				}
				sess, _, err := e.s.current(ctx, a)
				if err != nil || sess == nil {
					return
				}
				sl, err := e.s.handOver(ctx, a, func(sl loomstore.Slot) string { return "key-" + sl.RequestID })
				if err != nil {
					return
				}
				if sess.Prompt(ctx, loomharness.Input{Key: deref(sl.NativeKey), Text: sl.Body}) == nil {
					_ = e.s.store.MarkDelivered(ctx, "a1", sl.Sender, sl.RequestID)
					out <- *a.HarnessSessionID
				}
			}()
		}
	}()
	return out
}

func deliveredOn(t *testing.T, c <-chan string) string {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher never delivered the waiting slot")
		return ""
	}
}

func noMore(t *testing.T, c <-chan string) {
	t.Helper()
	select {
	case v := <-c:
		t.Fatalf("slot delivered twice (again on %s)", v)
	case <-time.After(100 * time.Millisecond):
	}
}

func (e *switchEnv) kinds(t *testing.T, kind string) int {
	t.Helper()
	p, err := e.s.events.Page(context.Background(), loomstore.EventQuery{AgentID: "a1", Kinds: []string{kind}})
	if err != nil {
		t.Fatal(err)
	}
	return len(p.Events)
}

func (e *switchEnv) owned(t *testing.T) []loomstore.NativeSession {
	t.Helper()
	n, err := e.s.store.NativeSessions(context.Background(), "a1")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func switchReq(id string, ver int64, h string) UpdateRequest {
	return UpdateRequest{Envelope: Envelope{RequestID: id, Expect: &Expect{SpecVersion: &ver}}, AgentID: "a1", Harness: h}
}

func TestUpdateNameAndModel(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateIdle)
	other := svcAgent("a2", "single_task", StateActive)
	if err := e.s.store.InsertAgent(ctx, other); err != nil {
		t.Fatal(err)
	}
	got, err := e.s.Update(ctx, UpdateRequest{Envelope: Envelope{RequestID: "r1"}, AgentID: "a1", Name: "lead-x"})
	if err != nil || got.Name != "lead-x" || got.SpecVersion != 2 {
		t.Fatalf("rename = %+v, %v", got, err)
	}
	if got, err = e.s.Update(ctx, UpdateRequest{Envelope: Envelope{RequestID: "r1"}, AgentID: "a1", Name: "lead-x"}); err != nil ||
		got.Name != "lead-x" || got.SpecVersion != 2 {
		t.Fatalf("retry = %+v, %v", got, err)
	}
	_, err = e.s.Update(ctx, UpdateRequest{Envelope: Envelope{RequestID: "r1"}, AgentID: "a1", Name: "again"})
	wantCode(t, err, CodeConflict) // r1 is bound to its first payload
	stale := int64(1)
	_, err = e.s.Update(ctx, UpdateRequest{Envelope: Envelope{Expect: &Expect{SpecVersion: &stale}}, AgentID: "a1", Name: "y"})
	wantCode(t, err, CodeSpecVersionMismatch)
	_, err = e.s.Update(ctx, UpdateRequest{AgentID: "a1", Name: "a2"})
	wantCode(t, err, CodeAgentNameTaken)
	_, err = e.s.Update(ctx, UpdateRequest{AgentID: "a1", Model: "no pe"})
	wantCode(t, err, CodePresetInvalid) // malformed; an unlisted model passes (MCS1)
	_, err = e.s.Update(ctx, UpdateRequest{AgentID: "a2", Model: "fake-model"})
	wantCode(t, err, CodeAgentBusy)
	if _, err = e.s.Update(ctx, UpdateRequest{AgentID: "a2", Name: "renamed"}); err != nil {
		t.Fatalf("rename unfinished task: %v", err)
	}
	if e.kinds(t, KindAgentUpdated) != 1 {
		t.Fatalf("agent.updated events = %d, want 1", e.kinds(t, KindAgentUpdated))
	}
}

func TestHarnessSwitchSameHarnessIsNoop(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateIdle)
	got, err := e.s.Update(ctx, switchReq("r1", 1, "fa"))
	if err != nil || got.SpecVersion != 1 || got.Harness != "fa" {
		t.Fatalf("same harness = %+v, %v", got, err)
	}
	if a := e.s.get(t, "a1"); *a.HarnessSessionID != e.old.NativeID || len(e.owned(t)) != 1 || e.kinds(t, KindHarnessChanged) != 0 {
		t.Fatalf("no-op changed state: session %s owned %d", *a.HarnessSessionID, len(e.owned(t)))
	}
	_, err = e.s.Update(ctx, UpdateRequest{AgentID: "a1", Harness: "fb"})
	wantCode(t, err, CodeSpecVersionMismatch) // Expect.SpecVersion is required
}

func TestHarnessSwitchMidTurnStopsAndDeliversSlotOnce(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateActive)
	e.startTurn(t)
	queue(t, e.s, "a1", "user", "next")
	delivered := e.dispatcher(t)
	got, err := e.s.Update(ctx, switchReq("r1", 1, "fb"))
	if err != nil || got.Harness != "fb" || got.SpecVersion != 2 || got.State != StateIdle {
		t.Fatalf("switch = %+v, %v", got, err)
	}
	a := e.s.get(t, "a1")
	if e.interrupts != 1 || *a.HarnessSessionID == e.old.NativeID || a.Model == nil || *a.Model != "fake-model" {
		t.Fatalf("interrupts %d session %s model %v", e.interrupts, *a.HarnessSessionID, a.Model)
	}
	if on := deliveredOn(t, delivered); on != *a.HarnessSessionID {
		t.Fatalf("slot delivered on %s, want the new session %s", on, *a.HarnessSessionID)
	}
	noMore(t, delivered)
	if e.kinds(t, KindHarnessChanged) != 1 || e.kinds(t, KindAskLost) != 0 || e.kinds(t, "item.completed") != 1 {
		t.Fatal("want one harness.changed, no ask.lost and the partial output kept")
	}
	if st, _ := e.fa.Session(e.old).Status(ctx); st.Running || !st.LastTurnInterrupt {
		t.Fatalf("old turn not stopped: %+v", st)
	}
	if got, err = e.s.Update(ctx, switchReq("r1", 1, "fb")); err != nil || got.SpecVersion != 2 || e.kinds(t, KindHarnessChanged) != 1 {
		t.Fatalf("retry = %+v, %v", got, err)
	}
}

func TestHarnessSwitchOpenApprovalReportsAskLostOnce(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateWaiting)
	e.startTurn(t)
	to := e.s.get(t, "a1").StateOf()
	to.WaitingOn, to.RunningTurn = sp("approval"), sp("turn1")
	if _, err := e.s.store.CommitState(ctx, "a1", e.s.get(t, "a1").StateOf(), to, e.s.get(t, "a1").Revision, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Update(ctx, switchReq("r1", 1, "fb")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Update(ctx, switchReq("r1", 1, "fb")); err != nil {
		t.Fatal(err)
	}
	p, err := e.s.events.Page(ctx, loomstore.EventQuery{AgentID: "a1", Kinds: []string{KindAskLost}})
	if err != nil || len(p.Events) != 1 || !strings.Contains(string(p.Events[0].Payload), `"askId":"ask1"`) {
		t.Fatalf("ask.lost = %+v, %v; want one, for ask1", p.Events, err)
	}
	if a := e.s.get(t, "a1"); a.State != StateIdle || a.WaitingOn != nil || a.RunningTurnID != nil {
		t.Fatalf("state = %s waiting %v", a.State, a.WaitingOn)
	}
}

func TestHarnessSwitchStartupFailureKeepsOldHarness(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateWaiting)
	e.startTurn(t)
	queue(t, e.s, "a1", "user", "next")
	delivered := e.dispatcher(t)
	e.fb.Crash()
	_, err := e.s.Update(ctx, switchReq("r1", 1, "fb"))
	wantCode(t, err, CodeHarnessUnavailable)
	a := e.s.get(t, "a1")
	if a.Harness != "fa" || *a.HarnessSessionID != e.old.NativeID || a.SpecVersion != 1 || a.State != StateIdle {
		t.Fatalf("row changed: %s %s v%d %s", a.Harness, *a.HarnessSessionID, a.SpecVersion, a.State)
	}
	if on := deliveredOn(t, delivered); on != e.old.NativeID {
		t.Fatalf("slot delivered on %s, want the old session", on)
	}
	noMore(t, delivered)
	if e.kinds(t, KindError) != 1 || e.kinds(t, KindHarnessChanged) != 0 || e.kinds(t, KindAskLost) != 1 ||
		e.kinds(t, "item.completed") != 1 || e.interrupts != 1 {
		t.Fatal("want one error event, no harness.changed, one ask.lost, kept partial output, one interrupt")
	}
	if err := e.fb.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := e.s.Update(ctx, switchReq("r2", 1, "fb")); err != nil || got.Harness != "fb" {
		t.Fatalf("retry with the same SpecVersion = %+v, %v", got, err)
	}
}

func TestHarnessSwitchRecordsReturnedNativeRef(t *testing.T) {
	ctx := context.Background()
	t.Run("success", func(t *testing.T) {
		e := newSwitchEnv(t, StateIdle)
		if _, err := e.s.Update(ctx, switchReq("r1", 1, "fb")); err != nil {
			t.Fatal(err)
		}
		a, owned := e.s.get(t, "a1"), e.owned(t)
		if len(owned) != 2 || owned[0].NativeID != e.old.NativeID || owned[1].NativeRoot != "/root/fb" ||
			owned[1].Harness != "fb" || owned[1].NativeID != *a.HarnessSessionID {
			t.Fatalf("owned = %+v", owned)
		}
	})
	t.Run("crash before commit", func(t *testing.T) {
		e := newSwitchEnv(t, StateWaiting)
		e.startTurn(t)
		queue(t, e.s, "a1", "user", "next")
		delivered := e.dispatcher(t)
		beforeSwitchCommit = func() { panic("crash") }
		t.Cleanup(func() { beforeSwitchCommit = func() {} })
		func() {
			defer func() { _ = recover() }()
			_, _ = e.s.Update(ctx, switchReq("r1", 1, "fb"))
		}()
		a, owned := e.s.get(t, "a1"), e.owned(t)
		if a.Harness != "fa" || *a.HarnessSessionID != e.old.NativeID || a.SpecVersion != 1 || a.State != StateIdle {
			t.Fatalf("row changed: %+v", a)
		}
		if len(owned) != 2 || owned[1].NativeRoot != "/root/fb" {
			t.Fatalf("orphan not recorded for R29: %+v", owned)
		}
		if e.kinds(t, KindAskLost) != 1 || e.kinds(t, "item.completed") != 1 || e.kinds(t, KindHarnessChanged) != 0 {
			t.Fatal("want one ask.lost, kept partial output and no harness.changed")
		}
		if on := deliveredOn(t, delivered); on != e.old.NativeID {
			t.Fatalf("slot delivered on %s, want the old session", on)
		}
		noMore(t, delivered)
	})
	t.Run("failed start", func(t *testing.T) {
		e := newSwitchEnv(t, StateIdle)
		beforeSwitchCommit = func() { // another writer wins the spec version
			to := e.s.get(t, "a1").SpecOf()
			to.SpecVersion = 9
			if err := e.s.store.CompareAndSetSpec(ctx, "a1", 1, to); err != nil {
				t.Error(err)
			}
		}
		t.Cleanup(func() { beforeSwitchCommit = func() {} })
		_, err := e.s.Update(ctx, switchReq("r1", 1, "fb"))
		wantCode(t, err, CodeSpecVersionMismatch)
		if a, owned := e.s.get(t, "a1"), e.owned(t); a.Harness != "fa" || len(owned) != 2 || owned[1].Harness != "fb" {
			t.Fatalf("harness %s owned %+v", a.Harness, owned)
		}
		if e.kinds(t, KindError) != 1 || e.kinds(t, KindHarnessChanged) != 0 {
			t.Fatal("want one error event and no harness.changed")
		}
	})
}

// movedHarness returns a session whose Resume yields a new ref.
type movedHarness struct {
	*fake.Harness
	to loomharness.NativeRef
}

func (h movedHarness) Session(ref loomharness.NativeRef) loomharness.Session {
	return movedSession{h.Harness.Session(ref), h.to}
}

type movedSession struct {
	loomharness.Session
	to loomharness.NativeRef
}

func (s movedSession) Resume(context.Context, loomharness.Launch, []loomharness.PermissionRule) (loomharness.NativeRef, error) {
	return s.to, nil
}

func TestHarnessResumeRecordsReplacementNativeRef(t *testing.T) {
	ctx := context.Background()
	to := loomharness.NativeRef{Root: "/root/fa", NativeID: "fake_ses_new"}
	e := newSwitchEnv(t, StateIdle)
	e.s.harnesses["fa"] = movedHarness{e.fa, to}
	for range 2 { // replay-safe
		a, err := e.s.resume(ctx, e.s.get(t, "a1"))
		if err != nil || *a.HarnessSessionID != to.NativeID {
			t.Fatalf("resume = %v, %v", a.HarnessSessionID, err)
		}
	}
	owned := e.owned(t)
	if len(owned) != 2 || owned[0].NativeID != e.old.NativeID || owned[1].NativeID != to.NativeID ||
		e.s.get(t, "a1").SpecVersion != 1 {
		t.Fatalf("owned = %+v", owned)
	}
}

// TestHarnessResumeChangedRootSameID reproduces the batch-7 probe: Resume
// returns the same NativeID under a new root, which must become current,
// survive a retry and a reload, and keep the old ref owned.
func TestHarnessResumeChangedRootSameID(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateIdle)
	to := loomharness.NativeRef{Root: "/root/replaced", NativeID: e.old.NativeID}
	e.s.harnesses["fa"] = movedHarness{e.fa, to}
	for range 2 { // replay-safe
		if _, err := e.s.resume(ctx, e.s.get(t, "a1")); err != nil {
			t.Fatal(err)
		}
		if _, ref, err := e.s.current(ctx, e.s.get(t, "a1")); err != nil || ref != to {
			t.Fatalf("current = %+v, %v; want %+v", ref, err, to)
		}
	}
	e.s.harnesses["fa"] = movedHarness{e.fa, e.old} // and back to the older recorded root
	if _, err := e.s.resume(ctx, e.s.get(t, "a1")); err != nil {
		t.Fatal(err)
	}
	if _, ref, err := e.s.current(ctx, e.s.get(t, "a1")); err != nil || ref != e.old {
		t.Fatalf("current = %+v, %v; want %+v", ref, err, e.old)
	}
	if owned := e.owned(t); len(owned) != 2 || e.s.get(t, "a1").SpecVersion != 1 {
		t.Fatalf("owned = %+v", owned)
	}
	if got, err := e.s.Get(ctx, "a1"); err != nil || got.HarnessSessionRoot != nil {
		t.Fatalf("Get exposes the session root: %v, %v", got.HarnessSessionRoot, err)
	}
}

// TestUpdateEffortAppliesOnNextTurn: PATCH effort (or options) is checked
// against the harness catalog, saved in the spec and used by the session's
// next turn, never the one before; an unknown option or value is
// preset_invalid and changes nothing.
func TestUpdateEffortAppliesOnNextTurn(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateIdle)
	sess := e.fa.Session(e.old)
	if err := sess.Prompt(ctx, loomharness.Input{Key: "k1", Text: "before"}); err != nil {
		t.Fatal(err)
	}
	got, err := e.s.Update(ctx, UpdateRequest{Envelope: Envelope{RequestID: "r1"}, AgentID: "a1", Effort: "high"})
	if err != nil || got.SpecVersion != 2 {
		t.Fatalf("effort = %+v, %v", got, err)
	}
	cfg, err := loadConfig(e.s.get(t, "a1"))
	if err != nil || len(cfg.Options) != 1 || cfg.Options[0] != (loomharness.Option{ID: "effort", Value: "high"}) {
		t.Fatalf("saved options = %+v, %v", cfg.Options, err)
	}
	if err := sess.Prompt(ctx, loomharness.Input{Key: "k2", Text: "after"}); err != nil {
		t.Fatal(err)
	}
	turns := e.fa.Turns(e.old)
	if len(turns) != 2 || len(turns[0].Options) != 0 || turns[1].Model != "fake-model" ||
		loomharness.OptionValue(turns[1].Options, "effort") != "high" {
		t.Fatalf("turns = %+v; want the second turn on fake-model with effort high", turns)
	}

	for _, req := range []UpdateRequest{
		{AgentID: "a1", Effort: "ultra"},
		{AgentID: "a1", Options: []loomharness.Option{{ID: "speed", Value: "fast"}}},
	} {
		perr := wantCode(t, func() error { _, err := e.s.Update(ctx, req); return err }(), CodePresetInvalid)
		if perr.Message == "" || len(perr.Allowed) == 0 {
			t.Fatalf("%+v: error %+v lacks a message or the allowed values", req, perr)
		}
	}
	if a := e.s.get(t, "a1"); a.SpecVersion != 2 {
		t.Fatalf("a refused update changed the agent: spec version %d", a.SpecVersion)
	}
	if got, err = e.s.Update(ctx, UpdateRequest{AgentID: "a1", Options: []loomharness.Option{{ID: "effort", Value: "high"}}}); err != nil || got.SpecVersion != 2 {
		t.Fatalf("same effort again = %+v, %v; want a no-op", got, err)
	}
	_, err = e.s.Update(ctx, UpdateRequest{Envelope: Envelope{Expect: &Expect{SpecVersion: &got.SpecVersion}}, AgentID: "a1", Harness: "fb", Effort: "low"})
	wantCode(t, err, CodePresetInvalid)
	got, err = e.s.Update(ctx, switchReq("r2", 2, "fb"))
	if err != nil || got.Harness != "fb" {
		t.Fatalf("switch = %+v, %v", got, err)
	}
	if cfg, _ := loadConfig(e.s.get(t, "a1")); len(cfg.Options) != 0 {
		t.Fatalf("options survived the harness switch: %+v", cfg.Options)
	}
}

// TestUpdateModelAppliesOnNextTurn (SM1): a PATCHed model runs the next
// turn. Every hand-off resumes the session and sets the spec's saved model
// again, so that model must be the new one, not the one the agent was
// created with.
func TestUpdateModelAppliesOnNextTurn(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateIdle)
	a := e.s.get(t, "a1")
	to := a.SpecOf()
	to.SpecJSON = `{"Model":"fake-model"}` // as Create saves it
	if err := e.s.store.CompareAndSetSpec(ctx, "a1", a.SpecVersion, to); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Update(ctx, UpdateRequest{AgentID: "a1", Model: "fake/other"}); err != nil {
		t.Fatal(err)
	}
	queue(t, e.s, "a1", "user", "after")
	if _, err := e.s.handOff(ctx, e.s.get(t, "a1")); err != nil {
		t.Fatal(err)
	}
	if turns := e.fa.Turns(e.old); len(turns) != 1 || turns[0].Model != "fake/other" {
		t.Fatalf("turns = %+v; want one turn on fake/other", turns)
	}
}

// TestUpdateModelDropsCreateEffort (SM1): a model change keeps only the
// options the new model takes, the create override's effort included, and
// the next turn's reapply does not bring a dropped effort back.
func TestUpdateModelDropsCreateEffort(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateIdle)
	a := e.s.get(t, "a1")
	to := a.SpecOf()
	to.SpecJSON = `{"Model":"fake-model","Effort":"low"}` // a create override's effort
	if err := e.s.store.CompareAndSetSpec(ctx, "a1", a.SpecVersion, to); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Update(ctx, UpdateRequest{AgentID: "a1", Model: "fake/other"}); err != nil {
		t.Fatal(err)
	}
	queue(t, e.s, "a1", "user", "after")
	if _, err := e.s.handOff(ctx, e.s.get(t, "a1")); err != nil {
		t.Fatal(err)
	}
	if turns := e.fa.Turns(e.old); len(turns) != 1 || turns[0].Model != "fake/other" || len(turns[0].Options) != 0 {
		t.Fatalf("turns = %+v; want one turn on fake/other with no options", turns)
	}
}
