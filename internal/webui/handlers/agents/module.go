package agents

import (
	"context"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/sessioncoord"
)

// BindingGrantCompatibility is the narrow Phase-3 connector seam needed by
// agent deletion. Grant creation remains owned by the existing connector store
// until the Connectors capability moves in Phase 5.
type BindingGrantCompatibility interface {
	RevokeBindingGrants(ctx context.Context, workspaceKey, bindingID string) (int, error)
}

type agentSessionTranscriptEvents = sessioncoord.TranscriptEvents

// Config composes the unified transport with the canonical Agents identity
// surface and Automation bindings. The composite Store remains for
// prompt-agent build/provisioning projections, connector
// compatibility, and run history; public durable Agent identity reads and
// mutations never use store.AgentServices directly.
type Config struct {
	AgentRecords          AgentRecordAPI
	AgentIdentityCreator  CanonicalInteractiveAgentAPI
	InteractiveRuntime    agentcoord.InteractiveAgentRuntime
	AgentRecordAuthority  workflowcataloghttp.OperatorAuthorityResolver
	SessionTranscripts    sessioncoord.AgentSessionTranscriptService
	Store                 agentProjectionStore
	Hub                   *realtime.Hub
	Bindings              automation.BindingOperations
	OperatorAuthority     workflowcataloghttp.OperatorAuthorityResolver
	Provisioning          agentprovisioning.Commands
	ProvisioningAuthority workflowcataloghttp.OperatorAuthorityResolver
	PrepareWorkflowTarget func(context.Context, string, string) (*workflowcatalog.Driver, error)
	WorkspaceFromContext  func(context.Context) string
	BindingGrants         BindingGrantCompatibility
}

type agentProjectionStore interface {
	AgentServices() store.AgentServiceStore
	Roles() store.RoleStore
	DriverRuns() store.DriverRunStore
}

// Module registers fleet-db-backed agent assignment routes.
type Module struct {
	agentRecords          AgentRecordAPI
	agentIdentityCreator  CanonicalInteractiveAgentAPI
	agentRoleQueries      agentsmodule.RoleQueries
	interactiveRuntime    agentcoord.InteractiveAgentRuntime
	agentRecordAuthority  workflowcataloghttp.OperatorAuthorityResolver
	sessionTranscripts    sessioncoord.AgentSessionTranscriptService
	store                 agentProjectionStore
	hub                   *realtime.Hub
	bindings              automation.BindingOperations
	operatorAuthority     workflowcataloghttp.OperatorAuthorityResolver
	provisioning          agentprovisioning.Commands
	provisioningAuthority workflowcataloghttp.OperatorAuthorityResolver
	prepareWorkflowTarget func(context.Context, string, string) (*workflowcatalog.Driver, error)
	agentLifecycle        AgentLifecycleAPI
	workspaceFromContext  func(context.Context) string
	bindingGrants         BindingGrantCompatibility
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
		store:                config.Store, hub: config.Hub,
		bindings: config.Bindings, operatorAuthority: config.OperatorAuthority,
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
	if m.agentRecords == nil && m.store == nil && m.sessionTranscripts == nil {
		return
	}
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
	var svcErr *apperrors.ServiceError
	status := http.StatusInternalServerError
	message := "internal server error"
	if errors.As(err, &svcErr) {
		status = handler.StatusForKind(svcErr.Kind)
		message = svcErr.Message
	}
	handler.WriteJSON(w, status, agentSessionTranscriptResponse{
		Success: false,
		Error:   message,
	})
}
