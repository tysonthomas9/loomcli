package workflows

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

// TestListWorkflowsHTTP: GET …/workflows returns one summary per driver, with
// the active version's approval/trust surfaced once a version is active.
func TestListWorkflowsHTTP(t *testing.T) {
	ctx := context.Background()
	st := seedEpicDriver(t, ctx)
	mux := newVersioningMux(st)

	// No active version yet.
	rec := doRequest(t, mux, http.MethodGet, "/api/workspaces/TEST/workflows", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var out struct {
		Workflows []struct {
			DriverID        string `json:"driver_id"`
			BuiltIn         bool   `json:"built_in"`
			ActiveVersionID string `json:"active_version_id"`
			Approved        bool   `json:"approved"`
			EffectiveTrust  string `json:"effective_trust"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(out.Workflows) != 1 {
		t.Fatalf("workflows = %d, want 1: %s", len(out.Workflows), rec.Body.String())
	}
	wf := out.Workflows[0]
	if wf.DriverID != BuiltinEpicRunnerWorkflowName || !wf.BuiltIn || wf.ActiveVersionID != "" {
		t.Fatalf("summary = %+v, want built-in epic-runner with no active version", wf)
	}

	// Activate + approve version-1, then the summary reflects it.
	if _, _, err := driverpkg.ActivateDriverVersion(ctx, st, "TEST", BuiltinEpicRunnerWorkflowName, "version-1"); err != nil {
		t.Fatalf("activate v1: %v", err)
	}
	if _, _, err := driverpkg.ApproveDriverVersion(ctx, st, "TEST", BuiltinEpicRunnerWorkflowName, "version-1"); err != nil {
		t.Fatalf("approve v1: %v", err)
	}
	rec = doRequest(t, mux, http.MethodGet, "/api/workspaces/TEST/workflows", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list 2: %v", err)
	}
	wf = out.Workflows[0]
	if wf.ActiveVersionID != "version-1" || !wf.Approved || wf.EffectiveTrust != "trusted" {
		t.Fatalf("summary after activate+approve = %+v, want version-1 approved trusted", wf)
	}
}

// TestApproveUnapproveVersionHTTP: the approve/unapprove routes flip approval
// and return the refreshed version row.
func TestApproveUnapproveVersionHTTP(t *testing.T) {
	ctx := context.Background()
	st := seedEpicDriver(t, ctx)
	mux := newVersioningMux(st)
	base := "/api/workspaces/TEST/workflows/" + BuiltinEpicRunnerWorkflowName + "/versions/version-1"

	rec := doRequest(t, mux, http.MethodPost, base+"/approve", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var out struct {
		Approved bool `json:"approved"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode approve: %v", err)
	}
	if !out.Approved {
		t.Fatalf("approve out = %+v, want approved=true", out)
	}

	rec = doRequest(t, mux, http.MethodPost, base+"/unapprove", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unapprove status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode unapprove: %v", err)
	}
	if out.Approved {
		t.Fatalf("unapprove out = %+v, want approved=false", out)
	}
}

// TestApproveReservedNameRejectedHTTP: "builtin" is a reserved path literal and
// must be rejected before any driver resolution.
func TestApproveReservedNameRejectedHTTP(t *testing.T) {
	ctx := context.Background()
	st := seedEpicDriver(t, ctx)
	mux := newVersioningMux(st)
	rec := doRequest(t, mux, http.MethodPost, "/api/workspaces/TEST/workflows/builtin/versions/version-1/approve", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("approve reserved status = %d, want 400", rec.Code)
	}
}

// TestApproveUnknownWorkflowHTTP: approving on a workflow that does not exist is
// a 404, not a 500.
func TestApproveUnknownWorkflowHTTP(t *testing.T) {
	ctx := context.Background()
	st := seedEpicDriver(t, ctx)
	mux := newVersioningMux(st)
	rec := doRequest(t, mux, http.MethodPost, "/api/workspaces/TEST/workflows/does-not-exist/versions/version-1/approve", `{}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("approve unknown status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}
