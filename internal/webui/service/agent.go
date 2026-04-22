package service

import (
	"context"
	"regexp"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

// AgentService defines the business logic operations for agents.
// Handlers call this interface to perform agent-scoped operations and map
// the returned errors/results to HTTP responses.
type AgentService interface {
	// GetTerminalInfo reports whether an agent has a live tmux session.
	GetTerminalInfo(ctx context.Context, wsID, agentName string) (*AgentTerminalInfoResult, error)

	// GenerateTerminalToken generates a one-time token scoped to an agent logs stream
	// within a specific workspace. The token is rejected if replayed at a different
	// workspace's terminal endpoint.
	GenerateTerminalToken(ctx context.Context, wsID, agentName, userID string) (string, error)

	// GetLog returns log file content for an agent.
	GetLog(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*AgentLogResult, error)

	// GetDiffStat returns diff statistics for an agent's worktree.
	GetDiffStat(ctx context.Context, wsID, agentName string) (*AgentDiffStatResult, error)

	// GitPush merges the agent's branch into the target branch.
	GitPush(ctx context.Context, wsID, agentName, target string) (*ops.GitPushResult, error)

	// GitPushAll pushes all agent worktrees to their target branches.
	GitPushAll(ctx context.Context, wsID string) (*GitPushAllResult, error)

	// GitPull merges the source branch into the agent's worktree branch.
	GitPull(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error)

	// GitSync performs a full push+pull cycle against the default branch.
	GitSync(ctx context.Context, wsID, agentName string) (*GitSyncResult, error)

	// CreatePR creates a GitHub PR from the agent's worktree branch.
	CreatePR(ctx context.Context, wsID, agentName, target string) (*ops.GitPRResult, error)

	// GitReset hard-resets the agent's worktree to a branch.
	GitReset(ctx context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error)

	// GitStatus returns detailed git status for the agent's worktree.
	GitStatus(ctx context.Context, wsID, agentName string) (*ops.GitStatusResult, error)

	// SetTargetBranch changes the target/integration branch for a worktree.
	SetTargetBranch(ctx context.Context, wsID, agentName, branch string) error
}

// AgentTerminalInfoResult contains the terminal mode for an agent.
type AgentTerminalInfoResult struct {
	Agent string
	Mode  string // "tmux" or "archive"
}

// AgentLogResult contains log file content for an agent.
type AgentLogResult struct {
	Lines     []string
	LineCount int64
	StartLine int64
}

// AgentDiffStatResult contains diff statistics for an agent's worktree.
type AgentDiffStatResult struct {
	Branch  string
	Added   int
	Removed int
}

// GitSyncResult contains the combined push+pull results.
type GitSyncResult struct {
	PushResult *ops.GitPushResult `json:"push_result"`
	PullResult *ops.GitPullResult `json:"pull_result"`
}

// GitPushAllResult contains aggregate results from pushing all worktrees.
type GitPushAllResult struct {
	Results []GitPushAllWorktreeResult `json:"results"`
	Pushed  int                        `json:"pushed"`
	Failed  int                        `json:"failed"`
}

// GitPushAllWorktreeResult contains the push result for a single worktree.
type GitPushAllWorktreeResult struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Terminal mode constants for AgentTerminalInfoResult.Mode.
const (
	AgentTerminalModeTmux    = "tmux"
	AgentTerminalModeArchive = "archive"
)

// ValidAgentName matches alphanumeric characters, hyphens, and underscores.
var ValidAgentName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
