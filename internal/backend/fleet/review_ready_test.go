package fleet

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// answersCodeReviewLookup answers the code-review lookup that Ready and
// Blocked make, with no task in code review.
func answersCodeReviewLookup(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/api/v1/test-ws/issues" && r.URL.Query().Get("status") == "review" {
		respondOK(w, []fleetIssueWire{})
		return true
	}
	return false
}

func ids(issues []backend.IssueData) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.ID)
	}
	return out
}

// reviewReadyServer serves FleetDB's ready list, its blocked list and the
// tasks in review. A and A2 in epic e1, and A3 in epic-2, are in code review;
// X in e1 is a plan review without the label. K is in no epic and L in
// another epic than its blocker A.
func reviewReadyServer(t *testing.T, seen *[]string, ready []*readyIssueWithParent) (*FleetBackend, func()) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	future := now.Add(time.Hour)
	blocked := func(id, status, parent string, blockers ...string) blockedIssueResponseWire {
		entry := blockedIssueResponseWire{Issue: fleetIssueWire{ID: id, Title: id, Status: status, Priority: 2,
			Type: "task", ParentID: parent, CreatedAt: now, UpdatedAt: now}}
		for _, b := range blockers {
			entry.Blockers = append(entry.Blockers, blockedBlockerWire{ID: b})
		}
		return entry
	}
	assigned := blocked("H", "open", "e1", "A")
	assigned.Issue.Assignee = "agent-1"
	deferred := blocked("G", "open", "e1", "A")
	deferred.Issue.DeferUntil = &future
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/test-ws/issues/ready":
			respondOK(w, ready)
		case "/api/v1/test-ws/issues":
			if r.URL.Query().Get("status") != "review" {
				t.Fatalf("unexpected list: %s", r.URL.String())
			}
			respondOK(w, []fleetIssueWire{
				{ID: "A", Title: "A", Status: "review", Labels: []string{backend.CodeReviewLabel}, ParentID: "e1", CreatedAt: now, UpdatedAt: now},
				{ID: "X", Title: "X", Status: "review", ParentID: "e1", CreatedAt: now, UpdatedAt: now},
				{ID: "A2", Title: "A2", Status: "review", Labels: []string{backend.CodeReviewLabel}, ParentID: "e1", CreatedAt: now, UpdatedAt: now},
				{ID: "A4", Title: "A4", Status: "review", Labels: []string{backend.CodeReviewLabel}, CreatedAt: now, UpdatedAt: now},
				{ID: "A3", Title: "A3", Status: "review", Labels: []string{backend.CodeReviewLabel}, ParentID: "epic-2", CreatedAt: now, UpdatedAt: now},
			})
		case "/api/v1/test-ws/issues/blocked":
			respondOK(w, []blockedIssueResponseWire{
				blocked("B", "open", "e1", "A"),
				blocked("C", "open", "e1", "A", "Y"),
				blocked("D", "open", "e1", "X"),
				blocked("epic-2", "open", "", "Z"),
				blocked("E", "open", "epic-2", "A3"),
				blocked("F", "in_progress", "e1", "A"),
				deferred,
				assigned,
				blocked("K", "open", "", "A"),
				blocked("N", "open", "", "A4"),
				blocked("L", "open", "e2", "A"),
				blocked("M", "open", "e1", "A", "A2"),
			})
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	})
	return fb, ts.Close
}

// Tyson, 2026-10-09: a dependent starts once its blocker's agent finished,
// on the blocker's frozen revision, without waiting for the code review.
func TestReadyStartsATaskWhoseOnlyOpenBlockersAreInCodeReview(t *testing.T) {
	var seen []string
	now := time.Now().UTC().Truncate(time.Second)
	fb, done := reviewReadyServer(t, &seen, []*readyIssueWithParent{
		{fleetIssueWire: fleetIssueWire{ID: "r1", Title: "r1", Status: "open", Priority: 1, CreatedAt: now, UpdatedAt: now}},
		{fleetIssueWire: fleetIssueWire{ID: "r3", Title: "r3", Status: "open", Priority: 3, CreatedAt: now, UpdatedAt: now}},
	})
	defer done()

	ready, err := fb.Ready(context.Background(), backend.ReadyOpts{Limit: 10})
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	// B and H wait only on A, in code review in their epic. C also waits on
	// an open task, D on a plan review, E's parent is blocked, F is already
	// running and G is deferred: they keep waiting. K is in no epic and L in
	// another epic, so neither can be built on A's code: they wait until A
	// closes; N and its blocker A4 are both in no epic, so N waits too. M waits on A and on A2, both in code review: it can be built on
	// only one, so it waits too.
	if got, want := ids(ready), []string{"r1", "B", "H", "r3"}; !slices.Equal(got, want) {
		t.Fatalf("Ready = %v, want %v (B in priority order)", got, want)
	}

	limited, err := fb.Ready(context.Background(), backend.ReadyOpts{Limit: 2})
	if err != nil || !slices.Equal(ids(limited), []string{"r1", "B"}) {
		t.Fatalf("Ready with limit 2 = %v, %v; want [r1 B]", ids(limited), err)
	}

	blocked, err := fb.Blocked(context.Background(), backend.BlockedOpts{})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if got := ids(blocked); slices.Contains(got, "B") || !slices.Contains(got, "C") ||
		!slices.Contains(got, "D") || !slices.Contains(got, "E") || !slices.Contains(got, "K") || !slices.Contains(got, "L") || !slices.Contains(got, "M") || !slices.Contains(got, "N") {
		t.Fatalf("Blocked = %v, want C, D, E, K, L, M and N but not B", got)
	}
}

func TestReadyKeepsCallerFiltersForTasksBehindReview(t *testing.T) {
	var seen []string
	fb, done := reviewReadyServer(t, &seen, nil)
	defer done()

	for _, tc := range []struct {
		name string
		opts backend.ReadyOpts
		want []string
	}{
		{"no filter", backend.ReadyOpts{}, []string{"B", "H"}},
		{"unassigned", backend.ReadyOpts{Unassigned: true}, []string{"B"}},
		{"another parent", backend.ReadyOpts{ParentID: "epic-9"}, nil},
		{"another priority", backend.ReadyOpts{Priority: new(int)}, nil},
		{"label it lacks", backend.ReadyOpts{Labels: []string{"repo:web"}}, nil},
		{"molecule query", backend.ReadyOpts{MolType: "swarm"}, nil},
	} {
		ready, err := fb.Ready(context.Background(), tc.opts)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := ids(ready); !slices.Equal(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
			t.Fatalf("%s: Ready = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestReadyAsksForBlockedTasksOnlyWhenATaskIsInCodeReview(t *testing.T) {
	var seen []string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		if answersCodeReviewLookup(w, r) {
			return
		}
		if r.URL.Path != "/api/v1/test-ws/issues/ready" {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		respondOK(w, []*readyIssueWithParent{})
	})
	defer ts.Close()

	if _, err := fb.Ready(context.Background(), backend.ReadyOpts{}); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if slices.Contains(seen, "/api/v1/test-ws/issues/blocked") {
		t.Fatalf("requests = %v: no task is in code review, so the blocked list is not needed", seen)
	}
}

func TestReadyFallsBackToFleetDBWhenTheReviewLookupFails(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/test-ws/issues/ready" {
			respondOK(w, []*readyIssueWithParent{{fleetIssueWire: fleetIssueWire{ID: "r1", Title: "r1", Status: "open", CreatedAt: now, UpdatedAt: now}}})
			return
		}
		http.Error(w, "down", http.StatusInternalServerError)
	})
	defer ts.Close()

	ready, err := fb.Ready(context.Background(), backend.ReadyOpts{})
	if err != nil || !slices.Equal(ids(ready), []string{"r1"}) {
		t.Fatalf("Ready = %v, %v; want FleetDB's [r1]", ids(ready), err)
	}
}
