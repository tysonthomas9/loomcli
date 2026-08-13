package fleet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// A Planner writing its design sends design + design_format together. fleet-db
// strict-decodes and rejects design_format, which used to fail the whole PATCH
// and lose the design with it (FINDINGS §1.13). The design must still land.
func TestUpdateDropsUnknownFieldAndKeepsTheRest(t *testing.T) {
	var bodies []map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			respondOK(w, json.RawMessage(`{}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		bodies = append(bodies, body)
		if _, ok := body["design_format"]; ok {
			respondErr(w, http.StatusBadRequest, `unknown field "design_format"`)
			return
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	err := fb.Update(context.Background(), "E2E-WS-2", backend.UpdateParams{
		Design:       strPtr("## Design\n\nAFT-LW2 token"),
		DesignFormat: strPtr("markdown"),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected one rejected PATCH then one retry, got %d", len(bodies))
	}
	if _, ok := bodies[1]["design_format"]; ok {
		t.Errorf("retry still carried design_format: %v", bodies[1])
	}
	// The whole point: the field that fleet-db DOES accept must survive.
	if got := bodies[1]["design"]; got != "## Design\n\nAFT-LW2 token" {
		t.Errorf("design did not survive the retry: %#v", got)
	}
}

// If every field is rejected, nothing landed — reporting success would be
// silent data loss.
func TestUpdateFailsWhenEveryFieldIsRejected(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		respondErr(w, http.StatusBadRequest, `unknown field "design_format"`)
	})
	defer ts.Close()

	err := fb.Update(context.Background(), "E2E-WS-2", backend.UpdateParams{
		DesignFormat: strPtr("markdown"),
	})
	if err == nil {
		t.Fatal("expected an error when fleet-db rejected every field")
	}
	if !strings.Contains(err.Error(), "design_format") {
		t.Errorf("error should name the rejected field, got: %v", err)
	}
}

// A validation error that is not an unknown-field rejection must propagate
// unchanged rather than being retried into a different shape.
func TestUpdateDoesNotRetryUnrelatedValidationErrors(t *testing.T) {
	patches := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		patches++
		respondErr(w, http.StatusBadRequest, "priority must be between 0 and 4")
	})
	defer ts.Close()

	err := fb.Update(context.Background(), "E2E-WS-2", backend.UpdateParams{
		Design: strPtr("d"),
	})
	if err == nil {
		t.Fatal("expected the validation error to propagate")
	}
	if patches != 1 {
		t.Errorf("unrelated validation error must not be retried, got %d PATCHes", patches)
	}
}

// If the server names a field loom did not send, retrying would loop on an
// unchanged body forever.
func TestUpdateDoesNotLoopWhenServerNamesAnUnsentField(t *testing.T) {
	patches := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		patches++
		respondErr(w, http.StatusBadRequest, `unknown field "not_sent_by_loom"`)
	})
	defer ts.Close()

	err := fb.Update(context.Background(), "E2E-WS-2", backend.UpdateParams{
		Design: strPtr("d"),
	})
	if err == nil {
		t.Fatal("expected the error to propagate")
	}
	if patches != 1 {
		t.Errorf("expected exactly one PATCH, got %d — the retry loop did not terminate", patches)
	}
}

// A business-rule error that merely mentions the phrase must NOT cause loom to
// delete a field and retry — it has to surface as the validation failure it is.
func TestUpdateDoesNotStripOnUnrelatedMessageMentioningUnknownField(t *testing.T) {
	patches := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		patches++
		respondErr(w, http.StatusBadRequest, `policy references unknown field "owner"`)
	})
	defer ts.Close()

	err := fb.Update(context.Background(), "E2E-WS-1", backend.UpdateParams{
		Design: strPtr("d"), Owner: strPtr("alice"),
	})
	if err == nil {
		t.Fatal("expected the business-rule validation error to propagate")
	}
	if patches != 1 {
		t.Errorf("must not retry on a non-strict-decoder message, got %d PATCHes", patches)
	}
}
