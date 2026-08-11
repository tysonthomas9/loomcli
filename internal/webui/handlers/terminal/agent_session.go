package terminal

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

const (
	terminalKindAgent = "agent"
	rolePlan          = "plan"
	roleTask          = "task"
)

var errBackgroundWorkerTerminal = errors.New("background worker terminals cannot be launched directly; use worker logs or task session history")

// HandleEnsureAgentTerminalSession resolves an agent name to a persisted UUID
// terminal session. The session's launch command lives in tab metadata; the
// WebSocket path never infers agent behavior from the session name.
func HandleEnsureAgentTerminalSession(
	svc webuterminal.TerminalService,
	st terminalStore,
	identities ...terminalAgentIdentity,
) http.HandlerFunc {
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
		if !agentcoord.IsValidAgentName(agentName) {
			handler.WriteJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "invalid agent name",
			})
			return
		}

		meta, err := ensureAgentTerminalSession(
			r.Context(),
			svc,
			st,
			workspace,
			agentName,
			identities...,
		)
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

//nolint:funlen // Keep the non-reentrant per-agent lifecycle lock, durable tab write, unlock, and stale-tab cleanup ordering in one boundary.
func ensureAgentTerminalSession(
	ctx context.Context,
	svc webuterminal.TerminalService,
	st terminalStore,
	workspace,
	agentName string,
	identities ...terminalAgentIdentity,
) (*webuterminal.TabMetadata, error) {
	unlock := webuterminal.LockAgentLifecycle(workspace, agentName)
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()

	agent, err := loadTerminalAgent(ctx, workspace, agentName, identities...)
	if err != nil {
		return nil, terminalAgentIdentityServiceError(err)
	}
	role, err := loadAgentLaunchRole(ctx, st, workspace, agent.RoleName)
	if err != nil {
		return nil, err
	}
	roleKind := domain.ResolveRoleKind(role, agent.RoleName)

	if isBackgroundWorker(agent, roleKind) {
		return nil, apperrors.ErrValidation(errBackgroundWorkerTerminal.Error())
	}

	tabs, err := svc.ListTabs(ctx, workspace)
	if err != nil {
		return nil, err
	}
	existing := selectAgentTerminalTab(tabs, agentName)
	if existing != nil && existing.PTYAlive {
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
		return inactiveAgentTerminalSession(existing)
	}

	sessionName, label, sortOrder := newAgentTerminalTabPlacement(tabs, existing, agentName)
	agentForLaunch, orchestratorID := ensureTerminalOrchestratorLink(ctx, st, workspace, agent, roleKind)
	launch, backend, err := buildAgentLaunchSpec(ctx, st, workspace, sessionName, &agentForLaunch, orchestratorID)
	if err != nil {
		return nil, err
	}

	meta := newAgentTerminalTabMetadata(workspace, sessionName, label, sortOrder, &agentForLaunch, backend, launch, existing)
	if err := svc.PutTab(ctx, workspace, meta); err != nil {
		return nil, err
	}
	stored, err := svc.GetTab(ctx, workspace, sessionName)
	if err != nil {
		return nil, err
	}

	// DeleteTab deliberately acquires the same per-agent lifecycle boundary
	// before converging canonical Interaction identity. Release our creation
	// boundary only after the new tab is durably readable, then let each stale
	// delete reacquire it through the public service path. Calling DeleteTab
	// while holding this non-reentrant mutex deadlocks.
	unlock()
	locked = false
	pruneStaleAgentTerminalTabs(ctx, svc, workspace, agentName, sessionName, tabs)
	return stored, nil
}

func terminalAgentIdentityServiceError(err error) error {
	if errors.Is(err, agents.ErrNotFound) {
		return apperrors.ErrNotFound("agent not found")
	}
	return apperrors.ErrInternal("failed to load agent identity", err)
}

// agentTerminalLaunchSpecStale returns true when the existing tab's cached
// launch spec no longer matches what would be built for the current agent
// state. Common trigger: agent.backend was patched after the terminal
// session was created, so the cached argv has no --backend flag but the
// next render would include it. Without this check, the stale spec is
// returned indefinitely and the running PTY never picks up the change.
func agentTerminalLaunchSpecStale(
	ctx context.Context,
	st terminalStore,
	workspace string,
	existing *webuterminal.TabMetadata,
	agent *agents.RuntimeIdentity,
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

func inactiveAgentTerminalSession(existing *webuterminal.TabMetadata) (*webuterminal.TabMetadata, error) {
	if existing == nil {
		return nil, apperrors.ErrValidation("agent is not running and has no terminal session")
	}

	// Canonical agent tabs are Interaction-owned lifecycle records. Generic
	// PutTab deliberately cannot replace them, and merely viewing a stopped
	// agent must not erase the exact session/terminal/lease identity needed by
	// history and recovery. Return a disabled response projection instead. The
	// WebSocket path independently verifies desired state before any attach, so
	// the retained launch spec cannot restart the stopped agent.
	meta := *existing
	meta.Launch = nil
	meta.Writable = false
	return &meta, nil
}

func newAgentTerminalTabPlacement(tabs []webuterminal.TabMetadata, existing *webuterminal.TabMetadata, agentName string) (string, string, int) {
	sessionName := "term_" + uuid.NewString()
	label := "agent-" + agentName
	sortOrder := len(tabs)
	if existing != nil {
		label = existing.Label
		sortOrder = existing.SortOrder
	}
	return sessionName, label, sortOrder
}

// ensureTerminalOrchestratorLink resolves an active orchestration session or
// reserves an ID for the launch environment. It deliberately does not persist
// a running record: loom lead creates and heartbeats that record only after the
// PTY child actually starts, so an unlaunched tab cannot become a stale session.
func ensureTerminalOrchestratorLink(ctx context.Context, st terminalStore, workspace string, agent *agents.RuntimeIdentity, kind domain.RoleKind) (agents.RuntimeIdentity, string) {
	agentForLaunch := *agent
	if kind != domain.RoleKindInteractive {
		return agentForLaunch, ""
	}
	// Skip when this terminal agent already has an active orchestration session.
	if existingID, err := store.OrchestrationSessionIDFor(ctx, st, workspace, agentForLaunch.AgentID); err == nil && existingID != "" {
		return agentForLaunch, existingID
	}

	return agentForLaunch, "lead-" + uuid.NewString()
}

func newAgentTerminalTabMetadata(workspace, sessionName, label string, sortOrder int, agent *agents.RuntimeIdentity, backend string, launch *webuterminal.LaunchSpec, existing *webuterminal.TabMetadata) *webuterminal.TabMetadata {
	now := time.Now().UTC()
	meta := &webuterminal.TabMetadata{
		SessionName: sessionName,
		Workspace:   workspace,
		Label:       label,
		SortOrder:   sortOrder,
		Pinned:      existing != nil && existing.Pinned,
		Kind:        terminalKindAgent,
		AgentID:     agent.AgentID,
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

func agentTerminalLaunchAllowed(agent *agents.RuntimeIdentity, kind domain.RoleKind) bool {
	return agent != nil &&
		agent.DesiredState == agents.DesiredRunning &&
		kind == domain.RoleKindInteractive
}

func isBackgroundWorker(agent *agents.RuntimeIdentity, kind domain.RoleKind) bool {
	return agent != nil && kind != domain.RoleKindInteractive
}

func selectAgentTerminalTab(tabs []webuterminal.TabMetadata, agentName string) *webuterminal.TabMetadata {
	var newest *webuterminal.TabMetadata
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

func pruneStaleAgentTerminalTabs(ctx context.Context, svc webuterminal.TerminalService, workspace, agentName, keepSession string, tabs []webuterminal.TabMetadata) {
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

// buildAgentLaunchSpec constructs the PTY launch spec for an agent terminal.
// orchestratorID is the lead → orchestration session id resolved by
// ensureTerminalOrchestratorLink. It is passed in rather than read off the
// agent struct because AgentSession is the single source of truth.
func buildAgentLaunchSpec(ctx context.Context, st terminalStore, workspace, sessionName string, agent *agents.RuntimeIdentity, orchestratorID string) (*webuterminal.LaunchSpec, string, error) {
	role, err := loadAgentLaunchRole(ctx, st, workspace, agent.RoleName)
	if err != nil {
		return nil, "", err
	}
	roleKind := domain.ResolveRoleKind(role, agent.RoleName)
	if isBackgroundWorker(agent, roleKind) {
		return nil, "", apperrors.ErrValidation(errBackgroundWorkerTerminal.Error())
	}
	backend := agentLaunchBackend(workspace, agent, role)
	commandArgs, err := agentLaunchCommandArgs(roleKind, role)
	if err != nil {
		return nil, "", err
	}
	args := append(agentLaunchBaseArgs(workspace, backend), commandArgs...)

	return &webuterminal.LaunchSpec{
		Argv: webuterminal.ShellArgvForCommand(args),
		Env:  agentLaunchEnv(workspace, sessionName, backend, orchestratorID, agent),
		Cwd:  agentLaunchCwd(workspace, agent),
	}, backend, nil
}

func agentLaunchCwd(workspace string, agent *agents.RuntimeIdentity) string {
	if agent == nil {
		return ""
	}
	worktree, ok := localworkspace.RememberedAgentWorktree(workspace, agent.AgentID)
	if !ok {
		return ""
	}
	return worktree
}

func loadAgentLaunchRole(ctx context.Context, st terminalStore, workspace, roleName string) (*domain.Role, error) {
	role, err := st.Roles().Get(ctx, workspace, roleName)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apperrors.ErrInternal("failed to load agent role", err)
	}
	return role, nil
}

func agentLaunchBackend(workspace string, agent *agents.RuntimeIdentity, role *domain.Role) string {
	backend := strings.TrimSpace(agent.Backend)
	if backend == "" && role != nil {
		backend = strings.TrimSpace(role.Backend)
	}
	// The local-node workspace default (set via PATCH /config/backend) is the
	// last fallback before the launch spec ships with
	// no --backend flag at all. Without this, an agent created via
	// `loom agentdef add … --role lead` (no --backend) produced a terminal
	// command of `loom lead` with no backend wired, so codex never started.
	if backend == "" {
		backend, _ = bootstrap.RuntimeProvider(workspace)
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

func agentLaunchCommandArgs(kind domain.RoleKind, role *domain.Role) ([]string, error) {
	if kind == domain.RoleKindInteractive {
		args := []string{"lead"}
		if role != nil && strings.TrimSpace(role.Prompt) == "" && strings.TrimSpace(role.PromptFile) != "" {
			args = append(args, "--prompt", role.PromptFile)
		}
		return args, nil
	}
	return nil, apperrors.ErrValidation(errBackgroundWorkerTerminal.Error())
}

func agentLaunchEnv(workspace, sessionName, backend, orchestratorID string, agent *agents.RuntimeIdentity) map[string]string {
	env := map[string]string{
		"LOOM_AGENT_NAME":        agent.AgentID,
		"LOOM_AGENT_ROLE":        agent.RoleName,
		"LOOM_AGENT_TERMINAL_ID": sessionName,
		"LOOM_WORKSPACE":         workspace,
	}
	// The PTY base environment deliberately strips every ambient LOOM_* value.
	// Add the server-resolved local data directory back as a trusted launch
	// overlay so the child CLI shares the Desktop workspace registry instead of
	// silently falling back to ~/.loom.
	if configDir := strings.TrimSpace(bootstrap.LoomDir()); configDir != "" {
		env["LOOM_CONFIG_DIR"] = configDir
	}
	if backend != "" {
		env["LOOM_BACKEND"] = backend
	}
	if orchID := strings.TrimSpace(orchestratorID); orchID != "" {
		env["LOOM_ORCHESTRATOR_SESSION_ID"] = orchID
	}
	return env
}
