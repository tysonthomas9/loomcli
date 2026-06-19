package driver

import (
	"encoding/json"
	"strings"
)

// TaskLineage is the per-task stack-lineage carrier. It rides inside the
// existing TaskExecRequest.Input payload under the namespaced "lineage" key so
// it travels verbatim to a runner — including a daytona sandbox that never
// reads the host stackstore — alongside whatever task payload already exists
// (a review diff+rubric, repo selectors, etc.) without colliding with it.
//
// Wire shape (camelCase, matching the rest of the driver wire):
//
//		{"...existing task input...", "lineage": {
//		    "stackId": "epic:E", "baseRef": "loom/stack/epic:E/A", "outputBranch": "loom/stack/epic:E/B"}}
//
//	  - StackID is the deterministic stack the task belongs to (epic:<id>).
//	  - BaseRef is the git branch the task's worktree must be cut from — its
//	    predecessor's output branch, or the stack root base for a chain root.
//	  - OutputBranch is the canonical branch the runner pushes its work to.
//
// All fields are advisory carriers: the host-local resolver (local runner) can
// recompute BaseRef from the stackstore, but daytona has no store, so the
// carrier is the only channel for it there (Stage 5).
type TaskLineage struct {
	StackID      string `json:"stackId,omitempty"`
	BaseRef      string `json:"baseRef,omitempty"`
	OutputBranch string `json:"outputBranch,omitempty"`
}

// Empty reports whether the carrier holds no lineage at all (every field blank).
func (l TaskLineage) Empty() bool {
	return strings.TrimSpace(l.StackID) == "" &&
		strings.TrimSpace(l.BaseRef) == "" &&
		strings.TrimSpace(l.OutputBranch) == ""
}

// lineageEnvelope is the carrier's slot inside the Input object. Kept separate
// from TaskLineage so encode/decode never disturbs sibling task-payload keys.
type lineageEnvelope struct {
	Lineage *TaskLineage `json:"lineage,omitempty"`
}

// WithLineage merges lin into the "lineage" key of an existing Input payload,
// preserving every other key already present. A nil/empty input becomes a fresh
// object carrying only the lineage. An empty lin returns the input unchanged so
// non-stacked tasks keep a byte-identical payload.
func WithLineage(input json.RawMessage, lin TaskLineage) (json.RawMessage, error) {
	if lin.Empty() {
		return input, nil
	}
	obj := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(input))) > 0 {
		if err := json.Unmarshal(input, &obj); err != nil {
			// Input is not a JSON object (or is invalid). We only ever attach
			// lineage to object-shaped task inputs; leave anything else as-is
			// rather than corrupting it.
			return input, nil
		}
	}
	encoded, err := json.Marshal(lin)
	if err != nil {
		return nil, err
	}
	obj["lineage"] = encoded
	merged, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return merged, nil
}

// LineageFromInput extracts the lineage carrier from an Input payload. ok=false
// means no lineage key was present (or the input was not a JSON object), in
// which case the caller falls back to its pre-stacking behavior.
func LineageFromInput(input json.RawMessage) (TaskLineage, bool) {
	if len(strings.TrimSpace(string(input))) == 0 {
		return TaskLineage{}, false
	}
	var env lineageEnvelope
	if err := json.Unmarshal(input, &env); err != nil || env.Lineage == nil {
		return TaskLineage{}, false
	}
	if env.Lineage.Empty() {
		return TaskLineage{}, false
	}
	return *env.Lineage, true
}
