package workflowcatalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// Service implements the public Workflow Catalog API over catalog-owned
// persistence ports and a default-deny operation registry.
type Service struct {
	reader       Reader
	lifecycle    VersionLifecycleStore
	authoring    AuthoringStore
	availability AvailabilityStore
	admission    *authority.Admission
}

var _ API = (*Service)(nil)

// New constructs a Workflow Catalog service. A nil lifecycle port is useful
// during the read-seam rollout; all commands fail closed as unavailable.
func New(reader Reader, lifecycle VersionLifecycleStore, admission *authority.Admission) *Service {
	return &Service{reader: reader, lifecycle: lifecycle, admission: admission}
}

// NewWithAuthoring constructs the complete catalog including the Phase 5
// atomic authoring port. New remains available for read/lifecycle-only
// profiles and makes authoring fail closed.
func NewWithAuthoring(reader Reader, lifecycle VersionLifecycleStore, authoring AuthoringStore, admission *authority.Admission) *Service {
	return &Service{reader: reader, lifecycle: lifecycle, authoring: authoring, admission: admission}
}

// NewComplete constructs the production Workflow Catalog command surface.
// Keeping availability explicit prevents composition from silently equating a
// validation-passed version with a distributable bundle.
func NewComplete(reader Reader, lifecycle VersionLifecycleStore, authoring AuthoringStore, availability AvailabilityStore, admission *authority.Admission) *Service {
	return &Service{
		reader: reader, lifecycle: lifecycle, authoring: authoring,
		availability: availability, admission: admission,
	}
}

// NewWithAvailability constructs the narrow availability command surface for
// focused compositions and tests. Production composition supplies all catalog
// ports together.
func NewWithAvailability(reader Reader, availability AvailabilityStore, admission *authority.Admission) *Service {
	return &Service{reader: reader, availability: availability, admission: admission}
}

// GetDriver resolves an exact driver ID first, then a driver name for legacy
// callers that have not yet adopted stable IDs.
func (s *Service) GetDriver(ctx context.Context, workspace, driverRef string) (*Driver, error) {
	workspace, driverRef, err := normalizeWorkspaceAndRef(workspace, driverRef)
	if err != nil {
		return nil, err
	}
	if s == nil || s.reader == nil {
		return nil, ErrUnavailable
	}

	driver, err := s.reader.GetDriver(ctx, workspace, driverRef)
	if err == nil {
		if err := validateDriver(driver, workspace, driverRef, false); err != nil {
			return nil, err
		}
		return cloneDriver(driver), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("get driver %q: %w", driverRef, err)
	}

	driver, err = s.reader.FindDriverByName(ctx, workspace, driverRef)
	if err != nil {
		return nil, fmt.Errorf("find driver named %q: %w", driverRef, err)
	}
	if err := validateDriver(driver, workspace, driverRef, true); err != nil {
		return nil, err
	}
	return cloneDriver(driver), nil
}

// ListDrivers returns defensive copies in the persistence-defined stable
// ordering.
func (s *Service) ListDrivers(ctx context.Context, workspace string) ([]*Driver, error) {
	workspace, err := normalizeRequired("workspace", workspace)
	if err != nil {
		return nil, err
	}
	if s == nil || s.reader == nil {
		return nil, ErrUnavailable
	}
	drivers, err := s.reader.ListDrivers(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list drivers: %w", err)
	}
	out := make([]*Driver, 0, len(drivers))
	for _, driver := range drivers {
		if err := validateDriver(driver, workspace, "", false); err != nil {
			return nil, err
		}
		out = append(out, cloneDriver(driver))
	}
	return out, nil
}

// GetVersion returns one workspace-scoped version by exact ID.
func (s *Service) GetVersion(ctx context.Context, workspace, versionID string) (*DriverVersion, error) {
	workspace, err := normalizeRequired("workspace", workspace)
	if err != nil {
		return nil, err
	}
	versionID, err = requireCanonical("version id", versionID)
	if err != nil {
		return nil, err
	}
	if s == nil || s.reader == nil {
		return nil, ErrUnavailable
	}
	version, err := s.reader.GetVersion(ctx, workspace, versionID)
	if err != nil {
		return nil, fmt.Errorf("get version %q: %w", versionID, err)
	}
	if err := validateVersion(version, workspace, "", versionID); err != nil {
		return nil, err
	}
	return cloneVersion(version), nil
}

// ListVersions resolves a driver ID/name and returns only versions owned by
// that driver in the requested workspace.
func (s *Service) ListVersions(ctx context.Context, workspace, driverRef string) (*VersionSet, error) {
	driver, err := s.GetDriver(ctx, workspace, driverRef)
	if err != nil {
		return nil, err
	}
	versions, err := s.reader.ListVersions(ctx, driver.WorkspaceKey, driver.DriverID)
	if err != nil {
		return nil, fmt.Errorf("list versions for driver %q: %w", driver.DriverID, err)
	}
	out := make([]*DriverVersion, 0, len(versions))
	for _, version := range versions {
		if err := validateVersion(version, driver.WorkspaceKey, driver.DriverID, ""); err != nil {
			return nil, err
		}
		out = append(out, cloneVersion(version))
	}
	return &VersionSet{Driver: driver, Versions: out}, nil
}

// ResolveEffectiveVersion is a pure, system-authorized query that selects only
// the driver's activated version. It never registers, approves, or activates
// state, and it intentionally accepts no requested version ID.
func (s *Service) ResolveEffectiveVersion(ctx context.Context, auth authority.SystemAuthority, workspace, driverRef string) (*EffectiveVersion, error) {
	workspace, driverRef, err := normalizeWorkspaceAndRef(workspace, driverRef)
	if err != nil {
		return nil, err
	}
	if s == nil || s.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.admission.RequireSystem(ActionResolveEffectiveVersion, workspace, auth); err != nil {
		return nil, err
	}

	driver, err := s.GetDriver(ctx, workspace, driverRef)
	if err != nil {
		return nil, err
	}
	versionID := strings.TrimSpace(driver.ActiveVersionID)
	if versionID == "" {
		return nil, fmt.Errorf("driver %q has no active version: %w", driver.DriverID, ErrInvalid)
	}
	if driver.ActiveVersionID != versionID {
		return nil, fmt.Errorf("driver %q has malformed active version id: %w", driver.DriverID, ErrInvalidPersistedState)
	}
	version, err := s.loadPassedVersion(ctx, driver, versionID)
	if err != nil {
		return nil, err
	}
	if version.VersionID != driver.ActiveVersionID {
		return nil, ErrInvalidPersistedState
	}
	approved := VersionApproved(driver, version)
	return &EffectiveVersion{
		Driver:         driver,
		Version:        version,
		Approved:       approved,
		EffectiveTrust: EffectiveTrust(driver, version),
	}, nil
}

// ResolveRequestedVersion is the explicit operator-preview counterpart to
// ResolveEffectiveVersion. It permits a passed, inactive version without
// changing the driver's activated version.
func (s *Service) ResolveRequestedVersion(ctx context.Context, auth authority.OperatorAuthority, workspace, driverRef, requestedVersionID string) (*RequestedVersion, error) {
	workspace, driverRef, err := normalizeWorkspaceAndRef(workspace, driverRef)
	if err != nil {
		return nil, err
	}
	requestedVersionID, err = requireCanonical("requested version id", requestedVersionID)
	if err != nil {
		return nil, err
	}
	if s == nil || s.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.admission.RequireOperator(ActionResolveRequestedVersion, workspace, auth); err != nil {
		return nil, err
	}

	driver, err := s.GetDriver(ctx, workspace, driverRef)
	if err != nil {
		return nil, err
	}
	version, err := s.loadPassedVersion(ctx, driver, requestedVersionID)
	if err != nil {
		return nil, err
	}
	approved := VersionApproved(driver, version)
	return &RequestedVersion{
		Driver:         driver,
		Version:        version,
		Active:         driver.ActiveVersionID == version.VersionID,
		Approved:       approved,
		EffectiveTrust: EffectiveTrust(driver, version),
	}, nil
}

func (s *Service) loadPassedVersion(ctx context.Context, driver *Driver, versionID string) (*DriverVersion, error) {
	version, err := s.GetVersion(ctx, driver.WorkspaceKey, versionID)
	if err != nil {
		return nil, err
	}
	if err := validateVersion(version, driver.WorkspaceKey, driver.DriverID, versionID); err != nil {
		return nil, err
	}
	if version.ValidationStatus != DriverVersionValidationPassed {
		return nil, fmt.Errorf("version %q validation is %q: %w", version.VersionID, version.ValidationStatus, ErrVersionNotValidated)
	}
	if !VersionAvailable(version) {
		return nil, fmt.Errorf("version %q availability is %q: %w", version.VersionID, version.AvailabilityStatus, ErrVersionNotAvailable)
	}
	return version, nil
}

func (s *Service) ApproveVersion(ctx context.Context, auth authority.OperatorAuthority, command VersionCommand) (*VersionResult, error) {
	return s.executeLifecycle(ctx, auth, ActionApproveVersion, command)
}

func (s *Service) UnapproveVersion(ctx context.Context, auth authority.OperatorAuthority, command VersionCommand) (*VersionResult, error) {
	return s.executeLifecycle(ctx, auth, ActionUnapproveVersion, command)
}

func (s *Service) ActivateVersion(ctx context.Context, auth authority.OperatorAuthority, command VersionCommand) (*VersionResult, error) {
	return s.executeLifecycle(ctx, auth, ActionActivateVersion, command)
}

func (s *Service) ApproveManagedVersion(ctx context.Context, auth authority.SystemAuthority, command VersionCommand) (*VersionResult, error) {
	return s.executeManagedLifecycle(ctx, auth, ActionApproveManagedVersion, command)
}

func (s *Service) ActivateManagedVersion(ctx context.Context, auth authority.SystemAuthority, command VersionCommand) (*VersionResult, error) {
	return s.executeManagedLifecycle(ctx, auth, ActionActivateManagedVersion, command)
}

func (s *Service) executeLifecycle(ctx context.Context, auth authority.OperatorAuthority, action authority.Action, command VersionCommand) (*VersionResult, error) {
	command, err := normalizeVersionCommand(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.admission.RequireOperator(action, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	return s.executeAdmittedLifecycle(ctx, action, command)
}

func (s *Service) executeManagedLifecycle(ctx context.Context, auth authority.SystemAuthority, action authority.Action, command VersionCommand) (*VersionResult, error) {
	command, err := normalizeVersionCommand(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.admission.RequireSystem(action, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	return s.executeAdmittedLifecycle(ctx, action, command)
}

func (s *Service) executeAdmittedLifecycle(ctx context.Context, action authority.Action, command VersionCommand) (*VersionResult, error) {
	if s.reader == nil || s.lifecycle == nil {
		return nil, ErrUnavailable
	}

	storeAction := lifecycleStoreAction(action)
	if storeAction == "" {
		return nil, authority.ErrAdmissionDenied
	}
	driver, version, err := s.loadCommandState(ctx, storeAction, command)
	if err != nil {
		return nil, err
	}

	mutation := LifecycleMutation(command)
	var persisted *LifecycleResult
	switch storeAction {
	case ActionApproveVersion:
		persisted, err = s.lifecycle.ApproveVersion(ctx, mutation)
	case ActionUnapproveVersion:
		persisted, err = s.lifecycle.UnapproveVersion(ctx, mutation)
	case ActionActivateVersion:
		persisted, err = s.lifecycle.ActivateVersion(ctx, mutation)
	default:
		return nil, authority.ErrAdmissionDenied
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	result, err := validateLifecycleResult(storeAction, command, driver, version, persisted)
	if err != nil {
		return nil, err
	}
	result.Action = action
	return result, nil
}

func lifecycleStoreAction(action authority.Action) authority.Action {
	switch action {
	case ActionApproveVersion, ActionUnapproveVersion, ActionActivateVersion:
		return action
	case ActionApproveManagedVersion:
		return ActionApproveVersion
	case ActionActivateManagedVersion:
		return ActionActivateVersion
	default:
		return ""
	}
}

func (s *Service) loadCommandState(ctx context.Context, action authority.Action, command VersionCommand) (*Driver, *DriverVersion, error) {
	driver, err := s.reader.GetDriver(ctx, command.WorkspaceKey, command.DriverID)
	if err != nil {
		return nil, nil, fmt.Errorf("get driver %q: %w", command.DriverID, err)
	}
	if err := validateDriver(driver, command.WorkspaceKey, command.DriverID, false); err != nil {
		return nil, nil, err
	}
	version, err := s.reader.GetVersion(ctx, command.WorkspaceKey, command.VersionID)
	if err != nil {
		return nil, nil, fmt.Errorf("get version %q: %w", command.VersionID, err)
	}
	if err := validateVersion(version, command.WorkspaceKey, command.DriverID, command.VersionID); err != nil {
		return nil, nil, err
	}
	if (action == ActionApproveVersion || action == ActionActivateVersion) && version.ValidationStatus != DriverVersionValidationPassed {
		return nil, nil, fmt.Errorf("version %q validation is %q: %w", version.VersionID, version.ValidationStatus, ErrVersionNotValidated)
	}
	if (action == ActionApproveVersion || action == ActionActivateVersion) && !VersionAvailable(version) {
		return nil, nil, fmt.Errorf("version %q availability is %q: %w", version.VersionID, version.AvailabilityStatus, ErrVersionNotAvailable)
	}
	return cloneDriver(driver), cloneVersion(version), nil
}

func validateLifecycleResult(action authority.Action, command VersionCommand, beforeDriver *Driver, beforeVersion *DriverVersion, persisted *LifecycleResult) (*VersionResult, error) {
	if persisted == nil {
		return nil, ErrInvalidPersistedState
	}
	if err := validateDriver(persisted.Driver, command.WorkspaceKey, command.DriverID, false); err != nil {
		return nil, err
	}
	if err := validateVersion(persisted.Version, command.WorkspaceKey, command.DriverID, command.VersionID); err != nil {
		return nil, err
	}
	if persisted.CommittedRevision == 0 ||
		persisted.CommittedRevision != command.ExpectedRevision+1 ||
		persisted.Driver.Revision < persisted.CommittedRevision {
		return nil, ErrInvalidPersistedState
	}
	driver := cloneDriver(persisted.Driver)
	version := cloneVersion(persisted.Version)
	if beforeVersion == nil || version.SourceDigest != beforeVersion.SourceDigest || version.BundleDigest != beforeVersion.BundleDigest {
		return nil, ErrInvalidPersistedState
	}
	if persisted.SemanticImpact != semanticImpactFor(action) {
		return nil, ErrInvalidPersistedState
	}
	approved := VersionApproved(driver, version)
	// FleetDB commits the mutation before reading the aggregate for its
	// response. A concurrent command can therefore advance Driver beyond this
	// command's CommittedRevision even on a first (non-replayed) response. Only
	// an equal revision is the exact committed state and can safely be checked
	// against action-specific postconditions; a later state has already passed
	// FleetDB's atomic command validation and is still ownership-checked above.
	if driver.Revision == persisted.CommittedRevision {
		if err := validateCommittedLifecycleState(action, beforeDriver, driver, version, approved); err != nil {
			return nil, ErrInvalidPersistedState
		}
	}
	return &VersionResult{
		Action:            action,
		Driver:            driver,
		Version:           version,
		Active:            driver.ActiveVersionID == version.VersionID,
		Approved:          approved,
		EffectiveTrust:    EffectiveTrust(driver, version),
		Replayed:          persisted.Replayed,
		CommittedRevision: persisted.CommittedRevision,
		SemanticImpact:    persisted.SemanticImpact,
	}, nil
}

func validateCommittedLifecycleState(action authority.Action, beforeDriver, driver *Driver, version *DriverVersion, approved bool) error {
	if !hasExpectedMetadata(action, beforeDriver, driver, version) {
		return ErrInvalidPersistedState
	}
	switch action {
	case ActionApproveVersion:
		if version.ValidationStatus != DriverVersionValidationPassed || !approved {
			return ErrInvalidPersistedState
		}
	case ActionUnapproveVersion:
		if approved || driver.ActiveVersionID != beforeDriver.ActiveVersionID {
			return ErrInvalidPersistedState
		}
	case ActionActivateVersion:
		if version.ValidationStatus != DriverVersionValidationPassed || !approved || driver.ActiveVersionID != version.VersionID || driver.Status != DriverStatusActive {
			return ErrInvalidPersistedState
		}
	default:
		return ErrInvalidPersistedState
	}
	return nil
}

func hasExpectedMetadata(action authority.Action, before, after *Driver, version *DriverVersion) bool {
	if before == nil || after == nil || version == nil {
		return false
	}
	expected := make(map[string]string, len(before.Metadata)+len(version.Manifest))
	for key, value := range before.Metadata {
		expected[key] = value
	}
	targetApprovalKey := ApprovedVersionMetadataKey(version.VersionID)
	switch action {
	case ActionApproveVersion:
		expected[targetApprovalKey] = version.SourceDigest
	case ActionUnapproveVersion:
		delete(expected, targetApprovalKey)
	case ActionActivateVersion:
		for key, value := range version.Manifest {
			if !strings.HasPrefix(key, ApprovedVersionMetadataPrefix) {
				expected[key] = value
			}
		}
	default:
		return false
	}
	if len(expected) != len(after.Metadata) {
		return false
	}
	for key, value := range expected {
		if current, ok := after.Metadata[key]; !ok || current != value {
			return false
		}
	}
	return true
}

func normalizeVersionCommand(command VersionCommand) (VersionCommand, error) {
	var err error
	command.WorkspaceKey, err = normalizeRequired("workspace", command.WorkspaceKey)
	if err != nil {
		return VersionCommand{}, err
	}
	command.DriverID, err = requireCanonicalDriverID(command.DriverID)
	if err != nil {
		return VersionCommand{}, err
	}
	command.VersionID, err = requireCanonical("version id", command.VersionID)
	if err != nil {
		return VersionCommand{}, err
	}
	if command.ExpectedRevision == 0 {
		return VersionCommand{}, fmt.Errorf("expected revision must be at least 1: %w", ErrInvalid)
	}
	if command.ExpectedRevision > MaxExpectedRevision {
		return VersionCommand{}, fmt.Errorf("expected revision cannot advance within FleetDB's signed persistence range: %w", ErrInvalid)
	}
	return command, nil
}

func normalizeWorkspaceAndRef(workspace, ref string) (string, string, error) {
	workspace, err := normalizeRequired("workspace", workspace)
	if err != nil {
		return "", "", err
	}
	ref, err = normalizeRequired("reference", ref)
	if err != nil {
		return "", "", err
	}
	return workspace, ref, nil
}

func normalizeRequired(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required: %w", label, ErrInvalid)
	}
	return value, nil
}

func requireCanonical(label, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required: %w", label, ErrInvalid)
	}
	if value != trimmed {
		return "", fmt.Errorf("%s must not contain leading or trailing whitespace: %w", label, ErrInvalid)
	}
	return value, nil
}

func requireCanonicalDriverID(value string) (string, error) {
	value, err := requireCanonical("driver id", value)
	if err != nil {
		return "", err
	}
	if strings.Contains(value, ":") {
		return "", fmt.Errorf("driver id must not contain the reserved ':' delimiter: %w", ErrInvalid)
	}
	return value, nil
}

func validateDriver(driver *Driver, workspace, ref string, byName bool) error {
	if driver == nil || strings.TrimSpace(driver.DriverID) == "" || strings.TrimSpace(driver.DriverID) != driver.DriverID ||
		strings.Contains(driver.DriverID, ":") ||
		strings.TrimSpace(driver.WorkspaceKey) == "" || strings.TrimSpace(driver.WorkspaceKey) != driver.WorkspaceKey {
		return ErrInvalidPersistedState
	}
	if driver.WorkspaceKey != workspace {
		return fmt.Errorf("driver %q belongs to workspace %q, not %q: %w", driver.DriverID, driver.WorkspaceKey, workspace, ErrWrongWorkspace)
	}
	if ref != "" {
		if byName && driver.Name != ref {
			return ErrInvalidPersistedState
		}
		if !byName && driver.DriverID != ref {
			return ErrInvalidPersistedState
		}
	}
	return nil
}

func validateVersion(version *DriverVersion, workspace, driverID, versionID string) error {
	if version == nil || strings.TrimSpace(version.VersionID) == "" || strings.TrimSpace(version.VersionID) != version.VersionID ||
		strings.TrimSpace(version.DriverID) == "" || strings.TrimSpace(version.DriverID) != version.DriverID ||
		strings.Contains(version.DriverID, ":") ||
		strings.TrimSpace(version.WorkspaceKey) == "" || strings.TrimSpace(version.WorkspaceKey) != version.WorkspaceKey {
		return ErrInvalidPersistedState
	}
	if version.WorkspaceKey != workspace {
		return fmt.Errorf("version %q belongs to workspace %q, not %q: %w", version.VersionID, version.WorkspaceKey, workspace, ErrWrongWorkspace)
	}
	if driverID != "" && version.DriverID != driverID {
		return fmt.Errorf("version %q belongs to driver %q, not %q: %w", version.VersionID, version.DriverID, driverID, ErrVersionOwnership)
	}
	if versionID != "" && version.VersionID != versionID {
		return ErrInvalidPersistedState
	}
	return nil
}

func semanticImpactFor(action authority.Action) string {
	switch action {
	case ActionApproveVersion, ActionUnapproveVersion:
		return SemanticImpactVersionTrustChanged
	case ActionActivateVersion:
		return SemanticImpactEffectiveVersionChanged
	default:
		return ""
	}
}
