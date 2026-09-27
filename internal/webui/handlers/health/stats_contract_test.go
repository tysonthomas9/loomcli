package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// TestServeStatsViaBackend_CarriesEveryField pins the StatsData → types.Statistics
// mapping in serveStatsViaBackend. That mapping is a field-by-field literal, so a
// field added to both structs but forgotten there compiles fine and silently
// serves zero. Every field gets a distinct value so a copy/paste slip between two
// fields fails too.
func TestServeStatsViaBackend_CarriesEveryField(t *testing.T) {
	data := &backend.StatsData{
		TotalIssues:             408,
		OpenIssues:              52,
		InProgressIssues:        2,
		ClosedIssues:            340,
		BlockedIssues:           6,
		DeferredIssues:          1,
		ReadyIssues:             30,
		ReviewIssues:            4,
		StatusBlockedIssues:     10,
		TombstoneIssues:         7,
		PinnedIssues:            3,
		EpicsEligibleForClosure: 9,
	}
	handler := HandleStatsWithBackend(nil, func(_ context.Context) backend.IssueBackend {
		return &stubIssueBackend{stats: data}
	})

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/stats", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	var resp StatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode StatsResponse: %v (body=%q)", err, rec.Body.String())
	}
	if !resp.Success {
		t.Fatalf("Success = false, want true (error=%q)", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("Data = nil, want the mapped statistics")
	}

	for _, tc := range []struct {
		field string
		got   int
		want  int
	}{
		{"total_issues", resp.Data.TotalIssues, data.TotalIssues},
		{"open_issues", resp.Data.OpenIssues, data.OpenIssues},
		{"in_progress_issues", resp.Data.InProgressIssues, data.InProgressIssues},
		{"closed_issues", resp.Data.ClosedIssues, data.ClosedIssues},
		{"blocked_issues", resp.Data.BlockedIssues, data.BlockedIssues},
		{"deferred_issues", resp.Data.DeferredIssues, data.DeferredIssues},
		{"ready_issues", resp.Data.ReadyIssues, data.ReadyIssues},
		{"review_issues", resp.Data.ReviewIssues, data.ReviewIssues},
		{"status_blocked_issues", resp.Data.StatusBlockedIssues, data.StatusBlockedIssues},
		{"tombstone_issues", resp.Data.TombstoneIssues, data.TombstoneIssues},
		{"pinned_issues", resp.Data.PinnedIssues, data.PinnedIssues},
		{"epics_eligible_for_closure", resp.Data.EpicsEligibleForClosure, data.EpicsEligibleForClosure},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.field, tc.got, tc.want)
		}
	}
}

// TestServeStatsViaBackend_WrapsInEnvelope pins the wire shape the frontend's
// getStats() unwraps: {"success":true,"data":{...}}, not a bare Statistics.
func TestServeStatsViaBackend_WrapsInEnvelope(t *testing.T) {
	handler := HandleStatsWithBackend(nil, func(_ context.Context) backend.IssueBackend {
		return &stubIssueBackend{stats: &backend.StatsData{TotalIssues: 5}}
	})

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/stats", nil))

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v (body=%q)", err, rec.Body.String())
	}
	if _, ok := envelope["success"]; !ok {
		t.Errorf("response has no \"success\" key: %q", rec.Body.String())
	}
	if _, ok := envelope["data"]; !ok {
		t.Errorf("response has no \"data\" key: %q", rec.Body.String())
	}
	if _, ok := envelope["total_issues"]; ok {
		t.Errorf("statistics leaked to the top level; the endpoint must wrap them")
	}
}
