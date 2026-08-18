package memstore

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

func TestDriverGenericLifecycleFieldsRetired(t *testing.T) {
	ctx := t.Context()
	s := New()
	approvalKey := workflowcatalog.ApprovedVersionMetadataKey("version-1")

	if _, err := s.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: "WS", DriverID: "forged", Name: "forged",
		Metadata: map[string]string{approvalKey: "sha256:source"},
	}); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("generic create approval metadata err = %v, want ErrInvalid", err)
	}
	if _, err := s.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: "WS", DriverID: "driver-1", Name: "driver", Status: workflowcatalog.DriverStatusDraft,
		TrustLevel: workflowcatalog.DriverTrustUntrusted, Metadata: map[string]string{"team": "runtime"},
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey: "WS", DriverID: "driver-1", VersionID: "version-1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	active := workflowcatalog.DriverStatusActive
	renamed := "must-not-partially-apply"
	if _, err := s.Drivers().Update(ctx, "WS", "driver-1", workflowcatalog.DriverUpdate{
		Name: &renamed, Status: &active,
	}); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("generic active status err = %v, want ErrInvalid", err)
	}
	driver, err := s.Drivers().Get(ctx, "WS", "driver-1")
	if err != nil {
		t.Fatal(err)
	}
	if driver.Name != "driver" || driver.Status != workflowcatalog.DriverStatusDraft || driver.ActiveVersionID != "" {
		t.Fatalf("rejected activation partially mutated driver: %+v", driver)
	}

	forged := map[string]string{"team": "runtime", approvalKey: "forged"}
	if _, err := s.Drivers().Update(ctx, "WS", "driver-1", workflowcatalog.DriverUpdate{
		Metadata: &forged,
	}); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("generic approval metadata err = %v, want ErrInvalid", err)
	}
	if _, err := s.ApproveDriverVersionForTest(ctx, "WS", "driver-1", "version-1"); err != nil {
		t.Fatalf("typed approve: %v", err)
	}
	if _, err := s.ActivateDriverVersionForTest(ctx, "WS", "driver-1", "version-1"); err != nil {
		t.Fatalf("typed activate: %v", err)
	}

	replacement := map[string]string{"team": "catalog"}
	disabled := workflowcatalog.DriverStatusDisabled
	trusted := workflowcatalog.DriverTrustTrusted
	driver, err = s.Drivers().Update(ctx, "WS", "driver-1", workflowcatalog.DriverUpdate{
		Status: &disabled, TrustLevel: &trusted, Metadata: &replacement,
	})
	if err != nil {
		t.Fatalf("legitimate generic administration: %v", err)
	}
	if driver.Status != disabled || driver.TrustLevel != trusted ||
		driver.Metadata["team"] != "catalog" ||
		driver.Metadata[approvalKey] != "sha256:source" ||
		driver.ActiveVersionID != "version-1" {
		t.Fatalf("generic administration lost lifecycle state: %+v", driver)
	}
	if _, err := s.UnapproveDriverVersionForTest(ctx, "WS", "driver-1", "version-1"); err != nil {
		t.Fatalf("typed unapprove: %v", err)
	}
	driver, err = s.Drivers().Get(ctx, "WS", "driver-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := driver.Metadata[approvalKey]; present {
		t.Fatalf("typed unapprove left lifecycle marker: %+v", driver.Metadata)
	}
}

func TestPlatformRegisteredEpicRunViaTriggerBinding(t *testing.T) {
	ctx := t.Context()
	s := New()

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
		SourceDigest:       "sha256:source-v1",
		BundleDigest:       "sha256:bundle-v1",
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, automation.TriggerBindingCreate{
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
		ConcurrencyPolicy: automation.ConcurrencyOneActivePerEpic,
		IdempotencyPolicy: "header:Idempotency-Key",
		AuthPolicy:        "workspace_user",
		Permissions:       []string{"driver_run.create"},
		Enabled:           true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey:       "WS",
		VersionID:          "version-2",
		DriverID:           "driver-1",
		Version:            2,
		SourceDigest:       "sha256:source-v2",
		BundleDigest:       "sha256:bundle-v2",
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatalf("Create driver version 2: %v", err)
	}

	run, err := s.DriverRuns().CreateEpic(ctx, "WS", "WS-9", execution.EpicRunCreate{
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

	replay, err := s.DriverRuns().CreateEpic(ctx, "WS", "WS-9", execution.EpicRunCreate{
		RunID:          "run-replay",
		IdempotencyKey: "idem-epic-1",
	})
	if err != nil {
		t.Fatalf("CreateEpic replay: %v", err)
	}
	if replay.RunID != "run-epic-1" {
		t.Fatalf("replay run_id = %q, want run-epic-1", replay.RunID)
	}

	active, err := s.DriverRuns().CreateEpic(ctx, "WS", "WS-9", execution.EpicRunCreate{
		RunID:          "run-active",
		IdempotencyKey: "idem-epic-2",
	})
	if err != nil {
		t.Fatalf("CreateEpic active replay: %v", err)
	}
	if active.RunID != "run-epic-1" {
		t.Fatalf("active run_id = %q, want run-epic-1", active.RunID)
	}

	if _, err := s.TriggerBindings().Create(ctx, automation.TriggerBindingCreate{
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
	if _, err := s.TriggerBindings().Update(ctx, "WS", "binding-2", automation.TriggerBindingUpdate{RouteKey: &duplicateRoute}); !errors.Is(err, persistence.ErrAlreadyExists) {
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
	if _, err := s.TriggerBindings().Update(ctx, "WS", "binding-1", automation.TriggerBindingUpdate{DriverVersionID: &missingVersion}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Update missing trigger binding version err = %v, want ErrNotFound", err)
	}
	pinned, err := s.TriggerBindings().Get(ctx, "WS", "binding-1")
	if err != nil {
		t.Fatalf("Get trigger binding after failed version update: %v", err)
	}
	if pinned.DriverVersionID != "version-1" {
		t.Fatalf("failed version update mutated driver_version_id = %q, want version-1", pinned.DriverVersionID)
	}

	if _, err := s.TriggerBindings().Create(ctx, automation.TriggerBindingCreate{
		WorkspaceKey:    "WS",
		BindingID:       "binding-duplicate-route",
		Name:            "Duplicate",
		SourceKind:      "http",
		RouteKey:        "epics.runs.create",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		Enabled:         true,
	}); !errors.Is(err, persistence.ErrAlreadyExists) {
		t.Fatalf("Create duplicate trigger binding err = %v, want ErrAlreadyExists", err)
	}
}

func TestTaskRunClaimQueuedAssignsPlacementAndHonorsProfileCapacity(t *testing.T) {
	ctx := t.Context()
	s := New()
	if _, err := s.Nodes().Create(ctx, execution.NodeCreate{
		WorkspaceKey:    "WS",
		NodeID:          "node-1",
		RuntimeProvider: execution.RuntimeProviderLocal,
		Capabilities:    []string{"daytona", "git", "shell"},
		DrainState:      execution.WorkerNodeActive,
		TTL:             time.Minute,
	}); err != nil {
		t.Fatalf("Create node: %v", err)
	}
	if _, err := s.WorkerProfiles().Create(ctx, execution.WorkerProfileCreate{
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
		if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
			WorkspaceKey:     "WS",
			TaskRunID:        id,
			TaskID:           "WS-1",
			WorkerProfileID:  "falcon",
			Status:           execution.TaskRunRecordQueued,
			SandboxPlacement: execution.TaskRunPlacementRecord{Provider: "daytona"},
		}); err != nil {
			t.Fatalf("Create task run %s: %v", id, err)
		}
	}

	claimed, err := s.TaskRuns().ClaimQueued(ctx, "WS", execution.TaskRunClaim{
		TaskRunID:          "task-run-claim-1",
		NodeID:             "node-1",
		RunnerID:           "runner-1",
		LeaseID:            "lease-1",
		SupportedProviders: []string{"daytona"},
		Capabilities:       []string{"git", "shell"},
		WorkerProfileIDs:   []string{"falcon"},
		SandboxPlacement: execution.TaskRunPlacementRecord{
			Provider:  "daytona",
			SandboxID: "sandbox-1",
			CWD:       "/workspace",
		},
	})
	if err != nil {
		t.Fatalf("ClaimQueued: %v", err)
	}
	if claimed.Status != execution.TaskRunRecordRunning || claimed.NodeID != "node-1" || claimed.LeaseID != "lease-1" || claimed.FencingToken == 0 {
		t.Fatalf("claimed task run = %+v, want running owner", claimed)
	}
	if claimed.RunnerPlacement.Provider != "daemon" || claimed.RunnerPlacement.RunnerID != "runner-1" || claimed.SandboxPlacement.SandboxID != "sandbox-1" {
		t.Fatalf("claimed placement = %+v/%+v, want runner and sandbox placement", claimed.RunnerPlacement, claimed.SandboxPlacement)
	}
	listed, err := s.TaskRuns().List(ctx, "WS", execution.TaskRunFilter{WorkerProfileID: "falcon", Status: execution.TaskRunRecordRunning})
	if err != nil {
		t.Fatalf("List claimed task runs: %v", err)
	}
	if len(listed) != 1 || listed[0].TaskRunID != "task-run-claim-1" {
		t.Fatalf("listed claimed task runs = %+v, want task-run-claim-1", listed)
	}
	if _, err := s.TaskRuns().ClaimQueued(ctx, "WS", execution.TaskRunClaim{
		TaskRunID:          "task-run-claim-2",
		NodeID:             "node-1",
		RunnerID:           "runner-2",
		LeaseID:            "lease-2",
		SupportedProviders: []string{"daytona"},
		Capabilities:       []string{"git"},
		WorkerProfileIDs:   []string{"falcon"},
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("capacity ClaimQueued err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.WorkerProfiles().Create(ctx, execution.WorkerProfileCreate{
		WorkspaceKey:  "WS",
		ProfileID:     "browser",
		Role:          "task",
		Backend:       "flue-local",
		Capabilities:  []string{"browser"},
		RuntimePolicy: map[string]string{"network": "restricted"},
	}); err != nil {
		t.Fatalf("Create browser worker profile: %v", err)
	}
	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey:     "WS",
		TaskRunID:        "task-run-browser",
		TaskID:           "WS-2",
		WorkerProfileID:  "browser",
		Status:           execution.TaskRunRecordQueued,
		SandboxPlacement: execution.TaskRunPlacementRecord{Provider: "flue-local"},
	}); err != nil {
		t.Fatalf("Create browser task run: %v", err)
	}
	if _, err := s.TaskRuns().ClaimQueued(ctx, "WS", execution.TaskRunClaim{
		TaskRunID:          "task-run-browser",
		NodeID:             "node-1",
		RunnerID:           "runner-browser",
		LeaseID:            "lease-browser",
		SupportedProviders: []string{"flue-local"},
		Capabilities:       []string{"browser"},
		WorkerProfileIDs:   []string{"browser"},
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("lying capability ClaimQueued err = %v, want ErrInvalidTransition", err)
	}
}

func TestTaskRunClaimQueuedHonorsExplicitTargetNode(t *testing.T) {
	ctx := t.Context()
	s := New()
	for _, nodeID := range []string{"node-wrong", "node-target"} {
		if _, err := s.Nodes().Create(ctx, execution.NodeCreate{
			WorkspaceKey: "WS", NodeID: nodeID, RuntimeProvider: execution.RuntimeProviderLocal,
			DrainState: execution.WorkerNodeActive, TTL: time.Minute,
		}); err != nil {
			t.Fatalf("Create node %s: %v", nodeID, err)
		}
	}
	created, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-targeted", TaskID: "WS-TARGET",
		TargetNodeID: "node-target", Status: execution.TaskRunRecordQueued,
	})
	if err != nil {
		t.Fatalf("Create targeted TaskRun: %v", err)
	}
	if created.NodeID != "" || created.TargetNodeID != "node-target" {
		t.Fatalf("queued TaskRun owner=%q target=%q", created.NodeID, created.TargetNodeID)
	}
	if _, err := s.TaskRuns().ClaimQueued(ctx, "WS", execution.TaskRunClaim{
		TaskRunID: created.TaskRunID, NodeID: "node-wrong", LeaseID: "lease-wrong",
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("wrong-node claim error = %v, want invalid transition", err)
	}
	claimed, err := s.TaskRuns().ClaimQueued(ctx, "WS", execution.TaskRunClaim{
		TaskRunID: created.TaskRunID, NodeID: "node-target", LeaseID: "lease-target",
	})
	if err != nil {
		t.Fatalf("target-node claim: %v", err)
	}
	if claimed.NodeID != "node-target" || claimed.TargetNodeID != "node-target" || claimed.Status != execution.TaskRunRecordRunning {
		t.Fatalf("claimed TaskRun = %+v", claimed)
	}
}

func TestPlatformRecoverStaleDriverRunsFailsStaleRunsAndReleasesAdmission(t *testing.T) {
	ctx := t.Context()
	s := New()

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
	for _, run := range []execution.DriverRunCreate{
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

	result, err := s.DriverRuns().RecoverStale(ctx, "WS", execution.StaleDriverRunRecovery{
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
	if staleRun.Status != execution.DriverRunFailed || staleRun.ErrorClass != "stale_driver_run" || staleRun.Summary != "executor heartbeat expired" || staleRun.FinishedAt == nil {
		t.Fatalf("stale run = %+v, want failed stale_driver_run", staleRun)
	}
	freshRun, err := s.DriverRuns().Get(ctx, "WS", "run-fresh")
	if err != nil {
		t.Fatalf("Get fresh run: %v", err)
	}
	if freshRun.Status != execution.DriverRunRunning {
		t.Fatalf("fresh run status = %s, want running", freshRun.Status)
	}
	next, err := s.DriverRuns().Create(ctx, execution.DriverRunCreate{
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

	if _, err := s.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey: "WS",
		VersionID:    "version-missing-driver",
		DriverID:     "driver-missing",
		Version:      1,
		SourceDigest: "sha256:source",
		BundleDigest: "sha256:bundle",
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Create version without driver err = %v, want ErrNotFound", err)
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

	payload := json.RawMessage(`{"epicId":"WS-1"}`)
	run, err := s.DriverRuns().Create(ctx, execution.DriverRunCreate{
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
	if run.Status != execution.DriverRunQueued || string(run.Payload) != `{"epicId":"WS-1"}` {
		t.Fatalf("created run = %+v, want queued clone with original payload", run)
	}

	replay, err := s.DriverRuns().Create(ctx, execution.DriverRunCreate{
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

	active, err := s.DriverRuns().Create(ctx, execution.DriverRunCreate{
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

	queued, err := s.DriverRuns().List(ctx, "WS", execution.DriverRunFilter{Status: execution.DriverRunQueued})
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
	if claimed.Status != execution.DriverRunRunning || claimed.NodeID != "node-1" || claimed.FencingToken <= 0 {
		t.Fatalf("claimed = %+v, want running node-1 with fencing token", claimed)
	}
	if _, err := s.DriverSteps().Create(ctx, execution.DriverStepCreate{
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
	if _, err := s.DriverRuns().Claim(ctx, "WS", "run-1", "node-2", "lease-2"); !errors.Is(err, persistence.ErrAlreadyClaimed) {
		t.Fatalf("second Claim err = %v, want ErrAlreadyClaimed", err)
	}
	if _, err := s.DriverRuns().Heartbeat(ctx, "WS", "run-1", "node-2", "lease-1", claimed.FencingToken); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("Heartbeat wrong owner err = %v, want ErrNotOwner", err)
	}
	if _, err := s.DriverRuns().Heartbeat(ctx, "WS", "run-1", "node-1", "stale-lease", claimed.FencingToken); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("Heartbeat stale lease err = %v, want ErrNotOwner", err)
	}
	if _, err := s.DriverRuns().Finish(ctx, "WS", "run-1", execution.DriverRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: claimed.FencingToken + 1,
		Status:       execution.DriverRunCompleted,
	}); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("Finish stale fence err = %v, want ErrNotOwner", err)
	}

	finished, err := s.DriverRuns().Finish(ctx, "WS", "run-1", execution.DriverRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: claimed.FencingToken,
		Status:       execution.DriverRunCompleted,
		Summary:      "done",
	})
	if err != nil {
		t.Fatalf("Finish driver run: %v", err)
	}
	if finished.Status != execution.DriverRunCompleted || finished.FinishedAt == nil {
		t.Fatalf("finished = %+v, want completed with finished_at", finished)
	}

	next, err := s.DriverRuns().Create(ctx, execution.DriverRunCreate{
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
	if _, err := s.DriverSteps().Create(ctx, execution.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-other",
		DriverRunID:  "run-after-finish",
		StepKind:     "task_run",
	}); err != nil {
		t.Fatalf("Create other driver step: %v", err)
	}
	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-missing-step",
		DriverRunID:  "run-1",
		DriverStepID: "missing-step",
		TaskID:       "WS-1",
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Create task run missing step err = %v, want ErrNotFound", err)
	}
	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-mismatched-step",
		DriverRunID:  "run-1",
		DriverStepID: "step-other",
		TaskID:       "WS-1",
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Create task run mismatched step err = %v, want ErrInvalidTransition", err)
	}

	taskRun, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey:    "WS",
		TaskRunID:       "task-run-1",
		DriverRunID:     "run-1",
		DriverStepID:    "step-1",
		TaskID:          "WS-1",
		ProviderProfile: "codex-default",
		Status:          execution.TaskRunRecordRunning,
		NodeID:          "node-1",
		LeaseID:         "lease-1",
		RuntimeMetadata: map[string]string{"container": "abc"},
	})
	if err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	if taskRun.Status != execution.TaskRunRecordRunning || taskRun.StartedAt.IsZero() {
		t.Fatalf("taskRun = %+v, want running with started_at", taskRun)
	}

	children, err := s.TaskRuns().List(ctx, "WS", execution.TaskRunFilter{DriverRunID: "run-1"})
	if err != nil {
		t.Fatalf("List child task runs: %v", err)
	}
	if len(children) != 1 || children[0].TaskRunID != "task-run-1" {
		t.Fatalf("children = %+v, want task-run-1", children)
	}
	stepChildren, err := s.TaskRuns().List(ctx, "WS", execution.TaskRunFilter{DriverStepID: "step-1"})
	if err != nil {
		t.Fatalf("List driver step task runs: %v", err)
	}
	if len(stepChildren) != 1 || stepChildren[0].DriverStepID != "step-1" {
		t.Fatalf("step children = %+v, want step-linked task-run-1", stepChildren)
	}

	heartbeat, err := s.TaskRuns().Heartbeat(ctx, "WS", "task-run-1", execution.TaskRunHeartbeat{
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
	if _, err := s.TaskRuns().Heartbeat(ctx, "WS", "task-run-1", execution.TaskRunHeartbeat{
		NodeID:       "node-2",
		LeaseID:      "lease-1",
		FencingToken: taskRun.FencingToken,
	}); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("Heartbeat task run wrong owner err = %v, want ErrNotOwner", err)
	}
	logTimestamp := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	firstAppend := execution.TaskRunLogAppend{
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
	if _, err := s.TaskRuns().AppendLog(ctx, "WS", "task-run-1", conflictingAppend); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("AppendLog conflicting replay err = %v, want ErrConflict", err)
	}
	secondLog, err := s.TaskRuns().AppendLog(ctx, "WS", "task-run-1", execution.TaskRunLogAppend{
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
	logs, err := s.TaskRuns().ListLogs(ctx, "WS", "task-run-1", execution.TaskRunLogFilter{AfterSequence: 1})
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].Sequence != 2 || logs[0].Text != "default stream\n" {
		t.Fatalf("logs after sequence 1 = %+v, want second log", logs)
	}
	if _, err := s.TaskRuns().AppendLog(ctx, "WS", "task-run-1", execution.TaskRunLogAppend{
		RequestID:    "task-run-log-wrong-owner",
		NodeID:       "node-2",
		LeaseID:      "lease-1",
		FencingToken: taskRun.FencingToken,
		Text:         "bad owner\n",
		Timestamp:    logTimestamp.Add(2 * time.Second),
	}); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("AppendLog wrong owner err = %v, want ErrNotOwner", err)
	}

	exitCode := 0
	if _, err := s.TaskRuns().Finish(ctx, "WS", "task-run-1", execution.TaskRunFinish{
		NodeID:       "node-2",
		LeaseID:      "lease-2",
		FencingToken: taskRun.FencingToken,
		Status:       execution.TaskRunRecordCompleted,
		ExitCode:     &exitCode,
	}); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("Finish task run wrong owner err = %v, want ErrNotOwner", err)
	}
	completedTask, err := s.TaskRuns().Finish(ctx, "WS", "task-run-1", execution.TaskRunFinish{
		NodeID:           "node-1",
		LeaseID:          "lease-1",
		FencingToken:     taskRun.FencingToken,
		Status:           execution.TaskRunRecordCompleted,
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
	if _, err := s.TaskRuns().AppendLog(ctx, "WS", "task-run-1", execution.TaskRunLogAppend{
		RequestID:    "task-run-log-late",
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: taskRun.FencingToken,
		Text:         "late\n",
		Timestamp:    logTimestamp.Add(3 * time.Second),
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("AppendLog terminal err = %v, want ErrInvalidTransition", err)
	}
	if completedTask.Status != execution.TaskRunRecordCompleted || completedTask.ExitCode == nil || *completedTask.ExitCode != 0 || completedTask.FinishedAt == nil {
		t.Fatalf("completed task = %+v, want completed with exit code and finished_at", completedTask)
	}
	if completedTask.InputTokens != 11 || completedTask.OutputTokens != 7 || completedTask.CacheReadTokens != 5 || completedTask.CacheWriteTokens != 3 || completedTask.EstimatedCostUSD != 0.125 {
		t.Fatalf("completed task usage = %+v, want persisted usage", completedTask)
	}

	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-complete-1",
		TaskID:       "WS-2",
		Status:       execution.TaskRunRecordRunning,
		NodeID:       "node-1",
		LeaseID:      "lease-2",
	}); err != nil {
		t.Fatalf("Create complete task run: %v", err)
	}
	completeRun, err := s.TaskRuns().Get(ctx, "WS", "task-run-complete-1")
	if err != nil {
		t.Fatalf("Get complete task run: %v", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", execution.TaskRunComplete{
		Status: execution.TaskRunRecordCompleted,
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Complete task run blank completion ID err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", execution.TaskRunComplete{
		CompletionID: "completion-non-terminal",
		NodeID:       "node-1",
		LeaseID:      "lease-2",
		FencingToken: completeRun.FencingToken,
		Status:       execution.TaskRunRecordRunning,
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Complete task run non-terminal err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", execution.TaskRunComplete{
		CompletionID: "completion-negative-usage",
		NodeID:       "node-1",
		LeaseID:      "lease-2",
		FencingToken: completeRun.FencingToken,
		Status:       execution.TaskRunRecordCompleted,
		InputTokens:  -1,
	}); err == nil {
		t.Fatalf("Complete task run negative usage err = nil, want validation error")
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", execution.TaskRunComplete{
		CompletionID: "completion-close-failed",
		NodeID:       "node-1",
		LeaseID:      "lease-2",
		FencingToken: completeRun.FencingToken,
		Status:       execution.TaskRunRecordFailed,
		CloseTask:    true,
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Complete task run close failed err = %v, want ErrInvalidTransition", err)
	}
	completedByComplete, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", execution.TaskRunComplete{
		CompletionID:     "completion-1",
		NodeID:           "node-1",
		LeaseID:          "lease-2",
		FencingToken:     completeRun.FencingToken,
		Status:           execution.TaskRunRecordCompleted,
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
	replayed, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", execution.TaskRunComplete{
		CompletionID: "completion-1",
		NodeID:       "stale-node",
		LeaseID:      "stale-lease",
		FencingToken: completeRun.FencingToken + 1,
		Status:       execution.TaskRunRecordCompleted,
	})
	if err != nil {
		t.Fatalf("Replay complete task run: %v", err)
	}
	if replayed.TaskRunID != completedByComplete.TaskRunID || replayed.InputTokens != completedByComplete.InputTokens {
		t.Fatalf("replayed completion = %+v, want original completed task run", replayed)
	}
	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-complete-other",
		TaskID:       "WS-3",
		Status:       execution.TaskRunRecordRunning,
	}); err != nil {
		t.Fatalf("Create other complete task run: %v", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-other", execution.TaskRunComplete{
		CompletionID: "completion-1",
		Status:       execution.TaskRunRecordCompleted,
	}); !errors.Is(err, persistence.ErrAlreadyExists) {
		t.Fatalf("Complete task run completion-id collision err = %v, want ErrAlreadyExists", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-complete-1", execution.TaskRunComplete{
		CompletionID: "completion-terminal-new",
		Status:       execution.TaskRunRecordCompleted,
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Complete task run new completion after terminal err = %v, want ErrInvalidTransition", err)
	}
}

func TestPlatformTaskRunCompleteRequiresReadyArtifacts(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-artifact-1",
		TaskID:       "WS-4",
		Status:       execution.TaskRunRecordRunning,
		NodeID:       "node-1",
		LeaseID:      "lease-1",
	}); err != nil {
		t.Fatalf("Create artifact task run: %v", err)
	}
	running, err := s.TaskRuns().Get(ctx, "WS", "task-run-artifact-1")
	if err != nil {
		t.Fatalf("Get artifact task run: %v", err)
	}
	base := execution.TaskRunComplete{
		CompletionID:        "completion-artifact-missing",
		NodeID:              "node-1",
		LeaseID:             "lease-1",
		FencingToken:        running.FencingToken,
		Status:              execution.TaskRunRecordCompleted,
		RequiredArtifactIDs: []string{"missing-artifact"},
		RequireArtifacts:    true,
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base); !errors.Is(err, artifacts.ErrNotFound) {
		t.Fatalf("Complete missing artifact err = %v, want artifacts.ErrNotFound", err)
	}

	if _, err := s.SeedArtifact(ctx, artifacts.Artifact{
		WorkspaceKey:  "WS",
		ArtifactID:    "artifact-wrong-owner",
		TaskID:        "WS-4",
		OwnerType:     artifacts.OwnerTaskRun,
		OwnerID:       "other-task-run",
		Type:          "patch",
		URI:           "artifact://wrong-owner",
		ContentHash:   "sha256:wrong-owner",
		DurableStatus: artifacts.StatusFinalized,
	}, nil); err != nil {
		t.Fatalf("Create wrong-owner artifact: %v", err)
	}
	base.CompletionID = "completion-artifact-wrong-owner"
	base.RequiredArtifactIDs = []string{"artifact-wrong-owner"}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Complete wrong-owner artifact err = %v, want ErrInvalidTransition", err)
	}

	if _, err := s.SeedArtifact(ctx, artifacts.Artifact{
		WorkspaceKey:  "WS",
		ArtifactID:    "artifact-wrong-task",
		TaskID:        "WS-other",
		OwnerType:     artifacts.OwnerTaskRun,
		OwnerID:       "task-run-artifact-1",
		Type:          "patch",
		URI:           "artifact://wrong-task",
		ContentHash:   "sha256:wrong-task",
		DurableStatus: artifacts.StatusFinalized,
	}, nil); err != nil {
		t.Fatalf("Create wrong-task artifact: %v", err)
	}
	base.CompletionID = "completion-artifact-wrong-task"
	base.RequiredArtifactIDs = []string{"artifact-wrong-task"}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Complete wrong-task artifact err = %v, want ErrInvalidTransition", err)
	}

	if _, err := s.SeedArtifact(ctx, artifacts.Artifact{
		WorkspaceKey:  "WS",
		ArtifactID:    "artifact-empty-hash",
		TaskID:        "WS-4",
		OwnerType:     artifacts.OwnerTaskRun,
		OwnerID:       "task-run-artifact-1",
		Type:          "patch",
		DurableStatus: artifacts.StatusFinalized,
	}, nil); err != nil {
		t.Fatalf("Create empty-hash artifact: %v", err)
	}
	base.CompletionID = "completion-artifact-empty-hash"
	base.RequiredArtifactIDs = []string{"artifact-empty-hash"}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Complete empty-hash artifact err = %v, want ErrInvalidTransition", err)
	}

	if _, err := s.SeedArtifact(ctx, artifacts.Artifact{
		WorkspaceKey:  "WS",
		ArtifactID:    "artifact-declared",
		TaskID:        "WS-4",
		OwnerType:     artifacts.OwnerTaskRun,
		OwnerID:       "task-run-artifact-1",
		Type:          "patch",
		ContentHash:   "sha256:declared",
		DurableStatus: artifacts.StatusDeclared,
	}, nil); err != nil {
		t.Fatalf("Create declared artifact: %v", err)
	}
	base.CompletionID = "completion-artifact-declared"
	base.RequiredArtifactIDs = []string{"artifact-declared"}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Complete declared artifact err = %v, want ErrInvalidTransition", err)
	}

	if _, err := s.SeedArtifact(ctx, artifacts.Artifact{
		WorkspaceKey: "WS", ArtifactID: "artifact-declared", TaskID: "WS-4",
		OwnerType: artifacts.OwnerTaskRun, OwnerID: "task-run-artifact-1", Type: "patch",
		URI: "artifact://artifact-declared", ContentHash: "sha256:declared",
		DurableStatus: artifacts.StatusFinalized,
	}, nil); err != nil {
		t.Fatalf("Finalize artifact: %v", err)
	}
	base.CompletionID = "completion-artifact-finalized"
	completed, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-1", base)
	if err != nil {
		t.Fatalf("Complete finalized artifact: %v", err)
	}
	if completed.Status != execution.TaskRunRecordCompleted {
		t.Fatalf("completed task status = %q, want completed", completed.Status)
	}
}

func TestPlatformTaskRunCompleteRequiresCloudSafeArtifactsForCloudSandbox(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-cloud-artifact",
		TaskID:       "WS-4",
		Status:       execution.TaskRunRecordRunning,
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		SandboxPlacement: execution.TaskRunPlacementRecord{
			Provider:  "daytona",
			SandboxID: "sandbox-1",
		},
	}); err != nil {
		t.Fatalf("Create cloud artifact task run: %v", err)
	}
	if _, err := s.SeedArtifact(ctx, artifacts.Artifact{
		WorkspaceKey:  "WS",
		ArtifactID:    "artifact-cloud-file",
		TaskID:        "WS-4",
		OwnerType:     artifacts.OwnerTaskRun,
		OwnerID:       "task-run-cloud-artifact",
		Type:          "patch",
		URI:           "file:///tmp/patch.diff",
		ContentHash:   "sha256:cloud-file",
		DurableStatus: artifacts.StatusFinalized,
	}, nil); err != nil {
		t.Fatalf("Create cloud file artifact: %v", err)
	}
	running, err := s.TaskRuns().Get(ctx, "WS", "task-run-cloud-artifact")
	if err != nil {
		t.Fatalf("Get cloud artifact task run: %v", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-cloud-artifact", execution.TaskRunComplete{
		CompletionID:     "completion-cloud-local-ref",
		NodeID:           "node-1",
		LeaseID:          "lease-1",
		FencingToken:     running.FencingToken,
		Status:           execution.TaskRunRecordCompleted,
		ArtifactsRef:     "file:///tmp/result.tar",
		RequireArtifacts: true,
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Complete cloud file artifacts_ref err = %v, want ErrInvalidTransition", err)
	}
	exitCode := 0
	if _, err := s.TaskRuns().Finish(ctx, "WS", "task-run-cloud-artifact", execution.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: running.FencingToken,
		Status:       execution.TaskRunRecordCompleted,
		ExitCode:     &exitCode,
		ArtifactsRef: "file:///tmp/result.tar",
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Finish cloud file artifacts_ref err = %v, want ErrInvalidTransition", err)
	}
	complete := execution.TaskRunComplete{
		CompletionID:        "completion-cloud-file",
		NodeID:              "node-1",
		LeaseID:             "lease-1",
		FencingToken:        running.FencingToken,
		Status:              execution.TaskRunRecordCompleted,
		RequiredArtifactIDs: []string{"artifact-cloud-file"},
		RequireArtifacts:    true,
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-cloud-artifact", complete); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Complete cloud file artifact err = %v, want ErrInvalidTransition", err)
	}

	if _, err := s.SeedArtifact(ctx, artifacts.Artifact{
		WorkspaceKey: "WS", ArtifactID: "artifact-cloud-file", TaskID: "WS-4",
		OwnerType: artifacts.OwnerTaskRun, OwnerID: "task-run-cloud-artifact", Type: "patch",
		URI: "artifact://artifact-cloud-file", ContentHash: "sha256:cloud-file",
		DurableStatus: artifacts.StatusFinalized,
	}, nil); err != nil {
		t.Fatalf("Update cloud artifact URI: %v", err)
	}
	complete.CompletionID = "completion-cloud-safe"
	completed, err := s.TaskRuns().Complete(ctx, "WS", "task-run-cloud-artifact", complete)
	if err != nil {
		t.Fatalf("Complete cloud safe artifact: %v", err)
	}
	if completed.Status != execution.TaskRunRecordCompleted {
		t.Fatalf("completed task status = %q, want completed", completed.Status)
	}
}

func TestPlatformTaskRunAllowsLocalArtifactsForFlueLocal(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-flue-local-complete",
		TaskID:       "WS-5",
		Status:       execution.TaskRunRecordRunning,
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		SandboxPlacement: execution.TaskRunPlacementRecord{
			Provider: "flue-local",
		},
	}); err != nil {
		t.Fatalf("Create flue-local completion task run: %v", err)
	}
	running, err := s.TaskRuns().Get(ctx, "WS", "task-run-flue-local-complete")
	if err != nil {
		t.Fatalf("Get flue-local completion task run: %v", err)
	}
	completed, err := s.TaskRuns().Complete(ctx, "WS", "task-run-flue-local-complete", execution.TaskRunComplete{
		CompletionID:     "completion-flue-local-file-ref",
		NodeID:           "node-1",
		LeaseID:          "lease-1",
		FencingToken:     running.FencingToken,
		Status:           execution.TaskRunRecordCompleted,
		ArtifactsRef:     "file:///tmp/result.tar",
		RequireArtifacts: true,
	})
	if err != nil {
		t.Fatalf("Complete flue-local file ref: %v", err)
	}
	if completed.Status != execution.TaskRunRecordCompleted || completed.ArtifactsRef != "file:///tmp/result.tar" {
		t.Fatalf("completed task run = %+v, want local artifact ref", completed)
	}

	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-flue-local-finish",
		TaskID:       "WS-6",
		Status:       execution.TaskRunRecordRunning,
		NodeID:       "node-1",
		LeaseID:      "lease-2",
		SandboxPlacement: execution.TaskRunPlacementRecord{
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
	finished, err := s.TaskRuns().Finish(ctx, "WS", "task-run-flue-local-finish", execution.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-2",
		FencingToken: running.FencingToken,
		Status:       execution.TaskRunRecordCompleted,
		ExitCode:     &exitCode,
		ArtifactsRef: "file:///tmp/result.tar",
	})
	if err != nil {
		t.Fatalf("Finish flue-local file ref: %v", err)
	}
	if finished.Status != execution.TaskRunRecordCompleted || finished.ArtifactsRef != "file:///tmp/result.tar" {
		t.Fatalf("finished task run = %+v, want local artifact ref", finished)
	}
}

func TestPlatformTaskRunCompleteRequireArtifactsNeedsEvidence(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-artifact-ref",
		TaskID:       "WS-5",
		Status:       execution.TaskRunRecordRunning,
	}); err != nil {
		t.Fatalf("Create artifact-ref task run: %v", err)
	}
	if _, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-ref", execution.TaskRunComplete{
		CompletionID:     "completion-artifacts-required",
		Status:           execution.TaskRunRecordCompleted,
		RequireArtifacts: true,
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Complete require artifacts without evidence err = %v, want ErrInvalidTransition", err)
	}
	completed, err := s.TaskRuns().Complete(ctx, "WS", "task-run-artifact-ref", execution.TaskRunComplete{
		CompletionID:     "completion-artifacts-ref",
		Status:           execution.TaskRunRecordCompleted,
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
	if _, err := s.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-1",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
	}); err != nil {
		t.Fatalf("Create driver run: %v", err)
	}

	step, err := s.DriverSteps().Create(ctx, execution.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-1",
		DriverRunID:  "run-1",
		StepKind:     "custom_vendor_gate",
		Status:       execution.DriverStepRunning,
		InputRef:     "artifact://input-1",
		ExternalRef:  "github://check/123",
	})
	if err != nil {
		t.Fatalf("Create driver step: %v", err)
	}
	if step.StepKind != "custom_vendor_gate" || step.Status != execution.DriverStepRunning || step.StartedAt.IsZero() {
		t.Fatalf("created step = %+v, want running custom step with started_at", step)
	}

	if _, err := s.DriverSteps().Create(ctx, execution.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-missing-run",
		DriverRunID:  "missing-run",
		StepKind:     "gate",
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Create missing-run step err = %v, want ErrNotFound", err)
	}
	if _, err := s.DriverSteps().Create(ctx, execution.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-1",
		DriverRunID:  "run-1",
		StepKind:     "gate",
	}); !errors.Is(err, persistence.ErrAlreadyExists) {
		t.Fatalf("Create duplicate step err = %v, want ErrAlreadyExists", err)
	}

	steps, err := s.DriverSteps().ListForRun(ctx, "WS", "run-1", execution.DriverStepFilter{StepKind: "custom_vendor_gate", Status: execution.DriverStepRunning})
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
	if _, err := s.DriverSteps().Create(ctx, execution.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-no-owner",
		DriverRunID:  "run-1",
		StepKind:     "gate",
	}); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("Create claimed step without owner err = %v, want ErrNotOwner", err)
	}
	if _, err := s.DriverSteps().Create(ctx, execution.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-wrong-owner",
		DriverRunID:  "run-1",
		StepKind:     "gate",
		NodeID:       "node-2",
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("Create claimed step with wrong owner err = %v, want ErrNotOwner", err)
	}
	if _, err := s.DriverSteps().Create(ctx, execution.DriverStepCreate{
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
	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-1",
		DriverRunID:  "run-1",
		DriverStepID: "step-1",
		TaskID:       "WS-1",
		Status:       execution.TaskRunRecordRunning,
		NodeID:       "task-node-1",
		LeaseID:      "task-lease-1",
	}); err != nil {
		t.Fatalf("Create task run for driver step: %v", err)
	}

	completed := execution.DriverStepCompleted
	taskRunID := "task-run-1"
	actionID := "action-1"
	outputRef := "artifact://output-1"
	missingTaskRunID := "missing-task-run"
	if _, err := s.DriverSteps().Update(ctx, "WS", "step-1", execution.DriverStepUpdate{
		Status:       &completed,
		TaskRunID:    &missingTaskRunID,
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Update claimed step missing task run err = %v, want ErrNotFound", err)
	}
	if _, err := s.DriverSteps().Update(ctx, "WS", "step-1", execution.DriverStepUpdate{
		Status:         &completed,
		TaskRunID:      &taskRunID,
		ActionLedgerID: &actionID,
		OutputRef:      &outputRef,
	}); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("Update claimed step without owner err = %v, want ErrNotOwner", err)
	}
	if _, err := s.DriverSteps().Update(ctx, "WS", "step-1", execution.DriverStepUpdate{
		Status:         &completed,
		TaskRunID:      &taskRunID,
		ActionLedgerID: &actionID,
		OutputRef:      &outputRef,
		NodeID:         claimed.NodeID,
		LeaseID:        claimed.LeaseID,
		FencingToken:   claimed.FencingToken + 1,
	}); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("Update claimed step with stale fence err = %v, want ErrNotOwner", err)
	}
	updated, err := s.DriverSteps().Update(ctx, "WS", "step-1", execution.DriverStepUpdate{
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
	if updated.Status != execution.DriverStepCompleted || updated.TaskRunID != "task-run-1" || updated.ActionLedgerID != "action-1" || updated.OutputRef != "artifact://output-1" || updated.EndedAt == nil {
		t.Fatalf("updated step = %+v, want completed refs and ended_at", updated)
	}

	steps, err = s.DriverSteps().List(ctx, "WS", execution.DriverStepFilter{TaskRunID: "task-run-1", ActionLedgerID: "action-1", Status: execution.DriverStepCompleted})
	if err != nil {
		t.Fatalf("List completed driver steps: %v", err)
	}
	if len(steps) != 1 || steps[0].StepID != "step-1" {
		t.Fatalf("completed steps = %+v, want step-1", steps)
	}

	queued := execution.DriverStepQueued
	retry, err := s.DriverSteps().Update(ctx, "WS", "step-1", execution.DriverStepUpdate{
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
	if retry.Status != execution.DriverStepQueued || !retry.StartedAt.IsZero() || retry.EndedAt != nil {
		t.Fatalf("retry step = %+v, want queued with cleared timestamps", retry)
	}

	if _, err := s.DriverRuns().Finish(ctx, "WS", "run-1", execution.DriverRunFinish{
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
		Status:       execution.DriverRunCompleted,
	}); err != nil {
		t.Fatalf("Finish driver run: %v", err)
	}
	if _, err := s.DriverSteps().Create(ctx, execution.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-terminal",
		DriverRunID:  "run-1",
		StepKind:     "gate",
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Create terminal claimed step err = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.DriverSteps().Update(ctx, "WS", "step-1", execution.DriverStepUpdate{
		Status:       &completed,
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("Update terminal claimed step err = %v, want ErrInvalidTransition", err)
	}
}

func TestPlatformRecoverStaleTaskRunsFailsStaleRunsAndSteps(t *testing.T) {
	ctx := t.Context()
	s := New()

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
	if _, err := s.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-1",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
	}); err != nil {
		t.Fatalf("Create driver run: %v", err)
	}
	if _, err := s.DriverSteps().Create(ctx, execution.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-stale",
		DriverRunID:  "run-1",
		StepKind:     "run_agent",
		Status:       execution.DriverStepRunning,
	}); err != nil {
		t.Fatalf("Create stale driver step: %v", err)
	}
	if _, err := s.DriverSteps().Create(ctx, execution.DriverStepCreate{
		WorkspaceKey: "WS",
		StepID:       "step-fresh",
		DriverRunID:  "run-1",
		StepKind:     "run_agent",
		Status:       execution.DriverStepRunning,
	}); err != nil {
		t.Fatalf("Create fresh driver step: %v", err)
	}
	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-stale",
		DriverRunID:  "run-1",
		DriverStepID: "step-stale",
		TaskID:       "WS-1",
		Status:       execution.TaskRunRecordRunning,
		NodeID:       "task-node-1",
		LeaseID:      "task-lease-1",
	}); err != nil {
		t.Fatalf("Create stale task run: %v", err)
	}
	if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-fresh",
		DriverRunID:  "run-1",
		DriverStepID: "step-fresh",
		TaskID:       "WS-2",
		Status:       execution.TaskRunRecordRunning,
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

	result, err := s.DriverRuns().RecoverStaleTaskRuns(ctx, "WS", "run-1", execution.StaleTaskRunRecovery{
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
	if staleRun.Status != execution.TaskRunRecordFailed || staleRun.ErrorClass != "stale_task_run" || staleRun.ErrorMessage != "operator recovery" || staleRun.FinishedAt == nil {
		t.Fatalf("stale task run = %+v, want failed stale_task_run", staleRun)
	}
	freshRun, err := s.TaskRuns().Get(ctx, "WS", "task-run-fresh")
	if err != nil {
		t.Fatalf("Get fresh task run: %v", err)
	}
	if freshRun.Status != execution.TaskRunRecordRunning {
		t.Fatalf("fresh task run status = %s, want running", freshRun.Status)
	}
	staleStep, err := s.DriverSteps().Get(ctx, "WS", "step-stale")
	if err != nil {
		t.Fatalf("Get stale step: %v", err)
	}
	if staleStep.Status != execution.DriverStepFailed || staleStep.EndedAt == nil {
		t.Fatalf("stale step = %+v, want failed ended step", staleStep)
	}
	freshStep, err := s.DriverSteps().Get(ctx, "WS", "step-fresh")
	if err != nil {
		t.Fatalf("Get fresh step: %v", err)
	}
	if freshStep.Status != execution.DriverStepRunning {
		t.Fatalf("fresh step status = %s, want running", freshStep.Status)
	}
	if _, err := s.DriverRuns().RecoverStaleTaskRuns(ctx, "WS", "missing-run", execution.StaleTaskRunRecovery{}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Recover missing run err = %v, want ErrNotFound", err)
	}
}
