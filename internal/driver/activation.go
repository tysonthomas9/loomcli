package driver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Driver metadata keys written fresh on every activation. They live in driver
// metadata (not the version manifest) because they record who selected the
// active version and why — state about the activation, not the artifact.
// activationMetadata (approval.go) rebuilds driver metadata from the manifest +
// approved_version:* keys on every activation, so these keys are always set
// fresh here and never go stale.
const (
	MetadataKeyActivationActor           = "activation_actor"
	MetadataKeyActivationReason          = "activation_reason"
	MetadataKeyActivationAt              = "activation_at"
	MetadataKeyActivationPreviousVersion = "activation_previous_version_id"
	MetadataKeyBuiltinTrack              = "builtin_track"
)

// ActivationActor identifies whether a human or the system activated a version.
type ActivationActor string

const (
	ActivationActorUser   ActivationActor = "user"
	ActivationActorSystem ActivationActor = "system"
)

// ActivationReason records why a version became active.
type ActivationReason string

const (
	// ActivationReasonRegistration: activated as a side effect of
	// RegisterFlueDriver{Activate:true} (the operator/dev registration path).
	ActivationReasonRegistration ActivationReason = "registration"
	// ActivationReasonBuiltinSync: auto-activated by the built-in track policy
	// (SyncBuiltinWorkflow on the auto track).
	ActivationReasonBuiltinSync ActivationReason = "builtin_sync"
	// ActivationReasonOperator: an explicit operator `activate`.
	ActivationReasonOperator ActivationReason = "operator"
	// ActivationReasonRollback: an explicit `rollback` to a previous version.
	ActivationReasonRollback ActivationReason = "rollback"
)

// BuiltinTrack is the update policy for a built-in workflow driver. Only
// built-in-name drivers carry a track; the key is inert on custom-named
// drivers.
type BuiltinTrack string

const (
	// BuiltinTrackAuto follows this binary's packaged built-in artifact: an app
	// update auto-activates the new version and a downgrade re-activates the
	// older one.
	BuiltinTrackAuto BuiltinTrack = "auto"
	// BuiltinTrackPinned preserves the user's selection; app updates are
	// surfaced as update_available but never change the active version.
	BuiltinTrackPinned BuiltinTrack = "pinned"
)

// ActivationOptions describes an activation for the record it writes. The zero
// value is normalized to {user, operator, ""} — a bare operator activation with
// no track. Track is written only when non-empty.
type ActivationOptions struct {
	Actor  ActivationActor
	Reason ActivationReason
	Track  BuiltinTrack
	// Now overrides the activation timestamp source (tests inject a fixed
	// clock). Nil uses time.Now.
	Now func() time.Time
}

func (o ActivationOptions) normalized() ActivationOptions {
	if o.Actor == "" {
		o.Actor = ActivationActorUser
	}
	if o.Reason == "" {
		o.Reason = ActivationReasonOperator
	}
	return o
}

func (o ActivationOptions) at() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// registrationActivation normalizes the Activation carried on a
// RegisterFlueOptions. A registration that activates as a side effect defaults
// to {user, registration} rather than the {user, operator} default a bare
// ActivateDriverVersionWithOptions call would apply.
func registrationActivation(o ActivationOptions) ActivationOptions {
	if o.Actor == "" {
		o.Actor = ActivationActorUser
	}
	if o.Reason == "" {
		o.Reason = ActivationReasonRegistration
	}
	return o
}

// ActivateDriverVersionWithOptions activates versionID on its driver and writes
// an activation record (actor, reason, timestamp, previous active version, and
// — when opts.Track is set — the built-in track) into driver metadata. The
// record is rebuilt fresh on every activation, so it never goes stale and
// approvals (approved_version:* keys) are preserved by activationMetadata.
func ActivateDriverVersionWithOptions(ctx context.Context, s store.Store, ws, driverID, versionID string, opts ActivationOptions) (*domain.Driver, *domain.DriverVersion, error) {
	driver, version, err := loadDriverVersionForOperatorAction(ctx, s, ws, driverID, versionID)
	if err != nil {
		return nil, nil, err
	}
	opts = opts.normalized()
	previous := strings.TrimSpace(driver.ActiveVersionID)
	active := version.VersionID
	status := domain.DriverStatusActive
	metadata := activationMetadata(driver.Metadata, version.Manifest)
	metadata[MetadataKeyActivationActor] = string(opts.Actor)
	metadata[MetadataKeyActivationReason] = string(opts.Reason)
	metadata[MetadataKeyActivationAt] = opts.at().UTC().Format(time.RFC3339)
	metadata[MetadataKeyActivationPreviousVersion] = previous
	if opts.Track != "" {
		metadata[MetadataKeyBuiltinTrack] = string(opts.Track)
	}
	updated, err := s.Drivers().Update(ctx, ws, driver.DriverID, store.DriverUpdate{
		ActiveVersionID: &active,
		Status:          &status,
		Metadata:        &metadata,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("activate driver version: %w", err)
	}
	return updated, version, nil
}

// RollbackDriverVersion re-activates a previous version. An empty
// targetVersionID resolves to the driver's recorded
// activation_previous_version_id. A target that is empty or equal to the
// current active version fails rollback_target_missing. The activation itself
// runs through loadDriverVersionForOperatorAction, so a non-passed or foreign
// target is rejected exactly like activate.
func RollbackDriverVersion(ctx context.Context, s store.Store, ws, driverID, targetVersionID string) (*domain.Driver, *domain.DriverVersion, error) {
	if s == nil {
		return nil, nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	driverID = strings.TrimSpace(driverID)
	if ws == "" || driverID == "" {
		return nil, nil, fmt.Errorf("workspace key and driver id required: %w", domain.ErrInvalid)
	}
	driver, err := s.Drivers().Get(ctx, ws, driverID)
	if err != nil {
		return nil, nil, fmt.Errorf("get driver: %w", err)
	}
	active := strings.TrimSpace(driver.ActiveVersionID)
	target := strings.TrimSpace(targetVersionID)
	if target == "" {
		target = strings.TrimSpace(driver.Metadata[MetadataKeyActivationPreviousVersion])
	}
	if target == "" || target == active {
		return nil, nil, fmt.Errorf("rollback_target_missing: no previous active version recorded for driver %q: %w", driverID, domain.ErrInvalid)
	}
	return ActivateDriverVersionWithOptions(ctx, s, ws, driverID, target, ActivationOptions{
		Actor:  ActivationActorUser,
		Reason: ActivationReasonRollback,
		Track:  BuiltinTrackPinned,
	})
}

// ResolveBuiltinTrack resolves the built-in track for a driver. An explicit
// builtin_track metadata key wins; otherwise (legacy drivers with no activation
// record) a system-created built-in:// active version means auto and anything
// else means pinned. A driver with no active version is auto (a fresh sync
// should activate the packaged version).
func ResolveBuiltinTrack(driver *domain.Driver, active *domain.DriverVersion) BuiltinTrack {
	if driver != nil {
		switch BuiltinTrack(strings.TrimSpace(driver.Metadata[MetadataKeyBuiltinTrack])) {
		case BuiltinTrackAuto:
			return BuiltinTrackAuto
		case BuiltinTrackPinned:
			return BuiltinTrackPinned
		}
	}
	if active == nil {
		return BuiltinTrackAuto
	}
	if active.CreatedBy == "system" && strings.HasPrefix(active.SourceRef, "builtin://") {
		return BuiltinTrackAuto
	}
	return BuiltinTrackPinned
}

// BuiltinSyncDecision is the pure track policy: given the resolved track and
// the current active vs. this binary's packaged version id, it decides whether
// to activate the packaged version and whether an update is merely available
// (pinned track, a different version shipped).
func BuiltinSyncDecision(track BuiltinTrack, activeID, packagedID string) (activate, updateAvailable bool) {
	activeID = strings.TrimSpace(activeID)
	packagedID = strings.TrimSpace(packagedID)
	switch {
	case activeID == "":
		return true, false
	case activeID == packagedID:
		return false, false
	case track == BuiltinTrackAuto:
		return true, false
	default:
		return false, true
	}
}
