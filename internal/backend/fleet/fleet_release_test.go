package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// ReleaseClaim tests (LOOM-1).

// TestReleaseClaim_InProgressPostsToReleaseEndpoint asserts the wire shape for
// a planner that exits while the issue is still in_progress: a GET to confirm
// the supplied actor still owns the issue, followed by POST /issues/<id>/release
// as that actor. This both drops the lock and returns the issue to open so a
// downstream worker can claim it immediately.
func TestReleaseClaim_InProgressPostsToReleaseEndpoint(t *testing.T) {
	var sawRelease bool
	var releaseActor string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, testIssue{
				ID:        "test-1",
				Title:     "T",
				Status:    workitems.StatusInProgress,
				Assignee:  "planner",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/deps"):
			respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"):
			respondOK(w, []interface{}{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/release"):
			sawRelease = true
			releaseActor = r.Header.Get("X-Actor")
			respondOK(w, json.RawMessage(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	if err := fb.ReleaseClaim(context.Background(), "test-1", "planner"); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	if !sawRelease {
		t.Fatal("expected /release endpoint to be called")
	}
	if releaseActor != "planner" {
		t.Errorf("X-Actor = %q, want %q (the supplied completing actor)", releaseActor, "planner")
	}
}

func TestReleaseClaim_ReviewPostsToReleaseLockEndpoint(t *testing.T) {
	var sawReleaseLock bool
	var releaseActor string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, testIssue{
				ID:        "test-1",
				Title:     "T",
				Status:    workitems.StatusReview,
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
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	if err := fb.ReleaseClaim(context.Background(), "test-1", "planner"); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	if !sawReleaseLock {
		t.Fatal("expected /release-lock endpoint to be called")
	}
	if releaseActor != "planner" {
		t.Errorf("X-Actor = %q, want planner", releaseActor)
	}
}

func TestReleaseClaim_DifferentAssigneeIsNoop(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, testIssue{
				ID:        "test-1",
				Title:     "T",
				Status:    workitems.StatusInProgress,
				Assignee:  "new-worker",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/deps"):
			respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"):
			respondOK(w, []interface{}{})
		case r.Method == "POST":
			t.Fatalf("stale actor must not release or reset new owner: %s", r.URL.Path)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	if err := fb.ReleaseClaim(context.Background(), "test-1", "planner"); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
}

// TestReleaseClaim_EmptyAssigneeFastPath asserts no release endpoint is issued
// when the issue has no current assignee — nothing to release, so it's a no-op.
func TestReleaseClaim_EmptyAssigneeFastPath(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, testIssue{
				ID:        "test-1",
				Title:     "T",
				Status:    workitems.StatusOpen,
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

	if err := fb.ReleaseClaim(context.Background(), "test-1", "planner"); err != nil {
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
			respondOK(w, testIssue{
				ID:        "test-1",
				Title:     "T",
				Status:    workitems.StatusReview,
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

	err := fb.ReleaseClaim(context.Background(), "test-1", "planner")
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
	err := fb.ReleaseClaim(context.Background(), "", "planner")
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
					respondOK(w, testIssue{
						ID:        "test-1",
						Title:     "T",
						Status:    workitems.StatusInProgress,
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
			respondOK(w, testIssue{
				ID:        "test-1",
				Title:     "T",
				Status:    workitems.StatusInProgress,
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
			respondOK(w, testIssue{
				ID:        "test-1",
				Title:     "T",
				Status:    workitems.StatusOpen,
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

// Quarantine write shape (supervisor task-quarantine).

// TestUpdate_QuarantineShape_OpenToBlockedWithLabelAndUnassign pins the exact
// request decomposition for the supervisor's quarantine write — one Update
// with {Status: blocked, Assignee: "", AddLabels: [loom:quarantined]} against
// an OPEN issue: label POST -> status PATCH -> assign "". No /release-lock is
// issued from open; the in_progress race path (sibling claims the task in the
// open->blocked gap) is already pinned by
// TestUpdate_ReviewOrBlockedFromInProgress_ReleasesClaim above.
func TestUpdate_QuarantineShape_OpenToBlockedWithLabelAndUnassign(t *testing.T) {
	var mutations []string
	var labeled bool
	var labelAdded, patchedStatus, assignedTo string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-q1"):
			var labels []string
			if labeled { // waitForLabelState polls until the label projects
				labels = []string{"loom:quarantined"}
			}
			respondOK(w, testIssue{
				ID:        "test-q1",
				Title:     "stalled task",
				Status:    workitems.StatusOpen,
				Labels:    labels,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-q1/deps"):
			respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-q1/comments"):
			respondOK(w, []interface{}{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-q1/labels"):
			mutations = append(mutations, "label")
			labeled = true
			var body struct {
				Label string `json:"label"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode label body: %v", err)
			}
			labelAdded = body.Label
			respondOK(w, json.RawMessage(`{}`))
		case r.Method == "PATCH" && strings.HasSuffix(r.URL.Path, "/issues/test-q1"):
			mutations = append(mutations, "status")
			var body struct {
				Status string `json:"status"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode patch body: %v", err)
			}
			patchedStatus = body.Status
			respondOK(w, json.RawMessage(`{}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-q1/release-lock"):
			t.Error("unexpected /release-lock: nothing holds a claim on an open issue")
			respondOK(w, json.RawMessage(`{}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-q1/assign"):
			mutations = append(mutations, "assign")
			var body struct {
				Assignee string `json:"assignee"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode assign body: %v", err)
			}
			assignedTo = body.Assignee
			respondOK(w, json.RawMessage(`{}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			respondErr(w, http.StatusNotFound, "unexpected")
		}
	})
	defer ts.Close()

	blocked := "blocked"
	empty := ""
	if err := fb.Update(context.Background(), "test-q1", backend.UpdateParams{
		Status:    &blocked,
		Assignee:  &empty,
		AddLabels: []string{"loom:quarantined"},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got, want := strings.Join(mutations, ">"), "label>status>assign"; got != want {
		t.Errorf("mutation order = %q, want %q", got, want)
	}
	if labelAdded != "loom:quarantined" {
		t.Errorf("label added = %q, want loom:quarantined", labelAdded)
	}
	if patchedStatus != "blocked" {
		t.Errorf("patched status = %q, want blocked", patchedStatus)
	}
	if assignedTo != "" {
		t.Errorf("assigned to = %q, want explicit empty (unassign)", assignedTo)
	}
}
