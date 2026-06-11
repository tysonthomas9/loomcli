package memstore

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type taskRunStore struct {
	mu          sync.RWMutex
	items       map[string]map[string]*domain.TaskRun
	logs        map[string]map[string][]*domain.TaskRunLogEntry
	completions map[string]map[string]string
	parent      *driverRunStore
	steps       *driverStepStore
	artifacts   *artifactStore
	profiles    *workerProfileStore
}

func newTaskRunStore(parent *driverRunStore, steps *driverStepStore, artifacts *artifactStore, profiles *workerProfileStore) *taskRunStore {
	return &taskRunStore{
		items:       make(map[string]map[string]*domain.TaskRun),
		logs:        make(map[string]map[string][]*domain.TaskRunLogEntry),
		completions: make(map[string]map[string]string),
		parent:      parent,
		steps:       steps,
		artifacts:   artifacts,
		profiles:    profiles,
	}
}

var _ store.TaskRunStore = (*taskRunStore)(nil)

func (s *taskRunStore) Create(ctx context.Context, in store.TaskRunCreate) (*domain.TaskRun, error) {
	if in.WorkspaceKey == "" || in.TaskRunID == "" || in.TaskID == "" {
		return nil, fmt.Errorf("workspace_key + task_run_id + task_id required: %w", domain.ErrInvalid)
	}
	if in.DriverStepID != "" && s.steps != nil {
		step, err := s.steps.Get(ctx, in.WorkspaceKey, in.DriverStepID)
		if err != nil {
			return nil, err
		}
		if in.DriverRunID != "" && step.DriverRunID != in.DriverRunID {
			return nil, fmt.Errorf("driver step %q belongs to driver run %q: %w", in.DriverStepID, step.DriverRunID, domain.ErrInvalidTransition)
		}
		if in.DriverRunID == "" {
			in.DriverRunID = step.DriverRunID
		}
	}
	if in.DriverRunID != "" && s.parent != nil && !s.parent.exists(in.WorkspaceKey, in.DriverRunID) {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", in.DriverRunID, in.WorkspaceKey, domain.ErrNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.TaskRun)
	}
	if _, ok := s.items[in.WorkspaceKey][in.TaskRunID]; ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", in.TaskRunID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	run := newTaskRunFromCreateMem(in, time.Now().UTC())
	s.items[in.WorkspaceKey][in.TaskRunID] = run
	return cloneTaskRun(run), nil
}

func newTaskRunFromCreateMem(in store.TaskRunCreate, now time.Time) *domain.TaskRun {
	status := in.Status
	if status == "" {
		status = domain.TaskRunQueued
	}
	fencingToken := in.FencingToken
	if in.LeaseID != "" && fencingToken == 0 {
		fencingToken = now.UnixNano()
	}
	run := &domain.TaskRun{
		WorkspaceKey:     in.WorkspaceKey,
		TaskRunID:        in.TaskRunID,
		DriverRunID:      in.DriverRunID,
		DriverStepID:     in.DriverStepID,
		TaskID:           in.TaskID,
		WorkerProfileID:  in.WorkerProfileID,
		ProviderProfile:  in.ProviderProfile,
		Status:           status,
		NodeID:           in.NodeID,
		LeaseID:          in.LeaseID,
		FencingToken:     fencingToken,
		RunnerPlacement:  in.RunnerPlacement,
		SandboxPlacement: in.SandboxPlacement,
		RuntimeMetadata:  cloneMap(in.RuntimeMetadata),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if status == domain.TaskRunRunning {
		run.StartedAt = now
		run.LastHeartbeat = now
	}
	return run
}

func (s *taskRunStore) ClaimQueued(_ context.Context, ws string, claim store.TaskRunClaim) (*domain.TaskRun, error) {
	claim = normalizeTaskRunClaimMem(claim)
	if claim.NodeID == "" || claim.LeaseID == "" {
		return nil, fmt.Errorf("task run claim owner required in workspace %q: %w", ws, domain.ErrInvalidTransition)
	}
	now := claim.ClaimedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	claim = applyTaskRunClaimPlacementDefaultsMem(claim, now)

	s.mu.Lock()
	defer s.mu.Unlock()
	runningOnNode := 0
	for _, run := range s.items[ws] {
		if run.Status == domain.TaskRunRunning && run.NodeID == claim.NodeID {
			runningOnNode++
		}
	}
	for _, run := range claimCandidatesMem(s.items[ws], claim.TaskRunID) {
		if !taskRunMatchesClaimMem(run, s.profileLocked(ws, run.WorkerProfileID), claim) {
			continue
		}
		profile := s.profileLocked(ws, run.WorkerProfileID)
		if profile != nil && profile.MaxParallel > 0 && runningOnNode >= profile.MaxParallel {
			return nil, fmt.Errorf("node %q capacity for task runs in workspace %q: %w", claim.NodeID, ws, domain.ErrInvalidTransition)
		}
		run.Status = domain.TaskRunRunning
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
		return cloneTaskRun(run), nil
	}
	if claim.TaskRunID != "" {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", claim.TaskRunID, ws, domain.ErrInvalidTransition)
	}
	return nil, fmt.Errorf("queued task run in workspace %q: %w", ws, domain.ErrNotFound)
}

func normalizeTaskRunClaimMem(claim store.TaskRunClaim) store.TaskRunClaim {
	claim.TaskRunID = strings.TrimSpace(claim.TaskRunID)
	claim.NodeID = strings.TrimSpace(claim.NodeID)
	claim.RunnerID = strings.TrimSpace(claim.RunnerID)
	claim.LeaseID = strings.TrimSpace(claim.LeaseID)
	return claim
}

func applyTaskRunClaimPlacementDefaultsMem(claim store.TaskRunClaim, now time.Time) store.TaskRunClaim {
	if claim.RunnerPlacement.Provider == "" {
		claim.RunnerPlacement.Provider = "daemon"
	}
	if claim.RunnerPlacement.NodeID == "" {
		claim.RunnerPlacement.NodeID = claim.NodeID
	}
	if claim.RunnerPlacement.RunnerID == "" {
		claim.RunnerPlacement.RunnerID = claim.RunnerID
	}
	if claim.RunnerPlacement.StartedAt.IsZero() {
		claim.RunnerPlacement.StartedAt = now
	}
	if claim.RunnerPlacement.HeartbeatAt.IsZero() {
		claim.RunnerPlacement.HeartbeatAt = now
	}
	return claim
}

func (s *taskRunStore) profileLocked(ws, profileID string) *domain.WorkerProfile {
	if strings.TrimSpace(profileID) == "" || s.profiles == nil {
		return nil
	}
	profile, _ := s.profiles.Get(context.Background(), ws, profileID)
	return profile
}

func (s *taskRunStore) Get(_ context.Context, ws, taskRunID string) (*domain.TaskRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.items[ws][taskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotFound)
	}
	return cloneTaskRun(run), nil
}

func (s *taskRunStore) List(_ context.Context, ws string, filter store.TaskRunFilter) ([]*domain.TaskRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.TaskRun, 0, len(s.items[ws]))
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

func (s *taskRunStore) Finish(_ context.Context, ws, taskRunID string, finish store.TaskRunFinish) (*domain.TaskRun, error) {
	if !finish.Status.IsTerminal() {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][taskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotFound)
	}
	if run.Status.IsTerminal() {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	if run.LeaseID != "" || run.FencingToken != 0 {
		if finish.NodeID != run.NodeID || finish.LeaseID != run.LeaseID || finish.FencingToken != run.FencingToken {
			return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotOwner)
		}
	}
	if taskRunRequiresCloudSafeArtifactsMem(run) && !taskRunArtifactsRefCloudSafeForCompletionMem(finish.ArtifactsRef) {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	now := finish.FinishedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
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
	return cloneTaskRun(run), nil
}

func (s *taskRunStore) Heartbeat(_ context.Context, ws, taskRunID string, heartbeat store.TaskRunHeartbeat) (*domain.TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][taskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotFound)
	}
	if run.Status != domain.TaskRunRunning {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	if run.LeaseID != "" || run.FencingToken != 0 {
		if heartbeat.NodeID != run.NodeID || heartbeat.LeaseID != run.LeaseID || heartbeat.FencingToken != run.FencingToken {
			return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotOwner)
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

func (s *taskRunStore) Complete(ctx context.Context, ws, taskRunID string, complete store.TaskRunComplete) (*domain.TaskRun, error) {
	complete.CompletionID = strings.TrimSpace(complete.CompletionID)
	if err := validateTaskRunCompleteMem(ws, taskRunID, complete); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existingTaskRunID, ok := s.completions[ws][complete.CompletionID]; ok {
		if existingTaskRunID != taskRunID {
			return nil, fmt.Errorf("task run completion %q in workspace %q: %w", complete.CompletionID, ws, domain.ErrAlreadyExists)
		}
		run, ok := s.items[ws][existingTaskRunID]
		if !ok {
			return nil, fmt.Errorf("task run %q in workspace %q: %w", existingTaskRunID, ws, domain.ErrNotFound)
		}
		return cloneTaskRun(run), nil
	}
	run, ok := s.items[ws][taskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotFound)
	}
	if run.Status.IsTerminal() {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	if run.LeaseID != "" || run.FencingToken != 0 {
		if complete.NodeID != run.NodeID || complete.LeaseID != run.LeaseID || complete.FencingToken != run.FencingToken {
			return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotOwner)
		}
	}
	if taskRunRequiresCloudSafeArtifactsMem(run) && !taskRunArtifactsRefCloudSafeForCompletionMem(complete.ArtifactsRef) {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	if err := s.validateCompletionArtifactsLocked(ctx, ws, run, complete.RequiredArtifactIDs); err != nil {
		return nil, err
	}

	now := complete.FinishedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	applyTaskRunCompletionMem(run, complete, now)
	if s.completions[ws] == nil {
		s.completions[ws] = make(map[string]string)
	}
	s.completions[ws][complete.CompletionID] = taskRunID
	return cloneTaskRun(run), nil
}

func applyTaskRunCompletionMem(run *domain.TaskRun, complete store.TaskRunComplete, now time.Time) {
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

func validateTaskRunCompleteMem(ws, taskRunID string, complete store.TaskRunComplete) error {
	if complete.CompletionID == "" || !complete.Status.IsTerminal() {
		return fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	if complete.CloseTask && complete.Status != domain.TaskRunCompleted {
		return fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	if complete.RequireArtifacts && len(complete.RequiredArtifactIDs) == 0 && strings.TrimSpace(complete.ArtifactsRef) == "" {
		return fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	if !taskRunUsageValuesValidMem(complete.InputTokens, complete.OutputTokens, complete.CacheReadTokens, complete.CacheWriteTokens, complete.EstimatedCostUSD) {
		return fmt.Errorf("task run usage values must be finite and non-negative")
	}
	return nil
}

func (s *taskRunStore) validateCompletionArtifactsLocked(ctx context.Context, ws string, run *domain.TaskRun, artifactIDs []string) error {
	if len(artifactIDs) == 0 || s.artifacts == nil {
		return nil
	}
	for _, artifactID := range artifactIDs {
		artifact, err := s.artifacts.Get(ctx, ws, strings.TrimSpace(artifactID))
		if err != nil {
			return err
		}
		if artifact.TaskID != "" && artifact.TaskID != run.TaskID {
			return fmt.Errorf("artifact %q in workspace %q: %w", artifact.ArtifactID, ws, domain.ErrInvalidTransition)
		}
		if !artifactOwnedByTaskRunCompletionMem(artifact, run.TaskRunID) {
			return fmt.Errorf("artifact %q in workspace %q: %w", artifact.ArtifactID, ws, domain.ErrInvalidTransition)
		}
		if !artifactReadyForTaskRunCompletionMem(artifact) {
			return fmt.Errorf("artifact %q in workspace %q: %w", artifact.ArtifactID, ws, domain.ErrInvalidTransition)
		}
		if taskRunRequiresCloudSafeArtifactsMem(run) && !artifactCloudSafeForTaskRunCompletionMem(artifact) {
			return fmt.Errorf("artifact %q in workspace %q: %w", artifact.ArtifactID, ws, domain.ErrInvalidTransition)
		}
	}
	return nil
}

func artifactOwnedByTaskRunCompletionMem(artifact *domain.Artifact, taskRunID string) bool {
	if artifact == nil {
		return false
	}
	return artifact.OwnerType == "task_run" && artifact.OwnerID == strings.TrimSpace(taskRunID)
}

func artifactReadyForTaskRunCompletionMem(artifact *domain.Artifact) bool {
	if artifact == nil || artifact.DurableStatus != "finalized" {
		return false
	}
	return strings.TrimSpace(artifact.ContentHash) != "" || strings.TrimSpace(artifact.Checksum) != ""
}

func taskRunRequiresCloudSafeArtifactsMem(run *domain.TaskRun) bool {
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

func artifactCloudSafeForTaskRunCompletionMem(artifact *domain.Artifact) bool {
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

func (s *taskRunStore) AppendLog(_ context.Context, ws, taskRunID string, appendLog store.TaskRunLogAppend) (*domain.TaskRunLogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][taskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotFound)
	}
	if run.Status != domain.TaskRunRunning {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	if run.LeaseID != "" || run.FencingToken != 0 {
		if appendLog.NodeID != run.NodeID || appendLog.LeaseID != run.LeaseID || appendLog.FencingToken != run.FencingToken {
			return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotOwner)
		}
	}
	now := time.Now().UTC()
	ts := appendLog.Timestamp
	if ts.IsZero() {
		ts = now
	}
	stream := appendLog.Stream
	if stream == "" {
		stream = "stdout"
	}
	if s.logs[ws] == nil {
		s.logs[ws] = make(map[string][]*domain.TaskRunLogEntry)
	}
	entry := &domain.TaskRunLogEntry{
		WorkspaceKey: ws,
		TaskRunID:    taskRunID,
		Sequence:     int64(len(s.logs[ws][taskRunID]) + 1),
		Stream:       stream,
		Text:         appendLog.Text,
		NodeID:       appendLog.NodeID,
		LeaseID:      appendLog.LeaseID,
		FencingToken: appendLog.FencingToken,
		Timestamp:    ts,
		CreatedAt:    now,
	}
	s.logs[ws][taskRunID] = append(s.logs[ws][taskRunID], entry)
	return cloneTaskRunLogEntry(entry), nil
}

func (s *taskRunStore) ListLogs(_ context.Context, ws, taskRunID string, filter store.TaskRunLogFilter) ([]*domain.TaskRunLogEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.items[ws][taskRunID]; !ok {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrNotFound)
	}
	out := []*domain.TaskRunLogEntry{}
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

func cloneTaskRun(r *domain.TaskRun) *domain.TaskRun {
	out := *r
	out.ExitCode = clonePtr(r.ExitCode)
	out.RunnerPlacement = cloneTaskRunPlacement(r.RunnerPlacement)
	out.SandboxPlacement = cloneTaskRunPlacement(r.SandboxPlacement)
	out.RuntimeMetadata = cloneMap(r.RuntimeMetadata)
	out.FinishedAt = clonePtr(r.FinishedAt)
	return &out
}

func cloneTaskRunPlacement(p domain.TaskRunPlacement) domain.TaskRunPlacement {
	out := p
	out.RetainedUntil = clonePtr(p.RetainedUntil)
	return out
}

func cloneTaskRunLogEntry(entry *domain.TaskRunLogEntry) *domain.TaskRunLogEntry {
	if entry == nil {
		return nil
	}
	out := *entry
	return &out
}

func mergeStringMapMem(base, patch map[string]string) map[string]string {
	if len(base) == 0 && len(patch) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(patch))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		out[k] = v
	}
	return out
}

func taskRunMatchesMem(r *domain.TaskRun, f store.TaskRunFilter) bool {
	return (f.DriverRunID == "" || r.DriverRunID == f.DriverRunID) &&
		(f.DriverStepID == "" || r.DriverStepID == f.DriverStepID) &&
		(f.TaskID == "" || r.TaskID == f.TaskID) &&
		(f.WorkerProfileID == "" || r.WorkerProfileID == f.WorkerProfileID) &&
		(f.Status == "" || r.Status == f.Status)
}

func claimCandidatesMem(runs map[string]*domain.TaskRun, taskRunID string) []*domain.TaskRun {
	out := make([]*domain.TaskRun, 0, len(runs))
	for _, run := range runs {
		if taskRunID != "" && run.TaskRunID != taskRunID {
			continue
		}
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func taskRunMatchesClaimMem(run *domain.TaskRun, profile *domain.WorkerProfile, claim store.TaskRunClaim) bool {
	if run == nil || run.Status != domain.TaskRunQueued {
		return false
	}
	if claim.TaskRunID != "" && run.TaskRunID != claim.TaskRunID {
		return false
	}
	if run.WorkerProfileID != "" {
		if !stringListEmptyOrContainsMem(claim.WorkerProfileIDs, run.WorkerProfileID) {
			return false
		}
		if profile == nil || !profile.Enabled {
			return false
		}
		if profile.Backend != "" && !stringListEmptyOrContainsMem(claim.SupportedProviders, profile.Backend) {
			return false
		}
		if !stringListContainsAllMem(claim.Capabilities, profile.Capabilities) {
			return false
		}
	}
	provider := run.SandboxPlacement.Provider
	if provider == "" {
		provider = run.ProviderProfile
	}
	return provider == "" || stringListEmptyOrContainsMem(claim.SupportedProviders, provider)
}

func stringListEmptyOrContainsMem(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" || len(values) == 0 {
		return true
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func stringListContainsAllMem(have, required []string) bool {
	for _, want := range required {
		if !stringListEmptyOrContainsMem(have, want) {
			return false
		}
	}
	return true
}
