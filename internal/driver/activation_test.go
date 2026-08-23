//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// newActivationStore builds a memstore with one driver and two passed versions
// (v1, v2), no active version. Manifests carry trust_level=trusted so
// activation is permitted through loadDriverVersionForOperatorAction.
func newActivationStore(t *testing.T) store.Store {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST",
		DriverID:     "epic-runner",
		Name:         "epic-runner",
		Status:       domain.DriverStatusDraft,
		Metadata:     map[string]string{"source_ref": "builtin://x"},
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	trusted := map[string]string{ManifestTrustLevelKey: string(domain.DriverTrustTrusted)}
	for _, in := range []store.DriverVersionCreate{
		{WorkspaceKey: "TEST", VersionID: "v1", DriverID: "epic-runner", Version: 1, SourceDigest: "sha256:v1", BundleDigest: "sha256:b1", Manifest: cloneStringMap(trusted), ValidationStatus: domain.DriverVersionValidationPassed},
		{WorkspaceKey: "TEST", VersionID: "v2", DriverID: "epic-runner", Version: 2, SourceDigest: "sha256:v2", BundleDigest: "sha256:b2", Manifest: cloneStringMap(trusted), ValidationStatus: domain.DriverVersionValidationPassed},
	} {
		if _, err := st.DriverVersions().Create(ctx, in); err != nil {
			t.Fatalf("create version %s: %v", in.VersionID, err)
		}
	}
	return st
}

func fixedClock(ts string) func() time.Time {
	return func() time.Time {
		parsed, _ := time.Parse(time.RFC3339, ts)
		return parsed
	}
}

func TestActivateDriverVersionWithOptionsWritesRecord(t *testing.T) {
	ctx := context.Background()
	st := newActivationStore(t)

	drv, _, err := ActivateDriverVersionWithOptions(ctx, st, "TEST", "epic-runner", "v1", ActivationOptions{
		Actor:  ActivationActorSystem,
		Reason: ActivationReasonBuiltinSync,
		Track:  BuiltinTrackAuto,
		Now:    fixedClock("2026-08-23T10:00:00Z"),
	})
	if err != nil {
		t.Fatalf("activate v1: %v", err)
	}
	if drv.ActiveVersionID != "v1" {
		t.Fatalf("active = %q, want v1", drv.ActiveVersionID)
	}
	want := map[string]string{
		MetadataKeyActivationActor:           "system",
		MetadataKeyActivationReason:          "builtin_sync",
		MetadataKeyActivationAt:              "2026-08-23T10:00:00Z",
		MetadataKeyActivationPreviousVersion: "",
		MetadataKeyBuiltinTrack:              "auto",
	}
	for k, v := range want {
		if got := drv.Metadata[k]; got != v {
			t.Fatalf("metadata[%q] = %q, want %q", k, got, v)
		}
	}

	// Second activation overwrites activation_previous_version_id with the prior
	// active version and updates the record fresh.
	drv, _, err = ActivateDriverVersionWithOptions(ctx, st, "TEST", "epic-runner", "v2", ActivationOptions{
		Actor:  ActivationActorUser,
		Reason: ActivationReasonOperator,
		Track:  BuiltinTrackPinned,
		Now:    fixedClock("2026-08-23T11:00:00Z"),
	})
	if err != nil {
		t.Fatalf("activate v2: %v", err)
	}
	if got := drv.Metadata[MetadataKeyActivationPreviousVersion]; got != "v1" {
		t.Fatalf("previous version = %q, want v1", got)
	}
	if got := drv.Metadata[MetadataKeyActivationReason]; got != "operator" {
		t.Fatalf("reason = %q, want operator", got)
	}
	if got := drv.Metadata[MetadataKeyBuiltinTrack]; got != "pinned" {
		t.Fatalf("track = %q, want pinned", got)
	}
}

func TestActivateDriverVersionWithOptionsEmptyTrackOmitsKey(t *testing.T) {
	ctx := context.Background()
	st := newActivationStore(t)
	drv, _, err := ActivateDriverVersionWithOptions(ctx, st, "TEST", "epic-runner", "v1", ActivationOptions{
		Actor:  ActivationActorUser,
		Reason: ActivationReasonRegistration,
	})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, ok := drv.Metadata[MetadataKeyBuiltinTrack]; ok {
		t.Fatalf("builtin_track must be omitted when Track is empty, got %q", drv.Metadata[MetadataKeyBuiltinTrack])
	}
	if got := drv.Metadata[MetadataKeyActivationReason]; got != "registration" {
		t.Fatalf("reason = %q, want registration", got)
	}
}

func TestActivateDriverVersionWithOptionsZeroNormalizes(t *testing.T) {
	ctx := context.Background()
	st := newActivationStore(t)
	drv, _, err := ActivateDriverVersionWithOptions(ctx, st, "TEST", "epic-runner", "v1", ActivationOptions{})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if got := drv.Metadata[MetadataKeyActivationActor]; got != "user" {
		t.Fatalf("actor = %q, want user", got)
	}
	if got := drv.Metadata[MetadataKeyActivationReason]; got != "operator" {
		t.Fatalf("reason = %q, want operator", got)
	}
}

func TestActivateDriverVersionWrapperRecordsOperatorPinned(t *testing.T) {
	ctx := context.Background()
	st := newActivationStore(t)
	drv, _, err := ActivateDriverVersion(ctx, st, "TEST", "epic-runner", "v1")
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if got := drv.Metadata[MetadataKeyActivationActor]; got != "user" {
		t.Fatalf("actor = %q, want user", got)
	}
	if got := drv.Metadata[MetadataKeyActivationReason]; got != "operator" {
		t.Fatalf("reason = %q, want operator", got)
	}
	if got := drv.Metadata[MetadataKeyBuiltinTrack]; got != "pinned" {
		t.Fatalf("track = %q, want pinned", got)
	}
}

func TestActivationPreservesApprovals(t *testing.T) {
	ctx := context.Background()
	st := newActivationStore(t)
	// Approve v1, then activate v2: the approval must survive the metadata
	// rebuild (activationMetadata copies approved_version:* keys).
	if _, _, err := ApproveDriverVersion(ctx, st, "TEST", "epic-runner", "v1"); err != nil {
		t.Fatalf("approve v1: %v", err)
	}
	drv, _, err := ActivateDriverVersionWithOptions(ctx, st, "TEST", "epic-runner", "v2", ActivationOptions{Track: BuiltinTrackPinned})
	if err != nil {
		t.Fatalf("activate v2: %v", err)
	}
	v1, err := st.DriverVersions().Get(ctx, "TEST", "v1")
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if !DriverVersionApproved(drv, v1) {
		t.Fatalf("activation dropped v1 approval")
	}
}

func TestRollbackDriverVersionExplicitTarget(t *testing.T) {
	ctx := context.Background()
	st := newActivationStore(t)
	if _, _, err := ActivateDriverVersionWithOptions(ctx, st, "TEST", "epic-runner", "v2", ActivationOptions{Track: BuiltinTrackAuto}); err != nil {
		t.Fatalf("activate v2: %v", err)
	}
	drv, ver, err := RollbackDriverVersion(ctx, st, "TEST", "epic-runner", "v1")
	if err != nil {
		t.Fatalf("rollback to v1: %v", err)
	}
	if drv.ActiveVersionID != "v1" || ver.VersionID != "v1" {
		t.Fatalf("active = %q, want v1", drv.ActiveVersionID)
	}
	if got := drv.Metadata[MetadataKeyActivationReason]; got != "rollback" {
		t.Fatalf("reason = %q, want rollback", got)
	}
	if got := drv.Metadata[MetadataKeyBuiltinTrack]; got != "pinned" {
		t.Fatalf("track = %q, want pinned", got)
	}
}

func TestRollbackDriverVersionUsesRecordedPrevious(t *testing.T) {
	ctx := context.Background()
	st := newActivationStore(t)
	// Activate v1 then v2: activation_previous_version_id becomes v1.
	if _, _, err := ActivateDriverVersionWithOptions(ctx, st, "TEST", "epic-runner", "v1", ActivationOptions{Track: BuiltinTrackAuto}); err != nil {
		t.Fatalf("activate v1: %v", err)
	}
	if _, _, err := ActivateDriverVersionWithOptions(ctx, st, "TEST", "epic-runner", "v2", ActivationOptions{Track: BuiltinTrackAuto}); err != nil {
		t.Fatalf("activate v2: %v", err)
	}
	drv, _, err := RollbackDriverVersion(ctx, st, "TEST", "epic-runner", "")
	if err != nil {
		t.Fatalf("rollback (no target): %v", err)
	}
	if drv.ActiveVersionID != "v1" {
		t.Fatalf("active = %q, want v1 (recorded previous)", drv.ActiveVersionID)
	}
}

func TestRollbackDriverVersionMissingRecord(t *testing.T) {
	ctx := context.Background()
	st := newActivationStore(t)
	if _, _, err := ActivateDriverVersionWithOptions(ctx, st, "TEST", "epic-runner", "v1", ActivationOptions{Track: BuiltinTrackAuto}); err != nil {
		t.Fatalf("activate v1: %v", err)
	}
	// No previous recorded (first activation), and no explicit target.
	_, _, err := RollbackDriverVersion(ctx, st, "TEST", "epic-runner", "")
	if err == nil || !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("rollback with no record: err = %v, want ErrInvalid", err)
	}
	if got := err.Error(); !strings.Contains(got, "rollback_target_missing") {
		t.Fatalf("error %q must mention rollback_target_missing", got)
	}
}

func TestRollbackDriverVersionTargetEqualsActive(t *testing.T) {
	ctx := context.Background()
	st := newActivationStore(t)
	if _, _, err := ActivateDriverVersionWithOptions(ctx, st, "TEST", "epic-runner", "v1", ActivationOptions{Track: BuiltinTrackAuto}); err != nil {
		t.Fatalf("activate v1: %v", err)
	}
	_, _, err := RollbackDriverVersion(ctx, st, "TEST", "epic-runner", "v1")
	if err == nil || !strings.Contains(err.Error(), "rollback_target_missing") {
		t.Fatalf("rollback to active must fail rollback_target_missing, got %v", err)
	}
}

func TestRollbackDriverVersionNonPassedRejected(t *testing.T) {
	ctx := context.Background()
	st := newActivationStore(t)
	// Add a failed version; rolling back to it must be rejected by
	// loadDriverVersionForOperatorAction.
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "TEST", VersionID: "v-failed", DriverID: "epic-runner", Version: 3,
		SourceDigest: "sha256:f", BundleDigest: "sha256:bf", ValidationStatus: domain.DriverVersionValidationFailed,
	}); err != nil {
		t.Fatalf("create failed version: %v", err)
	}
	if _, _, err := ActivateDriverVersionWithOptions(ctx, st, "TEST", "epic-runner", "v1", ActivationOptions{Track: BuiltinTrackAuto}); err != nil {
		t.Fatalf("activate v1: %v", err)
	}
	_, _, err := RollbackDriverVersion(ctx, st, "TEST", "epic-runner", "v-failed")
	if err == nil || !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("rollback to failed version: err = %v, want ErrInvalid", err)
	}
}

func TestResolveBuiltinTrack(t *testing.T) {
	systemBuiltin := &domain.DriverVersion{CreatedBy: "system", SourceRef: "builtin://workflows/epic-runner/versions/x"}
	apiCustom := &domain.DriverVersion{CreatedBy: "api", SourceRef: "api://workflows/epic-runner/versions/x"}
	cases := []struct {
		name   string
		driver *domain.Driver
		active *domain.DriverVersion
		want   BuiltinTrack
	}{
		{"explicit auto wins", &domain.Driver{Metadata: map[string]string{MetadataKeyBuiltinTrack: "auto"}}, apiCustom, BuiltinTrackAuto},
		{"explicit pinned wins", &domain.Driver{Metadata: map[string]string{MetadataKeyBuiltinTrack: "pinned"}}, systemBuiltin, BuiltinTrackPinned},
		{"legacy system+builtin is auto", &domain.Driver{Metadata: map[string]string{}}, systemBuiltin, BuiltinTrackAuto},
		{"legacy api+custom is pinned", &domain.Driver{Metadata: map[string]string{}}, apiCustom, BuiltinTrackPinned},
		{"nil active is auto", &domain.Driver{Metadata: map[string]string{}}, nil, BuiltinTrackAuto},
		{"nil driver + nil active is auto", nil, nil, BuiltinTrackAuto},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveBuiltinTrack(tc.driver, tc.active); got != tc.want {
				t.Fatalf("ResolveBuiltinTrack = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuiltinSyncDecision(t *testing.T) {
	cases := []struct {
		name            string
		track           BuiltinTrack
		active          string
		packaged        string
		wantActivate    bool
		wantUpdateAvail bool
	}{
		{"no active activates", BuiltinTrackAuto, "", "vA", true, false},
		{"no active activates on pinned too", BuiltinTrackPinned, "", "vA", true, false},
		{"equal is no-op", BuiltinTrackAuto, "vA", "vA", false, false},
		{"auto different activates", BuiltinTrackAuto, "vA", "vB", true, false},
		{"pinned different is update available", BuiltinTrackPinned, "vA", "vB", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			activate, updateAvail := BuiltinSyncDecision(tc.track, tc.active, tc.packaged)
			if activate != tc.wantActivate || updateAvail != tc.wantUpdateAvail {
				t.Fatalf("BuiltinSyncDecision(%q,%q,%q) = (%v,%v), want (%v,%v)",
					tc.track, tc.active, tc.packaged, activate, updateAvail, tc.wantActivate, tc.wantUpdateAvail)
			}
		})
	}
}
