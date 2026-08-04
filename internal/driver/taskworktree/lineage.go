package taskworktree

import (
	"encoding/json"
	"strings"
)

// Lineage is the per-task stack-lineage carrier stored in a TaskRun input.
type Lineage struct {
	StackID      string `json:"stackId,omitempty"`
	BaseRef      string `json:"baseRef,omitempty"`
	OutputBranch string `json:"outputBranch,omitempty"`
}

// Empty reports whether the carrier holds no lineage at all.
func (lineage Lineage) Empty() bool {
	return strings.TrimSpace(lineage.StackID) == "" &&
		strings.TrimSpace(lineage.BaseRef) == "" &&
		strings.TrimSpace(lineage.OutputBranch) == ""
}

type lineageEnvelope struct {
	Lineage *Lineage `json:"lineage,omitempty"`
}

// WithLineage merges lineage into the namespaced key of an existing TaskRun
// input, preserving every other key.
func WithLineage(input json.RawMessage, lineage Lineage) (json.RawMessage, error) {
	if lineage.Empty() {
		return input, nil
	}
	object := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(input))) > 0 {
		if err := json.Unmarshal(input, &object); err != nil {
			return input, nil
		}
	}
	encoded, err := json.Marshal(lineage)
	if err != nil {
		return nil, err
	}
	object["lineage"] = encoded
	merged, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return merged, nil
}

// LineageFromInput extracts the lineage carrier from a TaskRun input.
func LineageFromInput(input json.RawMessage) (Lineage, bool) {
	if len(strings.TrimSpace(string(input))) == 0 {
		return Lineage{}, false
	}
	var envelope lineageEnvelope
	if err := json.Unmarshal(input, &envelope); err != nil || envelope.Lineage == nil {
		return Lineage{}, false
	}
	if envelope.Lineage.Empty() {
		return Lineage{}, false
	}
	return *envelope.Lineage, true
}
