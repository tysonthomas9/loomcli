package interaction

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// TerminalTabs is Interaction's complete terminal application surface. It
// owns terminal identity and lifecycle while keeping private PTY/tmux process
// mechanics behind process-neutral attachment results.
type TerminalTabs interface {
	ListTabs(ctx context.Context, wsID string) ([]TabMetadata, error)
	GetTab(ctx context.Context, wsID, session string) (*TabMetadata, error)
	PatchTab(ctx context.Context, wsID, session string, fields map[string]string) (*PatchTabResult, error)
	PutTab(ctx context.Context, command PutTerminalTabCommand) (*TabMetadata, error)
	DeleteTab(ctx context.Context, wsID, session string) error
	ListSessionsByIssue(ctx context.Context) (map[string][]string, error)
	EnsureAgentTerminal(ctx context.Context, command EnsureAgentTerminalCommand) (*TabMetadata, error)
	PlanTerminalAttach(ctx context.Context, command TerminalAttachCommand) (TerminalAttachPlan, error)
	AttachTerminal(ctx context.Context, command TerminalAttachCommand) (*TerminalAttachResult, error)
	DetachTerminal(ctx context.Context, workspaceKey, terminalID, attachmentID string)
	AgentTerminalInfo(ctx context.Context, workspaceKey, agentID string) (*AgentTerminalInfo, error)
	AttachAgentTerminal(ctx context.Context, command AttachAgentTerminalCommand) (*AgentTerminalAttachResult, error)
	DetachAgentTerminal(ctx context.Context, attachmentID string) error

	StartSetup(ctx context.Context, wsID string, req TerminalSetupRequest) (*TerminalSetupResult, error)
}

// PutTerminalTabCommand is the complete caller-controlled intent for one
// generic terminal tab. Interaction derives and persists the launch envelope;
// callers cannot supply argv, environment, paths, or lifecycle identity.
type PutTerminalTabCommand struct {
	WorkspaceKey string
	TerminalID   string
	Label        string
	Notes        string
	SortOrder    int
	Pinned       bool
	Backend      string
}

// EnsureAgentTerminalCommand names the canonical Agents identity whose
// interactive terminal placement Interaction must converge.
type EnsureAgentTerminalCommand struct {
	WorkspaceKey string
	AgentID      string
}

// TerminalAttachCommand carries one browser attachment request into
// Interaction. StartAuthority is required only when a new interactive Agent
// child must be spawned; reconnects never mint a new session generation.
type TerminalAttachCommand struct {
	WorkspaceKey   string
	TerminalID     string
	Columns        uint16
	Rows           uint16
	StartAuthority *authority.OperatorAuthority
}

// TerminalAttachPlan is the pre-upgrade result for one terminal attachment.
// Interaction evaluates capacity, persisted tab state, and whether the next
// attach would mint a new AgentSession generation in one owner call.
type TerminalAttachPlan struct {
	StartAuthorityRequired bool
}

// TerminalAttachResult returns only the transport-facing attachment and
// whether buffered output belongs to an existing child.
type TerminalAttachResult struct {
	Attachment TerminalAttachment
	Reattached bool
}

// AgentTerminalInfo is Interaction's projection of whether an externally
// hosted worker process has a live terminal available for read-only viewing.
type AgentTerminalInfo struct {
	AgentID     string
	Live        bool
	SessionName string
}

// AttachAgentTerminalCommand identifies a worker terminal by canonical Agent
// identity; delivery cannot select or forge a tmux session name.
type AttachAgentTerminalCommand struct {
	WorkspaceKey string
	AgentID      string
	Columns      uint16
	Rows         uint16
}

// AgentTerminalAttachResult exposes only the process-neutral connection and
// monitor needed by WebSocket framing.
type AgentTerminalAttachResult struct {
	Connection AgentTerminalConnection
	Monitor    AgentTerminalMonitor
}

// PatchTabResult contains the patched tab and whether the issue ID changed.
type PatchTabResult struct {
	Tab            *TabMetadata
	IssueIDChanged bool
}

// TerminalSetupRequest asks the backend to start a known setup command in a
// workspace-scoped terminal session.
type TerminalSetupRequest struct {
	Backend string
	Action  string
}

// TerminalSetupResult describes the setup terminal session the frontend
// should attach to and the command the backend started there.
type TerminalSetupResult struct {
	SessionName string
	Label       string
	Backend     string
	Action      string
	Command     string
	Title       string
	Message     string
	Manual      bool
	Created     bool
}
