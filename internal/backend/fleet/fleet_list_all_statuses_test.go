package fleet

// List's aggregate "all" status, split out of fleet_test.go to keep that file
// under the 2500-line test LOC ceiling.

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func TestList_AllMergesActiveAndClosedWithoutDuplicates(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var queries []string
	var limits []string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("status"))
		limits = append(limits, r.URL.Query().Get("limit"))
		if r.URL.Query().Get("status") == "closed" {
			respondOK(w, []*types.IssueWithCounts{
				{Issue: &types.Issue{ID: "closed", Title: "Closed", Status: types.StatusClosed, CreatedAt: now, UpdatedAt: now, ClosedAt: &now}},
			})
			return
		}
		respondOK(w, []*types.IssueWithCounts{
			{Issue: &types.Issue{ID: "open", Title: "Open", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now}},
			// Some FleetDB versions already include closed issues when status is
			// omitted; the aggregate contract must still return each issue once.
			{Issue: &types.Issue{ID: "closed", Title: "Closed", Status: types.StatusClosed, CreatedAt: now, UpdatedAt: now, ClosedAt: &now}},
		})
	})
	defer ts.Close()

	result, err := fb.List(context.Background(), backend.ListOpts{Status: "all", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !reflect.DeepEqual(queries, []string{"", "closed"}) {
		t.Fatalf("status queries = %v, want active then closed", queries)
	}
	if !reflect.DeepEqual(limits, []string{"10", "10"}) {
		t.Fatalf("limit queries = %v, want caller limit on both requests", limits)
	}
	if len(result) != 2 || result[0].ID != "open" || result[1].ID != "closed" {
		t.Fatalf("result = %+v, want deduplicated active and closed issues", result)
	}
}
