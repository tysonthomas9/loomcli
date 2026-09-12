package fleet

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestGet_PopulatesParent verifies fleet-db's parent_id (returned on /issues/{id})
// is projected onto IssueDetailData.Parent, which is what `loom data show
// --output json` exposes. Without this projection the field is silently null
// even though fleet-db has the relationship recorded.
func TestGet_PopulatesParent(t *testing.T) {
	now := time.Now().UTC()
	wire := map[string]any{
		"id":         "child-1",
		"title":      "Child",
		"status":     "open",
		"type":       "task",
		"parent_id":  "epic-1",
		"created_at": now,
		"updated_at": now,
	}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues/child-1"):
			respondOK(w, wire)
		case strings.HasSuffix(r.URL.Path, "/deps"), strings.HasSuffix(r.URL.Path, "/comments"):
			respondOK(w, map[string]interface{}{})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	result, err := fb.Get(context.Background(), "child-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if result.Parent != "epic-1" {
		t.Fatalf("Parent = %q, want epic-1", result.Parent)
	}
}

// TestGet_EstimatedMinutes verifies fleet-db's estimated_minutes survives the
// full Get path (fleetIssueWire -> toIssue -> detailsToDetailData). It was
// dropped on every fleet read until fleetIssueWire declared the field, which
// meant a value written straight to fleet-db read back as absent.
//
// Lives here rather than in fleet_test.go, which sits 40 lines under the gate's
// 2500-line test-file ceiling; TestGet_PopulatesParent above is the same shape.
func TestGet_EstimatedMinutes(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name string
		est  any // nil means the key is absent from the response
		want *int
	}{
		{"populated", 30, intPtr(30)},
		{"explicit zero", 0, intPtr(0)},
		{"absent", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire := map[string]any{
				"id":         "issue-1",
				"title":      "Estimated",
				"status":     "open",
				"type":       "task",
				"created_at": now,
				"updated_at": now,
			}
			if tt.est != nil {
				wire["estimated_minutes"] = tt.est
			}
			fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/issues/issue-1"):
					respondOK(w, wire)
				case strings.HasSuffix(r.URL.Path, "/deps"):
					respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
				case strings.HasSuffix(r.URL.Path, "/comments"):
					respondOK(w, map[string]interface{}{"comments": []interface{}{}})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			})
			defer ts.Close()

			result, err := fb.Get(context.Background(), "issue-1")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			switch {
			case tt.want == nil && result.EstimatedMinutes != nil:
				t.Fatalf("EstimatedMinutes = %d, want nil", *result.EstimatedMinutes)
			case tt.want != nil && result.EstimatedMinutes == nil:
				t.Fatalf("EstimatedMinutes = nil, want %d", *tt.want)
			case tt.want != nil && *result.EstimatedMinutes != *tt.want:
				t.Fatalf("EstimatedMinutes = %d, want %d", *result.EstimatedMinutes, *tt.want)
			}
		})
	}
}
