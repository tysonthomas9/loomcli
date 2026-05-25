package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// ReleaseClaim tests (LOOM-1).

// TestReleaseClaim_PostsToReleaseLockEndpoint asserts the wire shape: a GET to
// fetch the current assignee, followed by a POST /issues/<id>/release-lock with
// the actor header set from the issue's current assignee. ReleaseClaim is
// lock-only, so it must hit /release-lock, not the status-reverting /release.
func TestReleaseClaim_PostsToReleaseLockEndpoint(t *testing.T) {
	var sawRelease bool
	var releaseActor string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, types.Issue{
				ID:        "test-1",
				Title:     "T",
				Status:    types.StatusInProgress,
				Assignee:  "planner",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/deps"):
			respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"):
			respondOK(w, []interface{}{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/release-lock"):
			sawRelease = true
			releaseActor = r.Header.Get("X-Actor")
			respondOK(w, json.RawMessage(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	if err := fb.ReleaseClaim(context.Background(), "test-1"); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	if !sawRelease {
		t.Fatal("expected /release-lock endpoint to be called")
	}
	if releaseActor != "planner" {
		t.Errorf("X-Actor = %q, want %q (the issue's current assignee)", releaseActor, "planner")
	}
}

// TestReleaseClaim_EmptyAssigneeFastPath asserts no /release-lock is issued when
// the issue has no current assignee — nothing to release, so it's a no-op.
func TestReleaseClaim_EmptyAssigneeFastPath(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, types.Issue{
				ID:        "test-1",
				Title:     "T",
				Status:    types.StatusOpen,
				Assignee:  "",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/deps"):
			respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"):
			respondOK(w, []interface{}{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/release-lock"):
			t.Fatalf("did not expect /release-lock to be called when assignee is empty")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	if err := fb.ReleaseClaim(context.Background(), "test-1"); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
}

// TestReleaseClaim_PropagatesServerError asserts non-success HTTP responses
// from /release-lock surface back to the caller (so the loom-complete helper
// can log them) rather than being silently swallowed by the fleet layer.
func TestReleaseClaim_PropagatesServerError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, types.Issue{
				ID:        "test-1",
				Title:     "T",
				Status:    types.StatusInProgress,
				Assignee:  "planner",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/deps"):
			respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"):
			respondOK(w, []interface{}{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/release-lock"):
			respondErr(w, http.StatusConflict, "lock held by someone else")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	err := fb.ReleaseClaim(context.Background(), "test-1")
	if err == nil {
		t.Fatal("expected error from /release-lock 409, got nil")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Errorf("expected KindConflict, got %v", err)
	}
}

// TestReleaseClaim_EmptyIDValidationError asserts the same shape used by
// ClaimIssue: empty id returns KindValidation without touching the wire.
func TestReleaseClaim_EmptyIDValidationError(t *testing.T) {
	fb, _ := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	err := fb.ReleaseClaim(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

// Update review/blocked transition lock-leak fix (LOOM-1).

// TestUpdate_ReviewOrBlockedFromInProgress_ReleasesClaim asserts that moving a
// claimed (in_progress) issue to review or blocked with --assignee="" drops the
// claim lock first (POST /release-lock as the current assignee — the lock
// holder), PATCHes the target status, and then clears the assignee via /assign
// "". /release-lock is lock-only, so the assignee is cleared by the explicit
// assign step rather than as a release side effect. Releasing as "planner"
// proves the assign was deferred — had it run first, the holder identity would
// be gone and the lock would leak.
func TestUpdate_ReviewOrBlockedFromInProgress_ReleasesClaim(t *testing.T) {
	for _, target := range []string{"review", "blocked"} {
		t.Run(target, func(t *testing.T) {
			var sawReleaseLock, sawPatch, sawAssign bool
			var releaseActor, patchedStatus, assignedTo string
			fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
					respondOK(w, types.Issue{
						ID:        "test-1",
						Title:     "T",
						Status:    types.StatusInProgress,
						Assignee:  "planner",
						CreatedAt: time.Now(),
						UpdatedAt: time.Now(),
					})
				case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/deps"):
					respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
				case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"):
					respondOK(w, []interface{}{})
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/release-lock"):
					sawReleaseLock = true
					releaseActor = r.Header.Get("X-Actor")
					respondOK(w, json.RawMessage(`{}`))
				case r.Method == "PATCH" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
					sawPatch = true
					var body struct {
						Status string `json:"status"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatalf("decode patch body: %v", err)
					}
					patchedStatus = body.Status
					respondOK(w, json.RawMessage(`{}`))
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/assign"):
					sawAssign = true
					var body struct {
						Assignee string `json:"assignee"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatalf("decode assign body: %v", err)
					}
					assignedTo = body.Assignee
					respondOK(w, json.RawMessage(`{}`))
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			})
			defer ts.Close()

			status := target
			empty := ""
			if err := fb.Update(context.Background(), "test-1",
				backend.UpdateParams{Status: &status, Assignee: &empty}); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if !sawReleaseLock {
				t.Error("expected /release-lock to be called to drop the claim lock")
			}
			if releaseActor != "planner" {
				t.Errorf("release-lock X-Actor = %q, want %q (the lock holder; proves the assign was deferred)", releaseActor, "planner")
			}
			if !sawPatch || patchedStatus != target {
				t.Errorf("expected PATCH status=%q; sawPatch=%v patchedStatus=%q", target, sawPatch, patchedStatus)
			}
			if !sawAssign || assignedTo != "" {
				t.Errorf(`expected /assign "" to clear the assignee (lock-only release leaves it); sawAssign=%v assignedTo=%q`, sawAssign, assignedTo)
			}
		})
	}
}

// TestUpdate_ReviewFromInProgress_NoAssigneeChange_PreservesAssignee pins the
// new behavior: moving a claimed issue to review WITHOUT --assignee drops the
// lock (lock-only) and sets the status, but leaves the assignee in place — no
// /assign is issued. The old /release reverted to open and unassigned as a side
// effect; /release-lock does not, and we only assign when the caller asks.
func TestUpdate_ReviewFromInProgress_NoAssigneeChange_PreservesAssignee(t *testing.T) {
	var sawReleaseLock, sawPatch, sawAssign bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, types.Issue{
				ID:        "test-1",
				Title:     "T",
				Status:    types.StatusInProgress,
				Assignee:  "planner",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/deps"):
			respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"):
			respondOK(w, []interface{}{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/release-lock"):
			sawReleaseLock = true
			respondOK(w, json.RawMessage(`{}`))
		case r.Method == "PATCH" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			sawPatch = true
			respondOK(w, json.RawMessage(`{}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/assign"):
			sawAssign = true
			respondOK(w, json.RawMessage(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	status := "review"
	if err := fb.Update(context.Background(), "test-1",
		backend.UpdateParams{Status: &status}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !sawReleaseLock {
		t.Error("expected /release-lock to drop the claim lock")
	}
	if !sawPatch {
		t.Error("expected PATCH to set status=review")
	}
	if sawAssign {
		t.Error("did not expect /assign: no assignee change requested, so the assignee must be preserved")
	}
}

// TestUpdate_ReviewFromOpen_NoRelease asserts the negative: moving an issue to
// review when it is NOT in_progress (no claim held) only PATCHes the status —
// there is nothing to release, so no /release-lock call is issued.
func TestUpdate_ReviewFromOpen_NoRelease(t *testing.T) {
	var sawReleaseLock, sawPatch bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, types.Issue{
				ID:        "test-1",
				Title:     "T",
				Status:    types.StatusOpen,
				Assignee:  "",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/deps"):
			respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"):
			respondOK(w, []interface{}{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/release-lock"):
			sawReleaseLock = true
			respondOK(w, json.RawMessage(`{}`))
		case r.Method == "PATCH" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			sawPatch = true
			respondOK(w, json.RawMessage(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	status := "review"
	if err := fb.Update(context.Background(), "test-1", backend.UpdateParams{Status: &status}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if sawReleaseLock {
		t.Error("did not expect /release-lock when the issue is not in_progress")
	}
	if !sawPatch {
		t.Error("expected PATCH to set status=review")
	}
}
