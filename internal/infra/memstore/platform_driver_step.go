package memstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type driverStepStore struct {
	mu       sync.RWMutex
	items    map[string]map[string]*domain.DriverStep
	parent   *driverRunStore
	taskRuns *taskRunStore
}

func newDriverStepStore(parent *driverRunStore) *driverStepStore {
	return &driverStepStore{
		items:  make(map[string]map[string]*domain.DriverStep),
		parent: parent,
	}
}

var _ store.DriverStepStore = (*driverStepStore)(nil)

func (s *driverStepStore) Create(ctx context.Context, in store.DriverStepCreate) (*domain.DriverStep, error) {
	return s.create(ctx, in)
}

func (s *driverStepStore) CreateForRun(ctx context.Context, ws, runID string, in store.DriverStepCreate) (*domain.DriverStep, error) {
	in.WorkspaceKey = ws
	in.DriverRunID = runID
	return s.create(ctx, in)
}

func (s *driverStepStore) create(_ context.Context, in store.DriverStepCreate) (*domain.DriverStep, error) {
	if in.WorkspaceKey == "" || in.StepID == "" || in.DriverRunID == "" || in.StepKind == "" {
		return nil, fmt.Errorf("workspace_key + step_id + driver_run_id + step_kind required: %w", domain.ErrInvalid)
	}
	if in.Status != "" && !driverStepStatusValid(in.Status) {
		return nil, fmt.Errorf("driver step status %q: %w", in.Status, domain.ErrInvalid)
	}
	if err := validateDriverRunOwnerForStepMem(s.parent, in.WorkspaceKey, in.DriverRunID, in.NodeID, in.LeaseID, in.FencingToken); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.DriverStep)
	}
	if _, ok := s.items[in.WorkspaceKey][in.StepID]; ok {
		return nil, fmt.Errorf("driver step %q in workspace %q: %w", in.StepID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	status := in.Status
	if status == "" {
		status = domain.DriverStepQueued
	}
	now := time.Now().UTC()
	step := &domain.DriverStep{
		WorkspaceKey:   in.WorkspaceKey,
		StepID:         in.StepID,
		DriverRunID:    in.DriverRunID,
		StepKind:       in.StepKind,
		Status:         status,
		TaskRunID:      in.TaskRunID,
		ActionLedgerID: in.ActionLedgerID,
		ExternalRef:    in.ExternalRef,
		InputRef:       in.InputRef,
		OutputRef:      in.OutputRef,
		StartedAt:      in.StartedAt,
		EndedAt:        clonePtr(in.EndedAt),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if step.Status == domain.DriverStepRunning && step.StartedAt.IsZero() {
		step.StartedAt = now
	}
	if step.Status.IsTerminal() && step.EndedAt == nil {
		step.EndedAt = &now
	}
	s.items[in.WorkspaceKey][in.StepID] = step
	return cloneDriverStep(step), nil
}

func (s *driverStepStore) Get(_ context.Context, ws, stepID string) (*domain.DriverStep, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	step, ok := s.items[ws][stepID]
	if !ok {
		return nil, fmt.Errorf("driver step %q in workspace %q: %w", stepID, ws, domain.ErrNotFound)
	}
	return cloneDriverStep(step), nil
}

func (s *driverStepStore) List(_ context.Context, ws string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.DriverStep, 0, len(s.items[ws]))
	for _, step := range s.items[ws] {
		if driverStepMatchesMem(step, filter) {
			out = append(out, cloneDriverStep(step))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *driverStepStore) ListForRun(ctx context.Context, ws, runID string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	filter.DriverRunID = runID
	return s.List(ctx, ws, filter)
}

func (s *driverStepStore) Update(ctx context.Context, ws, stepID string, update store.DriverStepUpdate) (*domain.DriverStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	step, ok := s.items[ws][stepID]
	if !ok {
		return nil, fmt.Errorf("driver step %q in workspace %q: %w", stepID, ws, domain.ErrNotFound)
	}
	if err := validateDriverRunOwnerForStepMem(s.parent, ws, step.DriverRunID, update.NodeID, update.LeaseID, update.FencingToken); err != nil {
		return nil, err
	}
	if update.TaskRunID != nil && strings.TrimSpace(*update.TaskRunID) != "" && s.taskRuns != nil {
		taskRun, err := s.taskRuns.Get(ctx, ws, *update.TaskRunID)
		if err != nil {
			return nil, err
		}
		if taskRun.DriverRunID != step.DriverRunID || taskRun.DriverStepID != stepID {
			return nil, fmt.Errorf("task run %q does not point back to driver step %q: %w", *update.TaskRunID, stepID, domain.ErrInvalidTransition)
		}
	}
	if update.Status != nil {
		if !driverStepStatusValid(*update.Status) {
			return nil, fmt.Errorf("driver step status %q: %w", *update.Status, domain.ErrInvalid)
		}
		step.Status = *update.Status
	}
	applyDriverStepUpdateFieldsMem(step, update)
	now := time.Now().UTC()
	if step.Status == domain.DriverStepRunning && step.StartedAt.IsZero() {
		step.StartedAt = now
	}
	if step.Status.IsTerminal() && step.EndedAt == nil {
		step.EndedAt = &now
	}
	step.UpdatedAt = now
	return cloneDriverStep(step), nil
}

func applyDriverStepUpdateFieldsMem(step *domain.DriverStep, update store.DriverStepUpdate) {
	if update.TaskRunID != nil {
		step.TaskRunID = *update.TaskRunID
	}
	if update.ActionLedgerID != nil {
		step.ActionLedgerID = *update.ActionLedgerID
	}
	if update.ExternalRef != nil {
		step.ExternalRef = *update.ExternalRef
	}
	if update.InputRef != nil {
		step.InputRef = *update.InputRef
	}
	if update.OutputRef != nil {
		step.OutputRef = *update.OutputRef
	}
	if update.ClearStartedAt {
		step.StartedAt = time.Time{}
	}
	if update.StartedAt != nil {
		step.StartedAt = *update.StartedAt
	}
	if update.ClearEndedAt {
		step.EndedAt = nil
	}
	if update.EndedAt != nil {
		step.EndedAt = clonePtr(update.EndedAt)
	}
}

func validateDriverRunOwnerForStepMem(parent *driverRunStore, ws, runID, nodeID, leaseID string, fencingToken int64) error {
	if parent == nil {
		return nil
	}
	parent.mu.RLock()
	defer parent.mu.RUnlock()
	run, ok := parent.items[ws][runID]
	if !ok {
		return fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	if run.NodeID == "" && run.LeaseID == "" && run.FencingToken == 0 {
		return nil
	}
	if run.Status != domain.DriverRunRunning {
		return fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrInvalidTransition)
	}
	if strings.TrimSpace(nodeID) == "" || strings.TrimSpace(leaseID) == "" || fencingToken <= 0 {
		return fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotOwner)
	}
	if run.NodeID != nodeID || run.LeaseID != leaseID || run.FencingToken != fencingToken {
		return fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotOwner)
	}
	return nil
}

func driverStepStatusValid(status domain.DriverStepStatus) bool {
	switch status {
	case domain.DriverStepQueued, domain.DriverStepRunning, domain.DriverStepWaiting, domain.DriverStepCompleted, domain.DriverStepFailed, domain.DriverStepSkipped:
		return true
	default:
		return false
	}
}

func cloneDriverStep(s *domain.DriverStep) *domain.DriverStep {
	if s == nil {
		return nil
	}
	out := *s
	out.EndedAt = clonePtr(s.EndedAt)
	return &out
}

func driverStepMatchesMem(s *domain.DriverStep, f store.DriverStepFilter) bool {
	return (f.DriverRunID == "" || s.DriverRunID == f.DriverRunID) &&
		(f.TaskRunID == "" || s.TaskRunID == f.TaskRunID) &&
		(f.ActionLedgerID == "" || s.ActionLedgerID == f.ActionLedgerID) &&
		(f.StepKind == "" || s.StepKind == f.StepKind) &&
		(f.Status == "" || s.Status == f.Status)
}
