package workflowcatalog

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

var _ VersionAuthoringAPI = (*Service)(nil)

// AuthorVersion creates or reuses an operator-authored immutable version.
// Operator submissions are always untrusted and inactive.
func (s *Service) AuthorVersion(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command AuthorVersionCommand,
) (*AuthorVersionResult, error) {
	normalized, err := normalizeAuthorVersionCommand(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.admission.RequireOperator(ActionAuthorVersion, normalized.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	return s.author(ctx, ActionAuthorVersion, normalized, auth.Subject(), false, false)
}

// AuthorManagedVersion creates or reuses a trusted Loom-managed builtin and
// may activate it atomically. Only a SystemAuthority for the exact action and
// workspace can enter this lane.
func (s *Service) AuthorManagedVersion(
	ctx context.Context,
	auth authority.SystemAuthority,
	command AuthorManagedVersionCommand,
) (*AuthorVersionResult, error) {
	normalized, err := normalizeAuthorVersionCommand(command.AuthorVersionCommand)
	if err != nil {
		return nil, err
	}
	if s == nil || s.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.admission.RequireSystem(ActionAuthorManagedVersion, normalized.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if err := validateManagedBuiltinIntent(normalized); err != nil {
		return nil, err
	}
	return s.author(ctx, ActionAuthorManagedVersion, normalized, auth.Subject(), true, command.Activate)
}

func (s *Service) author(
	ctx context.Context,
	action authority.Action,
	command AuthorVersionCommand,
	auditActor string,
	managed, activate bool,
) (*AuthorVersionResult, error) {
	if s.authoring == nil {
		return nil, ErrUnavailable
	}
	result, err := s.authoring.AuthorVersion(ctx, AuthoringMutation{
		AuthorVersionCommand: command,
		AuditActor:           auditActor,
		Managed:              managed,
		Activate:             activate,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	return validateAuthoringResult(action, command, auditActor, managed, activate, result)
}

//nolint:funlen // Normalization validates every immutable source, trust, digest, and lifecycle coordinate together.
func normalizeAuthorVersionCommand(command AuthorVersionCommand) (AuthorVersionCommand, error) {
	var err error
	command.WorkspaceKey, err = normalizeRequired("workspace", command.WorkspaceKey)
	if err != nil {
		return AuthorVersionCommand{}, err
	}
	command.RequestID, err = requireCanonical("request id", command.RequestID)
	if err != nil {
		return AuthorVersionCommand{}, err
	}
	command.DriverID, err = requireCanonicalDriverID(command.DriverID)
	if err != nil {
		return AuthorVersionCommand{}, err
	}
	command.DriverName, err = requireCanonical("driver name", command.DriverName)
	if err != nil {
		return AuthorVersionCommand{}, err
	}
	command.VersionID, err = requireCanonical("version id", command.VersionID)
	if err != nil {
		return AuthorVersionCommand{}, err
	}
	command.SourceRef, err = requireCanonical("source ref", command.SourceRef)
	if err != nil {
		return AuthorVersionCommand{}, err
	}
	command.SourceDigest, err = requireSHA256Digest("source digest", command.SourceDigest)
	if err != nil {
		return AuthorVersionCommand{}, err
	}
	command.BundleRef, err = requireCanonicalRelativeRef("bundle ref", command.BundleRef)
	if err != nil {
		return AuthorVersionCommand{}, err
	}
	command.BundleDigest, err = requireSHA256Digest("bundle digest", command.BundleDigest)
	if err != nil {
		return AuthorVersionCommand{}, err
	}
	command.Runtime, err = requireCanonical("runtime", command.Runtime)
	if err != nil {
		return AuthorVersionCommand{}, err
	}
	if command.ExpectedRevision > MaxExpectedRevision {
		return AuthorVersionCommand{}, fmt.Errorf("expected revision cannot advance within FleetDB's signed persistence range: %w", ErrInvalid)
	}
	command.Manifest = cloneStringMap(command.Manifest)
	for key, value := range command.Manifest {
		if strings.TrimSpace(key) == "" || key != strings.TrimSpace(key) {
			return AuthorVersionCommand{}, fmt.Errorf("manifest keys must be non-empty and canonical: %w", ErrInvalid)
		}
		if strings.HasPrefix(key, ApprovedVersionMetadataPrefix) {
			return AuthorVersionCommand{}, fmt.Errorf("manifest key %q is reserved for catalog lifecycle authority: %w", key, ErrInvalid)
		}
		command.Manifest[key] = value
	}
	// The service owns trust stamping. Reject rather than silently overwrite a
	// caller-supplied trust declaration at this boundary.
	if _, present := command.Manifest[ManifestTrustLevelKey]; present {
		return AuthorVersionCommand{}, fmt.Errorf("manifest trust_level is server-owned: %w", ErrInvalid)
	}
	return command, nil
}

func validateManagedBuiltinIntent(command AuthorVersionCommand) error {
	if !IsBuiltinWorkflowName(command.DriverID) || command.DriverName != command.DriverID {
		return fmt.Errorf("managed authoring requires a canonical builtin identity: %w", ErrInvalid)
	}
	if command.SourceRef != BuiltinSourceRef(command.DriverID, command.SourceDigest) {
		return fmt.Errorf("managed authoring requires exact builtin source provenance: %w", ErrInvalid)
	}
	if command.VersionID != BuiltinVersionID(command.DriverID, command.BundleDigest) {
		return fmt.Errorf("managed authoring requires a content-derived version id: %w", ErrInvalid)
	}
	if command.BundleRef != BuiltinBundleRef(command.DriverID, command.VersionID) {
		return fmt.Errorf("managed authoring requires the canonical builtin bundle reference: %w", ErrInvalid)
	}
	for key, want := range map[string]string{
		"driver_id":     command.DriverID,
		"driver_name":   command.DriverName,
		"workflow_name": command.DriverID,
		"source_ref":    command.SourceRef,
		"source_digest": command.SourceDigest,
		"runtime":       command.Runtime,
		"provenance":    ManagedBuiltinProvenance,
	} {
		if command.Manifest[key] != want {
			return fmt.Errorf("managed authoring manifest %s must be %q: %w", key, want, ErrInvalid)
		}
	}
	return nil
}

func requireSHA256Digest(label, value string) (string, error) {
	value, err := requireCanonical(label, value)
	if err != nil {
		return "", err
	}
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return "", fmt.Errorf("%s must be sha256:<64 lowercase hex>: %w", label, ErrInvalid)
	}
	for _, char := range value[len("sha256:"):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return "", fmt.Errorf("%s must be sha256:<64 lowercase hex>: %w", label, ErrInvalid)
		}
	}
	return value, nil
}

func requireCanonicalRelativeRef(label, value string) (string, error) {
	value, err := requireCanonical(label, value)
	if err != nil {
		return "", err
	}
	if path.IsAbs(value) {
		return "", fmt.Errorf("%s must be relative: %w", label, ErrInvalid)
	}
	if strings.Contains(value, `\`) {
		return "", fmt.Errorf("%s must use canonical slash separators: %w", label, ErrInvalid)
	}
	clean := path.Clean(value)
	if clean == "." || clean != value || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%s must be a canonical contained relative path: %w", label, ErrInvalid)
	}
	return value, nil
}

//nolint:cyclop,funlen // Exact-result validation checks the full authored version identity, trust, source, and lifecycle contract.
func validateAuthoringResult(
	action authority.Action,
	command AuthorVersionCommand,
	auditActor string,
	managed, activate bool,
	result *AuthoringResult,
) (*AuthorVersionResult, error) {
	if result == nil {
		return nil, ErrInvalidPersistedState
	}
	if err := validateDriver(result.Driver, command.WorkspaceKey, command.DriverID, false); err != nil {
		return nil, err
	}
	if err := validateVersion(result.Version, command.WorkspaceKey, command.DriverID, command.VersionID); err != nil {
		return nil, err
	}
	if result.Driver.Name != command.DriverName || result.Version.Version < 1 {
		return nil, ErrInvalidPersistedState
	}
	if result.CommittedRevision == 0 ||
		result.CommittedRevision != command.ExpectedRevision+1 ||
		result.Driver.Revision < result.CommittedRevision ||
		result.SemanticImpact != SemanticImpactVersionAuthored {
		return nil, ErrInvalidPersistedState
	}
	version := result.Version
	if version.SourceRef != command.SourceRef ||
		version.SourceDigest != command.SourceDigest ||
		version.BundleRef != command.BundleRef ||
		version.BundleDigest != command.BundleDigest ||
		version.Runtime != command.Runtime ||
		version.ValidationStatus != DriverVersionValidationPassed {
		return nil, ErrInvalidPersistedState
	}
	wantTrust := DriverTrustUntrusted
	if managed {
		wantTrust = DriverTrustTrusted
	}
	if DriverTrustLevel(version.Manifest[ManifestTrustLevelKey]) != wantTrust {
		return nil, ErrInvalidPersistedState
	}
	for key, value := range command.Manifest {
		if version.Manifest[key] != value {
			return nil, ErrInvalidPersistedState
		}
	}
	if result.CreatedVersion == result.ReusedVersion {
		return nil, ErrInvalidPersistedState
	}
	if result.CreatedVersion {
		if version.BuildDiagnostics != command.BuildDiagnostics || version.CreatedBy != auditActor {
			return nil, ErrInvalidPersistedState
		}
	} else if strings.TrimSpace(version.CreatedBy) == "" || version.CreatedBy != strings.TrimSpace(version.CreatedBy) {
		// Reuse preserves the original immutable audit actor and diagnostics;
		// it must not rewrite either field to the current caller.
		return nil, ErrInvalidPersistedState
	}
	if result.Activated != activate {
		return nil, ErrInvalidPersistedState
	}
	// Like lifecycle commands, FleetDB may return a post-commit aggregate read
	// that has already advanced beyond this command's commit. Enforce mutable
	// Driver postconditions only for the exact committed revision; immutable
	// version fields above are safe to validate at every later revision.
	if result.Driver.Revision == result.CommittedRevision {
		if activate && (result.Driver.ActiveVersionID != version.VersionID || result.Driver.Status != DriverStatusActive) {
			return nil, ErrInvalidPersistedState
		}
		if !managed && result.Driver.TrustLevel.Trusted() {
			return nil, ErrInvalidPersistedState
		}
	}
	return &AuthorVersionResult{
		Action:            action,
		Driver:            cloneDriver(result.Driver),
		Version:           cloneVersion(result.Version),
		CreatedDriver:     result.CreatedDriver,
		CreatedVersion:    result.CreatedVersion,
		ReusedVersion:     result.ReusedVersion,
		Activated:         result.Activated,
		Replayed:          result.Replayed,
		CommittedRevision: result.CommittedRevision,
		SemanticImpact:    result.SemanticImpact,
	}, nil
}
