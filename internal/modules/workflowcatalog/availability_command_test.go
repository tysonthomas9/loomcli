package workflowcatalog

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type availabilityStoreFake struct {
	calls    int
	mutation AvailabilityMutation
	result   *AvailabilityResult
	err      error
}

func (fake *availabilityStoreFake) RecordVersionAvailability(
	_ context.Context,
	mutation AvailabilityMutation,
) (*AvailabilityResult, error) {
	fake.calls++
	fake.mutation = mutation
	return fake.result, fake.err
}

func TestRecordVersionAvailabilityUsesSystemAuthorityAndValidatesExactResult(t *testing.T) {
	fixture := newCatalogFixture(t)
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	fixture.reader.versions["v2"].SourceDigest = digestA
	fixture.reader.versions["v2"].BundleDigest = digestB
	fixture.reader.versions["v2"].AvailabilityStatus = DriverVersionAvailabilityPending

	admission, err := fixture.issuer.NewAdmission(
		authority.Allow(ActionRecordVersionAvailability, authority.ClassSystem),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &availabilityStoreFake{result: &AvailabilityResult{
		Driver: &Driver{WorkspaceKey: "TEST", DriverID: "driver-1", Revision: 8},
		Version: &DriverVersion{
			WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v2",
			SourceDigest: digestA, BundleDigest: digestB,
			ValidationStatus:     DriverVersionValidationPassed,
			AvailabilityStatus:   DriverVersionAvailabilityAvailable,
			AvailabilityAttempts: 1,
		},
		CommittedRevision: 8,
		SemanticImpact:    SemanticImpactVersionAvailabilityChanged,
	}}
	service := NewWithAvailability(fixture.reader, store, admission)
	command := AvailabilityCommand{
		WorkspaceKey: "TEST", RequestID: "availability-v2", ExpectedRevision: 7,
		DriverID: "driver-1", VersionID: "v2",
		SourceDigest: digestA, BundleDigest: digestB,
		Outcome: AvailabilityOutcomeAvailable,
	}
	result, err := service.RecordVersionAvailability(
		context.Background(),
		fixture.system(t, "TEST", ActionRecordVersionAvailability),
		command,
	)
	if err != nil {
		t.Fatalf("RecordVersionAvailability: %v", err)
	}
	if store.calls != 1 || store.mutation.AuditActor != "automation-1" ||
		store.mutation.Outcome != AvailabilityOutcomeAvailable {
		t.Fatalf("availability mutation = %+v, calls=%d", store.mutation, store.calls)
	}
	if result.Version.AvailabilityStatus != DriverVersionAvailabilityAvailable ||
		result.CommittedRevision != 8 {
		t.Fatalf("availability result = %+v", result)
	}
}
