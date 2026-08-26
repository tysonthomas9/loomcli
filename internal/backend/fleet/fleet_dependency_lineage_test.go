package fleet

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

func TestDependencyTaskIDsReplaysCompleteDependencyHistory(t *testing.T) {
	requests := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got, want := r.URL.Path, "/api/v2/test-ws/issues/TASK-B/history"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("action"), "dep.add,dep.remove"; got != want {
			t.Fatalf("action = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("limit"), "200"; got != want {
			t.Fatalf("limit = %q, want %q", got, want)
		}
		switch requests {
		case 1:
			if got := r.URL.Query().Get("since"); got != "0" {
				t.Fatalf("first since = %q, want 0", got)
			}
			respondOK(w, map[string]any{
				"history": []map[string]any{
					{"action": "dep.add", "metadata": map[string]string{"depends_on_id": "TASK-A", "dep_type": "blocks"}},
					{"action": "dep.add", "metadata": map[string]string{"depends_on_id": "TASK-X", "dep_type": "related"}},
					{"action": "dep.add", "metadata": map[string]string{"depends_on_id": "TASK-C", "dep_type": "blocks"}},
				},
				"cursor": "opaque-page-1", "has_more": true, "total_events": 4,
			})
		case 2:
			if got := r.URL.Query().Get("since"); got != "opaque-page-1" {
				t.Fatalf("second since = %q, want opaque-page-1", got)
			}
			respondOK(w, map[string]any{
				"history": []map[string]any{
					{"action": "dep.remove", "metadata": map[string]string{"depends_on_id": "TASK-C", "dep_type": "blocks"}},
				},
				"cursor": "opaque-page-2", "has_more": false, "total_events": 4,
			})
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	})
	defer ts.Close()

	got, err := fb.DependencyTaskIDs(context.Background(), "TASK-B")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"TASK-A"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DependencyTaskIDs = %v, want %v", got, want)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestDependencyTaskIDsRejectsNonAdvancingHistoryCursor(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		respondOK(w, map[string]any{
			"history": []map[string]any{},
			"cursor":  r.URL.Query().Get("since"), "has_more": true, "total_events": 1,
		})
	})
	defer ts.Close()

	if _, err := fb.DependencyTaskIDs(context.Background(), "TASK-B"); err == nil {
		t.Fatal("DependencyTaskIDs returned nil error for non-advancing cursor")
	}
}
