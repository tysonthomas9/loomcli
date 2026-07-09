package service

import (
	"context"
	"regexp"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

// AgentService defines the business logic operations for agents.
// Handlers call this interface to perform agent-scoped operations and map
// the returned errors/results to HTTP responses.
type AgentService interface {
	// GetTerminalInfo reports whether an agent has a live tmux session.
	GetTerminalInfo(ctx context.Context, wsID, agentName string) (*AgentTerminalInfoResult, error)

	// GenerateTerminalToken generates a one-time token scoped to an agent logs stream.
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

	// ListPullRequests returns GitHub PRs for all repos in the workspace,
	// with per-repo failures reported as warnings rather than errors.
	ListPullRequests(ctx context.Context, wsID, state string) (*ops.GitPullRequestList, error)

	// GitReset hard-resets the agent's worktree to a branch.
	GitReset(ctx context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error)

	// GitStatus returns detailed git status for the agent's worktree.
	GitStatus(ctx context.Context, wsID, agentName string) (*ops.GitStatusResult, error)

	// SetTargetBranch changes the target/integration branch for a worktree.
	SetTargetBranch(ctx context.Context, wsID, agentName, branch string) error

	// ListAgents returns all agent assignments registered to a workspace
	// in the fleet-db store. Empty slice when none. Returns ErrUnavailable
	// when the service was constructed without a store handle.
	ListAgents(ctx context.Context, wsKey string) ([]*domain.Agent, error)

	// CreateAgent registers a new agent assignment in the store.
	CreateAgent(ctx context.Context, in AgentCreateInput) (*domain.Agent, error)

	// UpdateAgent applies a partial-update to an existing agent.
	UpdateAgent(ctx context.Context, wsKey, name string, patch AgentUpdateInput) (*domain.Agent, error)

	// RequestAgentLifecycle updates desired agent state and enqueues a daemon
	// command for the corresponding lifecycle action.
	RequestAgentLifecycle(ctx context.Context, wsKey, name string, in AgentLifecycleInput) (*domain.Agent, error)

	// DeleteAgent removes an agent assignment from the store.
	DeleteAgent(ctx context.Context, wsKey, name string) error
}

// AgentCreateInput is the JSON payload for POST /api/agents.
// Mirrors store.AgentCreate but kept distinct so the service contract
// doesn't leak the persistence type.
type AgentCreateInput struct {
	WorkspaceKey     string                   `json:"workspace_key"`
	Name             string                   `json:"name"`
	RoleName         string                   `json:"role_name"`
	Kind             string                   `json:"kind,omitempty"`
	PromptFile       string                   `json:"prompt_file,omitempty"`
	Auto             bool                     `json:"auto"`
	Backend          string                   `json:"backend,omitempty"`
	FallbackBackends []string                 `json:"fallback_backends,omitempty"`
	Repos            []string                 `json:"repos,omitempty"`
	RepoGroups       []string                 `json:"repo_groups,omitempty"`
	CrossRepo        bool                     `json:"cross_repo,omitempty"`
	Parent           string                   `json:"parent,omitempty"`
	DesiredState     domain.AgentDesiredState `json:"desired_state,omitempty"`
}

// AgentUpdateInput is the partial-update payload for PATCH /api/agents.
type AgentUpdateInput struct {
	RoleName         *string                   `json:"role_name,omitempty"`
	Auto             *bool                     `json:"auto,omitempty"`
	Backend          *string                   `json:"backend,omitempty"`
	FallbackBackends *[]string                 `json:"fallback_backends,omitempty"`
	Repos            *[]string                 `json:"repos,omitempty"`
	RepoGroups       *[]string                 `json:"repo_groups,omitempty"`
	CrossRepo        *bool                     `json:"cross_repo,omitempty"`
	Parent           *string                   `json:"parent,omitempty"`
	State            *domain.AgentState        `json:"state,omitempty"`
	DesiredState     *domain.AgentDesiredState `json:"desired_state,omitempty"`
}

// AgentLifecycleInput describes an agent lifecycle request that should be
// persisted as agent state plus a queued daemon command.
type AgentLifecycleInput struct {
	State        domain.AgentState
	DesiredState domain.AgentDesiredState
	CommandType  string
	Payload      map[string]string
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

// ValidAgentName matches the legacy agent-name charset: alphanumeric (any
// case), hyphens, and underscores. Retained so existing names keep resolving;
// fleet-db names (lowercase with dots) are covered by ValidStoredAgentName.
var ValidAgentName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidStoredAgentName matches the fleet-db Agent.Name charset: 1-100 chars of
// lowercase letters, digits, dots, hyphens, or underscores, not starting or
// ending with punctuation. The create/store path enforces exactly this.
var ValidStoredAgentName = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$`)

// IsValidAgentName reports whether name is acceptable for an agent-scoped read
// endpoint (files, diffs, terminal): the legacy charset OR the fleet-db stored
// charset. Reads stay permissive so any storable name (including dotted
// fleet-db names) resolves everywhere, without regressing legacy names; the
// create/store path validates against ValidStoredAgentName alone. Both charsets
// are path-safe — no separators, and no leading/trailing punctuation or bare
// ".."/"." in the fleet-db form.
func IsValidAgentName(name string) bool {
	return ValidAgentName.MatchString(name) || ValidStoredAgentName.MatchString(name)
}
