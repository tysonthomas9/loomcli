package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type driverStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.Driver
}

func newDriverStore() *driverStore {
	return &driverStore{items: make(map[string]map[string]*domain.Driver)}
}

var _ store.DriverStore = (*driverStore)(nil)

func (s *driverStore) Create(_ context.Context, in store.DriverCreate) (*domain.Driver, error) {
	if in.WorkspaceKey == "" || in.DriverID == "" || in.Name == "" {
		return nil, fmt.Errorf("workspace_key + driver_id + name required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.Driver)
	}
	if _, ok := s.items[in.WorkspaceKey][in.DriverID]; ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", in.DriverID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	ownerType := in.OwnerType
	if ownerType == "" {
		ownerType = domain.DriverOwnerUser
	}
	status := in.Status
	if status == "" {
		status = domain.DriverStatusDraft
	}
	driver := &domain.Driver{
		WorkspaceKey:    in.WorkspaceKey,
		DriverID:        in.DriverID,
		Name:            in.Name,
		OwnerType:       ownerType,
		OwnerRef:        in.OwnerRef,
		Description:     in.Description,
		ActiveVersionID: in.ActiveVersionID,
		Status:          status,
		Metadata:        cloneMap(in.Metadata),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.items[in.WorkspaceKey][in.DriverID] = driver
	return cloneDriver(driver), nil
}

func (s *driverStore) Get(_ context.Context, ws, driverID string) (*domain.Driver, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	driver, ok := s.items[ws][driverID]
	if !ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", driverID, ws, domain.ErrNotFound)
	}
	return cloneDriver(driver), nil
}

func (s *driverStore) List(_ context.Context, ws string, filter store.DriverFilter) ([]*domain.Driver, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Driver, 0, len(s.items[ws]))
	for _, driver := range s.items[ws] {
		if driverMatchesMem(driver, filter) {
			out = append(out, cloneDriver(driver))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *driverStore) Update(_ context.Context, ws, driverID string, patch store.DriverUpdate) (*domain.Driver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	driver, ok := s.items[ws][driverID]
	if !ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", driverID, ws, domain.ErrNotFound)
	}
	if patch.Name != nil {
		driver.Name = *patch.Name
	}
	if patch.OwnerType != nil {
		driver.OwnerType = *patch.OwnerType
	}
	if patch.OwnerRef != nil {
		driver.OwnerRef = *patch.OwnerRef
	}
	if patch.Description != nil {
		driver.Description = *patch.Description
	}
	if patch.ActiveVersionID != nil {
		driver.ActiveVersionID = *patch.ActiveVersionID
	}
	if patch.Status != nil {
		driver.Status = *patch.Status
	}
	if patch.Metadata != nil {
		driver.Metadata = cloneMap(*patch.Metadata)
	}
	driver.UpdatedAt = time.Now().UTC()
	return cloneDriver(driver), nil
}

func (s *driverStore) exists(ws, driverID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.items[ws][driverID]
	return ok
}

type driverVersionStore struct {
	mu      sync.RWMutex
	items   map[string]map[string]*domain.DriverVersion
	drivers *driverStore
}

func newDriverVersionStore(drivers *driverStore) *driverVersionStore {
	return &driverVersionStore{items: make(map[string]map[string]*domain.DriverVersion), drivers: drivers}
}

var _ store.DriverVersionStore = (*driverVersionStore)(nil)

func (s *driverVersionStore) Create(_ context.Context, in store.DriverVersionCreate) (*domain.DriverVersion, error) {
	if in.WorkspaceKey == "" || in.VersionID == "" || in.DriverID == "" || in.Version <= 0 || in.SourceDigest == "" || in.BundleDigest == "" {
		return nil, fmt.Errorf("workspace_key + version_id + driver_id + version + source_digest + bundle_digest required: %w", domain.ErrInvalid)
	}
	if s.drivers != nil && !s.drivers.exists(in.WorkspaceKey, in.DriverID) {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", in.DriverID, in.WorkspaceKey, domain.ErrNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.DriverVersion)
	}
	if _, ok := s.items[in.WorkspaceKey][in.VersionID]; ok {
		return nil, fmt.Errorf("driver version %q in workspace %q: %w", in.VersionID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	status := in.ValidationStatus
	if status == "" {
		status = domain.DriverVersionValidationPending
	}
	version := &domain.DriverVersion{
		WorkspaceKey:     in.WorkspaceKey,
		VersionID:        in.VersionID,
		DriverID:         in.DriverID,
		Version:          in.Version,
		SourceRef:        in.SourceRef,
		SourceDigest:     in.SourceDigest,
		BundleRef:        in.BundleRef,
		BundleDigest:     in.BundleDigest,
		Runtime:          in.Runtime,
		Manifest:         cloneMap(in.Manifest),
		BuildDiagnostics: in.BuildDiagnostics,
		ValidationStatus: status,
		CreatedBy:        in.CreatedBy,
		CreatedAt:        time.Now().UTC(),
	}
	s.items[in.WorkspaceKey][in.VersionID] = version
	return cloneDriverVersion(version), nil
}

func (s *driverVersionStore) Get(_ context.Context, ws, versionID string) (*domain.DriverVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	version, ok := s.items[ws][versionID]
	if !ok {
		return nil, fmt.Errorf("driver version %q in workspace %q: %w", versionID, ws, domain.ErrNotFound)
	}
	return cloneDriverVersion(version), nil
}

func (s *driverVersionStore) List(_ context.Context, ws string, filter store.DriverVersionFilter) ([]*domain.DriverVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.DriverVersion, 0, len(s.items[ws]))
	for _, version := range s.items[ws] {
		if driverVersionMatchesMem(version, filter) {
			out = append(out, cloneDriverVersion(version))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *driverVersionStore) belongsToDriver(ws, versionID, driverID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	version, ok := s.items[ws][versionID]
	return ok && version.DriverID == driverID
}

type triggerBindingStore struct {
	mu       sync.RWMutex
	items    map[string]map[string]*domain.TriggerBinding
	versions *driverVersionStore
	services *agentServiceStore
}

func newTriggerBindingStore(versions *driverVersionStore, services *agentServiceStore) *triggerBindingStore {
	return &triggerBindingStore{items: make(map[string]map[string]*domain.TriggerBinding), versions: versions, services: services}
}

var _ store.TriggerBindingStore = (*triggerBindingStore)(nil)

func (s *triggerBindingStore) Create(_ context.Context, in store.TriggerBindingCreate) (*domain.TriggerBinding, error) {
	if in.WorkspaceKey == "" || in.BindingID == "" || in.Name == "" || in.SourceKind == "" || in.DriverID == "" || in.DriverVersionID == "" {
		return nil, fmt.Errorf("workspace_key + binding_id + name + source_kind + driver_id + driver_version_id required: %w", domain.ErrInvalid)
	}
	if s.versions != nil && !s.versions.belongsToDriver(in.WorkspaceKey, in.DriverVersionID, in.DriverID) {
		return nil, fmt.Errorf("driver version %q for driver %q in workspace %q: %w", in.DriverVersionID, in.DriverID, in.WorkspaceKey, domain.ErrNotFound)
	}
	if in.TargetAgentServiceID != "" && s.services != nil && !s.services.exists(in.WorkspaceKey, in.TargetAgentServiceID) {
		return nil, fmt.Errorf("target agent service %q in workspace %q: %w", in.TargetAgentServiceID, in.WorkspaceKey, domain.ErrNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.TriggerBinding)
	}
	if _, ok := s.items[in.WorkspaceKey][in.BindingID]; ok {
		return nil, fmt.Errorf("trigger binding %q in workspace %q: %w", in.BindingID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	if in.RouteKey != "" {
		for _, binding := range s.items[in.WorkspaceKey] {
			if binding.RouteKey == in.RouteKey {
				return nil, fmt.Errorf("trigger binding route %q in workspace %q: %w", in.RouteKey, in.WorkspaceKey, domain.ErrAlreadyExists)
			}
		}
	}
	policy := in.ConcurrencyPolicy
	if policy == "" {
		policy = domain.TriggerBindingConcurrencyOneActivePerEpic
	}
	idempotencyPolicy := in.IdempotencyPolicy
	if idempotencyPolicy == "" {
		idempotencyPolicy = "header:Idempotency-Key"
	}
	authPolicy := in.AuthPolicy
	if authPolicy == "" {
		authPolicy = "workspace_user"
	}
	now := time.Now().UTC()
	binding := &domain.TriggerBinding{
		WorkspaceKey:         in.WorkspaceKey,
		BindingID:            in.BindingID,
		Name:                 in.Name,
		SourceKind:           in.SourceKind,
		SourceRef:            in.SourceRef,
		SourceConfigRef:      in.SourceConfigRef,
		RouteKey:             in.RouteKey,
		Method:               in.Method,
		PathTemplate:         in.PathTemplate,
		Topic:                in.Topic,
		EventTypePatterns:    append([]string(nil), in.EventTypePatterns...),
		FilterRef:            in.FilterRef,
		DriverID:             in.DriverID,
		DriverVersionID:      in.DriverVersionID,
		TargetEntrypoint:     in.TargetEntrypoint,
		TargetAgentServiceID: in.TargetAgentServiceID,
		ConcurrencyPolicy:    policy,
		IdempotencyPolicy:    idempotencyPolicy,
		AuthPolicy:           authPolicy,
		Permissions:          append([]string(nil), in.Permissions...),
		Enabled:              in.Enabled,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	s.items[in.WorkspaceKey][in.BindingID] = binding
	return cloneTriggerBinding(binding), nil
}

func (s *triggerBindingStore) Get(_ context.Context, ws, bindingID string) (*domain.TriggerBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.items[ws][bindingID]
	if !ok {
		return nil, fmt.Errorf("trigger binding %q in workspace %q: %w", bindingID, ws, domain.ErrNotFound)
	}
	return cloneTriggerBinding(binding), nil
}

func (s *triggerBindingStore) GetByRouteKey(_ context.Context, ws, routeKey string) (*domain.TriggerBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, binding := range s.items[ws] {
		if binding.RouteKey == routeKey {
			return cloneTriggerBinding(binding), nil
		}
	}
	return nil, fmt.Errorf("trigger binding route %q in workspace %q: %w", routeKey, ws, domain.ErrNotFound)
}

func (s *triggerBindingStore) List(_ context.Context, ws string, filter store.TriggerBindingFilter) ([]*domain.TriggerBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.TriggerBinding, 0, len(s.items[ws]))
	for _, binding := range s.items[ws] {
		if triggerBindingMatchesMem(binding, filter) {
			out = append(out, cloneTriggerBinding(binding))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *triggerBindingStore) Update(_ context.Context, ws, bindingID string, patch store.TriggerBindingUpdate) (*domain.TriggerBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.items[ws][bindingID]
	if !ok {
		return nil, fmt.Errorf("trigger binding %q in workspace %q: %w", bindingID, ws, domain.ErrNotFound)
	}
	oldRoute := binding.RouteKey
	updated := cloneTriggerBinding(binding)
	applyTriggerBindingUpdateMem(updated, patch)
	if s.versions != nil && !s.versions.belongsToDriver(updated.WorkspaceKey, updated.DriverVersionID, updated.DriverID) {
		return nil, fmt.Errorf("driver version %q for driver %q in workspace %q: %w", updated.DriverVersionID, updated.DriverID, updated.WorkspaceKey, domain.ErrNotFound)
	}
	if updated.TargetAgentServiceID != "" && s.services != nil && !s.services.exists(updated.WorkspaceKey, updated.TargetAgentServiceID) {
		return nil, fmt.Errorf("target agent service %q in workspace %q: %w", updated.TargetAgentServiceID, updated.WorkspaceKey, domain.ErrNotFound)
	}
	if s.services != nil && !s.services.triggerRefTargetCompatible(updated.WorkspaceKey, updated.BindingID, updated.TargetAgentServiceID) {
		return nil, fmt.Errorf("trigger binding %q target %q would invalidate agent service trigger refs: %w", updated.BindingID, updated.TargetAgentServiceID, domain.ErrInvalidTransition)
	}
	if updated.RouteKey != "" && updated.RouteKey != oldRoute {
		for id, existing := range s.items[ws] {
			if id != bindingID && existing.RouteKey == updated.RouteKey {
				return nil, fmt.Errorf("trigger binding route %q in workspace %q: %w", updated.RouteKey, ws, domain.ErrAlreadyExists)
			}
		}
	}
	updated.UpdatedAt = time.Now().UTC()
	s.items[ws][bindingID] = updated
	return cloneTriggerBinding(updated), nil
}

func (s *triggerBindingStore) getForValidation(ws, bindingID string) (*domain.TriggerBinding, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.items[ws][bindingID]
	if !ok {
		return nil, false
	}
	return cloneTriggerBinding(binding), true
}

func (s *triggerBindingStore) hasTargetAgentService(ws, serviceID string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, binding := range s.items[ws] {
		if binding.TargetAgentServiceID == serviceID {
			return true
		}
	}
	return false
}

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
	if ws == "" {
		return nil, fmt.Errorf("workspace_key required: %w", domain.ErrInvalid)
	}
	if recover.MaxAgeSeconds < 0 || recover.Limit < 0 {
		return nil, fmt.Errorf("stale driver run recovery values must be non-negative: %w", domain.ErrInvalid)
	}
	recoveredAt := time.Now().UTC()
	staleBefore := recover.StaleBefore
	if staleBefore.IsZero() {
		maxAge := 5 * time.Minute
		if recover.MaxAgeSeconds > 0 {
			maxAge = time.Duration(recover.MaxAgeSeconds) * time.Second
		}
		staleBefore = recoveredAt.Add(-maxAge)
	}
	errorClass := strings.TrimSpace(recover.ErrorClass)
	if errorClass == "" {
		errorClass = "stale_driver_run"
	}
	summary := strings.TrimSpace(recover.Summary)
	if summary == "" {
		summary = "driver run heartbeat stale before " + staleBefore.Format(time.RFC3339Nano)
	}
	result := &store.StaleDriverRunRecoveryResult{
		WorkspaceKey: ws,
		StaleBefore:  staleBefore,
		RecoveredAt:  recoveredAt,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.items[ws] {
		if run.Status != domain.DriverRunRunning {
			continue
		}
		lastHeartbeat := driverRunRecoveryHeartbeatMem(run)
		if lastHeartbeat.IsZero() || !lastHeartbeat.Before(staleBefore) {
			result.SkippedFresh++
			result.SkippedFreshRunIDs = append(result.SkippedFreshRunIDs, run.RunID)
			continue
		}
		if recover.Limit > 0 && result.Recovered >= recover.Limit {
			break
		}
		run.Status = domain.DriverRunFailed
		run.Summary = summary
		run.ErrorClass = errorClass
		run.FinishedAt = &recoveredAt
		run.UpdatedAt = recoveredAt
		result.Recovered++
		result.RecoveredRunIDs = append(result.RecoveredRunIDs, run.RunID)
	}
	return result, nil
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
	if ws == "" || runID == "" {
		return nil, fmt.Errorf("workspace_key + run_id required: %w", domain.ErrInvalid)
	}
	if recover.MaxAgeSeconds < 0 {
		return nil, fmt.Errorf("max_age_seconds must be non-negative: %w", domain.ErrInvalid)
	}
	recoveredAt := time.Now().UTC()
	staleBefore := recover.StaleBefore
	if staleBefore.IsZero() {
		maxAge := 5 * time.Minute
		if recover.MaxAgeSeconds > 0 {
			maxAge = time.Duration(recover.MaxAgeSeconds) * time.Second
		}
		staleBefore = recoveredAt.Add(-maxAge)
	}
	errorClass := strings.TrimSpace(recover.ErrorClass)
	if errorClass == "" {
		errorClass = "stale_task_run"
	}
	errorMessage := strings.TrimSpace(recover.ErrorMessage)
	if errorMessage == "" {
		errorMessage = "task run heartbeat is stale"
	}

	s.mu.RLock()
	if _, ok := s.items[ws][runID]; !ok {
		s.mu.RUnlock()
		return nil, fmt.Errorf("driver run %q in workspace %q: %w", runID, ws, domain.ErrNotFound)
	}
	s.mu.RUnlock()

	result := &store.StaleTaskRunRecoveryResult{
		WorkspaceKey: ws,
		DriverRunID:  runID,
		StaleBefore:  staleBefore.UTC(),
		RecoveredAt:  recoveredAt,
	}
	if s.taskRuns == nil {
		return result, nil
	}

	recoveredTaskRunIDs := make(map[string]struct{})
	recoveredStepIDs := make(map[string]struct{})
	s.taskRuns.mu.Lock()
	for _, taskRun := range s.taskRuns.items[ws] {
		if taskRun.DriverRunID != runID || taskRun.Status != domain.TaskRunRunning {
			continue
		}
		if !taskRunLastObservedMem(taskRun).Before(staleBefore) {
			result.SkippedFresh++
			continue
		}
		taskRun.Status = domain.TaskRunFailed
		taskRun.ErrorClass = errorClass
		taskRun.ErrorMessage = errorMessage
		taskRun.FinishedAt = &recoveredAt
		taskRun.UpdatedAt = recoveredAt
		result.Recovered++
		result.RecoveredTaskRunIDs = append(result.RecoveredTaskRunIDs, taskRun.TaskRunID)
		recoveredTaskRunIDs[taskRun.TaskRunID] = struct{}{}
		if taskRun.DriverStepID != "" {
			recoveredStepIDs[taskRun.DriverStepID] = struct{}{}
		}
	}
	s.taskRuns.mu.Unlock()
	sort.Strings(result.RecoveredTaskRunIDs)

	if s.steps != nil && len(recoveredTaskRunIDs) > 0 {
		s.steps.mu.Lock()
		for _, step := range s.steps.items[ws] {
			if step.DriverRunID != runID || step.Status.IsTerminal() {
				continue
			}
			_, linkedByStepID := recoveredStepIDs[step.StepID]
			_, linkedByTaskRunID := recoveredTaskRunIDs[step.TaskRunID]
			if !linkedByStepID && !linkedByTaskRunID {
				continue
			}
			step.Status = domain.DriverStepFailed
			if step.EndedAt == nil {
				step.EndedAt = &recoveredAt
			}
			step.UpdatedAt = recoveredAt
		}
		s.steps.mu.Unlock()
	}
	return result, nil
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
	status := in.Status
	if status == "" {
		status = domain.TaskRunQueued
	}
	now := time.Now().UTC()
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
	s.items[in.WorkspaceKey][in.TaskRunID] = run
	return cloneTaskRun(run), nil
}

func (s *taskRunStore) ClaimQueued(_ context.Context, ws string, claim store.TaskRunClaim) (*domain.TaskRun, error) {
	claim.TaskRunID = strings.TrimSpace(claim.TaskRunID)
	claim.NodeID = strings.TrimSpace(claim.NodeID)
	claim.RunnerID = strings.TrimSpace(claim.RunnerID)
	claim.LeaseID = strings.TrimSpace(claim.LeaseID)
	if claim.NodeID == "" || claim.LeaseID == "" {
		return nil, fmt.Errorf("task run claim owner required in workspace %q: %w", ws, domain.ErrInvalidTransition)
	}
	now := claim.ClaimedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
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
	if complete.CompletionID == "" || !complete.Status.IsTerminal() {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	if complete.CloseTask && complete.Status != domain.TaskRunCompleted {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	if complete.RequireArtifacts && len(complete.RequiredArtifactIDs) == 0 && strings.TrimSpace(complete.ArtifactsRef) == "" {
		return nil, fmt.Errorf("task run %q in workspace %q: %w", taskRunID, ws, domain.ErrInvalidTransition)
	}
	if !taskRunUsageValuesValidMem(complete.InputTokens, complete.OutputTokens, complete.CacheReadTokens, complete.CacheWriteTokens, complete.EstimatedCostUSD) {
		return nil, fmt.Errorf("task run usage values must be finite and non-negative")
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
	if s.completions[ws] == nil {
		s.completions[ws] = make(map[string]string)
	}
	s.completions[ws][complete.CompletionID] = taskRunID
	return cloneTaskRun(run), nil
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

func cloneDriver(d *domain.Driver) *domain.Driver {
	out := *d
	out.Metadata = cloneMap(d.Metadata)
	return &out
}

func cloneDriverVersion(v *domain.DriverVersion) *domain.DriverVersion {
	out := *v
	out.Manifest = cloneMap(v.Manifest)
	return &out
}

func cloneTriggerBinding(b *domain.TriggerBinding) *domain.TriggerBinding {
	out := *b
	out.EventTypePatterns = append([]string(nil), b.EventTypePatterns...)
	out.Permissions = append([]string(nil), b.Permissions...)
	return &out
}

func cloneDriverRun(r *domain.DriverRun) *domain.DriverRun {
	out := *r
	out.Payload = cloneJSON(r.Payload)
	out.Output = cloneMap(r.Output)
	out.FinishedAt = clonePtr(r.FinishedAt)
	return &out
}

func cloneJSON(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 {
		return json.RawMessage(`{}`)
	}
	out := make(json.RawMessage, len(payload))
	copy(out, payload)
	return out
}

func cloneDriverStep(s *domain.DriverStep) *domain.DriverStep {
	if s == nil {
		return nil
	}
	out := *s
	out.EndedAt = clonePtr(s.EndedAt)
	return &out
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

func driverMatchesMem(d *domain.Driver, f store.DriverFilter) bool {
	return (f.Name == "" || d.Name == f.Name) && (f.Status == "" || d.Status == f.Status)
}

func driverVersionMatchesMem(v *domain.DriverVersion, f store.DriverVersionFilter) bool {
	return (f.DriverID == "" || v.DriverID == f.DriverID) && (f.ValidationStatus == "" || v.ValidationStatus == f.ValidationStatus)
}

func triggerBindingMatchesMem(b *domain.TriggerBinding, f store.TriggerBindingFilter) bool {
	return (f.SourceKind == "" || b.SourceKind == f.SourceKind) &&
		(f.RouteKey == "" || b.RouteKey == f.RouteKey) &&
		(f.DriverID == "" || b.DriverID == f.DriverID) &&
		(f.TargetAgentServiceID == "" || b.TargetAgentServiceID == f.TargetAgentServiceID) &&
		(f.Enabled == nil || b.Enabled == *f.Enabled)
}

func driverRunMatchesMem(r *domain.DriverRun, f store.DriverRunFilter) bool {
	return (f.DriverID == "" || r.DriverID == f.DriverID) &&
		(f.DriverVersionID == "" || r.DriverVersionID == f.DriverVersionID) &&
		(f.EpicID == "" || r.EpicID == f.EpicID) &&
		(f.NodeID == "" || r.NodeID == f.NodeID) &&
		(f.Status == "" || r.Status == f.Status)
}

func driverStepMatchesMem(s *domain.DriverStep, f store.DriverStepFilter) bool {
	return (f.DriverRunID == "" || s.DriverRunID == f.DriverRunID) &&
		(f.TaskRunID == "" || s.TaskRunID == f.TaskRunID) &&
		(f.ActionLedgerID == "" || s.ActionLedgerID == f.ActionLedgerID) &&
		(f.StepKind == "" || s.StepKind == f.StepKind) &&
		(f.Status == "" || s.Status == f.Status)
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

func applyTriggerBindingUpdateMem(b *domain.TriggerBinding, patch store.TriggerBindingUpdate) {
	if patch.Name != nil {
		b.Name = *patch.Name
	}
	if patch.SourceKind != nil {
		b.SourceKind = *patch.SourceKind
	}
	if patch.SourceRef != nil {
		b.SourceRef = *patch.SourceRef
	}
	if patch.SourceConfigRef != nil {
		b.SourceConfigRef = *patch.SourceConfigRef
	}
	if patch.RouteKey != nil {
		b.RouteKey = *patch.RouteKey
	}
	if patch.Method != nil {
		b.Method = *patch.Method
	}
	if patch.PathTemplate != nil {
		b.PathTemplate = *patch.PathTemplate
	}
	if patch.Topic != nil {
		b.Topic = *patch.Topic
	}
	if patch.EventTypePatterns != nil {
		b.EventTypePatterns = append([]string(nil), (*patch.EventTypePatterns)...)
	}
	if patch.FilterRef != nil {
		b.FilterRef = *patch.FilterRef
	}
	if patch.DriverID != nil {
		b.DriverID = *patch.DriverID
	}
	if patch.DriverVersionID != nil {
		b.DriverVersionID = *patch.DriverVersionID
	}
	if patch.TargetEntrypoint != nil {
		b.TargetEntrypoint = *patch.TargetEntrypoint
	}
	if patch.TargetAgentServiceID != nil {
		b.TargetAgentServiceID = *patch.TargetAgentServiceID
	}
	if patch.ConcurrencyPolicy != nil {
		b.ConcurrencyPolicy = *patch.ConcurrencyPolicy
	}
	if patch.IdempotencyPolicy != nil {
		b.IdempotencyPolicy = *patch.IdempotencyPolicy
	}
	if patch.AuthPolicy != nil {
		b.AuthPolicy = *patch.AuthPolicy
	}
	if patch.Permissions != nil {
		b.Permissions = append([]string(nil), (*patch.Permissions)...)
	}
	if patch.Enabled != nil {
		b.Enabled = *patch.Enabled
	}
}
