package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type driverRunStore struct {
	mu       sync.RWMutex
	items    map[string]map[string]*domain.DriverRun
	versions *driverVersionStore
	bindings *triggerBindingStore
	taskRuns *taskRunStore
	steps    *driverStepStore
	next     int64
}

func newDriverRunStore(versions *driverVersionStore, bindings *triggerBindingStore) *driverRunStore {
	return &driverRunStore{items: make(map[string]map[string]*domain.DriverRun), versions: versions, bindings: bindings}
}

var _ store.DriverRunStore = (*driverRunStore)(nil)

func (s *driverRunStore) Create(_ context.Context, in store.DriverRunCreate) (*domain.DriverRun, error) {
	if in.WorkspaceKey == "" || in.RunID == "" || in.DriverID == "" || in.DriverVersionID == "" {
		return nil, fmt.Errorf("workspace_key + run_id + driver_id + driver_version_id required: %w", domain.ErrInvalid)
	}
	if s.versions != nil && !s.versions.belongsToDriver(in.WorkspaceKey, in.DriverVersionID, in.DriverID) {
		return nil, fmt.Errorf("driver version %q for driver %q in workspace %q: %w", in.DriverVersionID, in.DriverID, in.WorkspaceKey, domain.ErrNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.DriverRun)
	}
	if in.IdempotencyKey != "" {
		for _, run := range s.items[in.WorkspaceKey] {
			if run.IdempotencyKey == in.IdempotencyKey {
				return cloneDriverRun(run), nil
			}
		}
	}
	if in.EpicID != "" {
		for _, run := range s.items[in.WorkspaceKey] {
			if run.EpicID == in.EpicID && (run.Status == domain.DriverRunQueued || run.Status == domain.DriverRunRunning) {
				return cloneDriverRun(run), nil
			}
		}
	}
	if _, ok := s.items[in.WorkspaceKey][in.RunID]; ok {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", in.RunID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	run := &domain.DriverRun{
		WorkspaceKey:    in.WorkspaceKey,
		RunID:           in.RunID,
		DriverID:        in.DriverID,
		DriverVersionID: in.DriverVersionID,
		Entrypoint:      in.Entrypoint,
		SourceKind:      in.SourceKind,
		SourceRef:       in.SourceRef,
		EpicID:          in.EpicID,
		Status:          domain.DriverRunQueued,
		IdempotencyKey:  in.IdempotencyKey,
		Payload:         cloneJSON(in.Payload),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.items[in.WorkspaceKey][in.RunID] = run
	return cloneDriverRun(run), nil
}

func (s *driverRunStore) CreateEpic(ctx context.Context, ws, epicID string, in store.EpicRunCreate) (*domain.DriverRun, error) {
	if ws == "" || epicID == "" {
		return nil, fmt.Errorf("workspace_key + epic_id required: %w", domain.ErrInvalid)
	}
	if s.bindings == nil {
		return nil, fmt.Errorf("trigger binding route %q in workspace %q: %w", "epics.runs.create", ws, domain.ErrNotFound)
	}
	binding, err := s.bindings.GetByRouteKey(ctx, ws, "epics.runs.create")
	if err != nil {
		return nil, err
	}
	if !binding.Enabled {
		return nil, fmt.Errorf("trigger binding route %q in workspace %q: %w", "epics.runs.create", ws, domain.ErrNotFound)
	}
	runID := in.RunID
	if runID == "" {
		runID = fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())
	}
	return s.Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    ws,
		RunID:           runID,
		DriverID:        binding.DriverID,
		DriverVersionID: binding.DriverVersionID,
		Entrypoint:      binding.TargetEntrypoint,
		SourceKind:      binding.SourceKind,
		SourceRef:       binding.RouteKey,
		EpicID:          epicID,
		IdempotencyKey:  in.IdempotencyKey,
		Payload:         cloneJSON(in.Payload),
	})
}

func (s *driverRunStore) Get(_ context.Context, ws, runID string) (*domain.DriverRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.items[ws][runID]
	if !ok {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	return cloneDriverRun(run), nil
}

func (s *driverRunStore) Events(ctx context.Context, ws, runID, after string, limit int) (*domain.PlatformEventsPage, error) {
	run, err := s.Get(ctx, ws, runID)
	if err != nil {
		return nil, err
	}
	if after != "" && after != "0" {
		return &domain.PlatformEventsPage{Events: []domain.PlatformEvent{}, Cursor: after}, nil
	}
	action := "driver_run.create"
	if run.Status == domain.DriverRunRunning {
		action = "driver_run.claim"
	} else if run.Status.IsTerminal() {
		action = "driver_run.finish"
	}
	timestamp := run.UpdatedAt
	if timestamp.IsZero() {
		timestamp = run.CreatedAt
	}
	event := domain.PlatformEvent{
		ID:          "1-0",
		Timestamp:   timestamp,
		Actor:       "memstore",
		Action:      action,
		EntityType:  "driver_run",
		EntityID:    run.RunID,
		WorkspaceID: ws,
		Metadata: map[string]string{
			"driver_id":         run.DriverID,
			"driver_version_id": run.DriverVersionID,
			"source_kind":       run.SourceKind,
			"source_ref":        run.SourceRef,
		},
	}
	return &domain.PlatformEventsPage{Events: []domain.PlatformEvent{event}, Cursor: event.ID}, nil
}

func (s *driverRunStore) List(_ context.Context, ws string, filter store.DriverRunFilter) ([]*domain.DriverRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.DriverRun, 0, len(s.items[ws]))
	for _, run := range s.items[ws] {
		if driverRunMatchesMem(run, filter) {
			out = append(out, cloneDriverRun(run))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *driverRunStore) Claim(_ context.Context, ws, runID, nodeID, leaseID string) (*domain.DriverRun, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("node_id required: %w", domain.ErrInvalid)
	}
	if leaseID == "" {
		return nil, fmt.Errorf("lease_id required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][runID]
	if !ok {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	if run.NodeID != "" {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrAlreadyClaimed)
	}
	if run.Status != domain.DriverRunQueued {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrInvalidTransition)
	}
	now := time.Now().UTC()
	s.next++
	run.Status = domain.DriverRunRunning
	run.NodeID = nodeID
	run.LeaseID = leaseID
	run.FencingToken = s.next
	run.StartedAt = now
	run.LastHeartbeat = now
	run.UpdatedAt = now
	return cloneDriverRun(run), nil
}

func (s *driverRunStore) Heartbeat(_ context.Context, ws, runID, nodeID, leaseID string, fencingToken int64) (*domain.DriverRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][runID]
	if !ok {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	if run.NodeID != nodeID || run.LeaseID != leaseID || run.FencingToken != fencingToken {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotOwner)
	}
	if run.Status != domain.DriverRunRunning {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrInvalidTransition)
	}
	now := time.Now().UTC()
	run.LastHeartbeat = now
	run.UpdatedAt = now
	return cloneDriverRun(run), nil
}

func (s *driverRunStore) Finish(_ context.Context, ws, runID string, finish store.DriverRunFinish) (*domain.DriverRun, error) {
	if !finish.Status.IsTerminal() {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrInvalidTransition)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][runID]
	if !ok {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	if run.NodeID != finish.NodeID || run.LeaseID != finish.LeaseID || run.FencingToken != finish.FencingToken {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotOwner)
	}
	if run.Status != domain.DriverRunRunning {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrInvalidTransition)
	}
	now := time.Now().UTC()
	run.Status = finish.Status
	run.Summary = finish.Summary
	run.ErrorClass = finish.ErrorClass
	run.Output = cloneMap(finish.Output)
	run.FinishedAt = &now
	run.UpdatedAt = now
	return cloneDriverRun(run), nil
}

func (s *driverRunStore) RecoverStale(_ context.Context, ws string, recover store.StaleDriverRunRecovery) (*store.StaleDriverRunRecoveryResult, error) {
	plan, err := newStaleDriverRunRecoveryMem(ws, recover)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.items[ws] {
		if run.Status != domain.DriverRunRunning {
			continue
		}
		lastHeartbeat := driverRunRecoveryHeartbeatMem(run)
		if lastHeartbeat.IsZero() || !lastHeartbeat.Before(plan.staleBefore) {
			plan.result.SkippedFresh++
			plan.result.SkippedFreshRunIDs = append(plan.result.SkippedFreshRunIDs, run.RunID)
			continue
		}
		if recover.Limit > 0 && plan.result.Recovered >= recover.Limit {
			break
		}
		run.Status = domain.DriverRunFailed
		run.Summary = plan.summary
		run.ErrorClass = plan.errorClass
		run.FinishedAt = &plan.recoveredAt
		run.UpdatedAt = plan.recoveredAt
		plan.result.Recovered++
		plan.result.RecoveredRunIDs = append(plan.result.RecoveredRunIDs, run.RunID)
	}
	return plan.result, nil
}

type staleDriverRunRecoveryMem struct {
	result      *store.StaleDriverRunRecoveryResult
	recoveredAt time.Time
	staleBefore time.Time
	errorClass  string
	summary     string
}

func newStaleDriverRunRecoveryMem(ws string, recover store.StaleDriverRunRecovery) (*staleDriverRunRecoveryMem, error) {
	if ws == "" {
		return nil, fmt.Errorf("workspace_key required: %w", domain.ErrInvalid)
	}
	if recover.MaxAgeSeconds < 0 || recover.Limit < 0 {
		return nil, fmt.Errorf("stale driver run recovery values must be non-negative: %w", domain.ErrInvalid)
	}
	recoveredAt := time.Now().UTC()
	staleBefore := staleBeforeMem(recover.StaleBefore, recoveredAt, recover.MaxAgeSeconds)
	return &staleDriverRunRecoveryMem{
		result:      &store.StaleDriverRunRecoveryResult{WorkspaceKey: ws, StaleBefore: staleBefore, RecoveredAt: recoveredAt},
		recoveredAt: recoveredAt,
		staleBefore: staleBefore,
		errorClass:  firstNonEmptyMem(recover.ErrorClass, "stale_driver_run"),
		summary:     firstNonEmptyMem(recover.Summary, "driver run heartbeat stale before "+staleBefore.Format(time.RFC3339Nano)),
	}, nil
}

func staleBeforeMem(explicit time.Time, recoveredAt time.Time, maxAgeSeconds int64) time.Time {
	if !explicit.IsZero() {
		return explicit
	}
	maxAge := 5 * time.Minute
	if maxAgeSeconds > 0 {
		maxAge = time.Duration(maxAgeSeconds) * time.Second
	}
	return recoveredAt.Add(-maxAge)
}

func driverRunRecoveryHeartbeatMem(run *domain.DriverRun) time.Time {
	if run == nil {
		return time.Time{}
	}
	if !run.LastHeartbeat.IsZero() {
		return run.LastHeartbeat
	}
	if !run.UpdatedAt.IsZero() {
		return run.UpdatedAt
	}
	return run.CreatedAt
}

func (s *driverRunStore) RecoverStaleTaskRuns(_ context.Context, ws, runID string, recover store.StaleTaskRunRecovery) (*store.StaleTaskRunRecoveryResult, error) {
	plan, err := newStaleTaskRunRecoveryMem(ws, runID, recover)
	if err != nil {
		return nil, err
	}
	if err := s.ensureDriverRunExistsMem(ws, runID); err != nil {
		return nil, err
	}
	if s.taskRuns == nil {
		return plan.result, nil
	}

	s.taskRuns.mu.Lock()
	recoveredTaskRunIDs, recoveredStepIDs := recoverStaleTaskRunsLocked(s.taskRuns.items[ws], runID, plan)
	s.taskRuns.mu.Unlock()
	sort.Strings(plan.result.RecoveredTaskRunIDs)

	s.recoverLinkedDriverStepsMem(ws, runID, recoveredTaskRunIDs, recoveredStepIDs, plan.recoveredAt)
	return plan.result, nil
}

type staleTaskRunRecoveryMem struct {
	result       *store.StaleTaskRunRecoveryResult
	recoveredAt  time.Time
	staleBefore  time.Time
	errorClass   string
	errorMessage string
}

func newStaleTaskRunRecoveryMem(ws, runID string, recover store.StaleTaskRunRecovery) (*staleTaskRunRecoveryMem, error) {
	if ws == "" || runID == "" {
		return nil, fmt.Errorf("workspace_key + run_id required: %w", domain.ErrInvalid)
	}
	if recover.MaxAgeSeconds < 0 {
		return nil, fmt.Errorf("max_age_seconds must be non-negative: %w", domain.ErrInvalid)
	}
	recoveredAt := time.Now().UTC()
	staleBefore := staleBeforeMem(recover.StaleBefore, recoveredAt, recover.MaxAgeSeconds)
	return &staleTaskRunRecoveryMem{
		result:       &store.StaleTaskRunRecoveryResult{WorkspaceKey: ws, DriverRunID: runID, StaleBefore: staleBefore.UTC(), RecoveredAt: recoveredAt},
		recoveredAt:  recoveredAt,
		staleBefore:  staleBefore,
		errorClass:   firstNonEmptyMem(recover.ErrorClass, "stale_task_run"),
		errorMessage: firstNonEmptyMem(recover.ErrorMessage, "task run heartbeat is stale"),
	}, nil
}

func (s *driverRunStore) ensureDriverRunExistsMem(ws, runID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.items[ws][runID]; !ok {
		return fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	return nil
}

func recoverStaleTaskRunsLocked(taskRuns map[string]*domain.TaskRun, runID string, plan *staleTaskRunRecoveryMem) (map[string]struct{}, map[string]struct{}) {
	recoveredTaskRunIDs := make(map[string]struct{})
	recoveredStepIDs := make(map[string]struct{})
	for _, taskRun := range taskRuns {
		if !recoverStaleTaskRunMem(taskRun, runID, plan) {
			continue
		}
		recoveredTaskRunIDs[taskRun.TaskRunID] = struct{}{}
		if taskRun.DriverStepID != "" {
			recoveredStepIDs[taskRun.DriverStepID] = struct{}{}
		}
	}
	return recoveredTaskRunIDs, recoveredStepIDs
}

func recoverStaleTaskRunMem(taskRun *domain.TaskRun, runID string, plan *staleTaskRunRecoveryMem) bool {
	if taskRun.DriverRunID != runID || taskRun.Status != domain.TaskRunRunning {
		return false
	}
	if !taskRunLastObservedMem(taskRun).Before(plan.staleBefore) {
		plan.result.SkippedFresh++
		return false
	}
	taskRun.Status = domain.TaskRunFailed
	taskRun.ErrorClass = plan.errorClass
	taskRun.ErrorMessage = plan.errorMessage
	taskRun.FinishedAt = &plan.recoveredAt
	taskRun.UpdatedAt = plan.recoveredAt
	plan.result.Recovered++
	plan.result.RecoveredTaskRunIDs = append(plan.result.RecoveredTaskRunIDs, taskRun.TaskRunID)
	return true
}

func (s *driverRunStore) recoverLinkedDriverStepsMem(ws, runID string, recoveredTaskRunIDs, recoveredStepIDs map[string]struct{}, recoveredAt time.Time) {
	if s.steps == nil || len(recoveredTaskRunIDs) == 0 {
		return
	}
	s.steps.mu.Lock()
	defer s.steps.mu.Unlock()
	for _, step := range s.steps.items[ws] {
		if driverStepRecoveredByTaskRunMem(step, runID, recoveredTaskRunIDs, recoveredStepIDs) {
			markDriverStepRecoveredMem(step, recoveredAt)
		}
	}
}

func driverStepRecoveredByTaskRunMem(step *domain.DriverStep, runID string, recoveredTaskRunIDs, recoveredStepIDs map[string]struct{}) bool {
	if step.DriverRunID != runID || step.Status.IsTerminal() {
		return false
	}
	_, linkedByStepID := recoveredStepIDs[step.StepID]
	_, linkedByTaskRunID := recoveredTaskRunIDs[step.TaskRunID]
	return linkedByStepID || linkedByTaskRunID
}

func markDriverStepRecoveredMem(step *domain.DriverStep, recoveredAt time.Time) {
	step.Status = domain.DriverStepFailed
	if step.EndedAt == nil {
		step.EndedAt = &recoveredAt
	}
	step.UpdatedAt = recoveredAt
}

func taskRunLastObservedMem(run *domain.TaskRun) time.Time {
	switch {
	case !run.LastHeartbeat.IsZero():
		return run.LastHeartbeat
	case !run.UpdatedAt.IsZero():
		return run.UpdatedAt
	default:
		return run.CreatedAt
	}
}

func (s *driverRunStore) exists(ws, runID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.items[ws][runID]
	return ok
}
