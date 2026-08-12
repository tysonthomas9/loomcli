package fleet

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// TestGet_EpicChildrenAreDependentsNotSelfReferences reproduces the
// "BLOCKED BY shows N self-references" bug.
//
// Scenario: epic WEB-EXTRACTOR-1 has 3 children (ML-1, ML-2, ML-3). In
// fleet-db the parent-child rows are stored on the *children's* side:
//
//	issue_id=ML-1, depends_on_id=WEB-EXTRACTOR-1, type=parent-child
//	issue_id=ML-2, depends_on_id=WEB-EXTRACTOR-1, type=parent-child
//	issue_id=ML-3, depends_on_id=WEB-EXTRACTOR-1, type=parent-child
//
// Querying the epic's /deps endpoint returns all 3 rows. The correct
// projection is:
//   - result.Dependencies == []        (the epic depends on nothing)
//   - result.Dependents   == [ML-1, ML-2, ML-3]  (they depend on the epic)
//
// Before the fix the code funneled every row into Dependencies and only ever
// read DependsOnID (== the epic itself), producing 3 self-references.
func TestGet_EpicChildrenAreDependentsNotSelfReferences(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	const epic = "WEB-EXTRACTOR-1"
	children := []string{"ML-1", "ML-2", "ML-3"}

	childTitle := map[string]string{
		"ML-1": "Child one",
		"ML-2": "Child two",
		"ML-3": "Child three",
	}

	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == "GET" && strings.HasSuffix(path, "/issues/"+epic):
			respondOK(w, fleetIssueWithCountsWire{
				fleetIssueWire: fleetIssueWire{
					ID:        epic,
					Title:     "Web extractor epic",
					Status:    "open",
					Priority:  1,
					Type:      "epic",
					CreatedAt: now,
				},
				DependentCount: len(children),
			})
		case r.Method == "GET" && strings.HasSuffix(path, "/issues/"+epic+"/deps"):
			// fleet-db returns the children's parent-child rows: each row's
			// depends_on_id is the epic, issue_id is the child.
			type depWire struct {
				IssueID     string `json:"issue_id"`
				DependsOnID string `json:"depends_on_id"`
				Type        string `json:"type"`
			}
			rows := make([]depWire, 0, len(children))
			for _, c := range children {
				rows = append(rows, depWire{IssueID: c, DependsOnID: epic, Type: "parent-child"})
			}
			respondOK(w, struct {
				Dependencies []depWire `json:"dependencies"`
			}{Dependencies: rows})
		case r.Method == "GET" && strings.HasSuffix(path, "/issues/"+epic+"/comments"):
			respondOK(w, map[string]interface{}{"comments": []interface{}{}})
		case r.Method == "GET":
			// Per-child summary lookups used for hydration.
			for _, c := range children {
				if strings.HasSuffix(path, "/issues/"+c) {
					respondOK(w, fleetIssueWithCountsWire{
						fleetIssueWire: fleetIssueWire{
							ID:        c,
							Title:     childTitle[c],
							Status:    "open",
							Priority:  2,
							Type:      "task",
							CreatedAt: now,
						},
					})
					return
				}
			}
			t.Fatalf("unexpected request: %s %s", r.Method, path)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, path)
		}
	})
	defer ts.Close()

	result, err := fb.Get(context.Background(), workitems.GetQuery{IssueID: epic})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// The epic depends on nothing — its "Blocked By" list must be empty.
	if len(result.Dependencies) != 0 {
		t.Errorf("Dependencies = %d, want 0 (epic depends on nothing); got %+v",
			len(result.Dependencies), result.Dependencies)
	}

	// The 3 children are dependents of the epic.
	if len(result.Dependents) != len(children) {
		t.Fatalf("Dependents = %d, want %d", len(result.Dependents), len(children))
	}

	// Each dependent must carry the real child ID, NOT the epic,
	// and its own metadata (title) rather than the epic's.
	got := map[string]bool{}
	for _, d := range result.Dependents {
		if d.ID == epic {
			t.Errorf("dependent ID is the epic itself (%q) — self-reference bug", epic)
		}
		got[d.ID] = true
		if d.Title != childTitle[d.ID] {
			t.Errorf("dependent %q title = %q, want %q", d.ID, d.Title, childTitle[d.ID])
		}
	}
	for _, c := range children {
		if !got[c] {
			t.Errorf("missing child %q in Dependents", c)
		}
	}
}
