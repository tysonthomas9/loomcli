package workflow

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/workflows"
)

func TestWorkflowSyncRejectsCustomWorkflow(t *testing.T) {
	_, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	err := runWorkflowSync(&cobra.Command{}, []string{"my-custom-wf"})
	if err == nil || !strings.Contains(err.Error(), "not_builtin_workflow") {
		t.Fatalf("sync custom workflow err = %v, want not_builtin_workflow", err)
	}
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestWorkflowRollbackWithoutRecordFails(t *testing.T) {
	_, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	// The fixture driver is active on version-1 with no activation_previous
	// record, so rollback has no target.
	err := runWorkflowRollback(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	if err == nil || !strings.Contains(err.Error(), "rollback_target_missing") {
		t.Fatalf("rollback err = %v, want rollback_target_missing", err)
	}
}

func TestWorkflowActivateTrackAutoOnNonPackagedFails(t *testing.T) {
	_, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	workflows.ResetPackagedCacheForTest()
	workflowVersionID = "version-2"
	workflowActivateTrack = "auto"
	err := runWorkflowActivate(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	if err == nil || !strings.Contains(err.Error(), "builtin_track_invalid") {
		t.Fatalf("activate --track auto on non-packaged version err = %v, want builtin_track_invalid", err)
	}
}

func TestWorkflowActivateBuiltinAndVersionMutuallyExclusive(t *testing.T) {
	_, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	workflowVersionID = "version-2"
	workflowActivateBuiltin = true
	err := runWorkflowActivate(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("activate --builtin --version err = %v, want mutual exclusion", err)
	}
}

func TestWorkflowActivateRequiresVersionOrBuiltin(t *testing.T) {
	_, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	// Neither --version nor --builtin.
	err := runWorkflowActivate(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	if err == nil || !strings.Contains(err.Error(), "one of --version or --builtin") {
		t.Fatalf("activate with no flags err = %v, want one-required", err)
	}
}

func TestWorkflowActivateVersionPinnedRecordsOperator(t *testing.T) {
	ctx, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	workflowVersionID = "version-2"
	// default track pinned
	if err := runWorkflowActivate(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName}); err != nil {
		t.Fatalf("activate --version: %v", err)
	}
	drv, err := st.Drivers().Get(ctx, "TEST", workflows.BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if drv.ActiveVersionID != "version-2" {
		t.Fatalf("active = %q, want version-2", drv.ActiveVersionID)
	}
	if got := drv.Metadata["activation_reason"]; got != "operator" {
		t.Fatalf("activation_reason = %q, want operator", got)
	}
	if got := drv.Metadata["builtin_track"]; got != "pinned" {
		t.Fatalf("builtin_track = %q, want pinned", got)
	}
}

func TestWorkflowVersionsListsBuiltinBlockNewestFirst(t *testing.T) {
	_, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	workflows.ResetPackagedCacheForTest()
	workflowVersionsJSON = true
	out, err := captureWorkflowStdout(t, func() error {
		return runWorkflowVersions(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	var payload struct {
		DriverID string `json:"driver_id"`
		Versions []struct {
			Version struct {
				VersionID string `json:"version_id"`
				Version   int    `json:"version"`
			} `json:"version"`
			BundleVerified bool `json:"bundle_verified"`
		} `json:"versions"`
		Builtin *workflows.BuiltinVersionsInfo `json:"builtin"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode versions JSON %q: %v", out, err)
	}
	if len(payload.Versions) != 2 {
		t.Fatalf("versions len = %d, want 2", len(payload.Versions))
	}
	if payload.Versions[0].Version.VersionID != "version-2" || payload.Versions[1].Version.VersionID != "version-1" {
		t.Fatalf("versions not newest-first: %q, %q", payload.Versions[0].Version.VersionID, payload.Versions[1].Version.VersionID)
	}
	if payload.Builtin == nil {
		t.Fatal("versions JSON missing builtin block for a built-in workflow")
	}
	// No packaged tree in this test → the block reports the reason without
	// failing the listing (AC#12).
	if payload.Builtin.PackagedError == "" {
		t.Fatalf("expected packaged_error on a dev binary without artifacts, got block %+v", payload.Builtin)
	}
}
