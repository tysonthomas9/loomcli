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

// terminalUIStateKey builds the per-workspace Redis hash key that holds
// persisted terminal UI state (currently just active_tab). Scoping by
// workspace prevents cross-workspace clobbering of the active tab.
func terminalUIStateKey(wsID string) string {
	return "terminal:ui-state:" + wsID
}

func (s *terminalServiceImpl) ListTabs(ctx context.Context, wsID string) ([]tabmeta.TabMetadata, error) {
	if s.tabStore == nil {
		return nil, service.ErrUnavailable("tab metadata not available (no Redis)")
	}

	tabs, err := s.tabStore.EnsureDefaults(ctx, wsID, nil)
	if err != nil {
		return nil, service.ErrInternal("failed to list tab metadata", err)
	}
	if tabs == nil {
		tabs = []tabmeta.TabMetadata{}
	}
	for i := range tabs {
		tabs[i].PTYAlive = s.ptyAttachable(wsID, &tabs[i])
		tabs[i].AttachedClients = s.attachedClients(wsID, tabs[i].SessionName)
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
	meta.PTYAlive = s.ptyAttachable(wsID, meta)
	meta.AttachedClients = s.attachedClients(wsID, session)
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
	if meta != nil {
		meta.PTYAlive = s.ptyAttachable(wsID, meta)
		meta.AttachedClients = s.attachedClients(wsID, session)
	}

	_, issueIDChanged := fields["issue_id"]
	if s.hub != nil {
		s.hub.Broadcast(&realtime.MutationPayload{
			Type:        "terminal_metadata",
			EntityType:  "terminal",
			EntityID:    session,
			Action:      "terminal.metadata",
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: wsID,
		})
		if issueIDChanged {
			s.hub.Broadcast(&realtime.MutationPayload{
				Type:        "terminal_session_change",
				EntityType:  "terminal",
				EntityID:    session,
				Action:      "terminal.session_change",
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

	// Reject replace when a live PTY already has metadata for the name so
	// label/pinning changes can't happen under a running shell; callers use
	// PATCH for that. If the PTY is live but metadata is missing, allow the
	// create: the frontend can legitimately race metadata PUT with the first
	// WebSocket attach that spawns the PTY.
	if s.ptyAlive(wsID, meta.SessionName) {
		existing, err := s.tabStore.Get(ctx, wsID, meta.SessionName)
		if err != nil {
			return service.ErrInternal("failed to get existing tab metadata", err)
		}
		if existing != nil {
			return service.ErrConflict("tab metadata already exists with a live PTY; use PATCH to update")
		}
	}

	if err := s.tabStore.Set(ctx, meta); err != nil {
		return service.ErrInternal("failed to create/replace tab metadata", err)
	}

	if s.hub != nil {
		s.hub.Broadcast(&realtime.MutationPayload{
			Type:        "terminal_metadata",
			EntityType:  "terminal",
			EntityID:    meta.SessionName,
			Action:      "terminal.metadata",
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

	// Metadata is gone, but a child PTY keyed by the same name could still
	// be running (especially with grace_period=0 on local serve). Kill it
	// so no orphaned shell outlives its tab. Best-effort: a Kill failure
	// is logged but doesn't undo the metadata delete.
	if s.ptyMgr != nil {
		if err := s.ptyMgr.Kill(SessionKey{Workspace: wsID, Name: session}); err != nil {
			slog.Warn("failed to kill PTY on tab delete",
				"workspace", wsID, "session", session, "err", err)
		}
	}

	if s.hub != nil {
		s.hub.Broadcast(&realtime.MutationPayload{
			Type:        "terminal_metadata",
			EntityType:  "terminal",
			EntityID:    session,
			Action:      "terminal.metadata",
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

func (s *terminalServiceImpl) GetTerminalState(ctx context.Context, wsID string) (string, error) {
	if s.redisClient == nil {
		return "", service.ErrUnavailable("terminal state not available (no Redis)")
	}
	vals, err := s.redisClient.HGetAll(ctx, terminalUIStateKey(wsID)).Result()
	if err != nil {
		slog.Warn("failed to get terminal state", "err", err)
		return "", nil // graceful degradation
	}
	activeTab := vals["active_tab"]
	if activeTab == "" {
		return "", nil
	}
	// active_tab is persisted across restarts (miniredis snapshot), so a
	// prior session's active tab can point at a name whose PTY is long
	// gone. Keep the reference when either the PTY is live *or* the tab
	// metadata still exists (so the UI can render a "session ended" state
	// on that tab). Clear it only when both are gone — otherwise the
	// frontend would open the page and try to attach to a pure ghost.
	if s.ptyAlive(wsID, activeTab) {
		return activeTab, nil
	}
	if s.tabStore != nil {
		if meta, metaErr := s.tabStore.Get(ctx, wsID, activeTab); metaErr == nil && meta != nil {
			return activeTab, nil
		}
	}
	return "", nil
}

func (s *terminalServiceImpl) PatchTerminalState(ctx context.Context, wsID string, activeTab string) error {
	if s.redisClient == nil {
		return service.ErrUnavailable("terminal state not available (no Redis)")
	}
	fields := map[string]interface{}{
		"active_tab": activeTab,
	}
	if err := s.redisClient.HSet(ctx, terminalUIStateKey(wsID), fields).Err(); err != nil {
		return service.ErrInternal("failed to save terminal state", err)
	}
	return nil
}
