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

// Release stays honestly not-implemented on this client: the supervisor falls
// back to TTL expiry, which is correct, whereas a silent success would claim a
// lock was freed when it was not.
func TestAPIBackend_ReleaseIssueAsActor_HonestlyNotImplemented(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, map[string]string{})
	})
	defer ts.Close()

	err := ab.ReleaseIssueAsActor(t.Context(), "T-1", "worker-2")
	if !backend.IsKind(err, backend.KindNotImplemented) {
		t.Fatalf("err = %v, want KindNotImplemented", err)
	}
}
