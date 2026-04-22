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
	GenerateToken(ctx context.Context, session, wsID, userID string) (string, error)

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
}

// PatchTabResult contains the patched tab and whether the issue ID changed.
type PatchTabResult struct {
	Tab            *tabmeta.TabMetadata
	IssueIDChanged bool
}
