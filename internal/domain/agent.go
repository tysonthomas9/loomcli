package domain

import (
	"fmt"
	"strings"
	"time"
)

// AgentState reports whether an agent assignment is currently running,
// stopped, or idle (registered but not active). This is loom's view of
// the assignment, distinct from fleet-db's per-claim Worker state.
type AgentState string

const (
	AgentStateIdle    AgentState = "idle"
	AgentStateActive  AgentState = "active"
	AgentStateStopped AgentState = "stopped"
	// AgentStateBackendUnavailable means the agent is registered but
	// the backend CLI it would invoke is not on PATH. Distinct from
	// "stopped" (manual shutdown) or "failed" (exhausted retries): the
	// daemon reconciler re-checks PATH each tick and auto-transitions
	// back to AgentStateIdle once the binary appears.
	AgentStateBackendUnavailable AgentState = "backend_unavailable"
)

// AgentLiveStatus is a DERIVED, read-only liveness signal carried straight from
// fleet-db's agent response (fleet-db computes it from the session+lease join;
// see its ComputeLiveWork). loom does not derive it — it only passes it through.
// Distinct from State, which is the coarse stored intent.
type AgentLiveStatus string

const (
	// AgentLiveWorking means the agent has a live session (running + fresh lease).
	AgentLiveWorking AgentLiveStatus = "working"
	// AgentLiveIdle means no live session was found.
	AgentLiveIdle AgentLiveStatus = "idle"
)

// AgentHookActionType enumerates the completion-hook actions the supervisor
// performs on an agent's behalf. The vocabulary is closed on purpose: agent
// definitions are remotely writable control-plane data, so accepting an
// executable path or shell string would be daemon-host code execution.
type AgentHookActionType string

const (
	// AgentHookActionComment posts the run's artifact as an issue comment.
	AgentHookActionComment AgentHookActionType = "comment"
	// AgentHookActionAddLabel stamps a literal label on the owned task.
	AgentHookActionAddLabel AgentHookActionType = "add_label"
)

// IsValid returns true if the action type is a recognized constant.
func (t AgentHookActionType) IsValid() bool {
	switch t {
	case AgentHookActionComment, AgentHookActionAddLabel:
		return true
	}
	return false
}

// AgentHookCommentSource enumerates where a comment action draws its text from.
type AgentHookCommentSource string

// AgentHookCommentSourceFinalReply takes the run's final assistant reply.
const AgentHookCommentSourceFinalReply AgentHookCommentSource = "final_reply"

// AgentHookAction is one step of a completion pipeline. Only the fields
// appropriate to Type may be set; Validate rejects the rest.
type AgentHookAction struct {
	Type   AgentHookActionType    `json:"type" yaml:"type"`
	Source AgentHookCommentSource `json:"source,omitempty" yaml:"source,omitempty"`
	Value  string                 `json:"value,omitempty" yaml:"value,omitempty"`
}

// AgentHooks holds the supervisor-owned hook pipelines for an agent.
type AgentHooks struct {
	// OnComplete runs in slice order after a successful agent turn. Every
	// comment action must precede every add_label action so a completion label
	// can never outrun the artifact it certifies (see Validate).
	OnComplete []AgentHookAction `json:"on_complete,omitempty" yaml:"on_complete,omitempty"`
}

// IsEmpty reports whether the hooks carry no configured actions. Nil and empty
// are the same value: "no hooks", which preserves the pre-hook behavior.
func (h *AgentHooks) IsEmpty() bool {
	return h == nil || len(h.OnComplete) == 0
}

// Clone returns a deep copy so hook slices are never shared across boundaries.
func (h *AgentHooks) Clone() *AgentHooks {
	if h == nil {
		return nil
	}
	out := &AgentHooks{}
	if h.OnComplete != nil {
		out.OnComplete = append([]AgentHookAction(nil), h.OnComplete...)
	}
	return out
}

// Equal reports whether two pipelines are semantically identical, treating nil
// and empty as the same value.
func (h *AgentHooks) Equal(other *AgentHooks) bool {
	if h.IsEmpty() || other.IsEmpty() {
		return h.IsEmpty() && other.IsEmpty()
	}
	if len(h.OnComplete) != len(other.OnComplete) {
		return false
	}
	for i := range h.OnComplete {
		if h.OnComplete[i] != other.OnComplete[i] {
			return false
		}
	}
	return true
}

// Validate checks the pipeline shape and its write ordering. It is enforced
// again at execution time: a stored pipeline that violates the order must be
// refused, never silently reordered.
func (h *AgentHooks) Validate() error {
	if h == nil {
		return nil
	}
	sawLabel := false
	for i := range h.OnComplete {
		a := h.OnComplete[i]
		if !a.Type.IsValid() {
			return fmt.Errorf("hooks.on_complete[%d]: unknown action type %q (must be one of: comment, add_label)", i, a.Type)
		}
		switch a.Type {
		case AgentHookActionComment:
			if a.Source == "" {
				return fmt.Errorf("hooks.on_complete[%d]: comment action requires source", i)
			}
			if a.Source != AgentHookCommentSourceFinalReply {
				return fmt.Errorf("hooks.on_complete[%d]: comment source %q must be final_reply", i, a.Source)
			}
			if a.Value != "" {
				return fmt.Errorf("hooks.on_complete[%d]: comment action must not set value", i)
			}
			// Write-before-stamp: the artifact must land before the label that
			// certifies it, so a label is never observable without its comment.
			if sawLabel {
				return fmt.Errorf("hooks.on_complete[%d]: comment action must not follow an add_label action", i)
			}
		case AgentHookActionAddLabel:
			if strings.TrimSpace(a.Value) == "" {
				return fmt.Errorf("hooks.on_complete[%d]: add_label action requires a non-blank value", i)
			}
			if a.Source != "" {
				return fmt.Errorf("hooks.on_complete[%d]: add_label action must not set source", i)
			}
			sawLabel = true
		}
	}
	return nil
}

// Agent is a long-lived assignment of a Role to one or more Repos within
// a Workspace. Distinct from a Worker (fleet-db's per-claim record); an
// Agent persists across many task claims.
//
// Name is the workspace-scoped agent identifier (unique within
// WorkspaceKey, typically used as the tmux session + worktree dir name).
// Empty Repos means "all repos in the workspace".
type Agent struct {
	WorkspaceKey     string   `json:"workspace_key"`
	Name             string   `json:"name"`
	RoleName         string   `json:"role_name"`
	Auto             bool     `json:"auto,omitempty"`
	Backend          string   `json:"backend,omitempty"`
	FallbackBackends []string `json:"fallback_backends,omitempty"`
	Repos            []string `json:"repos,omitempty"`
	RepoGroups       []string `json:"repo_groups,omitempty"`
	CrossRepo        bool     `json:"cross_repo,omitempty"`
	Parent           string   `json:"parent,omitempty"`
	// OrchestratorSessionID was here historically as a cache of the
	// lead-to-orchestration AgentSession join. AgentSession is the
	// single source of truth; use store.OrchestrationSessionIDFor.
	State          AgentState        `json:"state,omitempty"`
	Mode           AgentMode         `json:"mode,omitempty"`
	TaskFilter     string            `json:"task_filter,omitempty"`
	MaxConcurrency int               `json:"max_concurrency,omitempty"`
	BudgetPolicy   string            `json:"budget_policy,omitempty"`
	DesiredState   AgentDesiredState `json:"desired_state,omitempty"`
	// Hooks holds supervisor-owned post-run pipelines. Nil or empty preserves
	// the pre-hook behavior: the agent's own prompt does its bookkeeping.
	Hooks     *AgentHooks `json:"hooks,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`

	// LiveStatus, ActiveTaskID, and ActivePhase are DERIVED, read-only fields
	// carried from fleet-db's agent response (computed there from the live
	// session+lease join). They are never persisted; ActiveTaskID/ActivePhase
	// are set only when LiveStatus == AgentLiveWorking.
	LiveStatus   AgentLiveStatus `json:"live_status,omitempty"`
	ActiveTaskID string          `json:"active_task_id,omitempty"`
	ActivePhase  string          `json:"active_phase,omitempty"`

	// LastErrorClass is a DERIVED, read-only field carried from fleet-db: the
	// error_class of the agent's most recent terminal session when that run
	// failed, surfaced only while the agent is idle. Lets the UI explain why a
	// stalled agent stopped instead of showing a bare "agent missing".
	LastErrorClass string `json:"last_error_class,omitempty"`
}
