package interaction

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

const terminalKindAgent = "agent"

// AgentTerminalPlacement is the machine-local read adapter consumed by
// Interaction when it builds a terminal launch envelope. It exposes placement
// facts only; it cannot mutate Workspace, Source Control, or orchestration.
type AgentTerminalPlacement interface {
	FindActiveOrchestrationSession(context.Context, string, string) (string, error)
	AgentWorktree(context.Context, string, string) string
	WorkspacePath(context.Context, string) string
	DefaultBackend(context.Context, string) string
	ConfigDir() string
}

// AgentTerminalSessionDependencies are the fenced Interaction lifecycle
// ports used when the private PTY adapter spawns a new interactive child.
type AgentTerminalSessionDependencies struct {
	API         API
	Authorities SessionAuthorityResolver
	NodeID      string
	APIURL      string
}

// TerminalDependencies are the owner/query and private-adapter ports required
// to converge terminal intent. All launch, setup, and stale-tab policy stays
// behind TerminalTabs.
type TerminalDependencies struct {
	Agents    agents.IdentityQueries
	Roles     agents.RoleQueries
	Placement AgentTerminalPlacement
	Sessions  AgentTerminalSessionDependencies
	LiveView  AgentTerminalRuntime
	Setup     TerminalSetupCatalog
}

func (s *TerminalTabService) EnsureAgentTerminal(
	ctx context.Context,
	command EnsureAgentTerminalCommand,
) (*TabMetadata, error) {
	workspace, agentID, err := s.ensureAgentTerminalKey(command)
	if err != nil {
		return nil, err
	}

	unlock := LockAgentLifecycle(workspace, agentID)
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()

	agent, role, roleKind, err := s.loadAgentTerminalDefinition(ctx, workspace, agentID)
	if err != nil {
		return nil, err
	}
	tabs, err := s.ListTabs(ctx, workspace)
	if err != nil {
		return nil, err
	}
	existing := selectAgentTerminalTab(tabs, agentID)
	if agent.DesiredState != agents.DesiredRunning {
		return inactiveAgentTerminal(existing)
	}
	reusable, err := s.convergeAgentTerminal(ctx, workspace, existing, agent)
	if err != nil {
		return nil, err
	}
	if reusable {
		return existing, nil
	}
	stored, err := s.createAgentTerminal(ctx, workspace, agentID, roleKind, agent, role, existing, tabs)
	if err != nil {
		return nil, err
	}

	// DeleteTab takes the same non-reentrant lifecycle lock. Release creation
	// only after the new tab is durably readable, then prune stale placements.
	unlock()
	locked = false
	s.pruneAgentTabs(ctx, workspace, agentID, stored.SessionName, tabs)
	return stored, nil
}

func (s *TerminalTabService) ensureAgentTerminalKey(
	command EnsureAgentTerminalCommand,
) (string, string, error) {
	workspace := strings.TrimSpace(command.WorkspaceKey)
	agentID := strings.TrimSpace(command.AgentID)
	if workspace == "" || agentID == "" {
		return "", "", terminalError(ErrInvalid, "workspace and agent are required", nil)
	}
	if s == nil || s.agentTerminal.Agents == nil || s.agentTerminal.Roles == nil {
		return "", "", terminalError(ErrUnavailable, "agent terminal identity is unavailable", nil)
	}
	return workspace, agentID, nil
}

func (s *TerminalTabService) loadAgentTerminalDefinition(
	ctx context.Context,
	workspace, agentID string,
) (*agents.RuntimeIdentity, *agents.Role, string, error) {
	agent, err := s.loadAgentRuntimeIdentity(ctx, workspace, agentID)
	if err != nil {
		return nil, nil, "", err
	}
	role, err := s.loadAgentRole(ctx, workspace, agent.RoleName)
	if err != nil {
		return nil, nil, "", err
	}
	roleKind := agents.ResolveRoleKind(role, agent.RoleName)
	if roleKind != agents.RoleKindInteractive {
		return nil, nil, "", terminalError(ErrInvalid,
			"background worker terminals cannot be launched directly; use worker logs or task session history", nil)
	}
	return agent, role, roleKind, nil
}

func (s *TerminalTabService) convergeAgentTerminal(
	ctx context.Context,
	workspace string,
	existing *TabMetadata,
	agent *agents.RuntimeIdentity,
) (bool, error) {
	stale, err := s.agentLaunchStale(ctx, workspace, existing, agent)
	if err != nil {
		return false, err
	}
	if existing == nil || !existing.PTYAlive {
		return false, nil
	}
	if !stale {
		return true, nil
	}
	if s.runtime == nil {
		return false, terminalError(ErrUnavailable, "stale agent terminal runtime is unavailable", nil)
	}
	key := TerminalKey{WorkspaceKey: workspace, TerminalID: existing.SessionName}
	if err := s.runtime.Kill(key); err != nil {
		return false, terminalError(ErrUnavailable, "failed to stop stale agent terminal", err)
	}
	if err := s.tabStore.Delete(ctx, workspace, existing.SessionName); err != nil {
		return false, terminalError(ErrUnavailable, "failed to remove stale agent terminal placement", err)
	}
	return false, nil
}

func (s *TerminalTabService) createAgentTerminal(
	ctx context.Context,
	workspace, agentID, roleKind string,
	agent *agents.RuntimeIdentity,
	role *agents.Role,
	existing *TabMetadata,
	tabs []TabMetadata,
) (*TabMetadata, error) {
	sessionID, err := NewUUID()
	if err != nil {
		return nil, terminalError(ErrUnavailable, "failed to generate terminal identity", err)
	}
	sessionName := "term_" + sessionID
	label := "agent-" + agentID
	sortOrder := len(tabs)
	if existing != nil {
		label, sortOrder = existing.Label, existing.SortOrder
	}
	orchestrationID, err := s.agentOrchestrationID(ctx, workspace, agent, roleKind)
	if err != nil {
		return nil, err
	}
	launch, backend, err := s.agentLaunchSpec(ctx, workspace, sessionName, agent, role, orchestrationID)
	if err != nil {
		return nil, err
	}
	meta := newAgentTabMetadata(workspace, sessionName, label, sortOrder, agent, backend, launch, existing)
	if err := s.putTabMetadata(ctx, workspace, meta); err != nil {
		return nil, err
	}
	return s.GetTab(ctx, workspace, sessionName)
}

func (s *TerminalTabService) loadAgentRuntimeIdentity(
	ctx context.Context, workspace, agentID string,
) (*agents.RuntimeIdentity, error) {
	record, err := s.agentTerminal.Agents.GetAgent(ctx, workspace, agentID)
	if errors.Is(err, agents.ErrNotFound) {
		return nil, terminalError(ErrNotFound, "agent not found", err)
	}
	if err != nil {
		return nil, terminalError(ErrUnavailable, "failed to load agent identity", err)
	}
	runtime, err := agents.ResolveRuntimeIdentity(record)
	if err != nil {
		return nil, terminalError(ErrInvalidPersistedState, "agent identity is invalid", err)
	}
	return runtime, nil
}

func (s *TerminalTabService) loadAgentRole(
	ctx context.Context, workspace, roleName string,
) (*agents.Role, error) {
	role, err := s.agentTerminal.Roles.GetRole(ctx, workspace, roleName)
	if errors.Is(err, agents.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, terminalError(ErrUnavailable, "failed to load agent role", err)
	}
	return role, nil
}

func (s *TerminalTabService) agentLaunchStale(
	ctx context.Context,
	workspace string,
	existing *TabMetadata,
	agent *agents.RuntimeIdentity,
) (bool, error) {
	if existing == nil || existing.Launch == nil {
		return true, nil
	}
	role, err := s.loadAgentRole(ctx, workspace, agent.RoleName)
	if err != nil {
		return false, err
	}
	orchestrationID := existing.Launch.Env["LOOM_ORCHESTRATOR_SESSION_ID"]
	candidate, _, err := s.agentLaunchSpec(ctx, workspace, existing.SessionName, agent, role, orchestrationID)
	if err != nil {
		return false, err
	}
	if candidate == nil {
		return true, nil
	}
	return !slices.Equal(candidate.Argv, existing.Launch.Argv) ||
		candidate.Cwd != existing.Launch.Cwd ||
		!maps.Equal(candidate.Env, existing.Launch.Env), nil
}

func inactiveAgentTerminal(existing *TabMetadata) (*TabMetadata, error) {
	if existing == nil {
		return nil, terminalError(ErrInvalid, "agent is not running and has no terminal session", nil)
	}
	meta := *existing
	meta.Launch = nil
	meta.Writable = false
	return &meta, nil
}

func selectAgentTerminalTab(tabs []TabMetadata, agentID string) *TabMetadata {
	var newest *TabMetadata
	for index := range tabs {
		tab := &tabs[index]
		if tab.Kind != terminalKindAgent || tab.AgentID != agentID {
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

func (s *TerminalTabService) agentOrchestrationID(
	ctx context.Context,
	workspace string,
	agent *agents.RuntimeIdentity,
	roleKind string,
) (string, error) {
	if roleKind != agents.RoleKindInteractive || s.agentTerminal.Placement == nil {
		return "", nil
	}
	if existing, err := s.agentTerminal.Placement.FindActiveOrchestrationSession(
		ctx, workspace, agent.AgentID,
	); err == nil && strings.TrimSpace(existing) != "" {
		return existing, nil
	}
	value, err := NewUUID()
	if err != nil {
		return "", terminalError(ErrUnavailable, "failed to generate orchestration identity", err)
	}
	return "lead-" + value, nil
}

type resolvedTerminalLaunch struct {
	Launch  *LaunchSpec
	AgentID string
}

// resolveTerminalLaunch authorizes one terminal placement immediately before
// PTY attach. Agent desired-state and role policy stay in Interaction so a
// stored launch envelope cannot bypass Stop or expose a worker terminal.
func (s *TerminalTabService) resolveTerminalLaunch(
	ctx context.Context,
	key TerminalKey,
) (*resolvedTerminalLaunch, error) {
	workspace := strings.TrimSpace(key.WorkspaceKey)
	terminalID := strings.TrimSpace(key.TerminalID)
	if workspace == "" || terminalID == "" {
		return nil, terminalError(ErrInvalid, "workspace and terminal are required", nil)
	}
	meta, err := s.GetTab(ctx, workspace, terminalID)
	if err != nil {
		return nil, err
	}
	if meta.Kind != terminalKindAgent {
		if meta.Launch != nil && len(meta.Launch.Argv) > 0 {
			return &resolvedTerminalLaunch{Launch: cloneLaunchSpec(meta.Launch)}, nil
		}
		if s.runtime != nil && s.runtime.IsLive(TerminalKey{
			WorkspaceKey: workspace,
			TerminalID:   terminalID,
		}) {
			return &resolvedTerminalLaunch{}, nil
		}
		return nil, terminalError(ErrTerminalLaunchMissing, "terminal launch metadata missing", nil)
	}
	if strings.TrimSpace(meta.AgentID) == "" {
		return nil, terminalError(ErrInvalidPersistedState, "agent terminal identity is incomplete", nil)
	}
	agent, err := s.loadAgentRuntimeIdentity(ctx, workspace, meta.AgentID)
	if err != nil {
		return nil, err
	}
	role, err := s.loadAgentRole(ctx, workspace, agent.RoleName)
	if err != nil {
		return nil, err
	}
	if agents.ResolveRoleKind(role, agent.RoleName) != agents.RoleKindInteractive {
		return nil, terminalError(ErrAgentTerminalWorker,
			"background worker terminals cannot be launched directly; use worker logs or task session history", nil)
	}
	if agent.DesiredState != agents.DesiredRunning {
		return nil, terminalError(ErrAgentTerminalStopped,
			"agent terminal is stopped; start the agent before connecting", nil)
	}
	if meta.Launch == nil || len(meta.Launch.Argv) == 0 {
		return nil, terminalError(ErrTerminalLaunchMissing, "agent terminal launch metadata missing", nil)
	}
	return &resolvedTerminalLaunch{
		Launch:  cloneLaunchSpec(meta.Launch),
		AgentID: agent.AgentID,
	}, nil
}

func cloneLaunchSpec(value *LaunchSpec) *LaunchSpec {
	if value == nil {
		return nil
	}
	result := &LaunchSpec{Argv: append([]string(nil), value.Argv...), Cwd: value.Cwd}
	if value.Env != nil {
		result.Env = make(map[string]string, len(value.Env))
		for name, item := range value.Env {
			result.Env[name] = item
		}
	}
	return result
}

func (s *TerminalTabService) agentLaunchSpec(
	ctx context.Context,
	workspace, sessionName string,
	agent *agents.RuntimeIdentity,
	role *agents.Role,
	orchestrationID string,
) (*LaunchSpec, string, error) {
	if agents.ResolveRoleKind(role, agent.RoleName) != agents.RoleKindInteractive {
		return nil, "", terminalError(ErrInvalid,
			"background worker terminals cannot be launched directly; use worker logs or task session history", nil)
	}
	backend := strings.TrimSpace(agent.Backend)
	if backend == "" && role != nil {
		backend = strings.TrimSpace(role.Backend)
	}
	if backend == "" && s.agentTerminal.Placement != nil {
		backend = strings.TrimSpace(s.agentTerminal.Placement.DefaultBackend(ctx, workspace))
	}
	args := []string{LoomExecutableForTerminal()}
	if workspace != "" {
		args = append(args, "--workspace", workspace)
	}
	if backend != "" {
		args = append(args, "--backend", backend)
	}
	args = append(args, "lead")
	if role != nil && strings.TrimSpace(role.Prompt) == "" && strings.TrimSpace(role.PromptFile) != "" {
		args = append(args, "--prompt", role.PromptFile)
	}
	env := map[string]string{
		"LOOM_AGENT_NAME": agent.AgentID, "LOOM_AGENT_ROLE": agent.RoleName,
		"LOOM_AGENT_TERMINAL_ID": sessionName, "LOOM_WORKSPACE": workspace,
	}
	cwd := ""
	if s.agentTerminal.Placement != nil {
		if configDir := strings.TrimSpace(s.agentTerminal.Placement.ConfigDir()); configDir != "" {
			env["LOOM_CONFIG_DIR"] = configDir
		}
		cwd = s.agentTerminal.Placement.AgentWorktree(ctx, workspace, agent.AgentID)
	}
	if backend != "" {
		env["LOOM_BACKEND"] = backend
	}
	if orchestrationID = strings.TrimSpace(orchestrationID); orchestrationID != "" {
		env["LOOM_ORCHESTRATOR_SESSION_ID"] = orchestrationID
	}
	return &LaunchSpec{Argv: ShellArgvForCommand(args), Env: env, Cwd: cwd}, backend, nil
}

func newAgentTabMetadata(
	workspace, sessionName, label string,
	sortOrder int,
	agent *agents.RuntimeIdentity,
	backend string,
	launch *LaunchSpec,
	existing *TabMetadata,
) *TabMetadata {
	now := time.Now().UTC()
	meta := &TabMetadata{
		SessionName: sessionName, Workspace: workspace, Label: label,
		SortOrder: sortOrder, Pinned: existing != nil && existing.Pinned,
		Kind: terminalKindAgent, AgentID: agent.AgentID, Role: agent.RoleName,
		Backend: backend, Writable: true, Launch: launch, CreatedAt: now, UpdatedAt: now,
	}
	if existing != nil {
		meta.Notes, meta.IssueID = existing.Notes, existing.IssueID
	}
	return meta
}

func (s *TerminalTabService) pruneAgentTabs(
	ctx context.Context,
	workspace, agentID, keepSession string,
	tabs []TabMetadata,
) {
	for index := range tabs {
		tab := tabs[index]
		if tab.SessionName == keepSession || tab.Kind != terminalKindAgent || tab.AgentID != agentID || tab.PTYAlive {
			continue
		}
		if err := s.DeleteTab(ctx, workspace, tab.SessionName); err != nil {
			slog.Warn("failed to prune stale agent terminal tab",
				"workspace", workspace, "agent", agentID, "session", tab.SessionName, "err", err)
		}
	}
}
