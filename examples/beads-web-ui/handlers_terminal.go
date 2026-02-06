package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"log"
	"net/http"
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

// handleTerminalWS returns a WebSocket handler for terminal relay.
// It upgrades HTTP connections to WebSocket, bridges them to tmux sessions
// via the TerminalManager, and handles bidirectional binary data relay
// plus an in-band resize protocol. If the command query parameter is not
// specified, defaultCmd is used.
func handleTerminalWS(manager *TerminalManager, defaultCmd string) http.HandlerFunc {
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

		// Get command parameter, falling back to default if not specified
		command := r.URL.Query().Get("command")
		if command == "" {
			command = defaultCmd
		}

		// Accept WebSocket upgrade
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true, // CORS handled at middleware level
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
		termSession, err := manager.Attach(session, command, 80, 24)
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
