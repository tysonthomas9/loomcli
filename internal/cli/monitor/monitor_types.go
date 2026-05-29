package monitor

import "time"

// MonitorData holds all dashboard information
type MonitorData struct {
	Timestamp          time.Time
	Agents             []AgentStatus
	Tasks              TaskSummary
	NeedsPlanningTasks []TaskInfo          // Ready tasks without design (top 5)
	ReadyToImplement   []TaskInfo          // Ready tasks with design (top 5)
	ReviewTasks        []TaskInfo          // top 5 need review tasks
	InProgressTasks    []TaskInfo          // all in_progress tasks
	BacklogTasks       []TaskInfo          // backlog tasks (top 20)
	ClosedTasks        []TaskInfo          // closed tasks (top 50)
	AgentTasks         map[string]TaskInfo // agent name -> current task (from assignee)
	TaskConflicts      map[string][]string // TaskID -> agent names (if multiple agents claim same task)
	SyncStatus         SyncInfo
	Stats              MonitorStats
}

// CommitDetail represents a single commit with hash, message, and optional GitHub URL.
type CommitDetail struct {
	Hash    string `json:"hash"`
	Message string `json:"message"`
	URL     string `json:"url,omitempty"` // GitHub commit URL if remote available
}

// FileChange represents a single file change from git status.
type FileChange struct {
	Status string `json:"status"` // "M", "A", "D", "??", "R"
	Path   string `json:"path"`
}

// AgentStatus represents a single agent/worktree status
type AgentStatus struct {
	Name           string         `json:"name"`
	Branch         string         `json:"branch"`
	Status         string         `json:"status"`                     // "ready", "3 changes", "running (plan, 5m ago)"
	Ahead          int            `json:"ahead"`                      // commits ahead of integration branch
	Behind         int            `json:"behind"`                     // commits behind integration branch
	Role           string         `json:"role,omitempty"`             // role from daemon config (e.g., "plan", "task")
	Repo           string         `json:"repo,omitempty"`             // repository this agent is assigned to (multi-repo)
	Workspace      string         `json:"workspace"`                  // workspace name
	DaemonManaged  bool           `json:"daemon_managed,omitempty"`   // true if under daemon supervision
	Commits        []CommitDetail `json:"commits,omitempty"`          // recent commits ahead of integration branch
	Changes        []FileChange   `json:"changes,omitempty"`          // uncommitted file changes
	CurrentTaskID  string         `json:"current_task_id,omitempty"`  // task this daemon-managed agent has claimed; empty between tasks
	LastActivityAt *time.Time     `json:"last_activity_at,omitempty"` // most recent PTY-output observation from the agent's supervised backend; nil when not reported (a zero time.Time would serialize as "0001-01-01T00:00:00Z" and the UI would render it as a bogus "last seen" age)
}

// TaskInfo represents a task with basic info
type TaskInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
	Status   string `json:"status"` // "in_progress", "closed", "open"
}

// TaskSummary holds task counts by category
type TaskSummary struct {
	NeedsPlanning    int `json:"needs_planning"`     // Ready tasks without design
	ReadyToImplement int `json:"ready_to_implement"` // Ready tasks with approved design
	InProgress       int `json:"in_progress"`
	NeedReview       int `json:"need_review"`
	Backlog          int `json:"backlog"`
	Epics            int `json:"epics"` // Open epics (tracked separately)
}

// WorktreeSyncDetail holds per-worktree sync detail (commits ahead or behind).
type WorktreeSyncDetail struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// SyncInfo holds sync status information
type SyncInfo struct {
	DBSynced       bool                 `json:"db_synced"`
	DBLastSync     string               `json:"db_last_sync"`
	DBError        string               `json:"db_error,omitempty"`
	GitNeedsPush   int                  `json:"git_needs_push"`
	GitNeedsPull   int                  `json:"git_needs_pull"`
	GitPushDetails []WorktreeSyncDetail `json:"git_push_details,omitempty"`
	GitPullDetails []WorktreeSyncDetail `json:"git_pull_details,omitempty"`
}

// MonitorStats holds overall statistics
type MonitorStats struct {
	Open       int     `json:"open"`
	Closed     int     `json:"closed"`
	Total      int     `json:"total"`
	Completion float64 `json:"completion"`
	Remaining  int     `json:"remaining"`
	InProgress int     `json:"in_progress"`
	Review     int     `json:"review"`
	Blocked    int     `json:"blocked"`
}

// DaemonAgentState represents the daemon-agents.json file format.
// This matches the DaemonState written by daemon_cmd.go.
type DaemonAgentState struct {
	PID    int                     `json:"pid"`
	Agents []DaemonAgentStateEntry `json:"agents"`
}

// DaemonAgentStateEntry represents a single agent in daemon-agents.json
type DaemonAgentStateEntry struct {
	Worktree     string    `json:"worktree"`
	Status       string    `json:"status"`
	Role         string    `json:"role"`
	Repo         string    `json:"repo,omitempty"`
	TaskID       string    `json:"task_id,omitempty"`
	LastActivity time.Time `json:"last_activity,omitempty"`
}

// DaemonAgentInfo carries daemon supervision metadata for a worktree.
type DaemonAgentInfo struct {
	Managed       bool
	Role          string
	Repo          string
	CurrentTaskID string
	LastActivity  time.Time
}
