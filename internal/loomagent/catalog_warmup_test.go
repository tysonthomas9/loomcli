package loomagent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
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

// MC1: a catalog still empty after the wait is "not ready, retry", not an
// unknown model, and leaves no row.
func TestCreateCatalogNotReady(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s, _ := warmingService(e, 1<<30, 20*time.Millisecond)
	req := leadReq("r1")
	req.Overrides.Model = "fake-model"
	_, err := s.Create(ctx, req)
	if !isCode(err, CodeHarnessUnavailable) || !strings.Contains(err.Error(), "model catalog not ready") {
		t.Fatalf("create on an empty catalog = %v, want harness_unavailable 'model catalog not ready'", err)
	}
	if as, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true, IncludeDeleted: true}); len(as) != 0 {
		t.Fatalf("not-ready create left %d rows", len(as))
	}
}

// MC1: a loaded catalog still refuses an unknown model at once.
func TestCreateUnknownModelOnLoadedCatalogDoesNotWait(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s, w := warmingService(e, 0, time.Minute)
	req := leadReq("r1")
	req.Overrides.Model = "other"
	if _, err := s.Create(ctx, req); !isCode(err, CodePresetInvalid) || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("unknown model = %v, want preset_invalid", err)
	}
	if n := w.calls.Load(); n != 1 {
		t.Fatalf("unknown model on a loaded catalog listed it %d times, want 1", n)
	}
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
	if _, err := s.Create(ctx, req); !isCode(err, CodePresetInvalid) || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("create past the warm-up = %v, want preset_invalid unknown model", err)
	}
	if d := time.Since(start); d < 30*time.Millisecond || p.calls.Load() < 2 {
		t.Fatalf("returned after %s and %d calls; want a re-fetch for the warm-up window", d, p.calls.Load())
	}
	// Warm now: the next unknown model fails on its first listing.
	n := p.calls.Load()
	req = leadReq("r2")
	req.Name, req.Overrides.Model = "b", "other"
	if _, err := s.Create(ctx, req); !isCode(err, CodePresetInvalid) || p.calls.Load() != n+1 {
		t.Fatalf("unknown model on a warm harness = %v after %d calls, want preset_invalid at once", err, p.calls.Load()-n)
	}
}

// MC1: one create waits catalogWait at most, even inside the warm-up window,
// so it ends well before the API server's write timeout.
func TestCreateIncompleteCatalogWaitCapped(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s, _ := partialService(e, 1<<30, time.Hour)
	s.catalogWait = 20 * time.Millisecond
	req := leadReq("r1")
	req.Overrides.Model = "fake-model"
	start := time.Now()
	if _, err := s.Create(ctx, req); !isCode(err, CodePresetInvalid) || time.Since(start) > 5*time.Second {
		t.Fatalf("create = %v after %s, want unknown model after about catalogWait", err, time.Since(start))
	}
}
