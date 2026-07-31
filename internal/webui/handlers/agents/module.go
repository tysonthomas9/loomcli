package agents

import (
	"context"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/domain"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// BindingGrantCompatibility is the narrow Phase-3 connector seam needed by
// agent deletion. Grant creation remains owned by the existing connector store
// until the Connectors capability moves in Phase 5.
type BindingGrantCompatibility interface {
	RevokeBindingGrants(ctx context.Context, workspaceKey, bindingID string) (int, error)
}

type agentSessionTranscriptEvents = service.TranscriptEvents

// Config composes the unified transport with the canonical Agents identity
// surface and Automation bindings. The composite Store remains for supervised
// legacy assignments, prompt-agent build/provisioning projections, connector
// compatibility, and run history; public durable Agent identity reads and
// mutations never use store.AgentServices directly.
type Config struct {
	AgentService          service.AgentService
	AgentRecords          AgentRecordAPI
	AgentRecordAuthority  workflowcataloghttp.OperatorAuthorityResolver
	SupervisedAuthority   SupervisedAuthorityContext
	TaskRunHistory        AgentTaskRunHistoryReader
	SessionTranscripts    service.AgentSessionTranscriptService
	Store                 store.Store
	Hub                   *realtime.Hub
	Bindings              automation.BindingOperations
	OperatorAuthority     workflowcataloghttp.OperatorAuthorityResolver
	Provisioning          agentprovisioning.Commands
	ProvisioningAuthority workflowcataloghttp.OperatorAuthorityResolver
	PrepareWorkflowTarget func(context.Context, string, string) (*workflowcatalog.Driver, error)
	WorkspaceFromContext  func(context.Context) string
	BindingGrants         BindingGrantCompatibility
}

// Module registers fleet-db-backed agent assignment routes.
type Module struct {
	agentSvc              service.AgentService
	agentRecords          AgentRecordAPI
	agentRecordAuthority  workflowcataloghttp.OperatorAuthorityResolver
	supervisedAuthority   SupervisedAuthorityContext
	taskRunHistory        AgentTaskRunHistoryReader
	sessionTranscripts    service.AgentSessionTranscriptService
	store                 store.Store
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
	supervisedAuthority := config.SupervisedAuthority
	if supervisedAuthority == nil {
		supervisedAuthority = service.WithAgentOperatorAuthority
	}
	taskRunHistory := config.TaskRunHistory
	if taskRunHistory == nil && config.Store != nil && config.Store.TaskRuns() != nil {
		taskRuns := config.Store.TaskRuns()
		taskRunHistory = func(
			ctx context.Context,
			workspace,
			agentID string,
		) ([]*domain.TaskRun, error) {
			return taskRuns.List(ctx, workspace, store.TaskRunFilter{
				WorkerProfileID: agentID,
			})
		}
	}
	return &Module{
		agentSvc: config.AgentService, agentRecords: config.AgentRecords,
		agentRecordAuthority: config.AgentRecordAuthority,
		supervisedAuthority:  supervisedAuthority,
		taskRunHistory:       taskRunHistory,
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

type AgentLifecycleAPI interface {
	ApplyLifecycle(context.Context, authority.OperatorAuthority, agentsmodule.ApplyLifecycleCommand) (*agentsmodule.LifecycleResult, error)
}

// NewModule is retained for non-binding legacy/test composition. Binding reads
// and writes fail closed until callers migrate to New(Config).
func NewModule(agentSvc service.AgentService, st store.Store, hub *realtime.Hub) *Module {
	return New(Config{AgentService: agentSvc, Store: st, Hub: hub})
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.agentSvc == nil && m.store == nil && m.sessionTranscripts == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/interactive-prompts", HandleInteractivePrompts())
	mux.HandleFunc("GET /api/workspaces/{ws}/agents", m.listAgents)
	mux.HandleFunc("POST /api/workspaces/{ws}/agents", m.createAgent)
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}", m.getAgent)
	mux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}", m.patchAgent)
	mux.HandleFunc("DELETE /api/workspaces/{ws}/agents/{name}", m.deleteAgent)
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", HandleQueueUnsupported)

	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", m.authorizeSupervisedIntent(HandleStop(m.agentSvc, m.hub)))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/start", m.authorizeSupervisedIntent(HandleStart(m.agentSvc, m.hub)))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/restart", m.authorizeSupervisedIntent(HandleRestart(m.agentSvc, m.hub)))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/yield", m.authorizeSupervisedIntent(HandleYield(m.agentSvc, m.hub)))
	mux.HandleFunc(
		"GET /api/workspaces/{ws}/agents/{name}/lifecycle-commands/{command_id}",
		HandleGetLifecycleCommand(m.agentSvc),
	)
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{id}/enable", m.setRecordEnabled(true))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{id}/disable", m.setRecordEnabled(false))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{id}/runs", m.listAgentRuns)
	mux.HandleFunc(
		"GET /api/workspaces/{ws}/agents/{id}/sessions/{session_id}/transcript",
		m.getAgentSessionTranscript,
	)
}

func writeAgentSessionTranscriptServiceError(w http.ResponseWriter, err error) {
	var svcErr *service.ServiceError
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
