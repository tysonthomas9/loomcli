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

type agentServiceStore struct {
	mu       sync.RWMutex
	items    map[string]map[string]*domain.AgentService
	roles    *roleStore
	profiles *workerProfileStore
	bindings *triggerBindingStore
}

func newAgentServiceStore(roles *roleStore, profiles *workerProfileStore) *agentServiceStore {
	return &agentServiceStore{
		items:    make(map[string]map[string]*domain.AgentService),
		roles:    roles,
		profiles: profiles,
	}
}

var _ store.AgentServiceStore = (*agentServiceStore)(nil)

func (s *agentServiceStore) Create(_ context.Context, in store.AgentServiceCreate) (*domain.AgentService, error) {
	svc, err := newAgentServiceMem(in)
	if err != nil {
		return nil, err
	}
	if err := validateAgentServiceMem(svc); err != nil {
		return nil, err
	}
	if err := s.validateReferences(svc); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[svc.WorkspaceKey] == nil {
		s.items[svc.WorkspaceKey] = make(map[string]*domain.AgentService)
	}
	if _, ok := s.items[svc.WorkspaceKey][svc.ServiceID]; ok {
		return nil, fmt.Errorf("agent service %q in workspace %q: %w", svc.ServiceID, svc.WorkspaceKey, domain.ErrAlreadyExists)
	}
	s.items[svc.WorkspaceKey][svc.ServiceID] = svc
	return cloneAgentService(svc), nil
}

func newAgentServiceMem(in store.AgentServiceCreate) (*domain.AgentService, error) {
	serviceID := strings.TrimSpace(in.ServiceID)
	if in.WorkspaceKey == "" || serviceID == "" || strings.TrimSpace(in.RoleName) == "" {
		return nil, fmt.Errorf("workspace_key + service_id + role_name required: %w", domain.ErrInvalid)
	}
	now := time.Now().UTC()
	return &domain.AgentService{
		WorkspaceKey:    in.WorkspaceKey,
		ServiceID:       serviceID,
		Name:            firstNonEmptyMem(in.Name, serviceID),
		Kind:            in.Kind,
		DesiredState:    defaultAgentServiceDesiredStateMem(in.DesiredState),
		RoleName:        in.RoleName,
		ProfileName:     in.ProfileName,
		ScheduleID:      in.ScheduleID,
		EventSources:    cloneStringSlice(in.EventSources),
		TriggerRefs:     cloneStringSlice(in.TriggerRefs),
		PlacementPolicy: in.PlacementPolicy,
		MaxInstances:    defaultAgentServiceMaxInstancesMem(in.MaxInstances),
		LeaseID:         in.LeaseID,
		RestartPolicy:   in.RestartPolicy,
		Permissions:     cloneStringSlice(in.Permissions),
		BudgetPolicy:    in.BudgetPolicy,
		StateRef:        in.StateRef,
		Metadata:        cloneMap(in.Metadata),
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

func defaultAgentServiceDesiredStateMem(state domain.AgentServiceDesiredState) domain.AgentServiceDesiredState {
	if state == "" {
		return domain.AgentServiceDesiredStopped
	}
	return state
}

func defaultAgentServiceMaxInstancesMem(maxInstances int) int {
	if maxInstances == 0 {
		return 1
	}
	return maxInstances
}

func firstNonEmptyMem(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *agentServiceStore) Get(_ context.Context, ws, serviceID string) (*domain.AgentService, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svc, ok := s.items[ws][serviceID]
	if !ok {
		return nil, fmt.Errorf("agent service %q in workspace %q: %w", serviceID, ws, domain.ErrNotFound)
	}
	return cloneAgentService(svc), nil
}

func (s *agentServiceStore) List(_ context.Context, ws string, filter store.AgentServiceFilter) ([]*domain.AgentService, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.AgentService, 0, len(s.items[ws]))
	for _, svc := range s.items[ws] {
		if agentServiceMatchesMem(svc, filter) {
			out = append(out, cloneAgentService(svc))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *agentServiceStore) Update(_ context.Context, ws, serviceID string, patch store.AgentServiceUpdate) (*domain.AgentService, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.items[ws][serviceID]
	if !ok {
		return nil, fmt.Errorf("agent service %q in workspace %q: %w", serviceID, ws, domain.ErrNotFound)
	}
	updated := cloneAgentService(svc)
	applyAgentServiceUpdateMem(updated, patch)
	updated.ServiceID = strings.TrimSpace(updated.ServiceID)
	if strings.TrimSpace(updated.Name) == "" {
		updated.Name = updated.ServiceID
	}
	if updated.DesiredState == "" {
		updated.DesiredState = domain.AgentServiceDesiredStopped
	}
	if updated.MaxInstances == 0 {
		updated.MaxInstances = 1
	}
	updated.UpdatedAt = time.Now().UTC()
	if err := validateAgentServiceMem(updated); err != nil {
		return nil, err
	}
	if err := s.validateReferences(updated); err != nil {
		return nil, err
	}
	s.items[ws][serviceID] = updated
	return cloneAgentService(updated), nil
}

func (s *agentServiceStore) Delete(_ context.Context, ws, serviceID string) error {
	if s.bindings != nil && s.bindings.hasTargetAgentService(ws, serviceID) {
		return fmt.Errorf("agent service %q in workspace %q is targeted by trigger binding: %w", serviceID, ws, domain.ErrInvalidTransition)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[ws][serviceID]; !ok {
		return fmt.Errorf("agent service %q in workspace %q: %w", serviceID, ws, domain.ErrNotFound)
	}
	delete(s.items[ws], serviceID)
	return nil
}

func (s *agentServiceStore) exists(ws, serviceID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.items[ws][serviceID]
	return ok
}

func (s *agentServiceStore) triggerRefTargetCompatible(ws, bindingID, targetServiceID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	targetServiceID = strings.TrimSpace(targetServiceID)
	for _, svc := range s.items[ws] {
		for _, ref := range svc.TriggerRefs {
			if strings.TrimSpace(ref) != bindingID {
				continue
			}
			if targetServiceID != "" && targetServiceID != svc.ServiceID {
				return false
			}
		}
	}
	return true
}

func (s *agentServiceStore) hasRole(ws, roleName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, svc := range s.items[ws] {
		if svc.RoleName == roleName {
			return true
		}
	}
	return false
}

func (s *agentServiceStore) hasProfile(ws, profileName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, svc := range s.items[ws] {
		if svc.ProfileName == profileName {
			return true
		}
	}
	return false
}

func (s *agentServiceStore) validateReferences(svc *domain.AgentService) error {
	if s.roles != nil && !s.roles.exists(svc.WorkspaceKey, svc.RoleName) {
		return fmt.Errorf("agent service %q role %q in workspace %q: %w", svc.ServiceID, svc.RoleName, svc.WorkspaceKey, domain.ErrNotFound)
	}
	if svc.ProfileName != "" && s.profiles != nil && !s.profiles.exists(svc.WorkspaceKey, svc.ProfileName) {
		return fmt.Errorf("agent service %q profile %q in workspace %q: %w", svc.ServiceID, svc.ProfileName, svc.WorkspaceKey, domain.ErrNotFound)
	}
	seen := map[string]struct{}{}
	for _, ref := range svc.TriggerRefs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		binding, ok := s.bindings.getForValidation(svc.WorkspaceKey, ref)
		if s.bindings != nil && !ok {
			return fmt.Errorf("agent service %q trigger ref %q in workspace %q: %w", svc.ServiceID, ref, svc.WorkspaceKey, domain.ErrNotFound)
		}
		if binding != nil && binding.TargetAgentServiceID != "" && binding.TargetAgentServiceID != svc.ServiceID {
			return fmt.Errorf("agent service %q trigger ref %q targets %q: %w", svc.ServiceID, ref, binding.TargetAgentServiceID, domain.ErrInvalidTransition)
		}
	}
	return nil
}

func validateAgentServiceMem(svc *domain.AgentService) error {
	if svc.WorkspaceKey == "" || svc.ServiceID == "" || strings.TrimSpace(svc.RoleName) == "" {
		return fmt.Errorf("workspace_key + service_id + role_name required: %w", domain.ErrInvalid)
	}
	if !validAgentServiceKindMem(svc.Kind) {
		return fmt.Errorf("agent service %q kind %q invalid: %w", svc.ServiceID, svc.Kind, domain.ErrInvalid)
	}
	if !validAgentServiceDesiredStateMem(svc.DesiredState) {
		return fmt.Errorf("agent service %q desired_state %q invalid: %w", svc.ServiceID, svc.DesiredState, domain.ErrInvalid)
	}
	if svc.MaxInstances < 1 {
		return fmt.Errorf("agent service %q max_instances must be positive: %w", svc.ServiceID, domain.ErrInvalid)
	}
	if svc.CreatedAt.IsZero() || svc.UpdatedAt.IsZero() {
		return fmt.Errorf("agent service %q timestamps required: %w", svc.ServiceID, domain.ErrInvalid)
	}
	return nil
}

func validAgentServiceKindMem(kind domain.AgentServiceKind) bool {
	switch kind {
	case domain.AgentServiceKindLead, domain.AgentServiceKindSupport, domain.AgentServiceKindTriage,
		domain.AgentServiceKindOnCall, domain.AgentServiceKindScheduled, domain.AgentServiceKindMaintenance,
		domain.AgentServiceKindOrchestrator, domain.AgentServiceKindAlwaysOn, domain.AgentServiceKindCron,
		domain.AgentServiceKindEvent, domain.AgentServiceKindCampaignOrchestrator:
		return true
	default:
		return false
	}
}

func validAgentServiceDesiredStateMem(state domain.AgentServiceDesiredState) bool {
	switch state {
	case domain.AgentServiceDesiredRunning, domain.AgentServiceDesiredStopped, domain.AgentServiceDesiredPaused:
		return true
	default:
		return false
	}
}

func cloneAgentService(svc *domain.AgentService) *domain.AgentService {
	if svc == nil {
		return nil
	}
	out := *svc
	out.EventSources = cloneStringSlice(svc.EventSources)
	out.TriggerRefs = cloneStringSlice(svc.TriggerRefs)
	out.Permissions = cloneStringSlice(svc.Permissions)
	out.Metadata = cloneMap(svc.Metadata)
	return &out
}

func agentServiceMatchesMem(svc *domain.AgentService, filter store.AgentServiceFilter) bool {
	return (filter.Kind == "" || svc.Kind == filter.Kind) &&
		(filter.DesiredState == "" || svc.DesiredState == filter.DesiredState) &&
		(filter.RoleName == "" || svc.RoleName == filter.RoleName) &&
		(filter.ProfileName == "" || svc.ProfileName == filter.ProfileName)
}

func applyAgentServiceUpdateMem(svc *domain.AgentService, patch store.AgentServiceUpdate) {
	if patch.Name != nil {
		svc.Name = *patch.Name
	}
	if patch.Kind != nil {
		svc.Kind = *patch.Kind
	}
	if patch.DesiredState != nil {
		svc.DesiredState = *patch.DesiredState
	}
	if patch.RoleName != nil {
		svc.RoleName = *patch.RoleName
	}
	if patch.ProfileName != nil {
		svc.ProfileName = *patch.ProfileName
	}
	if patch.ScheduleID != nil {
		svc.ScheduleID = *patch.ScheduleID
	}
	if patch.EventSources != nil {
		svc.EventSources = cloneStringSlice(*patch.EventSources)
	}
	if patch.TriggerRefs != nil {
		svc.TriggerRefs = cloneStringSlice(*patch.TriggerRefs)
	}
	if patch.PlacementPolicy != nil {
		svc.PlacementPolicy = *patch.PlacementPolicy
	}
	if patch.MaxInstances != nil {
		svc.MaxInstances = *patch.MaxInstances
	}
	if patch.LeaseID != nil {
		svc.LeaseID = *patch.LeaseID
	}
	if patch.RestartPolicy != nil {
		svc.RestartPolicy = *patch.RestartPolicy
	}
	if patch.Permissions != nil {
		svc.Permissions = cloneStringSlice(*patch.Permissions)
	}
	if patch.BudgetPolicy != nil {
		svc.BudgetPolicy = *patch.BudgetPolicy
	}
	if patch.StateRef != nil {
		svc.StateRef = *patch.StateRef
	}
	if patch.Metadata != nil {
		svc.Metadata = cloneMap(*patch.Metadata)
	}
}
