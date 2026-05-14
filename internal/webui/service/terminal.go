package service

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// TerminalService defines the surviving terminal-related business logic after
// the tmux backend was removed. What remains is the WebSocket auth token
// endpoint, tab metadata CRUD (Redis-backed, not tmux-backed), and the
// terminal UI state key-value. Session lifecycle (spawn, kill, restart,
// scrollback, seed, lead-session, export, close-all) is gone — each
// WebSocket now owns a fresh PTY managed directly by PTYManager.
type TerminalService interface {
	// --- WebSocket auth ---
	GenerateToken(ctx context.Context, session, userID string) (string, error)

	// --- Tab metadata (Redis) ---
	ListTabs(ctx context.Context, wsID string) ([]tabmeta.TabMetadata, error)
	GetTab(ctx context.Context, wsID, session string) (*tabmeta.TabMetadata, error)
	PatchTab(ctx context.Context, wsID, session string, fields map[string]string) (*PatchTabResult, error)
	PutTab(ctx context.Context, wsID string, meta *tabmeta.TabMetadata) error
	DeleteTab(ctx context.Context, wsID, session string) error
	ListSessionsByIssue(ctx context.Context) (map[string][]string, error)

	// --- Terminal UI state (Redis) ---
	GetTerminalState(ctx context.Context, wsID string) (string, error)
	PatchTerminalState(ctx context.Context, wsID, activeTab string) error

	// --- Backend-owned setup terminal ---
	StartSetup(ctx context.Context, wsID string, req TerminalSetupRequest) (*TerminalSetupResult, error)
}

// PatchTabResult contains the patched tab and whether the issue ID changed.
type PatchTabResult struct {
	Tab            *tabmeta.TabMetadata
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
	SessionName string `json:"session_name"`
	Label       string `json:"label"`
	Backend     string `json:"backend"`
	Action      string `json:"action"`
	Command     string `json:"command"`
	Title       string `json:"title"`
	Message     string `json:"message"`
	Manual      bool   `json:"manual"`
	Created     bool   `json:"created"`
}
