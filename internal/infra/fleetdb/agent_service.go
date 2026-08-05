package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type agentServiceStore struct{ client *Client }

var _ store.AgentServiceStore = (*agentServiceStore)(nil)

func (s *agentServiceStore) Create(ctx context.Context, in store.AgentServiceCreate) (*domain.AgentService, error) {
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
	current, err := s.Get(ctx, ws, serviceID)
	if err != nil {
		return err
	}
	_, err = s.client.agentManagement.ArchiveAgentService(ctx, AgentServiceArchiveInput{
		WorkspaceKey:      ws,
		ServiceID:         serviceID,
		ExpectedUpdatedAt: current.UpdatedAt,
		DelegatedActor:    s.client.delegatedActor(),
	})
	return err
}

type agentOwnershipLeaseStore struct {
	client     *Client
	management AgentManagementTransport
}

var _ store.AgentOwnershipLeaseStore = (*agentOwnershipLeaseStore)(nil)
var _ store.AgentOwnershipLeaseOwnedStore = (*agentOwnershipLeaseStore)(nil)

func (s *agentOwnershipLeaseStore) Acquire(ctx context.Context, in store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	grant, err := s.management.AcquireAgentOwnership(ctx, AgentOwnershipAcquireInput{
		WorkspaceKey: in.WorkspaceKey, AgentID: in.AgentID, LeaseID: in.LeaseID,
		OwnerID: in.OwnerID, RuntimeProvider: in.RuntimeProvider, NodeID: in.NodeID,
		TTLSeconds: ttlSeconds(in.TTL), DelegatedActor: in.OwnerID,
	})
	if err != nil {
		return nil, err
	}
	if grant == nil || grant.Lease == nil {
		return nil, errors.New("fleetdb: agent ownership lease acquire response omitted lease")
	}
	if err := validateAgentOwnershipLeaseEnvelope(*grant.Lease, grant.Token, in); err != nil {
		return nil, err
	}
	grant.Lease.Token = grant.Token
	return grant.Lease, nil
}

func validateAgentOwnershipLeaseEnvelope(lease domain.AgentOwnershipLease, token string, in store.AgentOwnershipLeaseAcquire) error {
	switch {
	case token == "":
		return errors.New("fleetdb: agent ownership lease acquire response omitted one-time token")
	case lease.WorkspaceKey != in.WorkspaceKey:
		return fmt.Errorf("fleetdb: agent ownership lease acquire response workspace %q does not match %q", lease.WorkspaceKey, in.WorkspaceKey)
	case lease.AgentID != in.AgentID:
		return fmt.Errorf("fleetdb: agent ownership lease acquire response agent %q does not match %q", lease.AgentID, in.AgentID)
	case lease.LeaseID == "":
		return errors.New("fleetdb: agent ownership lease acquire response omitted lease id")
	case in.LeaseID != "" && lease.LeaseID != in.LeaseID:
		return fmt.Errorf("fleetdb: agent ownership lease acquire response lease %q does not match %q", lease.LeaseID, in.LeaseID)
	case lease.OwnerID != in.OwnerID:
		return fmt.Errorf("fleetdb: agent ownership lease acquire response owner %q does not match %q", lease.OwnerID, in.OwnerID)
	case lease.RuntimeProvider != in.RuntimeProvider:
		return fmt.Errorf("fleetdb: agent ownership lease acquire response runtime provider %q does not match %q", lease.RuntimeProvider, in.RuntimeProvider)
	case lease.NodeID != in.NodeID:
		return fmt.Errorf("fleetdb: agent ownership lease acquire response node %q does not match %q", lease.NodeID, in.NodeID)
	case lease.FencingToken <= 0:
		return fmt.Errorf("fleetdb: agent ownership lease acquire response has invalid fencing token %d", lease.FencingToken)
	}
	return nil
}

func (s *agentOwnershipLeaseStore) Get(ctx context.Context, ws, agentID string) (*domain.AgentOwnershipLease, error) {
	var out domain.AgentOwnershipLease
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agent-ownership-leases/"+pathEscape(agentID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentOwnershipLeaseStore) List(ctx context.Context, ws string, filter store.AgentOwnershipLeaseFilter) ([]*domain.AgentOwnershipLease, error) {
	q := url.Values{}
	if filter.OwnerID != "" {
		q.Set("owner_id", filter.OwnerID)
	}
	if filter.NodeID != "" {
		q.Set("node_id", filter.NodeID)
	}
	if filter.RuntimeProvider != "" {
		q.Set("runtime_provider", string(filter.RuntimeProvider))
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/agent-ownership-leases", q)
	var resp struct {
		AgentOwnershipLeases []*domain.AgentOwnershipLease `json:"agent_ownership_leases"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.AgentOwnershipLeases == nil {
		resp.AgentOwnershipLeases = []*domain.AgentOwnershipLease{}
	}
	return resp.AgentOwnershipLeases, nil
}

func (s *agentOwnershipLeaseStore) Heartbeat(ctx context.Context, ws, agentID, token string, ttl time.Duration) (*domain.AgentOwnershipLease, error) {
	path := "/api/v1/" + pathEscape(ws) + "/agent-ownership-leases/" + pathEscape(agentID) + "/heartbeat"
	if seconds := ttlSeconds(ttl); seconds > 0 {
		path += "?ttl_seconds=" + strconv.Itoa(seconds)
	}
	var out domain.AgentOwnershipLease
	if err := s.client.doWithHeaders(ctx, "POST", path, nil, &out, map[string]string{"X-Agent-Ownership-Lease-Token": token}); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentOwnershipLeaseStore) HeartbeatOwned(
	ctx context.Context,
	proof store.AgentOwnershipLeaseProof,
	ttl time.Duration,
) (*domain.AgentOwnershipLease, error) {
	return s.management.RenewAgentOwnership(ctx, AgentOwnershipRenewInput{
		Proof:          ownershipProofInput(proof),
		TTLSeconds:     ttlSeconds(ttl),
		DelegatedActor: proof.OwnerID,
	})
}

func (s *agentOwnershipLeaseStore) Release(ctx context.Context, ws, agentID, token string) (*domain.AgentOwnershipLease, error) {
	var out domain.AgentOwnershipLease
	if err := s.client.doWithHeaders(ctx, "POST", "/api/v1/"+pathEscape(ws)+"/agent-ownership-leases/"+pathEscape(agentID)+"/release", nil, &out, map[string]string{"X-Agent-Ownership-Lease-Token": token}); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *agentOwnershipLeaseStore) ReleaseOwned(
	ctx context.Context,
	proof store.AgentOwnershipLeaseProof,
) (*domain.AgentOwnershipLease, error) {
	return s.management.ReleaseAgentOwnership(ctx, AgentOwnershipReleaseInput{
		Proof:          ownershipProofInput(proof),
		DelegatedActor: proof.OwnerID,
	})
}

func ownershipProofInput(proof store.AgentOwnershipLeaseProof) AgentOwnershipProof {
	return AgentOwnershipProof{
		WorkspaceKey: proof.WorkspaceKey, AgentID: proof.AgentID, LeaseID: proof.LeaseID,
		LeaseToken: proof.LeaseToken, OwnerID: proof.OwnerID,
		RuntimeProvider: proof.RuntimeProvider, NodeID: proof.NodeID,
		FencingToken: proof.FencingToken,
	}
}
