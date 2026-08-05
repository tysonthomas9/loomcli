package svcimpl

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/agentscompat"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
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
	gitOps             ops.GitOps
	termMgr            *terminal.AgentTmuxManager
	termAuth           *realtime.TerminalAuth
	interactiveRuntime InteractiveRuntimeController
	store              store.Store // fleet-db backed store; nil disables CRUD endpoints
	compatibility      agents.CompatibilityAPI
	managed            agentscompat.ManagedCommands
	retirements        agentscompat.ManagedRetirements
}

// NewAgentService creates a new AgentService implementation.
// gitOps must be non-nil. termMgr (AgentTmuxManager) and termAuth may be nil;
// methods that require them return service.ErrUnavailable.
//
// Phase 4 of the loom -> fleet-db migration: store is the source of
// truth for agent CRUD endpoints. When nil, ListAgents / CreateAgent /
// UpdateAgent / DeleteAgent return service.ErrUnavailable.
func NewAgentService(gitOps ops.GitOps, termMgr *terminal.AgentTmuxManager, termAuth *realtime.TerminalAuth, st store.Store) service.AgentService {
	return NewAgentServiceWithInteractiveRuntime(gitOps, termMgr, termAuth, st, nil)
}

// NewAgentServiceWithInteractiveRuntime creates an AgentService with the
// process-local controller that owns UI-launched interactive PTYs. The
// compatibility constructor above remains available for consumers that do not
// serve interactive terminals.
func NewAgentServiceWithInteractiveRuntime(
	gitOps ops.GitOps,
	termMgr *terminal.AgentTmuxManager,
	termAuth *realtime.TerminalAuth,
	st store.Store,
	interactiveRuntime InteractiveRuntimeController,
) service.AgentService {
	return NewAgentServiceWithCompatibility(
		gitOps,
		termMgr,
		termAuth,
		st,
		interactiveRuntime,
		nil,
		nil,
		nil,
	)
}

// NewAgentServiceWithCompatibility is the production composition path for
// supervised assignments. Mutations fail closed unless the owner API and
// narrow managed-role workflow are both supplied.
func NewAgentServiceWithCompatibility(
	gitOps ops.GitOps,
	termMgr *terminal.AgentTmuxManager,
	termAuth *realtime.TerminalAuth,
	st store.Store,
	interactiveRuntime InteractiveRuntimeController,
	compatibility agents.CompatibilityAPI,
	managed agentscompat.ManagedCommands,
	retirements agentscompat.ManagedRetirements,
) service.AgentService {
	return &agentServiceImpl{
		gitOps:             gitOps,
		termMgr:            termMgr,
		termAuth:           termAuth,
		interactiveRuntime: interactiveRuntime,
		store:              st,
		compatibility:      compatibility,
		managed:            managed,
		retirements:        retirements,
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
	return s.gitOps.ListWorkspacePullRequests(wsID, state, 500)
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
	if s.store == nil || s.compatibility == nil || s.managed == nil {
		return nil, service.ErrUnavailable("Agents compatibility commands not configured")
	}
	in.RoleName = normalizeFirstClassAgentRole(in.RoleName)
	in.Name = normalizeStoredAgentName(in.Name)
	in.Kind = normalizeAgentRoleKind(in.Kind)
	in.Prompt = strings.TrimSpace(in.Prompt)
	in.PromptFile = strings.TrimSpace(in.PromptFile)
	if strings.TrimSpace(in.Parent) != "" {
		return nil, service.ErrValidation("parent is runtime-owned and cannot be set during agent creation")
	}
	if err := validateAgentCreateInput(in); err != nil {
		return nil, err
	}
	operatorAuth, ok := service.AgentOperatorAuthorityFromContext(ctx)
	if !ok {
		return nil, service.ErrForbidden("verified operator authority is required")
	}
	roleReceipt, err := s.ensureAgentRole(ctx, in.WorkspaceKey, in.RoleName, in.Kind, in.Prompt, in.PromptFile)
	if err != nil {
		return nil, err
	}
	created, err := s.compatibility.CreateSupervisedAssignment(
		ctx,
		operatorAuth,
		agents.CreateSupervisedAssignmentCommand{
			WorkspaceKey:     in.WorkspaceKey,
			AgentName:        in.Name,
			RoleName:         in.RoleName,
			Auto:             in.Auto,
			Backend:          in.Backend,
			FallbackBackends: in.FallbackBackends,
			Repos:            in.Repos,
			RepoGroups:       in.RepoGroups,
			CrossRepo:        in.CrossRepo,
			DesiredState:     agents.SupervisedAssignmentDesiredState(in.DesiredState),
		},
	)
	if err != nil {
		s.compensateAgentRole(ctx, roleReceipt)
		return nil, classifyStoreError("create agent", err)
	}
	createdDomain := supervisedAssignmentToDomain(created)
	if err := s.ensureLocalAgentWorktrees(ctx, *createdDomain); err != nil {
		s.compensateFailedAgentCreation(ctx, created, roleReceipt)
		return nil, err
	}
	return createdDomain, nil
}

func (s *agentServiceImpl) compensateFailedAgentCreation(
	ctx context.Context,
	created *agents.SupervisedAssignment,
	roleReceipt agentRoleCreateReceipt,
) {
	var deleteErr error
	cleanupCtx := context.WithoutCancel(ctx)
	if s.retirements == nil {
		deleteErr = agents.ErrUnavailable
	} else {
		deleteErr = s.retirements.RetireManagedAssignment(
			cleanupCtx,
			agents.RetireSupervisedAssignmentCommand{
				WorkspaceKey: created.WorkspaceKey,
				AgentName:    created.Name,
			},
			"compensate failed supervised assignment creation "+created.Name,
		)
	}
	if deleteErr != nil {
		logger.Warn("agent create: assignment compensation failed",
			"workspace", created.WorkspaceKey, "agent", created.Name, "err", deleteErr)
		return
	}
	s.compensateAgentRole(cleanupCtx, roleReceipt)
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
	repos, err := selectLocalAgentRepos(ws.Repos, agent)
	if err != nil {
		return err
	}
	createdPaths := make(map[string]string, len(repos))
	for _, repo := range repos {
		target := localworkspace.AgentWorktreePath(ws.Path, repo.Name, agent.Name)
		if err := localworkspace.EnsureGitWorktree(repo.Path, target, agent.Name); err != nil {
			// Keep earlier worktrees. They may already have been adopted, and
			// path-based force removal cannot be fenced against concurrent
			// edits. The stable path/branch makes them safe for an exact retry.
			return service.ErrInternal(fmt.Sprintf("create worktree for repo %q", repo.Name), err)
		}
		createdPaths[repo.Name] = target
	}
	if err := localworkspace.RememberAgentWorktree(agent.WorkspaceKey, agent.Name, localworkspace.FirstWorktreePath(createdPaths)); err != nil {
		return service.ErrInternal("update local agent state", err)
	}
	return nil
}

func selectLocalAgentRepos(workspaceRepos []ops.WorkspaceRepo, agent domain.Agent) ([]localworkspace.Repo, error) {
	localRepos := make([]localworkspace.Repo, 0, len(workspaceRepos))
	for _, repo := range workspaceRepos {
		localRepos = append(localRepos, localworkspace.Repo{
			Name:   repo.Name,
			Path:   repo.Path,
			Groups: append([]string(nil), repo.Groups...),
		})
	}
	repos, err := localworkspace.SelectAgentRepos(localRepos, agent)
	if err != nil {
		return nil, service.ErrValidation(err.Error())
	}
	if len(repos) == 0 {
		return nil, service.ErrValidation("workspace has no repos for agent")
	}
	for _, repo := range repos {
		if repo.Path == "" {
			return nil, service.ErrValidation(fmt.Sprintf("repo %q has no local path on this machine", repo.Name))
		}
	}
	return repos, nil
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

type agentRoleCreateReceipt struct {
	role    *domain.Role
	created bool
}

func (s *agentServiceImpl) ensureAgentRole(
	ctx context.Context,
	workspaceKey, roleName, kind, prompt, promptFile string,
) (agentRoleCreateReceipt, error) {
	if existing, err := s.store.Roles().Get(ctx, workspaceKey, roleName); err == nil {
		return agentRoleCreateReceipt{}, reconcileExistingAgentRole(existing, roleName, kind, prompt, promptFile)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return agentRoleCreateReceipt{}, service.ErrInternal("load agent role", err)
	}

	resolved := domain.ResolveRoleKind(&domain.Role{Kind: domain.RoleKind(kind)}, roleName)
	if kind == string(domain.RoleKindWorker) {
		return agentRoleCreateReceipt{}, service.ErrValidation(fmt.Sprintf("role %q must exist before creating a worker agent", roleName))
	}
	if resolved != domain.RoleKindInteractive {
		return agentRoleCreateReceipt{}, nil
	}

	description := "Interactive terminal agent"
	if isLeadAgentRole(roleName) {
		description = "Lead/orchestrator interactive"
	}
	if s.managed == nil {
		return agentRoleCreateReceipt{}, service.ErrUnavailable("Agents role commands unavailable")
	}
	_, err := s.managed.EnsureRole(ctx, agents.EnsureRoleCommand{
		RequestID:    "interactive-agent-role:" + workspaceKey + ":" + roleName,
		WorkspaceKey: workspaceKey,
		Role: agents.RoleDefinition{
			Name: roleName, Kind: string(domain.RoleKindInteractive),
			Description: description, Prompt: prompt, PromptFile: promptFile,
		},
	})
	if err != nil {
		if !errors.Is(err, agents.ErrConflict) && !errors.Is(err, domain.ErrAlreadyExists) {
			return agentRoleCreateReceipt{}, classifyStoreError("create agent role", err)
		}
		existing, getErr := s.store.Roles().Get(ctx, workspaceKey, roleName)
		if getErr != nil {
			return agentRoleCreateReceipt{}, classifyStoreError("load concurrently created agent role", getErr)
		}
		return agentRoleCreateReceipt{}, reconcileExistingAgentRole(existing, roleName, kind, prompt, promptFile)
	}
	persisted, err := s.store.Roles().Get(ctx, workspaceKey, roleName)
	if err != nil {
		return agentRoleCreateReceipt{}, classifyStoreError("load created agent role", err)
	}
	return agentRoleCreateReceipt{role: persisted, created: true}, nil
}

func (s *agentServiceImpl) compensateAgentRole(ctx context.Context, receipt agentRoleCreateReceipt) {
	if !receipt.created || receipt.role == nil {
		return
	}
	// Keep the role as retryable scaffolding. RoleStore has no conditional
	// delete, so check-then-delete could remove a concurrently edited,
	// recreated, or newly adopted role.
	logger.DebugContext(ctx, "agent create: retaining role after later failure",
		"workspace", receipt.role.WorkspaceKey, "role", receipt.role.Name)
}

// reconcileExistingAgentRole guards against silently launching an interactive
// agent under a pre-existing role of a different kind or prompt. Agent creation
// never mutates an existing role — so when the caller explicitly asks for an
// interactive role (e.g. a custom-file agent whose name collides with the
// seeded "task"/"plan" worker roles) that conflicts with the stored role, we
// surface the conflict instead of quietly running the agent as the wrong kind.
func reconcileExistingAgentRole(existing *domain.Role, roleName, kind, prompt, promptFile string) error {
	if kind != string(domain.RoleKindInteractive) {
		return nil
	}
	if domain.ResolveRoleKind(existing, roleName) != domain.RoleKindInteractive {
		return service.ErrValidation(fmt.Sprintf("role %q already exists and is not interactive; choose a different agent name", roleName))
	}
	if p := strings.TrimSpace(prompt); p != "" && strings.TrimSpace(existing.Prompt) != p {
		return service.ErrValidation(fmt.Sprintf("role %q already exists with a different prompt; choose a different agent name or reuse its prompt", roleName))
	}
	if pf := strings.TrimSpace(promptFile); pf != "" && strings.TrimSpace(existing.PromptFile) != pf {
		return service.ErrValidation(fmt.Sprintf("role %q already exists with a different prompt; choose a different agent name or reuse its prompt", roleName))
	}
	return nil
}

func normalizeAgentRoleKind(kind string) string {
	return strings.ToLower(strings.TrimSpace(kind))
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
	if s.store == nil || s.compatibility == nil {
		return nil, service.ErrUnavailable("Agents compatibility commands not configured")
	}
	name = normalizeStoredAgentName(name)
	if err := validateStoredAgentName(name); err != nil {
		return nil, err
	}
	if patch.State != nil {
		return nil, service.ErrValidation("state is runtime-owned and cannot be patched")
	}
	if patch.Parent != nil {
		return nil, service.ErrValidation("parent is execution-owned and cannot be patched")
	}
	operatorAuth, ok := service.AgentOperatorAuthorityFromContext(ctx)
	if !ok {
		return nil, service.ErrForbidden("verified operator authority is required")
	}
	updated, err := s.compatibility.UpdateSupervisedAssignmentIntent(
		ctx,
		operatorAuth,
		agents.UpdateSupervisedAssignmentIntentCommand{
			WorkspaceKey: wsKey,
			AgentName:    name,
			Patch: agents.SupervisedAssignmentIntentPatch{
				RoleName:         patch.RoleName,
				Auto:             patch.Auto,
				Backend:          patch.Backend,
				FallbackBackends: patch.FallbackBackends,
				Repos:            patch.Repos,
				RepoGroups:       patch.RepoGroups,
				CrossRepo:        patch.CrossRepo,
				DesiredState: supervisedAssignmentDesiredStatePointer(
					patch.DesiredState,
				),
			},
		},
	)
	if err != nil {
		return nil, classifyStoreError("update agent", err)
	}
	return supervisedAssignmentToDomain(updated), nil
}

func supervisedAssignmentDesiredStatePointer(
	value *domain.AgentDesiredState,
) *agents.SupervisedAssignmentDesiredState {
	if value == nil {
		return nil
	}
	converted := agents.SupervisedAssignmentDesiredState(*value)
	return &converted
}

func supervisedAssignmentToDomain(value *agents.SupervisedAssignment) *domain.Agent {
	if value == nil {
		return nil
	}
	return &domain.Agent{
		WorkspaceKey:     value.WorkspaceKey,
		Name:             value.Name,
		RoleName:         value.RoleName,
		Auto:             value.Auto,
		Backend:          value.Backend,
		FallbackBackends: slices.Clone(value.FallbackBackends),
		Repos:            slices.Clone(value.Repos),
		RepoGroups:       slices.Clone(value.RepoGroups),
		CrossRepo:        value.CrossRepo,
		Parent:           value.Parent,
		State:            domain.AgentState(value.State),
		Mode:             domain.AgentMode(value.Mode),
		TaskFilter:       value.TaskFilter,
		MaxConcurrency:   value.MaxConcurrency,
		BudgetPolicy:     value.BudgetPolicy,
		DesiredState:     domain.AgentDesiredState(value.DesiredState),
		CreatedAt:        value.CreatedAt,
		UpdatedAt:        value.UpdatedAt,
		LiveStatus:       domain.AgentLiveStatus(value.LiveStatus),
		ActiveTaskID:     value.ActiveTaskID,
		ActivePhase:      value.ActivePhase,
		LastErrorClass:   value.LastErrorClass,
	}
}

func (s *agentServiceImpl) requestInteractiveAgentLifecycle(
	ctx context.Context,
	wsKey string,
	agent *domain.Agent,
	in service.AgentLifecycleInput,
) (*domain.Agent, error) {
	unlock := terminal.LockAgentLifecycle(wsKey, agent.Name)
	defer unlock()

	switch in.CommandType {
	case "yield":
		return nil, service.ErrValidation("interactive agents do not support yield; use stop")
	case "stop", "restart":
		if _, err := s.terminateInteractiveRuntime(ctx, wsKey, agent); err != nil {
			return nil, err
		}
	case "start":
		// Interactive Start is placement-local: marking the assignment active
		// allows the terminal-session endpoint to launch or attach the PTY.
		// There is intentionally no daemon command.
	default:
		return nil, service.ErrValidation("invalid interactive agent lifecycle action")
	}

	updated, err := s.UpdateAgent(ctx, wsKey, agent.Name, service.AgentUpdateInput{
		DesiredState: &in.DesiredState,
	})
	if err != nil {
		return nil, err
	}
	// Runtime state is placement-owned. Preserve the existing response shape
	// without writing the transitional projection.
	updated.State = in.State
	return updated, nil
}

func (s *agentServiceImpl) terminateInteractiveRuntime(
	ctx context.Context,
	wsKey string,
	agent *domain.Agent,
) ([]terminal.SessionKey, error) {
	if s.interactiveRuntime == nil {
		return nil, service.ErrUnavailable("interactive terminal runtime is not configured")
	}
	ownedByKey, owned, err := s.interactiveRuntimeOwnership(ctx, wsKey, agent.Name)
	if err != nil {
		return nil, err
	}
	if err := s.validateActiveInteractiveRuntimeOwnership(ctx, wsKey, agent.Name, ownedByKey, owned); err != nil {
		return nil, err
	}
	ownedKeys := make([]terminal.SessionKey, 0, len(owned))
	for _, runtimeSession := range owned {
		ownedKeys = append(ownedKeys, runtimeSession.Key)
	}
	if err := s.killInteractiveRuntimeKeys(ownedKeys); err != nil {
		return nil, err
	}
	return ownedKeys, nil
}

func (s *agentServiceImpl) interactiveRuntimeOwnership(
	ctx context.Context,
	wsKey string,
	agentName string,
) (map[terminal.SessionKey]struct{}, []InteractiveRuntimeSession, error) {
	owned, err := s.interactiveRuntime.OwnedAgentSessions(ctx, wsKey, agentName)
	if err != nil {
		return nil, nil, err
	}
	ownedByKey := make(map[terminal.SessionKey]struct{}, len(owned))
	for _, runtimeSession := range owned {
		ownedByKey[runtimeSession.Key] = struct{}{}
	}
	return ownedByKey, owned, nil
}

func (s *agentServiceImpl) validateActiveInteractiveRuntimeOwnership(
	ctx context.Context,
	wsKey string,
	agentName string,
	ownedByKey map[terminal.SessionKey]struct{},
	owned []InteractiveRuntimeSession,
) error {
	canonical := make(map[string]string, len(owned))
	for _, runtimeSession := range owned {
		sessionID := strings.TrimSpace(runtimeSession.InteractionSessionID)
		terminalID := strings.TrimSpace(runtimeSession.InteractionTerminalID)
		if sessionID != "" && terminalID != "" {
			canonical[sessionID] = terminalID
		}
	}

	for _, kind := range []domain.AgentSessionKind{
		domain.AgentSessionKindInteractive,
		domain.AgentSessionKindOrchestration,
	} {
		sessions, err := s.store.AgentSessions().List(ctx, wsKey, store.AgentSessionFilter{
			AgentID: agentName,
			Kind:    kind,
			Limit:   100,
		})
		if err != nil {
			return service.ErrInternal("list interactive agent sessions", err)
		}
		for _, session := range sessions {
			if !isActiveInteractiveSession(session) {
				continue
			}
			if session.TerminalID == "" {
				return service.ErrConflict("interactive runtime is not owned by a web terminal")
			}
			if kind == domain.AgentSessionKindInteractive {
				if terminalID, processOwned := canonical[session.SessionID]; !processOwned ||
					terminalID != session.TerminalID {
					return service.ErrConflict("canonical interactive runtime is not owned by this agent on this server")
				}
				continue
			}
			// Phase-4 compatibility rows used TerminalID as the PTY tab key.
			// Canonical Interaction rows above use a distinct random terminal
			// identity and are matched through persisted session/terminal IDs.
			key := terminal.SessionKey{Workspace: wsKey, Name: session.TerminalID}
			if _, processOwned := ownedByKey[key]; !processOwned {
				return service.ErrConflict("interactive runtime is not owned by this agent on this server")
			}
		}
	}
	return nil
}

func (s *agentServiceImpl) killInteractiveRuntimeKeys(keys []terminal.SessionKey) error {
	for _, key := range keys {
		if err := s.interactiveRuntime.Kill(key); err != nil {
			return service.ErrInternal("stop interactive terminal runtime", err)
		}
	}
	return nil
}

func isActiveInteractiveSession(session *domain.AgentSession) bool {
	if session == nil || session.FinishedAt != nil {
		return false
	}
	switch session.Status {
	case "", domain.AgentSessionLeased, domain.AgentSessionStarting,
		domain.AgentSessionRunning, domain.AgentSessionIdle, domain.AgentSessionYielded:
		return true
	default:
		return false
	}
}

// DeleteAgent removes an agent assignment from the fleet-db store.
func (s *agentServiceImpl) DeleteAgent(ctx context.Context, wsKey, name string) error {
	if s.store == nil || s.compatibility == nil {
		return service.ErrUnavailable("Agents compatibility commands not configured")
	}
	name = normalizeStoredAgentName(name)
	if err := validateStoredAgentName(name); err != nil {
		return err
	}
	operatorAuth, ok := service.AgentOperatorAuthorityFromContext(ctx)
	if !ok {
		return service.ErrForbidden("verified operator authority is required")
	}
	if err := s.compatibility.RetireSupervisedAssignment(
		ctx,
		operatorAuth,
		agents.RetireSupervisedAssignmentCommand{
			WorkspaceKey: wsKey,
			AgentName:    name,
		},
	); err != nil {
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
	switch in.Kind {
	case "", string(domain.RoleKindInteractive), string(domain.RoleKindWorker):
		return nil
	default:
		return service.ErrValidation("invalid role kind")
	}
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
