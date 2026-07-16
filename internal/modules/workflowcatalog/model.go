package workflowcatalog

import (
	"strings"
	"time"
)

// DriverOwnerType identifies the principal that owns a workflow driver.
type DriverOwnerType string

const (
	DriverOwnerUser      DriverOwnerType = "user"
	DriverOwnerTeam      DriverOwnerType = "team"
	DriverOwnerLeadAgent DriverOwnerType = "lead_agent"
	DriverOwnerSystem    DriverOwnerType = "system"
)

// DriverStatus is the lifecycle status of a workflow driver.
type DriverStatus string

const (
	DriverStatusDraft    DriverStatus = "draft"
	DriverStatusActive   DriverStatus = "active"
	DriverStatusDisabled DriverStatus = "disabled"
	DriverStatusArchived DriverStatus = "archived"
)

// DriverTrustLevel classifies whether a workflow version may execute in a
// trusted host process. An unknown level never grants trust on its own.
type DriverTrustLevel string

const (
	DriverTrustTrusted   DriverTrustLevel = "trusted"
	DriverTrustUntrusted DriverTrustLevel = "untrusted"
)

// Trusted reports whether the level grants trusted-host execution.
func (t DriverTrustLevel) Trusted() bool { return t == DriverTrustTrusted }

// Driver is the Workflow Catalog aggregate root. Revision is the durable CAS
// value used by every version-lifecycle command.
type Driver struct {
	WorkspaceKey    string            `json:"workspace_key"`
	DriverID        string            `json:"driver_id"`
	Name            string            `json:"name"`
	OwnerType       DriverOwnerType   `json:"owner_type"`
	OwnerRef        string            `json:"owner_ref,omitempty"`
	Description     string            `json:"description,omitempty"`
	ActiveVersionID string            `json:"active_version_id,omitempty"`
	Status          DriverStatus      `json:"status"`
	TrustLevel      DriverTrustLevel  `json:"trust_level,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	Revision        uint64            `json:"revision"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// DriverVersionValidationStatus is the durable validation result for a
// workflow version.
type DriverVersionValidationStatus string

const (
	DriverVersionValidationPending DriverVersionValidationStatus = "pending"
	DriverVersionValidationPassed  DriverVersionValidationStatus = "passed"
	DriverVersionValidationFailed  DriverVersionValidationStatus = "failed"
)

// DriverVersion is an immutable built workflow version owned by the catalog.
type DriverVersion struct {
	WorkspaceKey     string                        `json:"workspace_key"`
	VersionID        string                        `json:"version_id"`
	DriverID         string                        `json:"driver_id"`
	Version          int                           `json:"version"`
	SourceRef        string                        `json:"source_ref"`
	SourceDigest     string                        `json:"source_digest"`
	BundleRef        string                        `json:"bundle_ref"`
	BundleDigest     string                        `json:"bundle_digest"`
	Runtime          string                        `json:"runtime,omitempty"`
	Manifest         map[string]string             `json:"manifest,omitempty"`
	BuildDiagnostics string                        `json:"build_diagnostics,omitempty"`
	ValidationStatus DriverVersionValidationStatus `json:"validation_status"`
	CreatedBy        string                        `json:"created_by,omitempty"`
	CreatedAt        time.Time                     `json:"created_at"`
}

const (
	// ApprovedVersionMetadataPrefix retains the existing durable approval key
	// format so old and new Loom versions interpret the same records.
	ApprovedVersionMetadataPrefix = "approved_version:"
	// ManifestTrustLevelKey is the version-manifest trust declaration.
	ManifestTrustLevelKey = "trust_level"
)

// ApprovedVersionMetadataKey returns the durable metadata key for a version's
// explicit operator approval.
func ApprovedVersionMetadataKey(versionID string) string {
	return ApprovedVersionMetadataPrefix + versionID
}

// VersionApproved preserves the legacy approval semantics: a present empty
// value, the exact source digest, or the legacy "trusted" marker approves the
// version. Any other value fails closed.
func VersionApproved(driver *Driver, version *DriverVersion) bool {
	if driver == nil || version == nil {
		return false
	}
	value, ok := driver.Metadata[ApprovedVersionMetadataKey(version.VersionID)]
	if !ok {
		return false
	}
	value = strings.TrimSpace(value)
	return value == "" || value == version.SourceDigest || value == string(DriverTrustTrusted)
}

// EffectiveTrust resolves trust for one exact driver version. Explicit
// approval wins, followed by the version manifest and then the legacy driver
// row fallback. Unknown values are untrusted.
func EffectiveTrust(driver *Driver, version *DriverVersion) DriverTrustLevel {
	if VersionApproved(driver, version) {
		return DriverTrustTrusted
	}
	if version != nil {
		switch DriverTrustLevel(strings.TrimSpace(version.Manifest[ManifestTrustLevelKey])) {
		case DriverTrustTrusted:
			return DriverTrustTrusted
		case DriverTrustUntrusted:
			return DriverTrustUntrusted
		}
	}
	if driver != nil && driver.TrustLevel.Trusted() {
		return DriverTrustTrusted
	}
	return DriverTrustUntrusted
}

func cloneDriver(in *Driver) *Driver {
	if in == nil {
		return nil
	}
	out := *in
	out.Metadata = cloneStringMap(in.Metadata)
	return &out
}

func cloneVersion(in *DriverVersion) *DriverVersion {
	if in == nil {
		return nil
	}
	out := *in
	out.Manifest = cloneStringMap(in.Manifest)
	return &out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
