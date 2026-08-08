package memstore

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestDispatchTriggerRouteStampsExternalProvenance verifies the in-memory
// dispatch path mirrors fleet-db's webhook ingest stamping: every event it
// persists carries origin=external at hop depth 0, regardless of caller input
// (the dispatch input has no provenance fields to forge).
func TestDispatchTriggerRouteStampsExternalProvenance(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "driver-1", Name: "build",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "version-1", DriverID: "driver-1", Version: 1,
		SourceDigest: "sha256:source-v1", BundleDigest: "sha256:bundle-v1",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "binding-1", Name: "Build webhook",
		SourceKind: "webhook", RouteKey: "webhook.build",
		DriverID: "driver-1", DriverVersionID: "version-1", Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}

	run, err := s.TriggerRoutes().DispatchTriggerRoute(ctx, "WS", "webhook.build", store.TriggerRouteDispatch{
		IdempotencyKey: "idem-1",
		EventType:      "push",
		SubjectRef:     "repo:example/main",
	})
	if err != nil {
		t.Fatalf("DispatchTriggerRoute: %v", err)
	}
	event, err := s.TriggerEvents().Get(ctx, "WS", run.SourceRef)
	if err != nil {
		t.Fatalf("Get trigger event %q: %v", run.SourceRef, err)
	}
	if event.Origin != automation.EventOriginExternal || event.HopDepth != 0 {
		t.Fatalf("dispatched provenance = %s/%d, want external/0", event.Origin, event.HopDepth)
	}
}
