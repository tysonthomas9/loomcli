package agents

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

// Test-only decoding shapes keep legacy assertion names readable while the
// production handlers emit the generated OpenAPI contracts directly.
type agentSessionTranscriptResponse struct {
	Success bool                        `json:"success"`
	Data    *agentSessionTranscriptData `json:"data,omitempty"`
	Error   string                      `json:"error,omitempty"`
}

type agentSessionTranscriptData struct {
	SessionID string                       `json:"session_id"`
	Entries   agentSessionTranscriptEvents `json:"entries"`
}

type agentRunsResponse struct {
	AgentID  string                       `json:"agent_id"`
	Runs     []*execution.DriverRunRecord `json:"runs"`
	Sessions []*agentHistorySessionDTO    `json:"sessions"`
}

type agentHistorySessionDTO struct {
	WorkspaceKey string                          `json:"workspace_key"`
	SessionID    string                          `json:"session_id"`
	AgentID      string                          `json:"agent_id"`
	Kind         interaction.SessionRecordKind   `json:"kind"`
	TaskID       string                          `json:"task_id,omitempty"`
	Status       interaction.SessionRecordStatus `json:"status"`
	StartedAt    *time.Time                      `json:"started_at,omitempty"`
	Metadata     map[string]string               `json:"metadata,omitempty"`
}

type interactivePromptsResponse struct {
	Prompts []agents.BuiltinInteractivePrompt `json:"prompts"`
}

// agentRecordDTO is a test-only decoder for assertions over the public JSON
// contract. Production handlers emit generated OpenAPI models directly.
type agentRecordDTO struct {
	ID                  string             `json:"id"`
	Name                string             `json:"name"`
	Kind                string             `json:"kind"`
	Enabled             bool               `json:"enabled"`
	Behavior            agentBehaviorDTO   `json:"behavior"`
	BudgetPolicy        string             `json:"budget_policy,omitempty"`
	WorkspaceKey        string             `json:"workspace_key"`
	Bindings            []recordBindingDTO `json:"bindings,omitempty"`
	LastRunStatus       string             `json:"last_run_status,omitempty"`
	ConsecutiveFailures int                `json:"consecutive_failures,omitempty"`
	NextFireAt          *time.Time         `json:"next_fire_at,omitempty"`
	Metadata            map[string]string  `json:"metadata,omitempty"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

type agentBehaviorDTO struct {
	RoleName        string `json:"role_name,omitempty"`
	DriverID        string `json:"driver_id,omitempty"`
	DriverVersionID string `json:"driver_version_id,omitempty"`
}

type recordBindingDTO struct {
	*automation.Binding
	NextFireAt          *time.Time `json:"next_fire_at,omitempty"`
	LastRunStatus       string     `json:"last_run_status,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures,omitempty"`
}
