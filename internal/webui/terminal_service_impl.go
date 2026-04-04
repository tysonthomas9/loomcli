package webui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

const terminalUIStateKeyImpl = "terminal:ui-state"

// terminalServiceImpl is the concrete implementation of TerminalService.
type terminalServiceImpl struct {
	termMgr     *TerminalManager
	termAuth    *realtime.TerminalAuth
	configPool  configConnectionGetter
	tabStore    *tabmeta.Store
	hub         *realtime.Hub
	histStore   *sessionhistory.Store
	redisClient *redis.Client
}

// NewTerminalService creates a new TerminalService implementation.
func NewTerminalService(
	termMgr *TerminalManager,
	termAuth *realtime.TerminalAuth,
	configPool configConnectionGetter,
	tabStore *tabmeta.Store,
	hub *realtime.Hub,
	histStore *sessionhistory.Store,
	redisClient *redis.Client,
) TerminalService {
	return &terminalServiceImpl{
		termMgr:     termMgr,
		termAuth:    termAuth,
		configPool:  configPool,
		tabStore:    tabStore,
		hub:         hub,
		histStore:   histStore,
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

func (s *terminalServiceImpl) RestartSession(ctx context.Context, wsID, session string) (*TerminalRestartResult, error) {
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	// Shell tabs: kill and return without changing defaultCommand.
	if strings.HasPrefix(session, "lead-shell-") {
		_ = s.termMgr.KillSessionByName(session)
		return &TerminalRestartResult{Backend: "shell"}, nil
	}

	// Read current backend from loom.yaml via daemon
	backend := s.termMgr.DefaultCommand() // fallback to current
	if s.configPool != nil {
		wsPath, err := getWorkspacePath(s.configPool, ctx)
		if err == nil {
			pf, err := loadProjectFile(wsPath)
			if err == nil {
				b := pf.Backend
				if b == "" {
					b = "claude"
				}
				if !isValidBackend(b) {
					return nil, service.ErrValidation(fmt.Sprintf("invalid backend %q; valid: %s", b, strings.Join(validBackends, ", ")))
				}
				backend = b
			}
		}
	}

	termCmd := fmt.Sprintf("loom lead --backend %s", backend)
	_ = s.termMgr.KillSessionByName(session)
	s.termMgr.SetDefaultCommand(termCmd)

	return &TerminalRestartResult{Backend: backend}, nil
}

func (s *terminalServiceImpl) KillSession(_ context.Context, session string) error {
	if s.termMgr == nil {
		return service.ErrUnavailable("terminal manager not initialized")
	}
	_ = s.termMgr.KillSessionByName(session)
	return nil
}

func (s *terminalServiceImpl) GetSessionStatus(_ context.Context, session string) (*TerminalStatusResult, error) {
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	alive := s.termMgr.SessionAlive(session)
	result := &TerminalStatusResult{Alive: alive}

	if !alive {
		if captured, err := s.termMgr.CapturePane(session, 10); err == nil && captured != "" {
			result.ExitReason = captured
		}
	} else if s.termMgr.PaneDead(session) {
		result.Alive = false
		if captured, err := s.termMgr.CapturePane(session, 10); err == nil && captured != "" {
			result.ExitReason = captured
		}
	}

	return result, nil
}

func (s *terminalServiceImpl) ListSessions(_ context.Context, wsID string) ([]TerminalSessionInfo, error) {
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}
	sessions, err := s.termMgr.ListActiveSessionsForWorkspace(wsID)
	if err != nil {
		return nil, service.ErrInternal("failed to list terminal sessions", err)
	}
	return sessions, nil
}

func (s *terminalServiceImpl) SpawnSession(_ context.Context, wsID string, params *SpawnParams) (*SpawnResult, error) {
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	if params.SessionName == "" {
		return nil, service.ErrValidation("missing required field: session_name")
	}
	if params.Backend == "" {
		return nil, service.ErrValidation("missing required field: backend")
	}

	// Sanitize dots to dashes (issue IDs like loomcli-fghge.1 contain dots)
	sanitizedName := strings.ReplaceAll(params.SessionName, ".", "-")
	if !validSessionName.MatchString(sanitizedName) {
		return nil, service.ErrValidation(fmt.Sprintf("invalid session name %q after sanitization: must match [a-zA-Z0-9_-]+", sanitizedName))
	}

	var command string
	if params.Backend == "shell" {
		command = shellCommand()
	} else if !isValidBackend(params.Backend) {
		return nil, service.ErrValidation(fmt.Sprintf("invalid backend %q; valid: %s", params.Backend, strings.Join(validBackends, ", ")))
	} else {
		command = params.Backend
	}

	created, err := s.termMgr.Spawn(sanitizedName, command, 120, 40)
	if err != nil {
		return nil, service.ErrInternal("failed to spawn terminal session", err)
	}

	if created {
		// Record workspace ownership
		if wsID != "" {
			s.termMgr.SetSessionOwner(sanitizedName, wsID)
		}
		// Record session history for issue-linked sessions
		if s.histStore != nil {
			issueID := extractIssueID(sanitizedName)
			if issueID != "" {
				now := time.Now().UTC()
				record := sessionhistory.SessionRecord{
					ID:          fmt.Sprintf("%s:%d", sanitizedName, now.Unix()),
					SessionName: sanitizedName,
					IssueID:     issueID,
					Backend:     params.Backend,
					Status:      "active",
					Launcher:    "user",
					StartedAt:   now,
				}
				if err := s.histStore.Add(context.Background(), wsID, record); err != nil {
					slog.Warn("failed to record session history", "session", sanitizedName, "err", err)
				}
			}
		}
	}

	return &SpawnResult{
		SessionName: sanitizedName,
		Backend:     params.Backend,
		Command:     command,
		Created:     created,
	}, nil
}

func (s *terminalServiceImpl) SeedSession(_ context.Context, session string, params *SeedParams) error {
	if s.termMgr == nil {
		return service.ErrUnavailable("terminal manager not initialized")
	}
	if session == "" {
		return service.ErrValidation("missing session name")
	}
	if params.IssueID == "" || params.Title == "" {
		return service.ErrValidation("issue_id and title are required")
	}

	prompt := formatSeedPromptFromParams(params)
	if err := s.termMgr.SendKeys(session, prompt); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return service.ErrNotFound("session not found: " + session)
		}
		return service.ErrInternal("failed to seed terminal session", err)
	}
	return nil
}

func (s *terminalServiceImpl) ScheduleKill(_ context.Context, session string) error {
	if s.termMgr == nil {
		return service.ErrUnavailable("terminal manager not initialized")
	}
	if err := tabmeta.ValidateSessionName(session); err != nil {
		return service.ErrValidation(err.Error())
	}
	s.termMgr.ScheduleKill(session, sessionKillGracePeriod)
	return nil
}

func (s *terminalServiceImpl) CloseAllSessions(ctx context.Context) (*CloseAllResult, error) {
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	if err := s.termMgr.KillAllSessions(); err != nil {
		return nil, service.ErrInternal("failed to kill all sessions", err)
	}

	result := &CloseAllResult{}
	affectedWorkspaces := make(map[string]bool)

	if s.tabStore != nil {
		allTabs, err := s.tabStore.ListAll(ctx)
		if err != nil {
			slog.Error("failed to list tab metadata for cleanup", "err", err)
			result.MetaCleanupIncomplete = true
		} else {
			for _, tab := range allTabs {
				if tab.Workspace != "" {
					affectedWorkspaces[tab.Workspace] = true
				}
				if err := s.tabStore.Delete(ctx, tab.Workspace, tab.SessionName); err != nil {
					slog.Error("failed to delete tab metadata", "session", tab.SessionName, "err", err)
					result.MetaCleanupIncomplete = true
				}
			}
		}
	}

	// Broadcast SSE event per affected workspace
	if s.hub != nil {
		now := time.Now().UTC().Format(time.RFC3339)
		for ws := range affectedWorkspaces {
			s.hub.Broadcast(&realtime.MutationPayload{
				Type:        "terminal_session_change",
				Timestamp:   now,
				WorkspaceID: ws,
			})
		}
	}

	for ws := range affectedWorkspaces {
		result.AffectedWorkspaces = append(result.AffectedWorkspaces, ws)
	}

	return result, nil
}

func (s *terminalServiceImpl) ExportSession(_ context.Context, session string) (string, error) {
	if session == "" || !validTerminalSession.MatchString(session) {
		return "", service.ErrValidation("invalid session name")
	}
	if s.termMgr == nil {
		return "", service.ErrUnavailable("terminal manager not initialized")
	}
	if !s.termMgr.SessionAlive(session) {
		return "", service.ErrNotFound("session not found")
	}

	content, err := s.termMgr.ExportSession(session)
	if err != nil {
		return "", service.ErrInternal("failed to capture scrollback", err)
	}

	// Strip ANSI escape codes for clean export.
	content = StripANSI(content)
	return content, nil
}

func (s *terminalServiceImpl) GetScrollbackInfo(_ context.Context, session string) (*ScrollbackInfoResult, error) {
	if session == "" || !validTerminalSession.MatchString(session) {
		return nil, service.ErrValidation("invalid session name")
	}
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	buf := s.termMgr.LookupScrollbackBuffer(session)
	result := &ScrollbackInfoResult{
		MaxLines: s.termMgr.ScrollbackMaxLines(),
	}
	if buf != nil {
		result.LineCount = buf.LineCount()
		result.TruncatedCount = buf.TruncatedCount()
	}
	return result, nil
}

func (s *terminalServiceImpl) GetScrollback(_ context.Context, session string) (*ScrollbackResult, error) {
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}
	if session == "" {
		return nil, service.ErrValidation("session name is required")
	}

	content, err := s.termMgr.CaptureScrollback(session)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, service.ErrNotFound("session not found")
		}
		return nil, service.ErrInternal("failed to capture scrollback", err)
	}

	lines := 0
	if content != "" {
		lines = strings.Count(content, "\n") + 1
	}
	return &ScrollbackResult{Content: content, Lines: lines}, nil
}

// formatSeedPromptFromParams builds the context prompt string from SeedParams.
func formatSeedPromptFromParams(params *SeedParams) string {
	var b strings.Builder

	fmt.Fprintf(&b, "I need help with issue %s: %s", params.IssueID, params.Title)

	if params.Description != "" {
		fmt.Fprintf(&b, "\n\nDescription: %s", truncateSvc(params.Description, maxDescriptionLen))
	}

	if params.Design != "" {
		fmt.Fprintf(&b, "\n\nDesign: %s", truncateSvc(params.Design, maxDesignLen))
	}

	if len(params.Blockers) > 0 {
		b.WriteString("\n\nBlockers:")
		limit := len(params.Blockers)
		if limit > maxBlockers {
			limit = maxBlockers
		}
		for _, blocker := range params.Blockers[:limit] {
			fmt.Fprintf(&b, "\n- %s: %s", blocker.ID, blocker.Title)
		}
	}

	return b.String()
}

// truncateSvc returns s truncated to maxLen runes with "..." suffix if needed.
func truncateSvc(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
