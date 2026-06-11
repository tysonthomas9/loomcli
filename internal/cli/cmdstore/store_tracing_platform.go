package cmdstore

// Traced wrappers for the platform stores (drivers, driver versions, worker
// profiles, agent services, trigger bindings, driver runs, driver steps,
// task runs), mirroring internal/store/platform_store.go. Shared span
// helpers live in store_tracing.go.

import (
	"context"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// --- DriverStore ---

type tracedDriverStore struct{ inner store.DriverStore }

func (t *tracedDriverStore) Create(ctx context.Context, in store.DriverCreate) (*domain.Driver, error) {
	return traced(ctx, "Drivers", "Create", func(ctx context.Context) (*domain.Driver, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedDriverStore) Get(ctx context.Context, ws, driverID string) (*domain.Driver, error) {
	return traced(ctx, "Drivers", "Get", func(ctx context.Context) (*domain.Driver, error) {
		return t.inner.Get(ctx, ws, driverID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverStore) List(ctx context.Context, ws string, filter store.DriverFilter) ([]*domain.Driver, error) {
	return tracedList(ctx, "Drivers", "List", func(ctx context.Context) ([]*domain.Driver, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverStore) Update(ctx context.Context, ws, driverID string, patch store.DriverUpdate) (*domain.Driver, error) {
	return traced(ctx, "Drivers", "Update", func(ctx context.Context) (*domain.Driver, error) {
		return t.inner.Update(ctx, ws, driverID, patch)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- DriverVersionStore ---

type tracedDriverVersionStore struct{ inner store.DriverVersionStore }

func (t *tracedDriverVersionStore) Create(ctx context.Context, in store.DriverVersionCreate) (*domain.DriverVersion, error) {
	return traced(ctx, "DriverVersions", "Create", func(ctx context.Context) (*domain.DriverVersion, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedDriverVersionStore) Get(ctx context.Context, ws, versionID string) (*domain.DriverVersion, error) {
	return traced(ctx, "DriverVersions", "Get", func(ctx context.Context) (*domain.DriverVersion, error) {
		return t.inner.Get(ctx, ws, versionID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverVersionStore) List(ctx context.Context, ws string, filter store.DriverVersionFilter) ([]*domain.DriverVersion, error) {
	return tracedList(ctx, "DriverVersions", "List", func(ctx context.Context) ([]*domain.DriverVersion, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- WorkerProfileStore ---

type tracedWorkerProfileStore struct{ inner store.WorkerProfileStore }

func (t *tracedWorkerProfileStore) Create(ctx context.Context, in store.WorkerProfileCreate) (*domain.WorkerProfile, error) {
	return traced(ctx, "WorkerProfiles", "Create", func(ctx context.Context) (*domain.WorkerProfile, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.profile", in.ProfileID),
	)
}

func (t *tracedWorkerProfileStore) Get(ctx context.Context, ws, profileID string) (*domain.WorkerProfile, error) {
	return traced(ctx, "WorkerProfiles", "Get", func(ctx context.Context) (*domain.WorkerProfile, error) {
		return t.inner.Get(ctx, ws, profileID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
}

func (t *tracedWorkerProfileStore) List(ctx context.Context, ws string, filter store.WorkerProfileFilter) ([]*domain.WorkerProfile, error) {
	return tracedList(ctx, "WorkerProfiles", "List", func(ctx context.Context) ([]*domain.WorkerProfile, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedWorkerProfileStore) Update(ctx context.Context, ws, profileID string, patch store.WorkerProfileUpdate) (*domain.WorkerProfile, error) {
	return traced(ctx, "WorkerProfiles", "Update", func(ctx context.Context) (*domain.WorkerProfile, error) {
		return t.inner.Update(ctx, ws, profileID, patch)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
}

func (t *tracedWorkerProfileStore) Delete(ctx context.Context, ws, profileID string) error {
	return tracedErr(ctx, "WorkerProfiles", func(ctx context.Context) error {
		return t.inner.Delete(ctx, ws, profileID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
}

// --- AgentServiceStore ---

type tracedAgentServiceStore struct{ inner store.AgentServiceStore }

func (t *tracedAgentServiceStore) Create(ctx context.Context, in store.AgentServiceCreate) (*domain.AgentService, error) {
	return traced(ctx, "AgentServices", "Create", func(ctx context.Context) (*domain.AgentService, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.service", in.ServiceID),
	)
}

func (t *tracedAgentServiceStore) Get(ctx context.Context, ws, serviceID string) (*domain.AgentService, error) {
	return traced(ctx, "AgentServices", "Get", func(ctx context.Context) (*domain.AgentService, error) {
		return t.inner.Get(ctx, ws, serviceID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
}

func (t *tracedAgentServiceStore) List(ctx context.Context, ws string, filter store.AgentServiceFilter) ([]*domain.AgentService, error) {
	return tracedList(ctx, "AgentServices", "List", func(ctx context.Context) ([]*domain.AgentService, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentServiceStore) Update(ctx context.Context, ws, serviceID string, patch store.AgentServiceUpdate) (*domain.AgentService, error) {
	return traced(ctx, "AgentServices", "Update", func(ctx context.Context) (*domain.AgentService, error) {
		return t.inner.Update(ctx, ws, serviceID, patch)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
}

func (t *tracedAgentServiceStore) Delete(ctx context.Context, ws, serviceID string) error {
	return tracedErr(ctx, "AgentServices", func(ctx context.Context) error {
		return t.inner.Delete(ctx, ws, serviceID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
}

// --- TriggerBindingStore ---

type tracedTriggerBindingStore struct{ inner store.TriggerBindingStore }

func (t *tracedTriggerBindingStore) Create(ctx context.Context, in store.TriggerBindingCreate) (*domain.TriggerBinding, error) {
	return traced(ctx, "TriggerBindings", "Create", func(ctx context.Context) (*domain.TriggerBinding, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.binding", in.BindingID),
		attribute.String("loom.route_key", in.RouteKey),
	)
}

func (t *tracedTriggerBindingStore) Get(ctx context.Context, ws, bindingID string) (*domain.TriggerBinding, error) {
	return traced(ctx, "TriggerBindings", "Get", func(ctx context.Context) (*domain.TriggerBinding, error) {
		return t.inner.Get(ctx, ws, bindingID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.binding", bindingID),
	)
}

func (t *tracedTriggerBindingStore) GetByRouteKey(ctx context.Context, ws, routeKey string) (*domain.TriggerBinding, error) {
	return traced(ctx, "TriggerBindings", "GetByRouteKey", func(ctx context.Context) (*domain.TriggerBinding, error) {
		return t.inner.GetByRouteKey(ctx, ws, routeKey)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.route_key", routeKey),
	)
}

func (t *tracedTriggerBindingStore) List(ctx context.Context, ws string, filter store.TriggerBindingFilter) ([]*domain.TriggerBinding, error) {
	return tracedList(ctx, "TriggerBindings", "List", func(ctx context.Context) ([]*domain.TriggerBinding, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTriggerBindingStore) Update(ctx context.Context, ws, bindingID string, patch store.TriggerBindingUpdate) (*domain.TriggerBinding, error) {
	return traced(ctx, "TriggerBindings", "Update", func(ctx context.Context) (*domain.TriggerBinding, error) {
		return t.inner.Update(ctx, ws, bindingID, patch)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.binding", bindingID),
	)
}

// --- DriverRunStore ---

type tracedDriverRunStore struct{ inner store.DriverRunStore }

func (t *tracedDriverRunStore) Create(ctx context.Context, in store.DriverRunCreate) (*domain.DriverRun, error) {
	return traced(ctx, "DriverRuns", "Create", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedDriverRunStore) CreateEpic(ctx context.Context, ws, epicID string, in store.EpicRunCreate) (*domain.DriverRun, error) {
	return traced(ctx, "DriverRuns", "CreateEpic", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.CreateEpic(ctx, ws, epicID, in)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.epic", epicID),
	)
}

func (t *tracedDriverRunStore) Get(ctx context.Context, ws, runID string) (*domain.DriverRun, error) {
	return traced(ctx, "DriverRuns", "Get", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.Get(ctx, ws, runID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverRunStore) List(ctx context.Context, ws string, filter store.DriverRunFilter) ([]*domain.DriverRun, error) {
	return tracedList(ctx, "DriverRuns", "List", func(ctx context.Context) ([]*domain.DriverRun, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
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
	return traced(ctx, "DriverRuns", "Claim", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.Claim(ctx, ws, runID, nodeID, leaseID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverRunStore) Heartbeat(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64) (*domain.DriverRun, error) {
	return traced(ctx, "DriverRuns", "Heartbeat", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.Heartbeat(ctx, ws, runID, nodeID, leaseID, fencingToken)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverRunStore) Finish(ctx context.Context, ws, runID string, finishRun store.DriverRunFinish) (*domain.DriverRun, error) {
	return traced(ctx, "DriverRuns", "Finish", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.Finish(ctx, ws, runID, finishRun)
	},
		attribute.String("loom.workspace", ws),
	)
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
	return traced(ctx, "DriverSteps", "Create", func(ctx context.Context) (*domain.DriverStep, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.driver_run_id", in.DriverRunID),
	)
}

func (t *tracedDriverStepStore) CreateForRun(ctx context.Context, ws, runID string, in store.DriverStepCreate) (*domain.DriverStep, error) {
	return traced(ctx, "DriverSteps", "CreateForRun", func(ctx context.Context) (*domain.DriverStep, error) {
		return t.inner.CreateForRun(ctx, ws, runID, in)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run_id", runID),
	)
}

func (t *tracedDriverStepStore) Get(ctx context.Context, ws, stepID string) (*domain.DriverStep, error) {
	return traced(ctx, "DriverSteps", "Get", func(ctx context.Context) (*domain.DriverStep, error) {
		return t.inner.Get(ctx, ws, stepID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverStepStore) List(ctx context.Context, ws string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	return tracedList(ctx, "DriverSteps", "List", func(ctx context.Context) ([]*domain.DriverStep, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverStepStore) ListForRun(ctx context.Context, ws, runID string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	return tracedList(ctx, "DriverSteps", "ListForRun", func(ctx context.Context) ([]*domain.DriverStep, error) {
		return t.inner.ListForRun(ctx, ws, runID, filter)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run_id", runID),
	)
}

func (t *tracedDriverStepStore) Update(ctx context.Context, ws, stepID string, update store.DriverStepUpdate) (*domain.DriverStep, error) {
	return traced(ctx, "DriverSteps", "Update", func(ctx context.Context) (*domain.DriverStep, error) {
		return t.inner.Update(ctx, ws, stepID, update)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- TaskRunStore ---

type tracedTaskRunStore struct{ inner store.TaskRunStore }

func (t *tracedTaskRunStore) Create(ctx context.Context, in store.TaskRunCreate) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "Create", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedTaskRunStore) ClaimQueued(ctx context.Context, ws string, claim store.TaskRunClaim) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "ClaimQueued", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.ClaimQueued(ctx, ws, claim)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) Get(ctx context.Context, ws, taskRunID string) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "Get", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.Get(ctx, ws, taskRunID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) List(ctx context.Context, ws string, filter store.TaskRunFilter) ([]*domain.TaskRun, error) {
	return tracedList(ctx, "TaskRuns", "List", func(ctx context.Context) ([]*domain.TaskRun, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) Heartbeat(ctx context.Context, ws, taskRunID string, heartbeat store.TaskRunHeartbeat) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "Heartbeat", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.Heartbeat(ctx, ws, taskRunID, heartbeat)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) Finish(ctx context.Context, ws, taskRunID string, taskFinish store.TaskRunFinish) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "Finish", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.Finish(ctx, ws, taskRunID, taskFinish)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) Complete(ctx context.Context, ws, taskRunID string, taskComplete store.TaskRunComplete) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "Complete", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.Complete(ctx, ws, taskRunID, taskComplete)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) AppendLog(ctx context.Context, ws, taskRunID string, appendLog store.TaskRunLogAppend) (*domain.TaskRunLogEntry, error) {
	return traced(ctx, "TaskRuns", "AppendLog", func(ctx context.Context) (*domain.TaskRunLogEntry, error) {
		return t.inner.AppendLog(ctx, ws, taskRunID, appendLog)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) ListLogs(ctx context.Context, ws, taskRunID string, filter store.TaskRunLogFilter) ([]*domain.TaskRunLogEntry, error) {
	return tracedList(ctx, "TaskRuns", "ListLogs", func(ctx context.Context) ([]*domain.TaskRunLogEntry, error) {
		return t.inner.ListLogs(ctx, ws, taskRunID, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}
