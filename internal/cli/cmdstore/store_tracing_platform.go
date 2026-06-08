package cmdstore

import (
	"context"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type tracedDriverStore struct{ inner store.DriverStore }

func (t *tracedDriverStore) Create(ctx context.Context, in store.DriverCreate) (*domain.Driver, error) {
	ctx, span := startStoreSpan(ctx, "Drivers", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverStore) Get(ctx context.Context, ws, driverID string) (*domain.Driver, error) {
	ctx, span := startStoreSpan(ctx, "Drivers", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, driverID)
	finish(span, err)
	return out, err
}

func (t *tracedDriverStore) List(ctx context.Context, ws string, filter store.DriverFilter) ([]*domain.Driver, error) {
	ctx, span := startStoreSpan(ctx, "Drivers", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverStore) Update(ctx context.Context, ws, driverID string, patch store.DriverUpdate) (*domain.Driver, error) {
	ctx, span := startStoreSpan(ctx, "Drivers", "Update",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Update(ctx, ws, driverID, patch)
	finish(span, err)
	return out, err
}

// --- DriverVersionStore ---

type tracedDriverVersionStore struct{ inner store.DriverVersionStore }

func (t *tracedDriverVersionStore) Create(ctx context.Context, in store.DriverVersionCreate) (*domain.DriverVersion, error) {
	ctx, span := startStoreSpan(ctx, "DriverVersions", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverVersionStore) Get(ctx context.Context, ws, versionID string) (*domain.DriverVersion, error) {
	ctx, span := startStoreSpan(ctx, "DriverVersions", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, versionID)
	finish(span, err)
	return out, err
}

func (t *tracedDriverVersionStore) List(ctx context.Context, ws string, filter store.DriverVersionFilter) ([]*domain.DriverVersion, error) {
	ctx, span := startStoreSpan(ctx, "DriverVersions", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

// --- WorkerProfileStore ---

type tracedWorkerProfileStore struct{ inner store.WorkerProfileStore }

func (t *tracedWorkerProfileStore) Create(ctx context.Context, in store.WorkerProfileCreate) (*domain.WorkerProfile, error) {
	ctx, span := startStoreSpan(ctx, "WorkerProfiles", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.profile", in.ProfileID),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedWorkerProfileStore) Get(ctx context.Context, ws, profileID string) (*domain.WorkerProfile, error) {
	ctx, span := startStoreSpan(ctx, "WorkerProfiles", "Get",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
	out, err := t.inner.Get(ctx, ws, profileID)
	finish(span, err)
	return out, err
}

func (t *tracedWorkerProfileStore) List(ctx context.Context, ws string, filter store.WorkerProfileFilter) ([]*domain.WorkerProfile, error) {
	ctx, span := startStoreSpan(ctx, "WorkerProfiles", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedWorkerProfileStore) Update(ctx context.Context, ws, profileID string, patch store.WorkerProfileUpdate) (*domain.WorkerProfile, error) {
	ctx, span := startStoreSpan(ctx, "WorkerProfiles", "Update",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
	out, err := t.inner.Update(ctx, ws, profileID, patch)
	finish(span, err)
	return out, err
}

func (t *tracedWorkerProfileStore) Delete(ctx context.Context, ws, profileID string) error {
	ctx, span := startStoreSpan(ctx, "WorkerProfiles", "Delete",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
	err := t.inner.Delete(ctx, ws, profileID)
	finish(span, err)
	return err
}

// --- AgentServiceStore ---

type tracedAgentServiceStore struct{ inner store.AgentServiceStore }

func (t *tracedAgentServiceStore) Create(ctx context.Context, in store.AgentServiceCreate) (*domain.AgentService, error) {
	ctx, span := startStoreSpan(ctx, "AgentServices", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.service", in.ServiceID),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedAgentServiceStore) Get(ctx context.Context, ws, serviceID string) (*domain.AgentService, error) {
	ctx, span := startStoreSpan(ctx, "AgentServices", "Get",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
	out, err := t.inner.Get(ctx, ws, serviceID)
	finish(span, err)
	return out, err
}

func (t *tracedAgentServiceStore) List(ctx context.Context, ws string, filter store.AgentServiceFilter) ([]*domain.AgentService, error) {
	ctx, span := startStoreSpan(ctx, "AgentServices", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedAgentServiceStore) Update(ctx context.Context, ws, serviceID string, patch store.AgentServiceUpdate) (*domain.AgentService, error) {
	ctx, span := startStoreSpan(ctx, "AgentServices", "Update",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
	out, err := t.inner.Update(ctx, ws, serviceID, patch)
	finish(span, err)
	return out, err
}

func (t *tracedAgentServiceStore) Delete(ctx context.Context, ws, serviceID string) error {
	ctx, span := startStoreSpan(ctx, "AgentServices", "Delete",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
	err := t.inner.Delete(ctx, ws, serviceID)
	finish(span, err)
	return err
}

// --- TriggerBindingStore ---

type tracedTriggerBindingStore struct{ inner store.TriggerBindingStore }

func (t *tracedTriggerBindingStore) Create(ctx context.Context, in store.TriggerBindingCreate) (*domain.TriggerBinding, error) {
	ctx, span := startStoreSpan(ctx, "TriggerBindings", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.binding", in.BindingID),
		attribute.String("loom.route_key", in.RouteKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedTriggerBindingStore) Get(ctx context.Context, ws, bindingID string) (*domain.TriggerBinding, error) {
	ctx, span := startStoreSpan(ctx, "TriggerBindings", "Get",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.binding", bindingID),
	)
	out, err := t.inner.Get(ctx, ws, bindingID)
	finish(span, err)
	return out, err
}

func (t *tracedTriggerBindingStore) GetByRouteKey(ctx context.Context, ws, routeKey string) (*domain.TriggerBinding, error) {
	ctx, span := startStoreSpan(ctx, "TriggerBindings", "GetByRouteKey",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.route_key", routeKey),
	)
	out, err := t.inner.GetByRouteKey(ctx, ws, routeKey)
	finish(span, err)
	return out, err
}

func (t *tracedTriggerBindingStore) List(ctx context.Context, ws string, filter store.TriggerBindingFilter) ([]*domain.TriggerBinding, error) {
	ctx, span := startStoreSpan(ctx, "TriggerBindings", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedTriggerBindingStore) Update(ctx context.Context, ws, bindingID string, patch store.TriggerBindingUpdate) (*domain.TriggerBinding, error) {
	ctx, span := startStoreSpan(ctx, "TriggerBindings", "Update",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.binding", bindingID),
	)
	out, err := t.inner.Update(ctx, ws, bindingID, patch)
	finish(span, err)
	return out, err
}

// --- DriverRunStore ---

type tracedDriverRunStore struct{ inner store.DriverRunStore }

func (t *tracedDriverRunStore) Create(ctx context.Context, in store.DriverRunCreate) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) CreateEpic(ctx context.Context, ws, epicID string, in store.EpicRunCreate) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "CreateEpic",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.epic", epicID),
	)
	out, err := t.inner.CreateEpic(ctx, ws, epicID, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) Get(ctx context.Context, ws, runID string) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, runID)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) List(ctx context.Context, ws string, filter store.DriverRunFilter) ([]*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) Events(ctx context.Context, ws, runID, after string, limit int) (*domain.PlatformEventsPage, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Events",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run", runID),
	)
	reader, ok := t.inner.(store.DriverRunEventsReader)
	if !ok {
		finish(span, store.ErrDriverRunEventsUnavailable)
		return nil, store.ErrDriverRunEventsUnavailable
	}
	out, err := reader.Events(ctx, ws, runID, after, limit)
	if err == nil && out != nil {
		span.SetAttributes(attribute.Int("result.count", len(out.Events)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) Claim(ctx context.Context, ws, runID, nodeID, leaseID string) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Claim",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Claim(ctx, ws, runID, nodeID, leaseID)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) Heartbeat(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Heartbeat",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Heartbeat(ctx, ws, runID, nodeID, leaseID, fencingToken)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) Finish(ctx context.Context, ws, runID string, finishRun store.DriverRunFinish) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Finish",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Finish(ctx, ws, runID, finishRun)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) RecoverStale(ctx context.Context, ws string, recover store.StaleDriverRunRecovery) (*store.StaleDriverRunRecoveryResult, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "RecoverStale",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.RecoverStale(ctx, ws, recover)
	if err == nil && out != nil {
		span.SetAttributes(
			attribute.Int("loom.driver_runs.recovered", out.Recovered),
			attribute.Int("loom.driver_runs.skipped_fresh", out.SkippedFresh),
		)
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) RecoverStaleTaskRuns(ctx context.Context, ws, runID string, recover store.StaleTaskRunRecovery) (*store.StaleTaskRunRecoveryResult, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "RecoverStaleTaskRuns",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run", runID),
	)
	out, err := t.inner.RecoverStaleTaskRuns(ctx, ws, runID, recover)
	if err == nil && out != nil {
		span.SetAttributes(
			attribute.Int("loom.task_runs.recovered", out.Recovered),
			attribute.Int("loom.tasks.released", out.Released),
		)
	}
	finish(span, err)
	return out, err
}

// --- DriverStepStore ---

type tracedDriverStepStore struct{ inner store.DriverStepStore }

func (t *tracedDriverStepStore) Create(ctx context.Context, in store.DriverStepCreate) (*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.driver_run_id", in.DriverRunID),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverStepStore) CreateForRun(ctx context.Context, ws, runID string, in store.DriverStepCreate) (*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "CreateForRun",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run_id", runID),
	)
	out, err := t.inner.CreateForRun(ctx, ws, runID, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverStepStore) Get(ctx context.Context, ws, stepID string) (*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, stepID)
	finish(span, err)
	return out, err
}

func (t *tracedDriverStepStore) List(ctx context.Context, ws string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverStepStore) ListForRun(ctx context.Context, ws, runID string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "ListForRun",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run_id", runID),
	)
	out, err := t.inner.ListForRun(ctx, ws, runID, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverStepStore) Update(ctx context.Context, ws, stepID string, update store.DriverStepUpdate) (*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "Update",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Update(ctx, ws, stepID, update)
	finish(span, err)
	return out, err
}

// --- TaskRunStore ---

type tracedTaskRunStore struct{ inner store.TaskRunStore }

func (t *tracedTaskRunStore) Create(ctx context.Context, in store.TaskRunCreate) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) ClaimQueued(ctx context.Context, ws string, claim store.TaskRunClaim) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "ClaimQueued",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.ClaimQueued(ctx, ws, claim)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) Get(ctx context.Context, ws, taskRunID string) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, taskRunID)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) List(ctx context.Context, ws string, filter store.TaskRunFilter) ([]*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) Heartbeat(ctx context.Context, ws, taskRunID string, heartbeat store.TaskRunHeartbeat) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "Heartbeat",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Heartbeat(ctx, ws, taskRunID, heartbeat)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) Finish(ctx context.Context, ws, taskRunID string, taskFinish store.TaskRunFinish) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "Finish",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Finish(ctx, ws, taskRunID, taskFinish)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) Complete(ctx context.Context, ws, taskRunID string, taskComplete store.TaskRunComplete) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "Complete",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Complete(ctx, ws, taskRunID, taskComplete)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) AppendLog(ctx context.Context, ws, taskRunID string, appendLog store.TaskRunLogAppend) (*domain.TaskRunLogEntry, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "AppendLog",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.AppendLog(ctx, ws, taskRunID, appendLog)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) ListLogs(ctx context.Context, ws, taskRunID string, filter store.TaskRunLogFilter) ([]*domain.TaskRunLogEntry, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "ListLogs",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.ListLogs(ctx, ws, taskRunID, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

// --- DaemonProfileStore ---

type tracedDaemonStore struct{ inner store.DaemonProfileStore }

func (t *tracedDaemonStore) Get(ctx context.Context, ws string) (*domain.DaemonProfile, error) {
	ctx, span := startStoreSpan(ctx, "Daemon", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws)
	finish(span, err)
	return out, err
}

func (t *tracedDaemonStore) Upsert(ctx context.Context, profile *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	ws := ""
	if profile != nil {
		ws = profile.WorkspaceKey
	}
	ctx, span := startStoreSpan(ctx, "Daemon", "Upsert",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Upsert(ctx, profile)
	finish(span, err)
	return out, err
}

// --- WorkerStore ---

type tracedWorkerStore struct{ inner store.WorkerStore }

func (t *tracedWorkerStore) Heartbeat(ctx context.Context, ws, workerID string) error {
	ctx, span := startStoreSpan(ctx, "Workers", "Heartbeat",
		attribute.String("loom.workspace", ws),
	)
	err := t.inner.Heartbeat(ctx, ws, workerID)
	finish(span, err)
	return err
}

func (t *tracedWorkerStore) Deregister(ctx context.Context, ws, workerID string) error {
	ctx, span := startStoreSpan(ctx, "Workers", "Deregister",
		attribute.String("loom.workspace", ws),
	)
	err := t.inner.Deregister(ctx, ws, workerID)
	finish(span, err)
	return err
}
