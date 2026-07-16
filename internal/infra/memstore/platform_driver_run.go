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
	outcomes map[string]map[string]*memDriverRunOutcome
	versions *driverVersionStore
	bindings *triggerBindingStore
	events   *triggerEventStore
	taskRuns *taskRunStore
	steps    *driverStepStore
	next     int64
	// awaitResumeEligible probes whether an await cycle has resolved
	// (satisfied/timed_out) — ResumeAwaiting's security gate. Wired by the
	// Store constructor after the await store exists; called OUTSIDE s.mu.
	awaitResumeEligible func(ws, instanceKey string) bool
}

type memDriverRunOutcome struct {
	value       store.DriverRunOutcome
	state       string
	claimID     string
	claimUntil  time.Time
	availableAt time.Time
	lastError   string
}

func newDriverRunStore(versions *driverVersionStore, bindings *triggerBindingStore) *driverRunStore {
	return &driverRunStore{
		items: make(map[string]map[string]*domain.DriverRun), outcomes: make(map[string]map[string]*memDriverRunOutcome),
		versions: versions, bindings: bindings,
	}
}

// setAwaitResumeEligible wires the await-status probe ResumeAwaiting consults
// (set during Store construction; the await store is built after the run
// store). Probe runs outside the run mutex — the await store locks itself.
func (s *driverRunStore) setAwaitResumeEligible(probe func(ws, instanceKey string) bool) {
	s.awaitResumeEligible = probe
}

var (
	_ store.DriverRunStore         = (*driverRunStore)(nil)
	_ store.DriverRunCancelSupport = (*driverRunStore)(nil)
	_ store.DriverRunOutcomeStore  = (*driverRunStore)(nil)
)

func (s *driverRunStore) Create(ctx context.Context, in store.DriverRunCreate) (*domain.DriverRun, error) { //nolint:funlen // Validation and persisted run assembly stay adjacent.
	if in.WorkspaceKey == "" || in.RunID == "" || in.DriverID == "" || in.DriverVersionID == "" {
		return nil, fmt.Errorf("workspace_key + run_id + driver_id + driver_version_id required: %w", domain.ErrInvalid)
	}
	if s.versions != nil && !s.versions.belongsToDriver(in.WorkspaceKey, in.DriverVersionID, in.DriverID) {
		return nil, fmt.Errorf("driver version %q for driver %q in workspace %q: %w", in.DriverVersionID, in.DriverID, in.WorkspaceKey, domain.ErrNotFound)
	}
	agentServiceID, err := s.resolveAgentServiceID(ctx, in)
	if err != nil {
		return nil, err
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
		WorkspaceKey:     in.WorkspaceKey,
		RunID:            in.RunID,
		DriverID:         in.DriverID,
		DriverVersionID:  in.DriverVersionID,
		Entrypoint:       in.Entrypoint,
		SourceKind:       in.SourceKind,
		SourceRef:        in.SourceRef,
		EpicID:           in.EpicID,
		TriggerBindingID: in.TriggerBindingID,
		AgentServiceID:   agentServiceID,
		ParentRunID:      in.ParentRunID,
		Status:           domain.DriverRunQueued,
		IdempotencyKey:   in.IdempotencyKey,
		Payload:          cloneJSON(in.Payload),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	s.items[in.WorkspaceKey][in.RunID] = run
	return cloneDriverRun(run), nil
}

func (s *driverRunStore) resolveAgentServiceID(ctx context.Context, in store.DriverRunCreate) (string, error) {
	if in.TriggerBindingID == "" {
		return "", nil
	}
	if s.bindings == nil {
		return "", fmt.Errorf("trigger binding %q in workspace %q: %w", in.TriggerBindingID, in.WorkspaceKey, domain.ErrNotFound)
	}
	binding, err := s.bindings.Get(ctx, in.WorkspaceKey, in.TriggerBindingID)
	if err != nil {
		return "", err
	}
	if binding.DriverID != in.DriverID || binding.DriverVersionID != in.DriverVersionID {
		return "", fmt.Errorf("trigger binding %q does not reference driver version %q/%q: %w", in.TriggerBindingID, in.DriverID, in.DriverVersionID, domain.ErrInvalid)
	}
	return binding.TargetAgentServiceID, nil
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
	// Newest-first by StartedAt (unstarted last), CreatedAt tiebreak — the same
	// order fleet-db applies server-side. Ordering BEFORE the limit is what lets
	// callers push a limit down safely: the newest-by-StartedAt window survives
	// truncation instead of being dropped by a CreatedAt-only order.
	store.SortDriverRunsNewestFirst(out)
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
	// Explicit suspended rejection BEFORE owner validation (mirrors
	// fleet-db's heartbeat guard, AW3): a suspended run released its slot,
	// so even the formerly-owning executor must not renew it.
	if run.Status == domain.DriverRunSuspendedAwaitingEvent {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrInvalidTransition)
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
	s.enqueueRunOutcomeLocked(run)
	return cloneDriverRun(run), nil
}

// Suspend suspends a running run in suspended_awaiting_event after its await
// suspended (chunk AW4, mirroring fleet-db's suspend_driver_run.lua, AW3). Only
// the owning executor — matching node + lease + fencing token, the same
// owner guard as Finish — may suspend, and the transition releases the
// execution slot by clearing node and lease. Suspending an already-suspended
// run is an idempotent no-op (current row returned) so the driver-op layer
// can retry the pending->suspend leg safely. awaitInstanceKey names and is
// persisted as the current await cycle, including when an atomic outcome
// resolution wins the pending->suspend window.
func (s *driverRunStore) Suspend(_ context.Context, ws, runID, nodeID, leaseID string, fencingToken int64, awaitInstanceKey string) (*domain.DriverRun, error) {
	if awaitInstanceKey == "" {
		return nil, fmt.Errorf("await instance key required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][runID]
	if !ok {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	if run.Status == domain.DriverRunSuspendedAwaitingEvent {
		if run.AwaitInstanceKey == awaitInstanceKey {
			return cloneDriverRun(run), nil
		}
		return nil, fmt.Errorf("driver run %q is suspended on await %q, not %q: %w",
			runID, run.AwaitInstanceKey, awaitInstanceKey, domain.ErrInvalidTransition)
	}
	if run.NodeID != nodeID || run.LeaseID != leaseID || run.FencingToken != fencingToken {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotOwner)
	}
	if run.Status != domain.DriverRunRunning {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrInvalidTransition)
	}
	if run.ResumeSourceEventID != "" {
		if run.AwaitInstanceKey == awaitInstanceKey {
			return nil, fmt.Errorf("driver run %q await %q already resolved: %w",
				runID, awaitInstanceKey, domain.ErrDriverRunAlreadyResumed)
		}
	}
	now := time.Now().UTC()
	run.Status = domain.DriverRunSuspendedAwaitingEvent
	run.AwaitInstanceKey = awaitInstanceKey
	// A resume marker belongs to the await cycle named by AwaitInstanceKey.
	// Once the re-queued run is claimed for a later cycle, a fresh suspend
	// replaces that cycle key and clears the stale marker. This mirrors the
	// FleetDB atomic suspend operation and keeps multi-turn awaits possible.
	run.ResumeSourceEventID = ""
	run.NodeID = ""
	run.LeaseID = ""
	run.SuspendedAt = &now
	run.UpdatedAt = now
	return cloneDriverRun(run), nil
}

// ResumeAwaiting re-queues a suspended run after its await resolved
// (fleet-db's resume_awaiting_driver_run.lua, AW3). Only from
// suspended_awaiting_event, recording the resolving event id so the resumed
// execution fetches its replay payload via GetSatisfiedAwait. Of two racing
// resume attempts (matched event vs timeout) exactly one wins; the loser —
// and a resume hitting a run still inside the accepted pending->suspend window —
// gets ErrInvalidTransition, which the resume path (AW7) tolerates and
// retries once the suspend lands. The re-queued run is claimable again with
// a fresh lease and fencing token.
func (s *driverRunStore) ResumeAwaiting(_ context.Context, ws, runID, awaitInstanceKey, resumeSourceEventID string) (*domain.DriverRun, error) {
	if awaitInstanceKey == "" {
		return nil, fmt.Errorf("await instance key required: %w", domain.ErrInvalid)
	}
	if resumeSourceEventID == "" {
		return nil, fmt.Errorf("resume source event id required: %w", domain.ErrInvalid)
	}
	// The await cycle must belong to THIS run and must have resolved
	// (satisfied/timed_out) before the run may leave suspended_awaiting_event
	// — only ResolveAwait gates resume (security review fix, mirroring
	// fleet-db's INVALID/AWAIT_NOT_TERMINAL Lua guards). Checked before the
	// run lock; the await store guards itself.
	if keyRunID, _, err := domain.ParseAwaitInstanceKey(awaitInstanceKey); err != nil || keyRunID != runID {
		return nil, fmt.Errorf("await %q does not belong to driver run %q: %w", awaitInstanceKey, runID, domain.ErrInvalidTransition)
	}
	if s.awaitResumeEligible != nil && !s.awaitResumeEligible(ws, awaitInstanceKey) {
		return nil, fmt.Errorf("await %q has not resolved; resume denied: %w", awaitInstanceKey, domain.ErrInvalidTransition)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][runID]
	if !ok {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	if run.Status != domain.DriverRunSuspendedAwaitingEvent {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrInvalidTransition)
	}
	if run.AwaitInstanceKey != awaitInstanceKey {
		return nil, fmt.Errorf("driver run %q awaits %q, not %q: %w",
			runID, run.AwaitInstanceKey, awaitInstanceKey, domain.ErrInvalidTransition)
	}
	run.Status = domain.DriverRunQueued
	run.ResumeSourceEventID = resumeSourceEventID
	run.UpdatedAt = time.Now().UTC()
	return cloneDriverRun(run), nil
}

// cancelQueuedForSupersede terminalizes a still-queued run as cancelled, with
// no owner check. Mirrors fleet-db's CancelQueuedDriverRun: a claimed/running
// run is left alone (returns false) so a superseding event never cancels a run
// already executing. Returns true when it cancelled a queued run.
func (s *driverRunStore) cancelQueuedForSupersede(ws, runID, summary string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][runID]
	if !ok || run.Status != domain.DriverRunQueued {
		return false
	}
	now := time.Now().UTC()
	run.Status = domain.DriverRunCancelled
	run.Summary = summary
	run.ErrorClass = "superseded"
	run.FinishedAt = &now
	run.UpdatedAt = now
	s.enqueueRunOutcomeLocked(run)
	return true
}

// CancelQueuedRun is the store.DriverRunCancelSupport queued leg (composition
// cascade, AW10): a still-queued run terminalizes as cancelled with no owner
// check — there is no owner to fence against. Idempotent on an
// already-cancelled run; any other status (claimed in the race window,
// suspended, otherwise terminal) returns ErrInvalidTransition so an executing
// run is never terminalized out from under its executor.
func (s *driverRunStore) CancelQueuedRun(_ context.Context, ws, runID, summary, errorClass string) (*domain.DriverRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][runID]
	if !ok {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	if run.Status == domain.DriverRunCancelled {
		return cloneDriverRun(run), nil
	}
	if run.Status != domain.DriverRunQueued {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrInvalidTransition)
	}
	now := time.Now().UTC()
	run.Status = domain.DriverRunCancelled
	run.Summary = summary
	run.ErrorClass = errorClass
	run.FinishedAt = &now
	run.UpdatedAt = now
	s.enqueueRunOutcomeLocked(run)
	return cloneDriverRun(run), nil
}

// RequestCancel is the store.DriverRunCancelSupport running leg: it stamps a
// cooperative cancel request on a RUNNING run. The owning executor sees the
// marker on its next heartbeat, cancels its runner, and the run terminalizes
// through the normal fenced Finish as cancelled. Idempotent once requested;
// non-running runs return ErrInvalidTransition.
func (s *driverRunStore) RequestCancel(_ context.Context, ws, runID, reason string) (*domain.DriverRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.items[ws][runID]
	if !ok {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	if run.CancelRequestedAt != nil {
		return cloneDriverRun(run), nil
	}
	if run.Status != domain.DriverRunRunning {
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrInvalidTransition)
	}
	now := time.Now().UTC()
	run.CancelRequestedAt = &now
	run.CancelRequestedReason = reason
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
		// Defensive twin of the running-only filter below (mirrors
		// fleet-db's recover guard, AW3): a suspended run holds no lease and
		// is never stale work, however ancient its last heartbeat — only the
		// await timeout sweeper may move it on.
		if run.Status == domain.DriverRunSuspendedAwaitingEvent {
			continue
		}
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
		s.enqueueRunOutcomeLocked(run)
		plan.result.Recovered++
		plan.result.RecoveredRunIDs = append(plan.result.RecoveredRunIDs, run.RunID)
	}
	return plan.result, nil
}

func (s *driverRunStore) enqueueRunOutcomeLocked(run *domain.DriverRun) {
	if run == nil || !run.Status.IsTerminal() || run.FinishedAt == nil {
		return
	}
	if s.outcomes[run.WorkspaceKey] == nil {
		s.outcomes[run.WorkspaceKey] = make(map[string]*memDriverRunOutcome)
	}
	if _, exists := s.outcomes[run.WorkspaceKey][run.RunID]; exists {
		return
	}
	parentEventID := ""
	if s.events != nil && run.SourceRef != "" {
		s.events.mu.RLock()
		_, exists := s.events.items[run.WorkspaceKey][run.SourceRef]
		s.events.mu.RUnlock()
		if exists {
			parentEventID = run.SourceRef
		}
	}
	occurredAt := run.FinishedAt.UTC()
	s.outcomes[run.WorkspaceKey][run.RunID] = &memDriverRunOutcome{
		value: store.DriverRunOutcome{
			WorkspaceKey: run.WorkspaceKey, RunID: run.RunID, Status: run.Status,
			Summary: run.Summary, ErrorClass: run.ErrorClass, ParentRunID: run.ParentRunID,
			ParentEventID: parentEventID, EpicID: run.EpicID, OccurredAt: occurredAt,
		},
		state: "pending", availableAt: occurredAt,
	}
}

func (s *driverRunStore) ClaimDriverRunOutcomes(_ context.Context, claim store.DriverRunOutcomeClaim) ([]store.DriverRunOutcome, error) {
	if claim.WorkspaceKey == "" || claim.ClaimID == "" || claim.Limit < 0 || claim.ClaimUntil.IsZero() || !claim.ClaimUntil.After(claim.Before) {
		return nil, fmt.Errorf("invalid driver run outcome claim: %w", domain.ErrInvalid)
	}
	limit := claim.Limit
	if limit == 0 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]*memDriverRunOutcome, 0, len(s.outcomes[claim.WorkspaceKey]))
	for _, row := range s.outcomes[claim.WorkspaceKey] {
		due := row.state == "pending" && !row.availableAt.After(claim.Before)
		expired := row.state == "claimed" && !row.claimUntil.After(claim.Before)
		if due || expired {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].availableAt.Equal(rows[j].availableAt) {
			return rows[i].value.RunID < rows[j].value.RunID
		}
		return rows[i].availableAt.Before(rows[j].availableAt)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]store.DriverRunOutcome, 0, len(rows))
	for _, row := range rows {
		row.state = "claimed"
		row.claimID = claim.ClaimID
		row.claimUntil = claim.ClaimUntil
		row.value.Attempt++
		out = append(out, row.value)
	}
	return out, nil
}

func (s *driverRunStore) CompleteDriverRunOutcome(_ context.Context, completion store.DriverRunOutcomeCompletion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.outcomes[completion.WorkspaceKey][completion.RunID]
	if row == nil {
		return domain.ErrNotFound
	}
	if row.state == "delivered" && row.claimID == completion.ClaimID {
		return nil
	}
	if row.state != "claimed" || row.claimID != completion.ClaimID {
		return domain.ErrNotOwner
	}
	row.state = "delivered"
	row.claimUntil = time.Time{}
	row.lastError = ""
	return nil
}

func (s *driverRunStore) RetryDriverRunOutcome(_ context.Context, retry store.DriverRunOutcomeRetry) error {
	if retry.AvailableAt.IsZero() {
		return fmt.Errorf("driver run outcome retry available_at required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.outcomes[retry.WorkspaceKey][retry.RunID]
	if row == nil {
		return domain.ErrNotFound
	}
	if row.state != "claimed" || row.claimID != retry.ClaimID {
		return domain.ErrNotOwner
	}
	row.state = "pending"
	row.claimID = ""
	row.claimUntil = time.Time{}
	row.availableAt = retry.AvailableAt
	row.lastError = retry.Error
	return nil
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
