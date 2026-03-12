package cli

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/kv"
)

// WorkspaceInfo represents workspace metadata for API responses.
type WorkspaceInfo struct {
	Mode       string   `json:"mode"`                 // "workspace" or "legacy"
	Name       string   `json:"name,omitempty"`       // workspace name (workspace mode only)
	Workspaces []string `json:"workspaces,omitempty"` // all workspace names (workspace mode only)
}

// WorkspacesResponse lists all configured workspaces.
type WorkspacesResponse struct {
	Mode       string                     `json:"mode"`       // "workspace" or "legacy"
	Default    string                     `json:"default"`    // default workspace name
	Workspaces map[string]WorkspaceDetail `json:"workspaces"` // workspace details
	Timestamp  time.Time                  `json:"timestamp"`
}

// WorkspaceDetail contains details about a single workspace.
type WorkspaceDetail struct {
	Path  string   `json:"path"`  // workspace root path
	Repos []string `json:"repos"` // repo names in this workspace
}

// HealthResponse is the health check response.
type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// AgentsResponse wraps the agents list with optional workspace grouping.
type AgentsResponse struct {
	Workspace   WorkspaceInfo            `json:"workspace"`              // workspace mode info
	Agents      []AgentStatus            `json:"agents"`                 // flat list (existing)
	ByWorkspace map[string][]AgentStatus `json:"by_workspace,omitempty"` // grouped by workspace
	Timestamp   time.Time                `json:"timestamp"`
}

// TasksResponse wraps task information.
type TasksResponse struct {
	Summary          TaskSummary `json:"summary"`
	NeedsPlanning    []TaskInfo  `json:"needs_planning"`
	ReadyToImplement []TaskInfo  `json:"ready_to_implement"`
	NeedsReview      []TaskInfo  `json:"needs_review"`
	InProgress       []TaskInfo  `json:"in_progress"`
	Backlog          []TaskInfo  `json:"backlog"`
	Timestamp        time.Time   `json:"timestamp"`
}

// StatsResponse wraps statistics.
type StatsResponse struct {
	Stats     MonitorStats `json:"stats"`
	Timestamp time.Time    `json:"timestamp"`
}

// SyncResponse wraps sync status.
type SyncResponse struct {
	Sync      SyncInfo  `json:"sync"`
	Timestamp time.Time `json:"timestamp"`
}

// StatusResponse is the full status (like monitor dashboard).
type StatusResponse struct {
	Workspace      WorkspaceInfo       `json:"workspace"`
	Agents         []AgentStatus       `json:"agents"`
	Tasks          TaskSummary         `json:"tasks"`
	InProgressList []TaskInfo          `json:"in_progress_list"`
	AgentTasks     map[string]TaskInfo `json:"agent_tasks"`
	Stats          MonitorStats        `json:"stats"`
	Sync           SyncInfo            `json:"sync"`
	Timestamp      time.Time           `json:"timestamp"`
}

// corsMiddleware adds CORS headers to responses.
func corsMiddleware(corsOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := corsOrigin
		if origin == "" {
			origin = fmt.Sprintf("http://localhost:%d", serveWebUIPort)
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, HealthResponse{
		Status:    "ok",
		Timestamp: time.Now(),
	})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc()
	writeJSON(w, StatusResponse{
		Workspace:      getWorkspaceInfo(),
		Agents:         data.Agents,
		Tasks:          data.Tasks,
		InProgressList: data.InProgressTasks,
		AgentTasks:     data.AgentTasks,
		Stats:          data.Stats,
		Sync:           data.SyncStatus,
		Timestamp:      data.Timestamp,
	})
}

func handleAgents(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc()
	wsInfo := getWorkspaceInfo()

	response := AgentsResponse{
		Workspace: wsInfo,
		Agents:    data.Agents,
		Timestamp: data.Timestamp,
	}

	// Group agents by workspace if in workspace mode
	if wsInfo.Mode == "workspace" {
		response.ByWorkspace = groupAgentsByWorkspace(data.Agents)
	}

	writeJSON(w, response)
}

func handleTasks(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc()
	writeJSON(w, TasksResponse{
		Summary:          data.Tasks,
		NeedsPlanning:    data.NeedsPlanningTasks,
		ReadyToImplement: data.ReadyToImplement,
		NeedsReview:      data.ReviewTasks,
		InProgress:       data.InProgressTasks,
		Backlog:          data.BacklogTasks,
		Timestamp:        data.Timestamp,
	})
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc()
	writeJSON(w, StatsResponse{
		Stats:     data.Stats,
		Timestamp: data.Timestamp,
	})
}

func handleSync(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc()
	writeJSON(w, SyncResponse{
		Sync:      data.SyncStatus,
		Timestamp: data.Timestamp,
	})
}

func handleStaleDetector(w http.ResponseWriter, r *http.Request) {
	if staleDetectorInstance == nil {
		writeJSON(w, kv.StaleDetectorStatus{Enabled: false})
		return
	}
	writeJSON(w, staleDetectorInstance.Status())
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("Failed to encode JSON response: %v", err)
	}
}

// getWorkspaceInfo returns workspace metadata for API responses.
func getWorkspaceInfo() WorkspaceInfo {
	resolver, err := NewResolver()
	if err != nil || resolver.Mode() != ModeWorkspace {
		return WorkspaceInfo{Mode: "legacy"}
	}
	return WorkspaceInfo{
		Mode:       "workspace",
		Name:       resolver.WorkspaceName(),
		Workspaces: resolver.WorkspaceNames(),
	}
}

// groupAgentsByWorkspace groups agents by their workspace field.
func groupAgentsByWorkspace(agents []AgentStatus) map[string][]AgentStatus {
	groups := make(map[string][]AgentStatus)
	for _, agent := range agents {
		ws := agent.Workspace
		if ws == "" {
			ws = "(legacy)"
		}
		groups[ws] = append(groups[ws], agent)
	}
	return groups
}

func handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	response := WorkspacesResponse{
		Workspaces: make(map[string]WorkspaceDetail),
		Timestamp:  time.Now(),
	}

	resolver, err := NewResolver()
	if err != nil || resolver.Mode() != ModeWorkspace {
		response.Mode = "legacy"
		writeJSON(w, response)
		return
	}

	response.Mode = "workspace"
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		writeJSON(w, response)
		return
	}

	response.Default = cfg.DefaultWorkspace
	for name, ws := range cfg.Workspaces {
		repos := make([]string, len(ws.Repos))
		for i, repo := range ws.Repos {
			repos[i] = repo.Name
		}
		response.Workspaces[name] = WorkspaceDetail{
			Path:  ws.Path,
			Repos: repos,
		}
	}

	writeJSON(w, response)
}
