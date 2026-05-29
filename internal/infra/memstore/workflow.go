package memstore

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type workflowStore struct {
	mu           sync.RWMutex
	seq          int64
	defVersions  map[string]map[string]*domain.DefinitionVersion
	workflowDefs map[string]map[string]*domain.WorkflowDefinition
	workflowRuns map[string]map[string]*domain.WorkflowRun
	taskRuns     map[string]map[string]*domain.TaskRun
	events       map[string][]*domain.RunEvent
	runtimes     map[string]map[string]*domain.RuntimeProfile
	routes       map[string]map[string]*domain.RouteBinding
	triggers     map[string]map[string]*domain.TriggerBinding
}

func newWorkflowStore() *workflowStore {
	return &workflowStore{
		defVersions:  make(map[string]map[string]*domain.DefinitionVersion),
		workflowDefs: make(map[string]map[string]*domain.WorkflowDefinition),
		workflowRuns: make(map[string]map[string]*domain.WorkflowRun),
		taskRuns:     make(map[string]map[string]*domain.TaskRun),
		events:       make(map[string][]*domain.RunEvent),
		runtimes:     make(map[string]map[string]*domain.RuntimeProfile),
		routes:       make(map[string]map[string]*domain.RouteBinding),
		triggers:     make(map[string]map[string]*domain.TriggerBinding),
	}
}

var (
	_ store.DefinitionVersionStore  = (*definitionVersionStore)(nil)
	_ store.WorkflowDefinitionStore = (*workflowDefinitionStore)(nil)
	_ store.WorkflowRunStore        = (*workflowRunStore)(nil)
	_ store.TaskRunStore            = (*taskRunStore)(nil)
	_ store.RunEventStore           = (*runEventStore)(nil)
	_ store.RuntimeProfileStore     = (*runtimeProfileStore)(nil)
	_ store.RouteBindingStore       = (*routeBindingStore)(nil)
	_ store.TriggerBindingStore     = (*triggerBindingStore)(nil)
)

type definitionVersionStore struct{ core *workflowStore }
type workflowDefinitionStore struct{ core *workflowStore }
type workflowRunStore struct{ core *workflowStore }
type taskRunStore struct{ core *workflowStore }
type runEventStore struct{ core *workflowStore }
type runtimeProfileStore struct{ core *workflowStore }
type routeBindingStore struct{ core *workflowStore }
type triggerBindingStore struct{ core *workflowStore }

func (s *definitionVersionStore) Apply(ctx context.Context, in store.DefinitionVersionApply) (*domain.DefinitionVersion, error) {
	return s.core.applyDefinitionVersion(ctx, in)
}

func (s *definitionVersionStore) Get(ctx context.Context, ws string, typ domain.DefinitionType, name, version string) (*domain.DefinitionVersion, error) {
	return s.core.getDefinitionVersion(ctx, ws, typ, name, version)
}

func (s *definitionVersionStore) List(ctx context.Context, ws string, filter store.DefinitionVersionFilter) ([]*domain.DefinitionVersion, error) {
	return s.core.listDefinitionVersions(ctx, ws, filter)
}

func (s *workflowDefinitionStore) Upsert(ctx context.Context, in store.WorkflowDefinitionUpsert) (*domain.WorkflowDefinition, error) {
	return s.core.upsertWorkflowDefinition(ctx, in)
}

func (s *workflowDefinitionStore) Get(ctx context.Context, ws, name string) (*domain.WorkflowDefinition, error) {
	return s.core.getWorkflowDefinition(ctx, ws, name)
}

func (s *workflowDefinitionStore) List(ctx context.Context, ws string, filter store.WorkflowDefinitionFilter) ([]*domain.WorkflowDefinition, error) {
	return s.core.listWorkflowDefinitions(ctx, ws, filter)
}

func (s *workflowRunStore) CreateOrResume(ctx context.Context, in store.WorkflowRunCreate) (*domain.WorkflowRun, error) {
	return s.core.createOrResumeWorkflowRun(ctx, in)
}

func (s *workflowRunStore) Get(ctx context.Context, ws, runID string) (*domain.WorkflowRun, error) {
	return s.core.getWorkflowRun(ctx, ws, runID)
}

func (s *workflowRunStore) List(ctx context.Context, ws string, filter store.WorkflowRunFilter) ([]*domain.WorkflowRun, error) {
	return s.core.listWorkflowRuns(ctx, ws, filter)
}

func (s *workflowRunStore) Update(ctx context.Context, ws, runID string, patch store.WorkflowRunUpdate) (*domain.WorkflowRun, error) {
	return s.core.updateWorkflowRun(ctx, ws, runID, patch)
}

func (s *taskRunStore) Ensure(ctx context.Context, in store.TaskRunEnsure) (*domain.TaskRun, error) {
	return s.core.ensureTaskRun(ctx, in)
}

func (s *taskRunStore) Get(ctx context.Context, ws, taskRunID string) (*domain.TaskRun, error) {
	return s.core.getTaskRun(ctx, ws, taskRunID)
}

func (s *taskRunStore) List(ctx context.Context, ws string, filter store.TaskRunFilter) ([]*domain.TaskRun, error) {
	return s.core.listTaskRuns(ctx, ws, filter)
}

func (s *taskRunStore) Update(ctx context.Context, ws, taskRunID string, patch store.TaskRunUpdate) (*domain.TaskRun, error) {
	return s.core.updateTaskRun(ctx, ws, taskRunID, patch)
}

func (s *runEventStore) Append(ctx context.Context, in store.RunEventAppend) (*domain.RunEvent, error) {
	return s.core.appendRunEvent(ctx, in)
}

func (s *runEventStore) List(ctx context.Context, ws string, filter store.RunEventFilter) ([]*domain.RunEvent, error) {
	return s.core.listRunEvents(ctx, ws, filter)
}

func (s *runtimeProfileStore) Upsert(ctx context.Context, in store.RuntimeProfileUpsert) (*domain.RuntimeProfile, error) {
	return s.core.upsertRuntimeProfile(ctx, in)
}

func (s *runtimeProfileStore) Get(ctx context.Context, ws, name string) (*domain.RuntimeProfile, error) {
	return s.core.getRuntimeProfile(ctx, ws, name)
}

func (s *runtimeProfileStore) List(ctx context.Context, ws string, filter store.RuntimeProfileFilter) ([]*domain.RuntimeProfile, error) {
	return s.core.listRuntimeProfiles(ctx, ws, filter)
}

func (s *routeBindingStore) Upsert(ctx context.Context, in store.RouteBindingUpsert) (*domain.RouteBinding, error) {
	return s.core.upsertRouteBinding(ctx, in)
}

func (s *routeBindingStore) Get(ctx context.Context, ws, bindingID string) (*domain.RouteBinding, error) {
	return s.core.getRouteBinding(ctx, ws, bindingID)
}

func (s *routeBindingStore) List(ctx context.Context, ws string, filter store.RouteBindingFilter) ([]*domain.RouteBinding, error) {
	return s.core.listRouteBindings(ctx, ws, filter)
}

func (s *triggerBindingStore) Upsert(ctx context.Context, in store.TriggerBindingUpsert) (*domain.TriggerBinding, error) {
	return s.core.upsertTriggerBinding(ctx, in)
}

func (s *triggerBindingStore) Get(ctx context.Context, ws, bindingID string) (*domain.TriggerBinding, error) {
	return s.core.getTriggerBinding(ctx, ws, bindingID)
}

func (s *triggerBindingStore) List(ctx context.Context, ws string, filter store.TriggerBindingFilter) ([]*domain.TriggerBinding, error) {
	return s.core.listTriggerBindings(ctx, ws, filter)
}

func (s *workflowStore) applyDefinitionVersion(_ context.Context, in store.DefinitionVersionApply) (*domain.DefinitionVersion, error) {
	if in.WorkspaceKey == "" || in.DefinitionType == "" || in.DefinitionName == "" {
		return nil, fmt.Errorf("workspace_key + definition_type + definition_name required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Version == "" {
		in.Version = versionFromHash(in.SourceHash)
	}
	if in.Status == "" {
		in.Status = domain.DefinitionStatusActive
	}
	if s.defVersions[in.WorkspaceKey] == nil {
		s.defVersions[in.WorkspaceKey] = make(map[string]*domain.DefinitionVersion)
	}
	key := definitionVersionKey(in.DefinitionType, in.DefinitionName, in.Version)
	if existing := s.defVersions[in.WorkspaceKey][key]; existing != nil {
		if existing.SourceHash == in.SourceHash && existing.BundleHash == in.BundleHash {
			return cloneDefinitionVersion(existing), nil
		}
		return nil, fmt.Errorf("definition version %s in workspace %q: %w", key, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	v := &domain.DefinitionVersion{
		WorkspaceKey:       in.WorkspaceKey,
		DefinitionType:     in.DefinitionType,
		DefinitionName:     in.DefinitionName,
		Version:            in.Version,
		SourceHash:         in.SourceHash,
		BundleHash:         in.BundleHash,
		Manifest:           cloneRaw(in.Manifest),
		CapabilityManifest: cloneRaw(in.CapabilityManifest),
		CreatedBy:          in.CreatedBy,
		Status:             in.Status,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	s.defVersions[in.WorkspaceKey][key] = v
	return cloneDefinitionVersion(v), nil
}

func (s *workflowStore) getDefinitionVersion(_ context.Context, ws string, typ domain.DefinitionType, name, version string) (*domain.DefinitionVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.defVersions[ws][definitionVersionKey(typ, name, version)]
	if v == nil {
		return nil, fmt.Errorf("definition version %s/%s/%s in workspace %q: %w", typ, name, version, ws, domain.ErrNotFound)
	}
	return cloneDefinitionVersion(v), nil
}

func (s *workflowStore) listDefinitionVersions(_ context.Context, ws string, filter store.DefinitionVersionFilter) ([]*domain.DefinitionVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.DefinitionVersion, 0, len(s.defVersions[ws]))
	for _, v := range s.defVersions[ws] {
		if filter.DefinitionType != "" && v.DefinitionType != filter.DefinitionType {
			continue
		}
		if filter.DefinitionName != "" && v.DefinitionName != filter.DefinitionName {
			continue
		}
		if filter.Status != "" && v.Status != filter.Status {
			continue
		}
		out = append(out, cloneDefinitionVersion(v))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DefinitionName == out[j].DefinitionName {
			return out[i].Version > out[j].Version
		}
		return out[i].DefinitionName < out[j].DefinitionName
	})
	return limitDefinitions(out, filter.Limit), nil
}

func (s *workflowStore) upsertWorkflowDefinition(_ context.Context, in store.WorkflowDefinitionUpsert) (*domain.WorkflowDefinition, error) {
	if in.WorkspaceKey == "" || in.Name == "" || in.Version == "" {
		return nil, fmt.Errorf("workspace_key + name + version required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Status == "" {
		in.Status = domain.DefinitionStatusActive
	}
	if s.workflowDefs[in.WorkspaceKey] == nil {
		s.workflowDefs[in.WorkspaceKey] = make(map[string]*domain.WorkflowDefinition)
	}
	now := time.Now().UTC()
	created := now
	if existing := s.workflowDefs[in.WorkspaceKey][in.Name]; existing != nil {
		created = existing.CreatedAt
	}
	def := &domain.WorkflowDefinition{
		WorkspaceKey:       in.WorkspaceKey,
		Name:               in.Name,
		Version:            in.Version,
		Description:        in.Description,
		InputSchema:        cloneRaw(in.InputSchema),
		ResultSchema:       cloneRaw(in.ResultSchema),
		SingletonPolicy:    in.SingletonPolicy,
		RuntimeProfileName: in.RuntimeProfileName,
		SourceRef:          in.SourceRef,
		BundleHash:         in.BundleHash,
		Manifest:           cloneRaw(in.Manifest),
		CapabilityManifest: cloneRaw(in.CapabilityManifest),
		Status:             in.Status,
		CreatedAt:          created,
		UpdatedAt:          now,
	}
	s.workflowDefs[in.WorkspaceKey][in.Name] = def
	return cloneWorkflowDefinition(def), nil
}

func (s *workflowStore) getWorkflowDefinition(_ context.Context, ws, name string) (*domain.WorkflowDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	def := s.workflowDefs[ws][name]
	if def == nil {
		return nil, fmt.Errorf("workflow definition %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	return cloneWorkflowDefinition(def), nil
}

func (s *workflowStore) listWorkflowDefinitions(_ context.Context, ws string, filter store.WorkflowDefinitionFilter) ([]*domain.WorkflowDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.WorkflowDefinition, 0, len(s.workflowDefs[ws]))
	for _, def := range s.workflowDefs[ws] {
		if filter.Status != "" && def.Status != filter.Status {
			continue
		}
		out = append(out, cloneWorkflowDefinition(def))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return limitWorkflowDefinitions(out, filter.Limit), nil
}

func (s *workflowStore) createOrResumeWorkflowRun(_ context.Context, in store.WorkflowRunCreate) (*domain.WorkflowRun, error) {
	if in.WorkspaceKey == "" || in.WorkflowName == "" {
		return nil, fmt.Errorf("workspace_key + workflow_name required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workflowRuns[in.WorkspaceKey] == nil {
		s.workflowRuns[in.WorkspaceKey] = make(map[string]*domain.WorkflowRun)
	}
	if in.IdempotencyKey != "" {
		for _, run := range s.workflowRuns[in.WorkspaceKey] {
			if run.WorkflowName == in.WorkflowName && run.IdempotencyKey == in.IdempotencyKey && domain.WorkflowRunStatusLive(run.Status) {
				return cloneWorkflowRun(run), nil
			}
		}
	}
	if in.RunID == "" {
		in.RunID = s.nextIDLocked("wrun")
	}
	if _, ok := s.workflowRuns[in.WorkspaceKey][in.RunID]; ok {
		return nil, fmt.Errorf("workflow run %q in workspace %q: %w", in.RunID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	status := in.Status
	if status == "" {
		status = domain.WorkflowRunQueued
	}
	run := &domain.WorkflowRun{
		WorkspaceKey:    in.WorkspaceKey,
		RunID:           in.RunID,
		WorkflowName:    in.WorkflowName,
		WorkflowVersion: in.WorkflowVersion,
		BundleHash:      in.BundleHash,
		IdempotencyKey:  in.IdempotencyKey,
		Input:           cloneRaw(in.Input),
		Status:          status,
		LeaseOwner:      in.LeaseOwner,
		LeaseToken:      in.LeaseToken,
		StartedAt:       in.StartedAt,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.workflowRuns[in.WorkspaceKey][in.RunID] = run
	return cloneWorkflowRun(run), nil
}

func (s *workflowStore) getWorkflowRun(_ context.Context, ws, runID string) (*domain.WorkflowRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run := s.workflowRuns[ws][runID]
	if run == nil {
		return nil, fmt.Errorf("workflow run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	return cloneWorkflowRun(run), nil
}

func (s *workflowStore) listWorkflowRuns(_ context.Context, ws string, filter store.WorkflowRunFilter) ([]*domain.WorkflowRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.WorkflowRun, 0, len(s.workflowRuns[ws]))
	for _, run := range s.workflowRuns[ws] {
		if filter.WorkflowName != "" && run.WorkflowName != filter.WorkflowName {
			continue
		}
		if filter.IdempotencyKey != "" && run.IdempotencyKey != filter.IdempotencyKey {
			continue
		}
		if filter.Status != "" && run.Status != filter.Status {
			continue
		}
		if filter.Live && !domain.WorkflowRunStatusLive(run.Status) {
			continue
		}
		out = append(out, cloneWorkflowRun(run))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return limitWorkflowRuns(out, filter.Limit), nil
}

func (s *workflowStore) updateWorkflowRun(_ context.Context, ws, runID string, patch store.WorkflowRunUpdate) (*domain.WorkflowRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.workflowRuns[ws][runID]
	if run == nil {
		return nil, fmt.Errorf("workflow run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	if patch.Status != nil {
		run.Status = *patch.Status
	}
	if patch.Result != nil {
		run.Result = cloneRaw(*patch.Result)
	}
	if patch.ErrorClass != nil {
		run.ErrorClass = *patch.ErrorClass
	}
	if patch.ErrorMessage != nil {
		run.ErrorMessage = *patch.ErrorMessage
	}
	if patch.WaitCondition != nil {
		run.WaitCondition = *patch.WaitCondition
	}
	if patch.LeaseOwner != nil {
		run.LeaseOwner = *patch.LeaseOwner
	}
	if patch.LeaseToken != nil {
		run.LeaseToken = *patch.LeaseToken
	}
	if patch.FencingToken != nil {
		run.FencingToken = *patch.FencingToken
	}
	if patch.StartedAt != nil {
		run.StartedAt = *patch.StartedAt
	}
	if patch.FinishedAt != nil {
		run.FinishedAt = clonePtr(*patch.FinishedAt)
	}
	run.UpdatedAt = time.Now().UTC()
	return cloneWorkflowRun(run), nil
}

//nolint:funlen // The in-memory store mirrors the full TaskRun record shape for test fidelity.
func (s *workflowStore) ensureTaskRun(_ context.Context, in store.TaskRunEnsure) (*domain.TaskRun, error) {
	if in.WorkspaceKey == "" || in.WorkflowRunID == "" || in.WorkItemID == "" || in.RoleName == "" {
		return nil, fmt.Errorf("workspace_key + workflow_run_id + work_item_id + role_name required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taskRuns[in.WorkspaceKey] == nil {
		s.taskRuns[in.WorkspaceKey] = make(map[string]*domain.TaskRun)
	}
	if in.IdempotencyKey == "" {
		in.IdempotencyKey = taskRunIdempotencyKey(in.WorkflowRunID, in.WorkItemID, in.RoleName)
	}
	attempt := 1
	for _, run := range s.taskRuns[in.WorkspaceKey] {
		same := run.WorkflowRunID == in.WorkflowRunID && run.WorkItemID == in.WorkItemID && run.RoleName == in.RoleName
		if !same && run.IdempotencyKey != in.IdempotencyKey {
			continue
		}
		if domain.TaskRunStatusLive(run.Status) {
			return cloneTaskRun(run), nil
		}
		if run.Attempt >= attempt {
			attempt = run.Attempt + 1
		}
	}
	if in.TaskRunID == "" {
		in.TaskRunID = s.nextIDLocked("trun")
	}
	if _, ok := s.taskRuns[in.WorkspaceKey][in.TaskRunID]; ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", in.TaskRunID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	status := in.Status
	if status == "" {
		status = domain.TaskRunQueued
	}
	run := &domain.TaskRun{
		WorkspaceKey:    in.WorkspaceKey,
		TaskRunID:       in.TaskRunID,
		IdempotencyKey:  in.IdempotencyKey,
		WorkflowRunID:   in.WorkflowRunID,
		WorkItemID:      in.WorkItemID,
		RoleName:        in.RoleName,
		ClaimActor:      in.ClaimActor,
		ClaimEventID:    in.ClaimEventID,
		Status:          status,
		Attempt:         attempt,
		AgentID:         in.AgentID,
		NodeID:          in.NodeID,
		CommandID:       in.CommandID,
		SessionID:       in.SessionID,
		LeaseID:         in.LeaseID,
		ParentSessionID: in.ParentSessionID,
		Reason:          in.Reason,
		Metadata:        cloneMap(in.Metadata),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.taskRuns[in.WorkspaceKey][in.TaskRunID] = run
	return cloneTaskRun(run), nil
}

func (s *workflowStore) getTaskRun(_ context.Context, ws, taskRunID string) (*domain.TaskRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run := s.taskRuns[ws][taskRunID]
	if run == nil {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotFound)
	}
	return cloneTaskRun(run), nil
}

func (s *workflowStore) listTaskRuns(_ context.Context, ws string, filter store.TaskRunFilter) ([]*domain.TaskRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.TaskRun, 0, len(s.taskRuns[ws]))
	for _, run := range s.taskRuns[ws] {
		if filter.WorkflowRunID != "" && run.WorkflowRunID != filter.WorkflowRunID {
			continue
		}
		if filter.WorkItemID != "" && run.WorkItemID != filter.WorkItemID {
			continue
		}
		if filter.RoleName != "" && run.RoleName != filter.RoleName {
			continue
		}
		if filter.AgentID != "" && run.AgentID != filter.AgentID {
			continue
		}
		if filter.Status != "" && run.Status != filter.Status {
			continue
		}
		if filter.Live && !domain.TaskRunStatusLive(run.Status) {
			continue
		}
		out = append(out, cloneTaskRun(run))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return limitTaskRuns(out, filter.Limit), nil
}

//nolint:funlen // Patch application is deliberately explicit for each persisted TaskRun field.
func (s *workflowStore) updateTaskRun(_ context.Context, ws, taskRunID string, patch store.TaskRunUpdate) (*domain.TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.taskRuns[ws][taskRunID]
	if run == nil {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotFound)
	}
	if patch.ClaimActor != nil {
		run.ClaimActor = *patch.ClaimActor
	}
	if patch.ClaimEventID != nil {
		run.ClaimEventID = *patch.ClaimEventID
	}
	if patch.Status != nil {
		run.Status = *patch.Status
	}
	if patch.AgentID != nil {
		run.AgentID = *patch.AgentID
	}
	if patch.NodeID != nil {
		run.NodeID = *patch.NodeID
	}
	if patch.CommandID != nil {
		run.CommandID = *patch.CommandID
	}
	if patch.SessionID != nil {
		run.SessionID = *patch.SessionID
	}
	if patch.LeaseID != nil {
		run.LeaseID = *patch.LeaseID
	}
	if patch.ParentSessionID != nil {
		run.ParentSessionID = *patch.ParentSessionID
	}
	if patch.Reason != nil {
		run.Reason = *patch.Reason
	}
	if patch.StartedAt != nil {
		run.StartedAt = *patch.StartedAt
	}
	if patch.FinishedAt != nil {
		run.FinishedAt = clonePtr(*patch.FinishedAt)
	}
	if patch.ErrorClass != nil {
		run.ErrorClass = *patch.ErrorClass
	}
	if patch.ErrorMessage != nil {
		run.ErrorMessage = *patch.ErrorMessage
	}
	if patch.Metadata != nil {
		run.Metadata = cloneMap(*patch.Metadata)
	}
	run.UpdatedAt = time.Now().UTC()
	return cloneTaskRun(run), nil
}

func (s *workflowStore) appendRunEvent(_ context.Context, in store.RunEventAppend) (*domain.RunEvent, error) {
	if in.WorkspaceKey == "" || in.WorkflowRunID == "" || in.Type == "" {
		return nil, fmt.Errorf("workspace_key + workflow_run_id + type required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var next int64 = 1
	for _, ev := range s.events[in.WorkspaceKey] {
		if ev.WorkflowRunID == in.WorkflowRunID && ev.EventIndex >= next {
			next = ev.EventIndex + 1
		}
	}
	if in.EventID == "" {
		in.EventID = s.nextIDLocked("rev")
	}
	ev := &domain.RunEvent{
		WorkspaceKey:  in.WorkspaceKey,
		EventID:       in.EventID,
		WorkflowRunID: in.WorkflowRunID,
		TaskRunID:     in.TaskRunID,
		EventIndex:    next,
		Type:          in.Type,
		Message:       in.Message,
		Data:          cloneRaw(in.Data),
		CreatedAt:     time.Now().UTC(),
	}
	s.events[in.WorkspaceKey] = append(s.events[in.WorkspaceKey], ev)
	return cloneRunEvent(ev), nil
}

func (s *workflowStore) listRunEvents(_ context.Context, ws string, filter store.RunEventFilter) ([]*domain.RunEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.RunEvent, 0, len(s.events[ws]))
	for _, ev := range s.events[ws] {
		if filter.WorkflowRunID != "" && ev.WorkflowRunID != filter.WorkflowRunID {
			continue
		}
		if filter.TaskRunID != "" && ev.TaskRunID != filter.TaskRunID {
			continue
		}
		if filter.AfterIndex > 0 && ev.EventIndex <= filter.AfterIndex {
			continue
		}
		out = append(out, cloneRunEvent(ev))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EventIndex < out[j].EventIndex })
	return limitRunEvents(out, filter.Limit), nil
}

func (s *workflowStore) upsertRuntimeProfile(_ context.Context, in store.RuntimeProfileUpsert) (*domain.RuntimeProfile, error) {
	if in.WorkspaceKey == "" || in.Name == "" || in.Version == "" {
		return nil, fmt.Errorf("workspace_key + name + version required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Status == "" {
		in.Status = domain.DefinitionStatusActive
	}
	if in.Provider == "" {
		in.Provider = domain.RuntimeProviderLocal
	}
	if s.runtimes[in.WorkspaceKey] == nil {
		s.runtimes[in.WorkspaceKey] = make(map[string]*domain.RuntimeProfile)
	}
	now := time.Now().UTC()
	created := now
	if existing := s.runtimes[in.WorkspaceKey][in.Name]; existing != nil {
		created = existing.CreatedAt
	}
	profile := &domain.RuntimeProfile{
		WorkspaceKey: in.WorkspaceKey,
		Name:         in.Name,
		Version:      in.Version,
		Provider:     in.Provider,
		Image:        in.Image,
		Repos:        append([]string(nil), in.Repos...),
		Env:          append([]string(nil), in.Env...),
		CPU:          in.CPU,
		Memory:       in.Memory,
		Manifest:     cloneRaw(in.Manifest),
		Status:       in.Status,
		CreatedAt:    created,
		UpdatedAt:    now,
	}
	s.runtimes[in.WorkspaceKey][in.Name] = profile
	return cloneRuntimeProfile(profile), nil
}

func (s *workflowStore) getRuntimeProfile(_ context.Context, ws, name string) (*domain.RuntimeProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile := s.runtimes[ws][name]
	if profile == nil {
		return nil, fmt.Errorf("runtime profile %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	return cloneRuntimeProfile(profile), nil
}

func (s *workflowStore) listRuntimeProfiles(_ context.Context, ws string, filter store.RuntimeProfileFilter) ([]*domain.RuntimeProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.RuntimeProfile, 0, len(s.runtimes[ws]))
	for _, profile := range s.runtimes[ws] {
		if filter.Status != "" && profile.Status != filter.Status {
			continue
		}
		out = append(out, cloneRuntimeProfile(profile))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return limitRuntimeProfiles(out, filter.Limit), nil
}

func (s *workflowStore) upsertRouteBinding(_ context.Context, in store.RouteBindingUpsert) (*domain.RouteBinding, error) {
	if in.WorkspaceKey == "" || in.DefinitionName == "" || in.Path == "" {
		return nil, fmt.Errorf("workspace_key + definition_name + path required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.BindingID == "" {
		in.BindingID = routeBindingID(in.DefinitionType, in.DefinitionName, in.Method, in.Path)
	}
	if in.Status == "" {
		in.Status = domain.DefinitionStatusActive
	}
	if s.routes[in.WorkspaceKey] == nil {
		s.routes[in.WorkspaceKey] = make(map[string]*domain.RouteBinding)
	}
	now := time.Now().UTC()
	created := now
	if existing := s.routes[in.WorkspaceKey][in.BindingID]; existing != nil {
		created = existing.CreatedAt
	}
	b := &domain.RouteBinding{
		WorkspaceKey:   in.WorkspaceKey,
		BindingID:      in.BindingID,
		DefinitionName: in.DefinitionName,
		DefinitionType: in.DefinitionType,
		Path:           in.Path,
		Method:         in.Method,
		AuthPolicy:     in.AuthPolicy,
		Status:         in.Status,
		CreatedAt:      created,
		UpdatedAt:      now,
	}
	s.routes[in.WorkspaceKey][in.BindingID] = b
	return cloneRouteBinding(b), nil
}

func (s *workflowStore) getRouteBinding(_ context.Context, ws, bindingID string) (*domain.RouteBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.routes[ws][bindingID]
	if b == nil {
		return nil, fmt.Errorf("route binding %q in workspace %q: %w", bindingID, ws, domain.ErrNotFound)
	}
	return cloneRouteBinding(b), nil
}

func (s *workflowStore) listRouteBindings(_ context.Context, ws string, filter store.RouteBindingFilter) ([]*domain.RouteBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.RouteBinding, 0, len(s.routes[ws]))
	for _, b := range s.routes[ws] {
		if filter.DefinitionName != "" && b.DefinitionName != filter.DefinitionName {
			continue
		}
		if filter.Status != "" && b.Status != filter.Status {
			continue
		}
		out = append(out, cloneRouteBinding(b))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BindingID < out[j].BindingID })
	return limitRouteBindings(out, filter.Limit), nil
}

func (s *workflowStore) upsertTriggerBinding(_ context.Context, in store.TriggerBindingUpsert) (*domain.TriggerBinding, error) {
	if in.WorkspaceKey == "" || in.WorkflowName == "" || in.EventType == "" {
		return nil, fmt.Errorf("workspace_key + workflow_name + event_type required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.BindingID == "" {
		in.BindingID = triggerBindingID(in.WorkflowName, in.EventType)
	}
	if in.Status == "" {
		in.Status = domain.DefinitionStatusActive
	}
	if s.triggers[in.WorkspaceKey] == nil {
		s.triggers[in.WorkspaceKey] = make(map[string]*domain.TriggerBinding)
	}
	now := time.Now().UTC()
	created := now
	if existing := s.triggers[in.WorkspaceKey][in.BindingID]; existing != nil {
		created = existing.CreatedAt
	}
	b := &domain.TriggerBinding{
		WorkspaceKey: in.WorkspaceKey,
		BindingID:    in.BindingID,
		WorkflowName: in.WorkflowName,
		EventType:    in.EventType,
		Filter:       cloneRaw(in.Filter),
		Status:       in.Status,
		CreatedAt:    created,
		UpdatedAt:    now,
	}
	s.triggers[in.WorkspaceKey][in.BindingID] = b
	return cloneTriggerBinding(b), nil
}

func (s *workflowStore) getTriggerBinding(_ context.Context, ws, bindingID string) (*domain.TriggerBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.triggers[ws][bindingID]
	if b == nil {
		return nil, fmt.Errorf("trigger binding %q in workspace %q: %w", bindingID, ws, domain.ErrNotFound)
	}
	return cloneTriggerBinding(b), nil
}

func (s *workflowStore) listTriggerBindings(_ context.Context, ws string, filter store.TriggerBindingFilter) ([]*domain.TriggerBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.TriggerBinding, 0, len(s.triggers[ws]))
	for _, b := range s.triggers[ws] {
		if filter.WorkflowName != "" && b.WorkflowName != filter.WorkflowName {
			continue
		}
		if filter.EventType != "" && b.EventType != filter.EventType {
			continue
		}
		if filter.Status != "" && b.Status != filter.Status {
			continue
		}
		out = append(out, cloneTriggerBinding(b))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BindingID < out[j].BindingID })
	return limitTriggerBindings(out, filter.Limit), nil
}

func (s *workflowStore) nextIDLocked(prefix string) string {
	s.seq++
	return prefix + "-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36) + "-" + strconv.FormatInt(s.seq, 36)
}

func definitionVersionKey(typ domain.DefinitionType, name, version string) string {
	return string(typ) + "/" + name + "/" + version
}

func versionFromHash(hash string) string {
	if len(hash) >= 12 {
		return hash[:12]
	}
	if hash != "" {
		return hash
	}
	return "v1"
}

func taskRunIdempotencyKey(workflowRunID, workItemID, role string) string {
	return "workflow_run:" + workflowRunID + ":work_item:" + workItemID + ":role:" + role
}

func routeBindingID(typ domain.DefinitionType, name, method, path string) string {
	if method == "" {
		method = "ANY"
	}
	return string(typ) + ":" + name + ":" + method + ":" + path
}

func triggerBindingID(workflowName, eventType string) string {
	return "workflow:" + workflowName + ":" + eventType
}

func cloneRaw(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func cloneDefinitionVersion(in *domain.DefinitionVersion) *domain.DefinitionVersion {
	out := *in
	out.Manifest = cloneRaw(in.Manifest)
	out.CapabilityManifest = cloneRaw(in.CapabilityManifest)
	return &out
}

func cloneWorkflowDefinition(in *domain.WorkflowDefinition) *domain.WorkflowDefinition {
	out := *in
	out.InputSchema = cloneRaw(in.InputSchema)
	out.ResultSchema = cloneRaw(in.ResultSchema)
	out.Manifest = cloneRaw(in.Manifest)
	out.CapabilityManifest = cloneRaw(in.CapabilityManifest)
	return &out
}

func cloneWorkflowRun(in *domain.WorkflowRun) *domain.WorkflowRun {
	out := *in
	out.Input = cloneRaw(in.Input)
	out.Result = cloneRaw(in.Result)
	out.FinishedAt = clonePtr(in.FinishedAt)
	return &out
}

func cloneTaskRun(in *domain.TaskRun) *domain.TaskRun {
	out := *in
	out.FinishedAt = clonePtr(in.FinishedAt)
	out.Metadata = cloneMap(in.Metadata)
	return &out
}

func cloneRunEvent(in *domain.RunEvent) *domain.RunEvent {
	out := *in
	out.Data = cloneRaw(in.Data)
	return &out
}

func cloneRuntimeProfile(in *domain.RuntimeProfile) *domain.RuntimeProfile {
	out := *in
	out.Repos = append([]string(nil), in.Repos...)
	out.Env = append([]string(nil), in.Env...)
	out.Manifest = cloneRaw(in.Manifest)
	return &out
}

func cloneRouteBinding(in *domain.RouteBinding) *domain.RouteBinding {
	out := *in
	return &out
}

func cloneTriggerBinding(in *domain.TriggerBinding) *domain.TriggerBinding {
	out := *in
	out.Filter = cloneRaw(in.Filter)
	return &out
}

func limitDefinitions(in []*domain.DefinitionVersion, limit int) []*domain.DefinitionVersion {
	if limit > 0 && len(in) > limit {
		return in[:limit]
	}
	return in
}

func limitWorkflowDefinitions(in []*domain.WorkflowDefinition, limit int) []*domain.WorkflowDefinition {
	if limit > 0 && len(in) > limit {
		return in[:limit]
	}
	return in
}

func limitWorkflowRuns(in []*domain.WorkflowRun, limit int) []*domain.WorkflowRun {
	if limit > 0 && len(in) > limit {
		return in[:limit]
	}
	return in
}

func limitTaskRuns(in []*domain.TaskRun, limit int) []*domain.TaskRun {
	if limit > 0 && len(in) > limit {
		return in[:limit]
	}
	return in
}

func limitRunEvents(in []*domain.RunEvent, limit int) []*domain.RunEvent {
	if limit > 0 && len(in) > limit {
		return in[:limit]
	}
	return in
}

func limitRuntimeProfiles(in []*domain.RuntimeProfile, limit int) []*domain.RuntimeProfile {
	if limit > 0 && len(in) > limit {
		return in[:limit]
	}
	return in
}

func limitRouteBindings(in []*domain.RouteBinding, limit int) []*domain.RouteBinding {
	if limit > 0 && len(in) > limit {
		return in[:limit]
	}
	return in
}

func limitTriggerBindings(in []*domain.TriggerBinding, limit int) []*domain.TriggerBinding {
	if limit > 0 && len(in) > limit {
		return in[:limit]
	}
	return in
}
