package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// --- ReleaseIssueAsActor tests ---

func TestReleaseIssueAsActor_PostsReleaseWithActorHeader(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/issues/test-1/release-lock") {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Actor"); got != "desktopqa" {
			t.Errorf("X-Actor = %q, want desktopqa", got)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := fb.ReleaseIssueAsActor(context.Background(), "test-1", "desktopqa"); err != nil {
		t.Fatalf("ReleaseIssueAsActor: %v", err)
	}
}

func TestReleaseIssueAsActor_NoAssigneeLookup(t *testing.T) {
	// The actor-scoped release must POST /release-lock directly: no GET of the
	// issue to discover the current assignee.
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			t.Errorf("unexpected GET %s: actor-scoped release must not look up the assignee", r.URL.Path)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := fb.ReleaseIssueAsActor(context.Background(), "test-1", "worker-a"); err != nil {
		t.Fatalf("ReleaseIssueAsActor: %v", err)
	}
}

func TestReleaseIssueAsActor_EmptyID(t *testing.T) {
	fb, _ := New(Config{BaseURL: "http://x", WorkspaceID: "ws", AuthToken: "tok"})
	err := fb.ReleaseIssueAsActor(context.Background(), "", "worker-a")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

func TestReleaseIssueAsActor_EmptyActor(t *testing.T) {
	fb, _ := New(Config{BaseURL: "http://x", WorkspaceID: "ws", AuthToken: "tok"})
	err := fb.ReleaseIssueAsActor(context.Background(), "test-1", "")
	if err == nil {
		t.Fatal("expected error for empty actor")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

func TestReleaseIssueAsActor_Conflict(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, http.StatusConflict, "lock held by another worker")
	})
	defer ts.Close()

	err := fb.ReleaseIssueAsActor(context.Background(), "test-1", "worker-a")
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("expected KindConflict, got %v", err)
	}
}
