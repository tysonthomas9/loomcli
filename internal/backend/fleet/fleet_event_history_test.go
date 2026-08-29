package fleet

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Event-history paging coverage that rides alongside events_paging_test.go's
// HTTP-seam fixture. Lives in its own file to keep fleet_test.go under the
// LOC ratchet.

func TestListEventHistory_SinceClampsToFleetPageMaximum(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	since := "opaque-cursor"
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("limit"); got != "200" {
			t.Errorf("limit = %q, want 200", got)
		}
		if got := r.URL.Query().Get("since"); got != since {
			t.Errorf("since = %q, want %q", got, since)
		}
		respondOK(w, map[string]any{
			"history": []map[string]any{{
				"id":        "201-0",
				"timestamp": now,
				"actor":     "agent",
				"action":    "issue.update",
			}},
			"cursor":       "next-cursor",
			"has_more":     true,
			"total_events": 295,
		})
	})
	defer ts.Close()

	result, err := fb.ListEventHistory(context.Background(), "test-1", backend.EventHistoryParams{
		Limit: 500,
		Since: &since,
	})
	if err != nil {
		t.Fatalf("ListEventHistory: %v", err)
	}
	if result.Cursor != "next-cursor" || !result.HasMore || result.TotalEvents != 295 {
		t.Errorf("result metadata = %+v, want forwarded cursor/has_more/total_events", result)
	}
}

// --- Not implemented surfaces (partial — implemented methods moved out) ---
//
// Count, Batch, GetMutations, and WaitForMutations used to live here as
// ErrNotImplemented stubs; they are now wired against fleet-db endpoints
// (see tests above). The only remaining KindNotImplemented paths are:
//
//   - Count with GroupBy set — Count cannot return grouped data through its
//     int return value; callers must use Stats or (future) a GroupedCount API.
//     This is exercised by TestCount_GroupByRejected.

func TestListEventHistory_TailReportsTrimWithoutTotalEvents(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var requests int
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Query().Get("limit"); got != "200" {
			t.Errorf("request %d limit = %q, want 200", requests, got)
		}

		switch requests {
		case 1:
			history := make([]map[string]any, 200)
			for index := range history {
				history[index] = map[string]any{
					"id":        strconv.Itoa(index+1) + "-0",
					"timestamp": now.Add(time.Duration(index) * time.Second),
					"actor":     "agent",
					"action":    "issue.update",
				}
			}
			respondOK(w, map[string]any{
				"history":  history,
				"cursor":   "200-0",
				"has_more": true,
			})
		case 2:
			respondOK(w, map[string]any{
				"history": []map[string]any{
					{"id": "201-0", "timestamp": now.Add(200 * time.Second), "actor": "agent", "action": "issue.update"},
					{"id": "202-0", "timestamp": now.Add(201 * time.Second), "actor": "agent", "action": "issue.update"},
					{"id": "203-0", "timestamp": now.Add(202 * time.Second), "actor": "agent", "action": "issue.update"},
				},
				"cursor":   "203-0",
				"has_more": false,
			})
		default:
			t.Errorf("unexpected request %d", requests)
		}
	})
	defer ts.Close()

	result, err := fb.ListEventHistory(context.Background(), "test-1", backend.EventHistoryParams{Limit: 3})
	if err != nil {
		t.Fatalf("ListEventHistory: %v", err)
	}
	if !result.HasMore {
		t.Error("HasMore = false, want true after trimming 203 compatibility events to a 3-event tail")
	}
}

func TestListEventHistory_TailReportsCompleteWithoutTotalEvents(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		respondOK(w, map[string]any{
			"history": []map[string]any{
				{"id": "1-0", "timestamp": now, "actor": "agent", "action": "issue.update"},
				{"id": "2-0", "timestamp": now.Add(time.Second), "actor": "agent", "action": "issue.update"},
			},
			"has_more": false,
		})
	})
	defer ts.Close()

	result, err := fb.ListEventHistory(context.Background(), "test-1", backend.EventHistoryParams{Limit: 3})
	if err != nil {
		t.Fatalf("ListEventHistory: %v", err)
	}
	if result.HasMore {
		t.Error("HasMore = true, want false for an untrimmed compatibility tail")
	}
}

//
// If future refactors reintroduce unimplemented stubs, add cases here.

// --- Connection refused test ---
