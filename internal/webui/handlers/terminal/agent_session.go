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

	tabs, err := svc.ListTabs(ctx, workspace)
	if err != nil {
		return nil, err
	}
	existing := selectAgentTerminalTab(tabs, agentName)
	if existing != nil && existing.PTYAlive {
		return existing, nil
	}

	if !agentTerminalLaunchAllowed(agent) {
		if existing != nil {
			return disableStoredAgentLaunch(ctx, svc, workspace, existing)
		}
		return nil, service.ErrValidation("agent is not running and has no terminal session")
	}

	sessionName := "term_" + uuid.NewString()
	sortOrder := len(tabs)
	label := "agent-" + agentName
	if existing != nil {
		sortOrder = existing.SortOrder
		label = existing.Label
	}

	agentForLaunch := *agent
	orchestratorID := strings.TrimSpace(agentForLaunch.OrchestratorSessionID)
	if isLeadRole(agentForLaunch.RoleName) && orchestratorID == "" {
		orchestratorID = "lead-" + uuid.NewString()
		if err := createLeadOrchestratorSession(ctx, st, workspace, sessionName, agentName, orchestratorID); err != nil {
			return nil, err
		}
		if _, err := st.Agents().Update(ctx, workspace, agentName, store.AgentUpdate{
			OrchestratorSessionID: &orchestratorID,
		}); err != nil {
			return nil, service.ErrInternal("failed to link lead agent to terminal session", err)
		}
		agentForLaunch.OrchestratorSessionID = orchestratorID
	}

	launch, backend, err := buildAgentLaunchSpec(ctx, st, workspace, sessionName, &agentForLaunch, loomServerURL)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	meta := &tabmeta.TabMetadata{
		SessionName: sessionName,
		Workspace:   workspace,
		Label:       label,
		Notes:       "",
		SortOrder:   sortOrder,
		Pinned:      existing != nil && existing.Pinned,
		Kind:        terminalKindAgent,
		AgentID:     agentName,
		Role:        agentForLaunch.RoleName,
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

	if err := svc.PutTab(ctx, workspace, meta); err != nil {
		return nil, err
	}
	pruneStaleAgentTerminalTabs(ctx, svc, workspace, agentName, sessionName, tabs)
	return svc.GetTab(ctx, workspace, sessionName)
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
	backend := strings.TrimSpace(agent.Backend)
	var role *domain.Role
	if loaded, err := st.Roles().Get(ctx, workspace, agent.RoleName); err == nil {
		role = loaded
		if backend == "" && role != nil {
			backend = strings.TrimSpace(role.Backend)
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, "", service.ErrInternal("failed to load agent role", err)
	}

	if roleName != roleLead && roleName != roleOrchestrator && roleName != rolePlan && roleName != roleTask && role == nil {
		return nil, "", service.ErrValidation(fmt.Sprintf("agent role %q has no launch spec", agent.RoleName))
	}

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

	switch {
	case roleName == roleLead || roleName == roleOrchestrator:
		args = append(args, "lead")
	case roleName == rolePlan || roleName == roleTask:
		args = append(args, roleName, agent.Name, "--auto", "--daemon-mode")
		if strings.TrimSpace(agent.Parent) != "" {
			args = append(args, "--parent", agent.Parent)
		}
	case role != nil && strings.TrimSpace(role.PromptFile) != "":
		args = append(args, "agent", agent.Name, "--prompt", role.PromptFile, "--auto", "--daemon-mode")
		if strings.TrimSpace(role.TaskFilter) != "" {
			args = append(args, "--task-filter", role.TaskFilter)
		}
		if strings.TrimSpace(agent.Parent) != "" {
			args = append(args, "--parent", agent.Parent)
		}
	default:
		return nil, "", service.ErrValidation(fmt.Sprintf("agent role %q has no launch spec", agent.RoleName))
	}

	env := map[string]string{
		"LOOM_AGENT_NAME":        agent.Name,
		"LOOM_AGENT_ROLE":        agent.RoleName,
		"LOOM_AGENT_TERMINAL_ID": sessionName,
		"LOOM_WORKSPACE":         workspace,
	}
	if backend != "" {
		env["LOOM_BACKEND"] = backend
	}
	if strings.TrimSpace(agent.OrchestratorSessionID) != "" {
		env["LOOM_ORCHESTRATOR_SESSION_ID"] = strings.TrimSpace(agent.OrchestratorSessionID)
	}

	return &tabmeta.LaunchSpec{
		Argv: webuterminal.ShellArgvForCommand(args),
		Env:  env,
	}, backend, nil
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
