package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
)

// terminalSpawnRequest is the JSON body for POST /api/terminal/spawn.
type terminalSpawnRequest struct {
	SessionName string `json:"session_name"`
	Backend     string `json:"backend"`
}

// terminalSpawnData is the data payload in a successful spawn response.
type terminalSpawnData struct {
	SessionName string `json:"session_name"`
	Backend     string `json:"backend"`
	Command     string `json:"command"`
	Created     bool   `json:"created"`
}

// terminalSpawnResponse is the JSON response for POST /api/workspaces/{ws}/terminal/spawn.
type terminalSpawnResponse struct {
	Success bool               `json:"success"`
	Data    *terminalSpawnData `json:"data,omitempty"`
	Error   string             `json:"error,omitempty"`
}

// terminalSpawner is an interface for the subset of TerminalManager used by
// the spawn handler. SpawnInDir starts the tmux session in workDir (via tmux
// -c) so the "+ Tab" flow lands in the active workspace's path rather than
// the loom service's cwd. The wsID parameter scopes the session to a
// workspace so two workspaces with the same user-facing session name don't
// collide in TerminalManager's internal state.
type terminalSpawner interface {
	SpawnInDir(wsID, name, command string, cols, rows uint16, workDir string) (bool, error)
}

// issueSessionPattern matches issue-linked session names: "issue-{project}-{number}".
var issueSessionPattern = regexp.MustCompile(`^issue-(.+)-(\d+)$`)

// extractIssueID converts a sanitized session name back to an issue ID.
// e.g., "issue-loomcli-fghge-1" → "loomcli-fghge.1"
func extractIssueID(sessionName string) string {
	m := issueSessionPattern.FindStringSubmatch(sessionName)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2]
}

// handleTerminalSpawn returns a handler that creates a tmux session for a
// given issue and backend. Registered on wsMux at
// POST /api/workspaces/{ws}/terminal/spawn — the workspace ID is read from
// the request context, resolved to an on-disk path via workspaceConfigByIDFn,
// and passed to SpawnInDir so the tmux session starts in that directory.
func handleTerminalSpawn(manager *TerminalManager, sessionHistoryStore *sessionhistory.Store, workspaceConfigByIDFn func(string) (*WorkspaceData, error)) http.HandlerFunc {
	if manager == nil {
		return func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, http.StatusServiceUnavailable, terminalSpawnResponse{
				Error: "terminal manager not initialized",
			})
		}
	}
	return handleTerminalSpawnImplWithHistory(manager, sessionHistoryStore, workspaceConfigByIDFn)
}

// handleTerminalSpawnImplWithHistory is the implementation that accepts an
// interface and optional session history store plus a workspace config
// resolver for cwd lookup.
func handleTerminalSpawnImplWithHistory(manager terminalSpawner, sessionHistoryStore *sessionhistory.Store, workspaceConfigByIDFn func(string) (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req terminalSpawnRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, terminalSpawnResponse{
					Error: "request body too large",
				})
				return
			}
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: "invalid request body",
			})
			return
		}

		if req.SessionName == "" {
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: "missing required field: session_name",
			})
			return
		}
		if req.Backend == "" {
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: "missing required field: backend",
			})
			return
		}

		// Sanitize dots to dashes (issue IDs like loomcli-fghge.1 contain dots)
		sanitizedName := strings.ReplaceAll(req.SessionName, ".", "-")

		if !validSessionName.MatchString(sanitizedName) {
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: fmt.Sprintf("invalid session name %q after sanitization: must match [a-zA-Z0-9_-]+", sanitizedName),
			})
			return
		}

		var command string
		if req.Backend == "shell" {
			command = shellCommand()
		} else if !isValidBackend(req.Backend) {
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: fmt.Sprintf("invalid backend %q; valid: %s", req.Backend, strings.Join(validBackends, ", ")),
			})
			return
		} else {
			// The command is the backend name itself (e.g., "claude")
			command = req.Backend
		}

		// Resolve workspace cwd from the request context (injected by
		// WorkspaceMiddleware). The workspace ID is required because
		// TerminalManager's public API is workspace-scoped; missing
		// context indicates a direct test invocation or a misconfigured
		// route — respond 400 rather than silently routing to a shared
		// bucket.
		wsID := WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, terminalSpawnResponse{
				Error: "workspace context required",
			})
			return
		}
		workDir := resolveWorkDirFromContext(r.Context(), workspaceConfigByIDFn)

		created, err := manager.SpawnInDir(wsID, sanitizedName, command, 120, 40, workDir)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, terminalSpawnResponse{
				Error: fmt.Sprintf("failed to spawn terminal session: %v", err),
			})
			return
		}

		// Record session creation in session history (only for issue-linked
		// sessions). Skip when the workspace context is missing — the store
		// would otherwise write to a malformed key. Production requests
		// always go through WorkspaceMiddleware so wsID is set; the guard
		// only matters for direct-handler test invocations.
		if created && sessionHistoryStore != nil && wsID != "" {
			if issueID := extractIssueID(sanitizedName); issueID != "" {
				now := time.Now().UTC()
				record := sessionhistory.SessionRecord{
					ID:          fmt.Sprintf("%s:%d", sanitizedName, now.Unix()),
					SessionName: sanitizedName,
					IssueID:     issueID,
					Backend:     req.Backend,
					Status:      "active",
					Launcher:    "user",
					StartedAt:   now,
				}
				// Workspace comes from the request context thanks to
				// WorkspaceMiddleware — resolves the old TODO(T41) that said
				// "derive workspace from request context when terminal spawn
				// moves to wsMux".
				if err := sessionHistoryStore.Add(r.Context(), wsID, record); err != nil {
					log.Printf("Warning: failed to record session history for %s: %v", sanitizedName, err)
				}
			}
		}

		respondJSON(w, http.StatusOK, terminalSpawnResponse{
			Success: true,
			Data: &terminalSpawnData{
				SessionName: sanitizedName,
				Backend:     req.Backend,
				Command:     command,
				Created:     created,
			},
		})
	}
}

// handleTerminalSpawnImpl is the internal testable implementation that accepts
// an interface (no session history, no workspace resolver).
func handleTerminalSpawnImpl(manager terminalSpawner) http.HandlerFunc {
	return handleTerminalSpawnImplWithHistory(manager, nil, nil)
}
