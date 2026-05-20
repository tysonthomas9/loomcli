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

// TestReleaseClaim_PostsToReleaseEndpoint asserts the wire shape: a GET to
// fetch the current assignee, followed by a POST /issues/<id>/release with
// the actor header set from the issue's current assignee.
func TestReleaseClaim_PostsToReleaseEndpoint(t *testing.T) {
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
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/release"):
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
		t.Fatal("expected /release endpoint to be called")
	}
	if releaseActor != "planner" {
		t.Errorf("X-Actor = %q, want %q (the issue's current assignee)", releaseActor, "planner")
	}
}

// TestReleaseClaim_EmptyAssigneeFastPath asserts no /release is issued when
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
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/release"):
			t.Fatalf("did not expect /release to be called when assignee is empty")
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
// from /release surface back to the caller (so the loom-complete helper can
// log them) rather than being silently swallowed by the fleet layer.
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
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/release"):
			respondErr(w, http.StatusConflict, "lock held by someone else")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	err := fb.ReleaseClaim(context.Background(), "test-1")
	if err == nil {
		t.Fatal("expected error from /release 409, got nil")
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
