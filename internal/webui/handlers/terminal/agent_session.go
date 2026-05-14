package terminal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

const (
	terminalKindAgent = "agent"
	roleLead          = "lead"
	roleOrchestrator  = "orchestrator"
	rolePlan          = "plan"
	roleTask          = "task"
)

var agentTerminalSessionLocks sync.Map

// HandleEnsureAgentTerminalSession resolves an agent name to a persisted UUID
// terminal session. The session's launch command lives in tab metadata; the
// WebSocket path never infers agent behavior from the session name.
func HandleEnsureAgentTerminalSession(svc service.TerminalService, st store.Store, loomServerURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, tabMetadataResponse{
				Success: false,
				Error:   "terminal service not initialized",
			})
			return
		}
		if st == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, tabMetadataResponse{
				Success: false,
				Error:   "agent store not initialized",
			})
			return
		}

		workspace := middleware.WorkspaceFromContext(r.Context())
		agentName := r.PathValue("name")
		if !service.ValidAgentName.MatchString(agentName) {
			handler.WriteJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "invalid agent name",
			})
			return
		}

		meta, err := ensureAgentTerminalSession(r.Context(), svc, st, workspace, agentName, loomServerURL)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    meta,
		})
	}
}

func ensureAgentTerminalSession(ctx context.Context, svc service.TerminalService, st store.Store, workspace, agentName, loomServerURL string) (*tabmeta.TabMetadata, error) {
	unlock := lockAgentTerminalSession(workspace, agentName)
	defer unlock()

	agent, err := loadTerminalAgent(ctx, st, workspace, agentName)
	if err != nil {
		return nil, err
	}

	tabs, err := svc.ListTabs(ctx, workspace)
	if err != nil {
		return nil, err
	}
	existing := selectAgentTerminalTab(tabs, agentName)
	if existing != nil && existing.PTYAlive {
		return existing, nil
	}
	if !agentTerminalLaunchAllowed(agent) {
		return inactiveAgentTerminalSession(ctx, svc, workspace, existing)
	}

	sessionName, label, sortOrder := newAgentTerminalTabPlacement(tabs, existing, agentName)
	agentForLaunch, err := ensureLeadOrchestratorLink(ctx, st, workspace, sessionName, agent)
	if err != nil {
		return nil, err
	}
	launch, backend, err := buildAgentLaunchSpec(ctx, st, workspace, sessionName, &agentForLaunch, loomServerURL)
	if err != nil {
		return nil, err
	}

	meta := newAgentTerminalTabMetadata(workspace, sessionName, label, sortOrder, &agentForLaunch, backend, launch, existing)
	if err := svc.PutTab(ctx, workspace, meta); err != nil {
		return nil, err
	}
	pruneStaleAgentTerminalTabs(ctx, svc, workspace, agentName, sessionName, tabs)
	return svc.GetTab(ctx, workspace, sessionName)
}

func loadTerminalAgent(ctx context.Context, st store.Store, workspace, agentName string) (*domain.Agent, error) {
	agent, err := st.Agents().Get(ctx, workspace, agentName)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, service.ErrNotFound("agent not found")
		}
		return nil, service.ErrInternal("failed to load agent", err)
	}
	if agent == nil {
		return nil, service.ErrNotFound("agent not found")
	}
	return agent, nil
}

func inactiveAgentTerminalSession(ctx context.Context, svc service.TerminalService, workspace string, existing *tabmeta.TabMetadata) (*tabmeta.TabMetadata, error) {
	if existing != nil {
		return disableStoredAgentLaunch(ctx, svc, workspace, existing)
	}
	return nil, service.ErrValidation("agent is not running and has no terminal session")
}

func newAgentTerminalTabPlacement(tabs []tabmeta.TabMetadata, existing *tabmeta.TabMetadata, agentName string) (string, string, int) {
	sessionName := "term_" + uuid.NewString()
	label := "agent-" + agentName
	sortOrder := len(tabs)
	if existing != nil {
		label = existing.Label
		sortOrder = existing.SortOrder
	}
	return sessionName, label, sortOrder
}

func ensureLeadOrchestratorLink(ctx context.Context, st store.Store, workspace, sessionName string, agent *domain.Agent) (domain.Agent, error) {
	agentForLaunch := *agent
	if !isLeadRole(agentForLaunch.RoleName) || strings.TrimSpace(agentForLaunch.OrchestratorSessionID) != "" {
		return agentForLaunch, nil
	}

	orchestratorID := "lead-" + uuid.NewString()
	if err := createLeadOrchestratorSession(ctx, st, workspace, sessionName, agentForLaunch.Name, orchestratorID); err != nil {
		return agentForLaunch, err
	}
	if _, err := st.Agents().Update(ctx, workspace, agentForLaunch.Name, store.AgentUpdate{
		OrchestratorSessionID: &orchestratorID,
	}); err != nil {
		return agentForLaunch, service.ErrInternal("failed to link lead agent to terminal session", err)
	}
	agentForLaunch.OrchestratorSessionID = orchestratorID
	return agentForLaunch, nil
}

func newAgentTerminalTabMetadata(workspace, sessionName, label string, sortOrder int, agent *domain.Agent, backend string, launch *tabmeta.LaunchSpec, existing *tabmeta.TabMetadata) *tabmeta.TabMetadata {
	now := time.Now().UTC()
	meta := &tabmeta.TabMetadata{
		SessionName: sessionName,
		Workspace:   workspace,
		Label:       label,
		SortOrder:   sortOrder,
		Pinned:      existing != nil && existing.Pinned,
		Kind:        terminalKindAgent,
		AgentID:     agent.Name,
		Role:        agent.RoleName,
		Backend:     backend,
		Writable:    true,
		Launch:      launch,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if existing != nil {
		meta.Notes = existing.Notes
		meta.IssueID = existing.IssueID
	}
	return meta
}

func lockAgentTerminalSession(workspace, agentName string) func() {
	key := workspace + "\x00" + agentName
	actual, _ := agentTerminalSessionLocks.LoadOrStore(key, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func agentTerminalLaunchAllowed(agent *domain.Agent) bool {
	if agent == nil {
		return false
	}
	return agent.State != domain.AgentStateStopped && agent.DesiredState != domain.AgentDesiredStopped
}

func disableStoredAgentLaunch(ctx context.Context, svc service.TerminalService, workspace string, existing *tabmeta.TabMetadata) (*tabmeta.TabMetadata, error) {
	if existing.Launch == nil && !existing.Writable {
		return existing, nil
	}

	now := time.Now().UTC()
	meta := *existing
	meta.Launch = nil
	meta.Writable = false
	meta.UpdatedAt = now
	if err := svc.PutTab(ctx, workspace, &meta); err != nil {
		return nil, err
	}
	return svc.GetTab(ctx, workspace, existing.SessionName)
}

func selectAgentTerminalTab(tabs []tabmeta.TabMetadata, agentName string) *tabmeta.TabMetadata {
	var newest *tabmeta.TabMetadata
	for i := range tabs {
		tab := &tabs[i]
		if tab.Kind != terminalKindAgent || tab.AgentID != agentName {
			continue
		}
		if tab.PTYAlive {
			return tab
		}
		if newest == nil || tab.UpdatedAt.After(newest.UpdatedAt) {
			newest = tab
		}
	}
	return newest
}

func pruneStaleAgentTerminalTabs(ctx context.Context, svc service.TerminalService, workspace, agentName, keepSession string, tabs []tabmeta.TabMetadata) {
	for i := range tabs {
		tab := tabs[i]
		if tab.SessionName == keepSession || tab.Kind != terminalKindAgent || tab.AgentID != agentName || tab.PTYAlive {
			continue
		}
		if err := svc.DeleteTab(ctx, workspace, tab.SessionName); err != nil {
			slog.Warn("failed to prune stale agent terminal tab",
				"workspace", workspace,
				"agent", agentName,
				"session", tab.SessionName,
				"err", err)
		}
	}
}

func buildAgentLaunchSpec(ctx context.Context, st store.Store, workspace, sessionName string, agent *domain.Agent, loomServerURL string) (*tabmeta.LaunchSpec, string, error) {
	roleName := strings.ToLower(strings.TrimSpace(agent.RoleName))
	role, err := loadAgentLaunchRole(ctx, st, workspace, agent.RoleName)
	if err != nil {
		return nil, "", err
	}
	backend := agentLaunchBackend(agent, role)
	commandArgs, err := agentLaunchCommandArgs(roleName, agent, role)
	if err != nil {
		return nil, "", err
	}
	args := append(agentLaunchBaseArgs(workspace, loomServerURL, backend), commandArgs...)

	return &tabmeta.LaunchSpec{
		Argv: webuterminal.ShellArgvForCommand(args),
		Env:  agentLaunchEnv(workspace, sessionName, backend, agent),
	}, backend, nil
}

func loadAgentLaunchRole(ctx context.Context, st store.Store, workspace, roleName string) (*domain.Role, error) {
	role, err := st.Roles().Get(ctx, workspace, roleName)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, service.ErrInternal("failed to load agent role", err)
	}
	return role, nil
}

func agentLaunchBackend(agent *domain.Agent, role *domain.Role) string {
	backend := strings.TrimSpace(agent.Backend)
	if backend == "" && role != nil {
		backend = strings.TrimSpace(role.Backend)
	}
	return backend
}

func agentLaunchBaseArgs(workspace, loomServerURL, backend string) []string {
	args := []string{webuterminal.LoomExecutableForTerminal()}
	if loomServerURL != "" {
		args = append(args, "--server", loomServerURL)
	}
	if workspace != "" {
		args = append(args, "--workspace", workspace)
	}
	if backend != "" {
		args = append(args, "--backend", backend)
	}
	return args
}

func agentLaunchCommandArgs(roleName string, agent *domain.Agent, role *domain.Role) ([]string, error) {
	switch roleName {
	case roleLead, roleOrchestrator:
		return []string{"lead"}, nil
	case rolePlan, roleTask:
		return builtInAgentLaunchArgs(roleName, agent), nil
	default:
		return customAgentLaunchArgs(agent, role)
	}
}

func builtInAgentLaunchArgs(roleName string, agent *domain.Agent) []string {
	args := []string{roleName, agent.Name, "--auto", "--daemon-mode"}
	return appendParentArg(args, agent.Parent)
}

func customAgentLaunchArgs(agent *domain.Agent, role *domain.Role) ([]string, error) {
	if role == nil || strings.TrimSpace(role.PromptFile) == "" {
		return nil, service.ErrValidation(fmt.Sprintf("agent role %q has no launch spec", agent.RoleName))
	}
	args := []string{"agent", agent.Name, "--prompt", role.PromptFile, "--auto", "--daemon-mode"}
	if strings.TrimSpace(role.TaskFilter) != "" {
		args = append(args, "--task-filter", role.TaskFilter)
	}
	return appendParentArg(args, agent.Parent), nil
}

func appendParentArg(args []string, parent string) []string {
	if strings.TrimSpace(parent) != "" {
		args = append(args, "--parent", parent)
	}
	return args
}

func agentLaunchEnv(workspace, sessionName, backend string, agent *domain.Agent) map[string]string {
	env := map[string]string{
		"LOOM_AGENT_NAME":        agent.Name,
		"LOOM_AGENT_ROLE":        agent.RoleName,
		"LOOM_AGENT_TERMINAL_ID": sessionName,
		"LOOM_WORKSPACE":         workspace,
	}
	if backend != "" {
		env["LOOM_BACKEND"] = backend
	}
	if orchestratorID := strings.TrimSpace(agent.OrchestratorSessionID); orchestratorID != "" {
		env["LOOM_ORCHESTRATOR_SESSION_ID"] = orchestratorID
	}
	return env
}

func createLeadOrchestratorSession(ctx context.Context, st store.Store, workspace, terminalID, agentName, sessionID string) error {
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: workspace,
		SessionID:    sessionID,
		AgentID:      agentName,
		Kind:         domain.AgentSessionKindOrchestration,
		TerminalID:   terminalID,
		Status:       domain.AgentSessionRunning,
		Metadata: map[string]string{
			"source": "web-terminal",
		},
	}); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return nil
		}
		return service.ErrInternal("failed to create lead orchestrator session", err)
	}
	return nil
}

func isLeadRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case roleLead, roleOrchestrator:
		return true
	default:
		return false
	}
}
