package terminal

import (
	"context"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

const terminalUIStateKeyImpl = "terminal:ui-state"

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
}

// NewTerminalService creates a new TerminalService implementation.
func NewTerminalService(
	termAuth *realtime.TerminalAuth,
	tabStore *tabmeta.Store,
	hub *realtime.Hub,
	redisClient *redis.Client,
	ptyMgr PTYSource,
) service.TerminalService {
	return &terminalServiceImpl{
		termAuth:    termAuth,
		tabStore:    tabStore,
		hub:         hub,
		redisClient: redisClient,
		ptyMgr:      ptyMgr,
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

// attachedClients reports the number of WebSocket clients currently
// attached to the named session. Zero when no PTY backend is wired or the
// session has no live PTY.
func (s *terminalServiceImpl) attachedClients(wsID, session string) int {
	if s.ptyMgr == nil {
		return 0
	}
	return s.ptyMgr.AttachmentCount(SessionKey{Workspace: wsID, Name: session})
}

func (s *terminalServiceImpl) GenerateToken(_ context.Context, session, userID string) (string, error) {
	if s.termAuth == nil {
		return "", service.ErrUnavailable("terminal auth not initialized")
	}
	if session == "" || !validTerminalSession.MatchString(session) {
		return "", service.ErrValidation("invalid session name")
	}
	token, err := s.termAuth.GenerateToken(session, userID)
	if err != nil {
		return "", service.ErrInternal("failed to generate token", err)
	}
	return token, nil
}
