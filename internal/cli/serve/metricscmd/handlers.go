package metricscmd

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

// WorkspaceInfo represents workspace metadata for API responses.
type WorkspaceInfo struct {
	Mode       string   `json:"mode"`
	Name       string   `json:"name,omitempty"`
	Workspaces []string `json:"workspaces,omitempty"`
}

// WorkspacesResponse lists all configured workspaces.
type WorkspacesResponse struct {
	Mode       string                     `json:"mode"`
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

type IssueBackendFn func(ctx context.Context) backend.IssueBackend

// HandleStatus returns an HTTP handler for the full status endpoint.
func HandleStatus(collectDataFn func() *monitor.MonitorData, st store.Store) http.HandlerFunc {
	return HandleStatusWithBackend(collectDataFn, st, nil)
}

func HandleStatusWithBackend(collectDataFn func() *monitor.MonitorData, st store.Store, backendFn IssueBackendFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := monitorDataForRequest(r, collectDataFn, backendFn)
		if data == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"data collection unavailable"}`))
			return
		}
		workspaceHint := r.URL.Query().Get("workspace")
		agents := mergeStoreAgents(r.Context(), st, data.Agents, workspaceHint)
		writeJSON(w, StatusResponse{
			Workspace:      getWorkspaceInfo(r.Context(), st, workspaceHint),
			Agents:         agents,
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
func HandleAgents(collectDataFn func() *monitor.MonitorData, st store.Store) http.HandlerFunc {
	return HandleAgentsWithBackend(collectDataFn, st, nil)
}

func HandleAgentsWithBackend(collectDataFn func() *monitor.MonitorData, st store.Store, backendFn IssueBackendFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := monitorDataForRequest(r, collectDataFn, backendFn)
		if data == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"data collection unavailable"}`))
			return
		}
		workspaceHint := r.URL.Query().Get("workspace")
		wsInfo := getWorkspaceInfo(r.Context(), st, workspaceHint)
		agents := mergeStoreAgents(r.Context(), st, data.Agents, workspaceHint)

		response := AgentsResponse{
			Workspace: wsInfo,
			Agents:    agents,
			Timestamp: data.Timestamp,
		}

		response.ByWorkspace = groupAgentsByWorkspace(agents)

		writeJSON(w, response)
	}
}

// HandleTasks returns an HTTP handler for the tasks endpoint.
func HandleTasks(collectDataFn func() *monitor.MonitorData) http.HandlerFunc {
	return HandleTasksWithBackend(collectDataFn, nil)
}

func HandleTasksWithBackend(collectDataFn func() *monitor.MonitorData, backendFn IssueBackendFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := monitorDataForRequest(r, collectDataFn, backendFn)
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
	return HandleStatsWithBackend(collectDataFn, nil)
}

func HandleStatsWithBackend(collectDataFn func() *monitor.MonitorData, backendFn IssueBackendFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := monitorDataForRequest(r, collectDataFn, backendFn)
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

func monitorDataForRequest(r *http.Request, collectDataFn func() *monitor.MonitorData, backendFn IssueBackendFn) *monitor.MonitorData {
	workspaceHint := r.URL.Query().Get("workspace")
	if workspaceHint == "" || backendFn == nil {
		return collectDataFn()
	}
	ctx := middleware.WithWorkspace(r.Context(), workspaceHint)
	if be := backendFn(ctx); be != nil {
		return monitor.CollectMonitorDataWithIssueBackend(be, 10000, "")
	}
	return collectDataFn()
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
func HandleWorkspaces(st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := WorkspacesResponse{
			Mode:       "workspace",
			Workspaces: make(map[string]WorkspaceDetail),
			Timestamp:  time.Now(),
		}

		if st == nil {
			writeJSON(w, response)
			return
		}

		active, err := bootstrap.ResolveActiveWorkspaceKey(r.Context(), st.Workspaces())
		if err == nil {
			if ws, getErr := st.Workspaces().Get(r.Context(), active); getErr == nil && ws != nil {
				response.Default = ws.Name
			}
		}

		workspaces, err := st.Workspaces().List(r.Context())
		if err != nil {
			writeJSON(w, response)
			return
		}

		for _, ws := range workspaces {
			storeRepos, repoErr := st.Repos().List(r.Context(), ws.Key)
			repos := make([]string, 0, len(storeRepos))
			if repoErr == nil {
				for _, repo := range storeRepos {
					repos = append(repos, repo.Name)
				}
			}
			response.Workspaces[ws.Name] = WorkspaceDetail{
				Path:  storeadapter.ResolveWorkspacePath(ws.Key),
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
func getWorkspaceInfo(ctx context.Context, st store.Store, workspaceHint string) WorkspaceInfo {
	info := WorkspaceInfo{Mode: "workspace"}
	if st == nil {
		return info
	}
	workspaces, err := st.Workspaces().List(ctx)
	if err != nil {
		return info
	}
	info.Workspaces = make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		info.Workspaces = append(info.Workspaces, ws.Name)
	}
	if _, wsName, ok := resolveMonitorWorkspace(ctx, st, workspaceHint); ok {
		info.Name = wsName
	}
	return info
}

// groupAgentsByWorkspace groups agents by their workspace field.
func groupAgentsByWorkspace(agents []monitor.AgentStatus) map[string][]monitor.AgentStatus {
	groups := make(map[string][]monitor.AgentStatus)
	for _, agent := range agents {
		ws := agent.Workspace
		if ws == "" {
			ws = "unassigned"
		}
		groups[ws] = append(groups[ws], agent)
	}
	return groups
}

func mergeStoreAgents(ctx context.Context, st store.Store, agents []monitor.AgentStatus, workspaceHint string) []monitor.AgentStatus {
	if st == nil {
		return agents
	}
	wsKey, wsName, ok := resolveMonitorWorkspace(ctx, st, workspaceHint)
	if !ok {
		return agents
	}
	assignments, err := st.Agents().List(ctx, wsKey)
	if err != nil {
		log.Printf("Failed to list store agents for monitor response: %v", err)
		return agents
	}
	if len(assignments) == 0 {
		return agents
	}

	assignmentsByName := agentsByName(assignments)
	merged, byName := filterRuntimeAgents(agents, assignmentsByName, workspaceHint, wsKey, wsName)
	for _, assignment := range assignments {
		if assignment == nil {
			continue
		}
		if idx, exists := byName[assignment.Name]; exists {
			enrichRuntimeAgent(&merged[idx], assignment, wsKey, wsName)
			continue
		}
		merged = append(merged, monitor.AgentStatus{
			Name:      assignment.Name,
			Branch:    monitorBranchFromStoreAgent(wsKey, assignment),
			Status:    monitorStatusFromAgentState(assignment.State),
			Role:      assignment.RoleName,
			Repo:      monitorRepoFromAgent(assignment),
			Workspace: wsName,
		})
	}
	return merged
}

func agentsByName(assignments []*domain.Agent) map[string]*domain.Agent {
	byName := make(map[string]*domain.Agent, len(assignments))
	for _, assignment := range assignments {
		if assignment != nil {
			byName[assignment.Name] = assignment
		}
	}
	return byName
}

func filterRuntimeAgents(
	agents []monitor.AgentStatus,
	assignmentsByName map[string]*domain.Agent,
	workspaceHint, wsKey, wsName string,
) ([]monitor.AgentStatus, map[string]int) {
	merged := make([]monitor.AgentStatus, 0, len(agents)+len(assignmentsByName))
	byName := make(map[string]int, len(agents))
	for _, agent := range agents {
		if !shouldKeepRuntimeAgent(agent, assignmentsByName, workspaceHint, wsKey, wsName) {
			continue
		}
		byName[agent.Name] = len(merged)
		merged = append(merged, agent)
	}
	return merged, byName
}

func shouldKeepRuntimeAgent(
	agent monitor.AgentStatus,
	assignmentsByName map[string]*domain.Agent,
	workspaceHint, wsKey, wsName string,
) bool {
	if workspaceHint == "" {
		return true
	}
	_, assignedToWorkspace := assignmentsByName[agent.Name]
	return assignedToWorkspace || agent.Workspace == wsName || agent.Workspace == wsKey
}

func enrichRuntimeAgent(agent *monitor.AgentStatus, assignment *domain.Agent, wsKey, wsName string) {
	if agent == nil || assignment == nil {
		return
	}
	if agent.Branch == "" || agent.Branch == "unknown" {
		agent.Branch = monitorBranchFromStoreAgent(wsKey, assignment)
	}
	if agent.Role == "" {
		agent.Role = assignment.RoleName
	}
	if agent.Repo == "" {
		agent.Repo = monitorRepoFromAgent(assignment)
	}
	if agent.Workspace == "" {
		agent.Workspace = wsName
	}
}

func monitorBranchFromStoreAgent(wsKey string, agent *domain.Agent) string {
	const unknownBranch = "unknown"
	repoName := monitorRepoFromAgent(agent)
	if wsKey == "" || repoName == "" || agent == nil {
		return unknownBranch
	}
	workspacePath := storeadapter.ResolveWorkspacePath(wsKey)
	if workspacePath == "" {
		return unknownBranch
	}
	branch, err := monitor.ReadBranchFromFS(filepath.Join(workspacePath, "worktrees", repoName, agent.Name))
	if err != nil || branch == "" {
		return unknownBranch
	}
	return branch
}

func monitorStatusFromAgentState(state domain.AgentState) string {
	switch state {
	case domain.AgentStateActive:
		return "working"
	default:
		return "idle"
	}
}

func monitorRepoFromAgent(agent *domain.Agent) string {
	if agent == nil || agent.CrossRepo || len(agent.Repos) != 1 {
		return ""
	}
	return agent.Repos[0]
}

func resolveMonitorWorkspace(ctx context.Context, st store.Store, workspaceHint string) (key string, name string, ok bool) {
	if st == nil {
		return "", "", false
	}
	if workspaceHint != "" {
		if ws, err := st.Workspaces().Get(ctx, workspaceHint); err == nil && ws != nil {
			return ws.Key, ws.Name, true
		}
		if ws, err := st.Workspaces().GetByName(ctx, workspaceHint); err == nil && ws != nil {
			return ws.Key, ws.Name, true
		}
		return "", "", false
	}
	key, err := bootstrap.ResolveActiveWorkspaceKey(ctx, st.Workspaces())
	if err != nil {
		return "", "", false
	}
	ws, err := st.Workspaces().Get(ctx, key)
	if err != nil || ws == nil {
		return "", "", false
	}
	return key, ws.Name, true
}
