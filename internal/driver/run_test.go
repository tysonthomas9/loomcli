//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestCreateDriverRunUsesActivePassedVersion(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST",
		DriverID:     "epic-runner",
		Name:         "epic-runner",
		Status:       workflowcatalog.DriverStatusDraft,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "TEST",
		VersionID:        "version-1",
		DriverID:         "epic-runner",
		Version:          1,
		SourceDigest:     "sha256:source1",
		BundleDigest:     "sha256:bundle1",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create version 1: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "TEST",
		VersionID:        "version-2",
		DriverID:         "epic-runner",
		Version:          2,
		SourceDigest:     "sha256:source2",
		BundleDigest:     "sha256:bundle2",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create version 2: %v", err)
	}
	if _, err := st.ApproveDriverVersionForTest(ctx, "TEST", "epic-runner", "version-1"); err != nil {
		t.Fatalf("Approve driver: %v", err)
	}
	if _, err := st.ActivateDriverVersionForTest(ctx, "TEST", "epic-runner", "version-1"); err != nil {
		t.Fatalf("Activate driver: %v", err)
	}

	run, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey:   "TEST",
		DriverID:       "epic-runner",
		EpicID:         "TEST-1",
		RunID:          "run-1",
		IdempotencyKey: "idem-1",
		Payload:        json.RawMessage(`{"epicId":"wrong","requestedBy":"cli"}`),
	})
	if err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	if run.Status != domain.DriverRunQueued || run.DriverVersionID != "version-1" || string(run.Payload) != `{"epicId":"wrong","requestedBy":"cli"}` {
		t.Fatalf("run = %+v, want queued pinned version-1 with raw payload", run)
	}

	replay, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey:   "TEST",
		DriverID:       "epic-runner",
		EpicID:         "TEST-1",
		RunID:          "run-replay",
		IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("CreateDriverRun replay: %v", err)
	}
	if replay.RunID != "run-1" {
		t.Fatalf("replay run_id = %q, want run-1", replay.RunID)
	}

	active, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey:   "TEST",
		DriverID:       "epic-runner",
		EpicID:         "TEST-1",
		RunID:          "run-active",
		IdempotencyKey: "idem-2",
	})
	if err != nil {
		t.Fatalf("CreateDriverRun active replay: %v", err)
	}
	if active.RunID != "run-1" {
		t.Fatalf("active run_id = %q, want run-1", active.RunID)
	}
}

func TestCreateDriverRunCanPinNonActivePreviewVersion(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST",
		DriverID:     "epic-runner",
		Name:         "epic-runner",
		Status:       workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	for _, in := range []store.DriverVersionCreate{
		{WorkspaceKey: "TEST", VersionID: "version-active", DriverID: "epic-runner", Version: 1, SourceDigest: "sha256:active", BundleDigest: "sha256:bundle-active", ValidationStatus: workflowcatalog.DriverVersionValidationPassed},
		{WorkspaceKey: "TEST", VersionID: "version-preview", DriverID: "epic-runner", Version: 2, SourceDigest: "sha256:preview", BundleDigest: "sha256:bundle-preview", ValidationStatus: workflowcatalog.DriverVersionValidationPassed},
	} {
		if _, err := st.DriverVersions().Create(ctx, in); err != nil {
			t.Fatalf("Create version %s: %v", in.VersionID, err)
		}
	}
	if _, err := st.ApproveDriverVersionForTest(ctx, "TEST", "epic-runner", "version-active"); err != nil {
		t.Fatalf("Approve driver: %v", err)
	}
	if _, err := st.ActivateDriverVersionForTest(ctx, "TEST", "epic-runner", "version-active"); err != nil {
		t.Fatalf("Activate driver: %v", err)
	}

	run, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey:    "TEST",
		DriverID:        "epic-runner",
		DriverVersionID: "version-preview",
		RunID:           "run-preview",
		Payload:         json.RawMessage(`{"preview":true}`),
	})
	if err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	if run.DriverVersionID != "version-preview" {
		t.Fatalf("run driver_version_id = %q, want preview version", run.DriverVersionID)
	}
}

func TestVersionScopedApprovalDoesNotTrustSiblingVersions(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST",
		DriverID:     "epic-runner",
		Name:         "epic-runner",
		Status:       workflowcatalog.DriverStatusActive,
		TrustLevel:   workflowcatalog.DriverTrustTrusted,
		Metadata:     map[string]string{"active": "metadata"},
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	trustedManifest := map[string]string{ManifestTrustLevelKey: string(workflowcatalog.DriverTrustTrusted)}
	untrustedManifest := map[string]string{ManifestTrustLevelKey: string(workflowcatalog.DriverTrustUntrusted)}
	for _, in := range []store.DriverVersionCreate{
		{WorkspaceKey: "TEST", VersionID: "version-trusted", DriverID: "epic-runner", Version: 1, SourceDigest: "sha256:trusted", BundleDigest: "sha256:bundle-trusted", Manifest: trustedManifest, ValidationStatus: workflowcatalog.DriverVersionValidationPassed},
		{WorkspaceKey: "TEST", VersionID: "version-custom-1", DriverID: "epic-runner", Version: 2, SourceDigest: "sha256:custom-1", BundleDigest: "sha256:bundle-custom-1", Manifest: untrustedManifest, ValidationStatus: workflowcatalog.DriverVersionValidationPassed},
		{WorkspaceKey: "TEST", VersionID: "version-custom-2", DriverID: "epic-runner", Version: 3, SourceDigest: "sha256:custom-2", BundleDigest: "sha256:bundle-custom-2", Manifest: untrustedManifest, ValidationStatus: workflowcatalog.DriverVersionValidationPassed},
	} {
		if _, err := st.DriverVersions().Create(ctx, in); err != nil {
			t.Fatalf("Create version %s: %v", in.VersionID, err)
		}
	}
	driver, err := st.Drivers().Get(ctx, "TEST", "epic-runner")
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	custom1, err := st.DriverVersions().Get(ctx, "TEST", "version-custom-1")
	if err != nil {
		t.Fatalf("get custom1: %v", err)
	}
	custom2, err := st.DriverVersions().Get(ctx, "TEST", "version-custom-2")
	if err != nil {
		t.Fatalf("get custom2: %v", err)
	}
	if got := DriverVersionEffectiveTrust(driver, custom1); got != workflowcatalog.DriverTrustUntrusted {
		t.Fatalf("custom1 trust before approval = %q, want untrusted", got)
	}
	driver, err = st.ApproveDriverVersionForTest(ctx, "TEST", "epic-runner", custom1.VersionID)
	if err != nil {
		t.Fatalf("approve test fixture version: %v", err)
	}
	if got := DriverVersionEffectiveTrust(driver, custom1); got != workflowcatalog.DriverTrustTrusted {
		t.Fatalf("custom1 trust after approval = %q, want trusted", got)
	}
	if got := DriverVersionEffectiveTrust(driver, custom2); got != workflowcatalog.DriverTrustUntrusted {
		t.Fatalf("custom2 trust inherited from custom1 approval = %q, want untrusted", got)
	}
	driver, _, err = ActivateDriverVersion(ctx, st, "TEST", "epic-runner", "version-custom-2")
	if err != nil {
		t.Fatalf("ActivateDriverVersion: %v", err)
	}
	if !DriverVersionApproved(driver, custom1) {
		t.Fatalf("activation dropped version-custom-1 approval metadata")
	}
	if DriverVersionApproved(driver, custom2) {
		t.Fatalf("activation implicitly approved version-custom-2")
	}
	driver, err = st.UnapproveDriverVersionForTest(ctx, "TEST", "epic-runner", custom1.VersionID)
	if err != nil {
		t.Fatalf("unapprove test fixture version: %v", err)
	}
	if DriverVersionApproved(driver, custom1) {
		t.Fatalf("custom1 still approved after unapprove")
	}
}

func TestCreateDriverRunRejectsDriverWithoutActiveVersion(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST",
		DriverID:     "draft-driver",
		Name:         "draft-driver",
		Status:       workflowcatalog.DriverStatusDraft,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: "draft-driver", RunID: "run-1"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("CreateDriverRun err = %v, want ErrInvalid", err)
	}
}
