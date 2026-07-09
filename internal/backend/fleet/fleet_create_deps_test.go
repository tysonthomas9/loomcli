package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// depRowWire mirrors one row of fleet-db's GET /issues/{id}/deps response for
// the mock servers below. Title is populated so depWireToData skips the
// related-issue hydration fetch.
type depRowWire struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
}

// TestCreate_WithDependencies_ComposesDepsCalls reproduces the silent
// --depends-on drop: CreateParams.Dependencies has no inline equivalent on
// fleet-db's CreateIssueRequest, so before the fix the edges were lost without
// error. Create must now compose one POST /issues/{id}/deps per dependency
// (type=blocks) after the issue exists.
func TestCreate_WithDependencies_ComposesDepsCalls(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var mu sync.Mutex
	var depBodies []map[string]string
	var depRows []depRowWire

	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == "POST" && strings.HasSuffix(path, "/issues/new-1/deps"):
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			mu.Lock()
			depBodies = append(depBodies, body)
			depRows = append(depRows, depRowWire{
				IssueID:     "new-1",
				DependsOnID: body["depends_on_id"],
				Type:        body["type"],
				Title:       "Blocker " + body["depends_on_id"],
			})
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(apiResponse{Success: true}) //nolint:errcheck
		case r.Method == "POST" && strings.HasSuffix(path, "/issues"):
			respondOK(w, types.Issue{
				ID:        "new-1",
				Title:     "Milestone 1",
				Status:    types.StatusOpen,
				Priority:  2,
				IssueType: types.TypeTask,
				CreatedAt: now,
				UpdatedAt: now,
			})
		case r.Method == "GET" && strings.HasSuffix(path, "/issues/new-1"):
			respondOK(w, fleetIssueWithCountsWire{
				fleetIssueWire: fleetIssueWire{ID: "new-1", Title: "Milestone 1", Status: "open", CreatedAt: now},
			})
		case r.Method == "GET" && strings.HasSuffix(path, "/issues/new-1/deps"):
			mu.Lock()
			rows := append([]depRowWire(nil), depRows...)
			mu.Unlock()
			respondOK(w, struct {
				Dependencies []depRowWire `json:"dependencies"`
			}{Dependencies: rows})
		case r.Method == "GET" && strings.HasSuffix(path, "/issues/new-1/comments"):
			respondOK(w, struct {
				Comments []fleetCommentWire `json:"comments"`
			}{})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer ts.Close()

	result, err := fb.Create(context.Background(), backend.CreateParams{
		Title:        "Milestone 1",
		IssueType:    "task",
		Priority:     2,
		Dependencies: []string{"m0", "m9"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Issue.ID != "new-1" {
		t.Errorf("ID = %q, want %q", result.Issue.ID, "new-1")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(depBodies) != 2 {
		t.Fatalf("dep POSTs = %d (%v), want 2", len(depBodies), depBodies)
	}
	for i, want := range []string{"m0", "m9"} {
		if depBodies[i]["depends_on_id"] != want {
			t.Errorf("dep[%d].depends_on_id = %q, want %q", i, depBodies[i]["depends_on_id"], want)
		}
		if depBodies[i]["type"] != "blocks" {
			t.Errorf("dep[%d].type = %q, want %q", i, depBodies[i]["type"], "blocks")
		}
	}
}

// TestCreate_DependencyAddFails_ErrorNamesCreatedIssue: when the issue is
// created but a dependency edge cannot be added, the error must say the issue
// already exists (so callers don't blind-retry the create) and preserve the
// underlying error kind. The partially-created issue is still returned.
func TestCreate_DependencyAddFails_ErrorNamesCreatedIssue(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == "POST" && strings.HasSuffix(path, "/issues/new-2/deps"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(apiResponse{Success: false, Error: "issue ghost-9 not found"}) //nolint:errcheck
		case r.Method == "POST" && strings.HasSuffix(path, "/issues"):
			respondOK(w, types.Issue{
				ID:        "new-2",
				Title:     "Milestone 2",
				Status:    types.StatusOpen,
				IssueType: types.TypeTask,
				CreatedAt: now,
				UpdatedAt: now,
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer ts.Close()

	result, err := fb.Create(context.Background(), backend.CreateParams{
		Title:        "Milestone 2",
		IssueType:    "task",
		Dependencies: []string{"ghost-9"},
	})
	if err == nil {
		t.Fatal("Create: want error when dependency add fails, got nil")
	}
	if !strings.Contains(err.Error(), "new-2 was created") {
		t.Errorf("error %q should name the created issue", err)
	}
	if !strings.Contains(err.Error(), "ghost-9") {
		t.Errorf("error %q should name the failed dependency", err)
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Errorf("error kind = %v, want KindNotFound preserved from the deps call", err)
	}
	if result == nil || result.Issue.ID != "new-2" {
		t.Errorf("result = %#v, want partially-created issue new-2 returned alongside the error", result)
	}
}
