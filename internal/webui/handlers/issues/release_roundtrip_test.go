package issues

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// serveWithReleaseRoute mounts the real issue routes on a real mux in front of
// a recording service, and returns an APIBackend pointed at it — i.e. exactly
// the production topology a worker sees with LOOM_SERVER_URL set: worker →
// APIBackend → loom serve → IssueService.
func serveWithReleaseRoute(t *testing.T, svc service.IssueService) *api.APIBackend {
	t.Helper()

	mux := http.NewServeMux()
	NewIssueModule(svc, nil).Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	ab, err := api.New(api.Config{BaseURL: ts.URL, WorkspaceID: "test-ws"})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return ab
}

// The bug this closes: with LOOM_SERVER_URL set, a worker's release went
// through APIBackend, which had no serve-side release route to call and
// answered KindNotImplemented. The supervisor took that as "fall back to TTL
// expiry" and moved on, so the claim stayed in_progress until the lock aged
// out — the orphaned-claim stall.
//
// This is the whole point of the route, so assert the loop actually closes:
// the release must arrive at the service on the other side of the HTTP hop,
// carrying the worker's identity, and must report success only when it did.
func TestReleaseRoundTrip_ReachesTheServiceThroughServe(t *testing.T) {
	var got service.ReleaseIssueParams
	var calls int
	svc := &mockIssueService{
		releaseIssueFunc: func(_ context.Context, params service.ReleaseIssueParams) error {
			calls++
			got = params
			return nil
		},
	}
	ab := serveWithReleaseRoute(t, svc)

	if err := ab.ReleaseIssueAsActor(t.Context(), "T-1", "worker-2"); err != nil {
		t.Fatalf("ReleaseIssueAsActor: %v", err)
	}

	if calls != 1 {
		t.Fatalf("service saw %d releases, want exactly 1 — a release that never reaches the service is the silent drop this route exists to fix", calls)
	}
	if got.IssueID != "T-1" {
		t.Errorf("IssueID = %q, want T-1", got.IssueID)
	}
	if got.Actor != "worker-2" {
		t.Errorf("Actor = %q, want worker-2 — without it the release is unscoped and can un-claim a live sibling", got.Actor)
	}
}

// The regression guard for the old behaviour: this call used to return
// KindNotImplemented no matter what, which is indistinguishable at the call
// site from "serve has no release route" and is what made the supervisor stop
// trying.
func TestReleaseRoundTrip_NoLongerReportsNotImplemented(t *testing.T) {
	ab := serveWithReleaseRoute(t, &mockIssueService{})

	err := ab.ReleaseIssueAsActor(t.Context(), "T-1", "worker-2")
	if backend.IsKind(err, backend.KindNotImplemented) {
		t.Fatalf("err = %v; serve now exposes the release route, so the client must stop answering KindNotImplemented", err)
	}
	if err != nil {
		t.Fatalf("ReleaseIssueAsActor: %v", err)
	}
}

// A lock held by someone else must surface as a conflict all the way back to
// the caller. Reporting success here would tell a recovering supervisor that
// it freed a lock a live sibling is still working under.
func TestReleaseRoundTrip_ConflictSurvivesTheHop(t *testing.T) {
	svc := &mockIssueService{
		releaseIssueFunc: func(context.Context, service.ReleaseIssueParams) error {
			return service.ErrConflict("issue is locked by another actor")
		},
	}
	ab := serveWithReleaseRoute(t, svc)

	err := ab.ReleaseIssueAsActor(t.Context(), "T-1", "worker-2")
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("err = %v, want KindConflict so the caller leaves the task claimed", err)
	}
}

// An older serve has no /release route. That must not read as success, and it
// must be distinguishable from a transient failure: it is the one case where
// degrading to the unscoped status transition is legitimate.
//
// A bare net/http mux answers a missing route with plain text, which the
// client's envelope parser cannot read — so this only works if the 404 is
// recognised by status code. Getting it wrong lands the case in
// KindUnavailable, where a caller correctly treats it as transient and never
// releases at all.
func TestReleaseRoundTrip_OlderServeReportsNotImplemented(t *testing.T) {
	ts := httptest.NewServer(http.NewServeMux()) // no routes registered
	t.Cleanup(ts.Close)
	ab, err := api.New(api.Config{BaseURL: ts.URL, WorkspaceID: "test-ws"})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}

	relErr := ab.ReleaseIssueAsActor(t.Context(), "T-1", "worker-2")
	if relErr == nil {
		t.Fatal("release against a serve with no route reported success")
	}
	if !backend.IsKind(relErr, backend.KindNotImplemented) {
		t.Fatalf("err = %v, want KindNotImplemented so the caller degrades to the legacy status transition", relErr)
	}
}

// The handler half: X-Actor validation applies to release exactly as it does
// to claim, and a rejected header must not fall through to an unscoped
// release.
func TestHandleReleaseIssue_RejectsInvalidActorHeader(t *testing.T) {
	called := false
	svc := &mockIssueService{
		releaseIssueFunc: func(context.Context, service.ReleaseIssueParams) error {
			called = true
			return nil
		},
	}
	h := HandleReleaseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/T-1/release", nil)
	req.SetPathValue("id", "T-1")
	req.Header["X-Actor"] = []string{"worker-3\nX-Injected: yes"}
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if called {
		t.Error("service was called with a rejected actor; an unscoped release can un-claim a live sibling")
	}
}

func TestHandleReleaseIssue_MissingIssueID(t *testing.T) {
	h := HandleReleaseIssue(&mockIssueService{})

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues//release", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
