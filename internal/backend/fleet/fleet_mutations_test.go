package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestGetMutations_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotPath, gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		respondOK(w, fleetMutationsResponse{
			Events: []fleetMutationEvent{
				{
					ID:         "1708000000000-0",
					Timestamp:  now,
					Actor:      "agent-a",
					Action:     "issue.create",
					EntityType: "issue",
					EntityID:   "loom-1",
					After:      `{"title":"New issue","status":"open","parent":"ep-1","repo":"org/repo"}`,
				},
				{
					ID:         "1708000000001-0",
					Timestamp:  now.Add(time.Second),
					Actor:      "agent-b",
					Action:     "issue.close",
					EntityType: "issue",
					EntityID:   "loom-2",
					Before:     `{"status":"open"}`,
					After:      `{"status":"closed"}`,
				},
			},
			Cursor:  "1708000000001-0",
			HasMore: false,
		})
	})
	defer ts.Close()

	got, err := fb.GetMutations(context.Background(), 1700000000000)
	if err != nil {
		t.Fatalf("GetMutations: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/events/mutations") {
		t.Errorf("path = %q, want suffix /events/mutations", gotPath)
	}
	if !strings.Contains(gotQuery, "since=1700000000000") {
		t.Errorf("query %q missing since=1700000000000", gotQuery)
	}
	if len(got) != 2 {
		t.Fatalf("got %d mutations, want 2", len(got))
	}
	if got[0].Type != backend.MutationCreate {
		t.Errorf("got[0].Type = %q, want %q", got[0].Type, backend.MutationCreate)
	}
	if got[0].EntityType != "issue" || got[0].EntityID != "loom-1" || got[0].Action != "issue.create" {
		t.Errorf("got[0] generic envelope = %q/%q/%q, want issue/loom-1/issue.create", got[0].EntityType, got[0].EntityID, got[0].Action)
	}
	if got[0].IssueID != "loom-1" || got[0].Title != "New issue" || got[0].ParentID != "ep-1" || got[0].SourceRepo != "org/repo" {
		t.Errorf("got[0] = %+v, after-snapshot fields not extracted", got[0])
	}
	if got[1].Type != backend.MutationStatus {
		t.Errorf("got[1].Type = %q, want %q (issue.close -> status)", got[1].Type, backend.MutationStatus)
	}
	if got[1].OldStatus != "open" || got[1].NewStatus != "closed" {
		t.Errorf("got[1] old/new status = %q/%q, want open/closed", got[1].OldStatus, got[1].NewStatus)
	}
}

func TestGetMutations_EmptyResponse(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, fleetMutationsResponse{Events: []fleetMutationEvent{}, Cursor: "0", HasMore: false})
	})
	defer ts.Close()

	got, err := fb.GetMutations(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetMutations: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestGetMutations_NullDataReturnsEmpty(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":null}`))
	})
	defer ts.Close()

	got, err := fb.GetMutations(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetMutations: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestGetMutations_ActionFolding(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, fleetMutationsResponse{
			Events: []fleetMutationEvent{
				{Timestamp: now, Action: "issue.update", EntityType: "issue", EntityID: "a"},
				{Timestamp: now, Action: "issue.delete", EntityType: "issue", EntityID: "b"},
				{Timestamp: now, Action: "comment.add", EntityType: "comment", EntityID: "c"},
				{Timestamp: now, Action: "label.add", EntityType: "label", EntityID: "d"},
				{Timestamp: now, Action: "workspace.update", EntityType: "workspace", EntityID: "ws"},
				{Timestamp: now, Action: "unknown.weird", EntityType: "issue", EntityID: "e"},
			},
		})
	})
	defer ts.Close()

	got, err := fb.GetMutations(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetMutations: %v", err)
	}
	wantTypes := []string{
		backend.MutationUpdate, backend.MutationDelete, backend.MutationComment,
		backend.MutationUpdate, backend.MutationRefresh, backend.MutationUpdate,
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("len = %d, want %d", len(got), len(wantTypes))
	}
	for i, wantT := range wantTypes {
		if got[i].Type != wantT {
			t.Errorf("got[%d].Type = %q, want %q", i, got[i].Type, wantT)
		}
		if got[i].EntityType == "" || got[i].EntityID == "" || got[i].Action == "" {
			t.Errorf("got[%d] missing generic envelope fields: %+v", i, got[i])
		}
	}
	if got[2].IssueID != "" || got[3].IssueID != "" || got[4].IssueID != "" {
		t.Errorf("non-issue fleet mutations should not populate legacy issue_id: comment=%q label=%q workspace=%q", got[2].IssueID, got[3].IssueID, got[4].IssueID)
	}
	if got[0].IssueID != "a" || got[5].IssueID != "e" {
		t.Errorf("issue fleet mutations should preserve legacy issue_id: update=%q unknown=%q", got[0].IssueID, got[5].IssueID)
	}
}

// --- WaitForMutations tests ---

func TestWaitForMutations_TimeoutEmpty(t *testing.T) {
	var gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		respondOK(w, fleetMutationsResponse{Events: []fleetMutationEvent{}, Cursor: "0"})
	})
	defer ts.Close()

	got, err := fb.WaitForMutations(context.Background(), 1000, 5000)
	if err != nil {
		t.Fatalf("WaitForMutations: %v", err)
	}
	if !strings.Contains(gotQuery, "since=1000") || !strings.Contains(gotQuery, "timeout=5000") {
		t.Errorf("query %q missing since=1000 and/or timeout=5000", gotQuery)
	}
	if len(got) != 0 {
		t.Errorf("expected empty on timeout, got %d", len(got))
	}
}

func TestWaitForMutations_DeliversEvents(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, fleetMutationsResponse{
			Events: []fleetMutationEvent{
				{Timestamp: now, Action: "issue.update", EntityType: "issue", EntityID: "x"},
			},
		})
	})
	defer ts.Close()

	got, err := fb.WaitForMutations(context.Background(), 0, 2000)
	if err != nil {
		t.Fatalf("WaitForMutations: %v", err)
	}
	if len(got) != 1 || got[0].IssueID != "x" {
		t.Errorf("got %+v, want 1 mutation for x", got)
	}
}

func TestWaitForMutations_ServerError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 400, "timeout out of range")
	})
	defer ts.Close()

	_, err := fb.WaitForMutations(context.Background(), 0, 500)
	if err == nil {
		t.Fatal("expected error for bad timeout")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

func TestGetMutationsAfter_ReturnsPageAndSendsExplicitLimit(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	var gotQuery url.Values
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		respondOK(w, fleetMutationsResponse{
			Events:  []fleetMutationEvent{{ID: "c1.event", Timestamp: now, Action: "issue.update", EntityType: "issue", EntityID: "task-1"}},
			Cursor:  "c1.next",
			HasMore: true,
		})
	})
	defer ts.Close()

	page, err := fb.GetMutationsAfter(context.Background(), "c1.c3RhcnQ", 100)
	if err != nil {
		t.Fatalf("GetMutationsAfter: %v", err)
	}
	if got := gotQuery.Get("since"); got != "c1.c3RhcnQ" {
		t.Fatalf("since = %q, want opaque token preserved", got)
	}
	if got := gotQuery.Get("limit"); got != "100" {
		t.Fatalf("limit = %q, want 100", got)
	}
	if len(page.Events) != 1 || page.Events[0].Cursor != "c1.event" {
		t.Fatalf("events = %+v, want cursor c1.event", page.Events)
	}
	if page.Cursor != "c1.next" || !page.HasMore {
		t.Fatalf("page = %+v, want cursor c1.next with has_more", page)
	}
}

func TestGetMutationsAfter_EmptyTerminalPageKeepsPreviousCursor(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, fleetMutationsResponse{Events: []fleetMutationEvent{}, Cursor: "", HasMore: false})
	})
	defer ts.Close()

	page, err := fb.GetMutationsAfter(context.Background(), "c1.previous", 100)
	if err != nil {
		t.Fatalf("GetMutationsAfter: %v", err)
	}
	if page.Cursor != "c1.previous" {
		t.Fatalf("cursor = %q, want previous cursor", page.Cursor)
	}
	if page.Events == nil {
		t.Fatal("events = nil, want non-nil empty slice")
	}
}

func TestMutationCursorNormalizationPreservesSupportedClasses(t *testing.T) {
	tests := []struct {
		cursor    string
		wantSince string
	}{
		{cursor: "$", wantSince: "c1.JA"},
		{cursor: "c1.b3BhcXVl", wantSince: "c1.b3BhcXVl"},
		{cursor: "1700000000000", wantSince: "c1.MTcwMDAwMDAwMDAwMC0w"},
		{cursor: "0", wantSince: "0"},
	}
	for _, tt := range tests {
		t.Run(tt.cursor, func(t *testing.T) {
			var gotSince string
			fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				gotSince = r.URL.Query().Get("since")
				respondOK(w, fleetMutationsResponse{Events: []fleetMutationEvent{}, Cursor: tt.cursor})
			})
			defer ts.Close()

			if _, err := fb.WaitForMutationsAfter(context.Background(), tt.cursor, 0, 100); err != nil {
				t.Fatalf("WaitForMutationsAfter: %v", err)
			}
			if gotSince != tt.wantSince {
				t.Fatalf("since = %q, want %q", gotSince, tt.wantSince)
			}
		})
	}
}

func TestMutationCursorNormalizationRejectsMalformedCursor(t *testing.T) {
	for _, cursor := range []string{"", "+1", "-1", "1700000000000-0", "not-a-cursor", " c1.token", "c1.", "c1.not*opaque"} {
		t.Run(strconv.Quote(cursor), func(t *testing.T) {
			fb, ts := newTestServer(t, func(http.ResponseWriter, *http.Request) {
				t.Fatal("malformed cursor must be rejected before sending a request")
			})
			defer ts.Close()

			if _, err := fb.GetMutationsAfter(context.Background(), cursor, 100); err == nil {
				t.Fatal("GetMutationsAfter error = nil, want malformed cursor error")
			}
		})
	}
}

func TestProbeHead(t *testing.T) {
	t.Run("supported", func(t *testing.T) {
		fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query(); got.Get("since") != "c1.JA" || got.Get("timeout") != "0" || got.Get("limit") != "1" {
				t.Fatalf("query = %q, want since=$ timeout=0 limit=1", got.Encode())
			}
			respondOK(w, fleetMutationsResponse{Events: []fleetMutationEvent{}, Cursor: "c1.head", HasMore: false})
		})
		defer ts.Close()

		cursor, supported, err := fb.ProbeHead(context.Background())
		if err != nil || !supported || cursor != "c1.head" {
			t.Fatalf("ProbeHead = (%q, %v, %v), want (c1.head, true, nil)", cursor, supported, err)
		}
	})

	t.Run("old fleet exact error is unsupported", func(t *testing.T) {
		fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(apiResponse{Success: false, Code: "invalid_parameter", Error: "invalid since parameter: expected opaque cursor token"})
		})
		defer ts.Close()

		cursor, supported, err := fb.ProbeHead(context.Background())
		if err != nil || supported || cursor != "" {
			t.Fatalf("ProbeHead = (%q, %v, %v), want (empty, false, nil)", cursor, supported, err)
		}
	})

	t.Run("other error is not compatibility fallback", func(t *testing.T) {
		fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(apiResponse{Success: false, Code: "invalid_parameter", Error: "different message"})
		})
		defer ts.Close()

		if _, _, err := fb.ProbeHead(context.Background()); err == nil {
			t.Fatal("ProbeHead error = nil, want non-compatibility error")
		}
	})

	t.Run("non-200 success status is an error", func(t *testing.T) {
		fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
		defer ts.Close()

		if _, _, err := fb.ProbeHead(context.Background()); err == nil {
			t.Fatal("ProbeHead error = nil, want non-200 status error")
		}
	})
}

func TestGetMutationsAfter_CursorExpiredPreservesTypedReasonAndFloor(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"error":{"code":"cursor_expired","message":"cursor expired","meta":{"cursor":"c1.floor"}}}`))
	})
	defer ts.Close()

	_, err := fb.GetMutationsAfter(context.Background(), "c1.old", 100)
	if !errors.Is(err, backend.ErrMutationCursorExpired) {
		t.Fatalf("error = %v, want ErrMutationCursorExpired", err)
	}
	var be *backend.BackendError
	if !errors.As(err, &be) || be.Meta["cursor"] != "c1.floor" {
		t.Fatalf("error meta = %#v, want cursor floor c1.floor", be)
	}
}

// --- Batch tests ---
