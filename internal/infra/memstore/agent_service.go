package memstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type agentServiceStore struct {
	mu       sync.RWMutex
	items    map[string]map[string]*agents.AgentServiceRecord
	roles    *roleStore
	profiles *workerProfileStore
	drivers  *driverStore
	versions *driverVersionStore
	bindings *triggerBindingStore
}

func newAgentServiceStore(
	roles *roleStore,
	profiles *workerProfileStore,
	drivers *driverStore,
	versions *driverVersionStore,
) *agentServiceStore {
	return &agentServiceStore{
		items:    make(map[string]map[string]*agents.AgentServiceRecord),
		roles:    roles,
		profiles: profiles,
		drivers:  drivers,
		versions: versions,
	}
}

var _ agents.AgentServiceStore = (*agentServiceStore)(nil)

func (s *agentServiceStore) Create(_ context.Context, in agents.AgentServiceCreate) (*agents.AgentServiceRecord, error) {
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
		s.items[svc.WorkspaceKey] = make(map[string]*agents.AgentServiceRecord)
	}
	if _, ok := s.items[svc.WorkspaceKey][svc.ServiceID]; ok {
		return nil, fmt.Errorf("agent service %q in workspace %q: %w", svc.ServiceID, svc.WorkspaceKey, persistence.ErrAlreadyExists)
	}
	s.items[svc.WorkspaceKey][svc.ServiceID] = svc
	return cloneAgentService(svc), nil
}

func newAgentServiceMem(in agents.AgentServiceCreate) (*agents.AgentServiceRecord, error) {
	serviceID := strings.TrimSpace(in.ServiceID)
	if in.WorkspaceKey == "" || serviceID == "" {
		return nil, fmt.Errorf("workspace_key + service_id required: %w", persistence.ErrInvalid)
	}
	now := time.Now().UTC()
	return &agents.AgentServiceRecord{
		WorkspaceKey:    in.WorkspaceKey,
		ServiceID:       serviceID,
		Name:            firstNonEmptyMem(in.Name, serviceID),
		Kind:            in.Kind,
		DesiredState:    defaultAgentServiceDesiredStateMem(in.DesiredState),
		RoleName:        in.RoleName,
		DriverID:        in.DriverID,
		DriverVersionID: in.DriverVersionID,
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

func defaultAgentServiceDesiredStateMem(state agents.DesiredState) agents.DesiredState {
	if state == "" {
		return agents.DesiredStopped
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

func (s *agentServiceStore) Get(_ context.Context, ws, serviceID string) (*agents.AgentServiceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svc, ok := s.items[ws][serviceID]
	if !ok {
		return nil, fmt.Errorf("agent service %q in workspace %q: %w", serviceID, ws, persistence.ErrNotFound)
	}
	return cloneAgentService(svc), nil
}

func (s *agentServiceStore) List(_ context.Context, ws string, filter agents.AgentServiceFilter) ([]*agents.AgentServiceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*agents.AgentServiceRecord, 0, len(s.items[ws]))
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

func (s *agentServiceStore) Update(_ context.Context, ws, serviceID string, patch agents.AgentServiceUpdate) (*agents.AgentServiceRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.items[ws][serviceID]
	if !ok {
		return nil, fmt.Errorf("agent service %q in workspace %q: %w", serviceID, ws, persistence.ErrNotFound)
	}
	updated := cloneAgentService(svc)
	applyAgentServiceUpdateMem(updated, patch)
	updated.ServiceID = strings.TrimSpace(updated.ServiceID)
	if strings.TrimSpace(updated.Name) == "" {
		updated.Name = updated.ServiceID
	}
	if updated.DesiredState == "" {
		updated.DesiredState = agents.DesiredStopped
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
		return fmt.Errorf("agent service %q in workspace %q is targeted by trigger binding: %w", serviceID, ws, persistence.ErrInvalidTransition)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.items[ws][serviceID]
	if !ok {
		return fmt.Errorf("agent service %q in workspace %q: %w", serviceID, ws, persistence.ErrNotFound)
	}
	// Wave B semantics (mirrors fleet-db): DELETE archives, never erases — the
	// record stays GET-able for run attribution; List hides it by default.
	now := time.Now().UTC()
	svc.DeletedAt = &now
	svc.UpdatedAt = now
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
		// FleetDB's reference check uses the default agent-service listing,
		// which excludes archived records. Mirror that behavior so a failed
		// create can archive its provisional service and then compensate a
		// role that never became runnable.
		if svc.DeletedAt == nil && svc.RoleName == roleName {
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

func (s *agentServiceStore) validateReferences(svc *agents.AgentServiceRecord) error {
	if err := s.validateBehaviorReferences(svc); err != nil {
		return err
	}
	if svc.ProfileName != "" && s.profiles != nil && !s.profiles.exists(svc.WorkspaceKey, svc.ProfileName) {
		return fmt.Errorf("agent service %q profile %q in workspace %q: %w", svc.ServiceID, svc.ProfileName, svc.WorkspaceKey, persistence.ErrNotFound)
	}
	return s.validateTriggerReferences(svc)
}

func (s *agentServiceStore) validateBehaviorReferences(svc *agents.AgentServiceRecord) error {
	if svc.RoleName != "" && s.roles != nil && !s.roles.exists(svc.WorkspaceKey, svc.RoleName) {
		return fmt.Errorf("agent service %q role %q in workspace %q: %w", svc.ServiceID, svc.RoleName, svc.WorkspaceKey, persistence.ErrNotFound)
	}
	if svc.DriverID != "" && s.drivers != nil && !s.drivers.exists(svc.WorkspaceKey, svc.DriverID) {
		return fmt.Errorf("agent service %q driver %q in workspace %q: %w", svc.ServiceID, svc.DriverID, svc.WorkspaceKey, persistence.ErrNotFound)
	}
	if svc.DriverVersionID != "" && s.versions != nil && !s.versions.belongsToDriver(svc.WorkspaceKey, svc.DriverVersionID, svc.DriverID) {
		return fmt.Errorf("agent service %q driver version %q does not belong to driver %q in workspace %q: %w", svc.ServiceID, svc.DriverVersionID, svc.DriverID, svc.WorkspaceKey, persistence.ErrNotFound)
	}
	return nil
}

func (s *agentServiceStore) validateTriggerReferences(svc *agents.AgentServiceRecord) error {
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
			return fmt.Errorf("agent service %q trigger ref %q in workspace %q: %w", svc.ServiceID, ref, svc.WorkspaceKey, persistence.ErrNotFound)
		}
		if binding != nil && binding.TargetAgentServiceID != "" && binding.TargetAgentServiceID != svc.ServiceID {
			return fmt.Errorf("agent service %q trigger ref %q targets %q: %w", svc.ServiceID, ref, binding.TargetAgentServiceID, persistence.ErrInvalidTransition)
		}
	}
	return nil
}

func validateAgentServiceMem(svc *agents.AgentServiceRecord) error {
	if svc.WorkspaceKey == "" || svc.ServiceID == "" {
		return fmt.Errorf("workspace_key + service_id required: %w", persistence.ErrInvalid)
	}
	hasRole := strings.TrimSpace(svc.RoleName) != ""
	hasDriver := strings.TrimSpace(svc.DriverID) != "" || strings.TrimSpace(svc.DriverVersionID) != ""
	if hasRole == hasDriver {
		return fmt.Errorf("agent service %q requires exactly one behavior reference (role_name or driver_id + driver_version_id): %w", svc.ServiceID, persistence.ErrInvalid)
	}
	if hasDriver && (strings.TrimSpace(svc.DriverID) == "" || strings.TrimSpace(svc.DriverVersionID) == "") {
		return fmt.Errorf("agent service %q driver_id + driver_version_id required together: %w", svc.ServiceID, persistence.ErrInvalid)
	}
	if !validAgentServiceKindMem(svc.Kind) {
		return fmt.Errorf("agent service %q kind %q invalid: %w", svc.ServiceID, svc.Kind, persistence.ErrInvalid)
	}
	if !validAgentServiceDesiredStateMem(svc.DesiredState) {
		return fmt.Errorf("agent service %q desired_state %q invalid: %w", svc.ServiceID, svc.DesiredState, persistence.ErrInvalid)
	}
	if svc.MaxInstances < 1 {
		return fmt.Errorf("agent service %q max_instances must be positive: %w", svc.ServiceID, persistence.ErrInvalid)
	}
	if svc.CreatedAt.IsZero() || svc.UpdatedAt.IsZero() {
		return fmt.Errorf("agent service %q timestamps required: %w", svc.ServiceID, persistence.ErrInvalid)
	}
	return nil
}

func validAgentServiceKindMem(kind agents.AgentKind) bool {
	switch kind {
	case agents.AgentKindLead, agents.AgentKindSupport, agents.AgentKindTriage,
		agents.AgentKindOnCall, agents.AgentKindScheduled, agents.AgentKindMaintenance,
		agents.AgentKindOrchestrator, agents.AgentKindAlwaysOn, agents.AgentKindCron,
		agents.AgentKindEvent, agents.AgentKindCampaignOrchestrator:
		return true
	default:
		return false
	}
}

func validAgentServiceDesiredStateMem(state agents.DesiredState) bool {
	switch state {
	case agents.DesiredRunning, agents.DesiredStopped, agents.DesiredPaused:
		return true
	default:
		return false
	}
}

func cloneAgentService(svc *agents.AgentServiceRecord) *agents.AgentServiceRecord {
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

func agentServiceMatchesMem(svc *agents.AgentServiceRecord, filter agents.AgentServiceFilter) bool {
	return (filter.IncludeDeleted || svc.DeletedAt == nil) &&
		(filter.Kind == "" || svc.Kind == filter.Kind) &&
		(filter.DesiredState == "" || svc.DesiredState == filter.DesiredState) &&
		(filter.RoleName == "" || svc.RoleName == filter.RoleName) &&
		(filter.ProfileName == "" || svc.ProfileName == filter.ProfileName)
}

func applyAgentServiceUpdateMem(svc *agents.AgentServiceRecord, patch agents.AgentServiceUpdate) {
	applyAgentServiceCoreUpdateMem(svc, patch)
	applyAgentServiceExecutionUpdateMem(svc, patch)
	applyAgentServicePolicyUpdateMem(svc, patch)
}

func applyAgentServiceCoreUpdateMem(svc *agents.AgentServiceRecord, patch agents.AgentServiceUpdate) {
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
	if patch.DriverID != nil {
		svc.DriverID = *patch.DriverID
	}
	if patch.DriverVersionID != nil {
		svc.DriverVersionID = *patch.DriverVersionID
	}
	if patch.ProfileName != nil {
		svc.ProfileName = *patch.ProfileName
	}
}

func applyAgentServiceExecutionUpdateMem(svc *agents.AgentServiceRecord, patch agents.AgentServiceUpdate) {
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
}

func applyAgentServicePolicyUpdateMem(svc *agents.AgentServiceRecord, patch agents.AgentServiceUpdate) {
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
