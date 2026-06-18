package driver

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const ApprovedVersionMetadataPrefix = "approved_version:"

func ApprovedVersionMetadataKey(versionID string) string {
	return ApprovedVersionMetadataPrefix + strings.TrimSpace(versionID)
}

func ApproveDriverVersion(ctx context.Context, s store.Store, ws, driverID, versionID string) (*domain.Driver, *domain.DriverVersion, error) {
	driver, version, err := loadDriverVersionForOperatorAction(ctx, s, ws, driverID, versionID)
	if err != nil {
		return nil, nil, err
	}
	metadata := cloneStringMap(driver.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata[ApprovedVersionMetadataKey(version.VersionID)] = version.SourceDigest
	updated, err := s.Drivers().Update(ctx, ws, driver.DriverID, store.DriverUpdate{Metadata: &metadata})
	if err != nil {
		return nil, nil, fmt.Errorf("approve driver version: %w", err)
	}
	return updated, version, nil
}

func UnapproveDriverVersion(ctx context.Context, s store.Store, ws, driverID, versionID string) (*domain.Driver, *domain.DriverVersion, error) {
	driver, version, err := loadDriverVersionForOperatorAction(ctx, s, ws, driverID, versionID)
	if err != nil {
		return nil, nil, err
	}
	metadata := cloneStringMap(driver.Metadata)
	delete(metadata, ApprovedVersionMetadataKey(version.VersionID))
	updated, err := s.Drivers().Update(ctx, ws, driver.DriverID, store.DriverUpdate{Metadata: &metadata})
	if err != nil {
		return nil, nil, fmt.Errorf("unapprove driver version: %w", err)
	}
	return updated, version, nil
}

func ActivateDriverVersion(ctx context.Context, s store.Store, ws, driverID, versionID string) (*domain.Driver, *domain.DriverVersion, error) {
	driver, version, err := loadDriverVersionForOperatorAction(ctx, s, ws, driverID, versionID)
	if err != nil {
		return nil, nil, err
	}
	active := version.VersionID
	status := domain.DriverStatusActive
	metadata := activationMetadata(driver.Metadata, version.Manifest)
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

func loadDriverVersionForOperatorAction(ctx context.Context, s store.Store, ws, driverID, versionID string) (*domain.Driver, *domain.DriverVersion, error) {
	if s == nil {
		return nil, nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	driverID = strings.TrimSpace(driverID)
	versionID = strings.TrimSpace(versionID)
	if ws == "" || driverID == "" || versionID == "" {
		return nil, nil, fmt.Errorf("workspace key, driver id, and version id required: %w", domain.ErrInvalid)
	}
	driver, err := s.Drivers().Get(ctx, ws, driverID)
	if err != nil {
		return nil, nil, fmt.Errorf("get driver: %w", err)
	}
	version, err := s.DriverVersions().Get(ctx, ws, versionID)
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
