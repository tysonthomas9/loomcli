package webui

import (
	"context"
	"encoding/binary"
	"errors"
	"log"
	"net/http"
	"time"

	"nhooyr.io/websocket"

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// validateTerminalWSParams validates the query parameters and pre-upgrade
// conditions for a terminal WebSocket connection. It returns the session name,
// workspace name, and true on success. On validation failure it writes an HTTP
// error response and returns ("", "", false).
//
// The workspace comes from the request context (injected by WorkspaceMiddleware
// from the URL path) now that this endpoint lives on wsMux under
// /api/workspaces/{ws}/terminal/ws. Falls back to "default" when context is
// absent so test harnesses that bypass middleware keep working.
func validateTerminalWSParams(w http.ResponseWriter, r *http.Request, manager *TerminalManager, auth *terminalAuth) (session, workspace string, ok bool) {
	// Check if manager is available
	if manager == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false,
			"error":   "terminal manager not initialized",
		})
		return "", "", false
	}

	// Parse and validate session parameter.
	session = r.URL.Query().Get("session")
	workspace = WorkspaceFromContext(r.Context())
	if workspace == "" {
		workspace = "default"
	}
	if session == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "missing session parameter",
		})
		return "", "", false
	}

	if !validTerminalSession.MatchString(session) {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "invalid session name: must match [a-zA-Z0-9_-]+",
		})
		return "", "", false
	}

	// Validate one-time terminal token before WebSocket upgrade
	if auth != nil {
		token := r.URL.Query().Get("token")
		if err := auth.ValidateToken(token, session); err != nil {
			respondJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"success": false,
				"error":   "terminal authentication failed",
			})
			log.Printf("Terminal auth failed for session %q: %v", session, err)
			return "", "", false
		}
	}

	// Pre-upgrade check: reject if session is being killed (tombstone).
	if manager.SessionIsBeingKilled(session) {
		respondJSON(w, http.StatusConflict, map[string]interface{}{
			"success": false,
			"error":   "session is being killed",
		})
		return "", "", false
	}

	// Pre-upgrade check: reject before WebSocket upgrade if at session limit.
	if manager.SessionCount() >= manager.MaxSessions() {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false,
			"error":   "maximum terminal sessions reached",
		})
		return "", "", false
	}

	return session, workspace, true
}

// handleTerminalWS returns a WebSocket handler for terminal relay.
// It upgrades HTTP connections to WebSocket, bridges them to tmux sessions
// via the TerminalManager, and handles bidirectional binary data relay
// plus an in-band resize protocol. The manager's current default command
// is used for new terminal sessions.
//
// allowedOrigins is a list of full origin URLs (e.g. "http://localhost:3000")
// whose host portions are used as OriginPatterns for the WebSocket upgrade.
// When nil or empty, only same-origin and non-browser (no Origin header)
// connections are accepted.
func handleTerminalWS(manager *TerminalManager, auth *terminalAuth, allowedOrigins []string, loomServerURL string, workspaceConfigFn func() (*WorkspaceData, error), tabMetaStore *tabmeta.Store, hub *SSEHub) http.HandlerFunc {
	// Compute origin host patterns once at construction time.
	patterns := originHosts(allowedOrigins)

	return func(w http.ResponseWriter, r *http.Request) {
		session, workspace, ok := validateTerminalWSParams(w, r, manager, auth)
		if !ok {
			return
		}

		// Disable write timeout for this long-lived WebSocket connection.
		// Must be called before websocket.Accept which hijacks the connection.
		rc := http.NewResponseController(w)
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			log.Printf("Terminal WS: failed to disable write deadline: %v", err)
		}

		// Accept WebSocket upgrade with origin validation.
		// OriginPatterns checks the Origin header against allowed hosts.
		// If no Origin header is present (non-browser clients), the connection is allowed.
		// If Origin matches the request Host (same-origin), the connection is allowed.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: patterns,
		})
		if err != nil {
			log.Printf("Failed to accept WebSocket: %v", err)
			return
		}
		conn.SetReadLimit(wsReadLimit)

		// Track close status for deferred cleanup
		closeStatus := websocket.StatusInternalError
		closeReason := "connection closed"
		defer func() {
			_ = conn.Close(closeStatus, closeReason)
		}()

		// Check whether the tmux session already exists before Attach creates it.
		// Only inject the context banner for freshly created talk-to-lead sessions.
		isNewSession := session == "talk-to-lead" && !manager.SessionExists(session)

		// Attach to terminal session with default 80x24 size
		// (frontend sends resize immediately after connect)
		termSession, err := manager.Attach(session, attachCommandForSession(session), 80, 24)
		if err != nil {
			if errors.Is(err, ErrSessionBeingKilled) {
				closeStatus = websocket.StatusCode(wsCloseSessionKilled)
				closeReason = "session is being killed"
			} else if errors.Is(err, ErrMaxSessionsReached) {
				log.Printf("Terminal session limit reached for %q", session)
				closeReason = err.Error()
			} else {
				log.Printf("Failed to attach terminal session %q: %v", session, err)
				closeReason = err.Error()
			}
			return
		}
		connID := termSession.ConnID

		// Inject project context banner into freshly created talk-to-lead sessions.
		if isNewSession && loomServerURL != "" {
			injectTerminalContextBanner(termSession, loomServerURL, workspaceConfigFn)
		}

		// Broadcast SSE event if this session is linked to an issue, so indicators update on connect.
		broadcastSessionIssueEvent(tabMetaStore, hub, workspace, session)

		// Create context for coordinating goroutines
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// Channel to signal when PTY reader finishes and communicate crash state
		crashCh := make(chan crashInfo, 1)

		// Watch for server-initiated kill (user closed the tab).
		go func() {
			select {
			case <-termSession.killCh:
				_ = conn.Close(websocket.StatusCode(wsCloseSessionKilled), "session killed")
				cancel()
			case <-ctx.Done():
			}
		}()

		// Start PTY -> WebSocket goroutine (with scrollback capture)
		scrollback := manager.GetScrollbackBuffer(session)
		go func() {
			result := ptyToWS(ctx, cancel, conn, termSession, manager, scrollback)
			crashCh <- result
		}()

		// Run WebSocket -> PTY relay (blocks until WebSocket closes)
		wsToPTY(ctx, conn, termSession, manager, connID)

		// WebSocket closed - detach connection to close PTY and unblock ptyToWS
		// (Detach closes the PTY, causing the Read in ptyToWS to return an error)
		if err := manager.Detach(connID); err != nil {
			log.Printf("Failed to detach terminal connection %q: %v", connID, err)
		}

		// Wait for PTY reader to finish and check for backend crash
		closeStatus, closeReason = (<-crashCh).wsClose()
	}
}

const wsCloseBackendExited = 4001 // WebSocket close code for backend process exit (4000-4999 range)
const wsCloseSessionKilled = 4002 // WebSocket close code for user-initiated session kill

// crashInfo communicates crash state from ptyToWS so the handler sets the right close code.
type crashInfo struct {
	crashed bool
	reason  string
}

// wsClose returns the WebSocket close status code and reason string for a PTY session exit.
func (c crashInfo) wsClose() (websocket.StatusCode, string) {
	if c.crashed {
		return websocket.StatusCode(wsCloseBackendExited), c.reason
	}
	return websocket.StatusNormalClosure, "session detached"
}

// ptyToWS relays PTY data to the WebSocket and detects backend crashes.
// If scrollback is non-nil, PTY output is also captured in the ring buffer.
func ptyToWS(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, session *TerminalSession, manager *TerminalManager, scrollback *ScrollbackBuffer) crashInfo {
	buf := make([]byte, terminalReadBufSize)
	for {
		select {
		case <-ctx.Done():
			return crashInfo{}
		default:
		}

		n, err := session.PTY.Read(buf)
		if err != nil {
			// PTY closed or error — check if the backend process has exited.
			// Use the raw internal tmux session name (session.Name) directly
			// since SessionAlive/PaneDead apply the prefix and we already have
			// the internal name.
			sessionGone := !manager.tmuxHasSession(session.Name)
			paneDead := false
			if !sessionGone {
				paneDead = manager.paneDead(session.Name)
			}

			cancel()

			if sessionGone || paneDead {
				reason := "backend process exited"
				captured := manager.capturePaneRaw(session.Name, 10)
				if captured != "" {
					reason = captured
				}
				// WebSocket close reasons are limited to 123 bytes.
				// Truncate safely at UTF-8 rune boundaries.
				reason = truncateUTF8(reason, 123)
				return crashInfo{crashed: true, reason: reason}
			}
			return crashInfo{}
		}

		if n > 0 {
			if err := conn.Write(ctx, websocket.MessageBinary, buf[:n]); err != nil {
				// WebSocket write failed - cancel context to unblock wsToPTY
				cancel()
				return crashInfo{}
			}
			if scrollback != nil {
				scrollback.Append(buf[:n])
			}
		}
	}
}

// truncateUTF8 truncates s to at most maxBytes bytes, keeping the last portion
// and ensuring the result is valid UTF-8 (doesn't split multi-byte characters).
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Take the tail
	s = s[len(s)-maxBytes:]
	// Skip any leading bytes that are continuation bytes (10xxxxxx)
	// to avoid splitting a multi-byte UTF-8 sequence.
	for len(s) > 0 && s[0]&0xC0 == 0x80 {
		s = s[1:]
	}
	return s
}

// wsToPTY reads from the WebSocket and writes to the PTY.
// Handles the in-band resize protocol.
func wsToPTY(ctx context.Context, conn *websocket.Conn, session *TerminalSession, manager *TerminalManager, connID string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgType, data, err := conn.Read(ctx)
		if err != nil {
			// WebSocket read failed - client disconnected
			return
		}

		// Binary messages may carry the in-band resize protocol.
		if msgType == websocket.MessageBinary {
			if len(data) == resizeMsgLen && data[0] == resizeMsgMarker {
				cols := binary.BigEndian.Uint16(data[1:3])
				rows := binary.BigEndian.Uint16(data[3:5])

				if cols > 0 && rows > 0 && cols <= maxTerminalCols && rows <= maxTerminalRows {
					if err := manager.Resize(connID, cols, rows); err != nil {
						log.Printf("Failed to resize terminal session %q: %v", connID, err)
					}
				}
				continue
			}
		}

		// Text and non-resize binary data - write to PTY
		if _, err := session.PTY.Write(data); err != nil {
			// PTY write failed
			return
		}
	}
}

// broadcastSessionIssueEvent sends an SSE event if the given session is linked to an issue.
// Uses a background context since the caller's request context may be invalid after WebSocket hijack.
func broadcastSessionIssueEvent(tabMetaStore *tabmeta.Store, hub *SSEHub, workspace, session string) {
	if tabMetaStore == nil || hub == nil {
		return
	}
	metaCtx, metaCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer metaCancel()
	meta, err := tabMetaStore.Get(metaCtx, workspace, session)
	if err != nil || meta == nil || meta.IssueID == "" {
		return
	}
	hub.Broadcast(&MutationPayload{
		Type:        "terminal_session_change",
		IssueID:     meta.IssueID,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		WorkspaceID: meta.Workspace,
	})
}

// injectTerminalContextBanner fetches project context from the loom server
// and writes a formatted banner to the terminal session's PTY.
func injectTerminalContextBanner(session *TerminalSession, loomServerURL string, workspaceConfigFn func() (*WorkspaceData, error)) {
	tc, err := FetchTerminalContext(loomServerURL)
	if err != nil {
		log.Printf("Terminal context fetch failed (skipping banner): %v", err)
		return
	}

	var wsName string
	if workspaceConfigFn != nil {
		if wsData, wsErr := workspaceConfigFn(); wsErr == nil && wsData != nil {
			wsName = wsData.Name
		} else if wsErr != nil {
			log.Printf("Warning: workspace config unavailable for terminal context: %v", wsErr)
		}
	}

	banner := FormatContextBanner(tc, wsName)
	if _, writeErr := session.PTY.Write([]byte(banner)); writeErr != nil {
		log.Printf("Warning: failed to write context banner to PTY: %v", writeErr)
	}
}
