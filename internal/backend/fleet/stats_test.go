package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func respondEmptyCanonicalView(w http.ResponseWriter, path string) bool {
	switch path {
	case "/api/v1/test-ws/issues/blocked":
		respondOK(w, []blockedIssueResponseWire{})
		return true
	case "/api/v1/test-ws/issues/deferred", "/api/v1/test-ws/issues/ready":
		respondOK(w, struct {
			Issues []*readyIssueWithParent `json:"issues"`
			Count  int                     `json:"count"`
		}{Issues: []*readyIssueWithParent{}, Count: 0})
		return true
	default:
		return false
	}
}

func TestStats_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/test-ws/issues/count":
			if r.URL.Query().Get("group_by") != "status" {
				t.Errorf("group_by = %q, want status", r.URL.Query().Get("group_by"))
			}
			respondOK(w, countIssuesResponse{
				Total: 10,
				Groups: map[string]int64{
					"open":        3,
					"closed":      2,
					"in_progress": 2,
					"blocked":     1,
					"deferred":    1,
					"tombstone":   1,
				},
			})
		case "/api/v1/test-ws/issues/blocked":
			respondOK(w, []blockedIssueResponseWire{
				{Issue: fleetIssueWire{ID: "blocked-1", Status: "open", CreatedAt: now, UpdatedAt: now}},
				{Issue: fleetIssueWire{ID: "blocked-2", Status: "blocked", CreatedAt: now, UpdatedAt: now}},
			})
		case "/api/v1/test-ws/issues/deferred":
			respondOK(w, struct {
				Issues []*readyIssueWithParent `json:"issues"`
				Count  int                     `json:"count"`
			}{Issues: []*readyIssueWithParent{
				{fleetIssueWire: fleetIssueWire{ID: "deferred-1", Status: "open", CreatedAt: now, UpdatedAt: now}},
			}, Count: 1})
		case "/api/v1/test-ws/issues/ready":
			respondOK(w, struct {
				Issues []*readyIssueWithParent `json:"issues"`
				Count  int                     `json:"count"`
			}{Issues: []*readyIssueWithParent{
				{fleetIssueWire: fleetIssueWire{ID: "ready-1", Status: "open", CreatedAt: now, UpdatedAt: now}},
				{fleetIssueWire: fleetIssueWire{ID: "ready-2", Status: "open", CreatedAt: now, UpdatedAt: now}},
				{fleetIssueWire: fleetIssueWire{ID: "ready-3", Status: "open", CreatedAt: now, UpdatedAt: now}},
			}, Count: 3})
		default:
			t.Errorf("unexpected path = %q", r.URL.Path)
		}
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalIssues != 10 {
		t.Errorf("TotalIssues = %d, want 10", result.TotalIssues)
	}
	if result.OpenIssues != 3 {
		t.Errorf("OpenIssues = %d, want 3", result.OpenIssues)
	}
	if result.ClosedIssues != 2 {
		t.Errorf("ClosedIssues = %d, want 2", result.ClosedIssues)
	}
	if result.InProgressIssues != 2 {
		t.Errorf("InProgressIssues = %d, want 2", result.InProgressIssues)
	}
	if result.BlockedIssues != 2 {
		t.Errorf("BlockedIssues = %d, want 2", result.BlockedIssues)
	}
	if result.DeferredIssues != 1 {
		t.Errorf("DeferredIssues = %d, want 1", result.DeferredIssues)
	}
	if result.TombstoneIssues != 1 {
		t.Errorf("TombstoneIssues = %d, want 1", result.TombstoneIssues)
	}
	if result.PinnedIssues != 0 {
		t.Errorf("PinnedIssues = %d, want 0 (not in groups)", result.PinnedIssues)
	}
	if result.ReadyIssues != 3 {
		t.Errorf("ReadyIssues = %d, want 3", result.ReadyIssues)
	}
	if result.EpicsEligibleForClosure != 0 {
		t.Errorf("EpicsEligibleForClosure = %d, want 0 (fleet-08yg)", result.EpicsEligibleForClosure)
	}
	if result.AverageLeadTime != 0 {
		t.Errorf("AverageLeadTime = %f, want 0 (fleet-08yg)", result.AverageLeadTime)
	}
}

func TestStats_AllStatuses(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if respondEmptyCanonicalView(w, r.URL.Path) {
			return
		}
		if r.URL.Path != "/api/v1/test-ws/issues/count" {
			t.Fatalf("unexpected path: %s", r.URL.String())
		}
		respondOK(w, countIssuesResponse{
			Total: 20,
			Groups: map[string]int64{
				"open":        3,
				"closed":      4,
				"in_progress": 2,
				"blocked":     1,
				"deferred":    2,
				"tombstone":   1,
				"pinned":      3,
				"review":      2,
				"hooked":      2,
			},
		})
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalIssues != 20 {
		t.Errorf("TotalIssues = %d, want 20", result.TotalIssues)
	}
	if result.PinnedIssues != 3 {
		t.Errorf("PinnedIssues = %d, want 3", result.PinnedIssues)
	}
}

func TestStats_EmptyWorkspace(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if respondEmptyCanonicalView(w, r.URL.Path) {
			return
		}
		if r.URL.Path != "/api/v1/test-ws/issues/count" {
			t.Fatalf("unexpected path: %s", r.URL.String())
		}
		respondOK(w, countIssuesResponse{Total: 0, Groups: map[string]int64{}})
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalIssues != 0 {
		t.Errorf("TotalIssues = %d, want 0", result.TotalIssues)
	}
	if result.OpenIssues != 0 {
		t.Errorf("OpenIssues = %d, want 0", result.OpenIssues)
	}
}

func TestStats_MissingStatusKeys(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if respondEmptyCanonicalView(w, r.URL.Path) {
			return
		}
		if r.URL.Path != "/api/v1/test-ws/issues/count" {
			t.Fatalf("unexpected path: %s", r.URL.String())
		}
		respondOK(w, countIssuesResponse{
			Total:  5,
			Groups: map[string]int64{"open": 3, "closed": 2},
		})
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalIssues != 5 {
		t.Errorf("TotalIssues = %d, want 5", result.TotalIssues)
	}
	if result.InProgressIssues != 0 {
		t.Errorf("InProgressIssues = %d, want 0", result.InProgressIssues)
	}
	if result.BlockedIssues != 0 {
		t.Errorf("BlockedIssues = %d, want 0", result.BlockedIssues)
	}
}

func TestStats_ClientError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 500, "internal error")
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
}

func TestStats_NilResponse(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{Success: true, Data: nil}) //nolint:errcheck
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err == nil {
		t.Fatal("expected error for nil response data, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
}

func TestStats_JSONNullResponse(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"data":null}`)) //nolint:errcheck
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err == nil {
		t.Fatal("expected error for JSON null data, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
}
