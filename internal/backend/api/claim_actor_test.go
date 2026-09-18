package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// The defect this closes: driver.claimIssue only uses the actor-scoped claim
// when the backend implements it, so an APIBackend without this method made
// every sibling worker claim as serve's single configured actor. Satisfying
// the interface is therefore load-bearing, not incidental — assert it.
func TestAPIBackend_SatisfiesActorCapabilities(t *testing.T) {
	// Compile-time is the real guard: the claim path selects the actor-scoped
	// call by type assertion, so losing these methods silently reverts to
	// every sibling claiming as serve itself.
	var _ backend.ActorClaimer = (*APIBackend)(nil)
	var _ backend.ActorReleaser = (*APIBackend)(nil)
}

func TestAPIBackend_ClaimIssueAsActor_ForwardsIdentity(t *testing.T) {
	var gotActor, gotPath, gotMethod string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotActor = r.Header.Get("X-Actor")
		gotPath = r.URL.Path
		gotMethod = r.Method
		respondOK(w, map[string]string{"id": "T-1"})
	})
	defer ts.Close()

	if err := ab.ClaimIssueAsActor(t.Context(), "T-1", 30*time.Second, "worker-2"); err != nil {
		t.Fatalf("ClaimIssueAsActor: %v", err)
	}
	if gotActor != "worker-2" {
		t.Errorf("X-Actor = %q, want worker-2 — without it fleet-db attributes the lock to serve", gotActor)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/workspaces/test-ws/issues/T-1/claim" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
}

func TestAPIBackend_ClaimIssueAsActor_Validation(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, map[string]string{})
	})
	defer ts.Close()

	// An empty actor must fail loudly rather than silently claiming as serve —
	// that silent path is the bug.
	if err := ab.ClaimIssueAsActor(t.Context(), "T-1", 0, ""); !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("empty actor: err = %v, want validation", err)
	}
	if err := ab.ClaimIssueAsActor(t.Context(), "", 0, "worker-1"); !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("empty id: err = %v, want validation", err)
	}
}

// A conflict must stay a conflict: the driver's fan-out loop skips to the next
// ready issue on KindConflict, which is how arbitration produces one winner.
func TestAPIBackend_ClaimIssueAsActor_ConflictSurfaces(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"success":false,"error":"already claimed by worker-1"}`))
	})
	defer ts.Close()

	err := ab.ClaimIssueAsActor(t.Context(), "T-1", 0, "worker-2")
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("err = %v, want KindConflict so the driver moves to the next ready issue", err)
	}
}

// Release now has a route to call. Answering KindNotImplemented was honest
// while serve had none, but it is what made the supervisor give up and wait
// for TTL expiry, leaving the claim in_progress in the meantime — so the
// identity has to reach the release endpoint the same way it reaches claim.
func TestAPIBackend_ReleaseIssueAsActor_ForwardsIdentity(t *testing.T) {
	var gotActor, gotPath, gotMethod string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotActor = r.Header.Get("X-Actor")
		gotPath = r.URL.Path
		gotMethod = r.Method
		respondOK(w, map[string]string{})
	})
	defer ts.Close()

	if err := ab.ReleaseIssueAsActor(t.Context(), "T-1", "worker-2"); err != nil {
		t.Fatalf("ReleaseIssueAsActor: %v", err)
	}
	if gotActor != "worker-2" {
		t.Errorf("X-Actor = %q, want worker-2 — an unscoped release can free a lock a sibling still holds", gotActor)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/workspaces/test-ws/issues/T-1/release" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
}

func TestAPIBackend_ReleaseIssueAsActor_Validation(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, map[string]string{})
	})
	defer ts.Close()

	// An empty actor must fail loudly rather than silently releasing as serve:
	// an unscoped release is how a stopped duplicate un-claimed a live sibling.
	if err := ab.ReleaseIssueAsActor(t.Context(), "T-1", ""); !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("empty actor: err = %v, want validation", err)
	}
	if err := ab.ReleaseIssueAsActor(t.Context(), "", "worker-1"); !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("empty id: err = %v, want validation", err)
	}
}

// Releasing a lock held by someone else is a conflict. The caller has to be
// able to tell that apart from success and leave the task claimed.
func TestAPIBackend_ReleaseIssueAsActor_ConflictSurfaces(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"success":false,"error":"lock held by worker-1"}`))
	})
	defer ts.Close()

	err := ab.ReleaseIssueAsActor(t.Context(), "T-1", "worker-2")
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("err = %v, want KindConflict so the caller leaves the task claimed", err)
	}
}

// An older serve has no /release route: an unregistered pattern is answered by
// net/http's mux in plain text and never reaches a handler. "This server
// cannot release" is what the caller needs — it degrades to the legacy status
// transition on not-implemented but must leave the task claimed on a transient
// error — so that case must not land in KindUnavailable with the other
// unparseable responses.
func TestAPIBackend_ReleaseIssueAsActor_NoRouteReportsNotImplemented(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 page not found\n"))
	})
	defer ts.Close()

	err := ab.ReleaseIssueAsActor(t.Context(), "T-1", "worker-2")
	if !backend.IsKind(err, backend.KindNotImplemented) {
		t.Fatalf("err = %v, want KindNotImplemented", err)
	}
}

// A 404 that does carry the API envelope came from the release handler itself,
// so it means the issue is missing rather than the route.
func TestAPIBackend_ReleaseIssueAsActor_MissingIssueStaysNotFound(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"error":"issue not found"}`))
	})
	defer ts.Close()

	err := ab.ReleaseIssueAsActor(t.Context(), "T-1", "worker-2")
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Fatalf("err = %v, want KindNotFound", err)
	}
}

// ReleaseIssueLock is the actor-less lock-only release and stays
// not-implemented: this client has no way to name the lock's owner, and a
// silent success would report a lock freed when it was not.
func TestAPIBackend_ReleaseIssueLock_StaysNotImplemented(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, map[string]string{})
	})
	defer ts.Close()

	err := ab.ReleaseIssueLock(t.Context(), "T-1", "worker-2")
	if !backend.IsKind(err, backend.KindNotImplemented) {
		t.Fatalf("err = %v, want KindNotImplemented", err)
	}
}
