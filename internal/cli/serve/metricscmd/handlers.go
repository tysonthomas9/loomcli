package metricscmd

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
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

// AgentsResponse wraps the agents list with optional workspace grouping.
type AgentsResponse struct {
	Workspace   WorkspaceInfo                    `json:"workspace"`              // workspace mode info
	Agents      []monitor.AgentStatus            `json:"agents"`                 // flat list (existing)
	ByWorkspace map[string][]monitor.AgentStatus `json:"by_workspace,omitempty"` // grouped by workspace
	Timestamp   time.Time                        `json:"timestamp"`
}

// TasksResponse wraps task information.
type TasksResponse struct {
	Summary          monitor.TaskSummary `json:"summary"`
	NeedsPlanning    []monitor.TaskInfo  `json:"needs_planning"`
	ReadyToImplement []monitor.TaskInfo  `json:"ready_to_implement"`
	NeedsReview      []monitor.TaskInfo  `json:"needs_review"`
	InProgress       []monitor.TaskInfo  `json:"in_progress"`
	Backlog          []monitor.TaskInfo  `json:"backlog"`
	Closed           []monitor.TaskInfo  `json:"closed"`
	Timestamp        time.Time           `json:"timestamp"`
}

// StatsResponse wraps statistics.
type StatsResponse struct {
	Stats     monitor.MonitorStats `json:"stats"`
	Timestamp time.Time            `json:"timestamp"`
}

// SyncResponse wraps sync status.
type SyncResponse struct {
	Sync      monitor.SyncInfo `json:"sync"`
	Timestamp time.Time        `json:"timestamp"`
}

// StatusResponse is the full status (like monitor dashboard).
type StatusResponse struct {
	Workspace      WorkspaceInfo               `json:"workspace"`
	Agents         []monitor.AgentStatus       `json:"agents"`
	Tasks          monitor.TaskSummary         `json:"tasks"`
	InProgressList []monitor.TaskInfo          `json:"in_progress_list"`
	AgentTasks     map[string]monitor.TaskInfo `json:"agent_tasks"`
	Stats          monitor.MonitorStats        `json:"stats"`
	Sync           monitor.SyncInfo            `json:"sync"`
	Timestamp      time.Time                   `json:"timestamp"`
}

// HandleStatus returns an HTTP handler for the full status endpoint.
func HandleStatus(collectDataFn func() *monitor.MonitorData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := collectDataFn()
		if data == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"data collection unavailable"}`))
			return
		}
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
}

// HandleAgents returns an HTTP handler for the agents endpoint.
func HandleAgents(collectDataFn func() *monitor.MonitorData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := collectDataFn()
		if data == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"data collection unavailable"}`))
			return
		}
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
}

// HandleTasks returns an HTTP handler for the tasks endpoint.
func HandleTasks(collectDataFn func() *monitor.MonitorData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := collectDataFn()
		if data == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"data collection unavailable"}`))
			return
		}
		writeJSON(w, TasksResponse{
			Summary:          data.Tasks,
			NeedsPlanning:    data.NeedsPlanningTasks,
			ReadyToImplement: data.ReadyToImplement,
			NeedsReview:      data.ReviewTasks,
			InProgress:       data.InProgressTasks,
			Backlog:          data.BacklogTasks,
			Closed:           data.ClosedTasks,
			Timestamp:        data.Timestamp,
		})
	}
}

// HandleStats returns an HTTP handler for the stats endpoint.
func HandleStats(collectDataFn func() *monitor.MonitorData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := collectDataFn()
		if data == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"data collection unavailable"}`))
			return
		}
		writeJSON(w, StatsResponse{
			Stats:     data.Stats,
			Timestamp: data.Timestamp,
		})
	}
}

// HandleSync returns an HTTP handler for the sync endpoint.
func HandleSync(collectDataFn func() *monitor.MonitorData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := collectDataFn()
		if data == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"data collection unavailable"}`))
			return
		}
		writeJSON(w, SyncResponse{
			Sync:      data.SyncStatus,
			Timestamp: data.Timestamp,
		})
	}
}

// HandleWorkspaces returns an HTTP handler for the workspaces endpoint.
func HandleWorkspaces() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := WorkspacesResponse{
			Workspaces: make(map[string]WorkspaceDetail),
			Timestamp:  time.Now(),
		}

		resolver, err := cli.NewResolver()
		if err != nil || resolver.Mode != cli.ModeWorkspace {
			response.Mode = "legacy"
			writeJSON(w, response)
			return
		}

		response.Mode = "workspace"
		cfg, err := config.LoadConfig()
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
	resolver, err := cli.NewResolver()
	if err != nil || resolver.Mode != cli.ModeWorkspace {
		return WorkspaceInfo{Mode: "legacy"}
	}
	return WorkspaceInfo{
		Mode:       "workspace",
		Name:       resolver.WorkspaceName(),
		Workspaces: resolver.WorkspaceNames(),
	}
}

// groupAgentsByWorkspace groups agents by their workspace field.
func groupAgentsByWorkspace(agents []monitor.AgentStatus) map[string][]monitor.AgentStatus {
	groups := make(map[string][]monitor.AgentStatus)
	for _, agent := range agents {
		ws := agent.Workspace
		if ws == "" {
			ws = "(legacy)"
		}
		groups[ws] = append(groups[ws], agent)
	}
	return groups
}
