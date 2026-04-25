package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// ErrAgentNotFound is returned by AgentQueueFn when the agent name
// is not found in the daemon config.
var ErrAgentNotFound = errors.New("agent not found in daemon config")

// ErrAgentAmbiguous is returned by AgentQueueFn when the bare agent name
// matches more than one agent in the daemon config (e.g., two agents with
// the same worktree name in different repos). Clients should retry with
// ?repo= to disambiguate. Mapped by HandleAgentQueue to HTTP 409.
var ErrAgentAmbiguous = errors.New("agent name ambiguous across repos; specify repo to disambiguate")

// DaemonSupervisorData is the response payload for GET /api/daemon/supervisor.
type DaemonSupervisorData struct {
	PID           int                `json:"pid"`
	StartedAt     time.Time          `json:"started_at"`
	UptimeSeconds float64            `json:"uptime_seconds"`
	Agents        []DaemonAgentEntry `json:"agents"`
}

// WorkspaceDaemonPaths is the set of filesystem paths for a workspace's loom daemon.
// SocketPath and StatePath are resolved via the workspace's daemon config (honoring
// PID file overrides). ConfigPath and WorkDir are derived directly from the
// workspace's projectDir; ConfigPath is the default workspace config location.
type WorkspaceDaemonPaths struct {
	SocketPath string // daemon control socket (daemon.sock)
	StatePath  string // daemon agent state file (daemon-agents.json)
	ConfigPath string // workspace loom.yaml (default location)
	WorkDir    string // workspace root directory
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

// AgentStatusCollectInput is the per-agent input to AgentStatusCollectFn.
type AgentStatusCollectInput struct {
	WorktreePath  string // absolute path; required
	AgentName     string // bare worktree name (for lock lookup + diagnostics)
	Repo          string // workspace repo name (for per-repo default branch)
	DefaultBranch string // integration branch for ahead/behind comparison
}

// AgentGitStatus carries enriched git + lock data returned by the collector.
// Always non-nil from the callback; Err != nil signals a collection failure
// that the handler maps to the per-entry "error" field.
type AgentGitStatus struct {
	Status  string
	Branch  string
	Ahead   int
	Behind  int
	Changes int
	TaskID  string
	Err     error
}

// AgentStatusCollectFn enriches a single agent worktree with git + lock data.
// Implementations capture the monitor package's BuildSingleAgentStatusCollector
// so per-package change-detector and commit caches are shared with the
// background monitor collector.
type AgentStatusCollectFn func(in AgentStatusCollectInput) *AgentGitStatus

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

// HandleDaemonSupervisor returns a handler for GET /api/daemon/supervisor.
// Maps os.ErrNotExist to 503 (daemon not running), other errors to 500.
func HandleDaemonSupervisor(fn func() (*DaemonSupervisorData, error)) http.HandlerFunc {
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

// HandleDaemonConfig returns a handler for GET /api/daemon/config.
// Returns the effective resolved config as raw JSON. Maps errors to 503.
func HandleDaemonConfig(fn func() (json.RawMessage, error)) http.HandlerFunc {
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

// HandleWsDaemonSupervisor is the workspace-scoped counterpart to
// HandleDaemonSupervisor. It extracts the workspace ID from the request
// context (set by middleware.Workspace) and delegates to the injected closure,
// which resolves per-workspace daemon paths internally (typically via
// WorkspaceDaemonResolver). Maps os.ErrNotExist to 503 (daemon not running),
// other errors to 503 daemon_unavailable.
func HandleWsDaemonSupervisor(fn func(wsID string) (*DaemonSupervisorData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		data, err := fn(wsID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				handler.WriteJSON(w, http.StatusServiceUnavailable,
					dto.NewErrorResponse("daemon is not running for this workspace", "daemon_not_running"))
				return
			}
			handler.WriteJSON(w, http.StatusServiceUnavailable,
				dto.NewErrorResponse("failed to read daemon state: "+err.Error(), "daemon_unavailable"))
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
	}
}

// HandleWsDaemonConfig is the workspace-scoped counterpart to
// HandleDaemonConfig. The injected closure takes a wsID and returns the
// daemon config as raw JSON; path resolution happens inside the closure via
// WorkspaceDaemonResolver.
func HandleWsDaemonConfig(fn func(wsID string) (json.RawMessage, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		data, err := fn(wsID)
		if err != nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable,
				dto.NewErrorResponse("failed to load daemon config: "+err.Error(), "config_error"))
			return
		}
		handler.WriteJSON(w, http.StatusOK, configResponse{Success: true, Data: data})
	}
}

// HandleAgentQueue returns a handler for GET /api/workspaces/{ws}/agents/{name}/queue.
// The optional ?repo= query parameter disambiguates duplicate worktree names
// across repos within a workspace; when present the handler forwards the
// compound key "repo/name" to fn. Maps ErrAgentNotFound to 404,
// ErrAgentAmbiguous to 409, other errors to 503.
func HandleAgentQueue(fn func(wsID, agentName string) ([]AgentQueueEntry, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		name := r.PathValue("name")
		repo := r.URL.Query().Get("repo")
		key := name
		if repo != "" {
			key = repo + "/" + name
		}
		entries, err := fn(wsID, key)
		if err != nil {
			if errors.Is(err, ErrAgentNotFound) {
				handler.WriteJSON(w, http.StatusNotFound,
					dto.NewErrorResponse("agent \""+key+"\" not found in daemon config", "agent_not_found"))
				return
			}
			if errors.Is(err, ErrAgentAmbiguous) {
				handler.WriteJSON(w, http.StatusConflict,
					dto.NewErrorResponse("agent \""+name+"\" is ambiguous across repos; use ?repo= to disambiguate", "agent_ambiguous"))
				return
			}
			handler.WriteJSON(w, http.StatusServiceUnavailable,
				dto.NewErrorResponse("daemon unavailable: "+err.Error(), "daemon_unavailable"))
			return
		}
		handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(entries, len(entries)))
	}
}
