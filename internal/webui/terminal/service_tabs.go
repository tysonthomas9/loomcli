package terminal

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

func (s *terminalServiceImpl) ListTabs(ctx context.Context, wsID string) ([]tabmeta.TabMetadata, error) {
	if s.tabStore == nil {
		return nil, service.ErrUnavailable("tab metadata not available (no Redis)")
	}

	// No active-session filter: without tmux, the backend no longer tracks
	// long-lived sessions, so EnsureDefaults is called with an empty list
	// and returns whatever tabs are persisted in Redis.
	tabs, err := s.tabStore.EnsureDefaults(ctx, wsID, nil)
	if err != nil {
		return nil, service.ErrInternal("failed to list tab metadata", err)
	}
	if tabs == nil {
		tabs = []tabmeta.TabMetadata{}
	}
	return tabs, nil
}

func (s *terminalServiceImpl) GetTab(ctx context.Context, wsID, session string) (*tabmeta.TabMetadata, error) {
	if s.tabStore == nil {
		return nil, service.ErrUnavailable("tab metadata not available (no Redis)")
	}
	if err := tabmeta.ValidateSessionName(session); err != nil {
		return nil, service.ErrValidation(err.Error())
	}

	meta, err := s.tabStore.Get(ctx, wsID, session)
	if err != nil {
		return nil, service.ErrInternal("failed to get tab metadata", err)
	}
	if meta == nil {
		return nil, service.ErrNotFound("tab metadata not found")
	}
	return meta, nil
}

func (s *terminalServiceImpl) PatchTab(ctx context.Context, wsID, session string, fields map[string]string) (*service.PatchTabResult, error) {
	if s.tabStore == nil {
		return nil, service.ErrUnavailable("tab metadata not available (no Redis)")
	}
	if err := tabmeta.ValidateSessionName(session); err != nil {
		return nil, service.ErrValidation(err.Error())
	}

	meta, err := s.tabStore.Patch(ctx, wsID, session, fields)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, service.ErrNotFound("tab metadata not found")
		}
		return nil, service.ErrInternal("failed to update tab metadata", err)
	}

	_, issueIDChanged := fields["issue_id"]
	if s.hub != nil {
		s.hub.Broadcast(&realtime.MutationPayload{
			Type:        "terminal_metadata",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: wsID,
		})
		if issueIDChanged {
			s.hub.Broadcast(&realtime.MutationPayload{
				Type:        "terminal_session_change",
				Timestamp:   time.Now().UTC().Format(time.RFC3339),
				WorkspaceID: wsID,
			})
		}
	}

	return &service.PatchTabResult{Tab: meta, IssueIDChanged: issueIDChanged}, nil
}

func (s *terminalServiceImpl) PutTab(ctx context.Context, wsID string, meta *tabmeta.TabMetadata) error {
	if s.tabStore == nil {
		return service.ErrUnavailable("tab metadata not available (no Redis)")
	}
	if err := tabmeta.ValidateSessionName(meta.SessionName); err != nil {
		return service.ErrValidation(err.Error())
	}

	if err := s.tabStore.Set(ctx, meta); err != nil {
		return service.ErrInternal("failed to create/replace tab metadata", err)
	}

	if s.hub != nil {
		s.hub.Broadcast(&realtime.MutationPayload{
			Type:        "terminal_metadata",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: wsID,
		})
	}
	return nil
}

func (s *terminalServiceImpl) DeleteTab(ctx context.Context, wsID, session string) error {
	if s.tabStore == nil {
		return service.ErrUnavailable("tab metadata not available (no Redis)")
	}
	if err := tabmeta.ValidateSessionName(session); err != nil {
		return service.ErrValidation(err.Error())
	}

	if err := s.tabStore.Delete(ctx, wsID, session); err != nil {
		return service.ErrInternal("failed to delete tab metadata", err)
	}

	if s.hub != nil {
		s.hub.Broadcast(&realtime.MutationPayload{
			Type:        "terminal_metadata",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: wsID,
		})
	}
	return nil
}

func (s *terminalServiceImpl) ListSessionsByIssue(ctx context.Context) (map[string][]string, error) {
	if s.tabStore == nil {
		return nil, service.ErrUnavailable("tab metadata not available (no Redis)")
	}
	sessionMap, err := s.tabStore.ListIssueSessionMap(ctx)
	if err != nil {
		return nil, service.ErrInternal("failed to list sessions by issue", err)
	}
	return sessionMap, nil
}

func (s *terminalServiceImpl) GetTerminalState(ctx context.Context, _ string) (string, error) {
	if s.redisClient == nil {
		return "", service.ErrUnavailable("terminal state not available (no Redis)")
	}
	vals, err := s.redisClient.HGetAll(ctx, terminalUIStateKeyImpl).Result()
	if err != nil {
		slog.Warn("failed to get terminal state", "err", err)
		return "", nil // graceful degradation
	}
	return vals["active_tab"], nil
}

func (s *terminalServiceImpl) PatchTerminalState(ctx context.Context, _ string, activeTab string) error {
	if s.redisClient == nil {
		return service.ErrUnavailable("terminal state not available (no Redis)")
	}
	fields := map[string]interface{}{
		"active_tab": activeTab,
	}
	if err := s.redisClient.HSet(ctx, terminalUIStateKeyImpl, fields).Err(); err != nil {
		return service.ErrInternal("failed to save terminal state", err)
	}
	return nil
}
