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
