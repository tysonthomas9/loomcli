package fake

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"

	lh "github.com/tysonthomas9/loomcli/internal/loomharness"
)

var ctx = context.Background()

func fixture(t *testing.T, name string) Turn {
	t.Helper()
	for _, f := range Fixtures {
		if f.Name == name {
			return f.Turn
		}
	}
	t.Fatalf("no fixture %q", name)
	return Turn{}
}

// start opens agent key under root /p/<key> with turns queued and a live feed.
func start(t *testing.T, h *Harness, key string, turns ...Turn) (lh.NativeRef, lh.Session, lh.Feed) {
	t.Helper()
	h.Script(key, turns...)
	ref, err := h.Open(ctx, lh.OpenSpec{Key: key, Launch: lh.Launch{Root: "/p/" + key}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := h.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ref, h.Session(ref), f
}

// drain returns the event types waiting on f; closed reports a closed feed.
func drain(f lh.Feed) (types []lh.EventType, closed bool) {
	for {
		select {
		case e, ok := <-f.Events():
			if !ok {
				return types, true
			}
			types = append(types, e.Type)
		default:
			return types, false
		}
	}
}

func want(t *testing.T, f lh.Feed, exp ...lh.EventType) {
	t.Helper()
	if got, _ := drain(f); !reflect.DeepEqual(got, exp) {
		t.Fatalf("events = %v; want %v", got, exp)
	}
}

func status(t *testing.T, s lh.Session) lh.Status {
	t.Helper()
	st, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestFakeTurnAndHasInput(t *testing.T) {
	h := New()
	ref, s, f := start(t, h, "agt_1", fixture(t, "streamed_reply"))
	if again, _ := h.Open(ctx, lh.OpenSpec{Key: "agt_1"}); again != ref {
		t.Fatalf("Open not idempotent: %v vs %v", again, ref)
	}
	if err := s.Prompt(ctx, lh.Input{Key: "m1", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	want(t, f, lh.EventMessageDelivered, lh.EventTurnStarted, lh.EventDelta, lh.EventDelta, lh.EventTurnCompleted)
	if st := status(t, s); st.Running || st.LastTurnInterrupt {
		t.Fatalf("status = %+v; want idle", st)
	}
	for key, exp := range map[string]lh.Landed{"m1": lh.LandedFound, "never": lh.LandedNotFound} {
		if got, _ := s.HasInput(ctx, key); got != exp {
			t.Fatalf("HasInput(%s) = %s; want %s", key, got, exp)
		}
	}
}

func TestFakeAskBusyAndReply(t *testing.T) {
	h := New()
	_, s, f := start(t, h, "agt_1", fixture(t, "ask_then_reply"))
	if err := s.Prompt(ctx, lh.Input{Key: "m1"}); err != nil {
		t.Fatal(err)
	}
	want(t, f, lh.EventMessageDelivered, lh.EventTurnStarted, lh.EventAskOpened)
	if st := status(t, s); !st.Running || st.TurnID == "" {
		t.Fatalf("status = %+v; want running", st)
	}
	if err := s.Prompt(ctx, lh.Input{Key: "m2"}); !errors.Is(err, lh.ErrBusy) {
		t.Fatalf("Prompt mid-turn = %v; want ErrBusy", err)
	}
	if err := s.Move(ctx, "/w2"); !errors.Is(err, lh.ErrBusy) {
		t.Fatalf("Move mid-turn = %v; want ErrBusy", err)
	}
	if err := s.Reply(ctx, "other", lh.Reply{Allow: true}); err == nil {
		t.Fatal("Reply to a wrong ask succeeded")
	}
	if err := s.Reply(ctx, "ask_1", lh.Reply{Allow: true}); err != nil {
		t.Fatal(err)
	}
	want(t, f, lh.EventAskResolved, lh.EventDelta, lh.EventTurnCompleted)
}

func TestFakeInterrupt(t *testing.T) {
	h := New()
	_, s, f := start(t, h, "agt_1", fixture(t, "ask_then_reply"))
	_ = s.Prompt(ctx, lh.Input{Key: "m1"})
	drain(f)
	if ok, err := s.Interrupt(ctx); !ok || err != nil {
		t.Fatalf("Interrupt = %v, %v", ok, err)
	}
	want(t, f, lh.EventAskLost, lh.EventTurnCompleted)
	if st := status(t, s); st.Running || !st.LastTurnInterrupt {
		t.Fatalf("status = %+v; want idle, interrupted", st)
	}
	if ok, _ := s.Interrupt(ctx); ok {
		t.Fatal("Interrupt on an idle session reported true")
	}
}

func TestFakeCrashRestartResume(t *testing.T) {
	h := New()
	ref, s, f := start(t, h, "agt_1", fixture(t, "ask_then_reply"))
	_ = s.Prompt(ctx, lh.Input{Key: "m1"})
	drain(f)
	h.Crash()
	if _, closed := drain(f); !closed {
		t.Fatal("feed still open after a crash")
	}
	if _, err := s.Status(ctx); !errors.Is(err, lh.ErrUnavailable) {
		t.Fatalf("Status while down = %v; want ErrUnavailable", err)
	}
	if hl, _ := h.Health(ctx); hl.OK {
		t.Fatal("Health OK while down")
	}
	if err := h.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	f, _ = h.Feed(ctx)
	if err := s.Prompt(ctx, lh.Input{Key: "m2"}); !errors.Is(err, lh.ErrBusy) {
		t.Fatalf("Prompt before Resume = %v; want ErrBusy", err)
	}
	if _, err := s.Resume(ctx, lh.Launch{Root: "/elsewhere"}, nil); !errors.Is(err, lh.ErrSessionNotFound) {
		t.Fatalf("Resume under another root = %v; want ErrSessionNotFound", err)
	}
	got, err := s.Resume(ctx, lh.Launch{Root: ref.Root}, nil)
	if err != nil || got != ref {
		t.Fatalf("Resume = %v, %v; want %v", got, err, ref)
	}
	want(t, f, lh.EventAskLost, lh.EventTurnCompleted)
	if st := status(t, s); st.Running || !st.LastTurnInterrupt {
		t.Fatalf("status = %+v; want idle, interrupted", st)
	}
	if err := s.Prompt(ctx, lh.Input{Key: "m2"}); err != nil {
		t.Fatal(err)
	}
	if st := status(t, s); st.LastTurnInterrupt {
		t.Fatal("a new turn kept the interrupted flag")
	}
}

func TestFakeResumeContinuesTurn(t *testing.T) {
	h := New()
	ref, s, f := start(t, h, "agt_1", fixture(t, "crash_mid_turn_resumed"))
	_ = s.Prompt(ctx, lh.Input{Key: "m1"})
	if got, closed := drain(f); !closed || len(got) != 3 {
		t.Fatalf("events before the crash = %v (closed %v)", got, closed)
	}
	_ = h.Restart(ctx)
	f, _ = h.Feed(ctx)
	if _, err := s.Resume(ctx, lh.Launch{Root: ref.Root}, nil); err != nil {
		t.Fatal(err)
	}
	want(t, f, lh.EventTurnResumed, lh.EventDelta, lh.EventTurnCompleted)
	if got, _ := s.HasInput(ctx, "m1"); got != lh.LandedFound {
		t.Fatalf("HasInput = %s", got)
	}
}

func TestFakeHasInputAfterCrashAtDelivery(t *testing.T) {
	for name, exp := range map[string]lh.Landed{
		"input_lost_before_delivery": lh.LandedNotFound,
		"input_delivery_unknown":     lh.LandedUnknown,
	} {
		h := New()
		_, s, f := start(t, h, "agt_1", fixture(t, name))
		if err := s.Prompt(ctx, lh.Input{Key: "m1"}); err != nil {
			t.Fatal(err)
		}
		if got, closed := drain(f); !closed || len(got) != 0 {
			t.Fatalf("%s: events = %v, closed %v; want none, closed", name, got, closed)
		}
		_ = h.Restart(ctx)
		if got, err := s.HasInput(ctx, "m1"); got != exp || err != nil {
			t.Fatalf("%s: HasInput = %s, %v; want %s", name, got, err, exp)
		}
	}
}

func TestFakeFeedGapAndCatchUp(t *testing.T) {
	h := New()
	_, s, f := start(t, h, "agt_1", fixture(t, "feed_gap"))
	_ = s.Prompt(ctx, lh.Input{Key: "m1"})
	var gapSeq int64
	var live []string
	for len(f.Events()) > 0 {
		e := <-f.Events()
		if e.Type == lh.EventFeedGap {
			gapSeq = e.Seq
		}
		if e.Type == lh.EventDelta {
			live = append(live, e.Text)
		}
	}
	if gapSeq == 0 || !reflect.DeepEqual(live, []string{"a", "c"}) {
		t.Fatalf("live deltas %v, gap after seq %d", live, gapSeq)
	}
	page, err := s.Messages(ctx, strconv.FormatInt(gapSeq, 10), 1)
	if err != nil || len(page.Events) != 1 || page.Events[0].Text != "b" || page.Next == "" {
		t.Fatalf("catch-up page = %+v, %v; want the missed delta b and a next cursor", page, err)
	}
	if page.Events[0].ItemID != page.Events[0].TurnID+"/msg" {
		t.Fatalf("delta ItemID %q is not the turn's message item", page.Events[0].ItemID)
	}
	rest, _ := s.Messages(ctx, page.Next, 0)
	if len(rest.Events) != 2 || rest.Next != "" {
		t.Fatalf("rest = %+v; want delta c and turn.completed", rest)
	}
}

func TestFakePurgeExactRefs(t *testing.T) {
	h := New()
	a, sa, _ := start(t, h, "agt_a")
	_, sb, _ := start(t, h, "agt_b")
	if err := h.Purge(ctx, []lh.NativeRef{a, {Root: "/p/agt_b", NativeID: "not_owned"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.Status(ctx); !errors.Is(err, lh.ErrSessionNotFound) {
		t.Fatalf("purged session Status = %v", err)
	}
	if _, err := sb.Status(ctx); err != nil {
		t.Fatalf("unpurged session Status = %v", err)
	}
}

func TestFakeFixturesKinds(t *testing.T) {
	seen := map[string]bool{}
	for _, fx := range Fixtures {
		if seen[fx.Name] || (fx.Kind != Orchestration && fx.Kind != ProviderContract) {
			t.Fatalf("fixture %q: duplicate or bad kind %q", fx.Name, fx.Kind)
		}
		seen[fx.Name] = true
		h := New()
		_, s, _ := start(t, h, "agt_1", fx.Turn)
		if err := s.Prompt(ctx, lh.Input{Key: "m1"}); err != nil {
			t.Fatalf("%s: %v", fx.Name, err)
		}
	}
}

func TestFakeResumeContinuesLosesOpenAsk(t *testing.T) {
	h := New()
	ref, s, f := start(t, h, "agt_1", Turn{Steps: []Step{{Ask: "ask_1"}, {Delta: "b"}}, ResumeContinues: true})
	_ = s.Prompt(ctx, lh.Input{Key: "m1"})
	drain(f)
	h.Crash()
	_ = h.Restart(ctx)
	f, _ = h.Feed(ctx)
	if _, err := s.Resume(ctx, lh.Launch{Root: ref.Root}, nil); err != nil {
		t.Fatal(err)
	}
	want(t, f, lh.EventAskLost, lh.EventTurnResumed, lh.EventDelta, lh.EventTurnCompleted)
	if err := s.Reply(ctx, "ask_1", lh.Reply{Allow: true}); err == nil {
		t.Fatal("Reply to a pre-crash ask succeeded")
	}
}

func TestFakeCloseKeepsHistoryUntilResume(t *testing.T) {
	h := New()
	ref, s, f := start(t, h, "agt_1", fixture(t, "streamed_reply"), fixture(t, "ask_then_reply"))
	_ = s.Prompt(ctx, lh.Input{Key: "m1"})
	_ = s.Prompt(ctx, lh.Input{Key: "m2"})
	drain(f)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Prompt(ctx, lh.Input{Key: "m3"}); !errors.Is(err, lh.ErrUnavailable) {
		t.Fatalf("Prompt after Close = %v; want ErrUnavailable", err)
	}
	if err := s.Reply(ctx, "ask_1", lh.Reply{Allow: true}); !errors.Is(err, lh.ErrUnavailable) {
		t.Fatalf("Reply after Close = %v; want ErrUnavailable", err)
	}
	if ok, err := s.Interrupt(ctx); ok || !errors.Is(err, lh.ErrUnavailable) {
		t.Fatalf("Interrupt after Close = %v, %v", ok, err)
	}
	if err := s.SetModel(ctx, "m", nil); !errors.Is(err, lh.ErrUnavailable) {
		t.Fatalf("SetModel after Close = %v", err)
	}
	if st := status(t, s); st.Running {
		t.Fatal("closed session reports running")
	}
	if got, _ := s.HasInput(ctx, "m1"); got != lh.LandedFound {
		t.Fatalf("HasInput after Close = %s", got)
	}
	if page, err := s.Messages(ctx, "", 0); err != nil || len(page.Events) == 0 {
		t.Fatalf("history after Close = %+v, %v", page, err)
	}
	if _, err := s.Resume(ctx, lh.Launch{Root: ref.Root}, nil); err != nil {
		t.Fatal(err)
	}
	want(t, f, lh.EventAskLost, lh.EventTurnCompleted)
	if err := s.Prompt(ctx, lh.Input{Key: "m3"}); err != nil {
		t.Fatalf("Prompt after Resume = %v", err)
	}
}

func TestFakeResumeInstallsRulesBeforeTheTurnContinues(t *testing.T) {
	h := New()
	old := []lh.PermissionRule{{Action: "bash", Resource: "*", Effect: "allow"}}
	cur := append(slices.Clone(old), lh.PermissionRule{Action: "bash", Resource: "gh *", Effect: "deny"})
	ref, s, f := start(t, h, "agt_1", Turn{Steps: []Step{{Delta: "a"}, {Crash: true}, {Delta: "b"}}, ResumeContinues: true})
	if _, err := h.Open(ctx, lh.OpenSpec{Key: "agt_1", Rules: old}); err != nil { // an Open repeat installs too
		t.Fatal(err)
	}
	_ = s.Prompt(ctx, lh.Input{Key: "m1"})
	drain(f)
	_ = h.Restart(ctx)

	h.FailInstall(errors.New("permissions not installed"))
	if _, err := s.Resume(ctx, lh.Launch{Root: ref.Root}, cur); err == nil {
		t.Fatal("Resume succeeded without installing its rules")
	}
	if installed, turns := h.Rules(ref); !slices.Equal(installed, old) || len(turns) != 1 {
		t.Fatalf("a failed install changed the session: rules %v, runs %d", installed, len(turns))
	}
	if err := s.Prompt(ctx, lh.Input{Key: "m2"}); err == nil {
		t.Fatal("the session accepted a prompt after a failed Resume")
	}

	h.FailInstall(nil)
	if _, err := s.Resume(ctx, lh.Launch{Root: ref.Root}, cur); err != nil {
		t.Fatal(err)
	}
	installed, turns := h.Rules(ref)
	if !slices.Equal(installed, cur) || len(turns) != 2 || !slices.Equal(turns[0], old) || !slices.Equal(turns[1], cur) {
		t.Fatalf("installed %v, runs %v; the resumed turn must run under the new rules", installed, turns)
	}
}
