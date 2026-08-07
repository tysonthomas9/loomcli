package managementapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/driver/nativearchive"
	"github.com/tysonthomas9/loomcli/internal/httpclient"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

const responseLimit = 8 << 20

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client is the shared standalone-CLI adapter for authenticated Loom
// management routes. It never opens Store or constructs capability authority
// locally. Open-mode clients send no credential; OIDC clients retain the
// authenticated HTTP transport configured by httpclient.
type Client struct {
	serverURL string
	workspace string
	doer      httpDoer
}

type SubmitDriverRunRequest struct {
	CLICommand      string          `json:"cli_command"`
	DriverRef       string          `json:"driver_ref"`
	DriverVersionID string          `json:"driver_version_id,omitempty"`
	RunID           string          `json:"run_id,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	Entrypoint      string          `json:"entrypoint,omitempty"`
	EpicID          string          `json:"epic_id,omitempty"`
	Payload         json.RawMessage `json:"payload"`
}

// RegisterNativeDriverRequest carries a bounded compressed dist bundle to the
// authenticated Loom management boundary. Workspace and actor are absent:
// serve derives both from the configured route and verified HTTP identity.
type RegisterNativeDriverRequest struct {
	Archive      []byte                           `json:"archive"`
	Manifest     []byte                           `json:"manifest,omitempty"`
	DriverName   string                           `json:"driver_name,omitempty"`
	DriverID     string                           `json:"driver_id,omitempty"`
	WorkflowName string                           `json:"workflow_name,omitempty"`
	SourceRef    string                           `json:"source_ref,omitempty"`
	SourceDigest string                           `json:"source_digest,omitempty"`
	Activate     bool                             `json:"activate,omitempty"`
	Trust        workflowcatalog.DriverTrustLevel `json:"trust"`
}

// CreateAgentRequest is the standalone-CLI intent sent to loom serve. The
// workspace and operator identity are deliberately absent: the configured
// management endpoint derives both at its authenticated HTTP boundary.
type CreateAgentRequest struct {
	AgentID         string                   `json:"agent_id"`
	Name            string                   `json:"name"`
	Kind            agents.AgentKind         `json:"kind"`
	Behavior        agents.BehaviorReference `json:"behavior"`
	DesiredState    agents.DesiredState      `json:"desired_state"`
	ProfileName     string                   `json:"profile_name,omitempty"`
	ScheduleID      string                   `json:"schedule_id,omitempty"`
	EventSources    []string                 `json:"event_sources,omitempty"`
	TriggerRefs     []string                 `json:"trigger_refs,omitempty"`
	PlacementPolicy string                   `json:"placement_policy,omitempty"`
	MaxInstances    int                      `json:"max_instances"`
	LeaseID         string                   `json:"lease_id,omitempty"`
	RestartPolicy   string                   `json:"restart_policy,omitempty"`
	Permissions     []string                 `json:"permissions,omitempty"`
	BudgetPolicy    string                   `json:"budget_policy,omitempty"`
	StateRef        string                   `json:"state_ref,omitempty"`
	Metadata        map[string]string        `json:"metadata,omitempty"`
}

type ArchiveAgentRequest struct {
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

type SetAgentDesiredStateRequest struct {
	ExpectedState     agents.DesiredState `json:"expected_state"`
	DesiredState      agents.DesiredState `json:"desired_state"`
	ExpectedUpdatedAt time.Time           `json:"expected_updated_at"`
}

type ApplyAgentLifecycleRequest struct {
	Action               agents.LifecycleAction `json:"action"`
	ExpectedUpdatedAt    time.Time              `json:"expected_updated_at"`
	ExpectedGenerationID string                 `json:"expected_generation_id,omitempty"`
	IdempotencyKey       string                 `json:"idempotency_key"`
}

type UpdateRoleRequest struct {
	Kind                *string   `json:"kind,omitempty"`
	Description         *string   `json:"description,omitempty"`
	Prompt              *string   `json:"prompt,omitempty"`
	PromptFile          *string   `json:"prompt_file,omitempty"`
	Model               *string   `json:"model,omitempty"`
	TaskFilter          *string   `json:"task_filter,omitempty"`
	Backend             *string   `json:"backend,omitempty"`
	Effort              *string   `json:"effort,omitempty"`
	PathPatterns        *[]string `json:"path_patterns,omitempty"`
	Skills              *[]string `json:"skills,omitempty"`
	MaxPriority         *int      `json:"max_priority,omitempty"`
	ClearMaxPriority    bool      `json:"clear_max_priority,omitempty"`
	MaxConcurrency      *int      `json:"max_concurrency,omitempty"`
	ClearConcurrency    bool      `json:"clear_max_concurrency,omitempty"`
	ReadOnly            *bool     `json:"read_only,omitempty"`
	AllowedTools        *[]string `json:"allowed_tools,omitempty"`
	DeniedTools         *[]string `json:"denied_tools,omitempty"`
	MaxBudgetUSD        *float64  `json:"max_budget_usd,omitempty"`
	ClearMaxBudgetUSD   bool      `json:"clear_max_budget_usd,omitempty"`
	PersistInlinePrompt bool      `json:"persist_inline_prompt,omitempty"`
}

func New(_ context.Context, purpose string) (*Client, error) {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		purpose = "Loom management command"
	}
	serverURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LOOM_SERVER_URL")), "/")
	if serverURL == "" {
		return nil, fmt.Errorf("%s requires --server or LOOM_SERVER_URL", purpose)
	}
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid Loom management server URL %q", serverURL)
	}
	workspace := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
	if workspace == "" {
		return nil, fmt.Errorf("%s requires --workspace or LOOM_WORKSPACE", purpose)
	}
	authClient, err := httpclient.New(httpclient.Config{
		ServerURL: serverURL,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%s endpoint discovery: %w", purpose, err)
	}
	client := &Client{serverURL: serverURL, workspace: workspace, doer: authClient}
	return client, nil
}

func (client *Client) Workspace() string {
	if client == nil {
		return ""
	}
	return client.workspace
}

func (client *Client) workspacePath(suffix string) string {
	return "/api/workspaces/" + url.PathEscape(client.workspace) + suffix
}

func (client *Client) SubmitDriverRun(ctx context.Context, request SubmitDriverRunRequest) (*domain.DriverRun, error) {
	var run domain.DriverRun
	if err := client.doJSON(ctx, http.MethodPost, client.workspacePath("/execution/driver-runs"), request, &run); err != nil {
		return nil, err
	}
	if strings.TrimSpace(run.RunID) == "" {
		return nil, errors.New("loom management API returned no DriverRun")
	}
	return &run, nil
}

func (client *Client) RegisterNativeDriver(
	ctx context.Context,
	request RegisterNativeDriverRequest,
) (*driver.RegisterFlueResult, error) {
	if err := nativearchive.ValidateArchiveSize(len(request.Archive)); err != nil {
		return nil, fmt.Errorf("%v: %w", err, domain.ErrInvalid)
	}
	if err := nativearchive.ValidateManifestSize(len(request.Manifest)); err != nil {
		return nil, fmt.Errorf("%v: %w", err, domain.ErrInvalid)
	}
	var result driver.RegisterFlueResult
	if err := client.doJSON(
		ctx,
		http.MethodPost,
		client.workspacePath("/workflow-catalog/native-drivers"),
		request,
		&result,
	); err != nil {
		return nil, err
	}
	if result.Driver == nil || result.Version == nil ||
		result.Driver.WorkspaceKey != client.workspace ||
		result.Version.WorkspaceKey != client.workspace ||
		strings.TrimSpace(result.Driver.DriverID) == "" ||
		strings.TrimSpace(result.Version.VersionID) == "" ||
		result.Version.DriverID != result.Driver.DriverID {
		return nil, errors.New("loom management API returned an invalid native DriverVersion")
	}
	return &result, nil
}

func (client *Client) GetDriverRun(ctx context.Context, runID string) (*domain.DriverRun, error) {
	var run domain.DriverRun
	if err := client.doJSON(ctx, http.MethodGet, client.workspacePath("/runs/"+url.PathEscape(strings.TrimSpace(runID))), nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (client *Client) CreateWorkerProfile(ctx context.Context, command execution.CreateWorkerProfileCommand) (*execution.WorkerProfile, error) {
	profileID := strings.TrimSpace(command.ProfileID)
	if profileID == "" {
		return nil, fmt.Errorf("worker profile id is required: %w", domain.ErrInvalid)
	}
	command.ProfileID = profileID
	var profile execution.WorkerProfile
	if err := client.doJSON(ctx, http.MethodPost, client.workspacePath("/execution/worker-profiles"), command, &profile); err != nil {
		return nil, err
	}
	if profile.ProfileID != profileID || profile.WorkspaceKey != client.workspace {
		return nil, errors.New("loom management API returned an invalid WorkerProfile identity")
	}
	return &profile, nil
}

func (client *Client) UpdateWorkerProfile(ctx context.Context, profileID string, patch execution.WorkerProfilePatch) (*execution.WorkerProfile, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, fmt.Errorf("worker profile id is required: %w", domain.ErrInvalid)
	}
	var profile execution.WorkerProfile
	if err := client.doJSON(ctx, http.MethodPatch, client.workspacePath("/execution/worker-profiles/"+url.PathEscape(profileID)), patch, &profile); err != nil {
		return nil, err
	}
	if profile.ProfileID != profileID || profile.WorkspaceKey != client.workspace {
		return nil, errors.New("loom management API returned an invalid WorkerProfile identity")
	}
	return &profile, nil
}

func (client *Client) DeleteWorkerProfile(ctx context.Context, profileID string) error {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return fmt.Errorf("worker profile id is required: %w", domain.ErrInvalid)
	}
	return client.doJSON(ctx, http.MethodDelete, client.workspacePath("/execution/worker-profiles/"+url.PathEscape(profileID)), nil, nil)
}

func (client *Client) CreateAgent(ctx context.Context, command agents.CreateAgentCommand) (*agents.Agent, error) {
	if client == nil || command.WorkspaceKey != client.workspace {
		return nil, fmt.Errorf("agent workspace does not match configured management workspace: %w", domain.ErrInvalid)
	}
	request := CreateAgentRequest{
		AgentID: command.AgentID, Name: command.Name, Kind: command.Kind,
		Behavior: command.Behavior, DesiredState: command.DesiredState,
		ProfileName: command.ProfileName, ScheduleID: command.ScheduleID,
		EventSources:    append([]string(nil), command.EventSources...),
		TriggerRefs:     append([]string(nil), command.TriggerRefs...),
		PlacementPolicy: command.PlacementPolicy, MaxInstances: command.MaxInstances,
		LeaseID: command.LeaseID, RestartPolicy: command.RestartPolicy,
		Permissions:  append([]string(nil), command.Permissions...),
		BudgetPolicy: command.BudgetPolicy, StateRef: command.StateRef,
		Metadata: cloneStringMap(command.Metadata),
	}
	var record agents.Agent
	if err := client.doJSON(ctx, http.MethodPost, client.workspacePath("/agent-identities"), request, &record); err != nil {
		return nil, err
	}
	if err := validateAgentIdentity(&record, client.workspace, command.AgentID); err != nil {
		return nil, err
	}
	return &record, nil
}

func (client *Client) UpdateAgent(
	ctx context.Context,
	agentID string,
	expectedUpdatedAt time.Time,
	patch agents.AgentPatch,
) (*agents.Agent, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || expectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf("agent id and expected revision are required: %w", domain.ErrInvalid)
	}
	var record agents.Agent
	if err := client.doJSON(
		ctx,
		http.MethodPatch,
		client.workspacePath("/agent-identities/"+url.PathEscape(agentID)),
		struct {
			ExpectedUpdatedAt time.Time         `json:"expected_updated_at"`
			Patch             agents.AgentPatch `json:"patch"`
		}{ExpectedUpdatedAt: expectedUpdatedAt, Patch: patch},
		&record,
	); err != nil {
		return nil, err
	}
	if err := validateAgentIdentity(&record, client.workspace, agentID); err != nil {
		return nil, err
	}
	return &record, nil
}

func (client *Client) GetAgent(ctx context.Context, agentID string) (*agents.Agent, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("agent id is required: %w", domain.ErrInvalid)
	}
	var record agents.Agent
	if err := client.doJSON(
		ctx,
		http.MethodGet,
		client.workspacePath("/agent-identities/"+url.PathEscape(agentID)),
		nil,
		&record,
	); err != nil {
		return nil, err
	}
	if err := validateAgentIdentity(&record, client.workspace, agentID); err != nil {
		return nil, err
	}
	return &record, nil
}

func (client *Client) ListAgents(ctx context.Context) ([]*agents.Agent, error) {
	var response struct {
		Agents []*agents.Agent `json:"agents"`
	}
	if err := client.doJSON(ctx, http.MethodGet, client.workspacePath("/agent-identities"), nil, &response); err != nil {
		return nil, err
	}
	if response.Agents == nil {
		response.Agents = []*agents.Agent{}
	}
	for _, record := range response.Agents {
		if err := validateAgentIdentity(record, client.workspace, ""); err != nil {
			return nil, err
		}
	}
	return response.Agents, nil
}

func (client *Client) ArchiveAgent(
	ctx context.Context,
	agentID string,
	expectedUpdatedAt time.Time,
) (*agents.Agent, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || expectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf("agent id and expected revision are required: %w", domain.ErrInvalid)
	}
	var record agents.Agent
	if err := client.doJSON(
		ctx,
		http.MethodPost,
		client.workspacePath("/agent-identities/"+url.PathEscape(agentID)+"/archive"),
		ArchiveAgentRequest{ExpectedUpdatedAt: expectedUpdatedAt},
		&record,
	); err != nil {
		return nil, err
	}
	if err := validateAgentIdentity(&record, client.workspace, agentID); err != nil {
		return nil, err
	}
	if record.DeletedAt == nil {
		return nil, errors.New("loom management API returned an unarchived Agent")
	}
	return &record, nil
}

func (client *Client) SetAgentDesiredState(
	ctx context.Context,
	agentID string,
	request SetAgentDesiredStateRequest,
) (*agents.Agent, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || request.ExpectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf("agent id and expected revision are required: %w", domain.ErrInvalid)
	}
	var record agents.Agent
	if err := client.doJSON(
		ctx,
		http.MethodPost,
		client.workspacePath("/agent-identities/"+url.PathEscape(agentID)+"/desired-state"),
		request,
		&record,
	); err != nil {
		return nil, err
	}
	if err := validateAgentIdentity(&record, client.workspace, agentID); err != nil {
		return nil, err
	}
	if record.DesiredState != request.DesiredState {
		return nil, errors.New("loom management API returned the wrong Agent desired state")
	}
	return &record, nil
}

func (client *Client) ApplyAgentLifecycle(
	ctx context.Context,
	agentID string,
	request ApplyAgentLifecycleRequest,
) (*agents.LifecycleResult, error) {
	agentID = strings.TrimSpace(agentID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if agentID == "" || request.ExpectedUpdatedAt.IsZero() || request.IdempotencyKey == "" {
		return nil, fmt.Errorf(
			"agent id, expected revision, and idempotency key are required: %w",
			domain.ErrInvalid,
		)
	}
	switch request.Action {
	case agents.LifecycleEnable, agents.LifecycleDisable, agents.LifecycleDelete:
	default:
		return nil, fmt.Errorf("invalid agent lifecycle action %q: %w", request.Action, domain.ErrInvalid)
	}
	if request.ExpectedGenerationID == "" && request.Action != agents.LifecycleDelete {
		return nil, fmt.Errorf("agent expected generation is required: %w", domain.ErrInvalid)
	}
	if request.ExpectedGenerationID != "" &&
		!agents.ValidGenerationID(request.ExpectedGenerationID) {
		return nil, fmt.Errorf("agent expected generation is invalid: %w", domain.ErrInvalid)
	}
	var result agents.LifecycleResult
	if err := client.doJSON(
		ctx,
		http.MethodPost,
		client.workspacePath("/agent-identities/"+url.PathEscape(agentID)+"/lifecycle"),
		request,
		&result,
	); err != nil {
		return nil, err
	}
	if result.WorkspaceKey != client.workspace ||
		result.AgentID != agentID ||
		result.Action != request.Action ||
		result.IdempotencyKey != request.IdempotencyKey ||
		result.Agent == nil {
		return nil, errors.New("loom management API returned invalid Agent lifecycle coordinates")
	}
	if err := validateAgentIdentity(result.Agent, client.workspace, agentID); err != nil {
		return nil, err
	}
	if request.ExpectedGenerationID != "" &&
		result.Agent.GenerationID != request.ExpectedGenerationID {
		return nil, errors.New("loom management API returned the wrong Agent generation")
	}
	return &result, nil
}

func (client *Client) CreateRole(
	ctx context.Context,
	definition agents.RoleDefinition,
) (*agents.Role, error) {
	if strings.TrimSpace(definition.Name) == "" {
		return nil, fmt.Errorf("role name is required: %w", domain.ErrInvalid)
	}
	var role agents.Role
	request := struct {
		agents.RoleDefinition
		PersistInlinePrompt bool `json:"persist_inline_prompt"`
	}{RoleDefinition: definition, PersistInlinePrompt: true}
	if err := client.doJSON(ctx, http.MethodPost, client.workspacePath("/roles"), request, &role); err != nil {
		return nil, err
	}
	if err := validateRole(&role, client.workspace, definition.Name); err != nil {
		return nil, err
	}
	return &role, nil
}

//nolint:funlen // Role patch encoding preserves every tri-state field and response classification in one wire-contract method.
func (client *Client) UpdateRole(
	ctx context.Context,
	roleName string,
	patch agents.RolePatch,
) (*agents.Role, error) {
	roleName = strings.TrimSpace(roleName)
	if roleName == "" {
		return nil, fmt.Errorf("role name is required: %w", domain.ErrInvalid)
	}
	request := UpdateRoleRequest{
		Kind: patch.Kind, Description: patch.Description, Prompt: patch.Prompt,
		PromptFile: patch.PromptFile, Model: patch.Model, TaskFilter: patch.TaskFilter,
		Backend: patch.Backend, Effort: patch.Effort, PathPatterns: patch.PathPatterns,
		Skills: patch.Skills, ReadOnly: patch.ReadOnly, AllowedTools: patch.AllowedTools,
		DeniedTools:         patch.DeniedTools,
		PersistInlinePrompt: patch.Prompt != nil,
	}
	if patch.MaxPriority != nil {
		if *patch.MaxPriority == nil {
			request.ClearMaxPriority = true
		} else {
			request.MaxPriority = *patch.MaxPriority
		}
	}
	if patch.MaxConcurrency != nil {
		if *patch.MaxConcurrency == nil {
			request.ClearConcurrency = true
		} else {
			request.MaxConcurrency = *patch.MaxConcurrency
		}
	}
	if patch.MaxBudgetUSD != nil {
		if *patch.MaxBudgetUSD == nil {
			request.ClearMaxBudgetUSD = true
		} else {
			request.MaxBudgetUSD = *patch.MaxBudgetUSD
		}
	}
	var response struct {
		Role *agents.Role `json:"role"`
	}
	if err := client.doJSON(
		ctx,
		http.MethodPatch,
		client.workspacePath("/roles/"+url.PathEscape(roleName)),
		request,
		&response,
	); err != nil {
		return nil, err
	}
	if err := validateRole(response.Role, client.workspace, roleName); err != nil {
		return nil, err
	}
	return response.Role, nil
}

func (client *Client) DeleteRole(ctx context.Context, roleName string) error {
	roleName = strings.TrimSpace(roleName)
	if roleName == "" {
		return fmt.Errorf("role name is required: %w", domain.ErrInvalid)
	}
	return client.doJSON(
		ctx,
		http.MethodDelete,
		client.workspacePath("/roles/"+url.PathEscape(roleName)),
		nil,
		nil,
	)
}

func validateRole(role *agents.Role, workspace, roleName string) error {
	if role == nil || strings.TrimSpace(role.WorkspaceKey) == "" ||
		role.WorkspaceKey != workspace || strings.TrimSpace(role.Name) == "" ||
		role.Name != roleName {
		return errors.New("loom management API returned an invalid Role")
	}
	return nil
}

func validateAgentIdentity(record *agents.Agent, workspace, agentID string) error {
	if record == nil || strings.TrimSpace(record.WorkspaceKey) == "" ||
		record.WorkspaceKey != workspace || strings.TrimSpace(record.AgentID) == "" ||
		!agents.ValidGenerationID(record.GenerationID) ||
		(agentID != "" && record.AgentID != agentID) {
		return errors.New("loom management API returned an invalid Agent identity")
	}
	return nil
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func (client *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
	if client == nil || client.doer == nil {
		return errors.New("loom management client is unavailable")
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode Loom management request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, client.serverURL+path, body)
	if err != nil {
		return fmt.Errorf("build Loom management request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.doer.Do(req)
	if err != nil {
		return fmt.Errorf("loom management endpoint unavailable at %s: %w", client.serverURL, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return fmt.Errorf("read Loom management response: %w", err)
	}
	if len(data) > responseLimit {
		return fmt.Errorf("loom management response exceeds %d bytes", responseLimit)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return statusError(response.StatusCode, data)
	}
	if output == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode Loom management response: %w", err)
	}
	return nil
}

func statusError(status int, data []byte) error {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	_ = json.Unmarshal(data, &payload)
	message := strings.TrimSpace(payload.Error)
	if message == "" {
		message = strings.TrimSpace(payload.Message)
	}
	if message == "" {
		message = strings.TrimSpace(string(data))
	}
	if message == "" {
		message = http.StatusText(status)
	}
	detail := fmt.Sprintf("Loom management API HTTP %d: %s", status, message)
	switch status {
	case http.StatusBadRequest, http.StatusPreconditionRequired, http.StatusPreconditionFailed:
		return fmt.Errorf("%s: %w", detail, domain.ErrInvalid)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", detail, domain.ErrNotFound)
	case http.StatusConflict:
		return fmt.Errorf("%s: %w", detail, domain.ErrConflict)
	case http.StatusUnauthorized:
		return errors.New("Loom management API unauthorized: " + detail)
	case http.StatusForbidden:
		return errors.New("Loom management API forbidden: " + detail)
	default:
		return errors.New(detail)
	}
}
