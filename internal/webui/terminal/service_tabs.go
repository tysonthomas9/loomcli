package terminal

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
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
		return nil, apperrors.ErrUnavailable("tab metadata not available (no Redis)")
	}

	tabs, err := s.tabStore.EnsureDefaults(ctx, wsID, nil)
	if err != nil {
		return nil, apperrors.ErrInternal("failed to list tab metadata", err)
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
		return nil, apperrors.ErrUnavailable("tab metadata not available (no Redis)")
	}
	if err := tabmeta.ValidateSessionName(session); err != nil {
		return nil, apperrors.ErrValidation(err.Error())
	}

	meta, err := s.tabStore.Get(ctx, wsID, session)
	if err != nil {
		return nil, apperrors.ErrInternal("failed to get tab metadata", err)
	}
	if meta == nil {
		return nil, apperrors.ErrNotFound("tab metadata not found")
	}
	meta.PTYAlive = s.ptyAttachable(wsID, meta)
	meta.AttachedClients = s.attachedClients(wsID, session)
	return meta, nil
}

func (s *terminalServiceImpl) PatchTab(ctx context.Context, wsID, session string, fields map[string]string) (*PatchTabResult, error) {
	if s.tabStore == nil {
		return nil, apperrors.ErrUnavailable("tab metadata not available (no Redis)")
	}
	if err := tabmeta.ValidateSessionName(session); err != nil {
		return nil, apperrors.ErrValidation(err.Error())
	}

	meta, err := s.tabStore.Patch(ctx, wsID, session, fields)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, apperrors.ErrNotFound("tab metadata not found")
		}
		return nil, apperrors.ErrInternal("failed to update tab metadata", err)
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

	return &PatchTabResult{Tab: meta, IssueIDChanged: issueIDChanged}, nil
}

func (s *terminalServiceImpl) PutTab(ctx context.Context, wsID string, meta *tabmeta.TabMetadata) error {
	if s.tabStore == nil {
		return apperrors.ErrUnavailable("tab metadata not available (no Redis)")
	}
	if err := tabmeta.ValidateSessionName(meta.SessionName); err != nil {
		return apperrors.ErrValidation(err.Error())
	}

	// Generic PUT must not erase server-owned canonical Interaction identity,
	// even when a restart has lost the process-local PTY. Such tabs must pass
	// through DeleteTab so the old exact lifecycle converges first.
	existing, err := s.tabStore.Get(ctx, wsID, meta.SessionName)
	if err != nil {
		return apperrors.ErrInternal("failed to get existing tab metadata", err)
	}
	if existing != nil {
		if existing.Kind == "agent" ||
			strings.TrimSpace(existing.InteractionSessionID) != "" ||
			strings.TrimSpace(existing.InteractionTerminalID) != "" ||
			strings.TrimSpace(existing.InteractionLeaseID) != "" ||
			existing.InteractionLeaseFencingToken != 0 {
			return apperrors.ErrConflict(
				"canonical agent tab must be deleted before replacement",
			)
		}
		// Reject replacement under a running shell; callers use PATCH for
		// label/pinning changes. If metadata is missing while the PTY is live,
		// allow create because the first WebSocket attach can race the PUT.
		if s.ptyAlive(wsID, meta.SessionName) {
			return apperrors.ErrConflict("tab metadata already exists with a live PTY; use PATCH to update")
		}
	}

	if err := s.tabStore.Set(ctx, meta); err != nil {
		return apperrors.ErrInternal("failed to create/replace tab metadata", err)
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

// PersistInteractionTabIdentity is the narrow server-owned write used after
// Interaction has atomically started a session and opened its terminal. It
// cannot be used as generic tab replacement: all canonical owner identities
// must be present and match the requested workspace before persistence.
func (s *terminalServiceImpl) PersistInteractionTabIdentity(
	ctx context.Context,
	wsID string,
	meta *tabmeta.TabMetadata,
) error {
	if s.tabStore == nil {
		return apperrors.ErrUnavailable("tab metadata not available (no Redis)")
	}
	if meta == nil || strings.TrimSpace(wsID) == "" || meta.Workspace != wsID ||
		meta.Kind != "agent" || strings.TrimSpace(meta.AgentID) == "" ||
		strings.TrimSpace(meta.InteractionSessionID) == "" ||
		strings.TrimSpace(meta.InteractionTerminalID) == "" ||
		strings.TrimSpace(meta.InteractionLeaseID) == "" ||
		meta.InteractionLeaseFencingToken <= 0 {
		return apperrors.ErrValidation("complete Interaction terminal identity is required")
	}
	if err := tabmeta.ValidateSessionName(meta.SessionName); err != nil {
		return apperrors.ErrValidation(err.Error())
	}
	meta.UpdatedAt = time.Now().UTC()
	if err := s.tabStore.Set(ctx, meta); err != nil {
		return apperrors.ErrInternal("failed to persist Interaction terminal identity", err)
	}
	return nil
}

func (s *terminalServiceImpl) DeleteTab(ctx context.Context, wsID, session string) error {
	if s.tabStore == nil {
		return apperrors.ErrUnavailable("tab metadata not available (no Redis)")
	}
	if err := tabmeta.ValidateSessionName(session); err != nil {
		return apperrors.ErrValidation(err.Error())
	}

	meta, err := s.tabStore.Get(ctx, wsID, session)
	if err != nil {
		return apperrors.ErrInternal("failed to load tab metadata before delete", err)
	}
	if meta != nil && meta.Kind == "agent" && strings.TrimSpace(meta.AgentID) != "" {
		unlock := LockAgentLifecycle(wsID, meta.AgentID)
		defer unlock()
		// The terminal launch path persists canonical IDs under the same
		// boundary. Re-read after acquiring it so delete cannot race a launch
		// between Start/Open and metadata persistence.
		meta, err = s.tabStore.Get(ctx, wsID, session)
		if err != nil {
			return apperrors.ErrInternal("failed to reload tab metadata before delete", err)
		}
		if meta == nil {
			return nil
		}
	}

	// Converge and kill the child before deleting its server-owned placement
	// metadata. The PTY lifecycle hook needs those exact canonical IDs, and a
	// failed convergence must retain both the process and metadata for retry.
	if s.ptyMgr != nil {
		if err := s.ptyMgr.Kill(SessionKey{Workspace: wsID, Name: session}); err != nil {
			return apperrors.ErrInternal("failed to converge and kill PTY before tab delete", err)
		}
	}

	if err := s.tabStore.Delete(ctx, wsID, session); err != nil {
		return apperrors.ErrInternal("failed to delete tab metadata", err)
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
		return nil, apperrors.ErrUnavailable("tab metadata not available (no Redis)")
	}
	sessionMap, err := s.tabStore.ListIssueSessionMap(ctx)
	if err != nil {
		return nil, apperrors.ErrInternal("failed to list sessions by issue", err)
	}
	return sessionMap, nil
}

func (s *terminalServiceImpl) GetTerminalState(ctx context.Context, wsID string) (string, error) {
	if s.redisClient == nil {
		return "", apperrors.ErrUnavailable("terminal state not available (no Redis)")
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
		return apperrors.ErrUnavailable("terminal state not available (no Redis)")
	}
	fields := map[string]interface{}{
		"active_tab": activeTab,
	}
	if err := s.redisClient.HSet(ctx, terminalUIStateKey(wsID), fields).Err(); err != nil {
		return apperrors.ErrInternal("failed to save terminal state", err)
	}
	return nil
}
