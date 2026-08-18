package workflowauthoring

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

type recoveryIndexFake struct{ workspaces []string }

func (fake recoveryIndexFake) ListWorkspaceKeys(context.Context) ([]string, error) {
	return fake.workspaces, nil
}

func TestReconcilePendingVersionsRecordsRetryableRecoveryFailureAndContinuesStartup(t *testing.T) {
	steps := []string{}
	staged := newLifecycleStaged(&steps)
	staged.disposition = FailureRetryable
	recoverErr := errors.New("temporary pending bundle read failure")
	coordinator, err := New(lifecycleStagerFake{staged: staged, recoverErr: recoverErr}, lifecycleAuthorityFake{})
	if err != nil {
		t.Fatal(err)
	}
	driver := &workflowcatalog.Driver{WorkspaceKey: "TEST", DriverID: "demo", Revision: 4}
	version := pendingRecoveryVersion()
	commands := &lifecycleCatalogFake{steps: &steps}
	catalog := &recoveryCatalogFake{lifecycleCatalogFake: commands, driver: driver, version: version}
	if err := coordinator.ReconcilePendingVersions(context.Background(), recoveryIndexFake{workspaces: []string{"TEST"}}, catalog, catalog); err != nil {
		t.Fatalf("retryable recovery must not stop startup: %v", err)
	}
	if got, want := strings.Join(steps, ","), "availability:retryable_failure"; got != want {
		t.Fatalf("steps=%q, want %q", got, want)
	}
	if commands.availability.Failure != "bundle_recovery_failed" {
		t.Fatalf("availability=%+v", commands.availability)
	}
}

func TestReconcilePendingVersionsRecordsPermanentRecoveryFailureAndContinuesStartup(t *testing.T) {
	steps := []string{}
	staged := newLifecycleStaged(&steps)
	staged.disposition = FailurePermanent
	recoverErr := errors.New("bundle digest drift")
	coordinator, err := New(lifecycleStagerFake{staged: staged, recoverErr: recoverErr}, lifecycleAuthorityFake{})
	if err != nil {
		t.Fatal(err)
	}
	driver := &workflowcatalog.Driver{WorkspaceKey: "TEST", DriverID: "demo", Revision: 4}
	version := pendingRecoveryVersion()
	commands := &lifecycleCatalogFake{steps: &steps}
	catalog := &recoveryCatalogFake{lifecycleCatalogFake: commands, driver: driver, version: version}
	if err := coordinator.ReconcilePendingVersions(context.Background(), recoveryIndexFake{workspaces: []string{"TEST"}}, catalog, catalog); err != nil {
		t.Fatalf("permanent recovery failure must be durably recorded without stopping unrelated startup: %v", err)
	}
	if got, want := strings.Join(steps, ","), "availability:permanent_failure"; got != want {
		t.Fatalf("steps=%q, want %q", got, want)
	}
	if commands.availability.Failure != "bundle_recovery_failed" {
		t.Fatalf("availability=%+v", commands.availability)
	}
}

func pendingRecoveryVersion() *workflowcatalog.DriverVersion {
	return &workflowcatalog.DriverVersion{
		WorkspaceKey: "TEST", DriverID: "demo", VersionID: "demo-v1",
		SourceDigest: testSourceDigest, BundleDigest: testBundleDigest,
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityPending,
	}
}

type recoveryCatalogFake struct {
	*lifecycleCatalogFake
	driver  *workflowcatalog.Driver
	version *workflowcatalog.DriverVersion
}

func (fake *recoveryCatalogFake) ListDrivers(context.Context, string) ([]*workflowcatalog.Driver, error) {
	return []*workflowcatalog.Driver{fake.driver}, nil
}
func (fake *recoveryCatalogFake) ListVersions(context.Context, string, string) (*workflowcatalog.VersionSet, error) {
	return &workflowcatalog.VersionSet{Driver: fake.driver, Versions: []*workflowcatalog.DriverVersion{fake.version}}, nil
}
func (fake *recoveryCatalogFake) GetDriver(context.Context, string, string) (*workflowcatalog.Driver, error) {
	return fake.driver, nil
}
func (fake *recoveryCatalogFake) GetVersion(context.Context, string, string) (*workflowcatalog.DriverVersion, error) {
	return fake.version, nil
}

func TestReconcilePendingVersionsPromotesVerifiesAndMarksAvailable(t *testing.T) {
	steps := []string{}
	staged := newLifecycleStaged(&steps)
	stager := lifecycleStagerFake{staged: staged}
	coordinator, err := New(stager, lifecycleAuthorityFake{})
	if err != nil {
		t.Fatal(err)
	}
	driver := &workflowcatalog.Driver{WorkspaceKey: "TEST", DriverID: "demo", Revision: 4}
	version := &workflowcatalog.DriverVersion{
		WorkspaceKey: "TEST", DriverID: "demo", VersionID: "demo-v1",
		SourceDigest: testSourceDigest, BundleDigest: testBundleDigest,
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityPending,
	}
	commands := &lifecycleCatalogFake{steps: &steps}
	catalog := &recoveryCatalogFake{lifecycleCatalogFake: commands, driver: driver, version: version}
	if err := coordinator.ReconcilePendingVersions(context.Background(), recoveryIndexFake{workspaces: []string{"TEST"}}, catalog, catalog); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(steps, ","), "promote,verify,availability:available,discard"; got != want {
		t.Fatalf("steps=%q, want %q", got, want)
	}
	if commands.availability.ExpectedRevision != 4 || commands.availability.VersionID != "demo-v1" {
		t.Fatalf("availability=%+v", commands.availability)
	}
}
