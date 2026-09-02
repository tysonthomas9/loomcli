package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestArchive_HappyPath(t *testing.T) {
	var archivePosted bool
	var claimReleased bool
	var gotReason string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/archive") {
			archivePosted = true
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			gotReason = body["reason"]
			respondOK(w, json.RawMessage(`{"id":"test-1","status":"tombstone"}`))
			return
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/assign") {
			claimReleased = true
			respondOK(w, json.RawMessage(`{}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	})
	defer ts.Close()

	if err := fb.Archive(context.Background(), "test-1", backend.ArchiveParams{Reason: "superseded"}); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if !archivePosted {
		t.Error("expected POST /issues/{id}/archive to fire")
	}
	if gotReason != "superseded" {
		t.Errorf("archive reason = %q, want %q", gotReason, "superseded")
	}
	if !claimReleased {
		t.Error("expected archive to release the stale assignee claim")
	}
}

func TestArchive_EmptyID(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request for empty id: %s %s", r.Method, r.URL.Path)
	})
	defer ts.Close()

	err := fb.Archive(context.Background(), "", backend.ArchiveParams{})
	if err == nil {
		t.Fatal("Archive with empty id: want error, got nil")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("Archive error = %v, want KindValidation", err)
	}
}

func TestUnarchive_HappyPath(t *testing.T) {
	var unarchivePosted bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/unarchive") {
			unarchivePosted = true
			respondOK(w, json.RawMessage(`{"id":"test-1","status":"closed"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	})
	defer ts.Close()

	if err := fb.Unarchive(context.Background(), "test-1"); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	if !unarchivePosted {
		t.Error("expected POST /issues/{id}/unarchive to fire")
	}
}

// Unarchive keeps fleet-db's strict semantics: restoring an issue that was
// never archived is a real error, not an idempotent no-op like Archive.
func TestUnarchive_NotArchivedIsAnError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(apiResponse{ //nolint:errcheck
			Success: false,
			Error:   "issue is not archived",
		})
	})
	defer ts.Close()

	if err := fb.Unarchive(context.Background(), "test-1"); err == nil {
		t.Fatal("Unarchive of a non-archived issue: want error, got nil")
	}
}

func TestUnarchive_EmptyID(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request for empty id: %s %s", r.Method, r.URL.Path)
	})
	defer ts.Close()

	err := fb.Unarchive(context.Background(), "")
	if err == nil {
		t.Fatal("Unarchive with empty id: want error, got nil")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("Unarchive error = %v, want KindValidation", err)
	}
}
