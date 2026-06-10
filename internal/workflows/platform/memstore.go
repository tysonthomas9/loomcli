package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// MemStore is a thread-safe in-memory Store that mirrors fleet-db's
// verified admission semantics: idempotency-key and one_active_per_epic
// hits return the EXISTING run, duplicate TaskRun IDs return
// domain.ErrAlreadyExists, ledger creation dedupes on idempotency key,
// and lifecycle transitions enforce the owner triple + fencing token.
//
// Used by unit tests and as the reference for the HTTP client's
// contract; production uses platformdb.Client.
type MemStore struct {
	mu sync.Mutex

	drivers   map[string]*Driver        // ws/driverID
	versions  map[string]*DriverVersion // ws/versionID
	runs      map[string]*DriverRun     // ws/runID
	runIdem   map[string]string         // ws/idemKey → runID
	taskRuns  map[string]*TaskRun       // ws/taskRunID
	ledger    map[string]*LedgerEntry   // ws/actionID
	ledgerIdx map[string]string         // ws/idemKey → actionID
	events    []MutationEvent           // global feed, cursor = index+1
	fence     int64

	pollCh chan struct{} // closed+replaced on each append, wakes pollers
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{
		drivers:   map[string]*Driver{},
		versions:  map[string]*DriverVersion{},
		runs:      map[string]*DriverRun{},
		runIdem:   map[string]string{},
		taskRuns:  map[string]*TaskRun{},
		ledger:    map[string]*LedgerEntry{},
		ledgerIdx: map[string]string{},
		pollCh:    make(chan struct{}),
	}
}

var _ Store = (*MemStore)(nil)

func (m *MemStore) Drivers() DriverStore            { return (*memDrivers)(m) }
func (m *MemStore) DriverRuns() DriverRunStore      { return (*memRuns)(m) }
func (m *MemStore) TaskRuns() TaskRunStore          { return (*memTaskRuns)(m) }
func (m *MemStore) ActionLedger() ActionLedgerStore { return (*memLedger)(m) }
func (m *MemStore) Events() EventStore              { return (*memEvents)(m) }

func key(ws, id string) string { return ws + "/" + id }

// AppendEvent publishes a mutation event to the feed (tests use this to
// simulate fleet-db issue/driver_run mutations; the run lifecycle
// methods also publish driver_run events like fleet-db does).
func (m *MemStore) AppendEvent(e MutationEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appendEventLocked(e)
}

func (m *MemStore) appendEventLocked(e MutationEvent) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	e.ID = fmt.Sprintf("%d-0", len(m.events)+1)
	m.events = append(m.events, e)
	close(m.pollCh)
	m.pollCh = make(chan struct{})
}

type memDrivers MemStore

func (m *memDrivers) Get(_ context.Context, ws, driverID string) (*Driver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.drivers[key(ws, driverID)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *d
	return &cp, nil
}

func (m *memDrivers) Create(_ context.Context, ws string, d Driver) (*Driver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(ws, d.DriverID)
	if _, ok := m.drivers[k]; ok {
		return nil, domain.ErrAlreadyExists
	}
	d.CreatedAt = time.Now().UTC()
	d.UpdatedAt = d.CreatedAt
	m.drivers[k] = &d
	cp := d
	return &cp, nil
}

func (m *memDrivers) CreateVersion(_ context.Context, ws, driverID string, v DriverVersion) (*DriverVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.drivers[key(ws, driverID)]; !ok {
		return nil, domain.ErrNotFound
	}
	k := key(ws, v.VersionID)
	if _, ok := m.versions[k]; ok {
		return nil, domain.ErrAlreadyExists
	}
	v.DriverID = driverID
	v.CreatedAt = time.Now().UTC()
	m.versions[k] = &v
	cp := v
	return &cp, nil
}

func (m *memDrivers) Activate(_ context.Context, ws, driverID, versionID string) (*Driver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.drivers[key(ws, driverID)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if _, ok := m.versions[key(ws, versionID)]; !ok {
		return nil, domain.ErrNotFound
	}
	d.ActiveVersionID = versionID
	d.UpdatedAt = time.Now().UTC()
	cp := *d
	return &cp, nil
}

type memRuns MemStore

func (m *memRuns) Create(_ context.Context, ws string, in DriverRunCreate) (*DriverRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(in.RunID) == "" || strings.TrimSpace(in.DriverID) == "" || strings.TrimSpace(in.DriverVersionID) == "" {
		return nil, fmt.Errorf("%w: run_id, driver_id, driver_version_id required", domain.ErrInvalid)
	}
	v, ok := m.versions[key(ws, in.DriverVersionID)]
	if !ok || v.DriverID != in.DriverID {
		return nil, domain.ErrNotFound
	}
	// Idempotency-key dedupe → return existing.
	if k := strings.TrimSpace(in.IdempotencyKey); k != "" {
		if id, ok := m.runIdem[key(ws, k)]; ok {
			cp := *m.runs[key(ws, id)]
			return &cp, nil
		}
	}
	// one_active_per_epic → return existing active run.
	if epic := strings.TrimSpace(in.EpicID); epic != "" {
		for k, r := range m.runs {
			if strings.HasPrefix(k, ws+"/") && r.EpicID == epic && !r.Status.Terminal() {
				cp := *r
				return &cp, nil
			}
		}
	}
	if _, ok := m.runs[key(ws, in.RunID)]; ok {
		return nil, domain.ErrAlreadyExists
	}
	now := time.Now().UTC()
	run := &DriverRun{
		RunID:           in.RunID,
		DriverID:        in.DriverID,
		DriverVersionID: in.DriverVersionID,
		Entrypoint:      in.Entrypoint,
		SourceKind:      in.SourceKind,
		SourceRef:       in.SourceRef,
		EpicID:          strings.TrimSpace(in.EpicID),
		Status:          DriverRunQueued,
		IdempotencyKey:  strings.TrimSpace(in.IdempotencyKey),
		Payload:         in.Payload,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	m.runs[key(ws, run.RunID)] = run
	if run.IdempotencyKey != "" {
		m.runIdem[key(ws, run.IdempotencyKey)] = run.RunID
	}
	(*MemStore)(m).appendEventLocked(MutationEvent{
		Action: "driver_run.create", EntityType: "driver_run", EntityID: run.RunID,
		Metadata: map[string]string{"epic_id": run.EpicID, "driver_id": run.DriverID, "source_kind": run.SourceKind},
	})
	cp := *run
	return &cp, nil
}

func (m *memRuns) Get(_ context.Context, ws, runID string) (*DriverRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[key(ws, runID)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (m *memRuns) List(_ context.Context, ws string, f DriverRunFilter) ([]*DriverRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*DriverRun
	for k, r := range m.runs {
		if !strings.HasPrefix(k, ws+"/") {
			continue
		}
		if f.DriverID != "" && r.DriverID != f.DriverID {
			continue
		}
		if f.EpicID != "" && r.EpicID != f.EpicID {
			continue
		}
		if f.Status != "" && r.Status != f.Status {
			continue
		}
		cp := *r
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (m *memRuns) Claim(_ context.Context, ws, runID, nodeID, leaseID string) (*DriverRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[key(ws, runID)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if r.NodeID != "" {
		return nil, fmt.Errorf("%w: already claimed by %s", domain.ErrConflict, r.NodeID)
	}
	if r.Status != DriverRunQueued {
		return nil, fmt.Errorf("%w: status %s", domain.ErrConflict, r.Status)
	}
	m.fence++
	now := time.Now().UTC()
	r.Status = DriverRunRunning
	r.NodeID = nodeID
	r.LeaseID = leaseID
	r.FencingToken = m.fence
	r.StartedAt = now
	r.LastHeartbeat = now
	r.UpdatedAt = now
	(*MemStore)(m).appendEventLocked(MutationEvent{
		Action: "driver_run.claim", EntityType: "driver_run", EntityID: r.RunID,
		Metadata: map[string]string{"epic_id": r.EpicID, "driver_id": r.DriverID, "source_kind": r.SourceKind},
	})
	cp := *r
	return &cp, nil
}

func (m *memRuns) ownerLocked(ws, runID, nodeID, leaseID string, fence int64) (*DriverRun, error) {
	r, ok := m.runs[key(ws, runID)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if r.NodeID != nodeID || r.LeaseID != leaseID || r.FencingToken != fence {
		return nil, fmt.Errorf("%w: not owner", domain.ErrConflict)
	}
	if r.Status != DriverRunRunning {
		return nil, fmt.Errorf("%w: status %s", domain.ErrConflict, r.Status)
	}
	return r, nil
}

func (m *memRuns) Heartbeat(_ context.Context, ws, runID, nodeID, leaseID string, fence int64) (*DriverRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.ownerLocked(ws, runID, nodeID, leaseID, fence)
	if err != nil {
		return nil, err
	}
	r.LastHeartbeat = time.Now().UTC()
	r.UpdatedAt = r.LastHeartbeat
	cp := *r
	return &cp, nil
}

func (m *memRuns) Finish(_ context.Context, ws, runID, nodeID, leaseID string, fence int64, in DriverRunFinish) (*DriverRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.ownerLocked(ws, runID, nodeID, leaseID, fence)
	if err != nil {
		return nil, err
	}
	if !in.Status.Terminal() {
		return nil, fmt.Errorf("%w: %s is not terminal", domain.ErrInvalid, in.Status)
	}
	now := time.Now().UTC()
	r.Status = in.Status
	r.Summary = in.Summary
	r.ErrorClass = in.ErrorClass
	r.Output = in.Output
	r.FinishedAt = &now
	r.UpdatedAt = now
	(*MemStore)(m).appendEventLocked(MutationEvent{
		Action: "driver_run.finish", EntityType: "driver_run", EntityID: r.RunID,
		Metadata: map[string]string{"status": string(in.Status), "epic_id": r.EpicID, "driver_id": r.DriverID, "source_kind": r.SourceKind},
	})
	cp := *r
	return &cp, nil
}

func (m *memRuns) RecoverStale(_ context.Context, ws string, maxAgeSeconds int64, errorClass, summary string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if maxAgeSeconds <= 0 {
		maxAgeSeconds = 300
	}
	cutoff := time.Now().UTC().Add(-time.Duration(maxAgeSeconds) * time.Second)
	var recovered []string
	for k, r := range m.runs {
		if !strings.HasPrefix(k, ws+"/") || r.Status != DriverRunRunning {
			continue
		}
		hb := r.LastHeartbeat
		if hb.IsZero() {
			hb = r.CreatedAt
		}
		if hb.After(cutoff) {
			continue
		}
		now := time.Now().UTC()
		r.Status = DriverRunFailed
		r.ErrorClass = errorClass
		r.Summary = summary
		r.FinishedAt = &now
		r.UpdatedAt = now
		(*MemStore)(m).appendEventLocked(MutationEvent{
			Action: "driver_run.recover", EntityType: "driver_run", EntityID: r.RunID,
			Metadata: map[string]string{"epic_id": r.EpicID, "driver_id": r.DriverID, "source_kind": r.SourceKind},
		})
		recovered = append(recovered, r.RunID)
	}
	return recovered, nil
}

func (m *memRuns) Events(_ context.Context, ws, runID, after string, limit int) ([]RunEvent, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[key(ws, runID)]; !ok {
		return nil, "", domain.ErrNotFound
	}
	if limit <= 0 {
		limit = 100
	}
	afterSeq := 0
	if after != "" && after != "0" {
		if _, err := fmt.Sscanf(after, "%d-0", &afterSeq); err != nil {
			return nil, "", fmt.Errorf("%w: bad cursor %q", domain.ErrInvalid, after)
		}
	}
	var out []RunEvent
	cursor := after
	for _, e := range m.events {
		if e.EntityType != "driver_run" || e.EntityID != runID {
			continue
		}
		var seq int
		if _, err := fmt.Sscanf(e.ID, "%d-0", &seq); err != nil || seq <= afterSeq {
			continue
		}
		out = append(out, RunEvent(e))
		cursor = e.ID
		if len(out) >= limit {
			break
		}
	}
	return out, cursor, nil
}

type memTaskRuns MemStore

func (m *memTaskRuns) Create(_ context.Context, ws string, in TaskRunCreate) (*TaskRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(in.TaskRunID) == "" || strings.TrimSpace(in.TaskID) == "" {
		return nil, fmt.Errorf("%w: task_run_id and task_id required", domain.ErrInvalid)
	}
	if in.DriverRunID != "" {
		if _, ok := m.runs[key(ws, in.DriverRunID)]; !ok {
			return nil, domain.ErrNotFound
		}
	}
	k := key(ws, in.TaskRunID)
	if _, ok := m.taskRuns[k]; ok {
		return nil, domain.ErrAlreadyExists
	}
	now := time.Now().UTC()
	tr := &TaskRun{
		TaskRunID:   in.TaskRunID,
		DriverRunID: in.DriverRunID,
		TaskID:      in.TaskID,
		Status:      TaskRunQueued,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	m.taskRuns[k] = tr
	cp := *tr
	return &cp, nil
}

func (m *memTaskRuns) List(_ context.Context, ws string, f TaskRunFilter) ([]*TaskRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*TaskRun
	for k, tr := range m.taskRuns {
		if !strings.HasPrefix(k, ws+"/") {
			continue
		}
		if f.DriverRunID != "" && tr.DriverRunID != f.DriverRunID {
			continue
		}
		if f.TaskID != "" && tr.TaskID != f.TaskID {
			continue
		}
		if f.Status != "" && tr.Status != f.Status {
			continue
		}
		cp := *tr
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

type memLedger MemStore

func (m *memLedger) Create(_ context.Context, ws string, in LedgerCreate) (*LedgerEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(in.IdempotencyKey) == "" || strings.TrimSpace(in.ActionType) == "" || strings.TrimSpace(in.TargetRef) == "" {
		return nil, fmt.Errorf("%w: idempotency_key, action_type, target_ref required", domain.ErrInvalid)
	}
	if id, ok := m.ledgerIdx[key(ws, in.IdempotencyKey)]; ok {
		cp := *m.ledger[key(ws, id)]
		return &cp, nil
	}
	entry := &LedgerEntry{
		ActionID:       fmt.Sprintf("action-%d", len(m.ledger)+1),
		IdempotencyKey: in.IdempotencyKey,
		ActionType:     in.ActionType,
		TargetRef:      in.TargetRef,
		Status:         LedgerPending,
		CreatedAt:      time.Now().UTC(),
	}
	m.ledger[key(ws, entry.ActionID)] = entry
	m.ledgerIdx[key(ws, in.IdempotencyKey)] = entry.ActionID
	cp := *entry
	return &cp, nil
}

func (m *memLedger) Complete(_ context.Context, ws, actionID string, status LedgerStatus) (*LedgerEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.ledger[key(ws, actionID)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	switch status {
	case LedgerApplied, LedgerFailed, LedgerSkipped:
	default:
		return nil, fmt.Errorf("%w: %s is not terminal", domain.ErrInvalid, status)
	}
	if e.Status != LedgerPending {
		if e.Status == status {
			cp := *e
			return &cp, nil
		}
		return nil, fmt.Errorf("%w: already %s", domain.ErrConflict, e.Status)
	}
	now := time.Now().UTC()
	e.Status = status
	e.AppliedAt = &now
	cp := *e
	return &cp, nil
}

type memEvents MemStore

func (m *memEvents) Poll(ctx context.Context, _ string, req MutationPoll) (*MutationPage, error) {
	deadline := time.Now().Add(req.Timeout)
	for {
		m.mu.Lock()
		start := 0
		if req.Since != "" && req.Since != "0" {
			if _, err := fmt.Sscanf(req.Since, "%d-0", &start); err != nil {
				m.mu.Unlock()
				return nil, fmt.Errorf("%w: bad cursor %q", domain.ErrInvalid, req.Since)
			}
		}
		limit := req.Limit
		if limit <= 0 {
			limit = 100
		}
		ch := m.pollCh
		if start < len(m.events) {
			end := min(len(m.events), start+limit)
			page := &MutationPage{
				Events:  append([]MutationEvent(nil), m.events[start:end]...),
				Cursor:  fmt.Sprintf("%d-0", end),
				HasMore: end < len(m.events),
			}
			m.mu.Unlock()
			return page, nil
		}
		cursor := fmt.Sprintf("%d-0", len(m.events))
		m.mu.Unlock()

		if req.Timeout <= 0 || time.Now().After(deadline) {
			return &MutationPage{Cursor: cursor}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
		case <-time.After(time.Until(deadline)):
		}
	}
}
