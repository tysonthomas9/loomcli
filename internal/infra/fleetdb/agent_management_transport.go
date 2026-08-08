package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	ErrAgentServiceRevisionConflict         = errors.New("fleetdb: agent service revision conflict")
	ErrAgentServiceDesiredStateConflict     = errors.New("fleetdb: agent service desired-state conflict")
	ErrAgentServiceIdempotencyConflict      = errors.New("fleetdb: agent service idempotency conflict")
	ErrAgentServiceUnsupportedIdentityPatch = errors.New("fleetdb: unsupported agent service identity patch")
	ErrAgentRoleRevisionConflict            = errors.New("fleetdb: agent role revision conflict")
	ErrAgentManagementInvalidDelegatedActor = errFleetInvalidDelegatedActor
)

// agentManagementRequester is the transport's complete dependency surface.
// Role reads and creates preserve the process-wide client's existing wire
// behavior; CAS role mutations continue to use the exact atomic commands.
type agentManagementRequester interface {
	fleetRequester
	GetRole(context.Context, string, string) (*domain.Role, error)
	ListRoles(context.Context, string) ([]*domain.Role, error)
	CreateRole(context.Context, store.RoleCreate) (*domain.Role, error)
}

// AgentManagementTransport is the low-level Phase 5 transport consumed only
// through the Agents capability adapter. Each mutation is one FleetDB command;
// full ownership proofs are never decomposed into read-then-write sequences.
type AgentManagementTransport interface {
	GetAgentService(context.Context, string, string) (*domain.AgentService, error)
	ListAgentServices(context.Context, string, AgentServiceQuery) ([]*domain.AgentService, error)
	GetAgentRole(context.Context, string, string) (*domain.Role, error)
	ListAgentRoles(context.Context, string) ([]*domain.Role, error)
	CreateAgentRole(context.Context, string, AgentRoleInput) (*domain.Role, error)
	UpdateAgentRole(context.Context, AgentRoleUpdateInput) (*domain.Role, error)
	DeleteAgentRole(context.Context, AgentRoleDeleteInput) error
	CreateAgentService(context.Context, AgentServiceCreateInput) (*domain.AgentService, error)
	UpdateAgentServiceIdentity(context.Context, AgentServiceUpdateInput) (*domain.AgentService, error)
	ArchiveAgentService(context.Context, AgentServiceArchiveInput) (*domain.AgentService, error)
	SetAgentServiceDesiredState(context.Context, AgentServiceDesiredStateInput) (*domain.AgentService, error)
	SetAgentServiceDesiredStateOwned(context.Context, AgentServiceOwnedDesiredStateInput) (*domain.AgentService, error)
	ApplyAgentServiceLifecycle(context.Context, AgentServiceLifecycleInput) (*AgentServiceLifecycleResult, error)
	AcquireAgentOwnership(context.Context, AgentOwnershipAcquireInput) (*AgentOwnershipGrant, error)
	GetAgentOwnership(context.Context, string, string) (*domain.AgentOwnershipLease, error)
	ListAgentOwnership(context.Context, string, AgentOwnershipQuery) ([]*domain.AgentOwnershipLease, error)
	RenewAgentOwnership(context.Context, AgentOwnershipRenewInput) (*domain.AgentOwnershipLease, error)
	ReleaseAgentOwnership(context.Context, AgentOwnershipReleaseInput) (*domain.AgentOwnershipLease, error)
}

type AgentServiceQuery struct {
	Kind           domain.AgentServiceKind
	DesiredState   domain.AgentServiceDesiredState
	RoleName       string
	IncludeDeleted bool
	Limit          int
}

type AgentServiceLifecycleInput struct {
	WorkspaceKey         string
	ServiceID            string
	Action               string
	ExpectedUpdatedAt    time.Time
	ExpectedGenerationID string
	IdempotencyKey       string
	DelegatedActor       string
}

type AgentServiceLifecycleResult struct {
	WorkspaceKey   string               `json:"workspace_key"`
	ServiceID      string               `json:"service_id"`
	IdempotencyKey string               `json:"idempotency_key"`
	Action         string               `json:"action"`
	Agent          *domain.AgentService `json:"agent"`
	BindingIDs     []string             `json:"binding_ids,omitempty"`
	GrantIDs       []string             `json:"grant_ids,omitempty"`
	CommittedAt    time.Time            `json:"committed_at"`
}

type AgentRoleInput struct {
	Name           string
	Kind           string
	Description    string
	Prompt         string
	PromptFile     string
	Model          string
	TaskFilter     string
	Backend        string
	Effort         string
	PathPatterns   []string
	Skills         []string
	MaxPriority    *int
	MaxConcurrency *int
	ReadOnly       bool
	AllowedTools   []string
	DeniedTools    []string
	MaxBudgetUSD   *float64
}

type AgentRolePatch struct {
	Kind           *string
	Description    *string
	Prompt         *string
	PromptFile     *string
	Model          *string
	TaskFilter     *string
	Backend        *string
	Effort         *string
	PathPatterns   *[]string
	Skills         *[]string
	MaxPriority    **int
	MaxConcurrency **int
	ReadOnly       *bool
	AllowedTools   *[]string
	DeniedTools    *[]string
	MaxBudgetUSD   **float64
}

type AgentRoleUpdateInput struct {
	WorkspaceKey      string
	RoleName          string
	ExpectedUpdatedAt time.Time
	Patch             AgentRolePatch
	DelegatedActor    string
}

type AgentRoleDeleteInput struct {
	WorkspaceKey      string
	RoleName          string
	ExpectedUpdatedAt time.Time
	DelegatedActor    string
}

type AgentServiceCreateInput struct {
	WorkspaceKey    string
	ServiceID       string
	Name            string
	Kind            domain.AgentServiceKind
	DesiredState    domain.AgentServiceDesiredState
	RoleName        string
	DriverID        string
	DriverVersionID string
	ProfileName     string
	ScheduleID      string
	EventSources    []string
	TriggerRefs     []string
	PlacementPolicy string
	MaxInstances    int
	LeaseID         string
	RestartPolicy   string
	Permissions     []string
	BudgetPolicy    string
	StateRef        string
	Metadata        map[string]string
	DelegatedActor  string
}

type AgentServiceIdentityPatch struct {
	Name            *string
	Kind            *domain.AgentServiceKind
	RoleName        *string
	DriverID        *string
	DriverVersionID *string
	ProfileName     *string
	ScheduleID      *string
	EventSources    *[]string
	TriggerRefs     *[]string
	PlacementPolicy *string
	MaxInstances    *int
	LeaseID         *string
	RestartPolicy   *string
	Permissions     *[]string
	BudgetPolicy    *string
	StateRef        *string
	Metadata        *map[string]string
}

type AgentServiceUpdateInput struct {
	WorkspaceKey      string
	ServiceID         string
	ExpectedUpdatedAt time.Time
	Patch             AgentServiceIdentityPatch
	DelegatedActor    string
}

type AgentServiceArchiveInput struct {
	WorkspaceKey      string
	ServiceID         string
	ExpectedUpdatedAt time.Time
	DelegatedActor    string
}

type AgentServiceDesiredStateInput struct {
	WorkspaceKey      string
	ServiceID         string
	ExpectedState     domain.AgentServiceDesiredState
	DesiredState      domain.AgentServiceDesiredState
	ExpectedUpdatedAt time.Time
	DelegatedActor    string
}

type AgentOwnershipProof struct {
	WorkspaceKey    string
	AgentID         string
	LeaseID         string
	LeaseToken      string
	OwnerID         string
	RuntimeProvider domain.RuntimeProvider
	NodeID          string
	FencingToken    int64
}

type AgentServiceOwnedDesiredStateInput struct {
	Proof             AgentOwnershipProof
	ExpectedState     domain.AgentServiceDesiredState
	DesiredState      domain.AgentServiceDesiredState
	ExpectedUpdatedAt time.Time
	IdempotencyKey    string
	DelegatedActor    string
}

type AgentOwnershipAcquireInput struct {
	WorkspaceKey    string
	AgentID         string
	LeaseID         string
	OwnerID         string
	RuntimeProvider domain.RuntimeProvider
	NodeID          string
	TTLSeconds      int
	DelegatedActor  string
}

type AgentOwnershipGrant struct {
	Lease *domain.AgentOwnershipLease
	Token string
}

type AgentOwnershipQuery struct {
	OwnerID         string
	RuntimeProvider domain.RuntimeProvider
	NodeID          string
	Status          domain.AgentLeaseStatus
	Limit           int
}

type AgentOwnershipRenewInput struct {
	Proof          AgentOwnershipProof
	TTLSeconds     int
	DelegatedActor string
}

type AgentOwnershipReleaseInput struct {
	Proof          AgentOwnershipProof
	DelegatedActor string
}

type agentManagementStore struct {
	client agentManagementRequester
}

var _ AgentManagementTransport = (*agentManagementStore)(nil)

func newAgentManagementTransport(client agentManagementRequester) AgentManagementTransport {
	return &agentManagementStore{client: client}
}

func (transport *agentManagementStore) GetAgentService(
	ctx context.Context,
	workspace,
	serviceID string,
) (*domain.AgentService, error) {
	var out domain.AgentService
	path := "/api/v1/" + pathEscape(workspace) + "/agent-services/" + pathEscape(serviceID)
	if err := transport.client.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (transport *agentManagementStore) ListAgentServices(
	ctx context.Context,
	workspace string,
	query AgentServiceQuery,
) ([]*domain.AgentService, error) {
	values := url.Values{}
	if query.Kind != "" {
		values.Set("kind", string(query.Kind))
	}
	if query.DesiredState != "" {
		values.Set("desired_state", string(query.DesiredState))
	}
	if query.RoleName != "" {
		values.Set("role_name", query.RoleName)
	}
	if query.IncludeDeleted {
		values.Set("include_deleted", "true")
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	var response struct {
		AgentServices []*domain.AgentService `json:"agent_services"`
	}
	path := withQuery(
		"/api/v1/"+pathEscape(workspace)+"/agent-services",
		values,
	)
	if err := transport.client.Do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	if response.AgentServices == nil {
		response.AgentServices = []*domain.AgentService{}
	}
	return response.AgentServices, nil
}

func (transport *agentManagementStore) GetAgentRole(
	ctx context.Context,
	workspace,
	name string,
) (*domain.Role, error) {
	return transport.client.GetRole(ctx, workspace, name)
}

func (transport *agentManagementStore) ListAgentRoles(
	ctx context.Context,
	workspace string,
) ([]*domain.Role, error) {
	return transport.client.ListRoles(ctx, workspace)
}

func (transport *agentManagementStore) CreateAgentRole(
	ctx context.Context,
	workspace string,
	input AgentRoleInput,
) (*domain.Role, error) {
	return transport.client.CreateRole(ctx, store.RoleCreate{
		WorkspaceKey: workspace, Name: input.Name, Kind: input.Kind,
		Description: input.Description, Prompt: input.Prompt, PromptFile: input.PromptFile,
		Model: input.Model, TaskFilter: input.TaskFilter, Backend: input.Backend, Effort: input.Effort,
		PathPatterns:   append([]string(nil), input.PathPatterns...),
		Skills:         append([]string(nil), input.Skills...),
		MaxPriority:    cloneAgentManagementInt(input.MaxPriority),
		MaxConcurrency: cloneAgentManagementInt(input.MaxConcurrency),
		ReadOnly:       input.ReadOnly,
		AllowedTools:   append([]string(nil), input.AllowedTools...),
		DeniedTools:    append([]string(nil), input.DeniedTools...),
		MaxBudgetUSD:   cloneAgentManagementFloat64(input.MaxBudgetUSD),
	})
}

func (transport *agentManagementStore) UpdateAgentRole(
	ctx context.Context,
	input AgentRoleUpdateInput,
) (*domain.Role, error) {
	headers, err := delegatedActorHeaders(input.DelegatedActor)
	if err != nil {
		return nil, err
	}
	body := struct {
		ExpectedUpdatedAt time.Time             `json:"expected_updated_at"`
		Patch             agentRoleCASPatchBody `json:"patch"`
	}{
		ExpectedUpdatedAt: input.ExpectedUpdatedAt,
		Patch:             newAgentRoleCASPatchBody(input.Patch),
	}
	var out domain.Role
	path := "/api/v1/" + pathEscape(input.WorkspaceKey) + "/roles/" +
		pathEscape(input.RoleName) + "/definition"
	if err := transport.client.DoWithHeaders(ctx, http.MethodPatch, path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (transport *agentManagementStore) DeleteAgentRole(
	ctx context.Context,
	input AgentRoleDeleteInput,
) error {
	headers, err := delegatedActorHeaders(input.DelegatedActor)
	if err != nil {
		return err
	}
	body := struct {
		ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
	}{ExpectedUpdatedAt: input.ExpectedUpdatedAt}
	path := "/api/v1/" + pathEscape(input.WorkspaceKey) + "/roles/" +
		pathEscape(input.RoleName) + "/delete"
	return transport.client.DoWithHeaders(ctx, http.MethodPost, path, body, nil, headers)
}

type agentRoleCASPatchBody struct {
	Kind              *string   `json:"kind,omitempty"`
	Description       *string   `json:"description,omitempty"`
	Prompt            *string   `json:"prompt,omitempty"`
	PromptFile        *string   `json:"prompt_file,omitempty"`
	Model             *string   `json:"model,omitempty"`
	TaskFilter        *string   `json:"task_filter,omitempty"`
	Backend           *string   `json:"backend,omitempty"`
	Effort            *string   `json:"effort,omitempty"`
	PathPatterns      *[]string `json:"path_patterns,omitempty"`
	Skills            *[]string `json:"skills,omitempty"`
	MaxPriority       *int      `json:"max_priority,omitempty"`
	ClearMaxPriority  bool      `json:"clear_max_priority,omitempty"`
	MaxConcurrency    *int      `json:"max_concurrency,omitempty"`
	ClearConcurrency  bool      `json:"clear_concurrency,omitempty"`
	ReadOnly          *bool     `json:"read_only,omitempty"`
	AllowedTools      *[]string `json:"allowed_tools,omitempty"`
	DeniedTools       *[]string `json:"denied_tools,omitempty"`
	MaxBudgetUSD      *float64  `json:"max_budget_usd,omitempty"`
	ClearMaxBudgetUSD bool      `json:"clear_max_budget_usd,omitempty"`
}

func newAgentRoleCASPatchBody(patch AgentRolePatch) agentRoleCASPatchBody {
	body := agentRoleCASPatchBody{
		Kind: patch.Kind, Description: patch.Description,
		Prompt: patch.Prompt, PromptFile: patch.PromptFile,
		Model: patch.Model, TaskFilter: patch.TaskFilter,
		Backend: patch.Backend, Effort: patch.Effort,
		PathPatterns: patch.PathPatterns, Skills: patch.Skills,
		ReadOnly: patch.ReadOnly, AllowedTools: patch.AllowedTools,
		DeniedTools: patch.DeniedTools,
	}
	if patch.MaxPriority != nil {
		if *patch.MaxPriority == nil {
			body.ClearMaxPriority = true
		} else {
			body.MaxPriority = *patch.MaxPriority
		}
	}
	if patch.MaxConcurrency != nil {
		if *patch.MaxConcurrency == nil {
			body.ClearConcurrency = true
		} else {
			body.MaxConcurrency = *patch.MaxConcurrency
		}
	}
	if patch.MaxBudgetUSD != nil {
		if *patch.MaxBudgetUSD == nil {
			body.ClearMaxBudgetUSD = true
		} else {
			body.MaxBudgetUSD = *patch.MaxBudgetUSD
		}
	}
	return body
}

func (transport *agentManagementStore) CreateAgentService(
	ctx context.Context,
	input AgentServiceCreateInput,
) (*domain.AgentService, error) {
	headers, err := delegatedActorHeaders(input.DelegatedActor)
	if err != nil {
		return nil, err
	}
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
		ServiceID: input.ServiceID, Name: input.Name, Kind: input.Kind,
		DesiredState: input.DesiredState, RoleName: input.RoleName,
		DriverID: input.DriverID, DriverVersionID: input.DriverVersionID,
		ProfileName: input.ProfileName, ScheduleID: input.ScheduleID,
		EventSources:    append([]string(nil), input.EventSources...),
		TriggerRefs:     append([]string(nil), input.TriggerRefs...),
		PlacementPolicy: input.PlacementPolicy, MaxInstances: input.MaxInstances,
		LeaseID: input.LeaseID, RestartPolicy: input.RestartPolicy,
		Permissions:  append([]string(nil), input.Permissions...),
		BudgetPolicy: input.BudgetPolicy, StateRef: input.StateRef,
		Metadata: cloneAgentManagementMap(input.Metadata),
	}
	var out domain.AgentService
	path := "/api/v1/" + pathEscape(input.WorkspaceKey) + "/agent-services"
	if err := transport.client.DoWithHeaders(ctx, http.MethodPost, path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (transport *agentManagementStore) UpdateAgentServiceIdentity(
	ctx context.Context,
	input AgentServiceUpdateInput,
) (*domain.AgentService, error) {
	headers, err := delegatedActorHeaders(input.DelegatedActor)
	if err != nil {
		return nil, err
	}
	if agentServiceCompatibilityPatch(input.Patch) {
		return nil, errors.Join(ErrAgentServiceUnsupportedIdentityPatch, domain.ErrInvalid)
	}
	body := struct {
		ExpectedUpdatedAt time.Time                 `json:"expected_updated_at"`
		Patch             AgentServiceIdentityPatch `json:"patch"`
	}{ExpectedUpdatedAt: input.ExpectedUpdatedAt, Patch: input.Patch}
	var out domain.AgentService
	path := "/api/v1/" + pathEscape(input.WorkspaceKey) + "/agent-services/" +
		pathEscape(input.ServiceID)
	if err := transport.client.DoWithHeaders(ctx, http.MethodPatch, path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func agentServiceCompatibilityPatch(patch AgentServiceIdentityPatch) bool {
	return patch.ProfileName != nil || patch.ScheduleID != nil ||
		patch.EventSources != nil || patch.TriggerRefs != nil ||
		patch.LeaseID != nil || patch.Permissions != nil || patch.StateRef != nil
}

func (transport *agentManagementStore) ArchiveAgentService(
	ctx context.Context,
	input AgentServiceArchiveInput,
) (*domain.AgentService, error) {
	headers, err := delegatedActorHeaders(input.DelegatedActor)
	if err != nil {
		return nil, err
	}
	body := struct {
		ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
	}{ExpectedUpdatedAt: input.ExpectedUpdatedAt}
	var out domain.AgentService
	path := "/api/v1/" + pathEscape(input.WorkspaceKey) + "/agent-services/" +
		pathEscape(input.ServiceID) + "/archive"
	if err := transport.client.DoWithHeaders(ctx, http.MethodPost, path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (transport *agentManagementStore) SetAgentServiceDesiredState(
	ctx context.Context,
	input AgentServiceDesiredStateInput,
) (*domain.AgentService, error) {
	headers, err := delegatedActorHeaders(input.DelegatedActor)
	if err != nil {
		return nil, err
	}
	body := struct {
		ExpectedState     domain.AgentServiceDesiredState `json:"expected_state"`
		DesiredState      domain.AgentServiceDesiredState `json:"desired_state"`
		ExpectedUpdatedAt time.Time                       `json:"expected_updated_at"`
	}{
		ExpectedState: input.ExpectedState, DesiredState: input.DesiredState,
		ExpectedUpdatedAt: input.ExpectedUpdatedAt,
	}
	var out domain.AgentService
	path := "/api/v1/" + pathEscape(input.WorkspaceKey) + "/agent-services/" +
		pathEscape(input.ServiceID) + "/desired-state"
	if err := transport.client.DoWithHeaders(ctx, http.MethodPost, path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (transport *agentManagementStore) SetAgentServiceDesiredStateOwned(
	ctx context.Context,
	input AgentServiceOwnedDesiredStateInput,
) (*domain.AgentService, error) {
	headers, err := ownershipHeaders(input.DelegatedActor, input.Proof.LeaseToken)
	if err != nil {
		return nil, err
	}
	body := struct {
		LeaseID           string                          `json:"lease_id"`
		OwnerID           string                          `json:"owner_id"`
		RuntimeProvider   domain.RuntimeProvider          `json:"runtime_provider"`
		NodeID            string                          `json:"node_id"`
		FencingToken      int64                           `json:"fencing_token"`
		ExpectedState     domain.AgentServiceDesiredState `json:"expected_state"`
		DesiredState      domain.AgentServiceDesiredState `json:"desired_state"`
		ExpectedUpdatedAt time.Time                       `json:"expected_updated_at"`
		IdempotencyKey    string                          `json:"idempotency_key"`
	}{
		LeaseID: input.Proof.LeaseID, OwnerID: input.Proof.OwnerID,
		RuntimeProvider: input.Proof.RuntimeProvider, NodeID: input.Proof.NodeID,
		FencingToken: input.Proof.FencingToken, ExpectedState: input.ExpectedState,
		DesiredState: input.DesiredState, ExpectedUpdatedAt: input.ExpectedUpdatedAt,
		IdempotencyKey: input.IdempotencyKey,
	}
	var out domain.AgentService
	path := "/api/v1/" + pathEscape(input.Proof.WorkspaceKey) + "/agent-services/" +
		pathEscape(input.Proof.AgentID) + "/desired-state/owned"
	if err := transport.client.DoWithHeaders(ctx, http.MethodPost, path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (transport *agentManagementStore) ApplyAgentServiceLifecycle(
	ctx context.Context,
	input AgentServiceLifecycleInput,
) (*AgentServiceLifecycleResult, error) {
	headers, err := delegatedActorHeaders(input.DelegatedActor)
	if err != nil {
		return nil, err
	}
	body := struct {
		Action               string    `json:"action"`
		ExpectedUpdatedAt    time.Time `json:"expected_updated_at"`
		ExpectedGenerationID string    `json:"expected_generation_id,omitempty"`
		IdempotencyKey       string    `json:"idempotency_key"`
	}{
		Action: input.Action, ExpectedUpdatedAt: input.ExpectedUpdatedAt,
		ExpectedGenerationID: input.ExpectedGenerationID,
		IdempotencyKey:       input.IdempotencyKey,
	}
	var out AgentServiceLifecycleResult
	path := "/api/v1/" + pathEscape(input.WorkspaceKey) + "/agent-services/" +
		pathEscape(input.ServiceID) + "/lifecycle"
	if err := transport.client.DoWithHeaders(ctx, http.MethodPost, path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (transport *agentManagementStore) AcquireAgentOwnership(
	ctx context.Context,
	input AgentOwnershipAcquireInput,
) (*AgentOwnershipGrant, error) {
	headers, err := delegatedActorHeaders(input.DelegatedActor)
	if err != nil {
		return nil, err
	}
	body := struct {
		LeaseID         string                 `json:"lease_id,omitempty"`
		OwnerID         string                 `json:"owner_id"`
		RuntimeProvider domain.RuntimeProvider `json:"runtime_provider"`
		NodeID          string                 `json:"node_id"`
		TTLSeconds      int                    `json:"ttl_seconds,omitempty"`
	}{
		LeaseID: input.LeaseID, OwnerID: input.OwnerID,
		RuntimeProvider: input.RuntimeProvider, NodeID: input.NodeID,
		TTLSeconds: input.TTLSeconds,
	}
	var response struct {
		Lease *domain.AgentOwnershipLease `json:"lease"`
		Token string                      `json:"token"`
	}
	path := "/api/v1/" + pathEscape(input.WorkspaceKey) + "/agent-ownership-leases/" +
		pathEscape(input.AgentID) + "/acquire"
	if err := transport.client.DoWithHeaders(ctx, http.MethodPost, path, body, &response, headers); err != nil {
		return nil, err
	}
	return &AgentOwnershipGrant{Lease: response.Lease, Token: response.Token}, nil
}

func (transport *agentManagementStore) GetAgentOwnership(
	ctx context.Context,
	workspace,
	agentID string,
) (*domain.AgentOwnershipLease, error) {
	var out domain.AgentOwnershipLease
	path := "/api/v1/" + pathEscape(workspace) + "/agent-ownership-leases/" + pathEscape(agentID)
	if err := transport.client.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (transport *agentManagementStore) ListAgentOwnership(
	ctx context.Context,
	workspace string,
	query AgentOwnershipQuery,
) ([]*domain.AgentOwnershipLease, error) {
	values := url.Values{}
	if query.OwnerID != "" {
		values.Set("owner_id", query.OwnerID)
	}
	if query.RuntimeProvider != "" {
		values.Set("runtime_provider", string(query.RuntimeProvider))
	}
	if query.NodeID != "" {
		values.Set("node_id", query.NodeID)
	}
	if query.Status != "" {
		values.Set("status", string(query.Status))
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	var response struct {
		AgentOwnershipLeases []*domain.AgentOwnershipLease `json:"agent_ownership_leases"`
	}
	path := withQuery(
		"/api/v1/"+pathEscape(workspace)+"/agent-ownership-leases",
		values,
	)
	if err := transport.client.Do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	if response.AgentOwnershipLeases == nil {
		response.AgentOwnershipLeases = []*domain.AgentOwnershipLease{}
	}
	return response.AgentOwnershipLeases, nil
}

func (transport *agentManagementStore) RenewAgentOwnership(
	ctx context.Context,
	input AgentOwnershipRenewInput,
) (*domain.AgentOwnershipLease, error) {
	headers, err := ownershipHeaders(input.DelegatedActor, input.Proof.LeaseToken)
	if err != nil {
		return nil, err
	}
	body := struct {
		LeaseID         string                 `json:"lease_id"`
		OwnerID         string                 `json:"owner_id"`
		RuntimeProvider domain.RuntimeProvider `json:"runtime_provider"`
		NodeID          string                 `json:"node_id"`
		FencingToken    int64                  `json:"fencing_token"`
		TTLSeconds      int                    `json:"ttl_seconds,omitempty"`
	}{
		LeaseID: input.Proof.LeaseID, OwnerID: input.Proof.OwnerID,
		RuntimeProvider: input.Proof.RuntimeProvider, NodeID: input.Proof.NodeID,
		FencingToken: input.Proof.FencingToken, TTLSeconds: input.TTLSeconds,
	}
	var out domain.AgentOwnershipLease
	path := "/api/v1/" + pathEscape(input.Proof.WorkspaceKey) + "/agent-ownership-leases/" +
		pathEscape(input.Proof.AgentID) + "/heartbeat"
	if err := transport.client.DoWithHeaders(ctx, http.MethodPost, path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func (transport *agentManagementStore) ReleaseAgentOwnership(
	ctx context.Context,
	input AgentOwnershipReleaseInput,
) (*domain.AgentOwnershipLease, error) {
	headers, err := ownershipHeaders(input.DelegatedActor, input.Proof.LeaseToken)
	if err != nil {
		return nil, err
	}
	body := struct {
		LeaseID         string                 `json:"lease_id"`
		OwnerID         string                 `json:"owner_id"`
		RuntimeProvider domain.RuntimeProvider `json:"runtime_provider"`
		NodeID          string                 `json:"node_id"`
		FencingToken    int64                  `json:"fencing_token"`
	}{
		LeaseID: input.Proof.LeaseID, OwnerID: input.Proof.OwnerID,
		RuntimeProvider: input.Proof.RuntimeProvider, NodeID: input.Proof.NodeID,
		FencingToken: input.Proof.FencingToken,
	}
	var out domain.AgentOwnershipLease
	path := "/api/v1/" + pathEscape(input.Proof.WorkspaceKey) + "/agent-ownership-leases/" +
		pathEscape(input.Proof.AgentID) + "/release"
	if err := transport.client.DoWithHeaders(ctx, http.MethodPost, path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

func ownershipHeaders(actor, token string) (map[string]string, error) {
	headers, err := delegatedActorHeaders(actor)
	if err != nil {
		return nil, err
	}
	if token == "" || token != strings.TrimSpace(token) {
		return nil, fmt.Errorf("agent ownership lease token is required: %w", domain.ErrInvalid)
	}
	headers[AgentOwnershipLeaseTokenHeader] = token
	return headers, nil
}

func cloneAgentManagementMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	out := make(map[string]string, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func cloneAgentManagementInt(value *int) *int {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneAgentManagementFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
