package svcimpl

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	webuilog "github.com/tysonthomas9/loomcli/internal/webui/log"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// Compile-time check that agentServiceImpl satisfies service.AgentService.
var _ service.AgentService = (*agentServiceImpl)(nil)

// agentServiceImpl is the concrete implementation of AgentService.
type agentServiceImpl struct {
	gitOps   ops.GitOps
	termMgr  *terminal.AgentTmuxManager
	termAuth *realtime.TerminalAuth
	store    store.Store // fleet-db backed store; nil disables CRUD endpoints
}

// NewAgentService creates a new AgentService implementation.
// gitOps must be non-nil. termMgr (AgentTmuxManager) and termAuth may be nil;
// methods that require them return service.ErrUnavailable.
//
// Phase 4 of the loom -> fleet-db migration: store is the source of
// truth for agent CRUD endpoints. When nil, ListAgents / CreateAgent /
// UpdateAgent / DeleteAgent return service.ErrUnavailable.
func NewAgentService(gitOps ops.GitOps, termMgr *terminal.AgentTmuxManager, termAuth *realtime.TerminalAuth, st store.Store) service.AgentService {
	return &agentServiceImpl{
		gitOps:   gitOps,
		termMgr:  termMgr,
		termAuth: termAuth,
		store:    st,
	}
}

// agentLogTokenScope returns the token scope string for an agent's log stream.
func agentLogTokenScope(agentName string) string {
	return "agent:" + agentName + ":logs"
}

// resolveAgentWorktree validates the agent name and resolves the worktree.
func (s *agentServiceImpl) resolveAgentWorktree(wsID, agentName string) (*ops.AgentWorktree, error) {
	if err := validateAgentName(agentName); err != nil {
		return nil, err
	}
	wt, err := s.gitOps.ResolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", agentName))
	}
	return wt, nil
}

func (s *agentServiceImpl) GetTerminalInfo(_ context.Context, wsID, agentName string) (*service.AgentTerminalInfoResult, error) {
	if err := validateAgentName(agentName); err != nil {
		return nil, err
	}
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	mode := service.AgentTerminalModeArchive
	if _, found, err := s.termMgr.FindLatestAgentSession(wsID, agentName); err != nil {
		logger.Error("failed to resolve agent tmux session", "agent", agentName, "err", err)
		return nil, service.ErrInternal("failed to inspect terminal sessions", err)
	} else if found {
		mode = service.AgentTerminalModeTmux
	}

	return &service.AgentTerminalInfoResult{Agent: agentName, Mode: mode}, nil
}

func (s *agentServiceImpl) GenerateTerminalToken(_ context.Context, wsID, agentName, userID string) (string, error) {
	if err := validateAgentName(agentName); err != nil {
		return "", err
	}
	if s.termAuth == nil {
		return "", service.ErrUnavailable("terminal authentication not initialized")
	}

	token, err := s.termAuth.GenerateToken(agentLogTokenScope(agentName), wsID, userID)
	if err != nil {
		logger.Error("failed to generate agent terminal token", "agent", agentName, "workspace", wsID, "err", err)
		return "", service.ErrInternal("failed to generate token", err)
	}
	return token, nil
}

func (s *agentServiceImpl) GetLog(_ context.Context, wsID, agentName string, lines int, beforeLine int64) (*service.AgentLogResult, error) {
	if err := validateAgentName(agentName); err != nil {
		return nil, err
	}

	// Clamp lines to valid range
	if lines <= 0 {
		lines = webuilog.LogReadDefaultLines
	}
	if lines > webuilog.LogReadMaxLines {
		lines = webuilog.LogReadMaxLines
	}

	logPath, err := webuilog.GetAgentLogPath(wsID, agentName)
	if err != nil {
		logger.Error("agent log path error", "agent", agentName, "err", err)
		return nil, service.ErrInternal("failed to resolve log path", err)
	}

	if !webuilog.FileExists(logPath) {
		return nil, service.ErrNotFound("log file not found - agent may not be active")
	}

	content, startLine, err := webuilog.ReadFileLastLines(logPath, lines, beforeLine)
	if err != nil {
		logger.Error("failed to read agent log", "agent", agentName, "err", err)
		return nil, service.ErrInternal("failed to read log file", err)
	}

	return &service.AgentLogResult{
		Lines:     content,
		LineCount: startLine + int64(len(content)) - 1,
		StartLine: startLine,
	}, nil
}

func (s *agentServiceImpl) GetDiffStat(_ context.Context, wsID, agentName string) (*service.AgentDiffStatResult, error) {
	wt, err := s.resolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, err
	}

	stats := s.gitOps.DiffStat(wt.Path, wt.DefaultBranch)
	return &service.AgentDiffStatResult{
		Branch:  wt.Branch,
		Added:   stats.LinesAdded,
		Removed: stats.LinesRemoved,
	}, nil
}

func (s *agentServiceImpl) GitPush(_ context.Context, wsID, agentName, target string) (*ops.GitPushResult, error) {
	wt, err := s.resolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, err
	}

	if target == "" {
		target = wt.DefaultBranch
	}

	result, err := s.gitOps.Push(wt.Path, wt.Branch, target, wt.Remote)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *agentServiceImpl) GitPushAll(_ context.Context, wsID string) (*service.GitPushAllResult, error) {
	worktrees, err := s.gitOps.ListAgentWorktrees(wsID)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}

	var results []service.GitPushAllWorktreeResult
	pushed, failed := 0, 0

	for _, wt := range worktrees {
		r, ok := s.pushOneWorktree(wt)
		results = append(results, r)
		switch {
		case ok:
			pushed++
		case r.Error != "":
			failed++
		}
	}

	return &service.GitPushAllResult{Results: results, Pushed: pushed, Failed: failed}, nil
}

// pushOneWorktree pushes a single worktree and returns the result.
// The bool indicates whether the push was a successful new push.
func (s *agentServiceImpl) pushOneWorktree(wt ops.AgentWorktree) (service.GitPushAllWorktreeResult, bool) {
	remote := wt.Remote
	if remote == "" {
		remote = "origin"
	}
	result, pushErr := s.gitOps.Push(wt.Path, wt.Branch, wt.DefaultBranch, remote)
	if pushErr != nil {
		return service.GitPushAllWorktreeResult{Name: wt.Name, Error: pushErr.Error()}, false
	}
	if result.AlreadyUpToDate {
		return service.GitPushAllWorktreeResult{Name: wt.Name, Success: true, Message: "already up to date"}, false
	}
	if !result.Success {
		return service.GitPushAllWorktreeResult{Name: wt.Name, Error: result.Message}, false
	}
	return service.GitPushAllWorktreeResult{Name: wt.Name, Success: true, Message: result.Message}, true
}

func (s *agentServiceImpl) GitPull(_ context.Context, wsID, agentName, source string) (*ops.GitPullResult, error) {
	wt, err := s.resolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, err
	}

	if source == "" {
		source = wt.DefaultBranch
	}

	currentBranch, err := s.gitOps.GetCurrentBranch(wt.Path)
	if err != nil {
		return nil, fmt.Errorf("getting current branch: %w", err)
	}

	result, err := s.gitOps.Pull(wt.Path, currentBranch, source, wt.Remote)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *agentServiceImpl) GitSync(_ context.Context, wsID, agentName string) (*service.GitSyncResult, error) {
	wt, err := s.resolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, err
	}

	target := wt.DefaultBranch

	pushResult, err := s.gitOps.Push(wt.Path, wt.Branch, target, wt.Remote)
	if err != nil {
		return nil, fmt.Errorf("push failed: %w", err)
	}

	// If push resulted in conflicts, return immediately with partial result
	if !pushResult.Success && len(pushResult.ConflictedFiles) > 0 {
		return &service.GitSyncResult{PushResult: pushResult}, nil
	}

	currentBranch, err := s.gitOps.GetCurrentBranch(wt.Path)
	if err != nil {
		return nil, fmt.Errorf("getting current branch: %w", err)
	}

	pullResult, err := s.gitOps.Pull(wt.Path, currentBranch, target, wt.Remote)
	if err != nil {
		return nil, fmt.Errorf("pull failed: %w", err)
	}

	return &service.GitSyncResult{
		PushResult: pushResult,
		PullResult: pullResult,
	}, nil
}

func (s *agentServiceImpl) ListPullRequests(_ context.Context, wsID, state string) (*ops.GitPullRequestList, error) {
	if err := s.gitOps.CheckGhInstalled(); err != nil {
		// GitHub metadata is an enrichment, not a hard dependency — report
		// the missing CLI as a warning so loom-backed views keep working.
		return &ops.GitPullRequestList{
			PullRequests: []ops.GitPullRequest{},
			Warnings:     []string{"gh CLI not installed: install from https://cli.github.com/ and run 'gh auth login'"},
		}, nil
	}
	if state == "" {
		state = "all"
	}
	return s.gitOps.ListWorkspacePullRequests(wsID, state, 100)
}

func (s *agentServiceImpl) CreatePR(_ context.Context, wsID, agentName, target string) (*ops.GitPRResult, error) {
	if err := s.gitOps.CheckGhInstalled(); err != nil {
		return nil, service.ErrUnavailable("gh CLI not installed: install from https://cli.github.com/ and run 'gh auth login'")
	}

	wt, err := s.resolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, err
	}

	if target == "" {
		target = wt.DefaultBranch
	}

	result, err := s.gitOps.CreatePR(wt.Path, wt.Branch, target, wt.Remote)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *agentServiceImpl) GitReset(_ context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error) {
	wt, err := s.resolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, err
	}

	if branch == "" {
		branch = wt.DefaultBranch
	}

	result, err := s.gitOps.Reset(wt.Path, wt.Name, branch, force, push)
	if err != nil {
		return nil, err // ops.GitResetLockedError passes through
	}
	return result, nil
}

func (s *agentServiceImpl) GitStatus(_ context.Context, wsID, agentName string) (*ops.GitStatusResult, error) {
	wt, err := s.resolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, err
	}

	result, err := s.gitOps.Status(wt.Path, wt.DefaultBranch)
	if err != nil {
		return nil, fmt.Errorf("getting git status: %w", err)
	}
	return result, nil
}

// ListAgents returns all agents registered for the workspace via the
// fleet-db store. The derived live status (live_status/active_task_id/
// active_phase) is computed by fleet-db and carried through on each record;
// loom does not derive it. Returns ErrUnavailable when no store handle was
// provided at construction time.
func (s *agentServiceImpl) ListAgents(ctx context.Context, wsKey string) ([]*domain.Agent, error) {
	if s.store == nil {
		return nil, service.ErrUnavailable("fleet-db store not configured")
	}
	if wsKey == "" {
		return nil, service.ErrValidation("workspace key required")
	}
	agents, err := s.store.Agents().List(ctx, wsKey)
	if err != nil {
		return nil, service.ErrInternal("list agents", err)
	}
	return agents, nil
}

// CreateAgent registers a new agent assignment in the fleet-db store.
func (s *agentServiceImpl) CreateAgent(ctx context.Context, in service.AgentCreateInput) (*domain.Agent, error) {
	if s.store == nil {
		return nil, service.ErrUnavailable("fleet-db store not configured")
	}
	in.RoleName = normalizeFirstClassAgentRole(in.RoleName)
	in.Name = normalizeStoredAgentName(in.Name)
	if err := validateAgentCreateInput(in); err != nil {
		return nil, err
	}
	if err := s.ensureFirstClassAgentRole(ctx, in.WorkspaceKey, in.RoleName); err != nil {
		return nil, err
	}
	created, err := s.store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:     in.WorkspaceKey,
		Name:             in.Name,
		RoleName:         in.RoleName,
		Auto:             in.Auto,
		Backend:          in.Backend,
		FallbackBackends: in.FallbackBackends,
		Repos:            in.Repos,
		RepoGroups:       in.RepoGroups,
		CrossRepo:        in.CrossRepo,
		Parent:           in.Parent,
		DesiredState:     in.DesiredState,
	})
	if err != nil {
		return nil, classifyStoreError("create agent", err)
	}
	if err := s.ensureLocalAgentWorktrees(ctx, *created); err != nil {
		_ = s.store.Agents().Delete(ctx, created.WorkspaceKey, created.Name)
		return nil, err
	}
	return created, nil
}

func (s *agentServiceImpl) ensureLocalAgentWorktrees(ctx context.Context, agent domain.Agent) error {
	role, err := s.loadAgentRoleForKind(ctx, agent.WorkspaceKey, agent.RoleName)
	if err != nil {
		return err
	}
	if domain.ResolveRoleKind(role, agent.RoleName) == domain.RoleKindInteractive {
		return nil
	}
	ws, err := storeadapter.BuildWorkspaceDataForKey(ctx, s.store, agent.WorkspaceKey)
	if err != nil {
		return service.ErrInternal("load workspace for agent worktree", err)
	}
	if ws.Path == "" {
		// Distributed/cloud workspaces can be managed by this server without a
		// checkout mounted locally. In that shape the agent assignment is still
		// valid fleet-db state; local worktrees are created only on machines that
		// have workspace paths.
		return nil
	}
	localRepos := make([]localworkspace.Repo, 0, len(ws.Repos))
	for _, repo := range ws.Repos {
		localRepos = append(localRepos, localworkspace.Repo{
			Name:   repo.Name,
			Path:   repo.Path,
			Groups: append([]string(nil), repo.Groups...),
		})
	}
	repos, err := localworkspace.SelectAgentRepos(localRepos, agent)
	if err != nil {
		return service.ErrValidation(err.Error())
	}
	if len(repos) == 0 {
		return service.ErrValidation("workspace has no repos for agent")
	}
	createdPaths := make(map[string]string, len(repos))
	for _, repo := range repos {
		if repo.Path == "" {
			return service.ErrValidation(fmt.Sprintf("repo %q has no local path on this machine", repo.Name))
		}
		target := localworkspace.AgentWorktreePath(ws.Path, repo.Name, agent.Name)
		if err := localworkspace.EnsureGitWorktree(repo.Path, target, agent.Name); err != nil {
			return service.ErrInternal(fmt.Sprintf("create worktree for repo %q", repo.Name), err)
		}
		createdPaths[repo.Name] = target
	}
	if err := localworkspace.RememberAgentWorktree(agent.WorkspaceKey, agent.Name, localworkspace.FirstWorktreePath(createdPaths)); err != nil {
		return service.ErrInternal("update local agent state", err)
	}
	return nil
}

func (s *agentServiceImpl) loadAgentRoleForKind(ctx context.Context, workspaceKey, roleName string) (*domain.Role, error) {
	role, err := s.store.Roles().Get(ctx, workspaceKey, roleName)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, service.ErrInternal("load agent role", err)
	}
	return role, nil
}

func (s *agentServiceImpl) ensureFirstClassAgentRole(ctx context.Context, workspaceKey, roleName string) error {
	if !isLeadAgentRole(roleName) {
		return nil
	}
	if _, err := s.store.Roles().Get(ctx, workspaceKey, roleName); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return service.ErrInternal("load lead role", err)
	}
	if _, err := s.store.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: workspaceKey,
		Name:         roleName,
		Kind:         string(domain.RoleKindInteractive),
		Description:  "Lead/orchestrator interactive",
	}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		return classifyStoreError("create lead role", err)
	}
	return nil
}

func normalizeFirstClassAgentRole(roleName string) string {
	normalized := strings.ToLower(strings.TrimSpace(roleName))
	switch normalized {
	case "lead", "orchestrator":
		return normalized
	default:
		return roleName
	}
}

func isLeadAgentRole(roleName string) bool {
	return domain.IsInteractiveRoleName(roleName)
}

// UpdateAgent applies a partial update to an existing agent.
func (s *agentServiceImpl) UpdateAgent(ctx context.Context, wsKey, name string, patch service.AgentUpdateInput) (*domain.Agent, error) {
	if s.store == nil {
		return nil, service.ErrUnavailable("fleet-db store not configured")
	}
	name = normalizeStoredAgentName(name)
	if err := validateStoredAgentName(name); err != nil {
		return nil, err
	}
	updated, err := s.store.Agents().Update(ctx, wsKey, name, store.AgentUpdate{
		RoleName:         patch.RoleName,
		Auto:             patch.Auto,
		Backend:          patch.Backend,
		FallbackBackends: patch.FallbackBackends,
		Repos:            patch.Repos,
		RepoGroups:       patch.RepoGroups,
		CrossRepo:        patch.CrossRepo,
		Parent:           patch.Parent,
		State:            patch.State,
		DesiredState:     patch.DesiredState,
	})
	if err != nil {
		return nil, classifyStoreError("update agent", err)
	}
	return updated, nil
}

// RequestAgentLifecycle updates FleetDB state and creates a queued command for
// the daemon poller that owns this workspace.
func (s *agentServiceImpl) RequestAgentLifecycle(ctx context.Context, wsKey, name string, in service.AgentLifecycleInput) (*domain.Agent, error) {
	if s.store == nil {
		return nil, service.ErrUnavailable("fleet-db store not configured")
	}
	name = normalizeStoredAgentName(name)
	if err := validateStoredAgentName(name); err != nil {
		return nil, err
	}
	if err := validateAgentCommandType(in.CommandType); err != nil {
		return nil, err
	}
	updated, err := s.UpdateAgent(ctx, wsKey, name, service.AgentUpdateInput{
		State:        &in.State,
		DesiredState: &in.DesiredState,
	})
	if err != nil {
		return nil, err
	}
	if s.store.AgentCommands() == nil {
		return updated, nil
	}
	if _, err := s.store.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  wsKey,
		TargetAgentID: name,
		Type:          in.CommandType,
		Payload:       in.Payload,
	}); err != nil {
		return nil, classifyStoreError("create agent command", err)
	}
	return updated, nil
}

// DeleteAgent removes an agent assignment from the fleet-db store.
func (s *agentServiceImpl) DeleteAgent(ctx context.Context, wsKey, name string) error {
	if s.store == nil {
		return service.ErrUnavailable("fleet-db store not configured")
	}
	name = normalizeStoredAgentName(name)
	if err := validateStoredAgentName(name); err != nil {
		return err
	}
	if err := s.store.Agents().Delete(ctx, wsKey, name); err != nil {
		return classifyStoreError("delete agent", err)
	}
	return nil
}

func validateAgentCommandType(commandType string) error {
	switch commandType {
	case "start", "stop", "restart", "yield":
		return nil
	default:
		return service.ErrValidation("invalid agent command type")
	}
}

// validateAgentCreateInput checks required fields on an agent create payload.
func validateAgentCreateInput(in service.AgentCreateInput) error {
	if in.WorkspaceKey == "" {
		return service.ErrValidation("workspace_key required")
	}
	if err := validateStoredAgentName(in.Name); err != nil {
		return err
	}
	if in.RoleName == "" {
		return service.ErrValidation("role_name required")
	}
	return nil
}

func (s *agentServiceImpl) SetTargetBranch(_ context.Context, wsID, agentName, branch string) error {
	wt, err := s.resolveAgentWorktree(wsID, agentName)
	if err != nil {
		return err
	}

	if !wt.IsWorkspace {
		return service.ErrValidation("target branch update only supported in workspace mode")
	}

	if err := s.gitOps.SetRepoDefaultBranch(wsID, wt.RepoName, branch); err != nil {
		return fmt.Errorf("updating target branch: %w", err)
	}
	return nil
}
