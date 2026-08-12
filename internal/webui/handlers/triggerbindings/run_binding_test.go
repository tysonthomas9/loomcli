package triggerbindings

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// The binding-scoped run-now endpoint stamps the binding on the run and carries
// NO client-supplied run-input — config travels by reference. The run's roleName
// is resolved later via loom.binding.config(), not copied into the payload here.
func TestRunBinding_StampsBindingAndOmitsRunInput(t *testing.T) {
	mux, _ := seededMux(t)

	// A prompt-agent cron binding whose run-input configures a role.
	create := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","binding_id":"prompt-agent-cron","schedule":"*/10 * * * *","run_input":{"roleName":"docs-assistant"},"enabled":true}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", create.Code, create.Body.String())
	}

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings/prompt-agent-cron/run", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var run domain.DriverRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.TriggerBindingID != "prompt-agent-cron" {
		t.Fatalf("run trigger_binding_id = %q, want prompt-agent-cron", run.TriggerBindingID)
	}
	// Route-key stamped as source ref so provenance resolves the binding even if
	// the backing store drops trigger_binding_id on create.
	if run.SourceRef != "cron:prompt-agent-cron" {
		t.Fatalf("run source_ref = %q, want cron:prompt-agent-cron", run.SourceRef)
	}
	if run.DriverID != "driver-1" || run.Status != domain.DriverRunQueued {
		t.Fatalf("unexpected run: driver=%q status=%q", run.DriverID, run.Status)
	}
	// The whole point: run-input is NOT merged into the payload client-side.
	if strings.Contains(string(run.Payload), "roleName") {
		t.Fatalf("run payload leaked run-input (config must travel by reference): %s", run.Payload)
	}
}

// A run-now against an unknown binding is a clean 404, not a fabricated run.
func TestRunBinding_UnknownBinding404(t *testing.T) {
	mux, _ := seededMux(t)
	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings/nope/run", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("run status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
