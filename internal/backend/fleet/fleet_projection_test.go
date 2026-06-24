package fleet

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func TestGet_HydratesDependencyIssueMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/issue-1"):
			respondOK(w, fleetIssueWithCountsWire{
				fleetIssueWire: fleetIssueWire{
					ID:        "issue-1",
					Title:     "Blocked issue",
					Status:    "blocked",
					Priority:  2,
					Type:      "task",
					CreatedAt: now,
				},
				DependencyCount: 1,
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/issue-1/deps"):
			respondOK(w, struct {
				Dependencies []struct {
					IssueID     string `json:"issue_id"`
					DependsOnID string `json:"depends_on_id"`
					Type        string `json:"type"`
				} `json:"dependencies"`
			}{
				Dependencies: []struct {
					IssueID     string `json:"issue_id"`
					DependsOnID string `json:"depends_on_id"`
					Type        string `json:"type"`
				}{
					{IssueID: "issue-1", DependsOnID: "dep-1", Type: "blocks"},
				},
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/dep-1"):
			respondOK(w, fleetIssueWithCountsWire{
				fleetIssueWire: fleetIssueWire{
					ID:        "dep-1",
					Title:     "Dependency title",
					Status:    "open",
					Priority:  1,
					Type:      "bug",
					CreatedAt: now,
					CreatedBy: "alice",
				},
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/issue-1/comments"):
			respondOK(w, map[string]interface{}{"comments": []interface{}{}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	result, err := fb.Get(context.Background(), "issue-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(result.Dependencies) != 1 {
		t.Fatalf("Dependencies = %d, want 1", len(result.Dependencies))
	}
	dep := result.Dependencies[0]
	if dep.Title != "Dependency title" {
		t.Errorf("dependency title = %q, want %q", dep.Title, "Dependency title")
	}
	if dep.Status != "open" {
		t.Errorf("dependency status = %q, want %q", dep.Status, "open")
	}
	if dep.Priority != 1 {
		t.Errorf("dependency priority = %d, want 1", dep.Priority)
	}
	if dep.IssueType != "bug" {
		t.Errorf("dependency issue type = %q, want %q", dep.IssueType, "bug")
	}
}

func TestBlocked_NativeFleetDBWrapper(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	parentID := "epic-1"
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/test-ws/issues/blocked"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		body := struct {
			Issues []blockedIssueResponseWire `json:"issues"`
		}{
			Issues: []blockedIssueResponseWire{
				{
					Issue: fleetIssueWire{
						ID:        "b-1",
						Title:     "Blocked",
						Status:    string(types.StatusOpen),
						Type:      string(types.TypeTask),
						ParentID:  parentID,
						CreatedAt: now,
						UpdatedAt: now,
					},
					Blockers: []blockedBlockerWire{
						{ID: "dep-1", Title: "Dependency 1", Priority: 1},
						{ID: "dep-2", Title: "Dependency 2", Priority: 2},
					},
				},
			},
		}
		respondOK(w, body)
	})
	defer ts.Close()

	result, err := fb.Blocked(context.Background(), backend.BlockedOpts{Limit: 10})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	got := result[0]
	if got.ID != "b-1" {
		t.Errorf("ID = %q, want b-1", got.ID)
	}
	if got.Status != "open" {
		t.Errorf("Status = %q, want raw open status", got.Status)
	}
	if got.Parent != parentID {
		t.Errorf("Parent = %q, want %q", got.Parent, parentID)
	}
	if got.IssueType != string(types.TypeTask) {
		t.Errorf("IssueType = %q, want task", got.IssueType)
	}
	if got.BlockedByCount != 2 {
		t.Errorf("BlockedByCount = %d, want 2", got.BlockedByCount)
	}
	if len(got.BlockedBy) != 2 || got.BlockedBy[0] != "dep-1" || got.BlockedBy[1] != "dep-2" {
		t.Errorf("BlockedBy = %v, want [dep-1 dep-2]", got.BlockedBy)
	}
}
