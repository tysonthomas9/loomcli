package terminal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

const terminalUIStateKeyImpl = "terminal:ui-state"

// terminalServiceImpl is the concrete implementation of TerminalService.
type terminalServiceImpl struct {
	termMgr        *TerminalManager
	termAuth       *realtime.TerminalAuth
	configPool     ConfigConnectionGetter
	tabStore       *tabmeta.Store
	hub            *realtime.Hub
	histStore      *sessionhistory.Store
	redisClient    *redis.Client
	workspaceByIDF func(string) (*ops.WorkspaceData, error)
}

// NewTerminalService creates a new TerminalService implementation.
// workspaceByIDF (optional) resolves a workspace ID to its on-disk path so
// spawned tmux sessions start in the active workspace's directory; if nil,
// sessions inherit the loom service's cwd.
func NewTerminalService(
	termMgr *TerminalManager,
	termAuth *realtime.TerminalAuth,
	configPool ConfigConnectionGetter,
	tabStore *tabmeta.Store,
	hub *realtime.Hub,
	histStore *sessionhistory.Store,
	redisClient *redis.Client,
	workspaceByIDF func(string) (*ops.WorkspaceData, error),
) service.TerminalService {
	return &terminalServiceImpl{
		termMgr:        termMgr,
		termAuth:       termAuth,
		configPool:     configPool,
		tabStore:       tabStore,
		hub:            hub,
		histStore:      histStore,
		redisClient:    redisClient,
		workspaceByIDF: workspaceByIDF,
	}
}

// workspacePath returns the on-disk path for wsID. Returns "" when the ID is
// empty, the resolver is nil, the lookup errors, or the resolved data has no
// path. An empty result means "inherit the loom service's cwd", matching the
// legacy (pre-workspace-scoped) spawn behavior.
func (s *terminalServiceImpl) workspacePath(wsID string) string {
	if wsID == "" || s.workspaceByIDF == nil {
		return ""
	}
	ws, err := s.workspaceByIDF(wsID)
	if err != nil || ws == nil {
		return ""
	}
	return ws.Path
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

func (s *terminalServiceImpl) RestartSession(ctx context.Context, wsID, session string) (*service.TerminalRestartResult, error) {
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	// Shell tabs: kill and return without changing defaultCommand.
	if strings.HasPrefix(session, "lead-shell-") {
		_ = s.termMgr.KillSessionByName(session)
		return &service.TerminalRestartResult{Backend: "shell"}, nil
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

	return &service.TerminalRestartResult{Backend: backend}, nil
}

func (s *terminalServiceImpl) KillSession(_ context.Context, session string) error {
	if s.termMgr == nil {
		return service.ErrUnavailable("terminal manager not initialized")
	}
	_ = s.termMgr.KillSessionByName(session)
	return nil
}

func (s *terminalServiceImpl) GetSessionStatus(_ context.Context, session string) (*service.TerminalStatusResult, error) {
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	alive := s.termMgr.SessionAlive(session)
	result := &service.TerminalStatusResult{Alive: alive}

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

func (s *terminalServiceImpl) ListSessions(_ context.Context, wsID string) ([]service.TerminalSessionInfo, error) {
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}
	sessions, err := s.termMgr.ListActiveSessionsForWorkspace(wsID)
	if err != nil {
		return nil, service.ErrInternal("failed to list terminal sessions", err)
	}
	return sessions, nil
}

func (s *terminalServiceImpl) SpawnSession(_ context.Context, wsID string, params *service.SpawnParams) (*service.SpawnResult, error) { //nolint:funlen
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

	// Resolve the workspace's on-disk path so tmux -c lands the new session
	// in the active workspace instead of the loom service's cwd. Empty when
	// unresolvable; SpawnInDir then falls back to the inherited cwd.
	workDir := s.workspacePath(wsID)

	created, err := s.termMgr.SpawnInDir(sanitizedName, command, 120, 40, workDir)
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
			issueID := ExtractIssueID(sanitizedName)
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

	return &service.SpawnResult{
		SessionName: sanitizedName,
		Backend:     params.Backend,
		Command:     command,
		Created:     created,
	}, nil
}

// leadMessageMaxLen caps the user-supplied message to avoid comically large argv
// payloads. tmux and the backend agent handle long prompts, but we draw a
// reasonable line here to protect against accidental pastes or abuse.
const leadMessageMaxLen = 16 * 1024

// CreateLeadSession spawns a detached tmux session running
// `loom lead --backend <backend> --message <message>`. Because the message is
// baked into the argv, the backend agent receives the user's request as part
// of its initial prompt — no send-keys, no readiness polling, no TUI scraping.
func (s *terminalServiceImpl) CreateLeadSession(_ context.Context, wsID string, params *service.LeadSessionParams) (*service.LeadSessionResult, error) {
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	message := strings.TrimSpace(params.Message)
	if message == "" {
		return nil, service.ErrValidation("message is required")
	}
	if len(message) > leadMessageMaxLen {
		return nil, service.ErrValidation(fmt.Sprintf("message too long (max %d bytes)", leadMessageMaxLen))
	}

	backend := strings.TrimSpace(params.Backend)
	if backend == "" {
		return nil, service.ErrValidation("backend is required")
	}
	if !isValidBackend(backend) {
		return nil, service.ErrValidation(fmt.Sprintf("invalid backend %q; valid: %s", backend, strings.Join(validBackends, ", ")))
	}

	// "lead-<backend>-<unix_ms>" — timestamp-based so concurrent submissions
	// get unique names without inspecting existing sessions.
	sessionName := fmt.Sprintf("lead-%s-%d", backend, time.Now().UnixMilli())
	if !validSessionName.MatchString(sessionName) {
		// Defensive: backend name and digits should always satisfy the regex.
		return nil, service.ErrInternal("generated session name failed validation", nil)
	}

	// Passing argv as separate elements avoids shell interpretation of the
	// user's message — no quoting bugs, no injection.
	argv := []string{"loom", "lead", "--backend", backend, "--message", message}

	// Workspace cwd: SpawnArgv -c <workDir> starts the tmux session in the
	// active workspace's path (e.g., fixes the paperclip-in-loom cwd bug).
	// Empty falls back to the loom service's cwd.
	workDir := s.workspacePath(wsID)

	created, err := s.termMgr.SpawnArgv(sessionName, argv, 120, 40, workDir)
	if err != nil {
		return nil, service.ErrInternal("failed to spawn lead session", err)
	}
	if !created {
		return nil, service.ErrConflict("session already exists")
	}
	if wsID != "" {
		s.termMgr.SetSessionOwner(sessionName, wsID)
	}

	return &service.LeadSessionResult{
		SessionName: sessionName,
		Backend:     backend,
	}, nil
}

func (s *terminalServiceImpl) SeedSession(_ context.Context, session string, params *service.SeedParams) error {
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

// CloseAllSessions kills every tmux session belonging to wsID, deletes that
// workspace's tab metadata, and broadcasts one SSE event to the workspace.
// Sessions in other workspaces are untouched — multi-VM deployments must not
// accidentally kill a sibling workspace's sessions.
//
// When tabStore is nil (no Redis, single-workspace deployment) the workspace
// membership is unknown, so fall back to KillAllSessions — doing nothing
// would leave user-visible sessions running after "close all", worse than the
// scoping loss.
func (s *terminalServiceImpl) CloseAllSessions(ctx context.Context, wsID string) (*service.CloseAllResult, error) {
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	result := &service.CloseAllResult{}

	if s.tabStore == nil {
		return s.closeAllUnscoped(wsID, result)
	}

	sessionNames := s.workspaceSessionNames(ctx, wsID, result)

	for _, name := range sessionNames {
		if err := s.termMgr.KillSessionByName(name); err != nil {
			slog.Error("failed to kill session", "session", name, "err", err)
		}
		if err := s.tabStore.Delete(ctx, wsID, name); err != nil {
			slog.Error("failed to delete tab metadata", "session", name, "err", err)
			result.MetaCleanupIncomplete = true
		}
	}

	s.broadcastSessionChange(wsID)
	if wsID != "" {
		result.AffectedWorkspaces = append(result.AffectedWorkspaces, wsID)
	}
	return result, nil
}

// closeAllUnscoped kills every tmux session the manager knows about — used as
// the no-Redis fallback since workspace membership is unknown without the tab
// store.
func (s *terminalServiceImpl) closeAllUnscoped(wsID string, result *service.CloseAllResult) (*service.CloseAllResult, error) {
	if err := s.termMgr.KillAllSessions(); err != nil {
		return nil, service.ErrInternal("failed to kill all sessions", err)
	}
	s.broadcastSessionChange(wsID)
	return result, nil
}

// workspaceSessionNames lists session names owned by wsID via the tab store.
// Marks result.MetaCleanupIncomplete on listing error.
func (s *terminalServiceImpl) workspaceSessionNames(ctx context.Context, wsID string, result *service.CloseAllResult) []string {
	if wsID == "" {
		return nil
	}
	tabs, err := s.tabStore.List(ctx, wsID)
	if err != nil {
		slog.Error("failed to list tab metadata for workspace", "ws", wsID, "err", err)
		result.MetaCleanupIncomplete = true
		return nil
	}
	names := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		names = append(names, tab.SessionName)
	}
	return names
}

func (s *terminalServiceImpl) broadcastSessionChange(wsID string) {
	if s.hub == nil {
		return
	}
	s.hub.Broadcast(&realtime.MutationPayload{
		Type:        "terminal_session_change",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		WorkspaceID: wsID,
	})
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

func (s *terminalServiceImpl) GetScrollbackInfo(_ context.Context, session string) (*service.ScrollbackInfoResult, error) {
	if session == "" || !validTerminalSession.MatchString(session) {
		return nil, service.ErrValidation("invalid session name")
	}
	if s.termMgr == nil {
		return nil, service.ErrUnavailable("terminal manager not initialized")
	}

	buf := s.termMgr.LookupScrollbackBuffer(session)
	result := &service.ScrollbackInfoResult{
		MaxLines: s.termMgr.ScrollbackMaxLines(),
	}
	if buf != nil {
		result.LineCount = buf.LineCount()
		result.TruncatedCount = buf.TruncatedCount()
	}
	return result, nil
}

func (s *terminalServiceImpl) GetScrollback(_ context.Context, session string) (*service.ScrollbackResult, error) {
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
	return &service.ScrollbackResult{Content: content, Lines: lines}, nil
}

// formatSeedPromptFromParams builds the context prompt string from SeedParams.
func formatSeedPromptFromParams(params *service.SeedParams) string {
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
