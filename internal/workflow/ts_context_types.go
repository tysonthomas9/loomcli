package workflow

import (
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

type tsContextRequest struct {
	ID              string                   `json:"id"`
	SourcePath      string                   `json:"sourcePath"`
	Input           map[string]any           `json:"input"`
	Env             map[string]string        `json:"env,omitempty"`
	Request         tsContextRequestMetadata `json:"request"`
	Workspace       tsContextWorkspace       `json:"workspace"`
	Workflow        tsContextWorkflowState   `json:"workflow"`
	RuntimeProfile  *tsContextRuntimeProfile `json:"runtimeProfile,omitempty"`
	RuntimeRoot     string                   `json:"runtimeWorkspaceRoot,omitempty"`
	TaskRuns        []*domain.TaskRun        `json:"taskRuns,omitempty"`
	TaskClaims      []tsContextTaskClaim     `json:"taskClaims,omitempty"`
	ReadyChildren   []backend.IssueData      `json:"readyChildren,omitempty"`
	BlockedChildren []backend.IssueData      `json:"blockedChildren,omitempty"`
	ChildWorkItems  []backend.IssueData      `json:"childWorkItems,omitempty"`
}

type tsContextWorkflowState struct {
	Status          string `json:"status"`
	WaitCondition   string `json:"waitCondition,omitempty"`
	CancelRequested bool   `json:"cancelRequested"`
}

type tsContextTaskClaim struct {
	TaskRunID    string `json:"task_run_id"`
	WorkItemID   string `json:"work_item_id"`
	ClaimActor   string `json:"claim_actor,omitempty"`
	ClaimEventID string `json:"claim_event_id,omitempty"`
	Status       string `json:"status"`
	AgentID      string `json:"agent_id,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	Active       bool   `json:"active"`
}

type tsContextRequestMetadata struct {
	WorkspaceKey    string `json:"workspaceKey"`
	WorkflowName    string `json:"workflowName"`
	WorkflowVersion string `json:"workflowVersion"`
	Actor           string `json:"actor,omitempty"`
}

type tsContextResponse struct {
	Result     json.RawMessage       `json:"result"`
	Logs       []tsWorkflowLog       `json:"logs"`
	Operations []tsWorkflowOperation `json:"operations"`
}

type tsWorkflowLog struct {
	Level      string         `json:"level"`
	Message    string         `json:"message"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type tsWorkflowOperation struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

type tsAppliedOperations struct {
	TaskRuns      []*domain.TaskRun
	WaitCondition string
	Cancelled     bool
	Run           *domain.WorkflowRun
}
