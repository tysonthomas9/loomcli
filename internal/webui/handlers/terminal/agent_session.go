package terminal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

const (
	terminalKindAgent = "agent"
	rolePlan          = "plan"
	roleTask          = "task"
)

var agentTerminalSessionLocks sync.Map

// HandleEnsureAgentTerminalSession resolves an agent name to a persisted UUID
// terminal session. The session's launch command lives in tab metadata; the
// WebSocket path never infers agent behavior from the session name.
func HandleEnsureAgentTerminalSession(svc service.TerminalService, st store.Store) http.HandlerFunc {
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
		if !service.IsValidAgentName(agentName) {
			handler.WriteJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "invalid agent name",
			})
			return
		}

		meta, err := ensureAgentTerminalSession(r.Context(), svc, st, workspace, agentName)
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

func ensureAgentTerminalSession(ctx context.Context, svc service.TerminalService, st store.Store, workspace, agentName string) (*tabmeta.TabMetadata, error) {
	unlock := lockAgentTerminalSession(workspace, agentName)
	defer unlock()

	agent, err := loadTerminalAgent(ctx, st, workspace, agentName)
	if err != nil {
		return nil, err
	}
	role, err := loadAgentLaunchRole(ctx, st, workspace, agent.RoleName)
	if err != nil {
		return nil, err
	}
	roleKind := domain.ResolveRoleKind(role, agent.RoleName)

	if isDaemonOwnedEphemeralWorker(agent, roleKind) {
		return nil, service.ErrValidation("daemon-owned ephemeral worker terminals cannot be started from the agents page; use worker logs or task session history")
	}

	tabs, err := svc.ListTabs(ctx, workspace)
	if err != nil {
		return nil, err
	}
	existing := selectAgentTerminalTab(tabs, agentName)
	if existing != nil && existing.Attachable {
		// Cache-validity check: if the agent's effective backend/role has
		// changed since the existing tab was built, the cached launch spec
		// is stale (e.g. agent was created with no backend, workspace
		// default was codex, then user set agent.backend = claude). The
		// running PTY is still on the old backend. Rebuild a candidate
		// spec and compare argv; if they differ, fall through to the
		// rebuild path which will issue a fresh tab metadata. The stale
		// PTY is killed by svc.PutTab → reattach when the user reloads.
		if !agentTerminalLaunchSpecStale(ctx, st, workspace, existing, agent) {
			return existing, nil
		}
	}
	if !agentTerminalLaunchAllowed(agent, roleKind) {
		return inactiveAgentTerminalSession(ctx, svc, workspace, existing)
	}

	sessionName, label, sortOrder := newAgentTerminalTabPlacement(tabs, existing, agentName)
	agentForLaunch, orchestratorID, err := ensureTerminalOrchestratorLink(ctx, st, workspace, sessionName, agent, roleKind)
	if err != nil {
		return nil, err
	}
	launch, backend, err := buildAgentLaunchSpec(ctx, st, workspace, sessionName, &agentForLaunch, orchestratorID)
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

// agentTerminalLaunchSpecStale returns true when the existing tab's cached
// launch spec no longer matches what would be built for the current agent
// state. Common trigger: agent.backend was patched after the terminal
// session was created, so the cached argv has no --backend flag but the
// next render would include it. Without this check, the stale spec is
// returned indefinitely and the running PTY never picks up the change.
func agentTerminalLaunchSpecStale(
	ctx context.Context,
	st store.Store,
	workspace string,
	existing *tabmeta.TabMetadata,
	agent *domain.Agent,
) bool {
	if existing == nil || existing.Launch == nil {
		return true
	}
	// Build a candidate spec under the same name so any volatile parts
	// (sessionName) match. We only compare the launch argv — env vars carry
	// per-session ids that legitimately differ and shouldn't trigger churn.
	// Pass empty orchestratorID — the argv doesn't include it (it's an env
	// var only), so the stale-check is unaffected by the orchestrator.
	candidate, _, err := buildAgentLaunchSpec(ctx, st, workspace, existing.SessionName, agent, "")
	if err != nil || candidate == nil {
		return false
	}
	return !slices.Equal(candidate.Argv, existing.Launch.Argv) || candidate.Cwd != existing.Launch.Cwd
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

// ensureTerminalOrchestratorLink resolves (or creates) the terminal agent's
// orchestration session and returns its session id alongside the agent copy. The id is
// carried as a separate return value rather than on the domain.Agent struct
// — AgentSession is the single source of truth, accessed via this function
// and store.OrchestrationSessionIDFor.
func ensureTerminalOrchestratorLink(ctx context.Context, st store.Store, workspace, sessionName string, agent *domain.Agent, kind domain.RoleKind) (domain.Agent, string, error) {
	agentForLaunch := *agent
	if kind != domain.RoleKindInteractive {
		return agentForLaunch, "", nil
	}
	// Skip when this terminal agent already has an active orchestration session.
	if existingID, err := store.OrchestrationSessionIDFor(ctx, st, workspace, agentForLaunch.Name); err == nil && existingID != "" {
		return agentForLaunch, existingID, nil
	}

	orchestratorID := "lead-" + uuid.NewString()
	if err := createLeadOrchestratorSession(ctx, st, workspace, sessionName, agentForLaunch.Name, orchestratorID); err != nil {
		return agentForLaunch, "", err
	}
	return agentForLaunch, orchestratorID, nil
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

func agentTerminalLaunchAllowed(agent *domain.Agent, kind domain.RoleKind) bool {
	if agent == nil {
		return false
	}
	if kind == domain.RoleKindInteractive {
		return true
	}
	return agent.State != domain.AgentStateStopped && agent.DesiredState != domain.AgentDesiredStopped
}

func isDaemonOwnedEphemeralWorker(agent *domain.Agent, kind domain.RoleKind) bool {
	if agent == nil {
		return false
	}
	return agent.Mode == domain.AgentModeEphemeral && kind != domain.RoleKindInteractive
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
		if tab.Attachable {
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
		if tab.SessionName == keepSession || tab.Kind != terminalKindAgent || tab.AgentID != agentName || tab.Attachable {
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

// buildAgentLaunchSpec constructs the PTY launch spec for an agent terminal.
// orchestratorID is the lead → orchestration session id resolved by
// ensureTerminalOrchestratorLink. It is passed in rather than read off the
// agent struct because AgentSession is the single source of truth.
func buildAgentLaunchSpec(ctx context.Context, st store.Store, workspace, sessionName string, agent *domain.Agent, orchestratorID string) (*tabmeta.LaunchSpec, string, error) {
	role, err := loadAgentLaunchRole(ctx, st, workspace, agent.RoleName)
	if err != nil {
		return nil, "", err
	}
	roleKind := domain.ResolveRoleKind(role, agent.RoleName)
	backend := agentLaunchBackend(ctx, st, workspace, agent, role)
	commandArgs, err := agentLaunchCommandArgs(roleKind, agent, role)
	if err != nil {
		return nil, "", err
	}
	args := append(agentLaunchBaseArgs(workspace, backend), commandArgs...)

	return &tabmeta.LaunchSpec{
		Argv: webuterminal.ShellArgvForCommand(args),
		Env:  agentLaunchEnv(workspace, sessionName, backend, orchestratorID, agent),
		Cwd:  agentLaunchCwd(workspace, agent),
	}, backend, nil
}

func agentLaunchCwd(workspace string, agent *domain.Agent) string {
	if agent == nil {
		return ""
	}
	worktree, ok := localworkspace.RememberedAgentWorktree(workspace, agent.Name)
	if !ok {
		return ""
	}
	return worktree
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

func agentLaunchBackend(ctx context.Context, st store.Store, workspace string, agent *domain.Agent, role *domain.Role) string {
	backend := strings.TrimSpace(agent.Backend)
	if backend == "" && role != nil {
		backend = strings.TrimSpace(role.Backend)
	}
	// Workspace-level default backend (set via PATCH /config/backend or the
	// daemon profile) is the last fallback before the launch spec ships with
	// no --backend flag at all. Without this, an agent created via
	// `loom agentdef add … --role lead` (no --backend) produced a terminal
	// command of `loom lead` with no backend wired, so codex never started.
	if backend == "" && st != nil {
		if profile, err := st.Daemon().Get(ctx, workspace); err == nil && profile != nil {
			backend = strings.TrimSpace(profile.AgentBackend)
		}
	}
	return backend
}

func agentLaunchBaseArgs(workspace, backend string) []string {
	args := []string{webuterminal.LoomExecutableForTerminal()}
	if workspace != "" {
		args = append(args, "--workspace", workspace)
	}
	if backend != "" {
		args = append(args, "--backend", backend)
	}
	return args
}

func agentLaunchCommandArgs(kind domain.RoleKind, agent *domain.Agent, role *domain.Role) ([]string, error) {
	roleName := strings.ToLower(strings.TrimSpace(agent.RoleName))
	if kind == domain.RoleKindInteractive {
		args := []string{"lead"}
		if role != nil && strings.TrimSpace(role.Prompt) == "" && strings.TrimSpace(role.PromptFile) != "" {
			args = append(args, "--prompt", role.PromptFile)
		}
		return args, nil
	}
	switch roleName {
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

func agentLaunchEnv(workspace, sessionName, backend, orchestratorID string, agent *domain.Agent) map[string]string {
	env := map[string]string{
		"LOOM_AGENT_NAME":        agent.Name,
		"LOOM_AGENT_ROLE":        agent.RoleName,
		"LOOM_AGENT_TERMINAL_ID": sessionName,
		"LOOM_WORKSPACE":         workspace,
	}
	if backend != "" {
		env["LOOM_BACKEND"] = backend
	}
	if orchID := strings.TrimSpace(orchestratorID); orchID != "" {
		env["LOOM_ORCHESTRATOR_SESSION_ID"] = orchID
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
