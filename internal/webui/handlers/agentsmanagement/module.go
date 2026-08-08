// Package agentsmanagement exposes the authenticated standalone-management
// surface for the Phase 5 Agents capability. Bodies carry operator intent and
// optimistic revisions only; workspace scope and authority are server-derived.
package agentsmanagement

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	serverhandler "github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const maxRequestBytes = 1 << 20

type Config struct {
	Agents    IdentityAPI
	Authority workflowcataloghttp.OperatorAuthorityResolver
}

type IdentityAPI interface {
	agents.IdentityQueries
	agents.IdentityCommands
	agents.LifecycleCommands
}

type Module struct {
	agents    IdentityAPI
	authority workflowcataloghttp.OperatorAuthorityResolver
}

func New(config Config) *Module {
	return &Module{agents: config.Agents, authority: config.Authority}
}

func (module *Module) Register(mux *http.ServeMux) {
	if module == nil || mux == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/agent-identities", module.create)
	mux.HandleFunc("GET /api/workspaces/{ws}/agent-identities", module.list)
	mux.HandleFunc("GET /api/workspaces/{ws}/agent-identities/{agentId}", module.get)
	mux.HandleFunc("PATCH /api/workspaces/{ws}/agent-identities/{agentId}", module.update)
	mux.HandleFunc("POST /api/workspaces/{ws}/agent-identities/{agentId}/archive", module.archive)
	mux.HandleFunc("POST /api/workspaces/{ws}/agent-identities/{agentId}/desired-state", module.setDesiredState)
	mux.HandleFunc("POST /api/workspaces/{ws}/agent-identities/{agentId}/lifecycle", module.applyLifecycle)
}

type createRequest struct {
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

type updateRequest struct {
	ExpectedUpdatedAt time.Time         `json:"expected_updated_at"`
	Patch             agents.AgentPatch `json:"patch"`
}

type archiveRequest struct {
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

type desiredStateRequest struct {
	ExpectedState     agents.DesiredState `json:"expected_state"`
	DesiredState      agents.DesiredState `json:"desired_state"`
	ExpectedUpdatedAt time.Time           `json:"expected_updated_at"`
}

type lifecycleRequest struct {
	Action               agents.LifecycleAction `json:"action"`
	ExpectedUpdatedAt    time.Time              `json:"expected_updated_at"`
	ExpectedGenerationID string                 `json:"expected_generation_id,omitempty"`
	IdempotencyKey       string                 `json:"idempotency_key"`
}

func (module *Module) create(response http.ResponseWriter, request *http.Request) {
	var input createRequest
	workspace, ok := canonicalWorkspace(response, request)
	if !ok {
		return
	}
	if err := decodeOneObject(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	auth, ok := module.resolveOperator(response, request, workspace, agents.ActionCreateAgent)
	if !ok {
		return
	}
	if module.agents == nil {
		writeMappedError(response, agents.ErrUnavailable)
		return
	}
	record, err := module.agents.CreateAgent(request.Context(), auth, agents.CreateAgentCommand{
		WorkspaceKey: workspace, AgentID: input.AgentID, Name: input.Name, Kind: input.Kind,
		Behavior: input.Behavior, DesiredState: input.DesiredState,
		ProfileName: input.ProfileName, ScheduleID: input.ScheduleID,
		EventSources:    append([]string(nil), input.EventSources...),
		TriggerRefs:     append([]string(nil), input.TriggerRefs...),
		PlacementPolicy: input.PlacementPolicy, MaxInstances: input.MaxInstances,
		LeaseID: input.LeaseID, RestartPolicy: input.RestartPolicy,
		Permissions:  append([]string(nil), input.Permissions...),
		BudgetPolicy: input.BudgetPolicy, StateRef: input.StateRef,
		Metadata: cloneMap(input.Metadata),
	})
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, record)
}

func (module *Module) update(response http.ResponseWriter, request *http.Request) {
	workspace, agentID, ok := routeIdentity(response, request)
	if !ok {
		return
	}
	var input updateRequest
	if err := decodeOneObject(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	auth, ok := module.resolveOperator(response, request, workspace, agents.ActionUpdateAgent)
	if !ok {
		return
	}
	if module.agents == nil {
		writeMappedError(response, agents.ErrUnavailable)
		return
	}
	record, err := module.agents.UpdateAgent(request.Context(), auth, agents.UpdateAgentCommand{
		WorkspaceKey: workspace, AgentID: agentID,
		ExpectedUpdatedAt: input.ExpectedUpdatedAt, Patch: input.Patch,
	})
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, record)
}

func (module *Module) list(response http.ResponseWriter, request *http.Request) {
	workspace, ok := canonicalWorkspace(response, request)
	if !ok {
		return
	}
	if module.agents == nil {
		writeMappedError(response, agents.ErrUnavailable)
		return
	}
	records, err := module.agents.ListAgents(request.Context(), workspace, agents.AgentFilter{})
	if err != nil {
		writeMappedError(response, err)
		return
	}
	if records == nil {
		records = []*agents.Agent{}
	}
	writeJSON(response, http.StatusOK, map[string]any{"agents": records, "count": len(records)})
}

func (module *Module) get(response http.ResponseWriter, request *http.Request) {
	workspace, agentID, ok := routeIdentity(response, request)
	if !ok {
		return
	}
	if module.agents == nil {
		writeMappedError(response, agents.ErrUnavailable)
		return
	}
	record, err := module.agents.GetAgent(request.Context(), workspace, agentID)
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, record)
}

func (module *Module) archive(response http.ResponseWriter, request *http.Request) {
	workspace, agentID, ok := routeIdentity(response, request)
	if !ok {
		return
	}
	var input archiveRequest
	if err := decodeOneObject(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	auth, ok := module.resolveOperator(response, request, workspace, agents.ActionArchiveAgent)
	if !ok {
		return
	}
	if module.agents == nil {
		writeMappedError(response, agents.ErrUnavailable)
		return
	}
	record, err := module.agents.ArchiveAgent(request.Context(), auth, agents.ArchiveAgentCommand{
		WorkspaceKey: workspace, AgentID: agentID, ExpectedUpdatedAt: input.ExpectedUpdatedAt,
	})
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, record)
}

func (module *Module) setDesiredState(response http.ResponseWriter, request *http.Request) {
	workspace, agentID, ok := routeIdentity(response, request)
	if !ok {
		return
	}
	var input desiredStateRequest
	if err := decodeOneObject(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	auth, ok := module.resolveOperator(response, request, workspace, agents.ActionSetDesiredState)
	if !ok {
		return
	}
	if module.agents == nil {
		writeMappedError(response, agents.ErrUnavailable)
		return
	}
	record, err := module.agents.SetDesiredState(request.Context(), auth, agents.SetDesiredStateCommand{
		WorkspaceKey: workspace, AgentID: agentID, ExpectedState: input.ExpectedState,
		DesiredState: input.DesiredState, ExpectedUpdatedAt: input.ExpectedUpdatedAt,
	})
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, record)
}

func (module *Module) applyLifecycle(response http.ResponseWriter, request *http.Request) {
	workspace, agentID, ok := routeIdentity(response, request)
	if !ok {
		return
	}
	var input lifecycleRequest
	if err := decodeOneObject(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	auth, ok := module.resolveOperator(response, request, workspace, agents.ActionApplyLifecycle)
	if !ok {
		return
	}
	if module.agents == nil {
		writeMappedError(response, agents.ErrUnavailable)
		return
	}
	result, err := module.agents.ApplyLifecycle(request.Context(), auth, agents.ApplyLifecycleCommand{
		WorkspaceKey: workspace, AgentID: agentID, Action: input.Action,
		ExpectedUpdatedAt:    input.ExpectedUpdatedAt,
		ExpectedGenerationID: input.ExpectedGenerationID,
		IdempotencyKey:       input.IdempotencyKey,
	})
	if err != nil {
		writeMappedError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (module *Module) resolveOperator(
	response http.ResponseWriter,
	request *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, bool) {
	if module == nil || module.authority == nil {
		writeMappedError(response, agents.ErrUnavailable)
		return authority.OperatorAuthority{}, false
	}
	auth, err := module.authority.ResolveOperatorAuthority(request, workspace, action)
	if err != nil {
		writeMappedError(response, err)
		return authority.OperatorAuthority{}, false
	}
	return auth, true
}

func routeIdentity(response http.ResponseWriter, request *http.Request) (string, string, bool) {
	workspace, ok := canonicalWorkspace(response, request)
	if !ok {
		return "", "", false
	}
	agentID := strings.TrimSpace(request.PathValue("agentId"))
	if agentID == "" {
		writeError(response, http.StatusBadRequest, "invalid", "agent id is required")
		return "", "", false
	}
	return workspace, agentID, true
}

func canonicalWorkspace(response http.ResponseWriter, request *http.Request) (string, bool) {
	if request == nil {
		writeError(response, http.StatusBadRequest, "invalid", "canonical workspace is required")
		return "", false
	}
	workspace := strings.TrimSpace(middleware.WorkspaceFromContext(request.Context()))
	if workspace == "" {
		writeError(response, http.StatusBadRequest, "invalid", "canonical workspace is required")
		return "", false
	}
	return workspace, true
}

func decodeOneObject(response http.ResponseWriter, request *http.Request, output any) error {
	err := serverhandler.DecodeOneJSON(response, request, output, serverhandler.JSONDecodeOptions{
		MaxBytes: maxRequestBytes, DisallowUnknownFields: true,
	})
	if errors.Is(err, serverhandler.ErrTrailingJSON) {
		return errors.New("agent request must contain exactly one JSON object")
	}
	if err != nil {
		return errors.New("invalid Agent JSON: " + err.Error())
	}
	return nil
}

func writeMappedError(response http.ResponseWriter, err error) {
	if classification, ok := serverhandler.ClassifyAuthenticationAuthorityError(err); ok {
		message := "operator authentication required"
		if classification.Status == http.StatusForbidden {
			message = "operator is not allowed to manage this workspace"
		}
		writeError(response, classification.Status, classification.Code, message)
		return
	}
	switch {
	case errors.Is(err, agents.ErrInvalid):
		writeError(response, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, agents.ErrNotFound):
		writeError(response, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, agents.ErrAlreadyExists),
		errors.Is(err, agents.ErrConflict),
		errors.Is(err, agents.ErrInvalidTransition),
		errors.Is(err, agents.ErrNotOwner):
		writeError(response, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, agents.ErrUnavailable):
		writeError(response, http.StatusServiceUnavailable, "unavailable", err.Error())
	default:
		writeError(response, http.StatusInternalServerError, "internal", "Agent management failed")
	}
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]string{"code": code, "error": message})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func cloneMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
