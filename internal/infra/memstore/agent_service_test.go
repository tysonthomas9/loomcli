package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestAgentServiceMemstoreScriptedContractAndArchiveFilter(t *testing.T) {
	st := New()
	ctx := t.Context()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "scout-driver", Name: "scout",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "scout-v1", DriverID: "scout-driver", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "scout-v2", DriverID: "scout-driver", Version: 2,
		SourceDigest: "sha256:source-2", BundleDigest: "sha256:bundle-2",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create second driver version: %v", err)
	}

	created, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: "scout", Name: "Scout",
		TriggerKind: domain.AgentServiceTriggerKindCron, DesiredState: domain.AgentServiceDesiredRunning,
		DriverID: "scout-driver", DriverVersionID: "scout-v1", CreatedBy: "system",
	})
	if err != nil {
		t.Fatalf("create scripted service: %v", err)
	}
	if created.RoleName != "" || created.DriverID != "scout-driver" || created.DriverVersionID != "scout-v1" || created.CreatedBy != "system" {
		t.Fatalf("created = %#v, want role-less scripted behavior and creator", created)
	}

	emptyRole := ""
	version2 := "scout-v2"
	updated, err := st.AgentServices().Update(ctx, "WS", "scout", store.AgentServiceUpdate{RoleName: &emptyRole, DriverVersionID: &version2})
	if err != nil {
		t.Fatalf("role-less update: %v", err)
	}
	if updated.DriverVersionID != "scout-v2" {
		t.Fatalf("updated DriverVersionID = %q, want scout-v2", updated.DriverVersionID)
	}
	if err := st.AgentServices().Delete(ctx, "WS", "scout"); err != nil {
		t.Fatalf("archive service: %v", err)
	}
	archived, err := st.AgentServices().Get(ctx, "WS", "scout")
	if err != nil {
		t.Fatalf("get archived service: %v", err)
	}
	if archived.DeletedAt == nil || time.Since(*archived.DeletedAt) > time.Minute {
		t.Fatalf("DeletedAt = %v, want a recent archive timestamp", archived.DeletedAt)
	}
	visible, err := st.AgentServices().List(ctx, "WS", store.AgentServiceFilter{})
	if err != nil {
		t.Fatalf("list visible services: %v", err)
	}
	if len(visible) != 0 {
		t.Fatalf("visible services = %#v, want archived service hidden", visible)
	}
	all, err := st.AgentServices().List(ctx, "WS", store.AgentServiceFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("list archived services: %v", err)
	}
	if len(all) != 1 || all[0].DeletedAt == nil {
		t.Fatalf("all services = %#v, want archived scout", all)
	}
}

func TestAgentServiceMemstoreRequiresExactlyOneBehaviorReference(t *testing.T) {
	st := New()
	ctx := t.Context()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "triage"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{WorkspaceKey: "WS", DriverID: "driver-1", Name: "driver-1"}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "version-1", DriverID: "driver-1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	cases := []struct {
		name string
		in   store.AgentServiceCreate
	}{
		{name: "missing", in: store.AgentServiceCreate{WorkspaceKey: "WS", ServiceID: "missing", TriggerKind: domain.AgentServiceTriggerKindEvent}},
		{name: "mixed", in: store.AgentServiceCreate{WorkspaceKey: "WS", ServiceID: "mixed", TriggerKind: domain.AgentServiceTriggerKindEvent, RoleName: "triage", DriverID: "driver-1", DriverVersionID: "version-1"}},
		{name: "partial driver", in: store.AgentServiceCreate{WorkspaceKey: "WS", ServiceID: "partial", TriggerKind: domain.AgentServiceTriggerKindEvent, DriverID: "driver-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := st.AgentServices().Create(ctx, tc.in); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("Create err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestAgentServiceMemstoreLifecycle(t *testing.T) {
	s := New()
	ctx := t.Context()
	seedAgentServiceRefs(t, s)

	created, err := s.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey:    "WS",
		ServiceID:       "lead",
		Name:            "Lead",
		TriggerKind:     domain.AgentServiceTriggerKindLead,
		DesiredState:    domain.AgentServiceDesiredRunning,
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
	if created.ServiceID != "lead" || created.DesiredState != domain.AgentServiceDesiredRunning || created.MaxInstances != 2 {
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

	if _, err := s.AgentServices().Create(ctx, store.AgentServiceCreate{WorkspaceKey: "WS", ServiceID: "lead", TriggerKind: domain.AgentServiceTriggerKindLead, RoleName: "lead"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate Create err = %v, want ErrAlreadyExists", err)
	}
	if _, err := s.AgentServices().Create(ctx, store.AgentServiceCreate{WorkspaceKey: "WS", ServiceID: "bad", TriggerKind: domain.AgentServiceTriggerKind("bad"), RoleName: "lead"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid trigger kind err = %v, want ErrInvalid", err)
	}

	paused := domain.AgentServiceDesiredPaused
	leaseID := "lease-service-1"
	metadata := map[string]string{"tier": "silver"}
	updated, err := s.AgentServices().Update(ctx, "WS", "lead", store.AgentServiceUpdate{
		DesiredState: &paused,
		LeaseID:      &leaseID,
		Metadata:     &metadata,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.DesiredState != domain.AgentServiceDesiredPaused || updated.LeaseID != "lease-service-1" || updated.Metadata["tier"] != "silver" {
		t.Fatalf("updated = %#v, want paused leased silver service", updated)
	}

	services, err := s.AgentServices().List(ctx, "WS", store.AgentServiceFilter{TriggerKind: domain.AgentServiceTriggerKindLead, DesiredState: domain.AgentServiceDesiredPaused, RoleName: "lead", ProfileName: "falcon", Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(services) != 1 || services[0].ServiceID != "lead" {
		t.Fatalf("services = %#v, want lead", services)
	}
	services, err = s.AgentServices().List(ctx, "WS", store.AgentServiceFilter{DesiredState: domain.AgentServiceDesiredRunning})
	if err != nil {
		t.Fatalf("List running: %v", err)
	}
	if len(services) != 0 {
		t.Fatalf("running services = %d, want 0", len(services))
	}

	if err := s.AgentServices().Delete(ctx, "WS", "lead"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if archived, err := s.AgentServices().Get(ctx, "WS", "lead"); err != nil || archived.DeletedAt == nil {
		t.Fatalf("Get after delete = %#v err=%v, want archived record", archived, err)
	}
	if err := s.AgentServices().Delete(ctx, "WS", "lead"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("duplicate Delete err = %v, want ErrNotFound", err)
	}
}

func TestAgentServiceMemstoreReferenceValidation(t *testing.T) {
	s := New()
	ctx := t.Context()

	if _, err := s.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "lead",
		TriggerKind:  domain.AgentServiceTriggerKindLead,
		RoleName:     "lead",
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create missing role err = %v, want ErrNotFound", err)
	}
	if _, err := s.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "lead"}); err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if _, err := s.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "lead",
		TriggerKind:  domain.AgentServiceTriggerKindLead,
		RoleName:     "lead",
		ProfileName:  "falcon",
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create missing profile err = %v, want ErrNotFound", err)
	}
	if _, err := s.WorkerProfiles().Create(ctx, store.WorkerProfileCreate{WorkspaceKey: "WS", ProfileID: "falcon", Role: "lead"}); err != nil {
		t.Fatalf("Create worker profile: %v", err)
	}
	if _, err := s.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "lead",
		TriggerKind:  domain.AgentServiceTriggerKindLead,
		RoleName:     "lead",
		ProfileName:  "falcon",
		TriggerRefs:  []string{"binding-1"},
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create missing trigger ref err = %v, want ErrNotFound", err)
	}

	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    domain.DriverOwnerSystem,
		Status:       domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        "version-1",
		DriverID:         "driver-1",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "lead",
		TriggerKind:  domain.AgentServiceTriggerKindLead,
		RoleName:     "lead",
		ProfileName:  "falcon",
	}); err != nil {
		t.Fatalf("Create valid service: %v", err)
	}
	if _, err := s.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "other",
		TriggerKind:  domain.AgentServiceTriggerKindEvent,
		RoleName:     "lead",
		ProfileName:  "falcon",
	}); err != nil {
		t.Fatalf("Create other service: %v", err)
	}
	if err := s.Roles().Delete(ctx, "WS", "lead"); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Delete role referenced by service err = %v, want ErrInvalidTransition", err)
	}
	if err := s.WorkerProfiles().Delete(ctx, "WS", "falcon"); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Delete profile referenced by service err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
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
	targeted, err := s.TriggerBindings().List(ctx, "WS", store.TriggerBindingFilter{TargetAgentServiceID: "lead"})
	if err != nil {
		t.Fatalf("List targeted trigger bindings: %v", err)
	}
	if len(targeted) != 1 || targeted[0].BindingID != "binding-1" {
		t.Fatalf("targeted bindings = %+v, want binding-1", targeted)
	}
	triggerRefs := []string{"binding-1"}
	if _, err := s.AgentServices().Update(ctx, "WS", "lead", store.AgentServiceUpdate{TriggerRefs: &triggerRefs}); err != nil {
		t.Fatalf("Update trigger refs: %v", err)
	}
	otherTarget := "other"
	if _, err := s.TriggerBindings().Update(ctx, "WS", "binding-1", store.TriggerBindingUpdate{TargetAgentServiceID: &otherTarget}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Update binding target away from referencing service err = %v, want ErrInvalidTransition", err)
	}
	if err := s.AgentServices().Delete(ctx, "WS", "lead"); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Delete service targeted by binding err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey:         "WS",
		BindingID:            "binding-missing",
		Name:                 "Missing service",
		SourceKind:           "http",
		RouteKey:             "missing.service",
		DriverID:             "driver-1",
		DriverVersionID:      "version-1",
		TargetAgentServiceID: "missing-service",
		Enabled:              true,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create binding missing target service err = %v, want ErrNotFound", err)
	}
}

func seedAgentServiceRefs(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "lead"}); err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if _, err := s.WorkerProfiles().Create(ctx, store.WorkerProfileCreate{WorkspaceKey: "WS", ProfileID: "falcon", Role: "lead"}); err != nil {
		t.Fatalf("Create worker profile: %v", err)
	}
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    domain.DriverOwnerSystem,
		Status:       domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        "version-1",
		DriverID:         "driver-1",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
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
