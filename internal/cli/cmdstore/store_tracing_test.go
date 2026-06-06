package cmdstore

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestWrapStoreWithTracing_Smoke exercises every traced substore method so
// the span-decorator paths are reached. We don't assert behavior — the
// underlying memstore already has its own coverage; this is a guard
// against the tracing wrapper drifting from the store.Store interface and
// against any panic in the wrapper itself.
//
// Errors from the inner store are intentionally ignored: most methods
// either create resources we then read, or are called on resources the
// preceding step created. A few are called on missing keys to also
// exercise the "record error" branch in the wrapper.
func TestWrapStoreWithTracing_Smoke(t *testing.T) {
	inner := memstore.New()
	wrapped := WrapStoreWithTracing(inner)
	if wrapped == nil {
		t.Fatal("WrapStoreWithTracing returned nil for non-nil input")
	}

	// Accessors return their cached sub-store; should be non-nil and stable.
	if wrapped.Workspaces() == nil || wrapped.Workspaces() != wrapped.Workspaces() {
		t.Error("Workspaces accessor unstable")
	}
	_ = wrapped.Repos()
	_ = wrapped.Agents()
	_ = wrapped.Nodes()
	_ = wrapped.AgentSessions()
	_ = wrapped.TerminalSessions()
	_ = wrapped.Artifacts()
	_ = wrapped.AgentLeases()
	_ = wrapped.AgentOwnershipLeases()
	_ = wrapped.AgentCommands()
	_ = wrapped.Drivers()
	_ = wrapped.DriverVersions()
	_ = wrapped.AgentServices()
	_ = wrapped.TriggerBindings()
	_ = wrapped.DriverRuns()
	_ = wrapped.DriverSteps()
	_ = wrapped.TaskRuns()
	_ = wrapped.Roles()
	_ = wrapped.Daemon()

	ctx := context.Background()
	ws := wrapped.Workspaces()
	if _, err := ws.Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test", DefaultBranch: "main"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	_, _ = ws.Get(ctx, "TEST")
	_, _ = ws.GetByName(ctx, "test")
	_, _ = ws.List(ctx)
	_, _ = ws.Update(ctx, "TEST", store.WorkspaceUpdate{})
	// Error path on a missing key.
	_, _ = ws.Get(ctx, "missing")

	repos := wrapped.Repos()
	if _, err := repos.Create(ctx, store.RepoCreate{WorkspaceKey: "TEST", Name: "repo", DefaultBranch: "main", SourceRepoID: "repo"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	_, _ = repos.Get(ctx, "TEST", "repo")
	_, _ = repos.List(ctx, "TEST")
	_, _ = repos.Update(ctx, "TEST", "repo", store.RepoUpdate{})

	agents := wrapped.Agents()
	_, _ = agents.Create(ctx, store.AgentCreate{WorkspaceKey: "TEST", Name: "agent", RoleName: "worker"})
	_, _ = agents.Get(ctx, "TEST", "agent")
	_, _ = agents.List(ctx, "TEST")
	_, _ = agents.Update(ctx, "TEST", "agent", store.AgentUpdate{})

	roles := wrapped.Roles()
	_, _ = roles.Create(ctx, store.RoleCreate{WorkspaceKey: "TEST", Name: "worker"})
	_, _ = roles.Create(ctx, store.RoleCreate{WorkspaceKey: "TEST", Name: "lead"})
	_, _ = roles.Get(ctx, "TEST", "worker")
	_, _ = roles.List(ctx, "TEST")
	_, _ = roles.Update(ctx, "TEST", "worker", store.RoleUpdate{})

	profiles := wrapped.WorkerProfiles()
	_, _ = profiles.Create(ctx, store.WorkerProfileCreate{WorkspaceKey: "TEST", ProfileID: "falcon", Role: "lead"})
	_, _ = profiles.Get(ctx, "TEST", "falcon")
	_, _ = profiles.List(ctx, "TEST", store.WorkerProfileFilter{})
	_, _ = profiles.Update(ctx, "TEST", "falcon", store.WorkerProfileUpdate{})

	nodes := wrapped.Nodes()
	_, _ = nodes.Create(ctx, store.NodeCreate{WorkspaceKey: "TEST", NodeID: "node-1", OwnerActor: "test", RuntimeProvider: domain.RuntimeProviderLocal, TTL: time.Minute})
	_, _ = nodes.Get(ctx, "TEST", "node-1")
	_, _ = nodes.List(ctx, "TEST")
	_, _ = nodes.Heartbeat(ctx, "TEST", "node-1", time.Minute)

	sessions := wrapped.AgentSessions()
	_, _ = sessions.Create(ctx, store.AgentSessionCreate{WorkspaceKey: "TEST", SessionID: "sess-1", AgentID: "agent"})
	_, _ = sessions.Get(ctx, "TEST", "sess-1")
	_, _ = sessions.List(ctx, "TEST", store.AgentSessionFilter{})
	_, _ = sessions.Heartbeat(ctx, "TEST", "sess-1")
	_, _ = sessions.Update(ctx, "TEST", "sess-1", store.AgentSessionUpdate{})

	terms := wrapped.TerminalSessions()
	_, _ = terms.Create(ctx, store.TerminalSessionCreate{WorkspaceKey: "TEST", TerminalID: "term-1", AgentID: "agent"})
	_, _ = terms.Get(ctx, "TEST", "term-1")
	_, _ = terms.List(ctx, "TEST", store.TerminalSessionFilter{})
	_, _ = terms.Update(ctx, "TEST", "term-1", store.TerminalSessionUpdate{})

	artifacts := wrapped.Artifacts()
	_, _ = artifacts.Create(ctx, store.ArtifactCreate{WorkspaceKey: "TEST", ArtifactID: "art-1", Type: "log"})
	_, _ = artifacts.Get(ctx, "TEST", "art-1")
	_, _ = artifacts.List(ctx, "TEST", store.ArtifactFilter{})
	_, _ = artifacts.UploadContent(ctx, "TEST", "art-1", store.ArtifactContentUpload{Body: bytes.NewReader([]byte("artifact"))})
	_, _ = artifacts.Finalize(ctx, "TEST", "art-1", store.ArtifactFinalize{})
	_, _ = artifacts.Update(ctx, "TEST", "art-1", store.ArtifactUpdate{})

	leases := wrapped.AgentLeases()
	_, _ = leases.Create(ctx, store.AgentLeaseCreate{WorkspaceKey: "TEST", SessionID: "sess-1", LeaseID: "lease-1", AgentID: "agent", NodeID: "node-1", TTL: time.Minute})
	_, _ = leases.Get(ctx, "TEST", "lease-1")
	_, _ = leases.List(ctx, "TEST", store.AgentLeaseFilter{})
	_, _ = leases.Heartbeat(ctx, "TEST", "lease-1", "tok", time.Minute)
	_, _ = leases.Release(ctx, "TEST", "lease-1", "tok")

	owns := wrapped.AgentOwnershipLeases()
	_, _ = owns.Acquire(ctx, store.AgentOwnershipLeaseAcquire{WorkspaceKey: "TEST", AgentID: "agent", LeaseID: "ownlease-1", OwnerID: "owner", NodeID: "node-1", TTL: time.Minute})
	_, _ = owns.Get(ctx, "TEST", "agent")
	_, _ = owns.List(ctx, "TEST", store.AgentOwnershipLeaseFilter{})
	_, _ = owns.Heartbeat(ctx, "TEST", "agent", "tok", time.Minute)
	_, _ = owns.Release(ctx, "TEST", "agent", "tok")

	cmds := wrapped.AgentCommands()
	_, _ = cmds.Create(ctx, store.AgentCommandCreate{WorkspaceKey: "TEST", CommandID: "cmd-1", TargetAgentID: "agent", Type: "noop"})
	_, _ = cmds.Get(ctx, "TEST", "cmd-1")
	_, _ = cmds.List(ctx, "TEST", store.AgentCommandFilter{})
	_, _ = cmds.Ack(ctx, "TEST", "cmd-1")
	_, _ = cmds.Complete(ctx, "TEST", "cmd-1", store.AgentCommandComplete{Status: domain.AgentCommandSucceeded})

	drivers := wrapped.Drivers()
	_, _ = drivers.Create(ctx, store.DriverCreate{WorkspaceKey: "TEST", DriverID: "driver-1", Name: "complete-epic", OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive})
	_, _ = drivers.Get(ctx, "TEST", "driver-1")
	_, _ = drivers.List(ctx, "TEST", store.DriverFilter{})
	_, _ = drivers.Update(ctx, "TEST", "driver-1", store.DriverUpdate{})

	versions := wrapped.DriverVersions()
	_, _ = versions.Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "TEST",
		VersionID:        "version-1",
		DriverID:         "driver-1",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	})
	_, _ = versions.Get(ctx, "TEST", "version-1")
	_, _ = versions.List(ctx, "TEST", store.DriverVersionFilter{})

	services := wrapped.AgentServices()
	_, _ = services.Create(ctx, store.AgentServiceCreate{
		WorkspaceKey:  "TEST",
		ServiceID:     "lead",
		Kind:          domain.AgentServiceKindLead,
		DesiredState:  domain.AgentServiceDesiredRunning,
		RoleName:      "lead",
		ProfileName:   "falcon",
		MaxInstances:  1,
		EventSources:  []string{"github:issues"},
		Permissions:   []string{"task_run.create"},
		RestartPolicy: "always",
	})
	_, _ = services.Get(ctx, "TEST", "lead")
	_, _ = services.List(ctx, "TEST", store.AgentServiceFilter{Kind: domain.AgentServiceKindLead})
	paused := domain.AgentServiceDesiredPaused
	_, _ = services.Update(ctx, "TEST", "lead", store.AgentServiceUpdate{DesiredState: &paused})

	bindings := wrapped.TriggerBindings()
	_, _ = bindings.Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey:      "TEST",
		BindingID:         "binding-1",
		Name:              "Complete epic",
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
		Enabled:           true,
	})
	_, _ = bindings.Get(ctx, "TEST", "binding-1")
	_, _ = bindings.GetByRouteKey(ctx, "TEST", "epics.runs.create")
	_, _ = bindings.List(ctx, "TEST", store.TriggerBindingFilter{})
	_, _ = bindings.Update(ctx, "TEST", "binding-1", store.TriggerBindingUpdate{})

	driverRuns := wrapped.DriverRuns()
	_, _ = driverRuns.Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "TEST",
		RunID:           "driver-run-1",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		EpicID:          "TEST-1",
		IdempotencyKey:  "idem-driver-run-1",
		Input:           map[string]string{"epicId": "TEST-1"},
	})
	_, _ = driverRuns.CreateEpic(ctx, "TEST", "TEST-2", store.EpicRunCreate{
		RunID:          "driver-run-epic",
		IdempotencyKey: "idem-driver-run-epic",
		Input:          map[string]string{"epicId": "wrong"},
	})
	_, _ = driverRuns.Get(ctx, "TEST", "driver-run-1")
	_, _ = driverRuns.List(ctx, "TEST", store.DriverRunFilter{})
	claimed, _ := driverRuns.Claim(ctx, "TEST", "driver-run-1", "node-1", "driver-lease-1")
	fence := int64(1)
	if claimed != nil {
		fence = claimed.FencingToken
	}
	_, _ = driverRuns.Heartbeat(ctx, "TEST", "driver-run-1", "node-1", "driver-lease-1", fence)

	driverSteps := wrapped.DriverSteps()
	_, _ = driverSteps.Create(ctx, store.DriverStepCreate{WorkspaceKey: "TEST", StepID: "driver-step-1", DriverRunID: "driver-run-1", StepKind: "custom_vendor_gate", Status: domain.DriverStepWaiting, NodeID: "node-1", LeaseID: "driver-lease-1", FencingToken: fence})
	_, _ = driverSteps.CreateForRun(ctx, "TEST", "driver-run-1", store.DriverStepCreate{StepID: "driver-step-2", StepKind: "run_agent", NodeID: "node-1", LeaseID: "driver-lease-1", FencingToken: fence})
	_, _ = driverSteps.Get(ctx, "TEST", "driver-step-1")
	_, _ = driverSteps.List(ctx, "TEST", store.DriverStepFilter{DriverRunID: "driver-run-1"})
	_, _ = driverSteps.ListForRun(ctx, "TEST", "driver-run-1", store.DriverStepFilter{})
	completedStep := domain.DriverStepCompleted
	_, _ = driverSteps.Update(ctx, "TEST", "driver-step-1", store.DriverStepUpdate{Status: &completedStep, NodeID: "node-1", LeaseID: "driver-lease-1", FencingToken: fence})
	_, _ = driverRuns.Finish(ctx, "TEST", "driver-run-1", store.DriverRunFinish{NodeID: "node-1", LeaseID: "driver-lease-1", FencingToken: fence, Status: domain.DriverRunCompleted})

	taskRuns := wrapped.TaskRuns()
	_, _ = taskRuns.Create(ctx, store.TaskRunCreate{WorkspaceKey: "TEST", TaskRunID: "task-run-1", DriverRunID: "driver-run-1", TaskID: "TEST-1", Status: domain.TaskRunRunning})
	_, _ = taskRuns.Get(ctx, "TEST", "task-run-1")
	_, _ = taskRuns.List(ctx, "TEST", store.TaskRunFilter{})
	exitCode := 0
	_, _ = taskRuns.Finish(ctx, "TEST", "task-run-1", store.TaskRunFinish{Status: domain.TaskRunCompleted, ExitCode: &exitCode, LogsRef: "logs://task-run-1"})

	daemon := wrapped.Daemon()
	_, _ = daemon.Get(ctx, "TEST")
	_, _ = daemon.Upsert(ctx, &domain.DaemonProfile{WorkspaceKey: "TEST"})

	// Cleanup paths.
	_ = roles.Delete(ctx, "TEST", "worker")
	_ = services.Delete(ctx, "TEST", "lead")
	_ = agents.Delete(ctx, "TEST", "agent")
	_ = repos.Delete(ctx, "TEST", "repo")
	_ = ws.Delete(ctx, "TEST")

	if err := wrapped.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestWrapStoreWithTracing_Nil(t *testing.T) {
	if got := WrapStoreWithTracing(nil); got != nil {
		t.Errorf("WrapStoreWithTracing(nil) = %v, want nil", got)
	}
}
