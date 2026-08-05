package serve

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestNewRunOutcomeRuntimeRegistrationDoesNotRequireAutomationPublisher(t *testing.T) {
	st := memstore.New()
	registration, err := NewRunOutcomeRuntimeRegistration(
		st.DriverRuns(), st.Awaits(), st.TriggerEvents(), st.Workspaces(), nil, "WS",
	)
	if err != nil {
		t.Fatal(err)
	}
	if registration.Component == nil || string(registration.Component.ID()) != systemeventing.DriverRunOutcomeComponentID {
		t.Fatalf("registration component = %#v", registration.Component)
	}
	if !registration.Policy.Immediate || registration.Policy.Cadence != runOutcomeReconcileCadence {
		t.Fatalf("registration policy = %+v", registration.Policy)
	}
}

func TestRunOutcomeRuntimePublishesOpaqueRunIDThroughAutomationAdmission(t *testing.T) {
	st := memstore.New()
	ctx := t.Context()
	const workspace = "WS"
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: workspace, Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: workspace, DriverID: "driver", Name: "driver",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: workspace, DriverID: "driver", VersionID: "v1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatal(err)
	}
	runID := " run/1 " + strings.Repeat("x", 300)
	created, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: workspace, RunID: runID, DriverID: "driver", DriverVersionID: "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, workspace, created.RunID, "node", "lease")
	if err != nil {
		t.Fatal(err)
	}
	final, err := st.DriverRuns().Finish(ctx, workspace, created.RunID, store.DriverRunFinish{
		NodeID: "node", LeaseID: "lease", FencingToken: claimed.FencingToken,
		Status: domain.DriverRunFailed, Summary: "failed", ErrorClass: "runtime",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got automation.AdmitEventCommand
	workflow, err := systemeventing.New(
		runOutcomeAuthorityProviderFunc(func(context.Context, systemeventing.VerifiedSource) (authority.SystemAuthority, error) {
			return authority.SystemAuthority{}, nil
		}),
		runOutcomeAdmissionFunc(func(_ context.Context, _ automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			got = command
			return &automation.AdmissionResult{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	emitter, err := systemeventing.BindRunOutcomeEmitter(workflow)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := newAutomationDriverRunOutcomePublisher(emitter)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := NewRunOutcomeRuntimeRegistration(
		st.DriverRuns(), st.Awaits(), st.TriggerEvents(), st.Workspaces(), publisher, workspace,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.Component.RunOnce(ctx, final.FinishedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if got.SourceRef != runID || got.SubjectRef != runID {
		t.Fatalf("opaque source/subject = %q / %q", got.SourceRef, got.SubjectRef)
	}
	if got.SourceEventID == "" || len(got.SourceEventID) > 86 || !safePrintableASCII(got.SourceEventID) {
		t.Fatalf("bounded event id = %q (%d bytes)", got.SourceEventID, len(got.SourceEventID))
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["runId"] != runID || payload["status"] != string(domain.DriverRunFailed) {
		t.Fatalf("admission payload = %s", got.Payload)
	}
}

func safePrintableASCII(value string) bool {
	for index := range len(value) {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}
