package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func removeLabel(v string) AgentHookAction {
	return AgentHookAction{Type: AgentHookActionRemoveLabel, Value: v}
}

// The two repos each carry their own copy of this enum and validate
// independently; fleet-db rejects an unknown type on write. If they drift, a
// pipeline stored by one is refused by the other.
func TestAgentHookActionType_RemoveLabelIsRecognized(t *testing.T) {
	if !AgentHookActionRemoveLabel.IsValid() {
		t.Error("remove_label must be recognized or the supervisor cannot execute a stored pipeline")
	}
	if AgentHookActionRemoveLabel != "remove_label" {
		t.Errorf("wire value = %q, want remove_label to match fleet-db's models.AgentHookActionRemoveLabel",
			AgentHookActionRemoveLabel)
	}
	if AgentHookActionType("unset_label").IsValid() {
		t.Error("the action vocabulary must stay closed")
	}
}

// remove_label shares add_label's validation arm outright, so the two must
// reach the same verdict on every shape. A value storable through one and
// refused by the other is a difference no caller could predict — and fleet-db
// deliberately made them share one arm, so a divergence here would also mean
// loom accepting a pipeline the server rejects on write.
func TestAgentHooks_Validate_RemoveLabelMatchesAddLabel(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(a *AgentHookAction)
		wantErr string
	}{
		{name: "plain label"},
		{name: "blank value", mutate: func(a *AgentHookAction) { a.Value = "" }, wantErr: "requires a non-blank value"},
		{
			name:    "whitespace-only value",
			mutate:  func(a *AgentHookAction) { a.Value = "  \t " },
			wantErr: "requires a non-blank value",
		},
		{
			name:    "source set",
			mutate:  func(a *AgentHookAction) { a.Source = AgentHookCommentSourceFinalReply },
			wantErr: "must not set source",
		},
		{
			name:    "cycle block set",
			mutate:  func(a *AgentHookAction) { a.Cycle = &AgentHookCycle{Threshold: 2, RearmLabel: "a", ShipLabel: "b"} },
			wantErr: "must not set cycle",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			add, remove := label("needs-review"), removeLabel("needs-review")
			if tt.mutate != nil {
				tt.mutate(&add)
				tt.mutate(&remove)
			}
			addErr := (&AgentHooks{OnComplete: []AgentHookAction{add}}).Validate()
			removeErr := (&AgentHooks{OnComplete: []AgentHookAction{remove}}).Validate()

			if (addErr == nil) != (removeErr == nil) {
				t.Fatalf("the two arms disagree: add_label = %v, remove_label = %v", addErr, removeErr)
			}
			if tt.wantErr == "" {
				if removeErr != nil {
					t.Fatalf("Validate() = %v, want nil", removeErr)
				}
				return
			}
			if !strings.Contains(removeErr.Error(), tt.wantErr) {
				t.Errorf("Validate() = %v, want it to contain %q", removeErr, tt.wantErr)
			}
			// The message must name the action that was actually wrong, or an
			// operator debugging a rejected definition looks at the wrong step.
			if !strings.Contains(removeErr.Error(), "remove_label") {
				t.Errorf("Validate() = %v, want it to name remove_label", removeErr)
			}
		})
	}
}

// remove_label mutates the label set, so write-before-stamp binds it exactly as
// it binds add_label: a comment may never follow one, or a routing change
// becomes observable before the artifact that justifies it.
func TestAgentHooks_Validate_RemoveLabelOrdering(t *testing.T) {
	closeAct := AgentHookAction{Type: AgentHookActionClose}

	tests := []struct {
		name    string
		actions []AgentHookAction
		wantErr string
	}{
		{name: "remove alone", actions: []AgentHookAction{removeLabel("needs-review")}},
		{
			name:    "comment, remove, stamp",
			actions: []AgentHookAction{comment(), removeLabel("needs-review"), label("reviewed")},
		},
		{
			// The reverse is legal too: which of the two label writes goes
			// first is the caller's routing decision, not a model invariant.
			name:    "comment, stamp, remove",
			actions: []AgentHookAction{comment(), label("reviewed"), removeLabel("needs-review")},
		},
		{name: "comment, remove, close", actions: []AgentHookAction{comment(), removeLabel("needs-review"), closeAct}},
		{
			name:    "comment after remove_label",
			actions: []AgentHookAction{removeLabel("needs-review"), comment()},
			wantErr: "must not follow an add_label",
		},
		{
			name:    "comment after a remove later in a longer pipeline",
			actions: []AgentHookAction{comment(), removeLabel("needs-review"), comment()},
			wantErr: "must not follow an add_label",
		},
		{
			name:    "remove_label after close",
			actions: []AgentHookAction{closeAct, removeLabel("needs-review")},
			wantErr: "must not follow a close action",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&AgentHooks{OnComplete: tt.actions}).Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// The unknown-type error doubles as the published vocabulary, so it has to list
// the action or an operator who mistypes it is told the wrong set of options.
func TestAgentHooks_Validate_UnknownTypeListsRemoveLabel(t *testing.T) {
	err := (&AgentHooks{OnComplete: []AgentHookAction{{Type: "unset_label", Value: "x"}}}).Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an unknown-type error")
	}
	if !strings.Contains(err.Error(), "remove_label") {
		t.Errorf("Validate() = %v, want the vocabulary to advertise remove_label", err)
	}
}

// The wire shape is fleet-db's: type + value, no source, no cycle. A stored
// definition is written by one repo and executed by the other, so the keys have
// to match exactly.
func TestAgentHookAction_RemoveLabelWireShape(t *testing.T) {
	const wire = `{"type":"remove_label","value":"needs-review"}`

	data, err := json.Marshal(removeLabel("needs-review"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != wire {
		t.Errorf("wire shape = %s, want %s", data, wire)
	}

	var back AgentHookAction
	if err := json.Unmarshal([]byte(wire), &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != removeLabel("needs-review") {
		t.Errorf("round trip = %+v, want the remove_label action", back)
	}
}
