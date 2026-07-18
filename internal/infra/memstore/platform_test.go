package memstore

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestPlatformRegisteredEpicRunViaTriggerBinding(t *testing.T) {
	ctx := t.Context()
	s := New()

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
		SourceDigest:     "sha256:source-v1",
		BundleDigest:     "sha256:bundle-v1",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey:      "WS",
		BindingID:         "binding-1",
		Name:              "Epic runner",
		SourceKind:        "http",
		RouteKey:          "epics.runs.create",
		Method:            "POST",
		PathTemplate:      "/epics/{epic_id}/runs",
		DriverID:          "driver-1",
		DriverVersionID:   "version-1",
		TargetEntrypoint:  "run",
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyOneActivePerEpic,
		IdempotencyPolicy: "header:Idempotency-Key",
		AuthPolicy:        "workspace_user",
		Permissions:       []string{"driver_run.create"},
		Enabled:           true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        "version-2",
		DriverID:         "driver-1",
		Version:          2,
		SourceDigest:     "sha256:source-v2",
		BundleDigest:     "sha256:bundle-v2",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version 2: %v", err)
	}

	run, err := s.DriverRuns().CreateEpic(ctx, "WS", "WS-9", store.EpicRunCreate{
		RunID:          "run-epic-1",
		IdempotencyKey: "idem-epic-1",
		Payload:        json.RawMessage(`{"epicId":"wrong","requestedBy":"ui"}`),
	})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if run.DriverVersionID != "version-1" || run.EpicID != "WS-9" || string(run.Payload) != `{"epicId":"wrong","requestedBy":"ui"}` {
		t.Fatalf("registered epic run = %+v, want pinned version-1 and raw payload", run)
	}

	replay, err := s.DriverRuns().CreateEpic(ctx, "WS", "WS-9", store.EpicRunCreate{
		RunID:          "run-replay",
		IdempotencyKey: "idem-epic-1",
	})
	if err != nil {
		t.Fatalf("CreateEpic replay: %v", err)
	}
	if replay.RunID != "run-epic-1" {
		t.Fatalf("replay run_id = %q, want run-epic-1", replay.RunID)
	}

	active, err := s.DriverRuns().CreateEpic(ctx, "WS", "WS-9", store.EpicRunCreate{
		RunID:          "run-active",
		IdempotencyKey: "idem-epic-2",
	})
	if err != nil {
		t.Fatalf("CreateEpic active replay: %v", err)
	}
	if active.RunID != "run-epic-1" {
		t.Fatalf("active run_id = %q, want run-epic-1", active.RunID)
	}

	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey:    "WS",
		BindingID:       "binding-2",
		Name:            "Other route",
		SourceKind:      "http",
		RouteKey:        "epics.other.create",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		Enabled:         true,
	}); err != nil {
		t.Fatalf("Create second trigger binding: %v", err)
	}
	duplicateRoute := "epics.runs.create"
	if _, err := s.TriggerBindings().Update(ctx, "WS", "binding-2", store.TriggerBindingUpdate{RouteKey: &duplicateRoute}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("Update duplicate trigger binding route err = %v, want ErrAlreadyExists", err)
	}
	unchanged, err := s.TriggerBindings().Get(ctx, "WS", "binding-2")
	if err != nil {
		t.Fatalf("Get second trigger binding after failed route update: %v", err)
	}
	if unchanged.RouteKey != "epics.other.create" {
		t.Fatalf("failed route update mutated binding route = %q, want epics.other.create", unchanged.RouteKey)
	}

	missingVersion := "version-missing"
	if _, err := s.TriggerBindings().Update(ctx, "WS", "binding-1", store.TriggerBindingUpdate{DriverVersionID: &missingVersion}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update missing trigger binding version err = %v, want ErrNotFound", err)
	}
	pinned, err := s.TriggerBindings().Get(ctx, "WS", "binding-1")
	if err != nil {
		t.Fatalf("Get trigger binding after failed version update: %v", err)
	}
	if pinned.DriverVersionID != "version-1" {
		t.Fatalf("failed version update mutated driver_version_id = %q, want version-1", pinned.DriverVersionID)
	}

	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey:    "WS",
		BindingID:       "binding-duplicate-route",
		Name:            "Duplicate",
		SourceKind:      "http",
		RouteKey:        "epics.runs.create",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		Enabled:         true,
	}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("Create duplicate trigger binding err = %v, want ErrAlreadyExists", err)
	}
}

func TestDispatchTriggerRouteSupersedesQueuedRunsForSubject(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "pr-review", Name: "pr-review",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "v1", DriverID: "pr-review", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "binding-pr", Name: "pr", SourceKind: "github",
		RouteKey: "github.pull_request.synchronize", DriverID: "pr-review", DriverVersionID: "v1",
		TargetEntrypoint: "run", ConcurrencyPolicy: domain.TriggerBindingConcurrencyReplace, Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}

	dispatch := func(runID, idem, subject string) *domain.DriverRun {
		run, err := s.TriggerRoutes().DispatchTriggerRoute(ctx, "WS", "github.pull_request.synchronize", store.TriggerRouteDispatch{
			RunID: runID, IdempotencyKey: idem, EventType: "pull_request", SubjectRef: subject,
		})
		if err != nil {
			t.Fatalf("DispatchTriggerRoute %s: %v", runID, err)
		}
		return run
	}

	const subject = "acme/widgets#7"
	dispatch("run-1", "del-1", subject)
	dispatch("run-2", "del-2", subject)
	dispatch("run-3", "del-3", subject)

	statusOf := func(id string) domain.DriverRunStatus {
		r, err := s.DriverRuns().Get(ctx, "WS", id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		return r.Status
	}
	if statusOf("run-1") != domain.DriverRunCancelled || statusOf("run-2") != domain.DriverRunCancelled {
		t.Fatalf("older runs not superseded: run-1=%s run-2=%s", statusOf("run-1"), statusOf("run-2"))
	}
	if statusOf("run-3") != domain.DriverRunQueued {
		t.Fatalf("newest run-3 = %s, want queued", statusOf("run-3"))
	}

	// Different subject is untouched.
	dispatch("run-other", "del-9", "acme/widgets#99")
	if statusOf("run-3") != domain.DriverRunQueued || statusOf("run-other") != domain.DriverRunQueued {
		t.Fatalf("cross-subject supersede leaked: run-3=%s other=%s", statusOf("run-3"), statusOf("run-other"))
	}

	// Loser deliveries transition to superseded via their rendered subject
	// key (binding_id|subject_ref), keeping the audit trail consistent with
	// the cancelled runs; the winner's delivery stays dispatched.
	superseded, err := s.TriggerDeliveries().List(ctx, "WS", store.TriggerDeliveryFilter{Status: domain.TriggerDeliverySuperseded})
	if err != nil || len(superseded) != 2 {
		t.Fatalf("superseded deliveries = %d err=%v, want 2", len(superseded), err)
	}
	for _, delivery := range superseded {
		if delivery.SubjectKey != "binding-pr|"+subject || delivery.ErrorClass != "superseded" {
			t.Fatalf("superseded delivery = %+v, want subject key binding-pr|%s and error class superseded", delivery, subject)
		}
		if got := statusOf(delivery.DriverRunID); got != domain.DriverRunCancelled {
			t.Fatalf("superseded delivery run %s = %s, want cancelled", delivery.DriverRunID, got)
		}
	}
	dispatched, err := s.TriggerDeliveries().List(ctx, "WS", store.TriggerDeliveryFilter{Status: domain.TriggerDeliveryDispatched})
	if err != nil || len(dispatched) != 2 {
		t.Fatalf("dispatched deliveries = %d err=%v, want run-3 + run-other", len(dispatched), err)
	}
}

func TestDispatchTriggerRouteRedeliveryDoesNotSupersedeNewerRun(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "pr-review", Name: "pr-review",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "v1", DriverID: "pr-review", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "binding-pr", Name: "pr", SourceKind: "github",
		RouteKey: "github.pull_request.synchronize", DriverID: "pr-review", DriverVersionID: "v1",
		TargetEntrypoint: "run", ConcurrencyPolicy: domain.TriggerBindingConcurrencyReplace, Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}

	dispatch := func(runID, idem string) {
		if _, err := s.TriggerRoutes().DispatchTriggerRoute(ctx, "WS", "github.pull_request.synchronize", store.TriggerRouteDispatch{
			RunID: runID, IdempotencyKey: idem, EventType: "pull_request", SubjectRef: "acme/widgets#7",
		}); err != nil {
			t.Fatalf("DispatchTriggerRoute %s: %v", runID, err)
		}
	}
	dispatch("run-1", "del-1")
	dispatch("run-2", "del-2")
	dispatch("run-1", "del-1")

	run1, err := s.DriverRuns().Get(ctx, "WS", "run-1")
	if err != nil {
		t.Fatalf("Get run-1: %v", err)
	}
	run2, err := s.DriverRuns().Get(ctx, "WS", "run-2")
	if err != nil {
		t.Fatalf("Get run-2: %v", err)
	}
	if run1.Status != domain.DriverRunCancelled {
		t.Fatalf("redelivered run-1 = %s, want cancelled", run1.Status)
	}
	if run2.Status != domain.DriverRunQueued {
		t.Fatalf("redelivery cancelled newer run-2: status=%s, want queued", run2.Status)
	}
}

func TestDispatchTriggerRouteSupersedeCancelsOlderRunThatArrivesLate(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "pr-review", Name: "pr-review",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "v1", DriverID: "pr-review", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	binding, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "binding-pr", Name: "pr", SourceKind: "github",
		RouteKey: "github.pull_request.synchronize", DriverID: "pr-review", DriverVersionID: "v1",
		TargetEntrypoint: "run", ConcurrencyPolicy: domain.TriggerBindingConcurrencyReplace, Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}

	const subject = "acme/widgets#7"
	olderEvent, replay := s.events.create(dispatchTriggerEvent("WS", binding, store.TriggerRouteDispatch{
		RunID: "run-old", IdempotencyKey: "del-old", EventType: "pull_request", SubjectRef: subject,
	}, time.Now().Add(-time.Second)))
	if replay {
		t.Fatal("older event unexpectedly replayed")
	}
	if _, err := s.TriggerRoutes().DispatchTriggerRoute(ctx, "WS", "github.pull_request.synchronize", store.TriggerRouteDispatch{
		RunID: "run-new", IdempotencyKey: "del-new", EventType: "pull_request", SubjectRef: subject,
	}); err != nil {
		t.Fatalf("DispatchTriggerRoute run-new: %v", err)
	}
	oldRun, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "run-old", DriverID: "pr-review", DriverVersionID: "v1",
		Entrypoint: "run", SourceKind: "github", SourceRef: olderEvent.EventID, IdempotencyKey: "del-old",
	})
	if err != nil {
		t.Fatalf("Create late older driver run: %v", err)
	}
	subjectKey := renderTriggerSubjectKey(binding, olderEvent, nil)
	if err := s.deliveries.create(&domain.TriggerDelivery{
		WorkspaceKey:     "WS",
		DeliveryID:       "delivery-" + olderEvent.EventID,
		TriggerEventID:   olderEvent.EventID,
		TriggerBindingID: binding.BindingID,
		SubjectKey:       subjectKey,
		Status:           domain.TriggerDeliveryDispatched,
		DriverRunID:      oldRun.RunID,
		Attempt:          1,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}); err != nil {
		t.Fatalf("Create old delivery: %v", err)
	}
	if status := s.routes.applyTriggerReplacePolicy(ctx, "WS", binding, oldRun, subjectKey); status != domain.TriggerDeliverySuperseded {
		t.Fatalf("late older delivery status = %s, want superseded", status)
	}

	oldRun, err = s.DriverRuns().Get(ctx, "WS", "run-old")
	if err != nil {
		t.Fatalf("Get run-old: %v", err)
	}
	newRun, err := s.DriverRuns().Get(ctx, "WS", "run-new")
	if err != nil {
		t.Fatalf("Get run-new: %v", err)
	}
	if oldRun.Status != domain.DriverRunCancelled {
		t.Fatalf("late older run = %s, want cancelled", oldRun.Status)
	}
	if newRun.Status != domain.DriverRunQueued {
		t.Fatalf("newer run = %s, want queued", newRun.Status)
	}
}

func TestTaskRunClaimQueuedAssignsPlacementAndHonorsProfileCapacity(t *testing.T) {
	ctx := t.Context()
	s := New()
	if _, err := s.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    "WS",
		NodeID:          "node-1",
		RuntimeProvider: domain.RuntimeProviderLocal,
		Capabilities:    []string{"daytona", "git", "shell"},
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Minute,
	}); err != nil {
		t.Fatalf("Create node: %v", err)
	}
	if _, err := s.WorkerProfiles().Create(ctx, store.WorkerProfileCreate{
		WorkspaceKey:  "WS",
		ProfileID:     "falcon",
		Role:          "task",
		Backend:       "daytona",
		Capabilities:  []string{"git"},
		RuntimePolicy: map[string]string{"network": "restricted"},
		MaxParallel:   1,
	}); err != nil {
		t.Fatalf("Create worker profile: %v", err)
	}
	for _, id := range []string{"task-run-claim-1", "task-run-claim-2"} {
		if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
			WorkspaceKey:     "WS",
			TaskRunID:        id,
			TaskID:           "WS-1",
			WorkerProfileID:  "falcon",
			Status:           domain.TaskRunQueued,
			SandboxPlacement: domain.TaskRunPlacement{Provider: "daytona"},
		}); err != nil {
			t.Fatalf("Create task run %s: %v", id, err)
		}
	}

	claimed, err := s.TaskRuns().ClaimQueued(ctx, "WS", store.TaskRunClaim{
		TaskRunID:          "task-run-claim-1",
		NodeID:             "node-1",
		RunnerID:           "runner-1",
		LeaseID:            "lease-1",
		SupportedProviders: []string{"daytona"},
		Capabilities:       []string{"git", "shell"},
		WorkerProfileIDs:   []string{"falcon"},
		SandboxPlacement: domain.TaskRunPlacement{
			Provider:  "daytona",
			SandboxID: "sandbox-1",
			CWD:       "/workspace",
		},
	})
	if err != nil {
		t.Fatalf("ClaimQueued: %v", err)
	}
	if claimed.Status != domain.TaskRunRunning || claimed.NodeID != "node-1" || claimed.LeaseID != "lease-1" || claimed.FencingToken == 0 {
		t.Fatalf("claimed task run = %+v, want running owner", claimed)
	}
	if claimed.RunnerPlacement.Provider != "daemon" || claimed.RunnerPlacement.RunnerID != "runner-1" || claimed.SandboxPlacement.SandboxID != "sandbox-1" {
		t.Fatalf("claimed placement = %+v/%+v, want runner and sandbox placement", claimed.RunnerPlacement, claimed.SandboxPlacement)
	}
	listed, err := s.TaskRuns().List(ctx, "WS", store.TaskRunFilter{WorkerProfileID: "falcon", Status: domain.TaskRunRunning})
	if err != nil {
		t.Fatalf("List claimed task runs: %v", err)
	}
	if len(listed) != 1 || listed[0].TaskRunID != "task-run-claim-1" {
		t.Fatalf("listed claimed task runs = %+v, want task-run-claim-1", listed)
	}
	if _, err := s.TaskRuns().ClaimQueued(ctx, "WS", store.TaskRunClaim{
		TaskRunID:          "task-run-claim-2",
		NodeID:             "node-1",
		RunnerID:           "runner-2",
		LeaseID:            "lease-2",
		SupportedProviders: []string{"daytona"},
		Capabilities:       []string{"git"},
		WorkerProfileIDs:   []string{"falcon"},
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("capacity ClaimQueued err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.WorkerProfiles().Create(ctx, store.WorkerProfileCreate{
		WorkspaceKey:  "WS",
		ProfileID:     "browser",
		Role:          "task",
		Backend:       "flue-local",
		Capabilities:  []string{"browser"},
		RuntimePolicy: map[string]string{"network": "restricted"},
	}); err != nil {
		t.Fatalf("Create browser worker profile: %v", err)
	}
	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:     "WS",
		TaskRunID:        "task-run-browser",
		TaskID:           "WS-2",
		WorkerProfileID:  "browser",
		Status:           domain.TaskRunQueued,
		SandboxPlacement: domain.TaskRunPlacement{Provider: "flue-local"},
	}); err != nil {
		t.Fatalf("Create browser task run: %v", err)
	}
	if _, err := s.TaskRuns().ClaimQueued(ctx, "WS", store.TaskRunClaim{
		TaskRunID:          "task-run-browser",
		NodeID:             "node-1",
		RunnerID:           "runner-browser",
		LeaseID:            "lease-browser",
		SupportedProviders: []string{"flue-local"},
		Capabilities:       []string{"browser"},
		WorkerProfileIDs:   []string{"browser"},
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("lying capability ClaimQueued err = %v, want ErrInvalidTransition", err)
	}
}

func TestTaskRunClaimQueuedHonorsExplicitTargetNode(t *testing.T) {
	ctx := t.Context()
	s := New()
	for _, nodeID := range []string{"node-wrong", "node-target"} {
		if _, err := s.Nodes().Create(ctx, store.NodeCreate{
			WorkspaceKey: "WS", NodeID: nodeID, RuntimeProvider: domain.RuntimeProviderLocal,
			DrainState: domain.NodeDrainActive, TTL: time.Minute,
		}); err != nil {
			t.Fatalf("Create node %s: %v", nodeID, err)
		}
	}
	created, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-targeted", TaskID: "WS-TARGET",
		TargetNodeID: "node-target", Status: domain.TaskRunQueued,
	})
	if err != nil {
		t.Fatalf("Create targeted TaskRun: %v", err)
	}
	if created.NodeID != "" || created.TargetNodeID != "node-target" {
		t.Fatalf("queued TaskRun owner=%q target=%q", created.NodeID, created.TargetNodeID)
	}
	if _, err := s.TaskRuns().ClaimQueued(ctx, "WS", store.TaskRunClaim{
		TaskRunID: created.TaskRunID, NodeID: "node-wrong", LeaseID: "lease-wrong",
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("wrong-node claim error = %v, want invalid transition", err)
	}
	claimed, err := s.TaskRuns().ClaimQueued(ctx, "WS", store.TaskRunClaim{
		TaskRunID: created.TaskRunID, NodeID: "node-target", LeaseID: "lease-target",
	})
	if err != nil {
		t.Fatalf("target-node claim: %v", err)
	}
	if claimed.NodeID != "node-target" || claimed.TargetNodeID != "node-target" || claimed.Status != domain.TaskRunRunning {
		t.Fatalf("claimed TaskRun = %+v", claimed)
	}
}

func TestPlatformRecoverStaleDriverRunsFailsStaleRunsAndReleasesAdmission(t *testing.T) {
	ctx := t.Context()
	s := New()

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
	for _, run := range []store.DriverRunCreate{
		{WorkspaceKey: "WS", RunID: "run-stale", DriverID: "driver-1", DriverVersionID: "version-1", EpicID: "WS-STALE"},
		{WorkspaceKey: "WS", RunID: "run-fresh", DriverID: "driver-1", DriverVersionID: "version-1", EpicID: "WS-FRESH"},
	} {
		if _, err := s.DriverRuns().Create(ctx, run); err != nil {
			t.Fatalf("Create driver run %s: %v", run.RunID, err)
		}
		if _, err := s.DriverRuns().Claim(ctx, "WS", run.RunID, "node-1", run.RunID+"-lease"); err != nil {
			t.Fatalf("Claim driver run %s: %v", run.RunID, err)
		}
	}
	oldHeartbeat := time.Now().UTC().Add(-time.Hour)
	s.runs.mu.Lock()
	s.runs.items["WS"]["run-stale"].LastHeartbeat = oldHeartbeat
	s.runs.items["WS"]["run-stale"].UpdatedAt = oldHeartbeat
	s.runs.mu.Unlock()

	result, err := s.DriverRuns().RecoverStale(ctx, "WS", store.StaleDriverRunRecovery{
		StaleBefore: oldHeartbeat.Add(30 * time.Minute),
		Summary:     "executor heartbeat expired",
	})
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if result.Recovered != 1 || result.SkippedFresh != 1 || len(result.RecoveredRunIDs) != 1 || result.RecoveredRunIDs[0] != "run-stale" {
		t.Fatalf("recovery result = %+v, want one stale recovered and one fresh skipped", result)
	}
	staleRun, err := s.DriverRuns().Get(ctx, "WS", "run-stale")
	if err != nil {
		t.Fatalf("Get stale run: %v", err)
	}
	if staleRun.Status != domain.DriverRunFailed || staleRun.ErrorClass != "stale_driver_run" || staleRun.Summary != "executor heartbeat expired" || staleRun.FinishedAt == nil {
		t.Fatalf("stale run = %+v, want failed stale_driver_run", staleRun)
	}
	freshRun, err := s.DriverRuns().Get(ctx, "WS", "run-fresh")
	if err != nil {
		t.Fatalf("Get fresh run: %v", err)
	}
	if freshRun.Status != domain.DriverRunRunning {
		t.Fatalf("fresh run status = %s, want running", freshRun.Status)
	}
	next, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-after-stale",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		EpicID:          "WS-STALE",
	})
	if err != nil {
		t.Fatalf("Create driver run after stale recovery: %v", err)
	}
	if next.RunID != "run-after-stale" {
		t.Fatalf("post-recovery run_id = %q, want run-after-stale", next.RunID)
	}
}

func TestPlatformDriverRunAndTaskRunLifecycle(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS",
		VersionID:    "version-missing-driver",
		DriverID:     "driver-missing",
		Version:      1,
		SourceDigest: "sha256:source",
		BundleDigest: "sha256:bundle",
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create version without driver err = %v, want ErrNotFound", err)
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

	payload := json.RawMessage(`{"epicId":"WS-1"}`)
	run, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-1",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		EpicID:          "WS-1",
		IdempotencyKey:  "idem-1",
		Payload:         payload,
	})
	if err != nil {
		t.Fatalf("Create driver run: %v", err)
	}
	payload[11] = 'X'
	if run.Status != domain.DriverRunQueued || string(run.Payload) != `{"epicId":"WS-1"}` {
		t.Fatalf("created run = %+v, want queued clone with original payload", run)
	}

	replay, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-replay",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		EpicID:          "WS-1",
		IdempotencyKey:  "idem-1",
	})
	if err != nil {
		t.Fatalf("Create idempotent replay driver run: %v", err)
	}
	if replay.RunID != "run-1" {
		t.Fatalf("replay run_id = %q, want run-1", replay.RunID)
	}

	active, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-same-epic",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		EpicID:          "WS-1",
		IdempotencyKey:  "idem-2",
	})
	if err != nil {
		t.Fatalf("Create active epic driver run: %v", err)
	}
	if active.RunID != "run-1" {
		t.Fatalf("active epic run_id = %q, want run-1", active.RunID)
	}

	queued, err := s.DriverRuns().List(ctx, "WS", store.DriverRunFilter{Status: domain.DriverRunQueued})
	if err != nil {
		t.Fatalf("List queued driver runs: %v", err)
	}
	if len(queued) != 1 || queued[0].RunID != "run-1" {
		t.Fatalf("queued runs = %+v, want run-1", queued)
	}

	claimed, err := s.DriverRuns().Claim(ctx, "WS", "run-1", "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim driver run: %v", err)
	}
	if claimed.Status != domain.DriverRunRunning || claimed.NodeID != "node-1" || claimed.FencingToken <= 0 {
		t.Fatalf("claimed = %+v, want running node-1 with fencing token", claimed)
	}
	if _, err := s.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-1",
		DriverRunID:  "run-1",
		StepKind:     "task_run",
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}); err != nil {
		t.Fatalf("Create driver step: %v", err)
	}
	if _, err := s.DriverRuns().Claim(ctx, "WS", "run-1", "node-2", "lease-2"); !errors.Is(err, domain.ErrAlreadyClaimed) {
		t.Fatalf("second Claim err = %v, want ErrAlreadyClaimed", err)
	}
	if _, err := s.DriverRuns().Heartbeat(ctx, "WS", "run-1", "node-2", "lease-1", claimed.FencingToken); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Heartbeat wrong owner err = %v, want ErrNotOwner", err)
	}
	if _, err := s.DriverRuns().Heartbeat(ctx, "WS", "run-1", "node-1", "stale-lease", claimed.FencingToken); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Heartbeat stale lease err = %v, want ErrNotOwner", err)
	}
	if _, err := s.DriverRuns().Finish(ctx, "WS", "run-1", store.DriverRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: claimed.FencingToken + 1,
		Status:       domain.DriverRunCompleted,
	}); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Finish stale fence err = %v, want ErrNotOwner", err)
	}

	finished, err := s.DriverRuns().Finish(ctx, "WS", "run-1", store.DriverRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: claimed.FencingToken,
		Status:       domain.DriverRunCompleted,
		Summary:      "done",
	})
	if err != nil {
		t.Fatalf("Finish driver run: %v", err)
	}
	if finished.Status != domain.DriverRunCompleted || finished.FinishedAt == nil {
		t.Fatalf("finished = %+v, want completed with finished_at", finished)
	}

	next, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-after-finish",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		EpicID:          "WS-1",
		IdempotencyKey:  "idem-3",
	})
	if err != nil {
		t.Fatalf("Create driver run after finish: %v", err)
	}
	if next.RunID != "run-after-finish" {
		t.Fatalf("post-finish run_id = %q, want run-after-finish", next.RunID)
	}
	if _, err := s.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-other",
		DriverRunID:  "run-after-finish",
		StepKind:     "task_run",
	}); err != nil {
		t.Fatalf("Create other driver step: %v", err)
	}
	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-missing-step",
		DriverRunID:  "run-1",
		DriverStepID: "missing-step",
		TaskID:       "WS-1",
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create task run missing step err = %v, want ErrNotFound", err)
	}
	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-mismatched-step",
		DriverRunID:  "run-1",
		DriverStepID: "step-other",
		TaskID:       "WS-1",
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Create task run mismatched step err = %v, want ErrInvalidTransition", err)
	}

	taskRun, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:    "WS",
		TaskRunID:       "task-run-1",
		DriverRunID:     "run-1",
		DriverStepID:    "step-1",
		TaskID:          "WS-1",
		ProviderProfile: "codex-default",
		Status:          domain.TaskRunRunning,
		NodeID:          "node-1",
		LeaseID:         "lease-1",
		RuntimeMetadata: map[string]string{"container": "abc"},
	})
	if err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	if taskRun.Status != domain.TaskRunRunning || taskRun.StartedAt.IsZero() {
		t.Fatalf("taskRun = %+v, want running with started_at", taskRun)
	}

	children, err := s.TaskRuns().List(ctx, "WS", store.TaskRunFilter{DriverRunID: "run-1"})
	if err != nil {
		t.Fatalf("List child task runs: %v", err)
	}
	if len(children) != 1 || children[0].TaskRunID != "task-run-1" {
		t.Fatalf("children = %+v, want task-run-1", children)
	}
	stepChildren, err := s.TaskRuns().List(ctx, "WS", store.TaskRunFilter{DriverStepID: "step-1"})
	if err != nil {
		t.Fatalf("List driver step task runs: %v", err)
	}
	if len(stepChildren) != 1 || stepChildren[0].DriverStepID != "step-1" {
		t.Fatalf("step children = %+v, want step-linked task-run-1", stepChildren)
	}

	heartbeat, err := s.TaskRuns().Heartbeat(ctx, "WS", "task-run-1", store.TaskRunHeartbeat{
		NodeID:          "node-1",
		LeaseID:         "lease-1",
		FencingToken:    taskRun.FencingToken,
		LogsRef:         "logs://task-run-1",
		ArtifactsRef:    "artifacts://task-run-1",
		RuntimeMetadata: map[string]string{"phase": "running"},
	})
	if err != nil {
		t.Fatalf("Heartbeat task run: %v", err)
	}
	if heartbeat.LastHeartbeat.IsZero() || heartbeat.LogsRef != "logs://task-run-1" || heartbeat.ArtifactsRef != "artifacts://task-run-1" {
		t.Fatalf("heartbeat = %+v, want refs and last heartbeat", heartbeat)
	}
	if heartbeat.RuntimeMetadata["container"] != "abc" || heartbeat.RuntimeMetadata["phase"] != "running" {
		t.Fatalf("heartbeat metadata = %+v, want merged metadata", heartbeat.RuntimeMetadata)
	}
	if _, err := s.TaskRuns().Heartbeat(ctx, "WS", "task-run-1", store.TaskRunHeartbeat{
		NodeID:       "node-2",
		LeaseID:      "lease-1",
		FencingToken: taskRun.FencingToken,
	}); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Heartbeat task run wrong owner err = %v, want ErrNotOwner", err)
	}
	logTimestamp := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	firstAppend := store.TaskRunLogAppend{
		RequestID:    "task-run-log-1",
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: taskRun.FencingToken,
		Stream:       "stderr",
		Text:         "warning\n",
		Timestamp:    logTimestamp,
	}
	firstLog, err := s.TaskRuns().AppendLog(ctx, "WS", "task-run-1", firstAppend)
	if err != nil {
		t.Fatalf("AppendLog first: %v", err)
	}
	if firstLog.Sequence != 1 || firstLog.Stream != "stderr" || firstLog.Text != "warning\n" {
		t.Fatalf("first log = %+v, want stderr warning", firstLog)
	}
	replayedLog, err := s.TaskRuns().AppendLog(ctx, "WS", "task-run-1", firstAppend)
	if err != nil || replayedLog.Sequence != firstLog.Sequence {
		t.Fatalf("AppendLog replay = %+v err=%v, want committed sequence %d", replayedLog, err, firstLog.Sequence)
	}
	conflictingAppend := firstAppend
	conflictingAppend.Text = "different\n"
	if _, err := s.TaskRuns().AppendLog(ctx, "WS", "task-run-1", conflictingAppend); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("AppendLog conflicting replay err = %v, want ErrConflict", err)
	}
	secondLog, err := s.TaskRuns().AppendLog(ctx, "WS", "task-run-1", store.TaskRunLogAppend{
		RequestID:    "task-run-log-2",
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: taskRun.FencingToken,
		Text:         "default stream\n",
		Timestamp:    logTimestamp.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("AppendLog second: %v", err)
	}
	if secondLog.Sequence != 2 || secondLog.Stream != "stdout" {
		t.Fatalf("second log = %+v, want default stdout", secondLog)
	}
	logs, err := s.TaskRuns().ListLogs(ctx, "WS", "task-run-1", store.TaskRunLogFilter{AfterSequence: 1})
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].Sequence != 2 || logs[0].Text != "default stream\n" {
		t.Fatalf("logs after sequence 1 = %+v, want second log", logs)
	}
	if _, err := s.TaskRuns().AppendLog(ctx, "WS", "task-run-1", store.TaskRunLogAppend{
		RequestID:    "task-run-log-wrong-owner",
		NodeID:       "node-2",
		LeaseID:      "lease-1",
		FencingToken: taskRun.FencingToken,
		Text:         "bad owner\n",
		Timestamp:    logTimestamp.Add(2 * time.Second),
	}); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("AppendLog wrong owner err = %v, want ErrNotOwner", err)
	}

	exitCode := 0
	if _, err := s.TaskRuns().Finish(ctx, "WS", "task-run-1", store.TaskRunFinish{
		NodeID:       "node-2",
		LeaseID:      "lease-2",
		FencingToken: taskRun.FencingToken,
		Status:       domain.TaskRunCompleted,
		ExitCode:     &exitCode,
	}); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Finish task run wrong owner err = %v, want ErrNotOwner", err)
	}
	completedTask, err := s.TaskRuns().Finish(ctx, "WS", "task-run-1", store.TaskRunFinish{
		NodeID:           "node-1",
		LeaseID:          "lease-1",
		FencingToken:     taskRun.FencingToken,
		Status:           domain.TaskRunCompleted,
		ExitCode:         &exitCode,
		LogsRef:          "logs://task-run-1",
		ArtifactsRef:     "artifacts://task-run-1",
		InputTokens:      11,
		OutputTokens:     7,
		CacheReadTokens:  5,
		CacheWriteTokens: 3,
		EstimatedCostUSD: 0.125,
		RuntimeMetadata:  map[string]string{"container": "abc", "duration_ms": "42"},
	})
	if err != nil {
		t.Fatalf("Finish task run: %v", err)
	}
	if replayedAfterFinish, err := s.TaskRuns().AppendLog(ctx, "WS", "task-run-1", firstAppend); err != nil || replayedAfterFinish.Sequence != firstLog.Sequence {
		t.Fatalf("AppendLog replay after finish = %+v err=%v, want committed sequence %d", replayedAfterFinish, err, firstLog.Sequence)
	}
	if _, err := s.TaskRuns().AppendLog(ctx, "WS", "task-run-1", store.TaskRunLogAppend{
		RequestID:    "task-run-log-late",
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: taskRun.FencingToken,
		Text:         "late\n",
		Timestamp:    logTimestamp.Add(3 * time.Second),
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("AppendLog terminal err = %v, want ErrInvalidTransition", err)
	}
	if completedTask.Status != domain.TaskRunCompleted || completedTask.ExitCode == nil || *completedTask.ExitCode != 0 || completedTask.FinishedAt == nil {
		t.Fatalf("completed task = %+v, want completed with exit code and finished_at", completedTask)
	}
	if completedTask.InputTokens != 11 || completedTask.OutputTokens != 7 || completedTask.CacheReadTokens != 5 || completedTask.CacheWriteTokens != 3 || completedTask.EstimatedCostUSD != 0.125 {
		t.Fatalf("completed task usage = %+v, want persisted usage", completedTask)
	}

	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-complete-1",
		TaskID:       "WS-2",
		Status:       domain.TaskRunRunning,
		NodeID:       "node-1",
		LeaseID:      "lease-2",
	}); err != nil {
		t.Fatalf("Create complete task run: %v", err)
	}
	completeRun, err := s.TaskRuns().Get(ctx, "WS", "task-run-complete-1")
	if err != nil {
		t.Fatalf("Get complete task run: %v", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", store.TaskRunComplete{
		Status: domain.TaskRunCompleted,
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Complete task run blank completion ID err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", store.TaskRunComplete{
		CompletionID: "completion-non-terminal",
		NodeID:       "node-1",
		LeaseID:      "lease-2",
		FencingToken: completeRun.FencingToken,
		Status:       domain.TaskRunRunning,
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Complete task run non-terminal err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", store.TaskRunComplete{
		CompletionID: "completion-negative-usage",
		NodeID:       "node-1",
		LeaseID:      "lease-2",
		FencingToken: completeRun.FencingToken,
		Status:       domain.TaskRunCompleted,
		InputTokens:  -1,
	}); err == nil {
		t.Fatalf("Complete task run negative usage err = nil, want validation error")
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", store.TaskRunComplete{
		CompletionID: "completion-close-failed",
		NodeID:       "node-1",
		LeaseID:      "lease-2",
		FencingToken: completeRun.FencingToken,
		Status:       domain.TaskRunFailed,
		CloseTask:    true,
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Complete task run close failed err = %v, want ErrInvalidTransition", err)
	}
	completedByComplete, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", store.TaskRunComplete{
		CompletionID:     "completion-1",
		NodeID:           "node-1",
		LeaseID:          "lease-2",
		FencingToken:     completeRun.FencingToken,
		Status:           domain.TaskRunCompleted,
		ExitCode:         &exitCode,
		LogsRef:          "logs://task-run-complete-1",
		ArtifactsRef:     "artifacts://task-run-complete-1",
		InputTokens:      23,
		OutputTokens:     19,
		CacheReadTokens:  13,
		CacheWriteTokens: 2,
		EstimatedCostUSD: 0.25,
	})
	if err != nil {
		t.Fatalf("Complete task run: %v", err)
	}
	if completedByComplete.InputTokens != 23 || completedByComplete.OutputTokens != 19 || completedByComplete.CacheReadTokens != 13 || completedByComplete.CacheWriteTokens != 2 || completedByComplete.EstimatedCostUSD != 0.25 {
		t.Fatalf("completed-by-complete usage = %+v, want persisted usage", completedByComplete)
	}
	replayed, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", store.TaskRunComplete{
		CompletionID: "completion-1",
		NodeID:       "stale-node",
		LeaseID:      "stale-lease",
		FencingToken: completeRun.FencingToken + 1,
		Status:       domain.TaskRunCompleted,
	})
	if err != nil {
		t.Fatalf("Replay complete task run: %v", err)
	}
	if replayed.TaskRunID != completedByComplete.TaskRunID || replayed.InputTokens != completedByComplete.InputTokens {
		t.Fatalf("replayed completion = %+v, want original completed task run", replayed)
	}
	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-complete-other",
		TaskID:       "WS-3",
		Status:       domain.TaskRunRunning,
	}); err != nil {
		t.Fatalf("Create other complete task run: %v", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-other", store.TaskRunComplete{
		CompletionID: "completion-1",
		Status:       domain.TaskRunCompleted,
	}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("Complete task run completion-id collision err = %v, want ErrAlreadyExists", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", store.TaskRunComplete{
		CompletionID: "completion-terminal-new",
		Status:       domain.TaskRunCompleted,
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Complete task run new completion after terminal err = %v, want ErrInvalidTransition", err)
	}
}

func TestPlatformTaskRunCompleteRequiresReadyArtifacts(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-artifact-1",
		TaskID:       "WS-4",
		Status:       domain.TaskRunRunning,
		NodeID:       "node-1",
		LeaseID:      "lease-1",
	}); err != nil {
		t.Fatalf("Create artifact task run: %v", err)
	}
	running, err := s.TaskRuns().Get(ctx, "WS", "task-run-artifact-1")
	if err != nil {
		t.Fatalf("Get artifact task run: %v", err)
	}
	base := store.TaskRunComplete{
		CompletionID:        "completion-artifact-missing",
		NodeID:              "node-1",
		LeaseID:             "lease-1",
		FencingToken:        running.FencingToken,
		Status:              domain.TaskRunCompleted,
		RequiredArtifactIDs: []string{"missing-artifact"},
		RequireArtifacts:    true,
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Complete missing artifact err = %v, want ErrNotFound", err)
	}

	if _, err := s.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey:  "WS",
		ArtifactID:    "artifact-wrong-owner",
		TaskID:        "WS-4",
		OwnerType:     "task_run",
		OwnerID:       "other-task-run",
		Type:          "patch",
		URI:           "artifact://wrong-owner",
		ContentHash:   "sha256:wrong-owner",
		DurableStatus: "finalized",
	}); err != nil {
		t.Fatalf("Create wrong-owner artifact: %v", err)
	}
	base.CompletionID = "completion-artifact-wrong-owner"
	base.RequiredArtifactIDs = []string{"artifact-wrong-owner"}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Complete wrong-owner artifact err = %v, want ErrInvalidTransition", err)
	}

	if _, err := s.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey:  "WS",
		ArtifactID:    "artifact-wrong-task",
		TaskID:        "WS-other",
		OwnerType:     "task_run",
		OwnerID:       "task-run-artifact-1",
		Type:          "patch",
		URI:           "artifact://wrong-task",
		ContentHash:   "sha256:wrong-task",
		DurableStatus: "finalized",
	}); err != nil {
		t.Fatalf("Create wrong-task artifact: %v", err)
	}
	base.CompletionID = "completion-artifact-wrong-task"
	base.RequiredArtifactIDs = []string{"artifact-wrong-task"}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Complete wrong-task artifact err = %v, want ErrInvalidTransition", err)
	}

	if _, err := s.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey:  "WS",
		ArtifactID:    "artifact-empty-hash",
		TaskID:        "WS-4",
		OwnerType:     "task_run",
		OwnerID:       "task-run-artifact-1",
		Type:          "patch",
		DurableStatus: "finalized",
	}); err != nil {
		t.Fatalf("Create empty-hash artifact: %v", err)
	}
	base.CompletionID = "completion-artifact-empty-hash"
	base.RequiredArtifactIDs = []string{"artifact-empty-hash"}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Complete empty-hash artifact err = %v, want ErrInvalidTransition", err)
	}

	if _, err := s.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey:  "WS",
		ArtifactID:    "artifact-declared",
		TaskID:        "WS-4",
		OwnerType:     "task_run",
		OwnerID:       "task-run-artifact-1",
		Type:          "patch",
		ContentHash:   "sha256:declared",
		DurableStatus: "declared",
	}); err != nil {
		t.Fatalf("Create declared artifact: %v", err)
	}
	base.CompletionID = "completion-artifact-declared"
	base.RequiredArtifactIDs = []string{"artifact-declared"}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Complete declared artifact err = %v, want ErrInvalidTransition", err)
	}

	uri := "artifact://artifact-declared"
	if _, err := s.Artifacts().Finalize(ctx, "WS", "artifact-declared", store.ArtifactFinalize{
		URI: &uri,
	}); err != nil {
		t.Fatalf("Finalize artifact: %v", err)
	}
	base.CompletionID = "completion-artifact-finalized"
	completed, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base)
	if err != nil {
		t.Fatalf("Complete finalized artifact: %v", err)
	}
	if completed.Status != domain.TaskRunCompleted {
		t.Fatalf("completed task status = %q, want completed", completed.Status)
	}
}

func TestPlatformTaskRunCompleteRequiresCloudSafeArtifactsForCloudSandbox(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-cloud-artifact",
		TaskID:       "WS-4",
		Status:       domain.TaskRunRunning,
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		SandboxPlacement: domain.TaskRunPlacement{
			Provider:  "daytona",
			SandboxID: "sandbox-1",
		},
	}); err != nil {
		t.Fatalf("Create cloud artifact task run: %v", err)
	}
	if _, err := s.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey:  "WS",
		ArtifactID:    "artifact-cloud-file",
		TaskID:        "WS-4",
		OwnerType:     "task_run",
		OwnerID:       "task-run-cloud-artifact",
		Type:          "patch",
		URI:           "file:///tmp/patch.diff",
		ContentHash:   "sha256:cloud-file",
		DurableStatus: "finalized",
	}); err != nil {
		t.Fatalf("Create cloud file artifact: %v", err)
	}
	running, err := s.TaskRuns().Get(ctx, "WS", "task-run-cloud-artifact")
	if err != nil {
		t.Fatalf("Get cloud artifact task run: %v", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-cloud-artifact", store.TaskRunComplete{
		CompletionID:     "completion-cloud-local-ref",
		NodeID:           "node-1",
		LeaseID:          "lease-1",
		FencingToken:     running.FencingToken,
		Status:           domain.TaskRunCompleted,
		ArtifactsRef:     "file:///tmp/result.tar",
		RequireArtifacts: true,
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Complete cloud file artifacts_ref err = %v, want ErrInvalidTransition", err)
	}
	exitCode := 0
	if _, err := s.TaskRuns().Finish(ctx, "WS", "task-run-cloud-artifact", store.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: running.FencingToken,
		Status:       domain.TaskRunCompleted,
		ExitCode:     &exitCode,
		ArtifactsRef: "file:///tmp/result.tar",
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Finish cloud file artifacts_ref err = %v, want ErrInvalidTransition", err)
	}
	complete := store.TaskRunComplete{
		CompletionID:        "completion-cloud-file",
		NodeID:              "node-1",
		LeaseID:             "lease-1",
		FencingToken:        running.FencingToken,
		Status:              domain.TaskRunCompleted,
		RequiredArtifactIDs: []string{"artifact-cloud-file"},
		RequireArtifacts:    true,
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-cloud-artifact", complete); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Complete cloud file artifact err = %v, want ErrInvalidTransition", err)
	}

	uri := "artifact://artifact-cloud-file"
	if _, err := s.Artifacts().Update(ctx, "WS", "artifact-cloud-file", store.ArtifactUpdate{URI: &uri}); err != nil {
		t.Fatalf("Update cloud artifact URI: %v", err)
	}
	complete.CompletionID = "completion-cloud-safe"
	completed, err := s.TaskRuns().Complete(ctx, "WS", "task-run-cloud-artifact", complete)
	if err != nil {
		t.Fatalf("Complete cloud safe artifact: %v", err)
	}
	if completed.Status != domain.TaskRunCompleted {
		t.Fatalf("completed task status = %q, want completed", completed.Status)
	}
}

func TestPlatformTaskRunAllowsLocalArtifactsForFlueLocal(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-flue-local-complete",
		TaskID:       "WS-5",
		Status:       domain.TaskRunRunning,
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		SandboxPlacement: domain.TaskRunPlacement{
			Provider: "flue-local",
		},
	}); err != nil {
		t.Fatalf("Create flue-local completion task run: %v", err)
	}
	running, err := s.TaskRuns().Get(ctx, "WS", "task-run-flue-local-complete")
	if err != nil {
		t.Fatalf("Get flue-local completion task run: %v", err)
	}
	completed, err := s.TaskRuns().Complete(ctx, "WS", "task-run-flue-local-complete", store.TaskRunComplete{
		CompletionID:     "completion-flue-local-file-ref",
		NodeID:           "node-1",
		LeaseID:          "lease-1",
		FencingToken:     running.FencingToken,
		Status:           domain.TaskRunCompleted,
		ArtifactsRef:     "file:///tmp/result.tar",
		RequireArtifacts: true,
	})
	if err != nil {
		t.Fatalf("Complete flue-local file ref: %v", err)
	}
	if completed.Status != domain.TaskRunCompleted || completed.ArtifactsRef != "file:///tmp/result.tar" {
		t.Fatalf("completed task run = %+v, want local artifact ref", completed)
	}

	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-flue-local-finish",
		TaskID:       "WS-6",
		Status:       domain.TaskRunRunning,
		NodeID:       "node-1",
		LeaseID:      "lease-2",
		SandboxPlacement: domain.TaskRunPlacement{
			Provider: "flue-local",
		},
	}); err != nil {
		t.Fatalf("Create flue-local finish task run: %v", err)
	}
	running, err = s.TaskRuns().Get(ctx, "WS", "task-run-flue-local-finish")
	if err != nil {
		t.Fatalf("Get flue-local finish task run: %v", err)
	}
	exitCode := 0
	finished, err := s.TaskRuns().Finish(ctx, "WS", "task-run-flue-local-finish", store.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-2",
		FencingToken: running.FencingToken,
		Status:       domain.TaskRunCompleted,
		ExitCode:     &exitCode,
		ArtifactsRef: "file:///tmp/result.tar",
	})
	if err != nil {
		t.Fatalf("Finish flue-local file ref: %v", err)
	}
	if finished.Status != domain.TaskRunCompleted || finished.ArtifactsRef != "file:///tmp/result.tar" {
		t.Fatalf("finished task run = %+v, want local artifact ref", finished)
	}
}

func TestPlatformTaskRunCompleteRequireArtifactsNeedsEvidence(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-artifact-ref",
		TaskID:       "WS-5",
		Status:       domain.TaskRunRunning,
	}); err != nil {
		t.Fatalf("Create artifact-ref task run: %v", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-ref", store.TaskRunComplete{
		CompletionID:     "completion-artifacts-required",
		Status:           domain.TaskRunCompleted,
		RequireArtifacts: true,
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Complete require artifacts without evidence err = %v, want ErrInvalidTransition", err)
	}
	completed, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-ref", store.TaskRunComplete{
		CompletionID:     "completion-artifacts-ref",
		Status:           domain.TaskRunCompleted,
		ArtifactsRef:     "artifact://bundle",
		RequireArtifacts: true,
	})
	if err != nil {
		t.Fatalf("Complete with artifacts ref: %v", err)
	}
	if completed.ArtifactsRef != "artifact://bundle" {
		t.Fatalf("completed artifacts ref = %q, want artifact://bundle", completed.ArtifactsRef)
	}
}

func TestPlatformDriverStepLifecycle(t *testing.T) {
	ctx := t.Context()
	s := New()

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
	if _, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-1",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
	}); err != nil {
		t.Fatalf("Create driver run: %v", err)
	}

	step, err := s.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-1",
		DriverRunID:  "run-1",
		StepKind:     "custom_vendor_gate",
		Status:       domain.DriverStepRunning,
		InputRef:     "artifact://input-1",
		ExternalRef:  "github://check/123",
	})
	if err != nil {
		t.Fatalf("Create driver step: %v", err)
	}
	if step.StepKind != "custom_vendor_gate" || step.Status != domain.DriverStepRunning || step.StartedAt.IsZero() {
		t.Fatalf("created step = %+v, want running custom step with started_at", step)
	}

	if _, err := s.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-missing-run",
		DriverRunID:  "missing-run",
		StepKind:     "gate",
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create missing-run step err = %v, want ErrNotFound", err)
	}
	if _, err := s.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-1",
		DriverRunID:  "run-1",
		StepKind:     "gate",
	}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("Create duplicate step err = %v, want ErrAlreadyExists", err)
	}

	steps, err := s.DriverSteps().ListForRun(ctx, "WS", "run-1", store.DriverStepFilter{StepKind: "custom_vendor_gate", Status: domain.DriverStepRunning})
	if err != nil {
		t.Fatalf("ListForRun driver steps: %v", err)
	}
	if len(steps) != 1 || steps[0].StepID != "step-1" {
		t.Fatalf("steps = %+v, want step-1", steps)
	}

	claimed, err := s.DriverRuns().Claim(ctx, "WS", "run-1", "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim driver run: %v", err)
	}
	if _, err := s.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-no-owner",
		DriverRunID:  "run-1",
		StepKind:     "gate",
	}); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Create claimed step without owner err = %v, want ErrNotOwner", err)
	}
	if _, err := s.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-wrong-owner",
		DriverRunID:  "run-1",
		StepKind:     "gate",
		NodeID:       "node-2",
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Create claimed step with wrong owner err = %v, want ErrNotOwner", err)
	}
	if _, err := s.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-owned",
		DriverRunID:  "run-1",
		StepKind:     "gate",
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}); err != nil {
		t.Fatalf("Create claimed step with owner: %v", err)
	}
	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-1",
		DriverRunID:  "run-1",
		DriverStepID: "step-1",
		TaskID:       "WS-1",
		Status:       domain.TaskRunRunning,
		NodeID:       "task-node-1",
		LeaseID:      "task-lease-1",
	}); err != nil {
		t.Fatalf("Create task run for driver step: %v", err)
	}

	completed := domain.DriverStepCompleted
	taskRunID := "task-run-1"
	actionID := "action-1"
	outputRef := "artifact://output-1"
	missingTaskRunID := "missing-task-run"
	if _, err := s.DriverSteps().Update(ctx, "WS", "step-1", store.DriverStepUpdate{
		Status:       &completed,
		TaskRunID:    &missingTaskRunID,
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update claimed step missing task run err = %v, want ErrNotFound", err)
	}
	if _, err := s.DriverSteps().Update(ctx, "WS", "step-1", store.DriverStepUpdate{
		Status:         &completed,
		TaskRunID:      &taskRunID,
		ActionLedgerID: &actionID,
		OutputRef:      &outputRef,
	}); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Update claimed step without owner err = %v, want ErrNotOwner", err)
	}
	if _, err := s.DriverSteps().Update(ctx, "WS", "step-1", store.DriverStepUpdate{
		Status:         &completed,
		TaskRunID:      &taskRunID,
		ActionLedgerID: &actionID,
		OutputRef:      &outputRef,
		NodeID:         claimed.NodeID,
		LeaseID:        claimed.LeaseID,
		FencingToken:   claimed.FencingToken + 1,
	}); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Update claimed step with stale fence err = %v, want ErrNotOwner", err)
	}
	updated, err := s.DriverSteps().Update(ctx, "WS", "step-1", store.DriverStepUpdate{
		Status:         &completed,
		TaskRunID:      &taskRunID,
		ActionLedgerID: &actionID,
		OutputRef:      &outputRef,
		NodeID:         claimed.NodeID,
		LeaseID:        claimed.LeaseID,
		FencingToken:   claimed.FencingToken,
	})
	if err != nil {
		t.Fatalf("Update completed driver step: %v", err)
	}
	if updated.Status != domain.DriverStepCompleted || updated.TaskRunID != "task-run-1" || updated.ActionLedgerID != "action-1" || updated.OutputRef != "artifact://output-1" || updated.EndedAt == nil {
		t.Fatalf("updated step = %+v, want completed refs and ended_at", updated)
	}

	steps, err = s.DriverSteps().List(ctx, "WS", store.DriverStepFilter{TaskRunID: "task-run-1", ActionLedgerID: "action-1", Status: domain.DriverStepCompleted})
	if err != nil {
		t.Fatalf("List completed driver steps: %v", err)
	}
	if len(steps) != 1 || steps[0].StepID != "step-1" {
		t.Fatalf("completed steps = %+v, want step-1", steps)
	}

	queued := domain.DriverStepQueued
	retry, err := s.DriverSteps().Update(ctx, "WS", "step-1", store.DriverStepUpdate{
		Status:         &queued,
		ClearStartedAt: true,
		ClearEndedAt:   true,
		NodeID:         claimed.NodeID,
		LeaseID:        claimed.LeaseID,
		FencingToken:   claimed.FencingToken,
	})
	if err != nil {
		t.Fatalf("Update retry driver step: %v", err)
	}
	if retry.Status != domain.DriverStepQueued || !retry.StartedAt.IsZero() || retry.EndedAt != nil {
		t.Fatalf("retry step = %+v, want queued with cleared timestamps", retry)
	}

	if _, err := s.DriverRuns().Finish(ctx, "WS", "run-1", store.DriverRunFinish{
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
		Status:       domain.DriverRunCompleted,
	}); err != nil {
		t.Fatalf("Finish driver run: %v", err)
	}
	if _, err := s.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-terminal",
		DriverRunID:  "run-1",
		StepKind:     "gate",
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Create terminal claimed step err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.DriverSteps().Update(ctx, "WS", "step-1", store.DriverStepUpdate{
		Status:       &completed,
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Update terminal claimed step err = %v, want ErrInvalidTransition", err)
	}
}

func TestPlatformRecoverStaleTaskRunsFailsStaleRunsAndSteps(t *testing.T) {
	ctx := t.Context()
	s := New()

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
	if _, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-1",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
	}); err != nil {
		t.Fatalf("Create driver run: %v", err)
	}
	if _, err := s.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-stale",
		DriverRunID:  "run-1",
		StepKind:     "run_agent",
		Status:       domain.DriverStepRunning,
	}); err != nil {
		t.Fatalf("Create stale driver step: %v", err)
	}
	if _, err := s.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-fresh",
		DriverRunID:  "run-1",
		StepKind:     "run_agent",
		Status:       domain.DriverStepRunning,
	}); err != nil {
		t.Fatalf("Create fresh driver step: %v", err)
	}
	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-stale",
		DriverRunID:  "run-1",
		DriverStepID: "step-stale",
		TaskID:       "WS-1",
		Status:       domain.TaskRunRunning,
		NodeID:       "task-node-1",
		LeaseID:      "task-lease-1",
	}); err != nil {
		t.Fatalf("Create stale task run: %v", err)
	}
	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-fresh",
		DriverRunID:  "run-1",
		DriverStepID: "step-fresh",
		TaskID:       "WS-2",
		Status:       domain.TaskRunRunning,
		NodeID:       "task-node-2",
		LeaseID:      "task-lease-2",
	}); err != nil {
		t.Fatalf("Create fresh task run: %v", err)
	}

	now := time.Now().UTC()
	staleBefore := now.Add(-5 * time.Minute)
	s.taskRuns.mu.Lock()
	s.taskRuns.items["WS"]["task-run-stale"].LastHeartbeat = now.Add(-10 * time.Minute)
	s.taskRuns.items["WS"]["task-run-stale"].UpdatedAt = now.Add(-10 * time.Minute)
	s.taskRuns.items["WS"]["task-run-fresh"].LastHeartbeat = now
	s.taskRuns.items["WS"]["task-run-fresh"].UpdatedAt = now
	s.taskRuns.mu.Unlock()

	result, err := s.DriverRuns().RecoverStaleTaskRuns(ctx, "WS", "run-1", store.StaleTaskRunRecovery{
		StaleBefore:  staleBefore,
		ErrorMessage: "operator recovery",
	})
	if err != nil {
		t.Fatalf("RecoverStaleTaskRuns: %v", err)
	}
	if result.Recovered != 1 || result.Released != 0 || result.SkippedFresh != 1 || len(result.RecoveredTaskRunIDs) != 1 || result.RecoveredTaskRunIDs[0] != "task-run-stale" {
		t.Fatalf("recovery result = %+v, want one recovered stale task and one fresh skip", result)
	}
	staleRun, err := s.TaskRuns().Get(ctx, "WS", "task-run-stale")
	if err != nil {
		t.Fatalf("Get stale task run: %v", err)
	}
	if staleRun.Status != domain.TaskRunFailed || staleRun.ErrorClass != "stale_task_run" || staleRun.ErrorMessage != "operator recovery" || staleRun.FinishedAt == nil {
		t.Fatalf("stale task run = %+v, want failed stale_task_run", staleRun)
	}
	freshRun, err := s.TaskRuns().Get(ctx, "WS", "task-run-fresh")
	if err != nil {
		t.Fatalf("Get fresh task run: %v", err)
	}
	if freshRun.Status != domain.TaskRunRunning {
		t.Fatalf("fresh task run status = %s, want running", freshRun.Status)
	}
	staleStep, err := s.DriverSteps().Get(ctx, "WS", "step-stale")
	if err != nil {
		t.Fatalf("Get stale step: %v", err)
	}
	if staleStep.Status != domain.DriverStepFailed || staleStep.EndedAt == nil {
		t.Fatalf("stale step = %+v, want failed ended step", staleStep)
	}
	freshStep, err := s.DriverSteps().Get(ctx, "WS", "step-fresh")
	if err != nil {
		t.Fatalf("Get fresh step: %v", err)
	}
	if freshStep.Status != domain.DriverStepRunning {
		t.Fatalf("fresh step status = %s, want running", freshStep.Status)
	}
	if _, err := s.DriverRuns().RecoverStaleTaskRuns(ctx, "WS", "missing-run", store.StaleTaskRunRecovery{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Recover missing run err = %v, want ErrNotFound", err)
	}
}
