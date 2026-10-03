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
