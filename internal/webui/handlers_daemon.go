package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// ErrAgentNotFound is returned by AgentQueueFn when the agent name
// is not found in the daemon config.
var ErrAgentNotFound = errors.New("agent not found in daemon config")

// DaemonSupervisorData is the response payload for GET /api/daemon/supervisor.
type DaemonSupervisorData struct {
	PID           int                `json:"pid"`
	StartedAt     time.Time          `json:"started_at"`
	UptimeSeconds float64            `json:"uptime_seconds"`
	Agents        []DaemonAgentEntry `json:"agents"`
}

// DaemonAgentEntry represents a single supervised agent in the supervisor response.
// Mirrors cli/daemon.DaemonAgentStatus fields without importing cli.
type DaemonAgentEntry struct {
	Worktree       string    `json:"worktree"`
	Role           string    `json:"role"`
	Repo           string    `json:"repo,omitempty"`
	PID            int       `json:"pid"`
	Status         string    `json:"status"`
	TaskID         string    `json:"task_id,omitempty"`
	EpicID         string    `json:"epic_id,omitempty"`
	CurrentBackend string    `json:"current_backend,omitempty"`
	RestartCount   int       `json:"restart_count"`
	LastStart      time.Time `json:"last_start,omitempty"`
	LastExit       time.Time `json:"last_exit,omitempty"`
	LastExitCode   int       `json:"last_exit_code,omitempty"`
	StopReason     string    `json:"stop_reason,omitempty"`
	StoppedAt      time.Time `json:"stopped_at,omitempty"`
	WorktreePath   string    `json:"worktree_path,omitempty"`
	LastErrorClass string    `json:"last_error_class,omitempty"`
	NoWorkCount    int       `json:"no_work_count,omitempty"`
	BackoffUntil   time.Time `json:"backoff_until,omitempty"`
	RemoteBranch   string    `json:"remote_branch,omitempty"`
}

// AgentQueueEntry represents a single scored issue in the agent queue response.
type AgentQueueEntry struct {
	IssueID  string   `json:"issue_id"`
	Title    string   `json:"title"`
	Priority int      `json:"priority"`
	Score    int      `json:"score"`
	Reason   string   `json:"reason"`
	Labels   []string `json:"labels"`
	Parent   string   `json:"parent,omitempty"`
}

// handleDaemonSupervisor returns a handler for GET /api/daemon/supervisor.
// Maps os.ErrNotExist to 503 (daemon not running), other errors to 500.
func handleDaemonSupervisor(fn func() (*DaemonSupervisorData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fn()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				handler.WriteJSON(w, http.StatusServiceUnavailable,
					dto.NewErrorResponse("daemon is not running", "daemon_not_running"))
				return
			}
			handler.WriteJSON(w, http.StatusInternalServerError,
				dto.NewErrorResponse("failed to read daemon state: "+err.Error(), "internal_error"))
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data":    data,
		})
	}
}

// configResponse wraps raw JSON config data in a success envelope.
// Uses json.RawMessage so the encoder embeds it verbatim without double-encoding.
type configResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

// handleDaemonConfig returns a handler for GET /api/daemon/config.
// Returns the effective resolved config as raw JSON. Maps errors to 503.
func handleDaemonConfig(fn func() (json.RawMessage, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fn()
		if err != nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable,
				dto.NewErrorResponse("failed to load daemon config: "+err.Error(), "config_error"))
			return
		}
		handler.WriteJSON(w, http.StatusOK, configResponse{Success: true, Data: data})
	}
}

// handleAgentQueue returns a handler for GET /api/workspaces/{ws}/agents/{name}/queue.
// Maps ErrAgentNotFound to 404, other errors to 503.
func handleAgentQueue(fn func(string) ([]AgentQueueEntry, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		entries, err := fn(name)
		if err != nil {
			if errors.Is(err, ErrAgentNotFound) {
				handler.WriteJSON(w, http.StatusNotFound,
					dto.NewErrorResponse("agent \""+name+"\" not found in daemon config", "agent_not_found"))
				return
			}
			handler.WriteJSON(w, http.StatusServiceUnavailable,
				dto.NewErrorResponse("daemon unavailable: "+err.Error(), "daemon_unavailable"))
			return
		}
		handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(entries, len(entries)))
	}
}
