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
	binding := newTriggerBindingFromCreateMem(in, time.Now().UTC())
	s.items[in.WorkspaceKey][in.BindingID] = binding
	return cloneTriggerBinding(binding), nil
}

func newTriggerBindingFromCreateMem(in store.TriggerBindingCreate, now time.Time) *domain.TriggerBinding {
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
		ConcurrencyPolicy:    policy,
		IdempotencyPolicy:    idempotencyPolicy,
		AuthPolicy:           authPolicy,
		Permissions:          append([]string(nil), in.Permissions...),
		Enabled:              in.Enabled,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
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

func cloneTriggerBinding(b *domain.TriggerBinding) *domain.TriggerBinding {
	out := *b
	out.EventTypePatterns = append([]string(nil), b.EventTypePatterns...)
	out.Permissions = append([]string(nil), b.Permissions...)
	return &out
}

func triggerBindingMatchesMem(b *domain.TriggerBinding, f store.TriggerBindingFilter) bool {
	return (f.SourceKind == "" || b.SourceKind == f.SourceKind) &&
		(f.RouteKey == "" || b.RouteKey == f.RouteKey) &&
		(f.DriverID == "" || b.DriverID == f.DriverID) &&
		(f.TargetAgentServiceID == "" || b.TargetAgentServiceID == f.TargetAgentServiceID) &&
		(f.Enabled == nil || b.Enabled == *f.Enabled)
}

func applyTriggerBindingUpdateMem(b *domain.TriggerBinding, patch store.TriggerBindingUpdate) {
	applyTriggerBindingSourceUpdateMem(b, patch)
	applyTriggerBindingTargetUpdateMem(b, patch)
}

func applyTriggerBindingSourceUpdateMem(b *domain.TriggerBinding, patch store.TriggerBindingUpdate) {
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
}

func applyTriggerBindingTargetUpdateMem(b *domain.TriggerBinding, patch store.TriggerBindingUpdate) {
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
