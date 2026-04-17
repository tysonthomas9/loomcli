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
type terminalServiceImpl struct {
	termAuth    *realtime.TerminalAuth
	tabStore    *tabmeta.Store
	hub         *realtime.Hub
	redisClient *redis.Client
}

// NewTerminalService creates a new TerminalService implementation.
func NewTerminalService(
	termAuth *realtime.TerminalAuth,
	tabStore *tabmeta.Store,
	hub *realtime.Hub,
	redisClient *redis.Client,
) service.TerminalService {
	return &terminalServiceImpl{
		termAuth:    termAuth,
		tabStore:    tabStore,
		hub:         hub,
		redisClient: redisClient,
	}
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
