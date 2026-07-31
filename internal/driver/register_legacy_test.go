package driver

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// RegistrationCatalog and RegisterFlueDriver are retained only in the driver
// package's test build while older runtime fixtures migrate to owner-faithful
// Workflow Catalog authoring. No production binary links this split-commit
// compatibility implementation.
type RegistrationCatalog interface {
	Drivers() store.DriverStore
	DriverVersions() store.DriverVersionStore
}

type registrationLifecycleTestCatalog interface {
	ApproveDriverVersionForTest(context.Context, string, string, string) (*domain.Driver, error)
	UnapproveDriverVersionForTest(context.Context, string, string, string) (*domain.Driver, error)
	ActivateDriverVersionForTest(context.Context, string, string, string) (*domain.Driver, error)
}

func RegisterFlueDriver(
	ctx context.Context,
	s RegistrationCatalog,
	opts RegisterFlueOptions,
) (*RegisterFlueResult, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	if opts.WorkspaceKey == "" {
		return nil, fmt.Errorf("workspace key required: %w", domain.ErrInvalid)
	}
	reg, err := resolveFlueRegistrationInput(opts)
	if err != nil {
		return nil, err
	}

	result := &RegisterFlueResult{}
	driverRecord, createdDriver, err := ensureRegisteredDriver(
		ctx,
		s,
		opts.WorkspaceKey,
		reg.driverID,
		reg.driverName,
		reg.sourceRef,
		registrationTrust(opts.Trust),
	)
	if err != nil {
		return nil, err
	}
	result.Driver = driverRecord
	result.CreatedDriver = createdDriver

	staged, err := stageFlueBundle(reg)
	cleanupTmp := staged != nil
	defer func() {
		if cleanupTmp {
			_ = os.RemoveAll(staged.tmpRoot)
		}
	}()
	if err != nil {
		return result, err
	}

	if existing, getErr := s.DriverVersions().Get(
		ctx,
		opts.WorkspaceKey,
		staged.versionID,
	); getErr == nil {
		return result, reuseRegisteredFlueVersion(
			ctx,
			s,
			opts,
			result,
			existing,
			reg.driverID,
			staged,
		)
	} else if !errors.Is(getErr, domain.ErrNotFound) {
		return result, fmt.Errorf("get driver version: %w", getErr)
	}

	nextVersion, err := nextDriverVersion(ctx, s, opts.WorkspaceKey, reg.driverID)
	if err != nil {
		return result, err
	}
	if err := promoteFlueBundle(staged.tmpRoot, staged.finalRoot); err != nil {
		return result, err
	}
	cleanupTmp = false
	if err := persistRegisteredFlueVersion(ctx, s, opts, reg, staged, result, nextVersion); err != nil {
		return result, err
	}
	return result, nil
}

func reuseRegisteredFlueVersion(
	ctx context.Context,
	s RegistrationCatalog,
	opts RegisterFlueOptions,
	result *RegisterFlueResult,
	existing *domain.DriverVersion,
	driverID string,
	staged *stagedFlueBundle,
) error {
	result.Version = existing
	result.ReusedVersion = true
	if existing.DriverID != driverID || existing.BundleDigest != staged.bundleDigest {
		return fmt.Errorf(
			"driver version %q already exists with different content: %w",
			staged.versionID,
			domain.ErrAlreadyExists,
		)
	}
	if registeredBundleMissing(staged.finalRoot) {
		if err := promoteFlueBundle(staged.tmpRoot, staged.finalRoot); err != nil {
			return err
		}
		staged.tmpRoot = ""
	}
	if opts.Activate && result.Driver.ActiveVersionID != existing.VersionID {
		if err := activateRegisteredDriver(
			ctx,
			s,
			result,
			opts.WorkspaceKey,
			driverID,
			existing.VersionID,
		); err != nil {
			return err
		}
	}
	result.Bundle = &Bundle{
		Root: staged.finalRoot, BundleRef: existing.BundleRef,
		SourceRef: existing.SourceRef, SourceDigest: existing.SourceDigest,
		BundleDigest: existing.BundleDigest, Manifest: existing.Manifest,
	}
	return nil
}

func persistRegisteredFlueVersion(
	ctx context.Context,
	s RegistrationCatalog,
	opts RegisterFlueOptions,
	reg *flueRegistrationInput,
	staged *stagedFlueBundle,
	result *RegisterFlueResult,
	nextVersion int,
) error {
	version, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     opts.WorkspaceKey,
		VersionID:        staged.versionID,
		DriverID:         reg.driverID,
		Version:          nextVersion,
		SourceRef:        reg.sourceRef,
		SourceDigest:     reg.sourceDigest,
		BundleRef:        staged.bundleRef,
		BundleDigest:     staged.bundleDigest,
		Runtime:          RuntimeFlueNode,
		Manifest:         staged.manifest,
		BuildDiagnostics: opts.BuildDiagnostics,
		ValidationStatus: domain.DriverVersionValidationPassed,
		CreatedBy:        opts.CreatedBy,
	})
	if err != nil {
		return fmt.Errorf("create native Flue driver version: %w", err)
	}
	result.Version = version
	result.CreatedVersion = true
	result.Bundle = &Bundle{
		Root: staged.finalRoot, BundleRef: staged.bundleRef,
		SourceRef: reg.sourceRef, SourceDigest: reg.sourceDigest,
		BundleDigest: staged.bundleDigest, Manifest: staged.manifest,
	}
	if opts.Activate {
		return activateRegisteredDriver(
			ctx,
			s,
			result,
			opts.WorkspaceKey,
			reg.driverID,
			staged.versionID,
		)
	}
	return nil
}

func ensureRegisteredDriver(
	ctx context.Context,
	s RegistrationCatalog,
	workspace, driverID, driverName, sourceRef string,
	trust domain.DriverTrustLevel,
) (*domain.Driver, bool, error) {
	driverRecord, err := s.Drivers().Get(ctx, workspace, driverID)
	if err == nil {
		return demoteReregisteredDriver(ctx, s, driverRecord, trust)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, false, fmt.Errorf("get driver: %w", err)
	}
	driverRecord, err = s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: workspace,
		DriverID:     driverID,
		Name:         driverName,
		OwnerType:    domain.DriverOwnerUser,
		Description:  "Native Flue driver registered from " + sourceRef,
		Status:       domain.DriverStatusDraft,
		TrustLevel:   trust,
		Metadata: map[string]string{
			"source_ref":    sourceRef,
			"runtime":       RuntimeFlueNode,
			"entrypoint":    EntrypointRun,
			"artifact_kind": NativeFlueArtifactKind,
		},
	})
	if err != nil {
		return nil, false, fmt.Errorf("create driver: %w", err)
	}
	return driverRecord, true, nil
}

func demoteReregisteredDriver(
	ctx context.Context,
	s RegistrationCatalog,
	driverRecord *domain.Driver,
	trust domain.DriverTrustLevel,
) (*domain.Driver, bool, error) {
	if trust.Trusted() || !driverRecord.TrustLevel.Trusted() {
		return driverRecord, false, nil
	}
	demoted := domain.DriverTrustUntrusted
	updated, err := s.Drivers().Update(
		ctx,
		driverRecord.WorkspaceKey,
		driverRecord.DriverID,
		store.DriverUpdate{TrustLevel: &demoted},
	)
	if err != nil {
		return nil, false, fmt.Errorf("demote re-registered driver trust: %w", err)
	}
	return updated, false, nil
}

func activateRegisteredDriver(
	ctx context.Context,
	s RegistrationCatalog,
	result *RegisterFlueResult,
	workspace, driverID, versionID string,
) error {
	driverRecord, version, err := ActivateDriverVersion(
		ctx,
		s,
		workspace,
		driverID,
		versionID,
	)
	if err != nil {
		return fmt.Errorf("activate native Flue driver version: %w", err)
	}
	result.Driver = driverRecord
	if result.Version == nil {
		result.Version = version
	}
	result.Activated = true
	return nil
}

func nextDriverVersion(
	ctx context.Context,
	s RegistrationCatalog,
	workspace, driverID string,
) (int, error) {
	versions, err := s.DriverVersions().List(
		ctx,
		workspace,
		store.DriverVersionFilter{DriverID: driverID},
	)
	if err != nil {
		return 0, fmt.Errorf("list driver versions: %w", err)
	}
	maxVersion := 0
	for _, version := range versions {
		if version != nil && version.DriverID == driverID && version.Version > maxVersion {
			maxVersion = version.Version
		}
	}
	return maxVersion + 1, nil
}

func ActivateDriverVersion(
	ctx context.Context,
	catalog RegistrationCatalog,
	workspace, driverID, versionID string,
) (*domain.Driver, *domain.DriverVersion, error) {
	driverRecord, version, err := loadDriverVersionForOperatorAction(ctx, catalog.Drivers(), catalog.DriverVersions(), workspace, driverID, versionID)
	if err != nil {
		return nil, nil, err
	}
	lifecycle, ok := catalog.(registrationLifecycleTestCatalog)
	if !ok {
		return nil, nil, fmt.Errorf("legacy test catalog lacks typed Workflow Catalog lifecycle fixtures: %w", domain.ErrInvalid)
	}
	if _, err := lifecycle.ApproveDriverVersionForTest(ctx, workspace, driverRecord.DriverID, version.VersionID); err != nil {
		return nil, nil, fmt.Errorf("approve driver version: %w", err)
	}
	updated, err := lifecycle.ActivateDriverVersionForTest(ctx, workspace, driverRecord.DriverID, version.VersionID)
	if err != nil {
		return nil, nil, fmt.Errorf("activate driver version: %w", err)
	}
	if DriverVersionEffectiveTrust(driverRecord, version) == domain.DriverTrustUntrusted {
		updated, err = lifecycle.UnapproveDriverVersionForTest(ctx, workspace, driverRecord.DriverID, version.VersionID)
		if err != nil {
			return nil, nil, fmt.Errorf("restore active untrusted version: %w", err)
		}
	}
	return updated, version, nil
}
