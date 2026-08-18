package workflowauthoring

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type lifecycleStagedFake struct {
	steps       *[]string
	metadata    StagedMetadata
	promoteErr  error
	verifyErr   error
	disposition FailureDisposition
	discarded   bool
}

func (fake *lifecycleStagedFake) Metadata() StagedMetadata { return fake.metadata }
func (fake *lifecycleStagedFake) Bundle() *Bundle          { return &Bundle{BundleRef: fake.metadata.BundleRef} }
func (fake *lifecycleStagedFake) Promote() error {
	*fake.steps = append(*fake.steps, "promote")
	return fake.promoteErr
}
func (fake *lifecycleStagedFake) Verify() error {
	*fake.steps = append(*fake.steps, "verify")
	return fake.verifyErr
}
func (fake *lifecycleStagedFake) ClassifyFailure(error) FailureDisposition { return fake.disposition }
func (fake *lifecycleStagedFake) Discard() {
	fake.discarded = true
	*fake.steps = append(*fake.steps, "discard")
}

type lifecycleStagerFake struct {
	staged     *lifecycleStagedFake
	recoverErr error
}

func (fake lifecycleStagerFake) BuildAndStage(context.Context, BuildOptions) (StagedBundle, string, error) {
	return fake.staged, "built", nil
}
func (fake lifecycleStagerFake) RecoverPending(context.Context, *workflowcatalog.DriverVersion) (StagedBundle, FailureDisposition, error) {
	return fake.staged, fake.staged.disposition, fake.recoverErr
}

type lifecycleCatalogFake struct {
	steps                    *[]string
	availability             workflowcatalog.AvailabilityCommand
	managed                  bool
	activeBefore             string
	activeObservedAtApproval string
	activeObservedAtActivate string
	currentDriver            *workflowcatalog.Driver
}

func (fake *lifecycleCatalogFake) AuthorVersion(context.Context, authority.OperatorAuthority, workflowcatalog.AuthorVersionCommand) (*workflowcatalog.AuthorVersionResult, error) {
	return fake.authored(), nil
}
func (fake *lifecycleCatalogFake) AuthorManagedVersion(context.Context, authority.SystemAuthority, workflowcatalog.AuthorVersionCommand) (*workflowcatalog.AuthorVersionResult, error) {
	return fake.authored(), nil
}
func (fake *lifecycleCatalogFake) authored() *workflowcatalog.AuthorVersionResult {
	*fake.steps = append(*fake.steps, "author-pending")
	driver := &workflowcatalog.Driver{
		WorkspaceKey: "TEST", DriverID: "demo", Revision: 1,
		ActiveVersionID: fake.activeBefore,
	}
	if fake.activeBefore != "" {
		driver.Status = workflowcatalog.DriverStatusActive
	}
	fake.currentDriver = driver
	return &workflowcatalog.AuthorVersionResult{
		Driver: driver,
		Version: &workflowcatalog.DriverVersion{
			WorkspaceKey: "TEST", DriverID: "demo", VersionID: "demo-v1",
			SourceDigest: testSourceDigest, BundleDigest: testBundleDigest,
			ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
			AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityPending,
		},
		CreatedDriver: true, CreatedVersion: true, CommittedRevision: 1,
	}
}
func (fake *lifecycleCatalogFake) RecordVersionAvailability(_ context.Context, _ authority.SystemAuthority, command workflowcatalog.AvailabilityCommand) (*workflowcatalog.AvailabilityResult, error) {
	*fake.steps = append(*fake.steps, "availability:"+string(command.Outcome))
	fake.availability = command
	status := workflowcatalog.DriverVersionAvailabilityPending
	if command.Outcome == workflowcatalog.AvailabilityOutcomeAvailable {
		status = workflowcatalog.DriverVersionAvailabilityAvailable
	} else if command.Outcome == workflowcatalog.AvailabilityOutcomePermanentFailure {
		status = workflowcatalog.DriverVersionAvailabilityFailed
	}
	driver := &workflowcatalog.Driver{WorkspaceKey: "TEST", DriverID: "demo", Revision: command.ExpectedRevision + 1}
	if fake.currentDriver != nil {
		*driver = *fake.currentDriver
		driver.Revision = command.ExpectedRevision + 1
	}
	fake.currentDriver = driver
	return &workflowcatalog.AvailabilityResult{
		Driver:            driver,
		Version:           &workflowcatalog.DriverVersion{WorkspaceKey: "TEST", DriverID: "demo", VersionID: "demo-v1", SourceDigest: testSourceDigest, BundleDigest: testBundleDigest, ValidationStatus: workflowcatalog.DriverVersionValidationPassed, AvailabilityStatus: status},
		CommittedRevision: command.ExpectedRevision + 1,
	}, nil
}
func (fake *lifecycleCatalogFake) ApproveManagedVersion(_ context.Context, _ authority.SystemAuthority, command workflowcatalog.VersionCommand) (*workflowcatalog.VersionResult, error) {
	if !fake.managed {
		return nil, errors.New("unexpected approve")
	}
	*fake.steps = append(*fake.steps, "approve")
	driver := *fake.currentDriver
	fake.activeObservedAtApproval = driver.ActiveVersionID
	driver.Revision = command.ExpectedRevision + 1
	fake.currentDriver = &driver
	return &workflowcatalog.VersionResult{
		Driver: &driver,
		Version: &workflowcatalog.DriverVersion{
			WorkspaceKey: "TEST", DriverID: "demo", VersionID: "demo-v1",
			SourceDigest: testSourceDigest, BundleDigest: testBundleDigest,
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed, AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
		},
	}, nil
}
func (fake *lifecycleCatalogFake) ActivateManagedVersion(_ context.Context, _ authority.SystemAuthority, command workflowcatalog.VersionCommand) (*workflowcatalog.VersionResult, error) {
	if !fake.managed {
		return nil, errors.New("unexpected activate")
	}
	*fake.steps = append(*fake.steps, "activate")
	driver := *fake.currentDriver
	fake.activeObservedAtActivate = driver.ActiveVersionID
	driver.Revision = command.ExpectedRevision + 1
	driver.ActiveVersionID = command.VersionID
	driver.Status = workflowcatalog.DriverStatusActive
	fake.currentDriver = &driver
	return &workflowcatalog.VersionResult{
		Driver: &driver,
		Version: &workflowcatalog.DriverVersion{
			WorkspaceKey: "TEST", DriverID: "demo", VersionID: command.VersionID,
			SourceDigest: testSourceDigest, BundleDigest: testBundleDigest,
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed, AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
		},
	}, nil
}

type lifecycleAuthorityFake struct{}

func (lifecycleAuthorityFake) AuthorityForVersionAvailability(context.Context, string, string) (authority.SystemAuthority, error) {
	return authority.SystemAuthority{}, nil
}
func (lifecycleAuthorityFake) AuthorityForManagedVersionLifecycle(context.Context, string, authority.Action, string) (authority.SystemAuthority, error) {
	return authority.SystemAuthority{}, nil
}

const (
	testSourceDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testBundleDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func newLifecycleStaged(steps *[]string) *lifecycleStagedFake {
	return &lifecycleStagedFake{steps: steps, disposition: FailurePermanent, metadata: StagedMetadata{
		DriverName: "demo", DriverID: "demo", VersionID: "demo-v1", SourceDigest: testSourceDigest,
		BundleDigest: testBundleDigest, BundleRef: ".loom/drivers/demo-v1", Runtime: "flue-node",
	}}
}

func TestAuthoringLifecyclePersistsPendingBeforePromoteVerifyAndAvailability(t *testing.T) {
	steps := []string{}
	staged := newLifecycleStaged(&steps)
	coordinator, err := New(lifecycleStagerFake{staged: staged}, lifecycleAuthorityFake{})
	if err != nil {
		t.Fatal(err)
	}
	catalog := &lifecycleCatalogFake{steps: &steps}
	result, _, err := coordinator.AuthorOperator(context.Background(), catalog, authority.OperatorAuthority{}, BuildOptions{WorkspaceKey: "TEST", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(steps, ","), "author-pending,promote,verify,availability:available,discard"; got != want {
		t.Fatalf("steps=%q, want %q", got, want)
	}
	if result.Version.AvailabilityStatus != workflowcatalog.DriverVersionAvailabilityAvailable || catalog.availability.ExpectedRevision != 1 {
		t.Fatalf("result=%+v command=%+v", result.Version, catalog.availability)
	}
}

func TestAuthoringLifecyclePreservesRetryableStageAndRecordsFailure(t *testing.T) {
	steps := []string{}
	staged := newLifecycleStaged(&steps)
	staged.promoteErr = errors.New("temporary rename failure")
	staged.disposition = FailureRetryable
	coordinator, err := New(lifecycleStagerFake{staged: staged}, lifecycleAuthorityFake{})
	if err != nil {
		t.Fatal(err)
	}
	catalog := &lifecycleCatalogFake{steps: &steps}
	_, _, err = coordinator.AuthorOperator(context.Background(), catalog, authority.OperatorAuthority{}, BuildOptions{WorkspaceKey: "TEST", Name: "demo"})
	if !errors.Is(err, staged.promoteErr) {
		t.Fatalf("err=%v", err)
	}
	if got, want := strings.Join(steps, ","), "author-pending,promote,availability:retryable_failure"; got != want {
		t.Fatalf("steps=%q, want %q", got, want)
	}
	if staged.discarded {
		t.Fatal("retryable stage was discarded")
	}
}

func TestManagedLifecyclePreservesActivePredecessorUntilAvailableVersionActivation(t *testing.T) {
	steps := []string{}
	staged := newLifecycleStaged(&steps)
	staged.metadata.DriverName = "epic-runner"
	staged.metadata.DriverID = "demo"
	coordinator, err := New(lifecycleStagerFake{staged: staged}, lifecycleAuthorityFake{})
	if err != nil {
		t.Fatal(err)
	}
	catalog := &lifecycleCatalogFake{steps: &steps, managed: true, activeBefore: "demo-v0"}
	result, _, err := coordinator.AuthorManaged(
		context.Background(),
		catalog,
		authority.SystemAuthority{},
		BuildOptions{WorkspaceKey: "TEST", Name: "epic-runner", Activate: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(steps, ","), "author-pending,promote,verify,availability:available,discard,approve,activate"; got != want {
		t.Fatalf("steps=%q, want %q", got, want)
	}
	if catalog.activeObservedAtApproval != "demo-v0" || catalog.activeObservedAtActivate != "demo-v0" {
		t.Fatalf("predecessor changed before activation: approval=%q activation=%q", catalog.activeObservedAtApproval, catalog.activeObservedAtActivate)
	}
	if result.Driver.ActiveVersionID != "demo-v1" || !result.Activated {
		t.Fatalf("result=%+v", result)
	}
}
