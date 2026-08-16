package agentdef

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/managementapi"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// ErrCapabilityUnavailable is the default-deny result while the standalone
// CLI has no authenticated composition for the Phase 5 capability root.
var ErrCapabilityUnavailable = errors.New("agentdef: capability boundary unavailable")

// AgentDefinition is the CLI read projection. The durable identity fields come
// from Agents. The remaining optional fields are present so a future composed
// projection can preserve the existing CLI output while their respective
// owners (Interaction, Source Control, and Automation) are migrated.
type AgentDefinition struct {
	WorkspaceKey string              `json:"workspace_key"`
	AgentID      string              `json:"agent_id,omitempty"`
	GenerationID string              `json:"generation_id"`
	Name         string              `json:"name"`
	RoleName     string              `json:"role_name"`
	Kind         agents.AgentKind    `json:"kind,omitempty"`
	State        string              `json:"state"`
	DesiredState agents.DesiredState `json:"desired_state"`
	Auto         bool                `json:"auto"`

	Backend        string   `json:"backend,omitempty"`
	Repos          []string `json:"repos,omitempty"`
	RepoGroups     []string `json:"repo_groups,omitempty"`
	CrossRepo      bool     `json:"cross_repo,omitempty"`
	Parent         string   `json:"parent,omitempty"`
	Mode           string   `json:"mode,omitempty"`
	TaskFilter     string   `json:"task_filter,omitempty"`
	MaxConcurrency int      `json:"max_concurrency,omitempty"`
	BudgetPolicy   string   `json:"budget_policy,omitempty"`

	ProfileName           string     `json:"profile_name,omitempty"`
	PlacementPolicy       string     `json:"placement_policy,omitempty"`
	RestartPolicy         string     `json:"restart_policy,omitempty"`
	OrchestratorSessionID string     `json:"orchestrator_session_id,omitempty"`
	CreatedBy             string     `json:"created_by,omitempty"`
	DeletedAt             *time.Time `json:"deleted_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// AgentDefinitionCreateCommand contains only fields owned by Agents. Legacy
// cross-capability flags are absent from the command surface, so Cobra rejects
// them before this boundary can mutate an Agent.
type AgentDefinitionCreateCommand struct {
	Canonical agents.CreateAgentCommand
}

type AgentLifecycleCommand struct {
	WorkspaceKey string
	AgentID      string
	Action       agents.LifecycleAction
	RequestID    string
}

// AgentDefinitionBoundary is the complete command/read seam consumed by the
// CLI. It deliberately exposes neither a composite Store nor transport types.
type AgentDefinitionBoundary interface {
	CreateAgentDefinition(context.Context, AgentDefinitionCreateCommand) (*AgentDefinition, error)
	GetAgentDefinition(context.Context, string, string) (*AgentDefinition, error)
	ListAgentDefinitions(context.Context, string) ([]*AgentDefinition, error)
	ApplyAgentLifecycle(context.Context, AgentLifecycleCommand) (*AgentDefinition, error)
}

type agentdefRuntime struct {
	definitions AgentDefinitionBoundary
}

type agentdefRuntimeResolver func(
	context.Context,
	string,
) (agentdefRuntime, error)

type agentManagementClient interface {
	Workspace() string
	CreateAgent(context.Context, agents.CreateAgentCommand) (*agents.Agent, error)
	GetAgent(context.Context, string) (*agents.Agent, error)
	ListAgents(context.Context) ([]*agents.Agent, error)
	ApplyAgentLifecycle(
		context.Context,
		string,
		managementapi.ApplyAgentLifecycleRequest,
	) (*agents.LifecycleResult, error)
}

var newAgentManagementClient = func(
	ctx context.Context,
) (agentManagementClient, error) {
	return managementapi.New(ctx, "loom agentdef")
}

// resolveAgentdefRuntime always builds the authenticated management-API
// adapter. Standalone agentdef commands never open Store, call FleetDB
// directly, construct authority, or start an implicit local server.
var resolveAgentdefRuntime agentdefRuntimeResolver = func(
	ctx context.Context,
	workspace string,
) (agentdefRuntime, error) {
	client, err := newAgentManagementClient(ctx)
	if err != nil {
		return agentdefRuntime{}, err
	}
	if client == nil || client.Workspace() != workspace {
		return agentdefRuntime{}, fmt.Errorf(
			"configured management workspace changed during agentdef startup: %w",
			ErrCapabilityUnavailable,
		)
	}
	return agentdefRuntime{
		definitions: &managementAgentDefinitionBoundary{client: client},
	}, nil
}

var resolveAgentdefWorkspace = func() (string, error) {
	workspace := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
	if workspace == "" {
		return "", errors.New("loom agentdef requires --workspace or LOOM_WORKSPACE")
	}
	return workspace, nil
}

func withAgentdefRuntime(
	ctx context.Context,
	run func(context.Context, agentdefRuntime, string) error,
) error {
	workspace, err := resolveAgentdefWorkspace()
	if err != nil {
		return err
	}
	runtime, err := resolveAgentdefRuntime(ctx, workspace)
	if err != nil {
		return fmt.Errorf("resolve agentdef capability: %w", err)
	}
	if runtime.definitions == nil {
		return ErrCapabilityUnavailable
	}
	return run(ctx, runtime, workspace)
}

type managementAgentDefinitionBoundary struct {
	client agentManagementClient
}

var _ AgentDefinitionBoundary = (*managementAgentDefinitionBoundary)(nil)

func (boundary *managementAgentDefinitionBoundary) CreateAgentDefinition(
	ctx context.Context,
	command AgentDefinitionCreateCommand,
) (*AgentDefinition, error) {
	if boundary == nil || boundary.client == nil {
		return nil, ErrCapabilityUnavailable
	}
	record, err := boundary.client.CreateAgent(ctx, command.Canonical)
	if err != nil {
		return nil, err
	}
	return projectAgentDefinition(record)
}

func (boundary *managementAgentDefinitionBoundary) GetAgentDefinition(
	ctx context.Context,
	workspace string,
	agentID string,
) (*AgentDefinition, error) {
	if boundary == nil || boundary.client == nil || workspace != boundary.client.Workspace() {
		return nil, ErrCapabilityUnavailable
	}
	record, err := boundary.client.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return projectAgentDefinition(record)
}

func (boundary *managementAgentDefinitionBoundary) ListAgentDefinitions(
	ctx context.Context,
	workspace string,
) ([]*AgentDefinition, error) {
	if boundary == nil || boundary.client == nil || workspace != boundary.client.Workspace() {
		return nil, ErrCapabilityUnavailable
	}
	records, err := boundary.client.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*AgentDefinition, 0, len(records))
	for _, record := range records {
		projected, projectErr := projectAgentDefinition(record)
		if projectErr != nil {
			return nil, projectErr
		}
		out = append(out, projected)
	}
	return out, nil
}

func (boundary *managementAgentDefinitionBoundary) ApplyAgentLifecycle(
	ctx context.Context,
	command AgentLifecycleCommand,
) (*AgentDefinition, error) {
	if boundary == nil || boundary.client == nil ||
		command.WorkspaceKey != boundary.client.Workspace() {
		return nil, ErrCapabilityUnavailable
	}
	if err := validateLifecycleRequest(command); err != nil {
		return nil, err
	}
	current, getErr := boundary.client.GetAgent(ctx, command.AgentID)
	expectedUpdatedAt, expectedGenerationID, err := lifecycleExpectedFence(
		command,
		current,
		getErr,
		errors.Is(getErr, persistence.ErrNotFound),
	)
	if err != nil {
		return nil, err
	}
	key := lifecycleIdempotencyKey(command)
	result, err := applyManagementLifecycleWithRetry(
		ctx,
		boundary.client,
		command.AgentID,
		managementapi.ApplyAgentLifecycleRequest{
			Action: command.Action, ExpectedUpdatedAt: expectedUpdatedAt,
			ExpectedGenerationID: expectedGenerationID,
			IdempotencyKey:       key,
		},
	)
	if err != nil {
		return nil, err
	}
	return projectAgentDefinition(result.Agent)
}

type canonicalAgentsAPI interface {
	GetAgent(context.Context, string, string) (*agents.Agent, error)
	ListAgents(context.Context, string, agents.AgentFilter) ([]*agents.Agent, error)
	CreateAgent(context.Context, authority.OperatorAuthority, agents.CreateAgentCommand) (*agents.Agent, error)
	ApplyLifecycle(context.Context, authority.OperatorAuthority, agents.ApplyLifecycleCommand) (*agents.LifecycleResult, error)
}

var _ canonicalAgentsAPI = (agents.API)(nil)

type operatorAuthorityResolver interface {
	ResolveOperatorAuthority(
		context.Context,
		string,
		authority.Action,
	) (authority.OperatorAuthority, error)
}

// canonicalAgentDefinitionBoundary is the narrow inbound adapter from agentdef
// to the canonical Agents capability.
type canonicalAgentDefinitionBoundary struct {
	agents      canonicalAgentsAPI
	authorities operatorAuthorityResolver
}

var _ AgentDefinitionBoundary = (*canonicalAgentDefinitionBoundary)(nil)

func newCanonicalAgentDefinitionBoundary(
	api canonicalAgentsAPI,
	authorities operatorAuthorityResolver,
) (*canonicalAgentDefinitionBoundary, error) {
	if api == nil || authorities == nil {
		return nil, ErrCapabilityUnavailable
	}
	return &canonicalAgentDefinitionBoundary{agents: api, authorities: authorities}, nil
}

func (boundary *canonicalAgentDefinitionBoundary) CreateAgentDefinition(
	ctx context.Context,
	command AgentDefinitionCreateCommand,
) (*AgentDefinition, error) {
	auth, err := boundary.authorities.ResolveOperatorAuthority(
		ctx,
		command.Canonical.WorkspaceKey,
		agents.ActionCreateAgent,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve create-agent authority: %w", err)
	}
	record, err := boundary.agents.CreateAgent(ctx, auth, command.Canonical)
	if err != nil {
		return nil, err
	}
	return projectAgentDefinition(record)
}

func (boundary *canonicalAgentDefinitionBoundary) GetAgentDefinition(
	ctx context.Context,
	workspace string,
	agentID string,
) (*AgentDefinition, error) {
	record, err := boundary.agents.GetAgent(ctx, workspace, agentID)
	if err != nil {
		return nil, err
	}
	return projectAgentDefinition(record)
}

func (boundary *canonicalAgentDefinitionBoundary) ListAgentDefinitions(
	ctx context.Context,
	workspace string,
) ([]*AgentDefinition, error) {
	records, err := boundary.agents.ListAgents(ctx, workspace, agents.AgentFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]*AgentDefinition, 0, len(records))
	for _, record := range records {
		projected, projectErr := projectAgentDefinition(record)
		if projectErr != nil {
			return nil, projectErr
		}
		out = append(out, projected)
	}
	return out, nil
}

func (boundary *canonicalAgentDefinitionBoundary) ApplyAgentLifecycle(
	ctx context.Context,
	command AgentLifecycleCommand,
) (*AgentDefinition, error) {
	if err := validateLifecycleRequest(command); err != nil {
		return nil, err
	}
	auth, err := boundary.authorities.ResolveOperatorAuthority(
		ctx,
		command.WorkspaceKey,
		agents.ActionApplyLifecycle,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve lifecycle authority: %w", err)
	}
	current, getErr := boundary.agents.GetAgent(ctx, command.WorkspaceKey, command.AgentID)
	expectedUpdatedAt, expectedGenerationID, err := lifecycleExpectedFence(
		command,
		current,
		getErr,
		errors.Is(getErr, agents.ErrNotFound),
	)
	if err != nil {
		return nil, err
	}
	result, err := applyCanonicalLifecycleWithRetry(ctx, boundary.agents, auth, agents.ApplyLifecycleCommand{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID,
		Action: command.Action, ExpectedUpdatedAt: expectedUpdatedAt,
		ExpectedGenerationID: expectedGenerationID,
		IdempotencyKey:       lifecycleIdempotencyKey(command),
	})
	if err != nil {
		return nil, err
	}
	return projectAgentDefinition(result.Agent)
}

var deleteReplayRevisionSentinel = time.Unix(0, 0).UTC()

const (
	lifecycleAttempts           = 2
	lifecycleRequestIDMaxLength = 128
	lifecycleBoundRequestPrefix = "reqv1-"
)

func validateLifecycleRequest(command AgentLifecycleCommand) error {
	switch command.Action {
	case agents.LifecycleEnable, agents.LifecycleDisable, agents.LifecycleDelete:
		requestID, err := normalizeLifecycleRequestID(command.RequestID)
		if err != nil {
			return fmt.Errorf("%v: %w", err, agents.ErrInvalid)
		}
		if _, bound, err := parseBoundLifecycleRequestID(requestID); err != nil || !bound {
			if err == nil {
				err = errors.New("lifecycle request id must be bound to an Agent generation")
			}
			return fmt.Errorf("%v: %w", err, agents.ErrInvalid)
		}
	default:
		return fmt.Errorf("unknown lifecycle action %q: %w", command.Action, agents.ErrInvalid)
	}
	return nil
}

func bindLifecycleRequestID(requestID, generationID string) (string, error) {
	requestID, err := normalizeLifecycleRequestID(requestID)
	if err != nil {
		return "", err
	}
	if boundGeneration, bound, parseErr := parseBoundLifecycleRequestID(requestID); bound {
		if parseErr != nil {
			return "", parseErr
		}
		if generationID != "" && generationID != boundGeneration {
			// Preserve the old generation in the token. FleetDB will reject it
			// against a replacement Agent instead of silently rebinding.
			return requestID, nil
		}
		return requestID, nil
	} else if parseErr != nil {
		return "", parseErr
	}
	if !agents.ValidGenerationID(generationID) {
		return "", errors.New("cannot bind an unbound lifecycle request without a current Agent generation")
	}
	digest := sha256.Sum256([]byte(requestID))
	return fmt.Sprintf("%s%s-%x", lifecycleBoundRequestPrefix, generationID, digest), nil
}

func parseBoundLifecycleRequestID(value string) (string, bool, error) {
	if !strings.HasPrefix(value, lifecycleBoundRequestPrefix) {
		return "", false, nil
	}
	const generationLength = 32
	const digestLength = 64
	if len(value) != len(lifecycleBoundRequestPrefix)+generationLength+1+digestLength {
		return "", true, errors.New("generation-bound lifecycle request id has an invalid length")
	}
	generationStart := len(lifecycleBoundRequestPrefix)
	generationEnd := generationStart + generationLength
	if value[generationEnd] != '-' {
		return "", true, errors.New("generation-bound lifecycle request id is malformed")
	}
	generationID := value[generationStart:generationEnd]
	if !agents.ValidGenerationID(generationID) ||
		!lowerHex(value[generationEnd+1:]) {
		return "", true, errors.New("generation-bound lifecycle request id is malformed")
	}
	return generationID, true, nil
}

func lowerHex(value string) bool {
	if value == "" {
		return false
	}
	for index := range value {
		char := value[index]
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return true
}

func normalizeLifecycleRequestID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("lifecycle request id is required")
	}
	if len(value) > lifecycleRequestIDMaxLength {
		return "", fmt.Errorf(
			"lifecycle request id must not exceed %d characters",
			lifecycleRequestIDMaxLength,
		)
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' ||
			char == '.' || char == '_' || char == ':' || char == '-' {
			continue
		}
		return "", errors.New(
			"lifecycle request id may contain only ASCII letters, digits, '.', '_', ':', and '-'",
		)
	}
	return value, nil
}

func applyManagementLifecycleWithRetry(
	ctx context.Context,
	client agentManagementClient,
	agentID string,
	request managementapi.ApplyAgentLifecycleRequest,
) (*agents.LifecycleResult, error) {
	var lastErr error
	for attempt := 0; attempt < lifecycleAttempts; attempt++ {
		result, err := client.ApplyAgentLifecycle(ctx, agentID, request)
		if err == nil && (result == nil || result.Agent == nil) {
			err = agents.ErrInvalidPersistedState
		}
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt+1 == lifecycleAttempts || !ambiguousLifecycleResponse(ctx, err) {
			break
		}
	}
	return nil, lastErr
}

func applyCanonicalLifecycleWithRetry(
	ctx context.Context,
	api canonicalAgentsAPI,
	auth authority.OperatorAuthority,
	command agents.ApplyLifecycleCommand,
) (*agents.LifecycleResult, error) {
	var lastErr error
	for attempt := 0; attempt < lifecycleAttempts; attempt++ {
		result, err := api.ApplyLifecycle(ctx, auth, command)
		if err == nil && (result == nil || result.Agent == nil) {
			err = agents.ErrInvalidPersistedState
		}
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt+1 == lifecycleAttempts || !ambiguousLifecycleResponse(ctx, err) {
			break
		}
	}
	return nil, lastErr
}

func ambiguousLifecycleResponse(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	for _, definitive := range []error{
		persistence.ErrInvalid,
		persistence.ErrNotFound,
		persistence.ErrConflict,
		agents.ErrInvalid,
		agents.ErrNotFound,
		agents.ErrAlreadyExists,
		agents.ErrConflict,
		agents.ErrNotOwner,
		agents.ErrInvalidTransition,
	} {
		if errors.Is(err, definitive) {
			return false
		}
	}
	return true
}

func lifecycleExpectedFence(
	command AgentLifecycleCommand,
	current *agents.Agent,
	getErr error,
	notFound bool,
) (time.Time, string, error) {
	expectedGenerationID, bound, err := parseBoundLifecycleRequestID(command.RequestID)
	if err != nil || !bound {
		if err == nil {
			err = errors.New("lifecycle request id is not generation-bound")
		}
		return time.Time{}, "", fmt.Errorf("%v: %w", err, agents.ErrInvalid)
	}
	if getErr == nil {
		if current == nil || current.UpdatedAt.IsZero() ||
			!agents.ValidGenerationID(current.GenerationID) {
			return time.Time{}, "", agents.ErrInvalidPersistedState
		}
		return current.UpdatedAt, expectedGenerationID, nil
	}
	if command.Action == agents.LifecycleDelete && notFound {
		// Fleet checks the stable delete receipt before live-state validation.
		// A deterministic nonzero revision satisfies request validation without
		// pretending to know the revision of an already archived Agent.
		return deleteReplayRevisionSentinel, expectedGenerationID, nil
	}
	return time.Time{}, "", getErr
}

func lifecycleIdempotencyKey(command AgentLifecycleCommand) string {
	// Every lifecycle action, including delete, is one caller-scoped operation.
	// Reusing RequestID recovers only that receipt; a fresh ID can act on a
	// replacement Agent only after the caller observes its new generation.
	parts := []string{
		command.WorkspaceKey,
		command.AgentID,
		string(command.Action),
		strings.TrimSpace(command.RequestID),
	}
	fingerprint := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("agentdef-%s-%x", command.Action, fingerprint)
}

func agentDefinitionFromCanonical(record *agents.Agent) *AgentDefinition {
	if record == nil {
		return nil
	}
	mode := ""
	if record.Kind == agents.AgentKindAlwaysOn {
		mode = "service"
	}
	return &AgentDefinition{
		WorkspaceKey:    record.WorkspaceKey,
		AgentID:         record.AgentID,
		GenerationID:    record.GenerationID,
		Name:            record.Name,
		RoleName:        record.Behavior.RoleName,
		Kind:            record.Kind,
		DesiredState:    record.DesiredState,
		Auto:            record.RestartPolicy == "always",
		Mode:            mode,
		MaxConcurrency:  record.MaxInstances,
		BudgetPolicy:    record.BudgetPolicy,
		ProfileName:     record.ProfileName,
		PlacementPolicy: record.PlacementPolicy,
		RestartPolicy:   record.RestartPolicy,
		CreatedBy:       record.CreatedBy,
		DeletedAt:       cloneTime(record.DeletedAt),
		CreatedAt:       record.CreatedAt,
		UpdatedAt:       record.UpdatedAt,
	}
}

func projectAgentDefinition(record *agents.Agent) (*AgentDefinition, error) {
	if record == nil || !agents.ValidGenerationID(record.GenerationID) {
		return nil, agents.ErrInvalidPersistedState
	}
	return agentDefinitionFromCanonical(record), nil
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
