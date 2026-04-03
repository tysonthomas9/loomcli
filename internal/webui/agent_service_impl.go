package webui

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// agentServiceImpl is the concrete implementation of AgentService.
type agentServiceImpl struct {
	gitOps   GitOps
	termMgr  *TerminalManager
	termAuth *terminalAuth
}

// NewAgentService creates a new AgentService implementation.
// gitOps must be non-nil. termMgr and termAuth may be nil (methods
// that require them return service.ErrUnavailable).
func NewAgentService(gitOps GitOps, termMgr *TerminalManager, termAuth *terminalAuth) AgentService {
	return &agentServiceImpl{
		gitOps:   gitOps,
		termMgr:  termMgr,
		termAuth: termAuth,
	}
}

// agentLogTokenScope returns the token scope string for an agent's log stream.
func agentLogTokenScope(agentName string) string {
	return "agent:" + agentName + ":logs"
}

// validateAgentName checks that the agent name is non-empty and matches the allowed pattern.
func validateAgentName(name string) error {
	if name == "" {
		return service.ErrValidation("missing agent name")
	}
	if !validAgentName.MatchString(name) {
		return service.ErrValidation("invalid agent name: must match [a-zA-Z0-9_-]+")
	}
	return nil
}

// resolveAgentWorktree validates the agent name and resolves the worktree.
func (s *agentServiceImpl) resolveAgentWorktree(wsID, agentName string) (*AgentWorktree, error) {
	if err := validateAgentName(agentName); err != nil {
		return nil, err
	}
	wt, err := s.gitOps.ResolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", agentName))
	}
	return wt, nil
}

func (s *agentServiceImpl) GetTerminalInfo(_ context.Context, wsID, agentName string) (*AgentTerminalInfoResult, error) {
	if err := validateAgentName(agentName); err != nil {
		return nil, err
	}
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	mode := agentTerminalModeArchive
	if _, found, err := s.termMgr.FindLatestAgentSession(wsID, agentName); err != nil {
		slog.Error("failed to resolve agent tmux session", "agent", agentName, "err", err)
		return nil, service.ErrInternal("failed to inspect terminal sessions", err)
	} else if found {
		mode = agentTerminalModeTmux
	}

	return &AgentTerminalInfoResult{Agent: agentName, Mode: mode}, nil
}

func (s *agentServiceImpl) GenerateTerminalToken(_ context.Context, agentName, userID string) (string, error) {
	if err := validateAgentName(agentName); err != nil {
		return "", err
	}
	if s.termAuth == nil {
		return "", service.ErrUnavailable("terminal authentication not initialized")
	}

	token, err := s.termAuth.GenerateToken(agentLogTokenScope(agentName), userID)
	if err != nil {
		slog.Error("failed to generate agent terminal token", "agent", agentName, "err", err)
		return "", service.ErrInternal("failed to generate token", err)
	}
	return token, nil
}

func (s *agentServiceImpl) GetLog(_ context.Context, wsID, agentName string, lines int, beforeLine int64) (*AgentLogResult, error) {
	if err := validateAgentName(agentName); err != nil {
		return nil, err
	}

	// Clamp lines to valid range
	if lines <= 0 {
		lines = logReadDefaultLines
	}
	if lines > logReadMaxLines {
		lines = logReadMaxLines
	}

	logPath, err := getAgentLogPath(wsID, agentName)
	if err != nil {
		slog.Error("agent log path error", "agent", agentName, "err", err)
		return nil, service.ErrInternal("failed to resolve log path", err)
	}

	if !fileExists(logPath) {
		return nil, service.ErrNotFound("log file not found - agent may not be active")
	}

	content, startLine, err := readFileLastLines(logPath, lines, beforeLine)
	if err != nil {
		slog.Error("failed to read agent log", "agent", agentName, "err", err)
		return nil, service.ErrInternal("failed to read log file", err)
	}

	return &AgentLogResult{
		Lines:     content,
		LineCount: startLine + int64(len(content)) - 1,
		StartLine: startLine,
	}, nil
}

func (s *agentServiceImpl) GetDiffStat(_ context.Context, wsID, agentName string) (*AgentDiffStatResult, error) {
	wt, err := s.resolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, err
	}

	stats := s.gitOps.DiffStat(wt.Path, wt.DefaultBranch)
	return &AgentDiffStatResult{
		Branch:  wt.Branch,
		Added:   stats.LinesAdded,
		Removed: stats.LinesRemoved,
	}, nil
}

func (s *agentServiceImpl) GitPush(_ context.Context, wsID, agentName, target string) (*GitPushResult, error) {
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

func (s *agentServiceImpl) GitPushAll(_ context.Context, wsID string) (*GitPushAllResult, error) {
	worktrees, err := s.gitOps.ListAgentWorktrees(wsID)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}

	var results []GitPushAllWorktreeResult
	pushed, failed := 0, 0

	for _, wt := range worktrees {
		remote := wt.Remote
		if remote == "" {
			remote = "origin"
		}
		result, pushErr := s.gitOps.Push(wt.Path, wt.Branch, wt.DefaultBranch, remote)
		if pushErr != nil {
			results = append(results, GitPushAllWorktreeResult{
				Name:  wt.Name,
				Error: pushErr.Error(),
			})
			failed++
			continue
		}
		if result.AlreadyUpToDate {
			results = append(results, GitPushAllWorktreeResult{
				Name:    wt.Name,
				Success: true,
				Message: "already up to date",
			})
			continue
		}
		if !result.Success {
			results = append(results, GitPushAllWorktreeResult{
				Name:  wt.Name,
				Error: result.Message,
			})
			failed++
			continue
		}
		results = append(results, GitPushAllWorktreeResult{
			Name:    wt.Name,
			Success: true,
			Message: result.Message,
		})
		pushed++
	}

	return &GitPushAllResult{
		Results: results,
		Pushed:  pushed,
		Failed:  failed,
	}, nil
}

func (s *agentServiceImpl) GitPull(_ context.Context, wsID, agentName, source string) (*GitPullResult, error) {
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

func (s *agentServiceImpl) GitSync(_ context.Context, wsID, agentName string) (*GitSyncResult, error) {
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
		return &GitSyncResult{PushResult: pushResult}, nil
	}

	currentBranch, err := s.gitOps.GetCurrentBranch(wt.Path)
	if err != nil {
		return nil, fmt.Errorf("getting current branch: %w", err)
	}

	pullResult, err := s.gitOps.Pull(wt.Path, currentBranch, target, wt.Remote)
	if err != nil {
		return nil, fmt.Errorf("pull failed: %w", err)
	}

	return &GitSyncResult{
		PushResult: pushResult,
		PullResult: pullResult,
	}, nil
}

func (s *agentServiceImpl) CreatePR(_ context.Context, wsID, agentName, target string) (*GitPRResult, error) {
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

func (s *agentServiceImpl) GitReset(_ context.Context, wsID, agentName, branch string, force, push bool) (*GitResetResult, error) {
	wt, err := s.resolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, err
	}

	if branch == "" {
		branch = wt.DefaultBranch
	}

	result, err := s.gitOps.Reset(wt.Path, wt.Name, branch, force, push)
	if err != nil {
		return nil, err // GitResetLockedError passes through
	}
	return result, nil
}

func (s *agentServiceImpl) GitStatus(_ context.Context, wsID, agentName string) (*GitStatusResult, error) {
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
