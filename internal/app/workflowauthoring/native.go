package workflowauthoring

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type NativeAuthoringAuthorities struct {
	Author   authority.OperatorAuthority
	Approve  *authority.OperatorAuthority
	Activate *authority.OperatorAuthority
}

// AuthorNative stages through the injected infrastructure port, then owns the
// author, approve, and activate command sequence against Workflow Catalog.
//
//nolint:cyclop,funlen // The author/approve/activate transaction retains explicit compensation and exact-result checks.
func (coordinator *Coordinator) AuthorNative(
	ctx context.Context,
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities NativeAuthoringAuthorities,
	options NativeOptions,
) (*Result, error) {
	if coordinator == nil || coordinator.nativeStager == nil ||
		catalog == nil || authoring == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	requestedTrust := options.Trust
	if err := validateNativeAuthorities(
		authorities,
		options.WorkspaceKey,
		requestedTrust,
		options.Activate,
	); err != nil {
		return nil, err
	}
	stageOptions := options
	stageOptions.Trust = workflowcatalog.DriverTrustUntrusted
	staged, err := coordinator.nativeStager.StageNative(ctx, stageOptions)
	if err != nil {
		return nil, err
	}
	if staged == nil {
		return nil, workflowcatalog.ErrInvalidPersistedState
	}
	defer staged.Cleanup()

	metadata := staged.Metadata()
	expectedRevision := uint64(0)
	existing, err := catalog.GetDriver(ctx, options.WorkspaceKey, metadata.DriverID)
	switch {
	case err == nil:
		if existing == nil || existing.Revision == 0 {
			return nil, workflowcatalog.ErrInvalidPersistedState
		}
		expectedRevision = existing.Revision
	case errors.Is(err, workflowcatalog.ErrNotFound):
	default:
		return nil, fmt.Errorf("resolve native driver %q: %w", metadata.DriverID, err)
	}
	if err := staged.Promote(); err != nil {
		return nil, err
	}
	result, err := coordinator.authorStaged(
		ctx,
		authoring,
		authorities.Author,
		authority.SystemAuthority{},
		BuildOptions{
			WorkspaceKey:     options.WorkspaceKey,
			ExpectedRevision: expectedRevision,
		},
		staged,
		false,
	)
	if err != nil {
		return nil, err
	}

	result.Driver, result.Version, err = refreshNativeVersion(
		ctx,
		catalog,
		options.WorkspaceKey,
		result.Driver.DriverID,
		result.Version.VersionID,
	)
	if err != nil {
		return nil, err
	}
	if requestedTrust.Trusted() || options.Activate {
		if !workflowcatalog.VersionApproved(result.Driver, result.Version) {
			approved, approveErr := catalog.ApproveVersion(
				ctx,
				*authorities.Approve,
				workflowcatalog.VersionCommand{
					WorkspaceKey: options.WorkspaceKey, DriverID: result.Driver.DriverID,
					VersionID: result.Version.VersionID, ExpectedRevision: result.Driver.Revision,
				},
			)
			if approveErr != nil {
				return nil, fmt.Errorf("approve native driver version: %w", approveErr)
			}
			result.Driver, result.Version = approved.Driver, approved.Version
		}
	}
	if options.Activate {
		result.Driver, result.Version, err = refreshNativeVersion(
			ctx,
			catalog,
			options.WorkspaceKey,
			result.Driver.DriverID,
			result.Version.VersionID,
		)
		if err != nil {
			return nil, err
		}
		if result.Driver.ActiveVersionID != result.Version.VersionID {
			activated, activateErr := catalog.ActivateVersion(
				ctx,
				*authorities.Activate,
				workflowcatalog.VersionCommand{
					WorkspaceKey: options.WorkspaceKey, DriverID: result.Driver.DriverID,
					VersionID: result.Version.VersionID, ExpectedRevision: result.Driver.Revision,
				},
			)
			if activateErr != nil {
				return nil, fmt.Errorf("activate native driver version: %w", activateErr)
			}
			result.Driver, result.Version = activated.Driver, activated.Version
		}
		result.Activated = true
	}
	return result, nil
}

func validateNativeAuthorities(
	authorities NativeAuthoringAuthorities,
	workspace string,
	trust workflowcatalog.DriverTrustLevel,
	activate bool,
) error {
	if authorities.Author.Workspace() != workspace ||
		authorities.Author.Action() != workflowcatalog.ActionAuthorVersion ||
		strings.TrimSpace(authorities.Author.Subject()) == "" {
		return authority.ErrAdmissionDenied
	}
	if trust.Trusted() || activate {
		if authorities.Approve == nil ||
			authorities.Approve.Workspace() != workspace ||
			authorities.Approve.Action() != workflowcatalog.ActionApproveVersion ||
			authorities.Approve.Subject() != authorities.Author.Subject() {
			return authority.ErrAdmissionDenied
		}
	}
	if activate {
		if authorities.Activate == nil ||
			authorities.Activate.Workspace() != workspace ||
			authorities.Activate.Action() != workflowcatalog.ActionActivateVersion ||
			authorities.Activate.Subject() != authorities.Author.Subject() {
			return authority.ErrAdmissionDenied
		}
	}
	return nil
}

func refreshNativeVersion(
	ctx context.Context,
	catalog workflowcatalog.API,
	workspace,
	driverID,
	versionID string,
) (*workflowcatalog.Driver, *workflowcatalog.DriverVersion, error) {
	driverRecord, err := catalog.GetDriver(ctx, workspace, driverID)
	if err != nil {
		return nil, nil, fmt.Errorf("refresh native driver %q: %w", driverID, err)
	}
	version, err := catalog.GetVersion(ctx, workspace, versionID)
	if err != nil {
		return nil, nil, fmt.Errorf("refresh native driver version %q: %w", versionID, err)
	}
	if driverRecord == nil || version == nil || driverRecord.DriverID != driverID ||
		version.DriverID != driverID || version.VersionID != versionID ||
		driverRecord.Revision == 0 {
		return nil, nil, workflowcatalog.ErrInvalidPersistedState
	}
	return driverRecord, version, nil
}
