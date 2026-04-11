package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// leadSessionRequest is the JSON body for POST /api/workspaces/{ws}/terminal/lead-session.
// The workspace is read from the URL path via WorkspaceMiddleware, not the body.
type leadSessionRequest struct {
	Message string `json:"message"`
	Backend string `json:"backend"`
}

// leadSessionData is the data payload in a successful lead-session response.
// The tab label is derived from session_name on the client, so we do not
// return a redundant label field.
type leadSessionData struct {
	SessionName string `json:"session_name"`
	Backend     string `json:"backend"`
}

// leadSessionResponse is the JSON response for POST /api/workspaces/{ws}/terminal/lead-session.
type leadSessionResponse struct {
	Success bool             `json:"success"`
	Data    *leadSessionData `json:"data,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// leadMessageMaxLen caps the user message to avoid comically large argv payloads.
// tmux + the backend agent will handle long prompts, but we draw a reasonable line
// here to protect against accidental pastes or abuse.
const leadMessageMaxLen = 16 * 1024

// argvSpawner is the narrow interface used by handleCreateLeadSession. Keeping
// it separate from *TerminalManager lets tests inject a fake without a real
// tmux binary — matching the `terminalSpawner` pattern in
// handlers_terminal_spawn.go. wsID scopes the session to a workspace so two
// workspaces with the same generated session name don't collide in
// TerminalManager's internal state.
type argvSpawner interface {
	SpawnArgv(wsID, name string, argv []string, cols, rows uint16, workDir string) (bool, error)
}

// handleCreateLeadSession creates a detached tmux session running
// `loom lead --backend <backend> --message <message>`. Because the message is
// baked into the loom-lead invocation as a CLI argument, the backend agent
// receives the user's request as part of its initial prompt — no send-keys, no
// readiness polling, no TUI scraping. Works for any backend loom supports
// (claude, codex, cursor, opencode, gemini) since they all accept a positional
// prompt argument.
//
// Registered on wsMux at POST /api/workspaces/{ws}/terminal/lead-session.
// WorkspaceMiddleware validates the workspace and injects its ID into the
// request context. workspaceConfigByIDFn resolves that ID to an on-disk path
// so the tmux session starts with its cwd at the active workspace instead of
// the loom service's own cwd.
func handleCreateLeadSession(manager *TerminalManager, workspaceConfigByIDFn func(string) (*WorkspaceData, error)) http.HandlerFunc {
	if manager == nil {
		return func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, http.StatusServiceUnavailable, leadSessionResponse{
				Error: "terminal manager not initialized",
			})
		}
	}
	return handleCreateLeadSessionImpl(manager, workspaceConfigByIDFn)
}

// handleCreateLeadSessionImpl is the testable implementation that accepts an
// interface. Tests use this directly with a mock spawner.
func handleCreateLeadSessionImpl(spawner argvSpawner, workspaceConfigByIDFn func(string) (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req leadSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, leadSessionResponse{
					Error: "request body too large",
				})
				return
			}
			respondJSON(w, http.StatusBadRequest, leadSessionResponse{
				Error: "invalid request body",
			})
			return
		}

		message := strings.TrimSpace(req.Message)
		if message == "" {
			respondJSON(w, http.StatusBadRequest, leadSessionResponse{
				Error: "message is required",
			})
			return
		}
		if len(message) > leadMessageMaxLen {
			respondJSON(w, http.StatusBadRequest, leadSessionResponse{
				Error: fmt.Sprintf("message too long (max %d bytes)", leadMessageMaxLen),
			})
			return
		}

		backend := strings.TrimSpace(req.Backend)
		if backend == "" {
			respondJSON(w, http.StatusBadRequest, leadSessionResponse{
				Error: "backend is required",
			})
			return
		}
		if !isValidBackend(backend) {
			respondJSON(w, http.StatusBadRequest, leadSessionResponse{
				Error: fmt.Sprintf("invalid backend %q; valid: %s", backend, strings.Join(validBackends, ", ")),
			})
			return
		}

		// Session name: "lead-<backend>-<unix_ms>". Timestamp-based so concurrent
		// submissions get unique names without needing to inspect existing sessions.
		sessionName := fmt.Sprintf("lead-%s-%d", backend, time.Now().UnixMilli())
		if !validSessionName.MatchString(sessionName) {
			// Defensive: backend name and digits should always satisfy the regex,
			// but fail loudly if the assumption breaks.
			respondJSON(w, http.StatusInternalServerError, leadSessionResponse{
				Error: "generated session name failed validation",
			})
			return
		}

		// Build argv for tmux. Passing argv as separate elements avoids shell
		// interpretation of the user's message — no quoting bugs, no injection.
		argv := []string{"loom", "lead", "--backend", backend, "--message", message}

		// Resolve the workspace ID from the request context. Required
		// because TerminalManager's public API is workspace-scoped;
		// missing context means the handler was invoked without
		// WorkspaceMiddleware, which is an error in production.
		wsID := WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, leadSessionResponse{
				Error: "workspace context required",
			})
			return
		}
		// Resolve the workspace path to an on-disk cwd for the tmux
		// session. Falls back to "" (inherit the loom service's cwd)
		// when the lookup fails.
		workDir := resolveWorkDirFromContext(r.Context(), workspaceConfigByIDFn)

		created, err := spawner.SpawnArgv(wsID, sessionName, argv, 120, 40, workDir)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, leadSessionResponse{
				Error: fmt.Sprintf("failed to spawn lead session: %v", err),
			})
			return
		}
		if !created {
			respondJSON(w, http.StatusConflict, leadSessionResponse{
				Error: "session already exists",
			})
			return
		}

		respondJSON(w, http.StatusOK, leadSessionResponse{
			Success: true,
			Data: &leadSessionData{
				SessionName: sessionName,
				Backend:     backend,
			},
		})
	}
}
