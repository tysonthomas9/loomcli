// Tests for the reserved-label force passthrough on label removal, and for the
// operator attribution that must survive alongside it.

package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// labelDelete is one label DELETE as it appeared on the wire.
type labelDelete struct {
	query string
	actor string
}

// removeLabelRecorder answers a label DELETE plus the read-back Get that
// Update's label wait performs, recording each DELETE's raw query string and
// X-Actor header.
func removeLabelRecorder(t *testing.T, labels []string) (http.HandlerFunc, *[]labelDelete) {
	t.Helper()
	var deletes []labelDelete
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/labels/") {
			deletes = append(deletes, labelDelete{query: r.URL.RawQuery, actor: r.Header.Get("X-Actor")})
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
	}, &deletes
}

func assertSingleLabelDelete(t *testing.T, deletes []labelDelete, wantQuery, wantActor string) {
	t.Helper()
	if len(deletes) != 1 {
		t.Fatalf("label DELETEs = %d, want 1", len(deletes))
	}
	if deletes[0].query != wantQuery {
		t.Errorf("DELETE query = %q, want %q", deletes[0].query, wantQuery)
	}
	if deletes[0].actor != wantActor {
		t.Errorf("DELETE X-Actor = %q, want %q", deletes[0].actor, wantActor)
	}
}

// An explicit RemoveLabels + Force carries ?force=true and still attributes the
// removal: forcing must not cost the operator attribution.
func TestUpdate_RemoveLabel_ForceQueryParam(t *testing.T) {
	for _, tt := range operatorActorCases() {
		t.Run(tt.name, func(t *testing.T) {
			handler, deletes := removeLabelRecorder(t, []string{})
			fb, ts := newOperatorActorTestBackend(t, handler)
			defer ts.Close()

			err := fb.Update(context.Background(), "test-1", backend.UpdateParams{
				Actor:        tt.actor,
				RemoveLabels: []string{"operator"},
				Force:        true,
			})
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			assertSingleLabelDelete(t, *deletes, "force=true", tt.wantActor)
		})
	}
}

func TestUpdate_RemoveLabel_NoForceByDefault(t *testing.T) {
	handler, deletes := removeLabelRecorder(t, []string{})
	fb, ts := newOperatorActorTestBackend(t, handler)
	defer ts.Close()

	err := fb.Update(context.Background(), "test-1", backend.UpdateParams{
		Actor:        "operator@local",
		RemoveLabels: []string{"operator"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertSingleLabelDelete(t, *deletes, "", "operator@local")
}

// RemoveLabel is the plain single-label entry point and never forces: only an
// explicit --force on the update path may strip a reserved label. It carries
// the process identity, since it takes no actor of its own.
func TestRemoveLabel_NeverForces(t *testing.T) {
	handler, deletes := removeLabelRecorder(t, []string{})
	fb, ts := newOperatorActorTestBackend(t, handler)
	defer ts.Close()

	if err := fb.RemoveLabel(context.Background(), "test-1", "operator"); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
	assertSingleLabelDelete(t, *deletes, "", "process@local")
}

// A wholesale SetLabels reconciliation must not force: it removes labels the
// caller never named, so forcing there could silently un-park an issue. The
// removals it does make are still attributed.
func TestUpdate_SetLabels_DoesNotForce(t *testing.T) {
	var deletes []labelDelete
	fb, ts := newOperatorActorTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/labels/") {
			deletes = append(deletes, labelDelete{query: r.URL.RawQuery, actor: r.Header.Get("X-Actor")})
			respondOK(w, json.RawMessage(`{}`))
			return
		}
		if r.Method == http.MethodGet {
			// The issue still carries "operator" on the first read; the
			// read-back wait then sees it gone.
			if len(deletes) == 0 {
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
		Actor:     "operator@local",
		SetLabels: []string{"keep"},
		Force:     true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertSingleLabelDelete(t, deletes, "", "operator@local")
}
