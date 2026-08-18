package workflowauthoring

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

// ReconcilePendingVersions resumes the durable authoring handoff after a
// process restart. Individual distribution failures are converted into the
// catalog's bounded availability state and do not prevent unrelated versions
// or the server from progressing; inability to read or record owner state is
// returned because that would make recovery unverifiable.
func (coordinator *Coordinator) ReconcilePendingVersions(
	ctx context.Context,
	index PendingVersionIndex,
	catalog PendingCatalog,
	commands CatalogCommands,
) error {
	if coordinator == nil || coordinator.stager == nil || coordinator.authorities == nil ||
		index == nil || catalog == nil || commands == nil {
		return workflowcatalog.ErrUnavailable
	}
	workspaces, err := index.ListWorkspaceKeys(ctx)
	if err != nil {
		return fmt.Errorf("list workspaces for workflow availability recovery: %w", err)
	}
	var recoveryErrs []error
	for _, value := range workspaces {
		workspace := strings.TrimSpace(value)
		if workspace == "" {
			continue
		}
		if err := coordinator.reconcileWorkspacePendingVersions(ctx, workspace, catalog, commands); err != nil {
			recoveryErrs = append(recoveryErrs, fmt.Errorf("workspace %q: %w", workspace, err))
		}
	}
	return errors.Join(recoveryErrs...)
}

func (coordinator *Coordinator) reconcileWorkspacePendingVersions(
	ctx context.Context,
	workspace string,
	catalog PendingCatalog,
	commands CatalogCommands,
) error {
	drivers, err := catalog.ListDrivers(ctx, workspace)
	if err != nil {
		return fmt.Errorf("list drivers: %w", err)
	}
	var errs []error
	for _, driver := range drivers {
		if driver == nil || strings.TrimSpace(driver.DriverID) == "" {
			continue
		}
		set, err := catalog.ListVersions(ctx, workspace, driver.DriverID)
		if err != nil {
			errs = append(errs, fmt.Errorf("list versions for driver %q: %w", driver.DriverID, err))
			continue
		}
		for _, version := range set.Versions {
			if version == nil || version.AvailabilityStatus != workflowcatalog.DriverVersionAvailabilityPending {
				continue
			}
			if err := coordinator.reconcilePendingVersion(ctx, workspace, driver.DriverID, version.VersionID, catalog, commands); err != nil {
				errs = append(errs, fmt.Errorf("recover version %q: %w", version.VersionID, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (coordinator *Coordinator) reconcilePendingVersion(
	ctx context.Context,
	workspace, driverID, versionID string,
	catalog PendingCatalog,
	commands CatalogCommands,
) error {
	version, err := catalog.GetVersion(ctx, workspace, versionID)
	if err != nil {
		return err
	}
	if version.AvailabilityStatus != workflowcatalog.DriverVersionAvailabilityPending {
		return nil
	}
	driver, err := catalog.GetDriver(ctx, workspace, driverID)
	if err != nil {
		return err
	}
	result := &Result{Driver: driver, Version: version}
	staged, disposition, recoverErr := coordinator.stager.RecoverPending(ctx, version)
	if recoverErr != nil {
		return coordinator.recordRecoveryFailure(ctx, commands, result, disposition, "bundle_recovery_failed")
	}
	if staged == nil {
		return workflowcatalog.ErrInvalidPersistedState
	}
	if err := staged.Promote(); err != nil {
		return coordinator.recordRecoveryFailure(ctx, commands, result, staged.ClassifyFailure(err), "bundle_promotion_failed")
	}
	if err := staged.Verify(); err != nil {
		return coordinator.recordRecoveryFailure(ctx, commands, result, staged.ClassifyFailure(err), "bundle_verification_failed")
	}
	available, err := coordinator.recordAvailability(ctx, commands, result, workflowcatalog.AvailabilityOutcomeAvailable, "")
	if err != nil {
		return err
	}
	if available == nil || !workflowcatalog.VersionAvailable(available.Version) {
		return workflowcatalog.ErrInvalidPersistedState
	}
	staged.Discard()
	return nil
}

func (coordinator *Coordinator) recordRecoveryFailure(
	ctx context.Context,
	commands CatalogCommands,
	result *Result,
	disposition FailureDisposition,
	failure string,
) error {
	outcome := workflowcatalog.AvailabilityOutcomePermanentFailure
	if disposition == FailureRetryable {
		outcome = workflowcatalog.AvailabilityOutcomeRetryableFailure
	}
	_, err := coordinator.recordAvailability(ctx, commands, result, outcome, failure)
	return err
}
