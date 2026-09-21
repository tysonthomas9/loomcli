package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend/advisoryactor"
)

const testVerifiedActor = "outsider@example.com"

func verifiedCtx(actor string) context.Context {
	return advisoryactor.WithVerified(context.Background(), actor)
}

// This is the authorisation boundary, and it turns on one fact: fleet-db
// answers "workspace access denied" for TWO different situations. For the
// open-mode operator identity it means "this label holds no role", and
// retrying as the process actor only restores attribution the operator never
// had. For an authenticated user it means "you are not authorised here" — and
// retrying THAT as the process actor would hand any signed-in user the process
// actor's privileges, which is a bypass rather than a fallback.
//
// The message is identical in both cases, so the backend cannot tell them
// apart. The caller can, and says so by stamping verified rather than
// advisory. These tests pin that the distinction survives.
func TestVerifiedActor_NoRoleDenialIsNotRetried(t *testing.T) {
	fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, n int, _ string) {
		if n == 1 {
			writeFleetDenial(w, http.StatusForbidden, "forbidden", "workspace access denied")
			return
		}
		// A second request means the fallback fired. Answer OK so the failure
		// is the actor list below rather than a confusing error.
		respondOK(w, json.RawMessage(`{}`))
	})
	defer closeFn()

	err := updateTitle(verifiedCtx(testVerifiedActor), fb, testVerifiedActor)
	if err == nil {
		t.Fatal("Update returned nil; a verified actor's denial must surface, not be retried as the process actor")
	}

	got := rec.seen()
	if len(got) != 1 {
		t.Fatalf("requests = %v, want exactly one (no retry for a verified identity)", got)
	}
	if got[0] != testVerifiedActor {
		t.Errorf("actor = %q, want %q", got[0], testVerifiedActor)
	}
	for _, a := range got {
		if a == testProcessActor {
			t.Fatalf("request was sent as the process actor %q; that is the bypass this test exists to prevent", testProcessActor)
		}
	}
}

// The same identity string, stamped advisory, DOES fall back. Without this the
// test above could pass because the fallback broke for everyone.
func TestVerifiedActor_SameIdentityStampedAdvisoryStillFallsBack(t *testing.T) {
	fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, n int, _ string) {
		if n == 1 {
			writeFleetDenial(w, http.StatusForbidden, "forbidden", "workspace access denied")
			return
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer closeFn()

	if err := updateTitle(advisoryCtx(testVerifiedActor), fb, testVerifiedActor); err != nil {
		t.Fatalf("Update returned %v, want nil — an advisory identity must still fall back", err)
	}
	got := rec.seen()
	if len(got) != 2 || got[1] != testProcessActor {
		t.Fatalf("actors = %v, want [%q %q]", got, testVerifiedActor, testProcessActor)
	}
}

// A verified denial must not poison the advisory denial cache either: the
// cache exists to skip a doomed first attempt, and a verified identity never
// had one to skip.
func TestVerifiedActor_DenialIsNotCached(t *testing.T) {
	fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, _ int, actor string) {
		if actor == testVerifiedActor {
			writeFleetDenial(w, http.StatusForbidden, "forbidden", "workspace access denied")
			return
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer closeFn()

	for i := 0; i < 2; i++ {
		if err := updateTitle(verifiedCtx(testVerifiedActor), fb, testVerifiedActor); err == nil {
			t.Fatalf("attempt %d: Update returned nil, want the denial surfaced", i+1)
		}
	}

	got := rec.seen()
	if len(got) != 2 {
		t.Fatalf("requests = %v, want two (one per call, neither skipped by the cache)", got)
	}
	for _, a := range got {
		if a != testVerifiedActor {
			t.Errorf("actor = %q, want every request as %q", a, testVerifiedActor)
		}
	}
}

// IsAdvisory must fail closed on every context that did not explicitly opt in.
func TestIsAdvisory_FailsClosed(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{"unstamped", context.Background(), false},
		{"nil context", nil, false},
		{"verified", advisoryactor.WithVerified(context.Background(), "u@example.com"), false},
		{"verified empty", advisoryactor.WithVerified(context.Background(), ""), false},
		{"advisory empty is not advisory", advisoryactor.With(context.Background(), ""), false},
		{"advisory", advisoryactor.With(context.Background(), "operator@local"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := advisoryactor.IsAdvisory(tc.ctx); got != tc.want {
				t.Errorf("IsAdvisory = %v, want %v", got, tc.want)
			}
		})
	}
}

// From must keep returning the actor for both stamps, or the verified identity
// is silently dropped and the write goes out as the process actor anyway —
// the same bypass by a different route.
func TestFrom_CarriesBothStamps(t *testing.T) {
	if got := advisoryactor.From(advisoryactor.WithVerified(context.Background(), "u@example.com")); got != "u@example.com" {
		t.Errorf("From(verified) = %q, want the actor carried", got)
	}
	if got := advisoryactor.From(advisoryactor.With(context.Background(), "operator@local")); got != "operator@local" {
		t.Errorf("From(advisory) = %q, want the actor carried", got)
	}
}
