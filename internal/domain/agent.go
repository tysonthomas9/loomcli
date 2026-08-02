package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/types"
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
	// AgentHookActionWriteDesign writes the run's final assistant reply into the
	// owned task's design field.
	//
	// It exists so a read_only role can produce a design at all. Writing one
	// otherwise means `loom data update --design`, which needs a shell: a planner
	// restricted to read-only tools can reason a design out and then has no way
	// to record it, so the artifact dies with the run. Handing the write to the
	// supervisor keeps the role read-only and still lands the design.
	//
	// Content comes from the same place a comment's does — Source must be
	// final_reply — rather than from a second mechanism that could disagree with
	// the first about what "the run's artifact" is. The supervisor reuses the one
	// extraction for both (see executeCompletionHooks).
	//
	// Deliberately uncapped, unlike comment: a comment body is chunked to
	// fleet-db's 10 KB per-comment cap, a design is a whole document, and
	// truncating one to a comment's budget would defeat the point of the action.
	// The effective ceiling is fleet-db's 1 MB request-body limit. Nothing grows
	// the stored pipeline either way — the action names a source and carries no
	// body of its own.
	AgentHookActionWriteDesign AgentHookActionType = "write_design"
	// AgentHookActionAddLabel stamps a literal label on the owned task.
	AgentHookActionAddLabel AgentHookActionType = "add_label"
	// AgentHookActionRemoveLabel strips a literal label from the owned task —
	// the symmetric counterpart of add_label, carrying the same literal value
	// and validated by exactly the same rules.
	//
	// Without it label state is monotonic: a stage can only ever add, so the
	// label that routed work to it is still on the task when it finishes and
	// the routing predicate keeps matching. A stage that wants to consume its
	// own token had no way to say so — the only removal primitive was the one
	// buried in a cycle's re-arm, which is not separable from the bounded-loop
	// counter it exists to drive.
	//
	// CAUTION — removing a label a FEEDING stage EXCLUDES re-arms that stage.
	// A stage that stopped re-claiming a task because the task carries label X
	// matches it again the moment X is removed, re-stamps X, and the loop runs
	// forever. This has been observed live as an unbounded ship/re-stamp cycle
	// firing every ~32 seconds. Before removing X, check what upstream filter
	// treats X as its "already handled" marker. Nothing here detects the loop:
	// which label an upstream filter keys on is configuration this action
	// cannot see, so a code guard would only be guessing at intent.
	//
	// Carries a literal label and nothing else, so the closed vocabulary above
	// is unchanged: like add_label it cannot express a path or a shell string.
	AgentHookActionRemoveLabel AgentHookActionType = "remove_label"
	// AgentHookActionSetStatus moves the owned task to a literal status, carried
	// in Value.
	//
	// Without it a stage could only route by label, so every hand-off condition
	// had to be encoded as one — including the ones the board and the task router
	// already express as status. A planner that finishes by parking its task in
	// `review` for a human had no way to say so.
	//
	// The legal set is not a preference: it is the server's own PATCH contract,
	// checked through types.ValidateSettableStatus, which is loom's copy of
	// fleet-db's models.ValidateSettableStatus down to the error strings. open,
	// blocked, deferred and review are settable; in_progress belongs to the claim
	// endpoint, which also takes the lease. closed is excluded because the close
	// action already owns it, through a different endpoint that also records
	// closed_at and a close reason — the two actions are non-overlapping by
	// server contract, not by convention, so neither can express what the other
	// does.
	//
	// A blocked status must carry a Reason; see the field.
	//
	// Carries a status drawn from a closed set, so the vocabulary above is
	// unchanged: like add_label it cannot express a path or a shell string.
	AgentHookActionSetStatus AgentHookActionType = "set_status"
	// AgentHookActionClose closes the owned task, and must be the last action
	// in a pipeline.
	//
	// It exists so a stage can close without giving up its hand-off. An agent
	// that closes its own task leaves nothing for the supervisor to write to:
	// add_label then fails against a terminal issue, so the label the next
	// stage waits on is never stamped and the pipeline stops. Deferring the
	// close to the supervisor keeps the writes and the terminal transition in
	// one ordered sequence it controls.
	AgentHookActionClose AgentHookActionType = "close"
	// AgentHookActionCycle advances a bounded review loop: it either re-arms the
	// previous stage for another round, or stamps the ship label once the
	// configured number of rounds has run.
	//
	// The counter lives in the label set as <prefix><n> rather than in a side
	// table, because labels are the only per-issue state both stages already
	// read. The count is the MAX of the parsed counters, so a leftover from a
	// crashed cleanup is harmless.
	//
	// Carries no free text — only the structured Cycle block — so the closed
	// vocabulary still holds.
	AgentHookActionCycle AgentHookActionType = "cycle"
)

// DefaultCycleLabelPrefix is the counter-label prefix when none is configured.
const DefaultCycleLabelPrefix = "review-cycle="

// AgentHookCycle parameterizes a bounded review loop.
type AgentHookCycle struct {
	// Threshold is the number of rounds to run before shipping. Must be >= 1.
	// A threshold of 1 ships on the first pass and writes no counter at all.
	Threshold int `json:"threshold" yaml:"threshold"`

	// RearmLabel is removed to hand the task back to the previous stage. It is
	// removed FIRST, before the counter is bumped: a crash in between repeats a
	// round, which is safe, whereas bumping first could leave the counter
	// advanced with the stage never re-armed — indistinguishable from a round
	// that already ran, silently skipping review.
	RearmLabel string `json:"rearm_label" yaml:"rearm_label"`

	// ShipLabel is stamped once the threshold is reached, handing the task to
	// the downstream stage.
	ShipLabel string `json:"ship_label" yaml:"ship_label"`

	// Prefix overrides DefaultCycleLabelPrefix.
	Prefix string `json:"prefix,omitempty" yaml:"prefix,omitempty"`
}

// LabelPrefix returns the configured prefix or the default.
func (c *AgentHookCycle) LabelPrefix() string {
	if c == nil || strings.TrimSpace(c.Prefix) == "" {
		return DefaultCycleLabelPrefix
	}
	return c.Prefix
}

// CounterLabel renders the counter label for n completed rounds.
func (c *AgentHookCycle) CounterLabel(n int) string {
	return fmt.Sprintf("%s%d", c.LabelPrefix(), n)
}

// ParseCounter returns the round count encoded in label, or 0 when it is not a
// counter for this cycle. Deliberately strict: a stray "review-cycle=1.5" is
// ignored rather than rounded into a round count.
func (c *AgentHookCycle) ParseCounter(label string) int {
	prefix := c.LabelPrefix()
	if !strings.HasPrefix(label, prefix) {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(label, prefix)))
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// CompletedRounds is the highest counter present. Taking the max rather than a
// sum keeps a leftover counter from a crashed cleanup harmless.
func (c *AgentHookCycle) CompletedRounds(labels []string) int {
	max := 0
	for _, l := range labels {
		if n := c.ParseCounter(l); n > max {
			max = n
		}
	}
	return max
}

// IsValid returns true if the action type is a recognized constant.
func (t AgentHookActionType) IsValid() bool {
	switch t {
	case AgentHookActionComment, AgentHookActionWriteDesign,
		AgentHookActionAddLabel, AgentHookActionRemoveLabel, AgentHookActionSetStatus,
		AgentHookActionClose, AgentHookActionCycle:
		return true
	}
	return false
}

// AgentHookCommentSource enumerates where a body-writing action draws its text
// from. Both comment and write_design use it: there is one notion of "the run's
// artifact", and a second source vocabulary could disagree with this one about
// what that is. The type keeps its original name — the wire value is what the
// contract publishes, and renaming the Go symbol would be churn with no effect
// on it.
type AgentHookCommentSource string

// AgentHookCommentSourceFinalReply takes the run's final assistant reply.
const AgentHookCommentSourceFinalReply AgentHookCommentSource = "final_reply"

// AgentHookAction is one step of a completion pipeline. Only the fields
// appropriate to Type may be set; Validate rejects the rest.
type AgentHookAction struct {
	Type   AgentHookActionType    `json:"type" yaml:"type"`
	Source AgentHookCommentSource `json:"source,omitempty" yaml:"source,omitempty"`
	Value  string                 `json:"value,omitempty" yaml:"value,omitempty"`

	// Reason explains a status transition. Required when Type == set_status and
	// Value is blocked, rejected on every other action type, and rejected on a
	// set_status moving to any other status — where nothing would consume it,
	// which is the inert-by-construction shape this validation exists to refuse.
	// Requiring it only for blocked can be widened later without breaking a
	// stored definition; the reverse cannot.
	//
	// It is its own field because Value already carries the status and Source
	// names an artifact, so neither is free. Cycle is the precedent for an
	// action-specific field; a lone scalar does not need a block of its own.
	//
	// The rule it enforces is that a blocked task must say why it is blocked.
	// loom checks that today only in `data update` (enforceBlockReason), and a
	// hook never goes through that path — so a supervisor could park a task in
	// blocked with no signal on it, and the board's needs-attention state is
	// literally "blocked AND has notes", so a bare blocked card sits until a
	// human happens to review it. The supervisor writes the reason into those
	// notes; see setTaskStatus.
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`

	// Cycle is required for Type == cycle and must be unset otherwise.
	Cycle *AgentHookCycle `json:"cycle,omitempty" yaml:"cycle,omitempty"`
}

// AgentHooks holds the supervisor-owned hook pipelines for an agent.
type AgentHooks struct {
	// OnComplete runs in slice order after a successful agent turn. Every
	// body-writing action (comment, write_design) must precede every stamping
	// action (add_label, remove_label, set_status, cycle) so a stamp can never
	// outrun the artifact it certifies (see Validate).
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
	// sawStamp covers every action that writes observable routing state — a
	// label or a status — not only add_label; see the write-before-stamp check
	// in validateHookAction.
	sawStamp := false
	sawClose := false
	sawCycle := false
	for i := range h.OnComplete {
		a := h.OnComplete[i]
		if !a.Type.IsValid() {
			return fmt.Errorf("hooks.on_complete[%d]: unknown action type %q (must be one of: comment, write_design, add_label, remove_label, set_status, close, cycle)", i, a.Type)
		}
		// Nothing may follow the close: every write in this pipeline targets the
		// task, and a terminal issue rejects further mutation.
		if sawClose {
			return fmt.Errorf("hooks.on_complete[%d]: %s action must not follow a close action", i, a.Type)
		}
		if a.Type == AgentHookActionCycle && sawCycle {
			return fmt.Errorf("hooks.on_complete[%d]: only one cycle action is allowed", i)
		}
		// cycle + close is self-defeating in BOTH of the cycle's branches, so it
		// is never what anyone meant: on a re-arm the close immediately closes
		// the task the cycle just handed back, killing the loop at round one;
		// on a ship it closes the task the ship label was supposed to route, so
		// nothing can claim it. Validate exists to make unsatisfiable pipelines
		// unrepresentable rather than to let them fail quietly at runtime.
		if a.Type == AgentHookActionClose && sawCycle {
			return fmt.Errorf("hooks.on_complete[%d]: close action must not be combined with a cycle action "+
				"(a cycle hands the task to the next stage; closing it makes that hand-off unclaimable)", i)
		}
		if err := validateHookAction(i, a, sawStamp); err != nil {
			return err
		}
		switch a.Type {
		// Both mutate the label set, so write-before-stamp binds both: a body
		// write may not follow either.
		case AgentHookActionAddLabel, AgentHookActionRemoveLabel:
			sawStamp = true
		case AgentHookActionSetStatus:
			// A status is observable routing state — the thing a predicate
			// selects on, and in loom the thing that decides whether the task
			// can be claimed at all — so it is a stamp, and binds
			// write-before-stamp exactly as add_label does.
			sawStamp = true
		case AgentHookActionCycle:
			// A cycle writes labels, so later body writes would violate
			// write-before-stamp exactly as they would after add_label.
			sawStamp = true
			sawCycle = true
		case AgentHookActionClose:
			sawClose = true
		}
	}
	return nil
}

// validateHookAction checks the fields appropriate to one action's type. Split
// out of Validate so the ordering rules above stay readable as the action
// vocabulary grows; the two concerns are independent.
func validateHookAction(i int, a AgentHookAction, sawStamp bool) error {
	if a.Cycle != nil && a.Type != AgentHookActionCycle {
		return fmt.Errorf("hooks.on_complete[%d]: %s action must not set cycle", i, a.Type)
	}
	// A reason justifies a status transition and nothing else. close is
	// deliberately included: it stays argument-free, and its own endpoint
	// carries the close reason.
	if a.Reason != "" && a.Type != AgentHookActionSetStatus {
		return fmt.Errorf("hooks.on_complete[%d]: %s action must not set reason", i, a.Type)
	}
	switch a.Type {
	// write_design shares comment's arm outright, matching fleet-db: both write
	// a body, both draw it from the same run artifact, and both are what a later
	// stamp certifies. Sharing is the point — a source one accepted and the
	// other refused would be a difference no caller could predict.
	case AgentHookActionComment, AgentHookActionWriteDesign:
		if a.Source == "" {
			return fmt.Errorf("hooks.on_complete[%d]: %s action requires source", i, a.Type)
		}
		if a.Source != AgentHookCommentSourceFinalReply {
			return fmt.Errorf("hooks.on_complete[%d]: %s source %q must be final_reply", i, a.Type, a.Source)
		}
		if a.Value != "" {
			return fmt.Errorf("hooks.on_complete[%d]: %s action must not set value", i, a.Type)
		}
		// Write-before-stamp: the artifact must land before the stamp that
		// certifies it, so a stamp is never observable without its body. The
		// message names add_label because that is the archetype of a stamp, not
		// because it is the only one that trips this (remove_label, set_status
		// and cycle set sawStamp too); the wording is fleet-db's verbatim so a
		// pipeline refused by one repo is refused by the other in the same
		// words.
		if sawStamp {
			return fmt.Errorf("hooks.on_complete[%d]: %s action must not follow an add_label action", i, a.Type)
		}
	// remove_label shares add_label's arm outright, matching fleet-db. Sharing
	// it is the point: a value the two disagreed about would be storable
	// through one action and refused by the other for no reason a caller could
	// predict. The value caps fleet-db layers on top (256 bytes, no commas or
	// semicolons — labels are stored comma-separated) are enforced there at
	// write time and bind both actions identically, so nothing here can accept
	// a value for one that the server would take only for the other.
	case AgentHookActionAddLabel, AgentHookActionRemoveLabel:
		if strings.TrimSpace(a.Value) == "" {
			return fmt.Errorf("hooks.on_complete[%d]: %s action requires a non-blank value", i, a.Type)
		}
		if a.Source != "" {
			return fmt.Errorf("hooks.on_complete[%d]: %s action must not set source", i, a.Type)
		}
	case AgentHookActionSetStatus:
		return validateSetStatusAction(i, a)
	case AgentHookActionClose:
		if a.Value != "" {
			return fmt.Errorf("hooks.on_complete[%d]: close action must not set value", i)
		}
		if a.Source != "" {
			return fmt.Errorf("hooks.on_complete[%d]: close action must not set source", i)
		}
	case AgentHookActionCycle:
		return validateCycleAction(i, a)
	}
	return nil
}

// validateSetStatusAction checks the one action whose legal values are owned by
// the server rather than by this file. Split out for the same reason
// validateCycleAction is: the ordering rules in Validate stay readable only if
// per-action field checks live beside the action they belong to.
func validateSetStatusAction(i int, a AgentHookAction) error {
	if strings.TrimSpace(a.Value) == "" {
		return fmt.Errorf("hooks.on_complete[%d]: set_status action requires a non-blank value", i)
	}
	// The legal set is the server's own PATCH contract, run through loom's copy
	// of the same function fleet-db runs — not a third list that could drift
	// from either. That contract is also what keeps this action and close
	// non-overlapping: it refuses closed and points at the close endpoint, which
	// the close action already owns. Note the value is checked untrimmed: " open"
	// is not a status any endpoint would accept either, and silently repairing
	// it here would hide a typo the server would still reject.
	if err := types.ValidateSettableStatus(types.Status(a.Value)); err != nil {
		return fmt.Errorf("hooks.on_complete[%d]: set_status value %q is not a settable status: %w", i, a.Value, err)
	}
	if a.Source != "" {
		return fmt.Errorf("hooks.on_complete[%d]: set_status action must not set source", i)
	}
	// A blocked task that does not say why is a dead card: it sits until a human
	// reviews it. loom enforces this only in `data update` today, and a hook does
	// not go through that path — so it holds here or nowhere.
	if types.Status(a.Value) == types.StatusBlocked && strings.TrimSpace(a.Reason) == "" {
		return fmt.Errorf("hooks.on_complete[%d]: set_status action to blocked requires a non-blank reason", i)
	}
	// ...and on any other status a reason is inert: nothing reads it. Refusing it
	// keeps the option of widening the rule later, which accepting-and-dropping
	// it would not.
	if a.Reason != "" && types.Status(a.Value) != types.StatusBlocked {
		return fmt.Errorf("hooks.on_complete[%d]: set_status action must not set reason for status %q (only blocked carries one)", i, a.Value)
	}
	return nil
}

func validateCycleAction(i int, a AgentHookAction) error {
	if a.Value != "" || a.Source != "" {
		return fmt.Errorf("hooks.on_complete[%d]: cycle action must not set value or source", i)
	}
	if a.Cycle == nil {
		return fmt.Errorf("hooks.on_complete[%d]: cycle action requires a cycle block", i)
	}
	if a.Cycle.Threshold < 1 {
		return fmt.Errorf("hooks.on_complete[%d]: cycle threshold must be >= 1, got %d", i, a.Cycle.Threshold)
	}
	if strings.TrimSpace(a.Cycle.RearmLabel) == "" {
		return fmt.Errorf("hooks.on_complete[%d]: cycle action requires a non-blank rearm_label", i)
	}
	if strings.TrimSpace(a.Cycle.ShipLabel) == "" {
		return fmt.Errorf("hooks.on_complete[%d]: cycle action requires a non-blank ship_label", i)
	}
	// The loop would re-arm the very label it ships with, so it could never end.
	if a.Cycle.RearmLabel == a.Cycle.ShipLabel {
		return fmt.Errorf("hooks.on_complete[%d]: cycle rearm_label and ship_label must differ", i)
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
