package webui

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// Constants for terminal WebSocket communication.
const (
	terminalReadBufSize = 4096
	resizeMsgMarker     = 0x01
	resizeMsgLen        = 5
	maxTerminalCols     = 500
	maxTerminalRows     = 200
	wsReadLimit         = 32768 // 32KB; explicit limit for defense-in-depth (matches nhooyr.io/websocket default)
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

// handleTerminalToken returns a handler that generates a one-time terminal auth token
// for the given session. The caller must already be authenticated via the API key
// middleware.
func handleTerminalToken(auth *terminalAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		if session == "" || !validTerminalSession.MatchString(session) {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session"})
			return
		}

		token, err := auth.GenerateToken(session)
		if err != nil {
			log.Printf("Failed to generate terminal token: %v", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate token"})
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		respondJSON(w, http.StatusOK, map[string]string{"token": token})
	}
}

// handleTerminalRestart returns a handler that restarts the terminal session
// with the current backend from loom.yaml. It reads the backend from the project
// config, updates the TerminalManager's default command, kills the existing tmux
// session, and returns the new backend name.
func handleTerminalRestart(manager *TerminalManager, pool daemon.Pool, auth *terminalAuth) http.HandlerFunc {
	var configPool configConnectionGetter
	if pool != nil {
		configPool = &configPoolAdapter{pool: pool}
	}
	return handleTerminalRestartWithPool(manager, configPool, auth)
}

// handleTerminalRestartWithPool is the internal testable implementation.
func handleTerminalRestartWithPool(manager *TerminalManager, configPool configConnectionGetter, auth *terminalAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
			return
		}

		// Validate session parameter
		session := r.URL.Query().Get("session")
		if session == "" {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "missing session parameter"})
			return
		}
		if !validTerminalSession.MatchString(session) {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "invalid session name"})
			return
		}

		// Validate terminal token
		if auth != nil {
			token := r.URL.Query().Get("token")
			if err := auth.ValidateToken(token, session); err != nil {
				respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"success": false, "error": "terminal authentication failed"})
				return
			}
		}

		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"success": false, "error": "terminal manager not initialized"})
			return
		}
		// Shell tabs: kill and return without changing defaultCommand.
		if strings.HasPrefix(session, "lead-shell-") {
			_ = manager.KillSessionByName(session)
			respondJSON(w, http.StatusOK, map[string]interface{}{"success": true, "backend": "shell"})
			return
		}
		// Read current backend from loom.yaml via daemon
		backend := manager.DefaultCommand() // fallback to current
		if configPool != nil {
			wsPath, err := getWorkspacePath(configPool, r.Context())
			if err == nil {
				pf, err := loadProjectFile(wsPath)
				if err == nil {
					b := pf.Backend
					if b == "" {
						b = "claude"
					}
					if !isValidBackend(b) {
						respondJSON(w, http.StatusBadRequest, map[string]interface{}{
							"success": false,
							"error":   fmt.Sprintf("invalid backend %q; valid: %s", b, strings.Join(validBackends, ", ")),
						})
						return
					}
					backend = b
				}
			}
		}

		// Build the full terminal command with the lead agent prompt
		termCmd := fmt.Sprintf("loom lead --backend %s", backend)

		// Kill existing session first, then update command. This ordering ensures
		// racing Attach calls either get killed or use the new backend.
		_ = manager.KillSessionByName(session)
		manager.SetDefaultCommand(termCmd)

		respondJSON(w, http.StatusOK, map[string]interface{}{"success": true, "backend": backend})
	}
}

// handleTerminalKill returns a handler that forcibly kills a terminal session.
// This is used for hung backends — it kills the tmux session, which triggers the
// PTY close → crash detection flow in ptyToWS.
func handleTerminalKill(manager *TerminalManager, auth *terminalAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
			return
		}

		session := r.URL.Query().Get("session")
		if session == "" || !validTerminalSession.MatchString(session) {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "invalid session"})
			return
		}

		if auth != nil {
			token := r.URL.Query().Get("token")
			if err := auth.ValidateToken(token, session); err != nil {
				respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"success": false, "error": "terminal authentication failed"})
				return
			}
		}

		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"success": false, "error": "terminal manager not initialized"})
			return
		}

		_ = manager.KillSessionByName(session)
		respondJSON(w, http.StatusOK, map[string]interface{}{"success": true})
	}
}

// handleTerminalSessionStatus returns a handler that checks whether a tmux session is alive.
// This is a fallback for when the WebSocket close code is missed.
func handleTerminalSessionStatus(manager *TerminalManager, auth *terminalAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		if session == "" || !validTerminalSession.MatchString(session) {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid session"})
			return
		}

		if auth != nil {
			token := r.URL.Query().Get("token")
			if err := auth.ValidateToken(token, session); err != nil {
				respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "terminal authentication failed"})
				return
			}
		}

		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "terminal manager not initialized"})
			return
		}

		alive := manager.SessionAlive(session)
		result := map[string]interface{}{
			"alive": alive,
		}

		if !alive {
			// Try to capture last lines of output for error context
			if captured, err := manager.CapturePane(session, 10); err == nil && captured != "" {
				result["exit_reason"] = captured
			}
		} else if manager.PaneDead(session) {
			result["alive"] = false
			if captured, err := manager.CapturePane(session, 10); err == nil && captured != "" {
				result["exit_reason"] = captured
			}
		}

		respondJSON(w, http.StatusOK, result)
	}
}

