package metricscmd

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
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
	Default    string                     `json:"default"`    // deprecated legacy workspace hint
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
	return HandleStatusWithDataSource(NewMonitorDataSource(collectDataFn, backendFn), st)
}

func HandleStatusWithDataSource(dataSource *MonitorDataSource, st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceHint := r.URL.Query().Get("workspace")
		ctx, span := startSpan(r.Context(), "service.Monitor.Status",
			attribute.String("loom.workspace", workspaceHint))
		defer span.End()
		r = r.WithContext(ctx)

		data := dataSource.Resolve(r)
		if data == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"data collection unavailable"}`))
			return
		}
		agents := storeAgentsForMonitor(ctx, st, workspaceHint)
		span.SetAttributes(attribute.Int("result.count", len(agents)))
		writeJSON(w, StatusResponse{
			Workspace:      getWorkspaceInfo(ctx, st, workspaceHint),
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
	return HandleAgentsWithDataSource(NewMonitorDataSource(collectDataFn, backendFn), st)
}

func HandleAgentsWithDataSource(dataSource *MonitorDataSource, st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceHint := r.URL.Query().Get("workspace")
		ctx, span := startSpan(r.Context(), "service.Monitor.Agents",
			attribute.String("loom.workspace", workspaceHint))
		defer span.End()
		r = r.WithContext(ctx)

		data := dataSource.Resolve(r)
		if data == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"data collection unavailable"}`))
			return
		}
		wsInfo := getWorkspaceInfo(ctx, st, workspaceHint)
		agents := storeAgentsForMonitor(ctx, st, workspaceHint)

		response := AgentsResponse{
			Workspace: wsInfo,
			Agents:    agents,
			Timestamp: data.Timestamp,
		}

		response.ByWorkspace = groupAgentsByWorkspace(agents)

		span.SetAttributes(attribute.Int("result.count", len(agents)))
		writeJSON(w, response)
	}
}

// HandleTasks returns an HTTP handler for the tasks endpoint.
func HandleTasks(collectDataFn func() *monitor.MonitorData) http.HandlerFunc {
	return HandleTasksWithBackend(collectDataFn, nil)
}

func HandleTasksWithBackend(collectDataFn func() *monitor.MonitorData, backendFn IssueBackendFn) http.HandlerFunc {
	return HandleTasksWithDataSource(NewMonitorDataSource(collectDataFn, backendFn))
}

func HandleTasksWithDataSource(dataSource *MonitorDataSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceHint := r.URL.Query().Get("workspace")
		ctx, span := startSpan(r.Context(), "service.Monitor.Tasks",
			attribute.String("loom.workspace", workspaceHint))
		defer span.End()
		r = r.WithContext(ctx)

		data := dataSource.Resolve(r)
		if data == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"data collection unavailable"}`))
			return
		}
		// result.count = total task entries surfaced across the buckets so
		// dashboards can see how chatty this endpoint is per workspace.
		span.SetAttributes(attribute.Int("result.count",
			len(data.NeedsPlanningTasks)+len(data.ReadyToImplement)+
				len(data.ReviewTasks)+len(data.InProgressTasks)+
				len(data.BacklogTasks)+len(data.ClosedTasks)))
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
	return HandleStatsWithDataSource(NewMonitorDataSource(collectDataFn, backendFn))
}

func HandleStatsWithDataSource(dataSource *MonitorDataSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := dataSource.Resolve(r)
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
	return NewMonitorDataSource(collectDataFn, backendFn).Resolve(r)
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

func storeAgentsForMonitor(ctx context.Context, st store.Store, workspaceHint string) []monitor.AgentStatus {
	agents := make([]monitor.AgentStatus, 0)
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
	workspaceData, err := storeadapter.BuildWorkspaceDataForKey(ctx, st, wsKey)
	if err != nil {
		log.Printf("Failed to load workspace data for monitor response: %v", err)
	}

	for _, assignment := range assignments {
		if assignment == nil {
			continue
		}
		agents = append(agents, monitor.AgentStatus{
			Name:      assignment.Name,
			Branch:    monitorBranchFromAgent(workspaceData, assignment),
			Status:    monitorStatusFromAgentState(assignment.State),
			Role:      assignment.RoleName,
			Repo:      monitorRepoFromAgent(assignment),
			Workspace: wsName,
		})
	}
	return agents
}

func monitorStatusFromAgentState(state domain.AgentState) string {
	switch state {
	case domain.AgentStateActive:
		return "working"
	default:
		return "idle"
	}
}

func monitorBranchFromAgent(ws *ops.WorkspaceData, agent *domain.Agent) string {
	if ws == nil || agent == nil || ws.Path == "" {
		return "unknown"
	}
	repo, ok := selectMonitorAgentRepo(ws.Repos, ops.WorkspaceAgentInfo{
		Name:       agent.Name,
		Repos:      agent.Repos,
		RepoGroups: agent.RepoGroups,
		CrossRepo:  agent.CrossRepo,
	})
	if !ok || repo.Name == "" {
		return "unknown"
	}
	worktreePath := filepath.Join(ws.Path, "worktrees", repo.Name, agent.Name)
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		return "unknown"
	}
	branch, err := cli.GetCurrentBranch(worktreePath)
	if err != nil || branch == "" {
		return "unknown"
	}
	return branch
}

func selectMonitorAgentRepo(repos []ops.WorkspaceRepo, agent ops.WorkspaceAgentInfo) (ops.WorkspaceRepo, bool) {
	if len(repos) == 0 {
		return ops.WorkspaceRepo{}, false
	}
	allowed := make(map[string]bool)
	for _, name := range agent.Repos {
		allowed[name] = true
	}
	for _, group := range agent.RepoGroups {
		for _, repo := range repos {
			for _, repoGroup := range repo.Groups {
				if repoGroup == group {
					allowed[repo.Name] = true
					break
				}
			}
		}
	}
	if len(allowed) == 0 {
		return repos[0], true
	}
	for _, repo := range repos {
		if allowed[repo.Name] {
			return repo, true
		}
	}
	return ops.WorkspaceRepo{}, false
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
