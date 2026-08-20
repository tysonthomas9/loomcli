// Tests for the reserved-label force passthrough on label removal.

package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// removeLabelRecorder answers a label DELETE plus the read-back Get that
// Update's label wait performs, recording the DELETE's raw query string.
func removeLabelRecorder(t *testing.T, labels []string) (http.HandlerFunc, *string) {
	t.Helper()
	var gotQuery string
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/labels/") {
			gotQuery = r.URL.RawQuery
			respondOK(w, json.RawMessage(`{}`))
			return
		}
		if r.Method == http.MethodGet {
			raw, _ := json.Marshal(labels)
			respondOK(w, json.RawMessage(`{"id":"test-1","status":"open","labels":`+string(raw)+`}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		respondOK(w, json.RawMessage(`{}`))
	}, &gotQuery
}

func TestUpdate_RemoveLabel_ForceQueryParam(t *testing.T) {
	handler, gotQuery := removeLabelRecorder(t, []string{})
	fb, ts := newTestServer(t, handler)
	defer ts.Close()

	err := fb.Update(context.Background(), "test-1", backend.UpdateParams{
		RemoveLabels: []string{"operator"},
		Force:        true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if *gotQuery != "force=true" {
		t.Errorf("DELETE query = %q, want %q", *gotQuery, "force=true")
	}
}

func TestUpdate_RemoveLabel_NoForceByDefault(t *testing.T) {
	handler, gotQuery := removeLabelRecorder(t, []string{})
	fb, ts := newTestServer(t, handler)
	defer ts.Close()

	err := fb.Update(context.Background(), "test-1", backend.UpdateParams{
		RemoveLabels: []string{"operator"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if *gotQuery != "" {
		t.Errorf("DELETE query = %q, want empty (force is opt-in)", *gotQuery)
	}
}

// RemoveLabel is the plain single-label entry point and never forces: only an
// explicit --force on the update path may strip a reserved label.
func TestRemoveLabel_NeverForces(t *testing.T) {
	handler, gotQuery := removeLabelRecorder(t, []string{})
	fb, ts := newTestServer(t, handler)
	defer ts.Close()

	if err := fb.RemoveLabel(context.Background(), "test-1", "operator"); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
	if *gotQuery != "" {
		t.Errorf("DELETE query = %q, want empty", *gotQuery)
	}
}

// A wholesale SetLabels reconciliation must not force: it removes labels the
// caller never named, so forcing there could silently un-park an issue.
func TestUpdate_SetLabels_DoesNotForce(t *testing.T) {
	var queries []string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/labels/") {
			queries = append(queries, r.URL.RawQuery)
			respondOK(w, json.RawMessage(`{}`))
			return
		}
		if r.Method == http.MethodGet {
			// The issue still carries "operator" on the first read; the
			// read-back wait then sees it gone.
			if len(queries) == 0 {
				respondOK(w, json.RawMessage(`{"id":"test-1","status":"open","labels":["operator","keep"]}`))
				return
			}
			respondOK(w, json.RawMessage(`{"id":"test-1","status":"open","labels":["keep"]}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	err := fb.Update(context.Background(), "test-1", backend.UpdateParams{
		SetLabels: []string{"keep"},
		Force:     true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	for _, q := range queries {
		if q != "" {
			t.Errorf("SetLabels DELETE query = %q, want empty (never forced)", q)
		}
	}
}
