package misc

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// WorkerInfo tracks a registered remote worker.
type WorkerInfo struct {
	ID        string    `json:"worker_id"`
	Workspace string    `json:"workspace"` // Stable workspace UUID (not name)
	Agent     string    `json:"agent"`
	Backend   string    `json:"backend"`
	StartedAt time.Time `json:"started_at"`
}

// WorkerRegistry holds registered workers in memory.
// For production use, this could be backed by Redis.
type WorkerRegistry struct {
	mu      sync.RWMutex
	workers map[string]*WorkerInfo
}

// NewWorkerRegistry creates an empty worker registry.
func NewWorkerRegistry() *WorkerRegistry {
	return &WorkerRegistry{
		workers: make(map[string]*WorkerInfo),
	}
}

func (r *WorkerRegistry) Register(w *WorkerInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workers[w.ID] = w
}

func (r *WorkerRegistry) Deregister(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.workers[id]; ok {
		delete(r.workers, id)
		return true
	}
	return false
}

func (r *WorkerRegistry) Get(id string) *WorkerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workers[id]
}

// workerAuthMiddleware validates the LOOM_WORKER_TOKEN shared secret.
func workerAuthMiddleware(workerToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if workerToken == "" {
			// No token configured — reject all worker API calls
			handler.RespondError(w, http.StatusServiceUnavailable, "worker API not configured (LOOM_WORKER_TOKEN not set)")
			return
		}

		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			handler.RespondError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(workerToken)) != 1 {
			handler.RespondError(w, http.StatusForbidden, "invalid worker token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// workerRegisterRequest is the JSON body for POST /api/internal/workers/register.
type workerRegisterRequest struct {
	Workspace string `json:"workspace"`
	Agent     string `json:"agent"`
	Backend   string `json:"backend"`
}

// workerRegisterResponse is the JSON response for worker registration.
type workerRegisterResponse struct {
	WorkerID string `json:"worker_id"`
	Token    string `json:"token,omitempty"`
}

// HandleWorkerRegister registers a new remote worker.
// If validateWorkspace is non-nil, the workspace UUID is validated at registration time.
func HandleWorkerRegister(registry *WorkerRegistry, validateWorkspace func(id string) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req workerRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			handler.RespondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		if req.Workspace == "" || req.Agent == "" {
			handler.RespondError(w, http.StatusBadRequest, "workspace and agent are required")
			return
		}

		if validateWorkspace != nil && !validateWorkspace(req.Workspace) {
			handler.RespondError(w, http.StatusBadRequest,
				fmt.Sprintf("unknown workspace ID: %s", req.Workspace))
			return
		}

		// Generate a unique worker ID
		idBytes := make([]byte, 16)
		if _, err := rand.Read(idBytes); err != nil {
			handler.RespondError(w, http.StatusInternalServerError, "failed to generate worker ID")
			return
		}
		workerID := hex.EncodeToString(idBytes)

		info := &WorkerInfo{
			ID:        workerID,
			Workspace: req.Workspace,
			Agent:     req.Agent,
			Backend:   req.Backend,
			StartedAt: time.Now(),
		}
		registry.Register(info)

		slog.Info("worker registered", "worker_id", workerID, "workspace", req.Workspace, "agent", req.Agent)

		handler.WriteJSON(w, http.StatusCreated, workerRegisterResponse{
			WorkerID: workerID,
		})
	}
}

// handleWorkerDeregister removes a registered worker.
func handleWorkerDeregister(registry *WorkerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workerID := r.PathValue("id")
		if workerID == "" {
			handler.RespondError(w, http.StatusBadRequest, "worker ID is required")
			return
		}

		if registry.Deregister(workerID) {
			slog.Info("worker deregistered", "worker_id", workerID)
			w.WriteHeader(http.StatusNoContent)
		} else {
			handler.RespondError(w, http.StatusNotFound, "worker not found")
		}
	}
}

// workerStateRequest is the JSON body for POST /api/internal/workers/{id}/state.
type workerStateRequest struct {
	Action    string `json:"action"`     // "update_state", "update_task", "clear_task", "read"
	State     string `json:"state"`      // for update_state
	AgentName string `json:"agent_name"` // agent name
	TaskID    string `json:"task_id"`    // for update_task
	TaskTitle string `json:"task_title"` // for update_task
}

// handleWorkerState handles lock state operations from remote workers.
// It writes to the agent's lock file so existing LogStreamer/status code continues working.
func handleWorkerState(registry *WorkerRegistry, resolveWorktreePath func(workspace, agent string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workerID := r.PathValue("id")
		worker := registry.Get(workerID)
		if worker == nil {
			handler.RespondError(w, http.StatusNotFound, "worker not found")
			return
		}

		var req workerStateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			handler.RespondError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		worktreePath := resolveWorktreePath(worker.Workspace, worker.Agent)
		if worktreePath == "" {
			handler.RespondError(w, http.StatusInternalServerError, "cannot resolve worktree path")
			return
		}

		dispatchWorkerAction(w, workerID, worktreePath, &req)
	}
}

// dispatchWorkerAction executes the requested worker state action.
func dispatchWorkerAction(w http.ResponseWriter, workerID, worktreePath string, req *workerStateRequest) {
	switch req.Action {
	case "update_state":
		if err := updateWorkerLockState(worktreePath, req.State); err != nil {
			slog.Warn("worker state update failed", "worker_id", workerID, "err", err)
			handler.RespondError(w, http.StatusInternalServerError, "failed to update state")
			return
		}
		w.WriteHeader(http.StatusOK)
	case "update_task":
		if err := updateWorkerLockTask(worktreePath, req.TaskID, req.TaskTitle); err != nil {
			slog.Warn("worker task update failed", "worker_id", workerID, "err", err)
			handler.RespondError(w, http.StatusInternalServerError, "failed to update task")
			return
		}
		w.WriteHeader(http.StatusOK)
	case "clear_task":
		if err := clearWorkerLockTask(worktreePath); err != nil {
			slog.Warn("worker clear task failed", "worker_id", workerID, "err", err)
			handler.RespondError(w, http.StatusInternalServerError, "failed to clear task")
			return
		}
		w.WriteHeader(http.StatusOK)
	case "read":
		info, err := readWorkerLock(worktreePath)
		if err != nil {
			handler.RespondError(w, http.StatusNotFound, "lock file not found")
			return
		}
		handler.WriteJSON(w, http.StatusOK, info)
	default:
		handler.RespondError(w, http.StatusBadRequest, fmt.Sprintf("unknown action %q", req.Action))
	}
}

// handleWorkerEvents receives domain events from remote workers and writes to JSONL.
func handleWorkerEvents(registry *WorkerRegistry, resolveEventsDir func(workspace string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workerID := r.PathValue("id")
		worker := registry.Get(workerID)
		if worker == nil {
			handler.RespondError(w, http.StatusNotFound, "worker not found")
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
		if err != nil {
			handler.RespondError(w, http.StatusBadRequest, "failed to read body")
			return
		}

		eventsDir := resolveEventsDir(worker.Workspace)
		if eventsDir == "" {
			handler.RespondError(w, http.StatusInternalServerError, "cannot resolve events directory")
			return
		}

		// Append the event to the JSONL file
		if err := appendToEventsFile(eventsDir, body); err != nil {
			slog.Warn("failed to write worker event", "worker_id", workerID, "err", err)
			handler.RespondError(w, http.StatusInternalServerError, "failed to write event")
			return
		}

		w.WriteHeader(http.StatusAccepted)
	}
}

// handleWorkerLogs receives log chunks from remote workers and appends to the agent's log file.
func handleWorkerLogs(registry *WorkerRegistry, resolveLogPath func(workspace, agent string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workerID := r.PathValue("id")
		worker := registry.Get(workerID)
		if worker == nil {
			handler.RespondError(w, http.StatusNotFound, "worker not found")
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
		if err != nil {
			handler.RespondError(w, http.StatusBadRequest, "failed to read body")
			return
		}

		logPath := resolveLogPath(worker.Workspace, worker.Agent)
		if logPath == "" {
			handler.RespondError(w, http.StatusInternalServerError, "cannot resolve log path")
			return
		}

		// Append to the agent's log file so existing log tailing works
		if err := appendToLogFile(logPath, body); err != nil {
			slog.Warn("failed to write worker log", "worker_id", workerID, "err", err)
			handler.RespondError(w, http.StatusInternalServerError, "failed to write log")
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

// workerLockPath returns the sanitized lock file path and validates it is
// under the expected worktree directory.
func workerLockPath(worktreePath string) (string, error) {
	lockPath := filepath.Join(worktreePath, ".agent.lock")
	// Ensure the resolved path is actually under the worktree (prevent traversal)
	absLock, err := filepath.Abs(lockPath)
	if err != nil {
		return "", fmt.Errorf("cannot resolve lock path: %w", err)
	}
	absBase, err := filepath.Abs(worktreePath)
	if err != nil {
		return "", fmt.Errorf("cannot resolve worktree path: %w", err)
	}
	if !strings.HasPrefix(absLock, absBase+string(filepath.Separator)) {
		return "", fmt.Errorf("lock path escapes worktree: %s", absLock)
	}
	return absLock, nil
}

// readWorkerLockFile reads and parses the lock file as a JSON map.
func readWorkerLockFile(worktreePath string) (string, map[string]interface{}, error) {
	lockPath, err := workerLockPath(worktreePath)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(filepath.Clean(lockPath))
	if err != nil {
		return "", nil, fmt.Errorf("no active lock to update: %w", err)
	}
	var info map[string]interface{}
	if err := json.Unmarshal(data, &info); err != nil {
		return "", nil, fmt.Errorf("invalid lock file: %w", err)
	}
	return lockPath, info, nil
}

// writeWorkerLockFile writes the JSON map back to the lock file.
func writeWorkerLockFile(lockPath string, info map[string]interface{}) error {
	updated, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(lockPath, updated, 0600)
}

// updateWorkerLockState writes the state to the lock file without PID validation.
func updateWorkerLockState(worktreePath, state string) error {
	lockPath, info, err := readWorkerLockFile(worktreePath)
	if err != nil {
		return err
	}
	info["state"] = state
	return writeWorkerLockFile(lockPath, info)
}

// updateWorkerLockTask writes task info to the lock file without PID validation.
func updateWorkerLockTask(worktreePath, taskID, taskTitle string) error {
	lockPath, info, err := readWorkerLockFile(worktreePath)
	if err != nil {
		return err
	}
	info["task_id"] = taskID
	info["task_title"] = taskTitle
	info["task_started_at"] = time.Now().Format(time.RFC3339Nano)
	return writeWorkerLockFile(lockPath, info)
}

// clearWorkerLockTask clears task fields in the lock file without PID validation.
func clearWorkerLockTask(worktreePath string) error {
	lockPath, info, err := readWorkerLockFile(worktreePath)
	if err != nil {
		return err
	}
	info["task_id"] = ""
	info["task_title"] = ""
	delete(info, "task_started_at")
	return writeWorkerLockFile(lockPath, info)
}

// readWorkerLock reads and returns the lock file contents.
func readWorkerLock(worktreePath string) (map[string]interface{}, error) {
	_, info, err := readWorkerLockFile(worktreePath)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// appendToEventsFile appends event data to the events JSONL file.
func appendToEventsFile(eventsDir string, data []byte) error {
	if err := os.MkdirAll(eventsDir, 0700); err != nil {
		return err
	}

	eventFile := filepath.Clean(filepath.Join(eventsDir, "events.jsonl"))
	f, err := os.OpenFile(eventFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	// Ensure newline termination
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	_, err = f.Write(data)
	return err
}

// appendToLogFile appends log data to the agent's log file.
func appendToLogFile(logPath string, data []byte) error {
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	f, err := os.OpenFile(filepath.Clean(logPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(data)
	return err
}

// SetupWorkerAPIRoutes registers the internal worker API endpoints on the given mux.
// These endpoints are called by remote workers, not by the browser.
// workerToken is the shared secret from LOOM_WORKER_TOKEN.
// resolveWorktreePath maps (workspace UUID, agent) to a filesystem path.
// resolveEventsDir maps workspace UUID to its events directory.
// resolveLogPath maps (workspace UUID, agent) to the agent's log file path.
// validateWorkspace checks whether a workspace UUID is known; nil skips validation.
func SetupWorkerAPIRoutes(
	mux *http.ServeMux,
	workerToken string,
	resolveWorktreePath func(workspace, agent string) string,
	resolveEventsDir func(workspace string) string,
	resolveLogPath func(workspace, agent string) string,
	validateWorkspace func(id string) bool,
) *WorkerRegistry {
	registry := NewWorkerRegistry()

	// Create an inner mux for worker API routes
	workerMux := http.NewServeMux()
	workerMux.HandleFunc("POST /api/internal/workers/register", HandleWorkerRegister(registry, validateWorkspace))
	workerMux.HandleFunc("DELETE /api/internal/workers/{id}", handleWorkerDeregister(registry))
	workerMux.HandleFunc("POST /api/internal/workers/{id}/state", handleWorkerState(registry, resolveWorktreePath))
	workerMux.HandleFunc("POST /api/internal/workers/{id}/events", handleWorkerEvents(registry, resolveEventsDir))
	workerMux.HandleFunc("POST /api/internal/workers/{id}/logs", handleWorkerLogs(registry, resolveLogPath))

	// Wrap with auth middleware and mount on the main mux. The JSON fallback
	// goes INSIDE the auth middleware: an unauthenticated request to an unknown
	// worker path must still get 401/403, so the route surface stays
	// unenumerable without the token.
	authed := workerAuthMiddleware(workerToken, handler.JSONFallbackMux(workerMux))
	mux.Handle("/api/internal/workers/", authed)
	mux.Handle("POST /api/internal/workers/register", authed)

	return registry
}
