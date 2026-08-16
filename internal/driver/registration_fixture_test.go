package driver

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// FlueDriverFixture is the catalog projection needed by Driver behavior tests.
// It is deliberately a fixture rather than a test copy of the retired native
// registration use case.
type FlueDriverFixture struct {
	Driver  *workflowcatalog.Driver
	Version *workflowcatalog.DriverVersion
	Bundle  *Bundle
}

type flueDriverFixtureStore interface {
	Drivers() workflowcatalog.DriverStore
	DriverVersions() workflowcatalog.DriverVersionStore
}

type flueDriverFixtureLifecycle interface {
	ApproveDriverVersionForTest(context.Context, string, string, string) (*workflowcatalog.Driver, error)
	UnapproveDriverVersionForTest(context.Context, string, string, string) (*workflowcatalog.Driver, error)
	ActivateDriverVersionForTest(context.Context, string, string, string) (*workflowcatalog.Driver, error)
}

// SeedFlueDriverFixture stages the real native bundle and writes only the
// minimal read projections required by Driver tests. Workflow Catalog command
// semantics are covered by the owner package and are not reproduced here.
func SeedFlueDriverFixture(
	ctx context.Context,
	state flueDriverFixtureStore,
	options RegisterFlueOptions,
) (*FlueDriverFixture, error) {
	if state == nil {
		return nil, fmt.Errorf("fixture store is required: %w", persistence.ErrInvalid)
	}
	staged, err := StageFlueDriverBundle(options)
	if err != nil {
		return nil, err
	}
	defer staged.Cleanup()
	if err := staged.Promote(); err != nil {
		return nil, err
	}

	driverRecord, err := state.Drivers().Get(ctx, options.WorkspaceKey, staged.DriverID)
	if errors.Is(err, persistence.ErrNotFound) {
		driverRecord, err = state.Drivers().Create(ctx, workflowcatalog.DriverCreate{
			WorkspaceKey: options.WorkspaceKey,
			DriverID:     staged.DriverID,
			Name:         staged.DriverName,
			OwnerType:    workflowcatalog.DriverOwnerUser,
			Description:  "Driver test fixture",
			Status:       workflowcatalog.DriverStatusDraft,
			TrustLevel:   registrationTrust(options.Trust),
			Metadata: map[string]string{
				"source_ref": staged.SourceRef,
				"runtime":    RuntimeFlueNode,
				"entrypoint": EntrypointRun,
			},
		})
	}
	if err != nil {
		return nil, err
	}

	versionRecord, err := state.DriverVersions().Get(ctx, options.WorkspaceKey, staged.VersionID)
	if errors.Is(err, persistence.ErrNotFound) {
		versions, listErr := state.DriverVersions().List(ctx, options.WorkspaceKey, workflowcatalog.DriverVersionFilter{DriverID: staged.DriverID})
		if listErr != nil {
			return nil, listErr
		}
		versionRecord, err = state.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
			WorkspaceKey:     options.WorkspaceKey,
			VersionID:        staged.VersionID,
			DriverID:         staged.DriverID,
			Version:          len(versions) + 1,
			SourceRef:        staged.SourceRef,
			SourceDigest:     staged.SourceDigest,
			BundleRef:        staged.BundleRef,
			BundleDigest:     staged.BundleDigest,
			Runtime:          staged.Runtime,
			Manifest:         staged.Bundle.Manifest,
			BuildDiagnostics: staged.BuildDiagnostics,
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
			CreatedBy:        options.CreatedBy,
		})
	}
	if err != nil {
		return nil, err
	}

	if options.Activate {
		lifecycle, ok := state.(flueDriverFixtureLifecycle)
		if !ok {
			return nil, fmt.Errorf("fixture store lacks Workflow Catalog lifecycle setup: %w", persistence.ErrInvalid)
		}
		if _, err := lifecycle.ApproveDriverVersionForTest(ctx, options.WorkspaceKey, staged.DriverID, staged.VersionID); err != nil {
			return nil, err
		}
		driverRecord, err = lifecycle.ActivateDriverVersionForTest(ctx, options.WorkspaceKey, staged.DriverID, staged.VersionID)
		if err != nil {
			return nil, err
		}
		if !registrationTrust(options.Trust).Trusted() {
			driverRecord, err = lifecycle.UnapproveDriverVersionForTest(ctx, options.WorkspaceKey, staged.DriverID, staged.VersionID)
			if err != nil {
				return nil, err
			}
		}
	}

	return &FlueDriverFixture{Driver: driverRecord, Version: versionRecord, Bundle: staged.Bundle}, nil
}
