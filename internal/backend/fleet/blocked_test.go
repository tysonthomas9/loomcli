package fleet

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func TestBlockedIncludesExplicitBlockedStatusIssues(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	parent := "epic-1"
	seen := map[string]bool{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path+"?"+r.URL.RawQuery] = true
		switch r.URL.Path {
		case "/api/v1/test-ws/issues/blocked":
			respondOK(w, []blockedIssueResponseWire{
				{
					Issue:    fleetIssueWire{ID: "dep-blocked", Title: "Dependency blocked", Status: "open", ParentID: parent, CreatedAt: now, UpdatedAt: now},
					Blockers: []blockedBlockerWire{{ID: "dep-1"}},
				},
				{
					Issue: fleetIssueWire{ID: "explicit-blocked", Title: "Explicitly blocked", Status: "blocked", Priority: 2, ParentID: parent, CreatedAt: now, UpdatedAt: now},
				},
			})
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	})
	defer ts.Close()

	result, err := fb.Blocked(context.Background(), workitems.AvailabilityQuery{ParentID: parent, Limit: 5})
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
	if len(seen) != 1 {
		t.Fatalf("requests = %#v, want only canonical /issues/blocked", seen)
	}
}

func TestBlockedEmptyCanonicalEndpointReturnsEmpty(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/test-ws/issues/blocked":
			respondOK(w, []blockedIssueResponseWire{})
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	})
	defer ts.Close()

	result, err := fb.Blocked(context.Background(), workitems.AvailabilityQuery{Limit: 5})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("result = %+v, want empty canonical blocked result", result)
	}
}

func TestBlockedRejectsRetiredFlatBridgeResponse(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, []map[string]any{{
			"id": "legacy-flat", "title": "legacy", "status": "blocked",
		}})
	})
	defer ts.Close()

	_, err := fb.Blocked(context.Background(), workitems.AvailabilityQuery{})
	if err == nil {
		t.Fatal("expected the retired flat blocked response to fail closed")
	}
}

func TestBlockedClientFiltersLabelsAndSourceReposWithoutServerLimit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		switch r.URL.Path {
		case "/api/v1/test-ws/issues/blocked":
			respondOK(w, []blockedIssueResponseWire{
				{
					Issue: fleetIssueWire{
						ID:        "repo-a",
						Title:     "Wrong repo",
						Status:    "open",
						Labels:    []string{"frontend"},
						Repo:      "repo-a",
						CreatedAt: now,
						UpdatedAt: now,
					},
					Blockers: []blockedBlockerWire{{ID: "dep-1"}},
				},
				{
					Issue: fleetIssueWire{
						ID:        "repo-b",
						Title:     "Right repo",
						Status:    "open",
						Labels:    []string{"frontend"},
						Repo:      "repo-b",
						CreatedAt: now,
						UpdatedAt: now,
					},
					Blockers: []blockedBlockerWire{{ID: "dep-2"}},
				},
			})
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	})
	defer ts.Close()

	result, err := fb.Blocked(context.Background(), workitems.AvailabilityQuery{
		Labels:      []string{"frontend"},
		SourceRepos: []string{"repo-b"},
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if strings.Contains(gotQuery, "label=") || strings.Contains(gotQuery, "source_repos=") || strings.Contains(gotQuery, "limit=") {
		t.Fatalf("query = %q, want no unsupported label/repo filter or pre-filter limit", gotQuery)
	}
	if len(result) != 1 || result[0].ID != "repo-b" {
		t.Fatalf("result = %+v, want repo-b", result)
	}
}

func TestBlockedClientFiltersUnassignedAndLabelsAnyWithoutServerLimit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		respondOK(w, []blockedIssueResponseWire{
			{Issue: fleetIssueWire{ID: "assigned", Assignee: "owner", Labels: []string{"urgent"}, CreatedAt: now, UpdatedAt: now}},
			{Issue: fleetIssueWire{ID: "unassigned", Labels: []string{"urgent"}, CreatedAt: now, UpdatedAt: now}},
		})
	})
	defer ts.Close()

	result, err := fb.Blocked(context.Background(), workitems.AvailabilityQuery{
		Unassigned: true, LabelsAny: []string{"urgent"}, Limit: 1,
	})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if strings.Contains(gotQuery, "limit=") {
		t.Fatalf("query = %q, want no pre-filter limit", gotQuery)
	}
	if len(result) != 1 || result[0].ID != "unassigned" {
		t.Fatalf("result = %+v, want unassigned", result)
	}
}

func TestBlockedRejectsUnsupportedOwnerFiltersBeforeRequest(t *testing.T) {
	fb, ts := newTestServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("unsupported filter must not issue a request")
	})
	defer ts.Close()

	_, err := fb.Blocked(context.Background(), workitems.AvailabilityQuery{SortPolicy: "priority"})
	if !errors.Is(err, backend.ErrFilterNotSupported) {
		t.Fatalf("Blocked error = %v, want unsupported filter", err)
	}
}
