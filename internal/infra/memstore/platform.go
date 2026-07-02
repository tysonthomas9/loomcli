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
	trust := in.TrustLevel
	if trust == "" {
		// Unknown/missing = untrusted (fail closed) — mirrors fleet-db's
		// Driver.Validate stamp; registration paths set an explicit level.
		trust = domain.DriverTrustUntrusted
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
		TrustLevel:      trust,
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
	if patch.TrustLevel != nil {
		driver.TrustLevel = *patch.TrustLevel
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
	in = in.WithDerivedRoute()
	if err := s.validateTriggerBindingCreate(in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.TriggerBinding)
	}
	if _, ok := s.items[in.WorkspaceKey][in.BindingID]; ok {
		return nil, fmt.Errorf("trigger binding %q in workspace %q: %w", in.BindingID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	if err := s.ensureTriggerBindingRouteAvailableLocked(in); err != nil {
		return nil, err
	}
	binding := newTriggerBindingMem(in)
	s.items[in.WorkspaceKey][in.BindingID] = binding
	return redactedTriggerBinding(binding), nil
}

func (s *triggerBindingStore) validateTriggerBindingCreate(in store.TriggerBindingCreate) error {
	if in.WorkspaceKey == "" || in.BindingID == "" || in.Name == "" || in.SourceKind == "" || in.DriverID == "" || in.DriverVersionID == "" {
		return fmt.Errorf("workspace_key + binding_id + name + source_kind + driver_id + driver_version_id required: %w", domain.ErrInvalid)
	}
	if s.versions != nil && !s.versions.belongsToDriver(in.WorkspaceKey, in.DriverVersionID, in.DriverID) {
		return fmt.Errorf("driver version %q for driver %q in workspace %q: %w", in.DriverVersionID, in.DriverID, in.WorkspaceKey, domain.ErrNotFound)
	}
	if in.TargetAgentServiceID != "" && s.services != nil && !s.services.exists(in.WorkspaceKey, in.TargetAgentServiceID) {
		return fmt.Errorf("target agent service %q in workspace %q: %w", in.TargetAgentServiceID, in.WorkspaceKey, domain.ErrNotFound)
	}
	if in.RetryMaxAttempts < 0 || in.RetryBackoffSeconds < 0 {
		return fmt.Errorf("retry_max_attempts and retry_backoff_seconds must be non-negative: %w", domain.ErrInvalid)
	}
	return nil
}

func (s *triggerBindingStore) ensureTriggerBindingRouteAvailableLocked(in store.TriggerBindingCreate) error {
	if in.RouteKey == "" {
		return nil
	}
	for _, binding := range s.items[in.WorkspaceKey] {
		if binding.RouteKey == in.RouteKey {
			return fmt.Errorf("trigger binding route %q in workspace %q: %w", in.RouteKey, in.WorkspaceKey, domain.ErrAlreadyExists)
		}
	}
	return nil
}

func newTriggerBindingMem(in store.TriggerBindingCreate) *domain.TriggerBinding {
	now := time.Now().UTC()
	return &domain.TriggerBinding{
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
		ConcurrencyPolicy:    defaultTriggerBindingConcurrencyMem(in.ConcurrencyPolicy),
		IdempotencyPolicy:    firstNonEmptyMem(in.IdempotencyPolicy, "header:Idempotency-Key"),
		AuthPolicy:           firstNonEmptyMem(in.AuthPolicy, "workspace_user"),
		WebhookSecret:        in.WebhookSecret,
		SubjectKeyTemplate:   in.SubjectKeyTemplate,
		ActorFilter:          normalizedActorFilterMem(in.ActorFilter),
		RetryMaxAttempts:     defaultRetryFieldMem(in.RetryMaxAttempts, domain.DefaultTriggerRetryMaxAttempts),
		RetryBackoffSeconds:  defaultRetryFieldMem(in.RetryBackoffSeconds, domain.DefaultTriggerRetryBackoffSeconds),
		Schedule:             in.Schedule,
		ScheduleTimezone:     in.ScheduleTimezone,
		Permissions:          append([]string(nil), in.Permissions...),
		Enabled:              in.Enabled,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func defaultTriggerBindingConcurrencyMem(policy domain.TriggerBindingConcurrencyPolicy) domain.TriggerBindingConcurrencyPolicy {
	if policy == "" {
		return domain.TriggerBindingConcurrencyOneActivePerEpic
	}
	return policy
}

func (s *triggerBindingStore) Get(_ context.Context, ws, bindingID string) (*domain.TriggerBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.items[ws][bindingID]
	if !ok {
		return nil, fmt.Errorf("trigger binding %q in workspace %q: %w", bindingID, ws, domain.ErrNotFound)
	}
	return redactedTriggerBinding(binding), nil
}

func (s *triggerBindingStore) GetByRouteKey(_ context.Context, ws, routeKey string) (*domain.TriggerBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, binding := range s.items[ws] {
		if binding.RouteKey == routeKey {
			return redactedTriggerBinding(binding), nil
		}
	}
	return nil, fmt.Errorf("trigger binding route %q in workspace %q: %w", routeKey, ws, domain.ErrNotFound)
}

// ResolveWebhookSecret returns the stored plaintext secret (never redacted),
// mirroring fleet-db's privileged webhook-secret endpoint.
func (s *triggerBindingStore) ResolveWebhookSecret(_ context.Context, ws, bindingID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.items[ws][bindingID]
	if !ok {
		return "", fmt.Errorf("trigger binding %q in workspace %q: %w", bindingID, ws, domain.ErrNotFound)
	}
	return binding.WebhookSecret, nil
}

func (s *triggerBindingStore) List(_ context.Context, ws string, filter store.TriggerBindingFilter) ([]*domain.TriggerBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.TriggerBinding, 0, len(s.items[ws]))
	for _, binding := range s.items[ws] {
		if triggerBindingMatchesMem(binding, filter) {
			out = append(out, redactedTriggerBinding(binding))
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
	return redactedTriggerBinding(updated), nil
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
