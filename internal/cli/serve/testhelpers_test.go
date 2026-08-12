package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
)

type MonitorData = monitor.MonitorData
type AgentStatus = monitor.AgentStatus
type TaskSummary = monitor.TaskSummary
type TaskInfo = monitor.TaskInfo
type SyncInfo = monitor.SyncInfo
type MonitorStats = monitor.MonitorStats
type LoomConfig = config.LoomConfig
type RepoConfig = config.RepoConfig
type WorkspaceConfig = config.WorkspaceConfig

// --- Serve test types ---

// collectDataFunc is a pluggable function for tests.
var collectDataFunc func(context.Context) *monitor.MonitorData

var testWorkspaceConfig *config.LoomConfig

// HealthResponse is a type for serve tests.
type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

type StatusResponse struct {
	Agents         []monitor.AgentStatus       `json:"agents"`
	Tasks          monitor.TaskSummary         `json:"tasks"`
	InProgressList []monitor.TaskInfo          `json:"in_progress_list"`
	AgentTasks     map[string]monitor.TaskInfo `json:"agent_tasks"`
	Stats          monitor.MonitorStats        `json:"stats"`
	Sync           monitor.SyncInfo            `json:"sync"`
	Timestamp      time.Time                   `json:"timestamp"`
}

type AgentsResponse struct {
	Workspace   WorkspaceInfo                    `json:"workspace"`
	Agents      []monitor.AgentStatus            `json:"agents"`
	ByWorkspace map[string][]monitor.AgentStatus `json:"by_workspace,omitempty"`
	Timestamp   time.Time                        `json:"timestamp"`
}

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

type StatsResponse struct {
	Stats     monitor.MonitorStats `json:"stats"`
	Timestamp time.Time            `json:"timestamp"`
}

type SyncResponse struct {
	Sync      monitor.SyncInfo `json:"sync"`
	Timestamp time.Time        `json:"timestamp"`
}

type UsageResponse struct{}

// WorkspaceDetail contains details about a single workspace.
type WorkspaceDetail struct {
	Path  string   `json:"path"`
	Repos []string `json:"repos"`
}

type WorkspacesResponse struct {
	Mode       string                     `json:"mode"`
	Default    string                     `json:"default"`
	Workspaces map[string]WorkspaceDetail `json:"workspaces"`
	Timestamp  time.Time                  `json:"timestamp"`
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

// writeJSON writes JSON to the response writer.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc(r.Context())
	if data == nil {
		http.Error(w, "no data", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, StatusResponse{
		Agents:    data.Agents,
		Tasks:     data.Tasks,
		Stats:     data.Stats,
		Sync:      data.SyncStatus,
		Timestamp: data.Timestamp,
	})
}

func handleAgents(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc(r.Context())
	if data == nil {
		http.Error(w, "no data", http.StatusServiceUnavailable)
		return
	}
	wsInfo := getWorkspaceInfo()
	response := AgentsResponse{
		Workspace: wsInfo,
		Agents:    data.Agents,
		Timestamp: data.Timestamp,
	}
	if wsInfo.Mode == "workspace" {
		response.ByWorkspace = groupAgentsByWorkspace(data.Agents)
	}
	writeJSON(w, response)
}

func handleTasks(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc(r.Context())
	if data == nil {
		http.Error(w, "no data", http.StatusServiceUnavailable)
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

func handleStats(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc(r.Context())
	if data == nil {
		http.Error(w, "no data", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, StatsResponse{Stats: data.Stats, Timestamp: data.Timestamp})
}

func handleSync(w http.ResponseWriter, r *http.Request) {
	data := collectDataFunc(r.Context())
	if data == nil {
		http.Error(w, "no data", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, SyncResponse{Sync: data.SyncStatus, Timestamp: data.Timestamp})
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	data := collectDataFunc(r.Context())
	inProgress := 0
	if data != nil {
		inProgress = data.Tasks.InProgress
	}

	fmt.Fprintf(w, "# HELP loom_ready_tasks Number of tasks ready to be claimed\n")
	fmt.Fprintf(w, "# TYPE loom_ready_tasks gauge\n")
	for p := 0; p <= 4; p++ {
		fmt.Fprintf(w, "loom_ready_tasks{priority=\"%d\"} %d\n", p, 0)
	}
	fmt.Fprintf(w, "\n# HELP loom_in_progress_tasks Number of tasks currently being worked on\n")
	fmt.Fprintf(w, "# TYPE loom_in_progress_tasks gauge\n")
	fmt.Fprintf(w, "loom_in_progress_tasks %d\n", inProgress)

	workerCounts := collectWorkerStatusCounts()
	fmt.Fprintf(w, "\n# HELP loom_fleet_workers Number of fleet workers by status\n")
	fmt.Fprintf(w, "# TYPE loom_fleet_workers gauge\n")
	for _, status := range []string{"active", "idle", "blocked"} {
		fmt.Fprintf(w, "loom_fleet_workers{status=\"%s\"} %d\n", status, workerCounts[status])
	}
}

func handleUsage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, struct{}{})
}

func handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	response := WorkspacesResponse{
		Mode:       "workspace",
		Workspaces: make(map[string]WorkspaceDetail),
		Timestamp:  time.Now(),
	}
	cfg := testWorkspaceConfig
	if cfg == nil {
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

type CommandResult = cli.CommandResult

func NewTestDeps(t testing.TB) (*cli.Deps, *clitest.MockGitRunner, *clitest.MockExecRunner, *clitest.MockFileSystem, *clitest.MockIssueBackend) {
	return clitest.NewTestDeps(t.(*testing.T))
}

// defaultResolver is a package-level resolver for tests.
var defaultResolver *struct{}

// WorkspaceInfo represents workspace metadata for test compat.
type WorkspaceInfo struct {
	Mode       string   `json:"mode"`
	Name       string   `json:"name,omitempty"`
	Workspaces []string `json:"workspaces,omitempty"`
}

// getWorkspaceInfo resolves workspace metadata from the test workspace config.
func getWorkspaceInfo() WorkspaceInfo {
	info := WorkspaceInfo{Mode: "workspace"}
	cfg := testWorkspaceConfig
	if cfg == nil {
		return info
	}
	info.Name = cfg.DefaultWorkspace
	info.Workspaces = make([]string, 0, len(cfg.Workspaces))
	for name := range cfg.Workspaces {
		info.Workspaces = append(info.Workspaces, name)
	}
	sort.Strings(info.Workspaces)
	return info
}

// collectWorkerStatusCounts is a stub for serve test compat.
func collectWorkerStatusCounts() map[string]int {
	return map[string]int{"active": 0, "idle": 0, "blocked": 0}
}

func setupWorkspaceConfig(t *testing.T, cfg *config.LoomConfig) {
	t.Helper()
	old := testWorkspaceConfig
	testWorkspaceConfig = cfg
	t.Cleanup(func() { testWorkspaceConfig = old })
}
