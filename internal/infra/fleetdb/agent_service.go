package fleetdb

import (
	"context"
	"net/url"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type agentServiceStore struct{ client *Client }

var _ store.AgentServiceStore = (*agentServiceStore)(nil)

func (s *agentServiceStore) Create(ctx context.Context, in store.AgentServiceCreate) (*domain.AgentService, error) {
	// FleetDB owns CreatedBy and stamps it from the authenticated request actor;
	// including created_by here would be rejected by its strict create schema.
	body := struct {
		ServiceID       string                          `json:"service_id"`
		Name            string                          `json:"name,omitempty"`
		Kind            domain.AgentServiceKind         `json:"kind"`
		DesiredState    domain.AgentServiceDesiredState `json:"desired_state,omitempty"`
		RoleName        string                          `json:"role_name,omitempty"`
		DriverID        string                          `json:"driver_id,omitempty"`
		DriverVersionID string                          `json:"driver_version_id,omitempty"`
		ProfileName     string                          `json:"profile_name,omitempty"`
		ScheduleID      string                          `json:"schedule_id,omitempty"`
		EventSources    []string                        `json:"event_sources,omitempty"`
		TriggerRefs     []string                        `json:"trigger_refs,omitempty"`
		PlacementPolicy string                          `json:"placement_policy,omitempty"`
		MaxInstances    int                             `json:"max_instances,omitempty"`
		LeaseID         string                          `json:"lease_id,omitempty"`
		RestartPolicy   string                          `json:"restart_policy,omitempty"`
		Permissions     []string                        `json:"permissions,omitempty"`
		BudgetPolicy    string                          `json:"budget_policy,omitempty"`
		StateRef        string                          `json:"state_ref,omitempty"`
		Metadata        map[string]string               `json:"metadata,omitempty"`
	}{
		ServiceID:       in.ServiceID,
		Name:            in.Name,
		Kind:            in.Kind,
		DesiredState:    in.DesiredState,
		RoleName:        in.RoleName,
		DriverID:        in.DriverID,
		DriverVersionID: in.DriverVersionID,
		ProfileName:     in.ProfileName,
		ScheduleID:      in.ScheduleID,
		EventSources:    in.EventSources,
		TriggerRefs:     in.TriggerRefs,
		PlacementPolicy: in.PlacementPolicy,
		MaxInstances:    in.MaxInstances,
		LeaseID:         in.LeaseID,
		RestartPolicy:   in.RestartPolicy,
		Permissions:     in.Permissions,
		BudgetPolicy:    in.BudgetPolicy,
		StateRef:        in.StateRef,
		Metadata:        in.Metadata,
	}
	var out domain.AgentService
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agent-services", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentServiceStore) Get(ctx context.Context, ws, serviceID string) (*domain.AgentService, error) {
	var out domain.AgentService
	path := "/api/v1/" + pathEscape(ws) + "/agent-services/" + pathEscape(serviceID)
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentServiceStore) List(ctx context.Context, ws string, filter store.AgentServiceFilter) ([]*domain.AgentService, error) {
	q := url.Values{}
	if filter.Kind != "" {
		q.Set("kind", string(filter.Kind))
	}
	if filter.DesiredState != "" {
		q.Set("desired_state", string(filter.DesiredState))
	}
	if filter.RoleName != "" {
		q.Set("role_name", filter.RoleName)
	}
	if filter.ProfileName != "" {
		q.Set("profile_name", filter.ProfileName)
	}
	if filter.IncludeDeleted {
		q.Set("include_deleted", "true")
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/agent-services", q)
	var resp struct {
		AgentServices []*domain.AgentService `json:"agent_services"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.AgentServices == nil {
		resp.AgentServices = []*domain.AgentService{}
	}
	return resp.AgentServices, nil
}

func (s *agentServiceStore) Update(ctx context.Context, ws, serviceID string, patch store.AgentServiceUpdate) (*domain.AgentService, error) {
	body := struct {
		Name            *string                          `json:"name,omitempty"`
		Kind            *domain.AgentServiceKind         `json:"kind,omitempty"`
		DesiredState    *domain.AgentServiceDesiredState `json:"desired_state,omitempty"`
		RoleName        *string                          `json:"role_name,omitempty"`
		DriverID        *string                          `json:"driver_id,omitempty"`
		DriverVersionID *string                          `json:"driver_version_id,omitempty"`
		ProfileName     *string                          `json:"profile_name,omitempty"`
		ScheduleID      *string                          `json:"schedule_id,omitempty"`
		EventSources    *[]string                        `json:"event_sources,omitempty"`
		TriggerRefs     *[]string                        `json:"trigger_refs,omitempty"`
		PlacementPolicy *string                          `json:"placement_policy,omitempty"`
		MaxInstances    *int                             `json:"max_instances,omitempty"`
		LeaseID         *string                          `json:"lease_id,omitempty"`
		RestartPolicy   *string                          `json:"restart_policy,omitempty"`
		Permissions     *[]string                        `json:"permissions,omitempty"`
		BudgetPolicy    *string                          `json:"budget_policy,omitempty"`
		StateRef        *string                          `json:"state_ref,omitempty"`
		Metadata        *map[string]string               `json:"metadata,omitempty"`
	}{
		Name:            patch.Name,
		Kind:            patch.Kind,
		DesiredState:    patch.DesiredState,
		RoleName:        patch.RoleName,
		DriverID:        patch.DriverID,
		DriverVersionID: patch.DriverVersionID,
		ProfileName:     patch.ProfileName,
		ScheduleID:      patch.ScheduleID,
		EventSources:    patch.EventSources,
		TriggerRefs:     patch.TriggerRefs,
		PlacementPolicy: patch.PlacementPolicy,
		MaxInstances:    patch.MaxInstances,
		LeaseID:         patch.LeaseID,
		RestartPolicy:   patch.RestartPolicy,
		Permissions:     patch.Permissions,
		BudgetPolicy:    patch.BudgetPolicy,
		StateRef:        patch.StateRef,
		Metadata:        patch.Metadata,
	}
	var out domain.AgentService
	path := "/api/v1/" + pathEscape(ws) + "/agent-services/" + pathEscape(serviceID)
	if err := s.client.do(ctx, "PATCH", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentServiceStore) Delete(ctx context.Context, ws, serviceID string) error {
	path := "/api/v1/" + pathEscape(ws) + "/agent-services/" + pathEscape(serviceID)
	return s.client.do(ctx, "DELETE", path, nil, nil)
}
