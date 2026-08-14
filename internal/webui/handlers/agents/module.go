package agents

import (
	"context"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/app/query/sessionarchive"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// BindingGrantCleanup is the consumer-owned Connectors intent needed by Agent
// deletion. It cannot enumerate grants or reach persistence directly.
type BindingGrantCleanup interface {
	RevokeBindingGrants(context.Context, string, string) (int, error)
}

type agentSessionTranscriptEvents = sessionarchive.TranscriptEvents

// Config composes the unified transport with capability-owned interfaces.
type Config struct {
	AgentRecords          AgentRecordAPI
	AgentIdentityCreator  CanonicalInteractiveAgentAPI
	InteractiveRuntime    agentcoord.InteractiveAgentRuntime
	AgentRecordAuthority  OperatorAuthorityResolver
	SessionTranscripts    sessionarchive.AgentSessionTranscriptService
	AgentRuns             AgentRunQueries
	Hub                   *realtime.Hub
	Bindings              automation.BindingOperations
	BindingRuns           triggerbindings.RunQueries
	OperatorAuthority     OperatorAuthorityResolver
	Provisioning          agentprovisioning.Commands
	ProvisioningAuthority OperatorAuthorityResolver
	PrepareWorkflowTarget func(context.Context, string, string) (*workflowcatalog.Driver, error)
	WorkspaceFromContext  func(context.Context) string
	BindingGrants         BindingGrantCleanup
}

// AgentRunQueries is the read-only Execution projection needed by Agent run
// history. The handler cannot reach Execution persistence or lifecycle commands.
type AgentRunQueries interface {
	ListDriverRuns(context.Context, execution.DriverRunQuery) ([]*execution.DriverRun, error)
}

// Module registers fleet-db-backed agent assignment routes.
type Module struct {
	agentRecords          AgentRecordAPI
	agentIdentityCreator  CanonicalInteractiveAgentAPI
	agentRoleQueries      agentsmodule.RoleQueries
	interactiveRuntime    agentcoord.InteractiveAgentRuntime
	agentRecordAuthority  OperatorAuthorityResolver
	sessionTranscripts    sessionarchive.AgentSessionTranscriptService
	agentRuns             AgentRunQueries
	hub                   *realtime.Hub
	bindings              automation.BindingOperations
	bindingRuns           triggerbindings.RunQueries
	operatorAuthority     OperatorAuthorityResolver
	provisioning          agentprovisioning.Commands
	provisioningAuthority OperatorAuthorityResolver
	prepareWorkflowTarget func(context.Context, string, string) (*workflowcatalog.Driver, error)
	agentLifecycle        AgentLifecycleAPI
	workspaceFromContext  func(context.Context) string
	bindingGrants         BindingGrantCleanup
}

// OperatorAuthorityResolver is the request-scoped authority seam consumed by
// Agents. Keeping it here avoids coupling this delivery adapter to Workflow
// Catalog's HTTP adapter package.
type OperatorAuthorityResolver interface {
	ResolveOperatorAuthority(*http.Request, string, authority.Action) (authority.OperatorAuthority, error)
}

// New constructs the Automation-aware agent HTTP module.
func New(config Config) *Module {
	lifecycle, _ := config.AgentRecords.(AgentLifecycleAPI)
	roleQueries, _ := config.AgentRecords.(agentsmodule.RoleQueries)
	identityCreator := config.AgentIdentityCreator
	if identityCreator == nil {
		identityCreator, _ = config.AgentRecords.(CanonicalInteractiveAgentAPI)
	}
	return &Module{
		agentRecords:         config.AgentRecords,
		agentIdentityCreator: identityCreator,
		agentRoleQueries:     roleQueries,
		interactiveRuntime:   config.InteractiveRuntime,
		agentRecordAuthority: config.AgentRecordAuthority,
		sessionTranscripts:   config.SessionTranscripts,
		agentRuns:            config.AgentRuns, hub: config.Hub,
		bindings: config.Bindings, bindingRuns: config.BindingRuns, operatorAuthority: config.OperatorAuthority,
		provisioning: config.Provisioning, provisioningAuthority: config.ProvisioningAuthority,
		prepareWorkflowTarget: config.PrepareWorkflowTarget,
		agentLifecycle:        lifecycle,
		workspaceFromContext:  config.WorkspaceFromContext, bindingGrants: config.BindingGrants,
	}
}

// AgentRecordAPI is the narrow canonical Agents surface used by the unified
// WebUI agent routes. Prompt-agent creation remains owned by
// AgentProvisioning; the public record routes only query identity, patch
// identity, archive, and change desired state.
type AgentRecordAPI interface {
	agentsmodule.IdentityQueries
	UpdateAgent(context.Context, authority.OperatorAuthority, agentsmodule.UpdateAgentCommand) (*agentsmodule.Agent, error)
	ArchiveAgent(context.Context, authority.OperatorAuthority, agentsmodule.ArchiveAgentCommand) (*agentsmodule.Agent, error)
	SetDesiredState(context.Context, authority.OperatorAuthority, agentsmodule.SetDesiredStateCommand) (*agentsmodule.Agent, error)
}

type CanonicalInteractiveAgentAPI interface {
	CreateAgent(context.Context, authority.OperatorAuthority, agentsmodule.CreateAgentCommand) (*agentsmodule.Agent, error)
	GetRole(context.Context, string, string) (*agentsmodule.Role, error)
	CreateRole(context.Context, authority.OperatorAuthority, agentsmodule.CreateRoleCommand) (*agentsmodule.Role, error)
}

type AgentLifecycleAPI interface {
	ApplyLifecycle(context.Context, authority.OperatorAuthority, agentsmodule.ApplyLifecycleCommand) (*agentsmodule.LifecycleResult, error)
}

func (m *Module) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workspaces/{ws}/interactive-prompts", HandleInteractivePrompts())
	mux.HandleFunc("GET /api/workspaces/{ws}/agents", m.listAgents)
	mux.HandleFunc("POST /api/workspaces/{ws}/agents", m.createAgent)
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}", m.getAgent)
	mux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}", m.patchAgent)
	mux.HandleFunc("DELETE /api/workspaces/{ws}/agents/{name}", m.deleteAgent)

	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", m.handleCanonicalLifecycle("stop"))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/start", m.handleCanonicalLifecycle("start"))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/restart", m.handleCanonicalLifecycle("restart"))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{id}/enable", m.setRecordEnabled(true))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{id}/disable", m.setRecordEnabled(false))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{id}/runs", m.listAgentRuns)
	mux.HandleFunc(
		"GET /api/workspaces/{ws}/agents/{id}/sessions/{session_id}/transcript",
		m.getAgentSessionTranscript,
	)
}

func writeAgentSessionTranscriptServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, sessionarchive.ErrInvalid) || errors.Is(err, sessionarchive.ErrNotFound) ||
		errors.Is(err, sessionarchive.ErrUnavailable) || errors.Is(err, sessionarchive.ErrInvalidPersistedState) {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, sessionarchive.ErrInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, sessionarchive.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, sessionarchive.ErrUnavailable):
			status = http.StatusServiceUnavailable
		}
		handler.WriteJSON(w, status, loomapi.TranscriptResponse{
			Success: false,
			Error:   optionalAgentRecordString(sessionarchive.PublicErrorMessage(err)),
		})
		return
	}
	var svcErr *apperrors.ServiceError
	status := http.StatusInternalServerError
	message := "internal server error"
	if errors.As(err, &svcErr) {
		status = handler.StatusForKind(svcErr.Kind)
		message = svcErr.Message
	}
	handler.WriteJSON(w, status, loomapi.TranscriptResponse{
		Success: false,
		Error:   optionalAgentRecordString(message),
	})
}
