package memstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type driverStepStore struct {
	mu       sync.RWMutex
	items    map[string]map[string]*execution.DriverStepRecord
	parent   *driverRunStore
	taskRuns *taskRunStore
}

func newDriverStepStore(parent *driverRunStore) *driverStepStore {
	return &driverStepStore{
		items:  make(map[string]map[string]*execution.DriverStepRecord),
		parent: parent,
	}
}

var _ execution.DriverStepStore = (*driverStepStore)(nil)
var _ execution.TerminalDriverStepRepairStore = (*driverStepStore)(nil)

func (s *driverStepStore) Create(ctx context.Context, in execution.DriverStepCreate) (*execution.DriverStepRecord, error) {
	return s.create(ctx, in)
}

func (s *driverStepStore) CreateForRun(ctx context.Context, ws, runID string, in execution.DriverStepCreate) (*execution.DriverStepRecord, error) {
	in.WorkspaceKey = ws
	in.DriverRunID = runID
	return s.create(ctx, in)
}

func (s *driverStepStore) create(_ context.Context, in execution.DriverStepCreate) (*execution.DriverStepRecord, error) {
	if in.WorkspaceKey == "" || in.StepID == "" || in.DriverRunID == "" || in.StepKind == "" {
		return nil, fmt.Errorf("workspace_key + step_id + driver_run_id + step_kind required: %w", persistence.ErrInvalid)
	}
	if in.Status != "" && !driverStepStatusValid(in.Status) {
		return nil, fmt.Errorf("driver step status %q: %w", in.Status, persistence.ErrInvalid)
	}
	if err := validateDriverRunOwnerForStepMem(s.parent, in.WorkspaceKey, in.DriverRunID, in.NodeID, in.LeaseID, in.FencingToken); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*execution.DriverStepRecord)
	}
	if _, ok := s.items[in.WorkspaceKey][in.StepID]; ok {
		return nil, fmt.Errorf("driver step %q in workspace %q: %w", in.StepID, in.WorkspaceKey, persistence.ErrAlreadyExists)
	}
	status := in.Status
	if status == "" {
		status = execution.DriverStepQueued
	}
	now := time.Now().UTC()
	step := &execution.DriverStepRecord{
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
	if step.Status == execution.DriverStepRunning && step.StartedAt.IsZero() {
		step.StartedAt = now
	}
	if step.Status.IsTerminal() && step.EndedAt == nil {
		step.EndedAt = &now
	}
	s.items[in.WorkspaceKey][in.StepID] = step
	return cloneDriverStep(step), nil
}

func (s *driverStepStore) Get(_ context.Context, ws, stepID string) (*execution.DriverStepRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	step, ok := s.items[ws][stepID]
	if !ok {
		return nil, fmt.Errorf("driver step %q in workspace %q: %w", stepID, ws, persistence.ErrNotFound)
	}
	return cloneDriverStep(step), nil
}

func (s *driverStepStore) List(_ context.Context, ws string, filter execution.DriverStepFilter) ([]*execution.DriverStepRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*execution.DriverStepRecord, 0, len(s.items[ws]))
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

func (s *driverStepStore) ListForRun(ctx context.Context, ws, runID string, filter execution.DriverStepFilter) ([]*execution.DriverStepRecord, error) {
	filter.DriverRunID = runID
	return s.List(ctx, ws, filter)
}

func (s *driverStepStore) Update(ctx context.Context, ws, stepID string, update execution.DriverStepUpdate) (*execution.DriverStepRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	step, ok := s.items[ws][stepID]
	if !ok {
		return nil, fmt.Errorf("driver step %q in workspace %q: %w", stepID, ws, persistence.ErrNotFound)
	}
	if err := validateDriverRunOwnerForStepMem(s.parent, ws, step.DriverRunID, update.NodeID, update.LeaseID, update.FencingToken); err != nil {
		return nil, err
	}
	if err := s.validateDriverStepUpdateMem(ctx, ws, stepID, step, update); err != nil {
		return nil, err
	}
	applyDriverStepUpdateMem(step, update, time.Now().UTC())
	return cloneDriverStep(step), nil
}

func (s *driverStepStore) RepairTerminalDriverStep(ctx context.Context, repair execution.TerminalDriverStepRepair) (*execution.DriverStepRecord, bool, error) {
	if !terminalDriverStepRepairValid(repair) {
		return nil, false, fmt.Errorf("terminal DriverStep repair: %w", persistence.ErrInvalid)
	}
	taskRun, err := s.taskRuns.Get(ctx, repair.WorkspaceKey, repair.TaskRunID)
	if err != nil {
		return nil, false, err
	}
	if !taskRun.Status.IsTerminal() || taskRun.DriverRunID != repair.DriverRunID || taskRun.DriverStepID != repair.DriverStepID {
		return nil, false, fmt.Errorf("terminal DriverStep repair TaskRun linkage mismatch: %w", persistence.ErrInvalidTransition)
	}
	wantStatus := terminalDriverStepStatusForTaskRun(taskRun.Status)
	if repair.Status != wantStatus {
		return nil, false, fmt.Errorf("terminal DriverStep repair status %q does not match TaskRun status %q: %w", repair.Status, taskRun.Status, persistence.ErrInvalidTransition)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	step, ok := s.items[repair.WorkspaceKey][repair.DriverStepID]
	if !ok {
		return nil, false, fmt.Errorf("driver step %q in workspace %q: %w", repair.DriverStepID, repair.WorkspaceKey, persistence.ErrNotFound)
	}
	if step.DriverRunID != repair.DriverRunID || (step.TaskRunID != "" && step.TaskRunID != repair.TaskRunID) {
		return nil, false, fmt.Errorf("terminal DriverStep repair linkage conflict: %w", persistence.ErrConflict)
	}
	if step.Status.IsTerminal() {
		if step.Status != repair.Status || step.TaskRunID != repair.TaskRunID || step.OutputRef != repair.OutputRef {
			return nil, false, fmt.Errorf("terminal DriverStep repair conflicts with existing terminal projection: %w", persistence.ErrConflict)
		}
		return cloneDriverStep(step), true, nil
	}
	now := repair.RepairedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	step.Status = repair.Status
	step.TaskRunID = repair.TaskRunID
	step.OutputRef = repair.OutputRef
	step.EndedAt = &now
	step.UpdatedAt = now
	return cloneDriverStep(step), false, nil
}

func terminalDriverStepRepairValid(repair execution.TerminalDriverStepRepair) bool {
	for _, value := range []string{repair.RequestID, repair.WorkspaceKey, repair.DriverRunID, repair.DriverStepID, repair.TaskRunID} {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return repair.Status.IsTerminal()
}

func terminalDriverStepStatusForTaskRun(status execution.TaskRunRecordStatus) execution.DriverStepStatus {
	switch status {
	case execution.TaskRunRecordCompleted:
		return execution.DriverStepCompleted
	case execution.TaskRunRecordCancelled:
		return execution.DriverStepSkipped
	default:
		return execution.DriverStepFailed
	}
}

func (s *driverStepStore) validateDriverStepUpdateMem(ctx context.Context, ws, stepID string, step *execution.DriverStepRecord, update execution.DriverStepUpdate) error {
	if update.Status != nil && !driverStepStatusValid(*update.Status) {
		return fmt.Errorf("driver step status %q: %w", *update.Status, persistence.ErrInvalid)
	}
	if update.TaskRunID == nil || strings.TrimSpace(*update.TaskRunID) == "" || s.taskRuns == nil {
		return nil
	}
	taskRun, err := s.taskRuns.Get(ctx, ws, *update.TaskRunID)
	if err != nil {
		return err
	}
	if taskRun.DriverRunID != step.DriverRunID || taskRun.DriverStepID != stepID {
		return fmt.Errorf("task run %q does not point back to driver step %q: %w", *update.TaskRunID, stepID, persistence.ErrInvalidTransition)
	}
	return nil
}

func applyDriverStepUpdateMem(step *execution.DriverStepRecord, update execution.DriverStepUpdate, now time.Time) {
	if update.Status != nil {
		step.Status = *update.Status
	}
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
	if step.Status == execution.DriverStepRunning && step.StartedAt.IsZero() {
		step.StartedAt = now
	}
	if step.Status.IsTerminal() && step.EndedAt == nil {
		step.EndedAt = &now
	}
	step.UpdatedAt = now
}

func validateDriverRunOwnerForStepMem(parent *driverRunStore, ws, runID, nodeID, leaseID string, fencingToken int64) error {
	if parent == nil {
		return nil
	}
	parent.mu.RLock()
	defer parent.mu.RUnlock()
	run, ok := parent.items[ws][runID]
	if !ok {
		return fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, persistence.ErrNotFound)
	}
	if run.NodeID == "" && run.LeaseID == "" && run.FencingToken == 0 {
		return nil
	}
	if run.Status != execution.DriverRunRunning {
		return fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, persistence.ErrInvalidTransition)
	}
	if strings.TrimSpace(nodeID) == "" || strings.TrimSpace(leaseID) == "" || fencingToken <= 0 {
		return fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, persistence.ErrNotOwner)
	}
	if run.NodeID != nodeID || run.LeaseID != leaseID || run.FencingToken != fencingToken {
		return fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, persistence.ErrNotOwner)
	}
	return nil
}

func driverStepStatusValid(status execution.DriverStepStatus) bool {
	switch status {
	case execution.DriverStepQueued, execution.DriverStepRunning, execution.DriverStepWaiting, execution.DriverStepCompleted, execution.DriverStepFailed, execution.DriverStepSkipped:
		return true
	default:
		return false
	}
}
