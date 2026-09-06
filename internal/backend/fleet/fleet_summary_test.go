package fleet

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// GetSummary exists so callers that need one scalar field — a parent's title,
// a dependency row's status — do not pay for Get's related-data lookups. The
// contract is one round-trip, so the test server fails the test on any /deps or
// /comments hit rather than merely asserting on the returned value.
func TestGetSummary_SingleRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var requests int

	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		requests++
		switch {
		case strings.HasSuffix(path, "/deps"), strings.HasSuffix(path, "/comments"):
			t.Errorf("GetSummary fetched related data: %s %s", r.Method, path)
			respondOK(w, map[string]interface{}{})
		case r.Method == "GET" && strings.HasSuffix(path, "/issues/PUPPET-1"):
			respondOK(w, fleetIssueWithCountsWire{
				fleetIssueWire: fleetIssueWire{
					ID: "PUPPET-1", Title: "Parent epic", Status: "open",
					Priority: 1, Type: "epic", CreatedAt: now,
					Labels: []string{"infra"},
				},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, path)
		}
	})
	defer ts.Close()

	data, err := fb.GetSummary(context.Background(), "PUPPET-1")
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if data == nil {
		t.Fatal("GetSummary returned nil data")
	}
	if data.Title != "Parent epic" {
		t.Errorf("Title = %q, want %q", data.Title, "Parent epic")
	}
	if data.Status != "open" {
		t.Errorf("Status = %q, want %q", data.Status, "open")
	}
	if requests != 1 {
		t.Errorf("GetSummary cost %d requests, want 1", requests)
	}
}

func TestGetSummary_EmptyIDIsValidationError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request for an empty ID: %s %s", r.Method, r.URL.Path)
	})
	defer ts.Close()

	if _, err := fb.GetSummary(context.Background(), "  "); err == nil {
		t.Fatal("GetSummary(\"  \") = nil error, want validation error")
	}
}

// fleet-db's /deps rows carry only the edge, so every row's display fields are
// empty and each distinct related issue needs a summary lookup. Hydration must
// therefore be deduplicated (an epic repeats the same related issue across
// rows) and capped, or a wide epic turns one `loom data show` into a fan-out.
func TestFetchDependencies_DedupesAndCapsHydration(t *testing.T) {
	const viewID = "EPIC-1"

	// depsServer serves `rows` /deps edges pointing at `distinct` related
	// issues, counting the per-issue summary lookups.
	depsServer := func(t *testing.T, rows, distinct int) (*FleetBackend, func() int, func()) {
		t.Helper()
		var (
			mu       sync.Mutex
			hydrates int
		)
		fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			switch {
			case strings.HasSuffix(path, "/issues/"+viewID+"/deps"):
				type depWire struct {
					IssueID     string `json:"issue_id"`
					DependsOnID string `json:"depends_on_id"`
					Type        string `json:"type"`
				}
				out := make([]depWire, 0, rows)
				for i := 0; i < rows; i++ {
					out = append(out, depWire{
						IssueID:     fmt.Sprintf("CHILD-%d", i%distinct),
						DependsOnID: viewID,
						Type:        "parent-child",
					})
				}
				respondOK(w, struct {
					Dependencies []depWire `json:"dependencies"`
				}{Dependencies: out})
			case strings.HasPrefix(path, "/api/v1/test-ws/issues/CHILD-"):
				mu.Lock()
				hydrates++
				mu.Unlock()
				id := strings.TrimPrefix(path, "/api/v1/test-ws/issues/")
				respondOK(w, fleetIssueWithCountsWire{
					fleetIssueWire: fleetIssueWire{ID: id, Title: "Title " + id, Status: "open", Type: "task"},
				})
			default:
				t.Errorf("unexpected request: %s %s", r.Method, path)
			}
		})
		count := func() int {
			mu.Lock()
			defer mu.Unlock()
			return hydrates
		}
		return fb, count, ts.Close
	}

	t.Run("dedupes repeated related issues", func(t *testing.T) {
		fb, hydrates, closeFn := depsServer(t, 40, 5)
		defer closeFn()

		deps, dependents, err := fb.fetchDependencies(context.Background(), viewID)
		if err != nil {
			t.Fatalf("fetchDependencies: %v", err)
		}
		if len(deps) != 0 {
			t.Errorf("deps = %d rows, want 0 (all rows are the epic's children)", len(deps))
		}
		if len(dependents) != 40 {
			t.Fatalf("dependents = %d rows, want 40", len(dependents))
		}
		if got := hydrates(); got > 5 {
			t.Errorf("hydration cost %d requests for 5 distinct related issues, want <= 5", got)
		}
		for _, d := range dependents {
			if d.Title == "" {
				t.Fatalf("row %s has no title: dedupe must still hydrate every row", d.IssueID)
			}
		}
	})

	t.Run("caps a wide epic", func(t *testing.T) {
		fb, hydrates, closeFn := depsServer(t, 200, 200)
		defer closeFn()

		_, dependents, err := fb.fetchDependencies(context.Background(), viewID)
		if err != nil {
			t.Fatalf("fetchDependencies: %v", err)
		}
		if len(dependents) != 200 {
			t.Errorf("dependents = %d rows, want all 200 kept", len(dependents))
		}
		if got := hydrates(); got != 0 {
			t.Errorf("hydration issued %d requests past the cap of %d, want 0", got, depHydrateMax)
		}
	})
}

// A row whose related ID is unusable must not be dropped, and must not cost a
// request: fetchIssueSummary would only reject it as a validation error.
func TestFetchDependencies_SkipsHydrationForEmptyRelatedID(t *testing.T) {
	const viewID = "PUPPET-1"
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !strings.HasSuffix(path, "/deps") {
			t.Errorf("unexpected request: %s %s", r.Method, path)
			return
		}
		respondOK(w, map[string]interface{}{
			"dependencies": []map[string]string{
				{"issue_id": viewID, "depends_on_id": "", "type": "blocks"},
			},
		})
	})
	defer ts.Close()

	deps, _, err := fb.fetchDependencies(context.Background(), viewID)
	if err != nil {
		t.Fatalf("fetchDependencies: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("deps = %d rows, want the malformed row kept", len(deps))
	}
}
