package driver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const ApprovedVersionMetadataPrefix = "approved_version:"

func ApprovedVersionMetadataKey(versionID string) string {
	return ApprovedVersionMetadataPrefix + strings.TrimSpace(versionID)
}

func DriverVersionApproved(driver *domain.Driver, version *domain.DriverVersion) bool {
	if driver == nil || version == nil {
		return false
	}
	value, ok := driver.Metadata[ApprovedVersionMetadataKey(version.VersionID)]
	if !ok {
		return false
	}
	value = strings.TrimSpace(value)
	return value == "" || value == version.SourceDigest || value == string(domain.DriverTrustTrusted)
}

func DriverVersionEffectiveTrust(driver *domain.Driver, version *domain.DriverVersion) domain.DriverTrustLevel {
	if DriverVersionApproved(driver, version) {
		return domain.DriverTrustTrusted
	}
	level := domain.DriverTrustLevel(strings.TrimSpace(version.Manifest[ManifestTrustLevelKey]))
	switch level {
	case domain.DriverTrustTrusted:
		return domain.DriverTrustTrusted
	case domain.DriverTrustUntrusted:
		return domain.DriverTrustUntrusted
	}
	if driver != nil && driver.TrustLevel.Trusted() {
		return domain.DriverTrustTrusted
	}
	return domain.DriverTrustUntrusted
}

// driverTrustLevel resolves trust for the exact driver version pinned on the
// run. New manifests are authoritative: trusted built-ins/operator bundles
// stamp trusted, untrusted custom submissions stamp untrusted, and explicit
// operator approval is scoped to one version id in driver metadata. It lives
// here (not in internal/driver/sandbox) because it depends on
// DriverVersionEffectiveTrust; the sandbox package owns only the admission
// decision once a trust level is known.
func driverTrustLevel(ctx context.Context, drivers store.DriverStore, run *domain.DriverRun, version *domain.DriverVersion) (domain.DriverTrustLevel, error) {
	driver, err := drivers.Get(ctx, run.WorkspaceKey, run.DriverID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.DriverTrustUntrusted, nil
	}
	if err != nil {
		return "", fmt.Errorf("load driver for trust placement policy: %w", err)
	}
	return DriverVersionEffectiveTrust(driver, version), nil
}

func loadDriverVersionForOperatorAction(ctx context.Context, drivers store.DriverStore, versions store.DriverVersionStore, ws, driverID, versionID string) (*domain.Driver, *domain.DriverVersion, error) {
	if drivers == nil || versions == nil {
		return nil, nil, fmt.Errorf("driver and driver version stores required: %w", domain.ErrInvalid)
	}
	driverID = strings.TrimSpace(driverID)
	versionID = strings.TrimSpace(versionID)
	if ws == "" || driverID == "" || versionID == "" {
		return nil, nil, fmt.Errorf("workspace key, driver id, and version id required: %w", domain.ErrInvalid)
	}
	driver, err := drivers.Get(ctx, ws, driverID)
	if err != nil {
		return nil, nil, fmt.Errorf("get driver: %w", err)
	}
	version, err := versions.Get(ctx, ws, versionID)
	if err != nil {
		return nil, nil, fmt.Errorf("get driver version: %w", err)
	}
	if version.DriverID != driver.DriverID {
		return nil, nil, fmt.Errorf("driver version %q belongs to %q, not %q: %w", version.VersionID, version.DriverID, driver.DriverID, domain.ErrInvalid)
	}
	if version.ValidationStatus != domain.DriverVersionValidationPassed {
		return nil, nil, fmt.Errorf("driver version %q is not passed: %w", version.VersionID, domain.ErrInvalid)
	}
	return driver, version, nil
}

func activationMetadata(current map[string]string, manifest map[string]string) map[string]string {
	next := cloneStringMap(manifest)
	if next == nil {
		next = map[string]string{}
	}
	for key, value := range current {
		if strings.HasPrefix(key, ApprovedVersionMetadataPrefix) {
			next[key] = value
		}
	}
	return next
}
