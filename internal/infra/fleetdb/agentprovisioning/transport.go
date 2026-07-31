package agentprovisioning

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	fleettransport "github.com/tysonthomas9/loomcli/internal/infra/fleetdb/transport"
)

var (
	ErrAgentProvisioningNotFound          = errors.New("fleetdb: agent provisioning not found")
	ErrAgentProvisioningInvalid           = errors.New("fleetdb: agent provisioning invalid")
	ErrAgentProvisioningConflict          = errors.New("fleetdb: agent provisioning intent conflict")
	ErrAgentProvisioningConcurrentWrite   = errors.New("fleetdb: agent provisioning concurrent write")
	ErrAgentProvisioningInvalidTransition = errors.New("fleetdb: agent provisioning invalid transition")
)

// AgentProvisioningTransport is the narrow low-level FleetDB surface consumed
// by the AgentProvisioning application adapter. It shares the process-wide
// Client's authentication, tracing, retry policy, and connection pool.
type AgentProvisioningTransport interface {
	BeginAgentProvisioning(context.Context, string, AgentProvisioningBeginInput) (*AgentProvisioningRecord, error)
	GetAgentProvisioning(context.Context, string, string) (*AgentProvisioningRecord, error)
	ListPendingAgentProvisioning(context.Context, string, int) ([]*AgentProvisioningRecord, error)
	SaveAgentProvisioningProgress(context.Context, string, string, AgentProvisioningProgressInput) (*AgentProvisioningRecord, error)
	EnsureAgentProvisioningRole(context.Context, string, string, string) (*AgentProvisioningRoleResult, error)
	EnsureAgentProvisioningAgentService(context.Context, string, string, string) (*AgentProvisioningAgentResult, error)
	EnsureAgentProvisioningTriggerBinding(context.Context, string, string, string) (*AgentProvisioningBindingResult, error)
	EnsureAgentProvisioningConnectorGrant(context.Context, string, string, string, string) (*AgentProvisioningGrantResult, error)
}

type AgentProvisioningRoleSpec struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind,omitempty"`
	Description    string   `json:"description,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
	PromptFile     string   `json:"prompt_file,omitempty"`
	Model          string   `json:"model,omitempty"`
	TaskFilter     string   `json:"task_filter,omitempty"`
	Backend        string   `json:"backend,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	PathPatterns   []string `json:"path_patterns,omitempty"`
	Skills         []string `json:"skills,omitempty"`
	MaxPriority    *int     `json:"max_priority,omitempty"`
	MaxConcurrency *int     `json:"max_concurrency,omitempty"`
	ReadOnly       bool     `json:"read_only,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`
	MaxBudgetUSD   *float64 `json:"max_budget_usd,omitempty"`
}

type AgentProvisioningAgentSpec struct {
	AgentID      string            `json:"agent_id"`
	Name         string            `json:"name,omitempty"`
	Kind         string            `json:"kind,omitempty"`
	DesiredState string            `json:"desired_state,omitempty"`
	RoleName     string            `json:"role_name"`
	BudgetPolicy string            `json:"budget_policy,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type AgentProvisioningBindingSpec struct {
	BindingID         string   `json:"binding_id"`
	Name              string   `json:"name,omitempty"`
	SourceKind        string   `json:"source_kind,omitempty"`
	SourceConfigRef   string   `json:"source_config_ref,omitempty"`
	RouteKey          string   `json:"route_key,omitempty"`
	EventPatterns     []string `json:"event_patterns,omitempty"`
	DriverID          string   `json:"driver_id"`
	DriverVersionID   string   `json:"driver_version_id"`
	Entrypoint        string   `json:"entrypoint,omitempty"`
	ConcurrencyPolicy string   `json:"concurrency_policy,omitempty"`
	Schedule          string   `json:"schedule,omitempty"`
	ScheduleZone      string   `json:"schedule_zone,omitempty"`
	Enabled           bool     `json:"enabled,omitempty"`
}

type AgentProvisioningGrantSpec struct {
	GrantID         string `json:"grant_id"`
	ConnectorID     string `json:"connector_id"`
	Action          string `json:"action"`
	ResourcePattern string `json:"resource_pattern"`
}

type AgentProvisioningBeginInput struct {
	ProvisioningID string                       `json:"provisioning_id"`
	Role           AgentProvisioningRoleSpec    `json:"role"`
	Agent          AgentProvisioningAgentSpec   `json:"agent"`
	Binding        AgentProvisioningBindingSpec `json:"binding"`
	Grants         []AgentProvisioningGrantSpec `json:"grants,omitempty"`
	DelegatedActor string                       `json:"-"`
}

type AgentProvisioningSpec struct {
	ProvisioningID string                       `json:"provisioning_id"`
	WorkspaceKey   string                       `json:"workspace_key"`
	RequestedBy    string                       `json:"requested_by"`
	Role           AgentProvisioningRoleSpec    `json:"role"`
	Agent          AgentProvisioningAgentSpec   `json:"agent"`
	Binding        AgentProvisioningBindingSpec `json:"binding"`
	Grants         []AgentProvisioningGrantSpec `json:"grants,omitempty"`
}

type AgentProvisioningRecord struct {
	ProvisioningID           string                `json:"provisioning_id"`
	ProvisioningGenerationID string                `json:"provisioning_generation_id"`
	WorkspaceKey             string                `json:"workspace_key"`
	RequestedBy              string                `json:"requested_by"`
	SpecFingerprint          string                `json:"spec_fingerprint"`
	Spec                     AgentProvisioningSpec `json:"spec"`
	State                    string                `json:"state"`
	CompletedSteps           []string              `json:"completed_steps,omitempty"`
	CompletedGrants          []string              `json:"completed_grants,omitempty"`
	UnusedRolePolicy         string                `json:"unused_role_policy"`
	LastErrorClass           string                `json:"last_error_class,omitempty"`
	Version                  int64                 `json:"version"`
	CreatedAt                time.Time             `json:"created_at"`
	UpdatedAt                time.Time             `json:"updated_at"`
	CompletedAt              *time.Time            `json:"completed_at,omitempty"`
}

type AgentProvisioningProgressInput struct {
	ExpectedProvisioningGenerationID string   `json:"expected_provisioning_generation_id"`
	ExpectedVersion                  int64    `json:"expected_version"`
	State                            string   `json:"state"`
	CompletedSteps                   []string `json:"completed_steps,omitempty"`
	CompletedGrants                  []string `json:"completed_grants,omitempty"`
	LastErrorClass                   string   `json:"last_error_class,omitempty"`
}

// AgentProvisioningRoleResult is the low-level transport DTO for the exact
// owner state returned after an ensure-role step.
type AgentProvisioningRoleResult struct {
	WorkspaceKey   string   `json:"workspace_key"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind,omitempty"`
	Description    string   `json:"description,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
	PromptFile     string   `json:"prompt_file,omitempty"`
	Model          string   `json:"model,omitempty"`
	TaskFilter     string   `json:"task_filter,omitempty"`
	Backend        string   `json:"backend,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	PathPatterns   []string `json:"path_patterns,omitempty"`
	Skills         []string `json:"skills,omitempty"`
	MaxPriority    *int     `json:"max_priority,omitempty"`
	MaxConcurrency *int     `json:"max_concurrency,omitempty"`
	ReadOnly       bool     `json:"read_only,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`
	MaxBudgetUSD   *float64 `json:"max_budget_usd,omitempty"`
}

// AgentProvisioningAgentResult is the low-level transport DTO for the exact
// owner state returned after an ensure-agent step.
type AgentProvisioningAgentResult struct {
	WorkspaceKey string            `json:"workspace_key"`
	ServiceID    string            `json:"service_id"`
	Name         string            `json:"name,omitempty"`
	Kind         string            `json:"kind,omitempty"`
	DesiredState string            `json:"desired_state,omitempty"`
	RoleName     string            `json:"role_name"`
	BudgetPolicy string            `json:"budget_policy,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// AgentProvisioningBindingResult is the low-level transport DTO for the exact
// owner state returned after an ensure-binding step.
type AgentProvisioningBindingResult struct {
	WorkspaceKey         string   `json:"workspace_key"`
	BindingID            string   `json:"binding_id"`
	Name                 string   `json:"name,omitempty"`
	SourceKind           string   `json:"source_kind,omitempty"`
	SourceConfigRef      string   `json:"source_config_ref,omitempty"`
	RouteKey             string   `json:"route_key,omitempty"`
	EventTypePatterns    []string `json:"event_type_patterns,omitempty"`
	DriverID             string   `json:"driver_id"`
	DriverVersionID      string   `json:"driver_version_id"`
	TargetEntrypoint     string   `json:"target_entrypoint,omitempty"`
	TargetAgentServiceID string   `json:"target_agent_service_id"`
	ConcurrencyPolicy    string   `json:"concurrency_policy,omitempty"`
	Schedule             string   `json:"schedule,omitempty"`
	ScheduleTimezone     string   `json:"schedule_timezone,omitempty"`
	Enabled              bool     `json:"enabled,omitempty"`
}

// AgentProvisioningGrantResult is the low-level transport DTO for the exact
// owner state returned after an ensure-grant step.
type AgentProvisioningGrantResult struct {
	WorkspaceKey    string     `json:"workspace_key"`
	GrantID         string     `json:"grant_id"`
	ConnectorID     string     `json:"connector_id"`
	BindingID       string     `json:"binding_id"`
	Action          string     `json:"action"`
	ResourcePattern string     `json:"resource_pattern"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
}

type agentProvisioningStepGuardInput struct {
	ExpectedProvisioningGenerationID string `json:"expected_provisioning_generation_id"`
}

type agentProvisioningStore struct {
	client fleettransport.Requester
}

var _ AgentProvisioningTransport = (*agentProvisioningStore)(nil)

func New(client fleettransport.Requester) AgentProvisioningTransport {
	return &agentProvisioningStore{client: client}
}

func (store *agentProvisioningStore) BeginAgentProvisioning(
	ctx context.Context,
	workspace string,
	input AgentProvisioningBeginInput,
) (*AgentProvisioningRecord, error) {
	var out AgentProvisioningRecord
	path := "/api/v1/" + pathEscape(workspace) + "/agent-provisioning"
	headers, err := fleettransport.DelegatedActorHeaders(input.DelegatedActor)
	if err != nil {
		return nil, err
	}
	if err := store.client.DoWithHeaders(ctx, "POST", path, input, &out, headers); err != nil {
		return nil, mapAgentProvisioningError(err)
	}
	return &out, nil
}

func (store *agentProvisioningStore) GetAgentProvisioning(
	ctx context.Context,
	workspace,
	provisioningID string,
) (*AgentProvisioningRecord, error) {
	var out AgentProvisioningRecord
	path := "/api/v1/" + pathEscape(workspace) + "/agent-provisioning/" + pathEscape(provisioningID)
	if err := store.client.Do(ctx, "GET", path, nil, &out); err != nil {
		return nil, mapAgentProvisioningError(err)
	}
	return &out, nil
}

func (store *agentProvisioningStore) ListPendingAgentProvisioning(
	ctx context.Context,
	workspace string,
	limit int,
) ([]*AgentProvisioningRecord, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	path := withQuery("/api/v1/"+pathEscape(workspace)+"/agent-provisioning/pending", query)
	var response struct {
		AgentProvisioning []*AgentProvisioningRecord `json:"agent_provisioning"`
	}
	if err := store.client.Do(ctx, "GET", path, nil, &response); err != nil {
		return nil, mapAgentProvisioningError(err)
	}
	if response.AgentProvisioning == nil {
		response.AgentProvisioning = []*AgentProvisioningRecord{}
	}
	return response.AgentProvisioning, nil
}

func (store *agentProvisioningStore) SaveAgentProvisioningProgress(
	ctx context.Context,
	workspace,
	provisioningID string,
	input AgentProvisioningProgressInput,
) (*AgentProvisioningRecord, error) {
	var out AgentProvisioningRecord
	path := "/api/v1/" + pathEscape(workspace) + "/agent-provisioning/" +
		pathEscape(provisioningID) + "/progress"
	if err := store.client.Do(ctx, "POST", path, input, &out); err != nil {
		return nil, mapAgentProvisioningError(err)
	}
	return &out, nil
}

func (store *agentProvisioningStore) EnsureAgentProvisioningRole(
	ctx context.Context,
	workspace,
	provisioningID,
	provisioningGenerationID string,
) (*AgentProvisioningRoleResult, error) {
	var out AgentProvisioningRoleResult
	if err := store.ensureAgentProvisioningStep(
		ctx,
		workspace,
		provisioningID,
		"ensure-role",
		"",
		provisioningGenerationID,
		&out,
	); err != nil {
		return nil, err
	}
	return &out, nil
}

func (store *agentProvisioningStore) EnsureAgentProvisioningAgentService(
	ctx context.Context,
	workspace,
	provisioningID,
	provisioningGenerationID string,
) (*AgentProvisioningAgentResult, error) {
	var out AgentProvisioningAgentResult
	if err := store.ensureAgentProvisioningStep(
		ctx,
		workspace,
		provisioningID,
		"ensure-agent-service",
		"",
		provisioningGenerationID,
		&out,
	); err != nil {
		return nil, err
	}
	return &out, nil
}

func (store *agentProvisioningStore) EnsureAgentProvisioningTriggerBinding(
	ctx context.Context,
	workspace,
	provisioningID,
	provisioningGenerationID string,
) (*AgentProvisioningBindingResult, error) {
	var out AgentProvisioningBindingResult
	if err := store.ensureAgentProvisioningStep(
		ctx,
		workspace,
		provisioningID,
		"ensure-trigger-binding",
		"",
		provisioningGenerationID,
		&out,
	); err != nil {
		return nil, err
	}
	return &out, nil
}

func (store *agentProvisioningStore) EnsureAgentProvisioningConnectorGrant(
	ctx context.Context,
	workspace,
	provisioningID,
	provisioningGenerationID,
	grantID string,
) (*AgentProvisioningGrantResult, error) {
	var out AgentProvisioningGrantResult
	if err := store.ensureAgentProvisioningStep(
		ctx,
		workspace,
		provisioningID,
		"ensure-connector-grant",
		grantID,
		provisioningGenerationID,
		&out,
	); err != nil {
		return nil, err
	}
	return &out, nil
}

func (store *agentProvisioningStore) ensureAgentProvisioningStep(
	ctx context.Context,
	workspace,
	provisioningID,
	operation,
	targetID,
	provisioningGenerationID string,
	out any,
) error {
	path := "/api/v1/" + pathEscape(workspace) + "/agent-provisioning/" +
		pathEscape(provisioningID) + "/" + operation
	if targetID != "" {
		path += "/" + pathEscape(targetID)
	}
	if err := store.client.Do(
		ctx,
		"POST",
		path,
		agentProvisioningStepGuardInput{
			ExpectedProvisioningGenerationID: provisioningGenerationID,
		},
		out,
	); err != nil {
		return mapAgentProvisioningError(err)
	}
	return nil
}

func mapAgentProvisioningError(err error) error {
	if err == nil {
		return nil
	}
	var sentinel error
	switch {
	case errors.Is(err, domain.ErrNotFound):
		sentinel = ErrAgentProvisioningNotFound
	case errors.Is(err, fleettransport.ErrRevisionConflict):
		sentinel = ErrAgentProvisioningConcurrentWrite
	case errors.Is(err, domain.ErrInvalidTransition):
		sentinel = ErrAgentProvisioningInvalidTransition
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		sentinel = ErrAgentProvisioningConflict
	case errors.Is(err, domain.ErrInvalid):
		sentinel = ErrAgentProvisioningInvalid
	default:
		return err
	}
	return fmt.Errorf("agent provisioning transport: %w", errors.Join(sentinel, err))
}

func pathEscape(value string) string {
	return fleettransport.PathEscape(value)
}

func withQuery(path string, query url.Values) string {
	return fleettransport.WithQuery(path, query)
}
