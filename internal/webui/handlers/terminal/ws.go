package terminal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"nhooyr.io/websocket" //nolint:staticcheck // SA1019: websocket migration tracked separately

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

var (
	errAgentLaunchSpecMissing    = errors.New("agent terminal launch spec missing")
	errAgentTerminalStopped      = errors.New("agent terminal is stopped; start the agent before connecting")
	errTerminalLaunchMetaMissing = errors.New("terminal launch metadata missing")
)

// terminalTracerName is the instrumentation library name for terminal WS
// spans. Stable so dashboards filtering on it don't break.
const terminalTracerName = "github.com/tysonthomas9/loomcli/internal/webui/handlers/terminal"

// Disconnect reasons reported on `ws.disconnect` spans. Bounded enum so
// the `disconnect.reason` attribute stays low-cardinality. See
// docs/observability/tracing-contract.md §3.
const (
	wsDisconnectReasonClientClose   = "client_close"
	wsDisconnectReasonServerClose   = "server_close"
	wsDisconnectReasonBackendExited = "backend_exited"
	wsDisconnectReasonSessionKilled = "session_killed"
	wsDisconnectReasonError         = "error"
)

// wsCloseReason maps a websocket close status to one of the bounded
// disconnectReason* enum values. Anything we don't explicitly recognize
// collapses to "error" so the cardinality of `disconnect.reason` stays
// tied to the enum, not the (effectively unbounded) close-code space.
func wsCloseReason(status websocket.StatusCode) string { //nolint:staticcheck // SA1019
	switch status {
	case websocket.StatusNormalClosure: //nolint:staticcheck // SA1019
		return wsDisconnectReasonClientClose
	case websocket.StatusGoingAway: //nolint:staticcheck // SA1019
		return wsDisconnectReasonServerClose
	case websocket.StatusCode(realtime.WSCloseBackendExited): //nolint:staticcheck // SA1019
		return wsDisconnectReasonBackendExited
	case websocket.StatusCode(realtime.WSCloseSessionKilled): //nolint:staticcheck // SA1019
		return wsDisconnectReasonSessionKilled
	default:
		return wsDisconnectReasonError
	}
}

// terminalWSParams holds the dependencies for a terminal WebSocket handler.
type terminalWSParams struct {
	manager       webuterminal.PTYSource
	auth          *realtime.TerminalAuth
	patterns      []string
	loomServerURL string
	store         store.Store
	tabMetaStore  *tabmeta.Store
	hub           *realtime.Hub
	// serverStartedAt is used to distinguish "tab metadata from a prior
	// server process whose PTY is long gone" from "tab metadata just
	// created in this process that hasn't spawned yet". Only the former
	// triggers a 4410 close; the latter proceeds to AttachSession as
	// normal.
	serverStartedAt time.Time
}

type workspacePTYEnsurer interface {
	EnsureRegistered(wsID, path string) error
}

// HandleTerminalWS returns a WebSocket handler for terminal relay. It upgrades
// the HTTP connection to a WebSocket, attaches to (or creates) the persistent
// session identified by the (workspace, session) pair, and bridges
// stdin/stdout bidirectionally using the wterm wire format: binary output
// frames and a \x1b[RESIZE:cols;rows] escape for resize.
//
// Unlike the previous PTY-per-WS model, the session outlives the WebSocket:
// on disconnect the PTY and child process stay alive for a grace period so a
// reconnecting client gets its shell and scrollback back. See PTYManager for
// the lifecycle details.
func HandleTerminalWS(manager webuterminal.PTYSource, auth *realtime.TerminalAuth, allowedOrigins []string, loomServerURL string, st store.Store, tabMetaStore *tabmeta.Store, hub *realtime.Hub, serverStartedAt time.Time) http.HandlerFunc {
	p := &terminalWSParams{
		manager:         manager,
		auth:            auth,
		patterns:        originHosts(allowedOrigins),
		loomServerURL:   loomServerURL,
		store:           st,
		tabMetaStore:    tabMetaStore,
		hub:             hub,
		serverStartedAt: serverStartedAt,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		session, workspace, ok := validateTerminalWSRequest(w, r, p.manager, p.auth)
		if !ok {
			return
		}
		initialCols, initialRows := initialTerminalSizeFromRequest(r)

		conn, upgradeCtx, ok := upgradeTerminalWithSpan(w, r, p.patterns, session, workspace)
		if !ok {
			return
		}

		closeStatus := websocket.StatusInternalError
		closeReason := "connection closed"
		defer func() {
			_ = conn.Close(closeStatus, closeReason) //nolint:staticcheck // SA1019: websocket migration tracked separately
			emitDisconnectSpan(upgradeCtx, workspace, session, closeStatus)
		}()

		closeStatus, closeReason = runTerminalRelay(
			r.Context(),
			conn,
			p,
			session,
			workspace,
			initialCols,
			initialRows,
		)
	}
}

// upgradeTerminalWithSpan opens a short-lived ws.upgrade span around the
// websocket handshake. We end the span before entering the bidirectional
// relay so we don't hold a multi-minute span open in Jaeger; the long-lived
// relay is unspanned by design (per-message spans would flood the
// collector). See docs/observability/tracing-contract.md §3.
func upgradeTerminalWithSpan(w http.ResponseWriter, r *http.Request, patterns []string, session, workspace string) (*websocket.Conn, context.Context, bool) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	upgradeCtx, upgradeSpan := otel.Tracer(terminalTracerName).Start(r.Context(), "ws.upgrade",
		trace.WithAttributes(
			attribute.String("loom.workspace", workspace),
			attribute.String("loom.session_id", session),
			attribute.String("network.peer.address", r.RemoteAddr),
		),
	)
	conn, ok := upgradeTerminalWS(w, r, patterns)
	if !ok {
		upgradeSpan.SetStatus(codes.Error, "network")
		upgradeSpan.End()
		return nil, upgradeCtx, false
	}
	upgradeSpan.End()
	return conn, upgradeCtx, true
}

// emitDisconnectSpan records a sibling span on terminal-WS close. The
// close-reason enum is bounded so dashboards can group disconnects without
// keeping a span open for the lifetime of the relay.
func emitDisconnectSpan(upgradeCtx context.Context, workspace, session string, closeStatus websocket.StatusCode) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	_, discSpan := otel.Tracer(terminalTracerName).Start(context.Background(), "ws.disconnect",
		trace.WithLinks(trace.LinkFromContext(upgradeCtx)),
		trace.WithAttributes(
			attribute.String("loom.workspace", workspace),
			attribute.String("loom.session_id", session),
			attribute.String("disconnect.reason", wsCloseReason(closeStatus)),
		),
	)
	if closeStatus == websocket.StatusInternalError { //nolint:staticcheck // SA1019
		discSpan.SetStatus(codes.Error, "crash")
	}
	discSpan.End()
}

// validateTerminalWSRequest validates the session parameter, auth token, and
// session limit. Returns (session, workspace, true) on success.
func validateTerminalWSRequest(w http.ResponseWriter, r *http.Request, manager webuterminal.PTYSource, auth *realtime.TerminalAuth) (string, string, bool) {
	if manager == nil {
		handler.WriteJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "terminal manager not initialized",
		})
		return "", "", false
	}

	session := r.URL.Query().Get("session")
	workspace := middleware.WorkspaceFromContext(r.Context())
	if session == "" {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "missing session parameter",
		})
		return "", "", false
	}
	if !validTerminalSession.MatchString(session) {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "invalid session name: must match [a-zA-Z0-9_-]+",
		})
		return "", "", false
	}

	if !authenticateTerminalSession(w, r, auth, session, workspace) {
		return "", "", false
	}

	// Reject only when we'd have to spawn a *new* session past the cap.
	// Reconnects to an existing (workspace, session) must still pass —
	// AttachSession doesn't count them against the cap, and a 503 here
	// would lock users out of live sessions until one is killed.
	key := webuterminal.SessionKey{Workspace: workspace, Name: session}
	if !manager.HasSession(key) && manager.SessionCountFor(workspace) >= manager.MaxSessions() {
		handler.WriteJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "maximum terminal sessions reached",
		})
		return "", "", false
	}
	return session, workspace, true
}

// authenticateTerminalSession validates the one-time terminal token if auth is configured.
func authenticateTerminalSession(w http.ResponseWriter, r *http.Request, auth *realtime.TerminalAuth, session, workspace string) bool {
	if auth == nil {
		return true
	}
	token := r.URL.Query().Get("token")
	userID, err := auth.ValidateToken(token, session, workspace)
	if err != nil {
		handler.WriteJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false, "error": "terminal authentication failed",
		})
		slog.Warn("terminal auth failed", "session", session, "workspace", workspace, "err", err)
		return false
	}
	if userID != "" {
		slog.Info("terminal session authenticated", "session", session, "workspace", workspace, "user_id", userID)
	}
	return true
}

// upgradeTerminalWS performs the WebSocket upgrade for a terminal connection.
func upgradeTerminalWS(w http.ResponseWriter, r *http.Request, patterns []string) (*websocket.Conn, bool) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("terminal ws: failed to disable write deadline", "err", err)
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{ //nolint:staticcheck // SA1019: websocket migration tracked separately
		OriginPatterns: patterns,
	})
	if err != nil {
		slog.Error("failed to accept websocket", "err", err)
		return nil, false
	}
	conn.SetReadLimit(realtime.WSReadLimit) //nolint:staticcheck // SA1019: websocket migration tracked separately
	return conn, true
}

// classifyAttachErr maps an AttachSession error to a WebSocket close code
// and log line. ErrPTYManagerClosed / ErrWorkspaceNotRegistered are
// expected outcomes when a workspace is deregistered mid-flight; they
// close with StatusGoingAway at INFO level rather than StatusInternalError
// at ERROR level, so on-call noise doesn't spike on every workspace delete.
func classifyAttachErr(err error, session, workspace string) (websocket.StatusCode, string) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	switch {
	case errors.Is(err, errAgentLaunchSpecMissing):
		slog.Error("agent terminal metadata missing launch spec", "session", session, "workspace", workspace)
		return websocket.StatusInternalError, err.Error() //nolint:staticcheck // SA1019
	case errors.Is(err, errAgentTerminalStopped):
		slog.Info("stopped agent terminal attach refused", "session", session, "workspace", workspace)
		return websocket.StatusPolicyViolation, err.Error() //nolint:staticcheck // SA1019
	case errors.Is(err, errDaemonSupervisedWorkerTerminal):
		slog.Info("daemon-supervised worker terminal attach refused", "session", session, "workspace", workspace)
		return websocket.StatusPolicyViolation, err.Error() //nolint:staticcheck // SA1019
	case errors.Is(err, errTerminalLaunchMetaMissing):
		slog.Error("terminal metadata missing launch spec", "session", session, "workspace", workspace)
		return websocket.StatusInternalError, err.Error() //nolint:staticcheck // SA1019
	case errors.Is(err, webuterminal.ErrPTYMaxSessionsReached):
		slog.Info("terminal session limit reached", "session", session)
		return websocket.StatusInternalError, err.Error() //nolint:staticcheck // SA1019
	case errors.Is(err, webuterminal.ErrPTYManagerClosed), errors.Is(err, webuterminal.ErrWorkspaceNotRegistered):
		slog.Info("terminal attach after workspace unavailable", "session", session, "workspace", workspace, "err", err)
		return websocket.StatusGoingAway, "workspace unavailable" //nolint:staticcheck // SA1019
	default:
		slog.Error("failed to attach terminal session", "session", session, "err", err)
		return websocket.StatusInternalError, err.Error() //nolint:staticcheck // SA1019
	}
}

// runTerminalRelay attaches to the (workspace, session) PTY session and runs
// the bidirectional relay until the WebSocket closes. On WS close the session
// is detached (grace period armed); the PTY and child process stay alive.
func runTerminalRelay(reqCtx context.Context, conn *websocket.Conn, p *terminalWSParams, session, workspace string, initialCols, initialRows uint16) (websocket.StatusCode, string) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	key := webuterminal.SessionKey{Workspace: workspace, Name: session}
	ensureWorkspacePTYRegistered(reqCtx, p, workspace)

	att, reattach, err := attachTerminalSession(reqCtx, p, key, initialCols, initialRows)
	if err != nil {
		return classifyAttachErr(err, session, workspace)
	}
	connID := att.ConnID()

	// Freshly spawned "talk-to-lead" sessions get a project-context banner
	// written before the shell's prompt. On reattach the banner is already
	// part of the scrollback replay.
	if !reattach && session == "talk-to-lead" && p.loomServerURL != "" {
		wsID := middleware.WorkspaceFromContext(reqCtx)
		injectTerminalContextBanner(att, p.loomServerURL, workspaceNameFromStore(reqCtx, p.store, wsID))
	}

	realtime.BroadcastSessionIssueEvent(p.tabMetaStore, p.hub, workspace, session)

	if !reattach {
		maybeEmitStaleRestartBanner(reqCtx, conn, p, workspace, session)
	}

	// Emit scrollback replay (reset escape + ring bytes) before going live.
	if replay := att.Scrollback(); len(replay) > 0 {
		if err := conn.Write(reqCtx, websocket.MessageBinary, replay); err != nil { //nolint:staticcheck // SA1019
			slog.Warn("scrollback replay write failed", "session", session, "err", err)
		}
	}

	ctx, cancel := context.WithCancel(reqCtx)
	defer cancel()

	crashCh := make(chan realtime.CrashInfo, 1)
	go func() {
		// Pump attachment output → WS. Exits when the channel closes
		// (session killed or replaced) or the context is cancelled.
		crashCh <- realtime.AttachmentToWS(ctx, cancel, conn, att)
	}()

	// WS → PTY until the client disconnects. The attachment satisfies
	// realtime.Resizer directly so the manager doesn't need a connID → PTY
	// lookup table.
	realtime.WSToPTY(ctx, conn, attachmentWriter{att}, att, connID)

	// WebSocket gone — detach the attachment. PTY stays alive for the
	// manager's grace period.
	p.manager.Detach(key, connID)

	return (<-crashCh).WSClose()
}

// attachTerminalSession resolves the server-owned launch contract before
// asking the PTY manager to attach or spawn. Keeping the authorization check
// on this boundary ensures a persisted tab cannot respawn a stopped
// interactive agent through a direct or reconnecting WebSocket.
func attachTerminalSession(
	ctx context.Context,
	p *terminalWSParams,
	key webuterminal.SessionKey,
	cols, rows uint16,
) (webuterminal.Attachment, bool, error) {
	launch, agentID, err := resolveTerminalLaunch(ctx, p, key.Workspace, key.Name)
	if err != nil {
		return nil, false, err
	}
	if agentID != "" {
		// resolveTerminalLaunch may have observed running just before Stop
		// entered its ownership snapshot. Re-authorize after taking Stop's
		// ordering boundary so an attachment either completes before Stop
		// snapshots/kills it or observes the durable stopped state afterward.
		unlock := webuterminal.LockAgentLifecycle(key.Workspace, agentID)
		defer unlock()
		if err := authorizeAgentTerminalLaunch(ctx, p.store, key.Workspace, agentID); err != nil {
			return nil, false, err
		}
	}
	attachment, reattached, err := p.manager.AttachSession(key, cols, rows, launch)
	if err != nil {
		return nil, false, err
	}
	if agentID == "" {
		return attachment, reattached, nil
	}

	// Keep the post-attach state check as a fail-closed defense if lifecycle
	// state is changed by a path that does not participate in the ordering
	// boundary.
	if err := authorizeAgentTerminalLaunch(ctx, p.store, key.Workspace, agentID); err != nil {
		if killErr := p.manager.Kill(key); killErr != nil {
			return nil, false, fmt.Errorf("%w; additionally failed to fence spawned PTY: %v", err, killErr)
		}
		return nil, false, err
	}
	return attachment, reattached, nil
}

func ensureWorkspacePTYRegistered(ctx context.Context, p *terminalWSParams, workspace string) {
	if p == nil || workspace == "" {
		return
	}
	ensurer, ok := p.manager.(workspacePTYEnsurer)
	if !ok {
		return
	}
	// Healing resolve: if the local path is missing from state.json, re-bind it
	// from an existing on-disk checkout so the PTY can register (otherwise the
	// attach fails with "workspace unavailable" → the UI shows "Disconnected").
	path := storeadapter.ResolveOrHealWorkspacePath(ctx, p.store, workspace)
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := ensurer.EnsureRegistered(workspace, path); err != nil {
		slog.Warn("terminal workspace pty self-heal failed",
			"workspace", workspace, "path", path, "err", err)
	}
}

func launchSpecForTerminalSession(ctx context.Context, p *terminalWSParams, workspace, session string) (*tabmeta.LaunchSpec, error) {
	launch, _, err := resolveTerminalLaunch(ctx, p, workspace, session)
	return launch, err
}

func resolveTerminalLaunch(
	ctx context.Context,
	p *terminalWSParams,
	workspace, session string,
) (*tabmeta.LaunchSpec, string, error) {
	if p.tabMetaStore == nil {
		if isUUIDTerminalSession(session) {
			return nil, "", errTerminalLaunchMetaMissing
		}
		return legacyLaunchSpecForSession(session), "", nil
	}
	meta, err := p.tabMetaStore.Get(ctx, workspace, session)
	if err != nil {
		return nil, "", fmt.Errorf("load terminal metadata: %w", err)
	}
	if meta == nil {
		if isUUIDTerminalSession(session) {
			return nil, "", errTerminalLaunchMetaMissing
		}
		return legacyLaunchSpecForSession(session), "", nil
	}
	if meta.Kind == "agent" {
		if err := authorizeAgentTerminalLaunch(ctx, p.store, workspace, meta.AgentID); err != nil {
			return nil, "", err
		}
		if meta.Launch == nil || (len(meta.Launch.Argv) == 0 && len(meta.Launch.Env) == 0) {
			return nil, "", errAgentLaunchSpecMissing
		}
		if len(meta.Launch.Argv) == 0 {
			return nil, "", errAgentLaunchSpecMissing
		}
		return meta.Launch, meta.AgentID, nil
	}
	if meta.Launch != nil && (len(meta.Launch.Argv) > 0 || len(meta.Launch.Env) > 0) {
		return meta.Launch, "", nil
	}
	return legacyLaunchSpecForSession(session), "", nil
}

func authorizeAgentTerminalLaunch(ctx context.Context, st store.Store, workspace, agentID string) error {
	if st == nil || st.Agents() == nil {
		return errors.New("agent terminal state store unavailable")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return errors.New("agent terminal metadata missing agent identity")
	}
	agent, err := st.Agents().Get(ctx, workspace, agentID)
	if err != nil {
		return fmt.Errorf("load agent terminal state: %w", err)
	}
	role, err := loadAgentLaunchRole(ctx, st, workspace, agent.RoleName)
	if err != nil {
		return fmt.Errorf("load agent terminal role: %w", err)
	}
	if isDaemonSupervisedWorker(agent, domain.ResolveRoleKind(role, agent.RoleName)) {
		return errDaemonSupervisedWorkerTerminal
	}
	if agent.State == domain.AgentStateStopped || agent.DesiredState == domain.AgentDesiredStopped {
		return errAgentTerminalStopped
	}
	return nil
}

func isUUIDTerminalSession(session string) bool {
	return strings.HasPrefix(session, "term_")
}

func legacyLaunchSpecForSession(session string) *tabmeta.LaunchSpec {
	argv := webuterminal.ArgvForSession(session)
	if len(argv) == 0 {
		return nil
	}
	return &tabmeta.LaunchSpec{Argv: argv}
}

func initialTerminalSizeFromRequest(r *http.Request) (uint16, uint16) {
	const (
		defaultCols = 80
		defaultRows = 24
	)

	parse := func(raw string, fallback int, max int) uint16 {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > max {
			return mustUint16(fallback)
		}
		return mustUint16(n)
	}

	return parse(r.URL.Query().Get("cols"), defaultCols, realtime.MaxTerminalCols),
		parse(r.URL.Query().Get("rows"), defaultRows, realtime.MaxTerminalRows)
}

func mustUint16(n int) uint16 {
	if n < 0 || n > int(^uint16(0)) {
		panic(fmt.Sprintf("terminal size %d exceeds uint16 range", n))
	}
	return uint16(n)
}

// attachmentWriter adapts Attachment to realtime.WSToPTY's io.Writer input.
type attachmentWriter struct{ a webuterminal.Attachment }

func (w attachmentWriter) Write(p []byte) (int, error) { return w.a.WriteInput(p) }

// maybeEmitStaleRestartBanner writes a visible notice to the freshly-spawned
// shell when the tab's metadata pre-dates the current server process — i.e.
// the prior PTY died with a previous server. The frontend's pty_alive gate
// on the tab DTO is the authoritative block, but browsers drop app-defined
// WebSocket close codes right after upgrade, so this in-band banner is the
// reliable fallback for any client that reached this path anyway.
func maybeEmitStaleRestartBanner(reqCtx context.Context, conn *websocket.Conn, p *terminalWSParams, workspace, session string) { //nolint:staticcheck // SA1019
	if p.tabMetaStore == nil {
		return
	}
	meta, err := p.tabMetaStore.Get(reqCtx, workspace, session)
	if err != nil || meta == nil || !meta.CreatedAt.Before(p.serverStartedAt) {
		return
	}
	slog.Info("terminal session stale across server restart; spawning fresh",
		"session", session, "workspace", workspace, "created_at", meta.CreatedAt)
	_ = conn.Write(reqCtx, websocket.MessageBinary, []byte("\r\n\x1b[33m[loom] Previous shell did not survive a server restart. This is a fresh session.\x1b[0m\r\n")) //nolint:staticcheck // SA1019
}

// injectTerminalContextBanner fetches project context from the loom server
// and writes a formatted banner to the newly attached session.
func injectTerminalContextBanner(att webuterminal.Attachment, loomServerURL string, wsName string) {
	tc, err := webuterminal.FetchTerminalContext(loomServerURL)
	if err != nil {
		slog.Error("terminal context fetch failed, skipping banner", "err", err)
		return
	}

	banner := webuterminal.FormatContextBanner(tc, wsName)
	if _, writeErr := att.WriteInput([]byte(banner)); writeErr != nil {
		slog.Warn("failed to write context banner to pty", "err", writeErr)
	}
}

func workspaceNameFromStore(ctx context.Context, st store.Store, wsID string) string {
	if st == nil || wsID == "" {
		return ""
	}
	ws, err := st.Workspaces().Get(ctx, wsID)
	if err != nil || ws == nil {
		return ""
	}
	return ws.Name
}
