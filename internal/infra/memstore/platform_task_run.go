package memstore

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type taskRunStore struct {
	mu          sync.RWMutex
	items       map[string]map[string]*execution.TaskRunRecord
	logs        map[string]map[string][]*execution.TaskRunLogEntry
	logReceipts map[string]map[string]map[string]taskRunLogReceipt
	completions map[string]map[string]string
	// leaseTokenHashes gives memstore parity with FleetDB's opaque
	// X-Lease-Token verification without putting the secret on TaskRun DTOs.
	leaseTokenHashes map[string]map[string][sha256.Size]byte
	// blockedTasks records task IDs blocked via TaskRunFinish.BlockTask.
	// memstore has no issue model (issues live in fleet-db), so this is
	// the in-memory stand-in tests use to observe the block signal.
	blockedTasks map[string]map[string]bool
	parent       *driverRunStore
	steps        *driverStepStore
	artifacts    *artifactStore
	profiles     *workerProfileStore
	nodes        *nodeStore
}

type taskRunLogFingerprint struct {
	NodeID         string
	LeaseID        string
	LeaseTokenHash [sha256.Size]byte
	FencingToken   int64
	Stream         string
	Text           string
	Timestamp      string
}

type taskRunLogReceipt struct {
	fingerprint taskRunLogFingerprint
	entry       *execution.TaskRunLogEntry
}

func newTaskRunStore(parent *driverRunStore, steps *driverStepStore, artifacts *artifactStore, profiles *workerProfileStore, nodes *nodeStore) *taskRunStore {
	return &taskRunStore{
		items:            make(map[string]map[string]*execution.TaskRunRecord),
		logs:             make(map[string]map[string][]*execution.TaskRunLogEntry),
		logReceipts:      make(map[string]map[string]map[string]taskRunLogReceipt),
		completions:      make(map[string]map[string]string),
		leaseTokenHashes: make(map[string]map[string][sha256.Size]byte),
		blockedTasks:     make(map[string]map[string]bool),
		parent:           parent,
		steps:            steps,
		artifacts:        artifacts,
		profiles:         profiles,
		nodes:            nodes,
	}
}

var _ execution.TaskRunStore = (*taskRunStore)(nil)
var _ execution.TaskRunTerminalConvergenceStore = (*taskRunStore)(nil)

func (s *taskRunStore) ListTaskRunTerminalConvergenceCandidates(
	_ context.Context,
	query execution.TaskRunTerminalConvergenceQuery,
) (execution.TaskRunTerminalConvergencePage, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.After = strings.TrimSpace(query.After)
	if query.WorkspaceKey == "" || query.RequiredVersion <= 0 {
		return execution.TaskRunTerminalConvergencePage{}, fmt.Errorf("terminal convergence workspace and positive version are required: %w", persistence.ErrInvalid)
	}
	limit := query.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	s.mu.RLock()
	ids := make([]string, 0)
	for id, run := range s.items[query.WorkspaceKey] {
		if id <= query.After || run == nil || !run.Status.IsTerminal() ||
			taskRunTerminalConvergenceSatisfiedMem(run, query.RequiredVersion) {
			continue
		}
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	sort.Strings(ids)

	page := execution.TaskRunTerminalConvergencePage{}
	if len(ids) > limit {
		page.TaskRunIDs = append([]string(nil), ids[:limit]...)
		page.Next = page.TaskRunIDs[len(page.TaskRunIDs)-1]
	} else {
		page.TaskRunIDs = append([]string(nil), ids...)
	}
	return page, nil
}

func (s *taskRunStore) CompleteTaskRunTerminalConvergence(
	_ context.Context,
	command execution.TaskRunTerminalConvergenceComplete,
) (*execution.TaskRunTerminalConvergenceResult, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.TaskRunID = strings.TrimSpace(command.TaskRunID)
	command.CompletedAt = command.CompletedAt.UTC()
	if command.WorkspaceKey == "" || command.TaskRunID == "" || command.RequiredVersion <= 0 || command.CompletedAt.IsZero() {
		return nil, fmt.Errorf("terminal convergence identity, positive version, and completion time are required: %w", persistence.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[command.WorkspaceKey][command.TaskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", command.TaskRunID, command.WorkspaceKey, persistence.ErrNotFound)
	}
	if !run.Status.IsTerminal() {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", command.TaskRunID, command.WorkspaceKey, persistence.ErrInvalidTransition)
	}
	if taskRunTerminalConvergenceSatisfiedMem(run, command.RequiredVersion) {
		return &execution.TaskRunTerminalConvergenceResult{TaskRun: cloneTaskRun(run), Replayed: true}, nil
	}
	if run.TerminalConvergenceVersion < 0 ||
		(run.TerminalConvergenceVersion == 0) != (run.TerminalConvergedAt == nil) ||
		run.TerminalConvergenceVersion >= command.RequiredVersion {
		return nil, fmt.Errorf("task run %q in workspace %q has an invalid terminal convergence marker: %w", command.TaskRunID, command.WorkspaceKey, persistence.ErrInvalidTransition)
	}
	run.TerminalConvergenceVersion = command.RequiredVersion
	run.TerminalConvergedAt = &command.CompletedAt
	return &execution.TaskRunTerminalConvergenceResult{TaskRun: cloneTaskRun(run)}, nil
}

func taskRunTerminalConvergenceSatisfiedMem(run *execution.TaskRunRecord, requiredVersion int) bool {
	return run != nil && run.TerminalConvergenceVersion >= requiredVersion && run.TerminalConvergedAt != nil
}

func (s *taskRunStore) Create(ctx context.Context, in execution.TaskRunCreate) (*execution.TaskRunRecord, error) {
	prepared, err := s.prepareTaskRunCreateMem(ctx, in)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[prepared.WorkspaceKey] == nil {
		s.items[prepared.WorkspaceKey] = make(map[string]*execution.TaskRunRecord)
	}
	if _, ok := s.items[prepared.WorkspaceKey][prepared.TaskRunID]; ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", prepared.TaskRunID, prepared.WorkspaceKey, persistence.ErrAlreadyExists)
	}
	run := newTaskRunMem(prepared, time.Now().UTC())
	s.items[prepared.WorkspaceKey][prepared.TaskRunID] = run
	s.setTaskRunLeaseTokenLocked(prepared.WorkspaceKey, prepared.TaskRunID, prepared.LeaseToken)
	return cloneTaskRun(run), nil
}

func (s *taskRunStore) prepareTaskRunCreateMem(ctx context.Context, in execution.TaskRunCreate) (execution.TaskRunCreate, error) {
	if in.WorkspaceKey == "" || in.TaskRunID == "" || in.TaskID == "" {
		return execution.TaskRunCreate{}, fmt.Errorf("workspace_key + task_run_id + task_id required: %w", persistence.ErrInvalid)
	}
	if err := s.applyDriverStepToTaskRunCreateMem(ctx, &in); err != nil {
		return execution.TaskRunCreate{}, err
	}
	if in.DriverRunID != "" && s.parent != nil && !s.parent.exists(in.WorkspaceKey, in.DriverRunID) {
		return execution.TaskRunCreate{}, fmt.Errorf("driver run %q in workspace %q: %w", in.DriverRunID, in.WorkspaceKey, persistence.ErrNotFound)
	}
	return in, nil
}

func (s *taskRunStore) applyDriverStepToTaskRunCreateMem(ctx context.Context, in *execution.TaskRunCreate) error {
	if in.DriverStepID == "" || s.steps == nil {
		return nil
	}
	step, err := s.steps.Get(ctx, in.WorkspaceKey, in.DriverStepID)
	if err != nil {
		return err
	}
	if in.DriverRunID != "" && step.DriverRunID != in.DriverRunID {
		return fmt.Errorf("driver step %q belongs to driver run %q: %w", in.DriverStepID, step.DriverRunID, persistence.ErrInvalidTransition)
	}
	if in.DriverRunID == "" {
		in.DriverRunID = step.DriverRunID
	}
	return nil
}

func newTaskRunMem(in execution.TaskRunCreate, now time.Time) *execution.TaskRunRecord {
	status := defaultTaskRunStatusMem(in.Status)
	run := &execution.TaskRunRecord{
		WorkspaceKey:     in.WorkspaceKey,
		TaskRunID:        in.TaskRunID,
		DriverRunID:      in.DriverRunID,
		DriverStepID:     in.DriverStepID,
		TaskID:           in.TaskID,
		WorkerProfileID:  in.WorkerProfileID,
		Runner:           in.Runner,
		RunnerRef:        in.RunnerRef,
		RunnerKind:       in.RunnerKind,
		RunnerEntrypoint: in.RunnerEntrypoint,
		RunnerVersionID:  in.RunnerVersionID,
		ProviderProfile:  in.ProviderProfile,
		TargetNodeID:     in.TargetNodeID,
		Status:           status,
		NodeID:           in.NodeID,
		LeaseID:          in.LeaseID,
		FencingToken:     taskRunCreateFencingTokenMem(in, now),
		RunnerPlacement:  in.RunnerPlacement,
		SandboxPlacement: in.SandboxPlacement,
		RuntimeMetadata:  cloneMap(in.RuntimeMetadata),
		Input:            cloneRawMessage(in.Input),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if status == execution.TaskRunRecordRunning {
		run.StartedAt = now
		run.LastHeartbeat = now
	}
	return run
}

func defaultTaskRunStatusMem(status execution.TaskRunRecordStatus) execution.TaskRunRecordStatus {
	if status == "" {
		return execution.TaskRunRecordQueued
	}
	return status
}

func taskRunCreateFencingTokenMem(in execution.TaskRunCreate, now time.Time) int64 {
	if in.LeaseID != "" && in.FencingToken == 0 {
		return now.UnixNano()
	}
	return in.FencingToken
}

func (s *taskRunStore) ClaimQueued(ctx context.Context, ws string, claim execution.TaskRunClaim) (*execution.TaskRunRecord, error) {
	normalized, now, err := normalizeTaskRunClaimMem(ws, claim)
	if err != nil {
		return nil, err
	}
	node, normalized, err := s.bindTaskRunClaimToNodeMem(ctx, ws, normalized, now)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	runningOnNode := s.runningTaskRunsOnNodeLocked(ws, normalized.NodeID)
	if node != nil && node.Capacity > 0 && runningOnNode >= node.Capacity {
		return nil, fmt.Errorf("node %q capacity for task runs in workspace %q: %w", normalized.NodeID, ws, persistence.ErrInvalidTransition)
	}
	for _, run := range claimCandidatesMem(s.items[ws], normalized.TaskRunID) {
		profile := s.profileLocked(ws, run.WorkerProfileID)
		if !taskRunMatchesClaimMem(run, profile, normalized, now) {
			continue
		}
		if profile != nil && profile.MaxParallel > 0 && runningOnNode >= profile.MaxParallel {
			return nil, fmt.Errorf("node %q capacity for task runs in workspace %q: %w", normalized.NodeID, ws, persistence.ErrInvalidTransition)
		}
		applyTaskRunClaimMem(run, normalized, now)
		s.setTaskRunLeaseTokenLocked(ws, run.TaskRunID, normalized.LeaseToken)
		return cloneTaskRun(run), nil
	}
	if normalized.TaskRunID != "" {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", normalized.TaskRunID, ws, persistence.ErrInvalidTransition)
	}
	return nil, fmt.Errorf("queued task run in workspace %q: %w", ws, persistence.ErrNotFound)
}

func normalizeTaskRunClaimMem(ws string, claim execution.TaskRunClaim) (execution.TaskRunClaim, time.Time, error) {
	claim.TaskRunID = strings.TrimSpace(claim.TaskRunID)
	claim.NodeID = strings.TrimSpace(claim.NodeID)
	claim.RunnerID = strings.TrimSpace(claim.RunnerID)
	claim.LeaseID = strings.TrimSpace(claim.LeaseID)
	if claim.NodeID == "" || claim.LeaseID == "" {
		return execution.TaskRunClaim{}, time.Time{}, fmt.Errorf("task run claim owner required in workspace %q: %w", ws, persistence.ErrInvalidTransition)
	}
	now := claim.ClaimedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	claim.RunnerPlacement = defaultTaskRunRunnerPlacementMem(claim.RunnerPlacement, claim, now)
	return claim, now, nil
}

func (s *taskRunStore) bindTaskRunClaimToNodeMem(ctx context.Context, ws string, claim execution.TaskRunClaim, now time.Time) (*execution.WorkerNode, execution.TaskRunClaim, error) {
	if s.nodes == nil {
		return nil, claim, nil
	}
	node, err := s.nodes.Get(ctx, ws, claim.NodeID)
	if err != nil {
		return nil, execution.TaskRunClaim{}, err
	}
	if node.DrainState != execution.WorkerNodeActive {
		return nil, execution.TaskRunClaim{}, fmt.Errorf("node %q is %s: %w", claim.NodeID, node.DrainState, persistence.ErrInvalidTransition)
	}
	if !node.ExpiresAt.IsZero() && !node.ExpiresAt.After(now) {
		return nil, execution.TaskRunClaim{}, fmt.Errorf("node %q lease expired: %w", claim.NodeID, persistence.ErrInvalidTransition)
	}
	providers := nodeAdvertisedProvidersMem(node)
	if len(claim.SupportedProviders) == 0 {
		claim.SupportedProviders = providers
	} else if !stringListContainsAllStrictMem(providers, claim.SupportedProviders) {
		return nil, execution.TaskRunClaim{}, fmt.Errorf("node %q does not advertise requested task providers: %w", claim.NodeID, persistence.ErrInvalidTransition)
	}
	if len(claim.Capabilities) == 0 {
		claim.Capabilities = normalizeStringListMem(node.Capabilities)
	} else if !stringListContainsAllStrictMem(node.Capabilities, claim.Capabilities) {
		return nil, execution.TaskRunClaim{}, fmt.Errorf("node %q does not advertise requested task capabilities: %w", claim.NodeID, persistence.ErrInvalidTransition)
	}
	return node, claim, nil
}

func defaultTaskRunRunnerPlacementMem(placement execution.TaskRunPlacementRecord, claim execution.TaskRunClaim, now time.Time) execution.TaskRunPlacementRecord {
	if placement.Provider == "" {
		placement.Provider = "daemon"
	}
	if placement.NodeID == "" {
		placement.NodeID = claim.NodeID
	}
	if placement.RunnerID == "" {
		placement.RunnerID = claim.RunnerID
	}
	if placement.StartedAt.IsZero() {
		placement.StartedAt = now
	}
	if placement.HeartbeatAt.IsZero() {
		placement.HeartbeatAt = now
	}
	return placement
}

func (s *taskRunStore) runningTaskRunsOnNodeLocked(ws, nodeID string) int {
	runningOnNode := 0
	for _, run := range s.items[ws] {
		if run.Status == execution.TaskRunRecordRunning && run.NodeID == nodeID {
			runningOnNode++
		}
	}
	return runningOnNode
}

func applyTaskRunClaimMem(run *execution.TaskRunRecord, claim execution.TaskRunClaim, now time.Time) {
	run.Status = execution.TaskRunRecordRunning
	run.NodeID = claim.NodeID
	run.LeaseID = claim.LeaseID
	run.FencingToken = now.UnixNano()
	run.StartedAt = now
	run.LastHeartbeat = now
	run.UpdatedAt = now
	run.RunnerPlacement = claim.RunnerPlacement
	if !claim.SandboxPlacement.Empty() {
		run.SandboxPlacement = claim.SandboxPlacement
	}
}

func (s *taskRunStore) profileLocked(ws, profileID string) *execution.WorkerProfile {
	if strings.TrimSpace(profileID) == "" || s.profiles == nil {
		return nil
	}
	profile, _ := s.profiles.Get(context.Background(), ws, profileID)
	return profile
}

func (s *taskRunStore) Get(_ context.Context, ws, taskRunID string) (*execution.TaskRunRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.items[ws][taskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotFound)
	}
	return cloneTaskRun(run), nil
}

func (s *taskRunStore) List(_ context.Context, ws string, filter execution.TaskRunFilter) ([]*execution.TaskRunRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*execution.TaskRunRecord, 0, len(s.items[ws]))
	for _, run := range s.items[ws] {
		if taskRunMatchesMem(run, filter) {
			out = append(out, cloneTaskRun(run))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *taskRunStore) Finish(_ context.Context, ws, taskRunID string, finish execution.TaskRunFinish) (*execution.TaskRunRecord, error) {
	if !finish.Status.IsTerminal() {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	if finish.BlockTask && finish.Status != execution.TaskRunRecordFailed {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][taskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotFound)
	}
	if run.Status.IsTerminal() {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	if err := s.validateTaskRunLeaseTokenLocked(ws, taskRunID, finish.LeaseToken); err != nil {
		return nil, err
	}
	if run.LeaseID != "" || run.FencingToken != 0 {
		if finish.NodeID != run.NodeID || finish.LeaseID != run.LeaseID || finish.FencingToken != run.FencingToken {
			return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotOwner)
		}
	}
	if taskRunRequiresCloudSafeArtifactsMem(run) && !taskRunArtifactsRefCloudSafeForCompletionMem(finish.ArtifactsRef) {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	now := finish.FinishedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	applyTaskRunFinishMem(run, finish, now)
	if finish.BlockTask && strings.TrimSpace(run.TaskID) != "" {
		if s.blockedTasks[ws] == nil {
			s.blockedTasks[ws] = make(map[string]bool)
		}
		s.blockedTasks[ws][run.TaskID] = true
	}
	return cloneTaskRun(run), nil
}

func applyTaskRunFinishMem(run *execution.TaskRunRecord, finish execution.TaskRunFinish, now time.Time) {
	run.Status = finish.Status
	run.ExitCode = clonePtr(finish.ExitCode)
	run.LogsRef = finish.LogsRef
	run.ArtifactsRef = finish.ArtifactsRef
	run.InputTokens = finish.InputTokens
	run.OutputTokens = finish.OutputTokens
	run.CacheReadTokens = finish.CacheReadTokens
	run.CacheWriteTokens = finish.CacheWriteTokens
	run.EstimatedCostUSD = finish.EstimatedCostUSD
	run.RuntimeMetadata = cloneMap(finish.RuntimeMetadata)
	run.ErrorClass = finish.ErrorClass
	run.ErrorMessage = finish.ErrorMessage
	run.FinishedAt = &now
	run.UpdatedAt = now
}

// TaskBlocked reports whether a BlockTask finish marked the given task ID
// blocked. memstore has no issue model, so this is the test-side observable
// for the fleet-db issue transition.
func (s *taskRunStore) TaskBlocked(ws, taskID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.blockedTasks[ws][taskID]
}

func (s *taskRunStore) Heartbeat(_ context.Context, ws, taskRunID string, heartbeat execution.TaskRunHeartbeat) (*execution.TaskRunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][taskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotFound)
	}
	if run.Status != execution.TaskRunRecordRunning {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	if err := s.validateTaskRunLeaseTokenLocked(ws, taskRunID, heartbeat.LeaseToken); err != nil {
		return nil, err
	}
	if run.LeaseID != "" || run.FencingToken != 0 {
		if heartbeat.NodeID != run.NodeID || heartbeat.LeaseID != run.LeaseID || heartbeat.FencingToken != run.FencingToken {
			return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotOwner)
		}
	}
	now := heartbeat.HeartbeatAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	run.LastHeartbeat = now
	run.UpdatedAt = now
	if heartbeat.LogsRef != "" {
		run.LogsRef = heartbeat.LogsRef
	}
	if heartbeat.ArtifactsRef != "" {
		run.ArtifactsRef = heartbeat.ArtifactsRef
	}
	run.RuntimeMetadata = mergeStringMapMem(run.RuntimeMetadata, heartbeat.RuntimeMetadata)
	return cloneTaskRun(run), nil
}

func (s *taskRunStore) Requeue(_ context.Context, ws, taskRunID string, requeue execution.TaskRunRequeue) (*execution.TaskRunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][taskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotFound)
	}
	if run.Status != execution.TaskRunRecordRunning {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	if err := s.validateTaskRunLeaseTokenLocked(ws, taskRunID, requeue.LeaseToken); err != nil {
		return nil, err
	}
	if run.LeaseID != "" || run.FencingToken != 0 {
		if requeue.NodeID != run.NodeID || requeue.LeaseID != run.LeaseID || requeue.FencingToken != run.FencingToken {
			return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotOwner)
		}
	}
	now := requeue.RequeuedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	run.Status = execution.TaskRunRecordQueued
	run.NodeID = ""
	run.LeaseID = ""
	run.FencingToken = 0
	run.RunnerPlacement = execution.TaskRunPlacementRecord{}
	run.ExitCode = nil
	run.FinishedAt = nil
	run.ErrorClass = requeue.ErrorClass
	run.ErrorMessage = requeue.ErrorMessage
	if requeue.LogsRef != "" {
		run.LogsRef = requeue.LogsRef
	}
	if requeue.ArtifactsRef != "" {
		run.ArtifactsRef = requeue.ArtifactsRef
	}
	run.RuntimeMetadata = mergeStringMapMem(run.RuntimeMetadata, requeue.RuntimeMetadata)
	run.NextEligibleAt = requeue.NextEligibleAt
	run.UpdatedAt = now
	delete(s.leaseTokenHashes[ws], taskRunID)
	return cloneTaskRun(run), nil
}

func (s *taskRunStore) Complete(ctx context.Context, ws, taskRunID string, complete execution.TaskRunComplete) (*execution.TaskRunRecord, error) {
	normalized, err := normalizeTaskRunCompleteMem(ws, taskRunID, complete)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateTaskRunLeaseTokenLocked(ws, taskRunID, complete.LeaseToken); err != nil {
		return nil, err
	}
	if run, handled, err := s.completedTaskRunByCompletionIDLocked(ws, taskRunID, normalized.CompletionID); handled || err != nil {
		return run, err
	}
	run, ok := s.items[ws][taskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotFound)
	}
	if run.Status.IsTerminal() {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	if err := validateTaskRunCompleteOwnerMem(ws, taskRunID, run, normalized); err != nil {
		return nil, err
	}
	if taskRunRequiresCloudSafeArtifactsMem(run) && !taskRunArtifactsRefCloudSafeForCompletionMem(normalized.ArtifactsRef) {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	if err := s.validateCompletionArtifactsLocked(ctx, ws, run, normalized.RequiredArtifactIDs); err != nil {
		return nil, err
	}

	applyTaskRunCompleteMem(run, normalized)
	if s.completions[ws] == nil {
		s.completions[ws] = make(map[string]string)
	}
	s.completions[ws][normalized.CompletionID] = taskRunID
	return cloneTaskRun(run), nil
}

func normalizeTaskRunCompleteMem(ws, taskRunID string, complete execution.TaskRunComplete) (execution.TaskRunComplete, error) {
	complete.CompletionID = strings.TrimSpace(complete.CompletionID)
	if complete.CompletionID == "" || !complete.Status.IsTerminal() {
		return execution.TaskRunComplete{}, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	if complete.CloseTask && complete.Status != execution.TaskRunRecordCompleted {
		return execution.TaskRunComplete{}, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	if complete.RequireArtifacts && len(complete.RequiredArtifactIDs) == 0 && strings.TrimSpace(complete.ArtifactsRef) == "" {
		return execution.TaskRunComplete{}, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	if !taskRunUsageValuesValidMem(complete.InputTokens, complete.OutputTokens, complete.CacheReadTokens, complete.CacheWriteTokens, complete.EstimatedCostUSD) {
		return execution.TaskRunComplete{}, fmt.Errorf("task run usage values must be finite and non-negative")
	}
	return complete, nil
}

func (s *taskRunStore) completedTaskRunByCompletionIDLocked(ws, taskRunID, completionID string) (*execution.TaskRunRecord, bool, error) {
	existingTaskRunID, ok := s.completions[ws][completionID]
	if !ok {
		return nil, false, nil
	}
	if existingTaskRunID != taskRunID {
		return nil, true, fmt.Errorf("task run completion %q in workspace %q: %w", completionID, ws, persistence.ErrAlreadyExists)
	}
	run, ok := s.items[ws][existingTaskRunID]
	if !ok {
		return nil, true, fmt.Errorf("task run %q in workspace %q: %w", existingTaskRunID, ws, persistence.ErrNotFound)
	}
	return cloneTaskRun(run), true, nil
}

func validateTaskRunCompleteOwnerMem(ws, taskRunID string, run *execution.TaskRunRecord, complete execution.TaskRunComplete) error {
	if run.LeaseID == "" && run.FencingToken == 0 {
		return nil
	}
	if complete.NodeID != run.NodeID || complete.LeaseID != run.LeaseID || complete.FencingToken != run.FencingToken {
		return fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotOwner)
	}
	return nil
}

func applyTaskRunCompleteMem(run *execution.TaskRunRecord, complete execution.TaskRunComplete) {
	now := complete.FinishedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	run.Status = complete.Status
	run.ExitCode = clonePtr(complete.ExitCode)
	run.LogsRef = complete.LogsRef
	run.ArtifactsRef = complete.ArtifactsRef
	run.InputTokens = complete.InputTokens
	run.OutputTokens = complete.OutputTokens
	run.CacheReadTokens = complete.CacheReadTokens
	run.CacheWriteTokens = complete.CacheWriteTokens
	run.EstimatedCostUSD = complete.EstimatedCostUSD
	run.RuntimeMetadata = cloneMap(complete.RuntimeMetadata)
	run.ErrorClass = complete.ErrorClass
	run.ErrorMessage = complete.ErrorMessage
	run.FinishedAt = &now
	run.UpdatedAt = now
}

func (s *taskRunStore) validateCompletionArtifactsLocked(ctx context.Context, ws string, run *execution.TaskRunRecord, artifactIDs []string) error {
	if len(artifactIDs) == 0 || s.artifacts == nil {
		return nil
	}
	for _, artifactID := range artifactIDs {
		artifact, err := s.artifacts.GetArtifactRecord(ctx, ws, strings.TrimSpace(artifactID))
		if err != nil {
			return err
		}
		if artifact.TaskID != "" && artifact.TaskID != run.TaskID {
			return fmt.Errorf("artifact %q in workspace %q: %w", artifact.ArtifactID, ws, persistence.ErrInvalidTransition)
		}
		if !artifactOwnedByTaskRunCompletionMem(artifact, run.TaskRunID) {
			return fmt.Errorf("artifact %q in workspace %q: %w", artifact.ArtifactID, ws, persistence.ErrInvalidTransition)
		}
		if !artifactReadyForTaskRunCompletionMem(artifact) {
			return fmt.Errorf("artifact %q in workspace %q: %w", artifact.ArtifactID, ws, persistence.ErrInvalidTransition)
		}
		if taskRunRequiresCloudSafeArtifactsMem(run) && !artifactCloudSafeForTaskRunCompletionMem(artifact) {
			return fmt.Errorf("artifact %q in workspace %q: %w", artifact.ArtifactID, ws, persistence.ErrInvalidTransition)
		}
	}
	return nil
}

func artifactOwnedByTaskRunCompletionMem(artifact *artifacts.Artifact, taskRunID string) bool {
	if artifact == nil {
		return false
	}
	return artifact.OwnerType == artifacts.OwnerTaskRun && artifact.OwnerID == strings.TrimSpace(taskRunID)
}

func artifactReadyForTaskRunCompletionMem(artifact *artifacts.Artifact) bool {
	if artifact == nil || artifact.DurableStatus != "finalized" {
		return false
	}
	return strings.TrimSpace(artifact.ContentHash) != "" || strings.TrimSpace(artifact.Checksum) != ""
}

func taskRunRequiresCloudSafeArtifactsMem(run *execution.TaskRunRecord) bool {
	if run == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(run.SandboxPlacement.Provider)) {
	case "", "local", "local-noop", "noop", "flue-local":
		return false
	default:
		return true
	}
}

func artifactCloudSafeForTaskRunCompletionMem(artifact *artifacts.Artifact) bool {
	if artifact == nil {
		return false
	}
	return taskRunArtifactURICloudSafeForCompletionMem(artifact.URI)
}

func taskRunArtifactsRefCloudSafeForCompletionMem(artifactsRef string) bool {
	artifactsRef = strings.TrimSpace(artifactsRef)
	if artifactsRef == "" {
		return true
	}
	return taskRunArtifactURICloudSafeForCompletionMem(artifactsRef)
}

func taskRunArtifactURICloudSafeForCompletionMem(uri string) bool {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return false
	}
	scheme, _, ok := strings.Cut(uri, ":")
	if !ok || strings.TrimSpace(scheme) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "artifact", "artifacts", "mem", "s3", "gs", "https":
		return true
	case "file", "local", "daytona":
		return false
	default:
		return false
	}
}

func taskRunUsageValuesValidMem(inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64, estimatedCostUSD float64) bool {
	return inputTokens >= 0 &&
		outputTokens >= 0 &&
		cacheReadTokens >= 0 &&
		cacheWriteTokens >= 0 &&
		estimatedCostUSD >= 0 &&
		!math.IsInf(estimatedCostUSD, 0) &&
		!math.IsNaN(estimatedCostUSD)
}

func (s *taskRunStore) AppendLog(_ context.Context, ws, taskRunID string, appendLog execution.TaskRunLogAppend) (*execution.TaskRunLogEntry, error) {
	appendLog, fingerprint, err := normalizeTaskRunLogAppendMem(appendLog)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if receipt, ok := s.logReceipts[ws][taskRunID][appendLog.RequestID]; ok {
		if receipt.fingerprint != fingerprint {
			return nil, fmt.Errorf("task run log request %q conflicts with its committed append: %w", appendLog.RequestID, persistence.ErrConflict)
		}
		return cloneTaskRunLogEntry(receipt.entry), nil
	}
	run, ok := s.items[ws][taskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotFound)
	}
	if run.Status != execution.TaskRunRecordRunning {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrInvalidTransition)
	}
	if err := s.validateTaskRunLeaseTokenLocked(ws, taskRunID, appendLog.LeaseToken); err != nil {
		return nil, err
	}
	if run.LeaseID != "" || run.FencingToken != 0 {
		if appendLog.NodeID != run.NodeID || appendLog.LeaseID != run.LeaseID || appendLog.FencingToken != run.FencingToken {
			return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotOwner)
		}
	}
	return s.appendTaskRunLogEntryLocked(ws, taskRunID, appendLog, fingerprint), nil
}

func normalizeTaskRunLogAppendMem(appendLog execution.TaskRunLogAppend) (execution.TaskRunLogAppend, taskRunLogFingerprint, error) {
	appendLog.RequestID = strings.TrimSpace(appendLog.RequestID)
	appendLog.NodeID = strings.TrimSpace(appendLog.NodeID)
	appendLog.LeaseID = strings.TrimSpace(appendLog.LeaseID)
	appendLog.Stream = strings.TrimSpace(appendLog.Stream)
	if appendLog.Stream == "" {
		appendLog.Stream = "stdout"
	}
	if appendLog.RequestID == "" || appendLog.Timestamp.IsZero() {
		return execution.TaskRunLogAppend{}, taskRunLogFingerprint{}, fmt.Errorf("append task run log request_id and timestamp required: %w", persistence.ErrInvalid)
	}
	appendLog.Timestamp = appendLog.Timestamp.UTC()
	fingerprint := taskRunLogFingerprint{
		NodeID:         appendLog.NodeID,
		LeaseID:        appendLog.LeaseID,
		LeaseTokenHash: sha256.Sum256([]byte(appendLog.LeaseToken)),
		FencingToken:   appendLog.FencingToken,
		Stream:         appendLog.Stream,
		Text:           appendLog.Text,
		Timestamp:      appendLog.Timestamp.Format(time.RFC3339Nano),
	}
	return appendLog, fingerprint, nil
}

func (s *taskRunStore) appendTaskRunLogEntryLocked(ws, taskRunID string, appendLog execution.TaskRunLogAppend, fingerprint taskRunLogFingerprint) *execution.TaskRunLogEntry {
	now := time.Now().UTC()
	if s.logs[ws] == nil {
		s.logs[ws] = make(map[string][]*execution.TaskRunLogEntry)
	}
	entry := &execution.TaskRunLogEntry{
		WorkspaceKey: ws,
		TaskRunID:    taskRunID,
		Sequence:     int64(len(s.logs[ws][taskRunID]) + 1),
		Stream:       appendLog.Stream,
		Text:         appendLog.Text,
		NodeID:       appendLog.NodeID,
		LeaseID:      appendLog.LeaseID,
		FencingToken: appendLog.FencingToken,
		Timestamp:    appendLog.Timestamp,
		CreatedAt:    now,
	}
	s.logs[ws][taskRunID] = append(s.logs[ws][taskRunID], entry)
	if s.logReceipts[ws] == nil {
		s.logReceipts[ws] = make(map[string]map[string]taskRunLogReceipt)
	}
	if s.logReceipts[ws][taskRunID] == nil {
		s.logReceipts[ws][taskRunID] = make(map[string]taskRunLogReceipt)
	}
	s.logReceipts[ws][taskRunID][appendLog.RequestID] = taskRunLogReceipt{
		fingerprint: fingerprint,
		entry:       cloneTaskRunLogEntry(entry),
	}
	return cloneTaskRunLogEntry(entry)
}

func (s *taskRunStore) setTaskRunLeaseTokenLocked(ws, taskRunID, token string) {
	if strings.TrimSpace(token) == "" {
		return
	}
	if s.leaseTokenHashes[ws] == nil {
		s.leaseTokenHashes[ws] = make(map[string][sha256.Size]byte)
	}
	s.leaseTokenHashes[ws][taskRunID] = sha256.Sum256([]byte(token))
}

func (s *taskRunStore) validateTaskRunLeaseTokenLocked(ws, taskRunID, token string) error {
	want, ok := s.leaseTokenHashes[ws][taskRunID]
	if !ok {
		// Legacy fixtures created before a claim do not have a token. Keep that
		// compatibility shape unfenced by secret while every seeded/claimed run
		// enforces the same opaque-token check as FleetDB.
		return nil
	}
	got := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
		return fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotOwner)
	}
	return nil
}

func (s *taskRunStore) ListLogs(_ context.Context, ws, taskRunID string, filter execution.TaskRunLogFilter) ([]*execution.TaskRunLogEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.items[ws][taskRunID]; !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, persistence.ErrNotFound)
	}
	out := []*execution.TaskRunLogEntry{}
	for _, entry := range s.logs[ws][taskRunID] {
		if entry.Sequence <= filter.AfterSequence {
			continue
		}
		out = append(out, cloneTaskRunLogEntry(entry))
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}
