package loomagent

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// warming lists no models for its first empty calls, as OpenCode does for
// a moment after its service boots, and then the wrapped catalog.
type warming struct {
	*openRec
	empty int64
	calls atomic.Int64
}

func (w *warming) Models(ctx context.Context) ([]loomharness.Model, error) {
	if w.calls.Add(1) <= w.empty {
		return []loomharness.Model{}, nil
	}
	return w.openRec.Models(ctx)
}

func warmingService(e *createEnv, empty int64, wait time.Duration) (*Service, *warming) {
	w := &warming{openRec: e.h, empty: empty}
	s := e.service(ServiceConfig{})
	s.harnesses["opencode"], s.catalogWait, s.catalogPoll = w, wait, time.Millisecond
	return s, w
}

// MC1: a create sent while the catalog is still loading waits for it.
func TestCreateWaitsForCatalogWarmUp(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s, w := warmingService(e, 3, time.Minute)
	req := leadReq("r1")
	req.Overrides.Model = "fake-model"
	a, err := s.Create(ctx, req)
	if err != nil || deref(a.Model) != "fake-model" || w.calls.Load() < 4 {
		t.Fatalf("create during warm-up = %v, %v after %d catalog calls", deref(a.Model), err, w.calls.Load())
	}
}

// wantUnverified fails unless a was created with model passed through to
// the harness, flagged unverified with one model.unverified event (MCS1).
func wantUnverified(t *testing.T, e *createEnv, a AgentInfo, err error, model string) {
	t.Helper()
	if err != nil || deref(a.Model) != model || !a.ModelUnverified {
		t.Fatalf("create = %s unverified=%v, %v; want %s accepted unverified", deref(a.Model), a.ModelUnverified, err, model)
	}
	if n := e.events(t, a.AgentID, KindModelUnverified); n != 1 {
		t.Fatalf("model.unverified events = %d, want 1", n)
	}
	if got := e.h.specs[len(e.h.specs)-1].Model; got != model {
		t.Fatalf("harness opened with model %q, want %q", got, model)
	}
}

// MCS1: a catalog still empty after the wait no longer fails the create
// (MC1's 503): the model is accepted unverified and the harness decides.
func TestCreateCatalogNotReady(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s, _ := warmingService(e, 1<<30, 20*time.Millisecond)
	req := leadReq("r1")
	req.Overrides.Model = "fake-model"
	a, err := s.Create(ctx, req)
	wantUnverified(t, e, a, err, "fake-model")
}

// MCS1: a loaded catalog that lacks the model accepts it at once, unverified;
// a listed model is not flagged.
func TestCreateUnknownModelOnLoadedCatalogDoesNotWait(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s, w := warmingService(e, 0, time.Minute)
	req := leadReq("r1")
	req.Overrides.Model = "openai/other"
	a, err := s.Create(ctx, req)
	wantUnverified(t, e, a, err, "openai/other")
	if n := w.calls.Load(); n != 1 {
		t.Fatalf("unknown model on a loaded catalog listed it %d times, want 1", n)
	}
	req = leadReq("r2")
	req.Name, req.Overrides.Model = "b", "fake-model"
	if b, err := s.Create(ctx, req); err != nil || b.ModelUnverified || e.events(t, b.AgentID, KindModelUnverified) != 0 {
		t.Fatalf("listed model = unverified %v, %v", b.ModelUnverified, err)
	}
}

// MCS1: only a malformed model id is still refused, with no row.
func TestCreateMalformedModelIs400(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s, _ := warmingService(e, 0, time.Minute)
	for i, m := range []string{" ", "fake model", "openai/", "/gpt", "a//b", "tab\tid"} {
		req := leadReq("r" + strconv.Itoa(i))
		req.Overrides.Model = m
		if _, err := s.Create(ctx, req); !isCode(err, CodePresetInvalid) || !strings.Contains(err.Error(), "malformed model") {
			t.Fatalf("model %q = %v, want preset_invalid malformed", m, err)
		}
	}
	if as, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true, IncludeDeleted: true}); len(as) != 0 {
		t.Fatalf("malformed creates left %d rows", len(as))
	}
}

// MCS1: when the harness refuses an unverified model on a turn, the turn
// ends as a normal failed turn naming the model, and the agent stays usable.
func TestUnverifiedModelRefusedOnTurn(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	stop := runFeed(t, s, "opencode")
	defer stop()
	req := leadReq("r1")
	req.Overrides.Model = "openai/bogus"
	info, err := s.Create(ctx, req)
	wantUnverified(t, e, info, err, "openai/bogus")
	fh.Script(info.AgentID, fake.Turn{Steps: []fake.Step{{Fail: `model "openai/bogus" not found`}}})
	mustSendMsg(t, s, sendReq(info.AgentID, "u1", "hi", user))
	done := func() []loomstore.Event { return kinds(rows(t, s, info.AgentID, 0), EventTurnCompleted) }
	drained(t, s, "the turn fails", func() bool { return len(done()) == 1 })
	var p struct{ StopReason, Error string }
	if err := json.Unmarshal(done()[0].Payload, &p); err != nil || p.StopReason != "failed" || !strings.Contains(p.Error, "openai/bogus") {
		t.Fatalf("turn_completed payload = %+v %v; want a failed turn naming the model", p, err)
	}
	drained(t, s, "the agent is idle", func() bool { return s.get(t, info.AgentID).State == StateIdle })
	if _, err := s.Update(ctx, UpdateRequest{AgentID: info.AgentID, Model: "fake-model"}); err != nil {
		t.Fatalf("switch to a listed model: %v", err)
	}
	if a, _ := s.Get(ctx, info.AgentID); a.ModelUnverified {
		t.Fatal("a listed model is still flagged unverified")
	}
	mustSendMsg(t, s, sendReq(info.AgentID, "u2", "again", user))
	drained(t, s, "the next turn completes", func() bool { return len(done()) == 2 })
}

// partial lists only early-model for its first calls, as OpenCode does
// while its providers are still loading after a start, then the full catalog.
type partial struct {
	*openRec
	early int64
	calls atomic.Int64
}

func (p *partial) Models(ctx context.Context) ([]loomharness.Model, error) {
	if p.calls.Add(1) <= p.early {
		return []loomharness.Model{{ID: "early-model"}}, nil
	}
	return p.openRec.Models(ctx)
}

func partialService(e *createEnv, early int64, warmUp time.Duration) (*Service, *partial) {
	p := &partial{openRec: e.h, early: early}
	s := e.service(ServiceConfig{CatalogWarmUp: warmUp})
	s.harnesses["opencode"], s.catalogPoll = p, time.Millisecond
	return s, p
}

// MC1: a create right after the harness starts, while its catalog is still
// incomplete, re-fetches until the model appears.
func TestCreateWaitsForIncompleteCatalog(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s, p := partialService(e, 3, time.Minute)
	req := leadReq("r1")
	req.Overrides.Model = "fake-model"
	a, err := s.Create(ctx, req)
	if err != nil || deref(a.Model) != "fake-model" || p.calls.Load() < 4 {
		t.Fatalf("create on an incomplete catalog = %v, %v after %d catalog calls", deref(a.Model), err, p.calls.Load())
	}
}

// MC1: the re-fetch is bounded by the warm-up window from the harness's
// first listing; after it a missing model is unknown at once.
func TestCreateIncompleteCatalogWarmUpBounded(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s, p := partialService(e, 1<<30, 30*time.Millisecond)
	req := leadReq("r1")
	req.Overrides.Model = "fake-model"
	start := time.Now()
	a, err := s.Create(ctx, req)
	wantUnverified(t, e, a, err, "fake-model")
	if d := time.Since(start); d < 30*time.Millisecond || p.calls.Load() < 2 {
		t.Fatalf("returned after %s and %d calls; want a re-fetch for the warm-up window", d, p.calls.Load())
	}
	// Warm now: the next unknown model passes unverified on its first listing.
	n := p.calls.Load()
	req = leadReq("r2")
	req.Name, req.Overrides.Model = "b", "other"
	a, err = s.Create(ctx, req)
	wantUnverified(t, e, a, err, "other")
	if p.calls.Load() != n+1 {
		t.Fatalf("unknown model on a warm harness listed %d times, want once", p.calls.Load()-n)
	}
}

// MC1: one create waits catalogWait at most, even inside the warm-up window,
// so it ends well before the API server's write timeout; then (MCS1) the
// model is accepted unverified.
func TestCreateIncompleteCatalogWaitCapped(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s, _ := partialService(e, 1<<30, time.Hour)
	s.catalogWait = 20 * time.Millisecond
	req := leadReq("r1")
	req.Overrides.Model = "fake-model"
	start := time.Now()
	a, err := s.Create(ctx, req)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("create took %s, want about catalogWait", time.Since(start))
	}
	wantUnverified(t, e, a, err, "fake-model")
}

// hung never answers a catalog listing until its context ends.
type hung struct{ *openRec }

func (hung) Models(ctx context.Context) ([]loomharness.Model, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// MC1: a hung catalog listing is bounded by catalogWait too; then (MCS1)
// the model is accepted unverified.
func TestCreateHungCatalogNotReady(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	s.harnesses["opencode"], s.catalogWait = hung{e.h}, 20*time.Millisecond
	req := leadReq("r1")
	req.Overrides.Model = "fake-model"
	type result struct {
		a   AgentInfo
		err error
	}
	done := make(chan result, 1)
	go func() { a, err := s.Create(ctx, req); done <- result{a, err} }()
	select {
	case r := <-done:
		wantUnverified(t, e, r.a, r.err, "fake-model")
	case <-time.After(5 * time.Second):
		t.Fatal("create on a hung catalog did not return within 5s")
	}
}

// nilCatalog is a wired harness that lists no models as a nil list.
type nilCatalog struct{ *openRec }

func (nilCatalog) Models(context.Context) ([]loomharness.Model, error) { return nil, nil }

// MCS1: a wired harness listing a nil catalog is an empty list, not an
// unwired one: Create and Update accept an unlisted model with the warning.
func TestNilCatalogModelUnverified(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	s.harnesses["opencode"], s.catalogWait = nilCatalog{e.h}, 20*time.Millisecond
	req := leadReq("r1")
	req.Overrides.Model = "openai/bogus"
	a, err := s.Create(ctx, req)
	wantUnverified(t, e, a, err, "openai/bogus")

	b, err := s.Create(ctx, CreateRequest{Envelope: Envelope{RequestID: "r2"}, Preset: "lead", Name: "b", Repo: "/repo",
		BaseRef: "main", Overrides: Overrides{Harness: "opencode"}})
	if err != nil || b.ModelUnverified {
		t.Fatalf("create with no model = unverified %v, %v", b.ModelUnverified, err)
	}
	b, err = s.Update(ctx, UpdateRequest{AgentID: b.AgentID, Model: "openai/other"})
	if err != nil || deref(b.Model) != "openai/other" || !b.ModelUnverified {
		t.Fatalf("update = %s unverified=%v, %v; want openai/other unverified", deref(b.Model), b.ModelUnverified, err)
	}
	if n := e.events(t, b.AgentID, KindModelUnverified); n != 1 {
		t.Fatalf("model.unverified events after update = %d, want 1", n)
	}
}
