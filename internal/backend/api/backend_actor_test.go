package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// --- ClaimIssueAsActor tests ---

func TestClaimIssueAsActor_SendsActorHeader(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/issues/test-1/claim") {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Actor"); got != "worker-a" {
			t.Errorf("X-Actor = %q, want worker-a", got)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := ab.ClaimIssueAsActor(context.Background(), "test-1", 0, "worker-a"); err != nil {
		t.Fatalf("ClaimIssueAsActor: %v", err)
	}
}

func TestClaimIssueAsActor_EmptyID(t *testing.T) {
	ab, _ := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	err := ab.ClaimIssueAsActor(context.Background(), "", 0, "worker-a")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

func TestClaimIssueAsActor_EmptyActor(t *testing.T) {
	ab, _ := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	err := ab.ClaimIssueAsActor(context.Background(), "test-1", 0, "")
	if err == nil {
		t.Fatal("expected error for empty actor")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

func TestClaimIssueAsActor_Conflict(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, http.StatusConflict, "issue already claimed by other-agent")
	})
	defer ts.Close()

	err := ab.ClaimIssueAsActor(context.Background(), "test-1", 0, "worker-a")
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("expected KindConflict, got %v", err)
	}
}

// --- ReleaseIssueAsActor tests ---

func TestReleaseIssueAsActor_SendsActorHeader(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/issues/test-1/release") {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Actor"); got != "worker-a" {
			t.Errorf("X-Actor = %q, want worker-a", got)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := ab.ReleaseIssueAsActor(context.Background(), "test-1", "worker-a"); err != nil {
		t.Fatalf("ReleaseIssueAsActor: %v", err)
	}
}

func TestReleaseIssueAsActor_EmptyID(t *testing.T) {
	ab, _ := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	err := ab.ReleaseIssueAsActor(context.Background(), "", "worker-a")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

func TestReleaseIssueAsActor_EmptyActor(t *testing.T) {
	ab, _ := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	err := ab.ReleaseIssueAsActor(context.Background(), "test-1", "")
	if err == nil {
		t.Fatal("expected error for empty actor")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

func TestReleaseIssueAsActor_Conflict(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, http.StatusConflict, "lock held by other-agent")
	})
	defer ts.Close()

	err := ab.ReleaseIssueAsActor(context.Background(), "test-1", "worker-a")
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("expected KindConflict, got %v", err)
	}
}

// --- Plain ClaimIssue must not leak an actor header ---

func TestClaimIssue_NoActorHeader(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.Header["X-Actor"]; present {
			t.Errorf("plain ClaimIssue must not send X-Actor, got %q", r.Header.Get("X-Actor"))
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := ab.ClaimIssue(context.Background(), "test-1", 0); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
}
