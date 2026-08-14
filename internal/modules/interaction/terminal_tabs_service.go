package interaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *TerminalTabService) ListTabs(ctx context.Context, wsID string) ([]TabMetadata, error) {
	if s.tabStore == nil {
		return nil, terminalError(ErrUnavailable, "tab metadata not available", nil)
	}

	tabs, err := s.tabStore.EnsureDefaults(ctx, wsID, nil)
	if err != nil {
		return nil, terminalError(ErrUnavailable, "failed to list tab metadata", err)
	}
	if tabs == nil {
		tabs = []TabMetadata{}
	}
	for i := range tabs {
		tabs[i].PTYAlive = s.ptyAttachable(wsID, &tabs[i])
		tabs[i].AttachedClients = s.attachedClients(wsID, tabs[i].SessionName)
	}
	return tabs, nil
}

func (s *TerminalTabService) GetTab(ctx context.Context, wsID, session string) (*TabMetadata, error) {
	if s.tabStore == nil {
		return nil, terminalError(ErrUnavailable, "tab metadata not available", nil)
	}
	if err := ValidateTerminalSessionName(session); err != nil {
		return nil, terminalError(ErrInvalid, err.Error(), nil)
	}

	meta, err := s.tabStore.Get(ctx, wsID, session)
	if err != nil {
		return nil, terminalError(ErrUnavailable, "failed to get tab metadata", err)
	}
	if meta == nil {
		return nil, terminalError(ErrNotFound, "tab metadata not found", nil)
	}
	meta.PTYAlive = s.ptyAttachable(wsID, meta)
	meta.AttachedClients = s.attachedClients(wsID, session)
	return meta, nil
}

func (s *TerminalTabService) PatchTab(ctx context.Context, wsID, session string, fields map[string]string) (*PatchTabResult, error) {
	if s.tabStore == nil {
		return nil, terminalError(ErrUnavailable, "tab metadata not available", nil)
	}
	if err := ValidateTerminalSessionName(session); err != nil {
		return nil, terminalError(ErrInvalid, err.Error(), nil)
	}

	meta, err := s.tabStore.Patch(ctx, wsID, session, fields)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, terminalError(ErrNotFound, "tab metadata not found", nil)
		}
		return nil, terminalError(ErrUnavailable, "failed to update tab metadata", err)
	}
	if meta != nil {
		meta.PTYAlive = s.ptyAttachable(wsID, meta)
		meta.AttachedClients = s.attachedClients(wsID, session)
	}

	_, issueIDChanged := fields["issue_id"]
	return &PatchTabResult{Tab: meta, IssueIDChanged: issueIDChanged}, nil
}

func (s *TerminalTabService) PutTab(
	ctx context.Context,
	command PutTerminalTabCommand,
) (*TabMetadata, error) {
	workspace := strings.TrimSpace(command.WorkspaceKey)
	terminalID := strings.TrimSpace(command.TerminalID)
	if workspace == "" || command.Label == "" {
		return nil, terminalError(ErrInvalid, "workspace and label are required", nil)
	}
	if err := ValidateTerminalSessionName(terminalID); err != nil {
		return nil, terminalError(ErrInvalid, err.Error(), nil)
	}
	configDir := ""
	if s != nil && s.agentTerminal.Placement != nil {
		configDir = strings.TrimSpace(s.agentTerminal.Placement.ConfigDir())
	}
	launch, err := LaunchSpecForBackend(command.Backend, configDir)
	if err != nil {
		return nil, terminalError(ErrInvalid, err.Error(), nil)
	}
	now := time.Now().UTC()
	meta := &TabMetadata{
		SessionName: terminalID,
		Workspace:   workspace,
		Label:       command.Label,
		Notes:       command.Notes,
		SortOrder:   command.SortOrder,
		Pinned:      command.Pinned,
		Backend:     strings.ToLower(strings.TrimSpace(command.Backend)),
		Launch:      launch,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.putTabMetadata(ctx, workspace, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func (s *TerminalTabService) putTabMetadata(ctx context.Context, wsID string, meta *TabMetadata) error {
	if s.tabStore == nil {
		return terminalError(ErrUnavailable, "tab metadata not available", nil)
	}
	if err := ValidateTerminalSessionName(meta.SessionName); err != nil {
		return terminalError(ErrInvalid, err.Error(), nil)
	}
	if meta.Launch == nil || len(meta.Launch.Argv) == 0 {
		return terminalError(ErrInvalid, "terminal launch envelope is required", nil)
	}
	if meta.Kind == "" && !IsValidTerminalBackend(meta.Backend) {
		return terminalError(ErrInvalid, "supported terminal backend is required", nil)
	}

	// Generic PUT must not erase server-owned canonical Interaction identity,
	// even when a restart has lost the process-local PTY. Such tabs must pass
	// through DeleteTab so the old exact lifecycle converges first.
	existing, err := s.tabStore.Get(ctx, wsID, meta.SessionName)
	if err != nil {
		return terminalError(ErrUnavailable, "failed to get existing tab metadata", err)
	}
	if existing != nil {
		if existing.Kind == "agent" ||
			strings.TrimSpace(existing.InteractionSessionID) != "" ||
			strings.TrimSpace(existing.InteractionTerminalID) != "" ||
			strings.TrimSpace(existing.InteractionLeaseID) != "" ||
			existing.InteractionLeaseFencingToken != 0 {
			return terminalError(ErrConflict,
				"canonical agent tab must be deleted before replacement",
				nil,
			)
		}
		// Reject replacement under a running shell; callers use PATCH for
		// label/pinning changes. If metadata is missing while the PTY is live,
		// allow create because the first WebSocket attach can race the PUT.
		if s.ptyAlive(wsID, meta.SessionName) {
			return terminalError(ErrConflict, "tab metadata already exists with a live PTY; use PATCH to update", nil)
		}
	}

	if err := s.tabStore.Set(ctx, meta); err != nil {
		return terminalError(ErrUnavailable, "failed to create/replace tab metadata", err)
	}

	return nil
}

// persistInteractionTabIdentity is the narrow owner-private write used after
// Interaction has atomically started a session and opened its terminal. It
// cannot be used as generic tab replacement: all canonical owner identities
// must be present and match the requested workspace before persistence.
func (s *TerminalTabService) persistInteractionTabIdentity(
	ctx context.Context,
	wsID string,
	meta *TabMetadata,
) error {
	if s.tabStore == nil {
		return terminalError(ErrUnavailable, "tab metadata not available", nil)
	}
	if meta == nil || strings.TrimSpace(wsID) == "" || meta.Workspace != wsID ||
		meta.Kind != "agent" || strings.TrimSpace(meta.AgentID) == "" ||
		strings.TrimSpace(meta.InteractionSessionID) == "" ||
		strings.TrimSpace(meta.InteractionTerminalID) == "" ||
		strings.TrimSpace(meta.InteractionLeaseID) == "" ||
		meta.InteractionLeaseFencingToken <= 0 {
		return terminalError(ErrInvalid, "complete Interaction terminal identity is required", nil)
	}
	if err := ValidateTerminalSessionName(meta.SessionName); err != nil {
		return terminalError(ErrInvalid, err.Error(), nil)
	}
	meta.UpdatedAt = time.Now().UTC()
	if err := s.tabStore.Set(ctx, meta); err != nil {
		return terminalError(ErrUnavailable, "failed to persist Interaction terminal identity", err)
	}
	return nil
}

func (s *TerminalTabService) DeleteTab(ctx context.Context, wsID, session string) error {
	if s.tabStore == nil {
		return terminalError(ErrUnavailable, "tab metadata not available", nil)
	}
	if err := ValidateTerminalSessionName(session); err != nil {
		return terminalError(ErrInvalid, err.Error(), nil)
	}

	meta, err := s.tabStore.Get(ctx, wsID, session)
	if err != nil {
		return terminalError(ErrUnavailable, "failed to load tab metadata before delete", err)
	}
	if meta != nil && meta.Kind == "agent" && strings.TrimSpace(meta.AgentID) != "" {
		unlock := LockAgentLifecycle(wsID, meta.AgentID)
		defer unlock()
		// The terminal launch path persists canonical IDs under the same
		// boundary. Re-read after acquiring it so delete cannot race a launch
		// between Start/Open and metadata persistence.
		meta, err = s.tabStore.Get(ctx, wsID, session)
		if err != nil {
			return terminalError(ErrUnavailable, "failed to reload tab metadata before delete", err)
		}
		if meta == nil {
			return nil
		}
	}

	// Converge and kill the child before deleting its server-owned placement
	// metadata. The PTY lifecycle hook needs those exact canonical IDs, and a
	// failed convergence must retain both the process and metadata for retry.
	if s.runtime != nil {
		if err := s.runtime.Kill(TerminalKey{WorkspaceKey: wsID, TerminalID: session}); err != nil {
			return terminalError(ErrUnavailable, "failed to converge and kill PTY before tab delete", err)
		}
	}

	if err := s.tabStore.Delete(ctx, wsID, session); err != nil {
		return terminalError(ErrUnavailable, "failed to delete tab metadata", err)
	}

	return nil
}

func (s *TerminalTabService) ListSessionsByIssue(ctx context.Context) (map[string][]string, error) {
	if s.tabStore == nil {
		return nil, terminalError(ErrUnavailable, "tab metadata not available", nil)
	}
	sessionMap, err := s.tabStore.ListIssueSessionMap(ctx)
	if err != nil {
		return nil, terminalError(ErrUnavailable, "failed to list sessions by issue", err)
	}
	return sessionMap, nil
}

func terminalError(kind error, message string, cause error) error {
	return &terminalFailure{kind: kind, message: message, cause: cause}
}

// terminalFailure preserves the capability sentinel and the diagnostic cause
// while keeping the client-safe message as a separate value. Delivery
// adapters must not derive a response body from Error because it can include
// infrastructure details from cause.
type terminalFailure struct {
	kind    error
	message string
	cause   error
}

func (f *terminalFailure) Error() string {
	if f.cause != nil {
		return fmt.Sprintf("%s: %v", f.message, f.cause)
	}
	return f.message
}

func (f *terminalFailure) Unwrap() []error {
	if f.cause == nil {
		return []error{f.kind}
	}
	return []error{f.kind, f.cause}
}

// PublicTerminalErrorMessage returns the terminal capability's client-safe
// failure description without exposing adapter or persistence diagnostics.
func PublicTerminalErrorMessage(err error) string {
	var failure *terminalFailure
	if errors.As(err, &failure) {
		return failure.message
	}
	return "terminal operation failed"
}
