package agentcoord

import (
	"context"
	"regexp"

	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

// AgentService is the remaining transport-oriented terminal and Source
// Control surface historically named for agents. Durable Agent identity and
// lifecycle are owned exclusively by internal/modules/agents.
type AgentService interface {
	// GetTerminalInfo reports whether an agent has a live tmux session.
	GetTerminalInfo(ctx context.Context, wsID, agentName string) (*AgentTerminalInfoResult, error)

	// GenerateTerminalToken generates a one-time token scoped to an agent logs stream.
	GenerateTerminalToken(ctx context.Context, wsID, agentName, userID string) (string, error)

	// GetLog returns log file content for an agent.
	GetLog(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*AgentLogResult, error)
}

// InteractiveAgentRuntime is the process-local side of canonical Agent
// lifecycle convergence. Durable desired state is changed through Agents;
// this port only tears down PTYs owned by the current web process. The PTY
// manager's before-kill hook fences the matching Interaction generation.
type InteractiveAgentRuntime interface {
	StopAgent(context.Context, string, string) error
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

// ValidateAgentName validates an agent-scoped filesystem and terminal key.
func ValidateAgentName(name string) error {
	if name == "" {
		return apperrors.ErrValidation("missing agent name")
	}
	if !IsValidAgentName(name) {
		return apperrors.ErrValidation("invalid agent name")
	}
	return nil
}
