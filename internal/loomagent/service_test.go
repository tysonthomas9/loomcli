package loomagent

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

func newService(t *testing.T, cfg ServiceConfig, agents ...loomstore.Agent) *Service {
	t.Helper()
	st, err := loomstore.Open(context.Background(), filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, a := range agents {
		if err := st.InsertAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Store, cfg.Events = st, NewEventLog(st)
	return New(cfg)
}

func svcAgent(id, mode, state string) loomstore.Agent {
	return loomstore.Agent{AgentID: id, WorkspaceID: "ws", Name: id, ProfileKey: id, Preset: "lead",
		PresetVersion: "1", Mode: mode, InteractionMode: "interactive", RoleKind: "interactive", SpecJSON: "{}",
		SpecVersion: 1, OwnerKind: "user", OwnerID: "u", CreatedByKind: "user", CreatedByID: "u",
		CreateRequestID: "req-" + id, Repo: "/repo", Harness: "fake", State: state, Attempt: 1}
}

func sp(s string) *string { return &s }

func (s *Service) get(t *testing.T, id string) loomstore.Agent {
	t.Helper()
	a, err := s.store.GetAgent(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// drain returns the events already queued on sub.
func drain(sub *BusSubscription) []Event {
	var out []Event
	for {
		select {
		case e, ok := <-sub.C:
			if !ok {
				return out
			}
			out = append(out, e)
		default:
			return out
		}
	}
}

func types(es []Event) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Type)
	}
	return out
}

func TestStateCompareAndSetPublishesAfterCommitOnce(t *testing.T) {
	ctx := context.Background()
	a := svcAgent("a1", "persistent", StateActive)
	a.RunningTurnID = sp("t1")
	s := newService(t, ServiceConfig{}, a)
	sub := s.Bus.Subscribe("a1")

	// Two writers race from the same snapshot; exactly one commits and publishes.
	snap := s.get(t, "a1")
	to := snap.StateOf()
	to.State, to.RunningTurn = StateIdle, nil
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); _, errs[i] = s.setState(ctx, snap, to) }()
	}
	wg.Wait()
	ok, lost := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, loomstore.ErrStateChanged):
			lost++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || lost != 1 {
		t.Fatalf("ok=%d lost=%d", ok, lost)
	}

	e := <-sub.C // published only after the commit
	if got := s.get(t, "a1"); got.State != StateIdle || got.RunningTurnID != nil {
		t.Fatalf("event %s before commit: row %+v", e.Type, got)
	}
	evs := append([]Event{e}, drain(sub)...)
	if got := types(evs); len(got) != 2 || got[0] != EventStateChanged || got[1] != EventIdle {
		t.Fatalf("events = %v", got)
	}
	if evs[0].From != StateActive || evs[0].To != StateIdle || evs[1].TurnID != "t1" {
		t.Fatalf("events = %+v", evs)
	}

	// An idle persistent agent is not settled; idle -> archived skips stopping.
	to = s.get(t, "a1").StateOf()
	to.State = StateArchived
	if _, err := s.setState(ctx, s.get(t, "a1"), to); err == nil {
		t.Fatal("idle -> archived allowed")
	}
	if got := drain(sub); len(got) != 0 {
		t.Fatalf("unexpected events %v", types(got))
	}
}

func TestStateSingleTaskSettlesOnce(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, svcAgent("t1", "single_task", StateActive))
	sub := s.Bus.Subscribe()
	a := s.get(t, "t1")
	to := a.StateOf()
	to.State, to.Outcome = StateFinished, sp("completed")
	a, err := s.setState(ctx, a, to)
	if err != nil {
		t.Fatal(err)
	}
	if a, err = s.raiseAttention(ctx, a, "delivery_unknown"); err != nil {
		t.Fatal(err)
	}
	evs := drain(sub)
	if got := types(evs); len(got) != 3 || got[0] != EventStateChanged || got[1] != EventSettled ||
		got[2] != EventAttentionRaised {
		t.Fatalf("events = %v", got) // no idle for a single task; settled only once
	}
	if evs[1].Reason != "finished" || evs[1].Outcome != "completed" || evs[1].Attempt != 1 {
		t.Fatalf("settled = %+v", evs[1])
	}
	// A Send starts the next attempt: finished -> active.
	to = a.StateOf()
	to.State, to.Outcome, to.Attempt = StateActive, nil, 2
	if _, err := s.setState(ctx, a, to); err != nil {
		t.Fatal(err)
	}
}

func TestAttentionRaiseAndClear(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, svcAgent("a1", "persistent", StateIdle))
	sub := s.Bus.Subscribe("a1")
	stale := s.get(t, "a1")
	a, err := s.raiseAttention(ctx, stale, "harness_unavailable")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.get(t, "a1"); deref(got.AttentionReason) != "harness_unavailable" || got.State != StateIdle {
		t.Fatalf("row = %+v", got)
	}
	if _, err := s.raiseAttention(ctx, stale, "session_missing"); !errors.Is(err, loomstore.ErrStateChanged) {
		t.Fatalf("stale raise = %v", err)
	}
	if _, err := s.clearAttention(ctx, a); err != nil {
		t.Fatal(err)
	}
	evs := drain(sub)
	if got := types(evs); len(got) != 3 || got[0] != EventAttentionRaised || got[1] != EventSettled ||
		got[2] != EventAttentionCleared {
		t.Fatalf("events = %v", got)
	}
	if evs[0].Reason != "harness_unavailable" || evs[1].Reason != "attention" || evs[2].Reason != "harness_unavailable" {
		t.Fatalf("events = %+v", evs)
	}
	if got := s.get(t, "a1"); got.AttentionReason != nil {
		t.Fatalf("attention not cleared: %+v", got)
	}
}

func TestStateHandOverStagesSkillsFirst(t *testing.T) {
	ctx := context.Background()
	var staged []string
	failStage := false
	cfg := ServiceConfig{
		PrepareWorktree: func(_ context.Context, target Target, a loomstore.Agent) error {
			staged = append(staged, string(target)+":"+a.AgentID)
			if failStage {
				return errors.New("stage failed")
			}
			return nil
		},
		ResolveRepo: func(_ context.Context, target Target, repo string) (string, error) {
			return string(target) + ":" + repo, nil
		},
	}
	s := newService(t, cfg, svcAgent("a1", "persistent", StateActive))
	if p, err := s.repoPath(ctx, "o/r"); err != nil || p != "local:o/r" {
		t.Fatalf("repoPath = %q, %v", p, err)
	}
	send := func(req string) {
		_, _, err := s.store.Send(ctx, loomstore.SlotSend{AgentID: "a1", Sender: "user", RequestID: req, Body: "hi",
			Source: "ui", Result: func(bool) (string, error) { return "{}", nil }})
		if err != nil {
			t.Fatal(err)
		}
	}
	key := func(sl loomstore.Slot) string { return "k-" + sl.RequestID }
	a := s.get(t, "a1")

	send("r1")
	failStage = true
	if _, err := s.handOver(ctx, a, key); err == nil {
		t.Fatal("handed over after failed staging")
	}
	if sl, _ := s.store.Slots(ctx, "a1"); len(sl) != 1 || sl[0].State != loomstore.SlotWaiting {
		t.Fatalf("slots = %+v", sl)
	}
	failStage = false
	sl, err := s.handOver(ctx, a, key)
	if err != nil || sl.State != loomstore.SlotHanded || deref(sl.NativeKey) != "k-r1" {
		t.Fatalf("handOver = %+v, %v", sl, err)
	}
	if len(staged) != 2 || staged[1] != "local:a1" {
		t.Fatalf("staged = %v", staged)
	}
}

func TestStateAgentLockOrdersWrites(t *testing.T) {
	s := newService(t, ServiceConfig{})
	n := 0
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer s.lock("a1")()
			v := n
			time.Sleep(time.Microsecond)
			n = v + 1
		}()
	}
	wg.Wait()
	if n != 50 {
		t.Fatalf("n = %d", n)
	}
}

func TestBusSlowSubscriberLagged(t *testing.T) {
	b := NewBus()
	slow := b.Subscribe("a1")
	other := b.Subscribe("b1")
	for range busBuffer + 1 {
		b.publish(Event{AgentID: "a1", Type: EventStateChanged})
	}
	if got := len(drain(slow)); got != busBuffer {
		t.Fatalf("slow got %d", got)
	}
	if _, ok := <-slow.C; ok {
		t.Fatal("slow subscriber still open")
	}
	var le *Error
	if !errors.As(slow.Err(), &le) || le.Code != CodeSubscriberLagged {
		t.Fatalf("slow err = %v", slow.Err())
	}
	b.publish(Event{AgentID: "b1", Type: EventIdle})
	if e := <-other.C; e.Type != EventIdle {
		t.Fatalf("other got %+v", e)
	}
	b.Unsubscribe(other)
	if _, ok := <-other.C; ok || other.Err() != nil {
		t.Fatalf("unsubscribed: ok=%v err=%v", ok, other.Err())
	}
}
