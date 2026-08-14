package agentcoord

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
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

// IsValidAgentName reports whether name is acceptable for an agent-scoped read
// endpoint (files, diffs, terminal): the legacy charset OR the Fleet-stored
// charset. Agents owns both contracts so every delivery path resolves dotted
// canonical names consistently.
func IsValidAgentName(name string) bool {
	return agents.ValidAgentIdentifier(name)
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
