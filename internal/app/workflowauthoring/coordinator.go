package workflowauthoring

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type Coordinator struct {
	stager       BundleStager
	nativeStager NativeBundleStager
	authorities  DistributionAuthorityProvider
}

func New(stager BundleStager, authorities DistributionAuthorityProvider) (*Coordinator, error) {
	if stager == nil || authorities == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	return &Coordinator{stager: stager, authorities: authorities}, nil
}

func NewWithNative(
	stager BundleStager,
	nativeStager NativeBundleStager,
	authorities DistributionAuthorityProvider,
) (*Coordinator, error) {
	coordinator, err := New(stager, authorities)
	if err != nil {
		return nil, err
	}
	if nativeStager == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	coordinator.nativeStager = nativeStager
	return coordinator, nil
}

func (coordinator *Coordinator) AuthorOperator(
	ctx context.Context,
	api CatalogCommands,
	auth authority.OperatorAuthority,
	options BuildOptions,
) (*Result, string, error) {
	if options.Activate {
		return nil, "", fmt.Errorf("operator workflow authoring cannot activate a version: %w", workflowcatalog.ErrInvalid)
	}
	if options.Trust == workflowcatalog.DriverTrustTrusted {
		return nil, "", fmt.Errorf("operator workflow authoring cannot select trusted placement: %w", workflowcatalog.ErrInvalid)
	}
	options.Trust = workflowcatalog.DriverTrustUntrusted
	return coordinator.author(ctx, api, auth, authority.SystemAuthority{}, options, false)
}

func (coordinator *Coordinator) AuthorManaged(
	ctx context.Context,
	api CatalogCommands,
	auth authority.SystemAuthority,
	options BuildOptions,
) (*Result, string, error) {
	if !workflowcatalog.IsBuiltinWorkflowName(strings.TrimSpace(options.Name)) {
		return nil, "", fmt.Errorf("managed workflow %q is not a canonical builtin: %w", options.Name, workflowcatalog.ErrInvalid)
	}
	options.Trust = workflowcatalog.DriverTrustTrusted
	options.Manifest = cloneManifest(options.Manifest)
	if provenance := strings.TrimSpace(options.Manifest["provenance"]); provenance != "" &&
		provenance != workflowcatalog.ManagedBuiltinProvenance {
		return nil, "", fmt.Errorf("managed workflow provenance %q is invalid: %w", provenance, workflowcatalog.ErrInvalid)
	}
	options.Manifest["provenance"] = workflowcatalog.ManagedBuiltinProvenance
	return coordinator.author(ctx, api, authority.OperatorAuthority{}, auth, options, true)
}

func (coordinator *Coordinator) author(
	ctx context.Context,
	api CatalogCommands,
	operatorAuth authority.OperatorAuthority,
	systemAuth authority.SystemAuthority,
	options BuildOptions,
	managed bool,
) (*Result, string, error) {
	if coordinator == nil || coordinator.stager == nil || coordinator.authorities == nil || api == nil {
		return nil, "", workflowcatalog.ErrUnavailable
	}
	staged, diagnostics, err := coordinator.stager.BuildAndStage(ctx, options)
	if err != nil {
		return nil, diagnostics, err
	}
	if staged == nil {
		return nil, diagnostics, workflowcatalog.ErrInvalidPersistedState
	}
	result, err := coordinator.authorStaged(
		ctx,
		api,
		operatorAuth,
		systemAuth,
		options,
		staged,
		managed,
	)
	if err != nil {
		staged.Discard()
		return nil, diagnostics, err
	}
	result, err = coordinator.distributeAuthoredVersion(ctx, api, result, staged)
	if err != nil {
		return nil, diagnostics, err
	}
	if managed && options.Activate {
		result, err = coordinator.activateManagedVersion(ctx, api, result)
	}
	return result, diagnostics, err
}

func (coordinator *Coordinator) authorStaged(
	ctx context.Context,
	api CatalogCommands,
	operatorAuth authority.OperatorAuthority,
	systemAuth authority.SystemAuthority,
	options BuildOptions,
	staged StagedBundle,
	managed bool,
) (*Result, error) {
	if api == nil || staged == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	metadata := staged.Metadata()
	command := workflowcatalog.AuthorVersionCommand{
		WorkspaceKey: options.WorkspaceKey, RequestID: options.RequestID,
		ExpectedRevision: options.ExpectedRevision,
		DriverID:         metadata.DriverID, DriverName: metadata.DriverName,
		VersionID: metadata.VersionID, SourceRef: metadata.SourceRef, SourceDigest: metadata.SourceDigest,
		BundleRef: metadata.BundleRef, BundleDigest: metadata.BundleDigest, Runtime: metadata.Runtime,
		Manifest: metadata.CatalogManifest, BuildDiagnostics: metadata.Diagnostics,
	}
	if strings.TrimSpace(command.RequestID) == "" {
		command.RequestID = "workflow-author:" + command.WorkspaceKey + ":" + command.DriverID + ":" + command.VersionID
	}
	var authored *workflowcatalog.AuthorVersionResult
	var err error
	if managed {
		authored, err = api.AuthorManagedVersion(ctx, systemAuth, command)
	} else {
		authored, err = api.AuthorVersion(ctx, operatorAuth, command)
	}
	if err != nil {
		return nil, err
	}
	if authored == nil || authored.Driver == nil || authored.Version == nil {
		return nil, workflowcatalog.ErrInvalidPersistedState
	}
	return &Result{
		Driver: authored.Driver, Version: authored.Version, Bundle: staged.Bundle(),
		CreatedDriver: authored.CreatedDriver, CreatedVersion: authored.CreatedVersion,
		ReusedVersion: authored.ReusedVersion,
	}, nil
}

func (coordinator *Coordinator) distributeAuthoredVersion(
	ctx context.Context,
	api CatalogCommands,
	result *Result,
	staged StagedBundle,
) (*Result, error) {
	if result == nil || result.Driver == nil || result.Version == nil {
		staged.Discard()
		return nil, workflowcatalog.ErrInvalidPersistedState
	}
	if err := staged.Promote(); err != nil {
		return nil, coordinator.recordDistributionFailure(ctx, api, result, staged, err, "bundle_promotion_failed")
	}
	if err := staged.Verify(); err != nil {
		return nil, coordinator.recordDistributionFailure(ctx, api, result, staged, err, "bundle_verification_failed")
	}
	if workflowcatalog.VersionAvailable(result.Version) {
		staged.Discard()
		return result, nil
	}
	available, err := coordinator.recordAvailability(ctx, api, result, workflowcatalog.AvailabilityOutcomeAvailable, "")
	if err != nil {
		return nil, err
	}
	result.Driver, result.Version = available.Driver, available.Version
	staged.Discard()
	return result, nil
}

func (coordinator *Coordinator) recordDistributionFailure(
	ctx context.Context,
	api CatalogCommands,
	result *Result,
	staged StagedBundle,
	cause error,
	failure string,
) error {
	outcome := workflowcatalog.AvailabilityOutcomePermanentFailure
	if staged.ClassifyFailure(cause) == FailureRetryable {
		outcome = workflowcatalog.AvailabilityOutcomeRetryableFailure
	}
	_, err := coordinator.recordAvailability(ctx, api, result, outcome, failure)
	if err != nil {
		return errors.Join(cause, err)
	}
	if outcome == workflowcatalog.AvailabilityOutcomePermanentFailure {
		staged.Discard()
	}
	return cause
}

func (coordinator *Coordinator) recordAvailability(
	ctx context.Context,
	api CatalogCommands,
	result *Result,
	outcome workflowcatalog.AvailabilityOutcome,
	failure string,
) (*workflowcatalog.AvailabilityResult, error) {
	revision := result.Driver.Revision
	auth, err := coordinator.authorities.AuthorityForVersionAvailability(
		ctx,
		result.Driver.WorkspaceKey,
		"record immutable workflow bundle availability",
	)
	if err != nil {
		return nil, err
	}
	return api.RecordVersionAvailability(ctx, auth, workflowcatalog.AvailabilityCommand{
		WorkspaceKey: result.Driver.WorkspaceKey,
		RequestID: "workflow-availability:" + result.Version.VersionID + ":" +
			strconv.FormatUint(revision, 10) + ":" + string(outcome),
		ExpectedRevision: revision,
		DriverID:         result.Driver.DriverID,
		VersionID:        result.Version.VersionID,
		SourceDigest:     result.Version.SourceDigest,
		BundleDigest:     result.Version.BundleDigest,
		Outcome:          outcome,
		Failure:          failure,
	})
}

func (coordinator *Coordinator) activateManagedVersion(
	ctx context.Context,
	api CatalogCommands,
	result *Result,
) (*Result, error) {
	auth, err := coordinator.authorities.AuthorityForManagedVersionLifecycle(
		ctx,
		result.Driver.WorkspaceKey,
		workflowcatalog.ActionApproveManagedVersion,
		"approve available managed workflow version",
	)
	if err != nil {
		return nil, err
	}
	approved, err := api.ApproveManagedVersion(ctx, auth, workflowcatalog.VersionCommand{
		WorkspaceKey: result.Driver.WorkspaceKey, DriverID: result.Driver.DriverID,
		VersionID: result.Version.VersionID, ExpectedRevision: result.Driver.Revision,
	})
	if err != nil {
		return nil, fmt.Errorf("approve managed workflow version: %w", err)
	}
	auth, err = coordinator.authorities.AuthorityForManagedVersionLifecycle(
		ctx,
		result.Driver.WorkspaceKey,
		workflowcatalog.ActionActivateManagedVersion,
		"activate available managed workflow version",
	)
	if err != nil {
		return nil, err
	}
	activated, err := api.ActivateManagedVersion(ctx, auth, workflowcatalog.VersionCommand{
		WorkspaceKey: result.Driver.WorkspaceKey, DriverID: result.Driver.DriverID,
		VersionID: result.Version.VersionID, ExpectedRevision: approved.Driver.Revision,
	})
	if err != nil {
		return nil, fmt.Errorf("activate managed workflow version: %w", err)
	}
	result.Driver, result.Version, result.Activated = activated.Driver, activated.Version, true
	return result, nil
}

func cloneManifest(input map[string]string) map[string]string {
	output := make(map[string]string, len(input)+1)
	for key, value := range input {
		output[key] = value
	}
	return output
}
