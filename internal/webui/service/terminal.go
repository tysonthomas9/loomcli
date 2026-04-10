package service

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// TerminalService defines business logic for terminal session management,
// tab metadata CRUD, scrollback/export, and terminal UI state persistence.
// Handlers call this interface and map returned errors to HTTP responses.
type TerminalService interface {
	// --- Core lifecycle ---
	GenerateToken(ctx context.Context, session, userID string) (string, error)
	RestartSession(ctx context.Context, wsID, session string) (*TerminalRestartResult, error)
	KillSession(ctx context.Context, session string) error
	GetSessionStatus(ctx context.Context, session string) (*TerminalStatusResult, error)
	ListSessions(ctx context.Context, wsID string) ([]TerminalSessionInfo, error)
	SpawnSession(ctx context.Context, wsID string, params *SpawnParams) (*SpawnResult, error)
	CreateLeadSession(ctx context.Context, wsID string, params *LeadSessionParams) (*LeadSessionResult, error)
	SeedSession(ctx context.Context, session string, params *SeedParams) error
	ScheduleKill(ctx context.Context, session string) error
	CloseAllSessions(ctx context.Context) (*CloseAllResult, error)

	// --- Scrollback & export ---
	ExportSession(ctx context.Context, session string) (string, error)
	GetScrollbackInfo(ctx context.Context, session string) (*ScrollbackInfoResult, error)
	GetScrollback(ctx context.Context, session string) (*ScrollbackResult, error)

	// --- Tab metadata ---
	ListTabs(ctx context.Context, wsID string) ([]tabmeta.TabMetadata, error)
	GetTab(ctx context.Context, wsID, session string) (*tabmeta.TabMetadata, error)
	PatchTab(ctx context.Context, wsID, session string, fields map[string]string) (*PatchTabResult, error)
	PutTab(ctx context.Context, wsID string, meta *tabmeta.TabMetadata) error
	DeleteTab(ctx context.Context, wsID, session string) error
	ListSessionsByIssue(ctx context.Context) (map[string][]string, error)

	// --- Terminal UI state ---
	GetTerminalState(ctx context.Context, wsID string) (string, error)
	PatchTerminalState(ctx context.Context, wsID, activeTab string) error
}

// TerminalSessionInfo contains summary info for a terminal session.
type TerminalSessionInfo struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Created int64  `json:"created"`
	IssueID string `json:"issue_id,omitempty"`
}

// TerminalRestartResult contains the backend name after a restart.
type TerminalRestartResult struct {
	Backend string
}

// TerminalStatusResult contains session liveness information.
type TerminalStatusResult struct {
	Alive      bool
	ExitReason string
}

// SpawnParams are the domain-level parameters for spawning a terminal session.
type SpawnParams struct {
	SessionName string
	Backend     string
}

// SpawnResult contains the result of spawning a terminal session.
type SpawnResult struct {
	SessionName string
	Backend     string
	Command     string
	Created     bool
}

// LeadSessionParams are the domain-level parameters for creating a lead session.
type LeadSessionParams struct {
	Message string
	Backend string
}

// LeadSessionResult contains the created lead session identifiers.
type LeadSessionResult struct {
	SessionName string
	Backend     string
}

// SeedParams are the domain-level parameters for seeding a terminal session.
type SeedParams struct {
	IssueID     string
	Title       string
	Description string
	Design      string
	Blockers    []SeedBlocker
}

// SeedBlocker represents a blocking issue in a seed prompt.
type SeedBlocker struct {
	ID    string
	Title string
}

// CloseAllResult contains the result of closing all terminal sessions.
type CloseAllResult struct {
	MetaCleanupIncomplete bool
	AffectedWorkspaces    []string
}

// ScrollbackInfoResult contains scrollback buffer statistics.
type ScrollbackInfoResult struct {
	LineCount      int
	MaxLines       int
	TruncatedCount int64
}

// ScrollbackResult contains scrollback buffer content.
type ScrollbackResult struct {
	Content string
	Lines   int
}

// PatchTabResult contains the patched tab and whether the issue ID changed.
type PatchTabResult struct {
	Tab            *tabmeta.TabMetadata
	IssueIDChanged bool
}
