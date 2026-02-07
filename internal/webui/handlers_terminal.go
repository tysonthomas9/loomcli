package webui

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"regexp"

	"nhooyr.io/websocket"
)

// Constants for terminal WebSocket communication.
const (
	terminalReadBufSize = 4096
	resizeMsgMarker     = 0x01
	resizeMsgLen        = 5
	maxTerminalCols     = 500
	maxTerminalRows     = 200
)

// validTerminalSession matches alphanumeric characters, hyphens, and underscores.
var validTerminalSession = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// originHosts extracts host (with port) from origin URLs for use as
// nhooyr.io/websocket OriginPatterns. For example, "http://localhost:3000"
// becomes "localhost:3000". Malformed URLs are logged and skipped.
func originHosts(origins []string) []string {
	if len(origins) == 0 {
		return nil
	}
	hosts := make([]string, 0, len(origins))
	for _, o := range origins {
		u, err := url.Parse(o)
		if err != nil || u.Host == "" {
			log.Printf("Warning: skipping malformed origin %q: %v", o, err)
			continue
		}
		hosts = append(hosts, u.Host)
	}
	return hosts
}

// handleTerminalWS returns a WebSocket handler for terminal relay.
// It upgrades HTTP connections to WebSocket, bridges them to tmux sessions
// via the TerminalManager, and handles bidirectional binary data relay
// plus an in-band resize protocol. The server-configured defaultCmd is
// always used as the terminal command.
//
// allowedOrigins is a list of full origin URLs (e.g. "http://localhost:3000")
// whose host portions are used as OriginPatterns for the WebSocket upgrade.
// When nil or empty, only same-origin and non-browser (no Origin header)
// connections are accepted.
// handleTerminalToken returns a handler that generates a one-time terminal auth token
// for the given session. The caller must already be authenticated via the API key
// middleware.
func handleTerminalToken(auth *terminalAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		if session == "" || !validTerminalSession.MatchString(session) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid session"})
			return
		}

		token, err := auth.GenerateToken(session)
		if err != nil {
			log.Printf("Failed to generate terminal token: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to generate token"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(map[string]string{"token": token})
	}
}

func handleTerminalWS(manager *TerminalManager, defaultCmd string, auth *terminalAuth, allowedOrigins []string) http.HandlerFunc {
	// Compute origin host patterns once at construction time.
	patterns := originHosts(allowedOrigins)

	return func(w http.ResponseWriter, r *http.Request) {
		// Check if manager is available
		if manager == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "terminal manager not initialized",
			}); err != nil {
				log.Printf("Failed to encode terminal error response: %v", err)
			}
			return
		}

		// Parse and validate session parameter
		session := r.URL.Query().Get("session")
		if session == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "missing session parameter",
			}); err != nil {
				log.Printf("Failed to encode terminal error response: %v", err)
			}
			return
		}

		if !validTerminalSession.MatchString(session) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "invalid session name: must match [a-zA-Z0-9_-]+",
			}); err != nil {
				log.Printf("Failed to encode terminal error response: %v", err)
			}
			return
		}

		// Validate one-time terminal token before WebSocket upgrade
		if auth != nil {
			token := r.URL.Query().Get("token")
			if err := auth.ValidateToken(token, session); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error":   "terminal authentication failed",
				}); encErr != nil {
					log.Printf("Failed to encode terminal auth error response: %v", encErr)
				}
				log.Printf("Terminal auth failed for session %q: %v", session, err)
				return
			}
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

		// Track close status for deferred cleanup
		closeStatus := websocket.StatusInternalError
		closeReason := "connection closed"
		defer func() {
			conn.Close(closeStatus, closeReason)
		}()

		// Attach to terminal session with default 80x24 size
		// (frontend sends resize immediately after connect)
		termSession, err := manager.Attach(session, defaultCmd, 80, 24)
		if err != nil {
			log.Printf("Failed to attach terminal session %q: %v", session, err)
			closeReason = err.Error()
			return
		}
		connID := termSession.ConnID

		// Create context for coordinating goroutines
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// Channel to signal when PTY reader finishes
		done := make(chan struct{})

		// Start PTY -> WebSocket goroutine
		go func() {
			defer close(done)
			ptyToWS(ctx, cancel, conn, termSession)
		}()

		// Run WebSocket -> PTY relay (blocks until WebSocket closes)
		wsToPTY(ctx, conn, termSession, manager, connID)

		// WebSocket closed - detach connection to close PTY and unblock ptyToWS
		// (Detach closes the PTY, causing the Read in ptyToWS to return an error)
		if err := manager.Detach(connID); err != nil {
			log.Printf("Failed to detach terminal connection %q: %v", connID, err)
		}

		// Now wait for PTY reader to finish (should be immediate after Detach)
		<-done

		// Set normal close status
		closeStatus = websocket.StatusNormalClosure
		closeReason = "session detached"
	}
}

// ptyToWS reads from the PTY and writes to the WebSocket.
func ptyToWS(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, session *TerminalSession) {
	buf := make([]byte, terminalReadBufSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := session.PTY.Read(buf)
		if err != nil {
			// PTY closed or error - cancel context to unblock wsToPTY
			cancel()
			return
		}

		if n > 0 {
			if err := conn.Write(ctx, websocket.MessageBinary, buf[:n]); err != nil {
				// WebSocket write failed - cancel context to unblock wsToPTY
				cancel()
				return
			}
		}
	}
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
