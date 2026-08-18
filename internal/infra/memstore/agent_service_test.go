package memstore

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

func TestAgentServiceMemstoreLifecycle(t *testing.T) {
	s := New()
	ctx := t.Context()
	seedAgentServiceRefs(t, s)

	created, err := s.AgentServices().Create(ctx, agents.AgentServiceCreate{
		WorkspaceKey:    "WS",
		ServiceID:       "lead",
		Name:            "Lead",
		Kind:            agents.AgentKindLead,
		DesiredState:    agents.DesiredRunning,
		RoleName:        "lead",
		ProfileName:     "falcon",
		EventSources:    []string{"github:issues"},
		TriggerRefs:     []string{"binding-1"},
		PlacementPolicy: "local",
		MaxInstances:    2,
		RestartPolicy:   "always",
		Permissions:     []string{"task_run.create"},
		BudgetPolicy:    "daily:10",
		StateRef:        "state://lead",
		Metadata:        map[string]string{"tier": "gold"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ServiceID != "lead" || created.DesiredState != agents.DesiredRunning || created.MaxInstances != 2 {
		t.Fatalf("created = %#v, want running lead service", created)
	}

	created.EventSources[0] = "mutated"
	got, err := s.AgentServices().Get(ctx, "WS", "lead")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.EventSources[0] != "github:issues" || got.Metadata["tier"] != "gold" {
		t.Fatalf("got = %#v, want clone-isolated service", got)
	}

	if _, err := s.AgentServices().Create(ctx, agents.AgentServiceCreate{WorkspaceKey: "WS", ServiceID: "lead", Kind: agents.AgentKindLead, RoleName: "lead"}); !errors.Is(err, persistence.ErrAlreadyExists) {
		t.Fatalf("duplicate Create err = %v, want ErrAlreadyExists", err)
	}
	if _, err := s.AgentServices().Create(ctx, agents.AgentServiceCreate{WorkspaceKey: "WS", ServiceID: "bad", Kind: agents.AgentKind("bad"), RoleName: "lead"}); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("invalid kind err = %v, want ErrInvalid", err)
	}

	paused := agents.DesiredPaused
	leaseID := "lease-service-1"
	metadata := map[string]string{"tier": "silver"}
	updated, err := s.AgentServices().Update(ctx, "WS", "lead", agents.AgentServiceUpdate{
		DesiredState: &paused,
		LeaseID:      &leaseID,
		Metadata:     &metadata,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.DesiredState != agents.DesiredPaused || updated.LeaseID != "lease-service-1" || updated.Metadata["tier"] != "silver" {
		t.Fatalf("updated = %#v, want paused leased silver service", updated)
	}

	services, err := s.AgentServices().List(ctx, "WS", agents.AgentServiceFilter{Kind: agents.AgentKindLead, DesiredState: agents.DesiredPaused, RoleName: "lead", ProfileName: "falcon", Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(services) != 1 || services[0].ServiceID != "lead" {
		t.Fatalf("services = %#v, want lead", services)
	}
	services, err = s.AgentServices().List(ctx, "WS", agents.AgentServiceFilter{DesiredState: agents.DesiredRunning})
	if err != nil {
		t.Fatalf("List running: %v", err)
	}
	if len(services) != 0 {
		t.Fatalf("running services = %d, want 0", len(services))
	}

	if err := s.AgentServices().Delete(ctx, "WS", "lead"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Wave B semantics (mirrors fleet-db): DELETE archives. Get still returns
	// the record (attribution/history) with DeletedAt set; default List hides it.
	archived, err := s.AgentServices().Get(ctx, "WS", "lead")
	if err != nil {
		t.Fatalf("Get after delete err = %v, want archived record", err)
	}
	if archived.DeletedAt == nil {
		t.Fatalf("archived record DeletedAt = nil, want set")
	}
	listed, err := s.AgentServices().List(ctx, "WS", agents.AgentServiceFilter{})
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	for _, svc := range listed {
		if svc.ServiceID == "lead" {
			t.Fatalf("default List returned archived record")
		}
	}
	withDeleted, err := s.AgentServices().List(ctx, "WS", agents.AgentServiceFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List include-deleted: %v", err)
	}
	found := false
	for _, svc := range withDeleted {
		if svc.ServiceID == "lead" {
			found = true
		}
	}
	if !found {
		t.Fatalf("IncludeDeleted List missing archived record")
	}
}

func TestAgentServiceMemstoreScriptedBehaviorParity(t *testing.T) {
	s := New()
	ctx := t.Context()
	seedAgentServiceRefs(t, s)

	created, err := s.AgentServices().Create(ctx, agents.AgentServiceCreate{
		WorkspaceKey:    "WS",
		ServiceID:       "scripted",
		Name:            "Scripted",
		Kind:            agents.AgentKindEvent,
		DesiredState:    agents.DesiredRunning,
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
	})
	if err != nil {
		t.Fatalf("Create scripted service: %v", err)
	}
	if created.RoleName != "" || created.DriverID != "driver-1" || created.DriverVersionID != "version-1" {
		t.Fatalf("created behavior = role %q driver %q/%q, want driver-1/version-1", created.RoleName, created.DriverID, created.DriverVersionID)
	}

	invalidCreates := []struct {
		name            string
		roleName        string
		driverID        string
		driverVersionID string
		wantErr         error
	}{
		{name: "missing behavior", wantErr: persistence.ErrInvalid},
		{name: "mixed behavior", roleName: "lead", driverID: "driver-1", driverVersionID: "version-1", wantErr: persistence.ErrInvalid},
		{name: "partial driver", driverID: "driver-1", wantErr: persistence.ErrInvalid},
		{name: "missing driver", driverID: "missing", driverVersionID: "version-1", wantErr: persistence.ErrNotFound},
		{name: "missing version", driverID: "driver-1", driverVersionID: "missing", wantErr: persistence.ErrNotFound},
	}
	for i, tt := range invalidCreates {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.AgentServices().Create(ctx, agents.AgentServiceCreate{
				WorkspaceKey:    "WS",
				ServiceID:       "invalid-" + string(rune('a'+i)),
				Kind:            agents.AgentKindEvent,
				RoleName:        tt.roleName,
				DriverID:        tt.driverID,
				DriverVersionID: tt.driverVersionID,
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Create err = %v, want %v", err, tt.wantErr)
			}
		})
	}

	if _, err := s.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey:       "WS",
		VersionID:          "version-2",
		DriverID:           "driver-1",
		Version:            2,
		SourceDigest:       "sha256:source-2",
		BundleDigest:       "sha256:bundle-2",
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatalf("Create second driver version: %v", err)
	}
	version2 := "version-2"
	updated, err := s.AgentServices().Update(ctx, "WS", "scripted", agents.AgentServiceUpdate{DriverVersionID: &version2})
	if err != nil {
		t.Fatalf("Update scripted driver version: %v", err)
	}
	if updated.DriverID != "driver-1" || updated.DriverVersionID != "version-2" {
		t.Fatalf("updated driver = %q/%q, want driver-1/version-2", updated.DriverID, updated.DriverVersionID)
	}
}

func TestAgentServiceMemstoreReferenceValidation(t *testing.T) {
	s := New()
	ctx := t.Context()

	if _, err := s.AgentServices().Create(ctx, agents.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "lead",
		Kind:         agents.AgentKindLead,
		RoleName:     "lead",
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Create missing role err = %v, want ErrNotFound", err)
	}
	if _, err := s.Roles().Create(ctx, agents.RoleRecordCreate{WorkspaceKey: "WS", Name: "lead"}); err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if _, err := s.AgentServices().Create(ctx, agents.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "lead",
		Kind:         agents.AgentKindLead,
		RoleName:     "lead",
		ProfileName:  "falcon",
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Create missing profile err = %v, want ErrNotFound", err)
	}
	if _, err := s.WorkerProfiles().Create(ctx, execution.WorkerProfileCreate{WorkspaceKey: "WS", ProfileID: "falcon", Role: "lead"}); err != nil {
		t.Fatalf("Create worker profile: %v", err)
	}
	if _, err := s.AgentServices().Create(ctx, agents.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "lead",
		Kind:         agents.AgentKindLead,
		RoleName:     "lead",
		ProfileName:  "falcon",
		TriggerRefs:  []string{"binding-1"},
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Create missing trigger ref err = %v, want ErrNotFound", err)
	}

	if _, err := s.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    workflowcatalog.DriverOwnerSystem,
		Status:       workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey:       "WS",
		VersionID:          "version-1",
		DriverID:           "driver-1",
		Version:            1,
		SourceDigest:       "sha256:source",
		BundleDigest:       "sha256:bundle",
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.AgentServices().Create(ctx, agents.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "lead",
		Kind:         agents.AgentKindLead,
		RoleName:     "lead",
		ProfileName:  "falcon",
	}); err != nil {
		t.Fatalf("Create valid service: %v", err)
	}
	if _, err := s.AgentServices().Create(ctx, agents.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "other",
		Kind:         agents.AgentKindSupport,
		RoleName:     "lead",
		ProfileName:  "falcon",
	}); err != nil {
		t.Fatalf("Create other service: %v", err)
	}
	if err := s.Roles().Delete(ctx, "WS", "lead"); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Delete role referenced by service err = %v, want ErrInvalidTransition", err)
	}
	if err := s.WorkerProfiles().Delete(ctx, "WS", "falcon"); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Delete profile referenced by service err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, automation.TriggerBindingCreate{
		WorkspaceKey:         "WS",
		BindingID:            "binding-1",
		Name:                 "Epic runner",
		SourceKind:           "http",
		RouteKey:             "epics.runs.create",
		DriverID:             "driver-1",
		DriverVersionID:      "version-1",
		TargetAgentServiceID: "lead",
		Enabled:              true,
	}); err != nil {
		t.Fatalf("Create trigger binding target service: %v", err)
	}
	targeted, err := s.TriggerBindings().List(ctx, "WS", automation.TriggerBindingFilter{TargetAgentServiceID: "lead"})
	if err != nil {
		t.Fatalf("List targeted trigger bindings: %v", err)
	}
	if len(targeted) != 1 || targeted[0].BindingID != "binding-1" {
		t.Fatalf("targeted bindings = %+v, want binding-1", targeted)
	}
	triggerRefs := []string{"binding-1"}
	if _, err := s.AgentServices().Update(ctx, "WS", "lead", agents.AgentServiceUpdate{TriggerRefs: &triggerRefs}); err != nil {
		t.Fatalf("Update trigger refs: %v", err)
	}
	otherTarget := "other"
	if _, err := s.TriggerBindings().Update(ctx, "WS", "binding-1", automation.TriggerBindingUpdate{TargetAgentServiceID: &otherTarget}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Update binding target away from referencing service err = %v, want ErrInvalidTransition", err)
	}
	if err := s.AgentServices().Delete(ctx, "WS", "lead"); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Delete service targeted by binding err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, automation.TriggerBindingCreate{
		WorkspaceKey:         "WS",
		BindingID:            "binding-missing",
		Name:                 "Missing service",
		SourceKind:           "http",
		RouteKey:             "missing.service",
		DriverID:             "driver-1",
		DriverVersionID:      "version-1",
		TargetAgentServiceID: "missing-service",
		Enabled:              true,
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Create binding missing target service err = %v, want ErrNotFound", err)
	}
}

func seedAgentServiceRefs(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Roles().Create(ctx, agents.RoleRecordCreate{WorkspaceKey: "WS", Name: "lead"}); err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if _, err := s.WorkerProfiles().Create(ctx, execution.WorkerProfileCreate{WorkspaceKey: "WS", ProfileID: "falcon", Role: "lead"}); err != nil {
		t.Fatalf("Create worker profile: %v", err)
	}
	if _, err := s.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    workflowcatalog.DriverOwnerSystem,
		Status:       workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey:       "WS",
		VersionID:          "version-1",
		DriverID:           "driver-1",
		Version:            1,
		SourceDigest:       "sha256:source",
		BundleDigest:       "sha256:bundle",
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, automation.TriggerBindingCreate{
		WorkspaceKey:     "WS",
		BindingID:        "binding-1",
		Name:             "Epic runner",
		SourceKind:       "http",
		RouteKey:         "epics.runs.create",
		DriverID:         "driver-1",
		DriverVersionID:  "version-1",
		TargetEntrypoint: "run",
		Enabled:          true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}
}
