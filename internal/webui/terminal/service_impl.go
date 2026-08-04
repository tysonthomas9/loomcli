package terminal

import (
	"context"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

var validTerminalSession = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// terminalServiceImpl is the concrete implementation of TerminalService.
// After the tmux removal, the service only handles Redis-backed tab
// metadata, Redis-backed UI state, and WebSocket auth token generation.
// All session lifecycle (spawn, kill, restart, etc.) is gone with tmux.
//
// ptyMgr keeps tab metadata in sync with the in-process PTY set: consulted
// at read time to annotate pty_alive, and used by PutTab / DeleteTab to
// reject clobbers of live sessions and kill the PTY when its tab goes
// away. nil is acceptable for callers without a PTY backend.
type terminalServiceImpl struct {
	termAuth    *realtime.TerminalAuth
	tabStore    *tabmeta.Store
	hub         *realtime.Hub
	redisClient *redis.Client
	ptyMgr      PTYSource
	startedAt   time.Time
}

// NewTerminalService creates a new TerminalService implementation.
func NewTerminalService(
	termAuth *realtime.TerminalAuth,
	tabStore *tabmeta.Store,
	hub *realtime.Hub,
	redisClient *redis.Client,
	ptyMgr PTYSource,
	startedAt time.Time,
) service.TerminalService {
	return &terminalServiceImpl{
		termAuth:    termAuth,
		tabStore:    tabStore,
		hub:         hub,
		redisClient: redisClient,
		ptyMgr:      ptyMgr,
		startedAt:   startedAt,
	}
}

// ptyAlive reports whether the named session has a live PTY in this server
// process. Returns false when no PTY backend is wired (e.g. auth-only tests).
func (s *terminalServiceImpl) ptyAlive(wsID, session string) bool {
	if s.ptyMgr == nil {
		return false
	}
	return s.ptyMgr.HasSession(SessionKey{Workspace: wsID, Name: session})
}

// ptyAttachable reports the value exposed as pty_alive to the UI. A live
// PTY is attachable. Metadata created during this server process is also
// attachable because the PTY may not exist until the first WebSocket connects.
// Metadata from before this server started and without a PTY remains false,
// which preserves stale-session protection after a server restart.
func (s *terminalServiceImpl) ptyAttachable(wsID string, meta *tabmeta.TabMetadata) bool {
	if meta == nil {
		return false
	}
	key := SessionKey{Workspace: wsID, Name: meta.SessionName}
	if s.ptyMgr != nil && s.ptyMgr.HasSession(key) {
		return true
	}
	if s.ptyMgr != nil && s.ptyMgr.SessionClosed(key) {
		return false
	}
	if meta.Kind == "agent" && (meta.Launch == nil || len(meta.Launch.Argv) == 0) {
		return false
	}
	if s.startedAt.IsZero() || meta.CreatedAt.IsZero() {
		return false
	}
	return !meta.CreatedAt.Before(s.startedAt)
}

// attachedClients reports the number of WebSocket clients currently
// attached to the named session. Zero when no PTY backend is wired or the
// session has no live PTY.
func (s *terminalServiceImpl) attachedClients(wsID, session string) int {
	if s.ptyMgr == nil {
		return 0
	}
	return s.ptyMgr.AttachmentCount(SessionKey{Workspace: wsID, Name: session})
}

func (s *terminalServiceImpl) GenerateToken(_ context.Context, wsID, session, userID string) (string, error) {
	if s.termAuth == nil {
		return "", service.ErrUnavailable("terminal auth not initialized")
	}
	if session == "" || !validTerminalSession.MatchString(session) {
		return "", service.ErrValidation("invalid session name")
	}
	token, err := s.termAuth.GenerateToken(session, wsID, userID)
	if err != nil {
		return "", service.ErrInternal("failed to generate token", err)
	}
	return token, nil
}
