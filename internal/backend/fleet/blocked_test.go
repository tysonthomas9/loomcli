package fleet

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func TestBlockedIncludesExplicitBlockedStatusIssues(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	parent := "epic-1"
	seen := map[string]bool{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path+"?"+r.URL.RawQuery] = true
		switch r.URL.Path {
		case "/api/v1/test-ws/issues/blocked":
			respondOK(w, []*types.BlockedIssue{
				{
					Issue:          types.Issue{ID: "dep-blocked", Title: "Dependency blocked", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now},
					BlockedBy:      []string{"dep-1"},
					BlockedByCount: 1,
				},
			})
		case "/api/v1/test-ws/issues":
			if got := r.URL.Query().Get("status"); got != "blocked" {
				t.Fatalf("status query = %q, want blocked", got)
			}
			if got := r.URL.Query().Get("parent_id"); got != parent {
				t.Fatalf("parent_id query = %q, want %q", got, parent)
			}
			respondOK(w, []fleetIssueWithCountsWire{
				{
					fleetIssueWire: fleetIssueWire{
						ID:        "explicit-blocked",
						Title:     "Explicitly blocked",
						Status:    "blocked",
						Priority:  2,
						ParentID:  parent,
						CreatedAt: now,
						UpdatedAt: now,
					},
				},
			})
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	})
	defer ts.Close()

	result, err := fb.Blocked(context.Background(), backend.BlockedOpts{ParentID: parent, Limit: 5})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len = %d, want dependency-blocked plus explicit status blocked: %+v", len(result), result)
	}
	gotIDs := map[string]bool{}
	for _, issue := range result {
		gotIDs[issue.ID] = true
	}
	for _, want := range []string{"dep-blocked", "explicit-blocked"} {
		if !gotIDs[want] {
			t.Fatalf("Blocked result missing %q: %+v", want, result)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("requests = %#v, want /issues/blocked and /issues status=blocked", seen)
	}
}

func TestBlockedFallsBackToExplicitStatusWhenDependencyEndpointEmpty(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/test-ws/issues/blocked":
			respondOK(w, []*types.BlockedIssue{})
		case "/api/v1/test-ws/issues":
			if got := r.URL.Query().Get("status"); got != "blocked" {
				t.Fatalf("status query = %q, want blocked", got)
			}
			respondOK(w, []fleetIssueWithCountsWire{
				{
					fleetIssueWire: fleetIssueWire{
						ID:        "explicit-blocked",
						Title:     "Explicitly blocked",
						Status:    "blocked",
						CreatedAt: now,
						UpdatedAt: now,
					},
				},
			})
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	})
	defer ts.Close()

	result, err := fb.Blocked(context.Background(), backend.BlockedOpts{Limit: 5})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if len(result) != 1 || result[0].ID != "explicit-blocked" {
		t.Fatalf("result = %+v, want explicit blocked issue", result)
	}
}
