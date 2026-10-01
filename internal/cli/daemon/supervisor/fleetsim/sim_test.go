package fleetsim

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

var t0 = time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)

func newSeeded(t *testing.T, g Guards) *Sim {
	t.Helper()
	s := New(t0, "LOCALMODE", g)
	s.Server.Seed(Issue{ID: "LOCALMODE-3", Title: "t", Design: "d", Priority: 2})
	return s
}

// Server semantics follow the pinned FleetDB source: lock-first claim with
// existing_owner meta on conflict, holder-scoped release-lock (204 when no
// lock, 409 for a different live holder), lock TTL default 300s, and a reaper
// that respects the 60s grace and a live lock.
func TestServerClaimLockAndReaperSemantics(t *testing.T) {
	s := newSeeded(t, Guards{})
	a := s.Backend("a", "")
	b := s.Backend("b", "")
	var errB error
	s.Go("a", func() error { return a.ClaimIssueAsActor(context.Background(), "LOCALMODE-3", 0, "alpha") })
	s.DrainFIFO()
	if err := s.Err("a"); err != nil {
		t.Fatalf("alpha claim: %v", err)
	}
	s.Go("b", func() error {
		errB = b.ClaimIssueAsActor(context.Background(), "LOCALMODE-3", 0, "beta")
		return nil
	})
	s.DrainFIFO()
	if !backend.IsKind(errB, backend.KindConflict) {
		t.Fatalf("beta claim err = %v, want conflict", errB)
	}
	var be *backend.BackendError
	if !errors.As(errB, &be) || be.Meta["existing_owner"] != "alpha" {
		t.Fatalf("conflict meta = %#v, want existing_owner=alpha", be)
	}

	s.Clock.Advance(DefaultLockTTL - time.Second)
	if got := s.Server.ReapStaleClaims(); len(got) != 0 {
		t.Fatalf("reaped %v while lock live", got)
	}
	s.Clock.Advance(time.Second)
	if h := s.Server.LockHolder("LOCALMODE-3"); h != "" {
		t.Fatalf("lock holder after TTL = %q", h)
	}
	if got := s.Server.ReapStaleClaims(); len(got) != 1 {
		t.Fatalf("reaped %v, want LOCALMODE-3", got)
	}
	is, _, _ := s.Server.Snapshot("LOCALMODE-3")
	if is.Status != "open" || is.Assignee != "" {
		t.Fatalf("after reap: %+v", is)
	}
	ev := s.Server.Events()
	if last := ev[len(ev)-1]; last.Actor != "system" || last.Reason != "lock_expired" {
		t.Fatalf("reap event = %+v", last)
	}

	var relErr error
	s.Go("rel", func() error {
		relErr = a.ReleaseIssueLock(context.Background(), "LOCALMODE-3", "alpha")
		return nil
	})
	s.DrainFIFO()
	if relErr != nil {
		t.Fatalf("release-lock with no lock = %v, want nil (204)", relErr)
	}
	if st := s.Records()[len(s.Records())-1].Status; st != 204 {
		t.Fatalf("release-lock status = %d, want 204", st)
	}
}

// The interposer can apply a request and lose its response, or drop it
// unapplied, and the real adapter surfaces both as errors.
func TestInterposerDeliveryModes(t *testing.T) {
	s := newSeeded(t, Guards{})
	w := NewScriptedWorker(s, "w", "operator@local", CloseStep("LOCALMODE-3", "sess", "done"))
	var code int
	s.Go("worker", func() error {
		var err error
		code, err = w.Run(context.Background())
		return err
	})
	if _, ok := s.DeliverNext(Req("w", "POST", "/assign"), Drop, time.Time{}); !ok {
		t.Fatal("no pending assign")
	}
	rec, ok := s.DeliverNext(Req("w", "POST", "/close"), ApplyLoseResponse, time.Time{})
	if !ok {
		t.Fatal("no pending close (Close must proceed after best-effort assign failure)")
	}
	if rec.Status != 200 {
		t.Fatalf("close applied status = %d", rec.Status)
	}
	if !s.Finished("worker") || code != 1 || !errors.Is(s.Err("worker"), ErrResponseLost) {
		t.Fatalf("worker code=%d err=%v, want 1/ErrResponseLost", code, s.Err("worker"))
	}
	is, _, _ := s.Server.Snapshot("LOCALMODE-3")
	if is.Status != "closed" {
		t.Fatalf("server status = %q, want closed although client saw an error", is.Status)
	}
	recs := s.Records()
	if recs[0].Delivery != Drop || recs[0].Status != 0 || recs[1].Delivery != ApplyLoseResponse {
		t.Fatalf("records = %+v", recs)
	}
	if len(s.Server.Unmodeled()) != 0 {
		t.Fatalf("unmodeled routes: %v", s.Server.Unmodeled())
	}
}

func TestCompareWritesReportsDivergence(t *testing.T) {
	run := ObservedRuns[1]
	recs := []Record{{Method: "POST", Path: "/issues/LOCALMODE-3/claim", IssueID: "LOCALMODE-3", Actor: "local-coder2", Status: 200, AppliedAt: run.Writes[0].At}}
	diffs := CompareWrites(run, recs)
	if len(diffs) == 0 || !strings.Contains(diffs[0], "write count") {
		t.Fatalf("diffs = %v, want write-count divergence", diffs)
	}
}
