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

// HandleAgentsScoped returns an HTTP handler for
// GET /api/workspaces/{ws}/monitor/agents. It resolves the workspace ID to a
// workspace name via nameFn, then filters the global monitor data to only
// that workspace's agents. An unknown workspace returns an empty list rather
// than the cross-workspace data that the old global endpoint leaked into
// every workspace's sidebar.
func HandleAgentsScoped(
	collectDataFn func() *monitor.MonitorData,
	nameFn func(wsID string) string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := collectDataFn()
		if data == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"data collection unavailable"}`))
			return
		}
		wsID := workspaceIDFromPath(r)
		wsName := nameFn(wsID)
		agents := filterAgentsByWorkspaceName(data.Agents, wsName)
		writeJSON(w, AgentsResponse{
			Workspace: WorkspaceInfo{Mode: "workspace", Name: wsName},
			Agents:    agents,
			Timestamp: data.Timestamp,
		})
	}
}

// workspaceIDFromPath reads the {ws} path value. Kept local to avoid pulling
// the middleware package into metricscmd.
func workspaceIDFromPath(r *http.Request) string {
	return r.PathValue("ws")
}

// filterAgentsByWorkspaceName returns the subset of agents whose Workspace
// field matches. Empty wsName returns nothing — callers that want all agents
// must use the unscoped HandleAgents.
func filterAgentsByWorkspaceName(all []monitor.AgentStatus, wsName string) []monitor.AgentStatus {
	out := make([]monitor.AgentStatus, 0, len(all))
	if wsName == "" {
		return out
	}
	for _, a := range all {
		if a.Workspace == wsName {
			out = append(out, a)
		}
	}
	return out
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
