package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/types"
)

// setStatus returns a set_status action with no reason.
func setStatus(v string) AgentHookAction {
	return AgentHookAction{Type: AgentHookActionSetStatus, Value: v}
}

// blocked returns the only shape a blocked transition may take: the status plus
// the reason it is blocked.
func blocked(reason string) AgentHookAction {
	return AgentHookAction{
		Type:   AgentHookActionSetStatus,
		Value:  string(types.StatusBlocked),
		Reason: reason,
	}
}

// writeDesign returns the only currently valid write_design action.
func writeDesign() AgentHookAction {
	return AgentHookAction{Type: AgentHookActionWriteDesign, Source: AgentHookCommentSourceFinalReply}
}

// The two repos each carry their own copy of this enum and validate
// independently; fleet-db rejects an unknown type on write. If they drift, a
// pipeline stored by one is refused by the other.
func TestAgentHookActionType_StatusAndDesignAreRecognized(t *testing.T) {
	for _, tt := range []struct {
		action AgentHookActionType
		wire   string
	}{
		{AgentHookActionSetStatus, "set_status"},
		{AgentHookActionWriteDesign, "write_design"},
	} {
		if !tt.action.IsValid() {
			t.Errorf("%s must be recognized or the supervisor cannot execute a stored pipeline", tt.wire)
		}
		if string(tt.action) != tt.wire {
			t.Errorf("wire value = %q, want %q to match fleet-db's models constant", tt.action, tt.wire)
		}
	}
	// The vocabulary stays closed around the new members.
	for _, bogus := range []AgentHookActionType{"set_state", "SetStatus", "set-status", "status", "design"} {
		if bogus.IsValid() {
			t.Errorf("%q must not be a recognized action type", bogus)
		}
	}
}

// set_status lets a stage route by status instead of only by label. The legal
// set is the server's own status-PATCH contract, so loom must refuse here
// exactly what fleet-db refuses — a status storable in a hook and rejected by
// the write it exists to perform would fail on every single run.
func TestAgentHooks_Validate_SetStatusAction(t *testing.T) {
	closeAct := AgentHookAction{Type: AgentHookActionClose}

	tests := []struct {
		name    string
		actions []AgentHookAction
		wantErr string
	}{
		// Every status the PATCH contract accepts.
		{name: "open", actions: []AgentHookAction{setStatus("open")}},
		{name: "deferred", actions: []AgentHookAction{setStatus("deferred")}},
		{name: "review", actions: []AgentHookAction{setStatus("review")}},
		{name: "blocked with a reason", actions: []AgentHookAction{blocked("upstream API decision pending")}},
		{
			// The pipeline this exists for: publish the artifact, consume the
			// routing token, stamp the hand-off, then open the gate.
			name: "the whole builder order",
			actions: []AgentHookAction{
				writeDesign(), comment(), removeLabel("needs-plan"), label("planned"), setStatus("open"),
			},
		},
		{name: "set_status then close", actions: []AgentHookAction{setStatus("review"), closeAct}},
		// Nothing bounds a pipeline to one status write: the last one wins at
		// execution, the same way two add_labels both apply.
		{name: "two set_status actions", actions: []AgentHookAction{setStatus("review"), setStatus("open")}},

		// The statuses the PATCH contract refuses, each carrying the message a
		// client is told to act on. These two are also why set_status and close
		// cannot express each other.
		{
			name:    "closed points at the close endpoint",
			actions: []AgentHookAction{setStatus("closed")},
			wantErr: `hooks.on_complete[0]: set_status value "closed" is not a settable status: status closed must use close endpoint`,
		},
		{
			name:    "in_progress points at the claim endpoint",
			actions: []AgentHookAction{setStatus("in_progress")},
			wantErr: `hooks.on_complete[0]: set_status value "in_progress" is not a settable status: status in_progress must use claim endpoint`,
		},
		{
			name:    "tombstone is system-managed",
			actions: []AgentHookAction{setStatus("tombstone")},
			wantErr: "status tombstone is system-managed",
		},
		{
			name:    "pinned is system-managed",
			actions: []AgentHookAction{setStatus("pinned")},
			wantErr: "status pinned is system-managed",
		},
		{
			name:    "hooked is system-managed",
			actions: []AgentHookAction{setStatus("hooked")},
			wantErr: "status hooked is system-managed",
		},
		{
			name:    "unknown status",
			actions: []AgentHookAction{setStatus("waiting")},
			wantErr: `hooks.on_complete[0]: set_status value "waiting" is not a settable status: invalid status "waiting"`,
		},
		{
			name:    "blank value",
			actions: []AgentHookAction{{Type: AgentHookActionSetStatus}},
			wantErr: "hooks.on_complete[0]: set_status action requires a non-blank value",
		},
		{
			name:    "whitespace value",
			actions: []AgentHookAction{setStatus("  \t\n ")},
			wantErr: "hooks.on_complete[0]: set_status action requires a non-blank value",
		},
		{
			// Not trimmed before the contract check: " open" is not a status any
			// endpoint would accept either, and repairing it here would hide a
			// typo the server still rejects.
			name:    "value with surrounding whitespace",
			actions: []AgentHookAction{setStatus(" open")},
			wantErr: `set_status value " open" is not a settable status`,
		},

		// The blocked-reason rule: a blocked card with no signal on it sits until
		// a human reviews it, and `data update`'s client-side rule that would
		// have caught this never runs for a hook.
		{
			name:    "blocked without a reason",
			actions: []AgentHookAction{setStatus("blocked")},
			wantErr: "hooks.on_complete[0]: set_status action to blocked requires a non-blank reason",
		},
		{
			name:    "blocked with a whitespace reason",
			actions: []AgentHookAction{blocked("  \t ")},
			wantErr: "hooks.on_complete[0]: set_status action to blocked requires a non-blank reason",
		},
		// ...and a reason anywhere else is inert, so it is refused rather than
		// stored and silently dropped.
		{
			name:    "reason on a non-blocked status",
			actions: []AgentHookAction{{Type: AgentHookActionSetStatus, Value: "review", Reason: "looks good"}},
			wantErr: `hooks.on_complete[0]: set_status action must not set reason for status "review" (only blocked carries one)`,
		},
		{
			name:    "reason on an add_label",
			actions: []AgentHookAction{{Type: AgentHookActionAddLabel, Value: "reviewed", Reason: "why"}},
			wantErr: "hooks.on_complete[0]: add_label action must not set reason",
		},
		{
			name:    "reason on a remove_label",
			actions: []AgentHookAction{{Type: AgentHookActionRemoveLabel, Value: "wip", Reason: "why"}},
			wantErr: "hooks.on_complete[0]: remove_label action must not set reason",
		},
		{
			name:    "reason on a comment",
			actions: []AgentHookAction{{Type: AgentHookActionComment, Source: AgentHookCommentSourceFinalReply, Reason: "why"}},
			wantErr: "hooks.on_complete[0]: comment action must not set reason",
		},
		{
			// close stays argument-free: its own endpoint carries the close reason.
			name:    "reason on a close",
			actions: []AgentHookAction{{Type: AgentHookActionClose, Reason: "done"}},
			wantErr: "hooks.on_complete[0]: close action must not set reason",
		},

		{
			name:    "source is rejected",
			actions: []AgentHookAction{{Type: AgentHookActionSetStatus, Source: AgentHookCommentSourceFinalReply, Value: "review"}},
			wantErr: "hooks.on_complete[0]: set_status action must not set source",
		},
		{
			name: "cycle block is rejected",
			actions: []AgentHookAction{{
				Type:  AgentHookActionSetStatus,
				Value: "review",
				Cycle: &AgentHookCycle{Threshold: 2, RearmLabel: "a", ShipLabel: "b"},
			}},
			wantErr: "hooks.on_complete[0]: set_status action must not set cycle",
		},

		// Ordering: a status is observable routing state, so set_status is a
		// stamp and write-before-stamp binds it exactly as it binds add_label.
		{
			name:    "comment after set_status",
			actions: []AgentHookAction{setStatus("review"), comment()},
			wantErr: "hooks.on_complete[1]: comment action must not follow an add_label action",
		},
		{
			name:    "write_design after set_status",
			actions: []AgentHookAction{setStatus("review"), writeDesign()},
			wantErr: "hooks.on_complete[1]: write_design action must not follow an add_label action",
		},
		// ...and close stays terminal.
		{
			name:    "set_status after close",
			actions: []AgentHookAction{closeAct, setStatus("review")},
			wantErr: "hooks.on_complete[1]: set_status action must not follow a close action",
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

// The hook and the update endpoint must agree on every status, in both
// directions. They share ValidateSettableStatus precisely so they cannot drift;
// this pins that they still do — and, because it walks BuiltinStatuses, a status
// added to the vocabulary later enters this test on its own.
func TestAgentHooks_Validate_SetStatusMatchesPatchContract(t *testing.T) {
	for _, s := range append(types.BuiltinStatuses(), types.Status("waiting"), types.Status("Open")) {
		action := setStatus(string(s))
		// blocked is the one status that needs more than a legal value, so give
		// it the reason and compare only the value rule.
		if s == types.StatusBlocked {
			action = blocked("upstream API decision pending")
		}
		hookErr := (&AgentHooks{OnComplete: []AgentHookAction{action}}).Validate()
		patchErr := types.ValidateSettableStatus(s)
		if (hookErr == nil) != (patchErr == nil) {
			t.Errorf("status %q: hook err = %v, PATCH contract err = %v; the two must agree",
				s, hookErr, patchErr)
		}
	}
}

// write_design exists so a read-only role can produce a design at all. It draws
// its body from the same place a comment does, so it takes comment's rules
// outright — and, like a comment, it must land before anything stamps the task.
func TestAgentHooks_Validate_WriteDesignAction(t *testing.T) {
	closeAct := AgentHookAction{Type: AgentHookActionClose}

	tests := []struct {
		name    string
		actions []AgentHookAction
		wantErr string
	}{
		{name: "write_design alone", actions: []AgentHookAction{writeDesign()}},
		{
			name:    "the pipeline this exists for: a read-only planner records its design and hands off",
			actions: []AgentHookAction{writeDesign(), label("planned")},
		},
		{name: "the order the builder emits", actions: []AgentHookAction{writeDesign(), comment()}},
		{name: "the reverse is legal too", actions: []AgentHookAction{comment(), writeDesign()}},
		{name: "write_design then close", actions: []AgentHookAction{writeDesign(), closeAct}},
		{name: "write_design then set_status", actions: []AgentHookAction{writeDesign(), setStatus("review")}},

		// The source mechanism is comment's, reused rather than duplicated.
		{
			name:    "missing source",
			actions: []AgentHookAction{{Type: AgentHookActionWriteDesign}},
			wantErr: "hooks.on_complete[0]: write_design action requires source",
		},
		{
			name:    "wrong source",
			actions: []AgentHookAction{{Type: AgentHookActionWriteDesign, Source: "first_reply"}},
			wantErr: `hooks.on_complete[0]: write_design source "first_reply" must be final_reply`,
		},
		{
			// The design body is resolved at run time from the source, so a value
			// on the action would be text nothing reads.
			name:    "value is rejected",
			actions: []AgentHookAction{{Type: AgentHookActionWriteDesign, Source: AgentHookCommentSourceFinalReply, Value: "## Design"}},
			wantErr: "hooks.on_complete[0]: write_design action must not set value",
		},
		{
			name:    "reason is rejected",
			actions: []AgentHookAction{{Type: AgentHookActionWriteDesign, Source: AgentHookCommentSourceFinalReply, Reason: "because"}},
			wantErr: "hooks.on_complete[0]: write_design action must not set reason",
		},
		{
			name: "cycle block is rejected",
			actions: []AgentHookAction{{
				Type:   AgentHookActionWriteDesign,
				Source: AgentHookCommentSourceFinalReply,
				Cycle:  &AgentHookCycle{Threshold: 2, RearmLabel: "a", ShipLabel: "b"},
			}},
			wantErr: "hooks.on_complete[0]: write_design action must not set cycle",
		},

		// Ordering: it writes a body, so write-before-stamp treats it exactly as
		// it treats a comment. The message names add_label because that is the
		// archetype of a stamp, whichever stamp actually preceded it.
		{
			name:    "write_design after add_label",
			actions: []AgentHookAction{label("planned"), writeDesign()},
			wantErr: "hooks.on_complete[1]: write_design action must not follow an add_label action",
		},
		{
			name:    "write_design after remove_label",
			actions: []AgentHookAction{removeLabel("needs-plan"), writeDesign()},
			wantErr: "hooks.on_complete[1]: write_design action must not follow an add_label action",
		},
		{
			name: "write_design after cycle",
			actions: []AgentHookAction{
				{Type: AgentHookActionCycle, Cycle: &AgentHookCycle{Threshold: 2, RearmLabel: "a", ShipLabel: "b"}},
				writeDesign(),
			},
			wantErr: "hooks.on_complete[1]: write_design action must not follow an add_label action",
		},
		// ...and close stays terminal.
		{
			name:    "write_design after close",
			actions: []AgentHookAction{closeAct, writeDesign()},
			wantErr: "hooks.on_complete[1]: write_design action must not follow a close action",
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

// write_design shares comment's validation arm outright, so the two must reach
// the same verdict on every shape. fleet-db deliberately made them share one
// arm, so a divergence here would also mean loom accepting a pipeline the server
// rejects on write.
func TestAgentHooks_Validate_WriteDesignMatchesComment(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(a *AgentHookAction)
		wantErr string
	}{
		{name: "plain body write"},
		{name: "missing source", mutate: func(a *AgentHookAction) { a.Source = "" }, wantErr: "requires source"},
		{
			name:    "wrong source",
			mutate:  func(a *AgentHookAction) { a.Source = "first_reply" },
			wantErr: "must be final_reply",
		},
		{name: "value set", mutate: func(a *AgentHookAction) { a.Value = "x" }, wantErr: "must not set value"},
		{name: "reason set", mutate: func(a *AgentHookAction) { a.Reason = "x" }, wantErr: "must not set reason"},
		{
			name:    "cycle block set",
			mutate:  func(a *AgentHookAction) { a.Cycle = &AgentHookCycle{Threshold: 2, RearmLabel: "a", ShipLabel: "b"} },
			wantErr: "must not set cycle",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			com, design := comment(), writeDesign()
			if tt.mutate != nil {
				tt.mutate(&com)
				tt.mutate(&design)
			}
			comErr := (&AgentHooks{OnComplete: []AgentHookAction{com}}).Validate()
			designErr := (&AgentHooks{OnComplete: []AgentHookAction{design}}).Validate()

			if (comErr == nil) != (designErr == nil) {
				t.Fatalf("the two arms disagree: comment = %v, write_design = %v", comErr, designErr)
			}
			if tt.wantErr == "" {
				if designErr != nil {
					t.Fatalf("Validate() = %v, want nil", designErr)
				}
				return
			}
			if !strings.Contains(designErr.Error(), tt.wantErr) {
				t.Errorf("Validate() = %v, want it to contain %q", designErr, tt.wantErr)
			}
			// The message must name the action that was actually wrong, or an
			// operator debugging a rejected definition looks at the wrong step.
			if !strings.Contains(designErr.Error(), "write_design") {
				t.Errorf("Validate() = %v, want it to name write_design", designErr)
			}
		})
	}
}

// The unknown-type error doubles as the published vocabulary, so it has to list
// the new actions or an operator who mistypes one is told the wrong set.
func TestAgentHooks_Validate_UnknownTypeListsStatusAndDesign(t *testing.T) {
	err := (&AgentHooks{OnComplete: []AgentHookAction{{Type: "status", Value: "review"}}}).Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an unknown-type error")
	}
	for _, want := range []string{"comment", "write_design", "add_label", "remove_label", "set_status", "close", "cycle"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() = %v, want the vocabulary to advertise %q", err, want)
		}
	}
}

// The wire shape is fleet-db's. A stored definition is written by one repo and
// executed by the other, so the keys have to match exactly — including reason
// being omitempty, so a status that carries none stays a two-key object rather
// than shipping an empty field to every consumer.
func TestAgentHookAction_StatusAndDesignWireShape(t *testing.T) {
	tests := []struct {
		name   string
		action AgentHookAction
		wire   string
	}{
		{name: "write_design", action: writeDesign(), wire: `{"type":"write_design","source":"final_reply"}`},
		{name: "set_status", action: setStatus("review"), wire: `{"type":"set_status","value":"review"}`},
		{
			name:   "blocked set_status",
			action: blocked("upstream API decision pending"),
			wire:   `{"type":"set_status","value":"blocked","reason":"upstream API decision pending"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.action)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(data) != tt.wire {
				t.Errorf("wire shape = %s, want %s", data, tt.wire)
			}
			var back AgentHookAction
			if err := json.Unmarshal([]byte(tt.wire), &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if back != tt.action {
				t.Errorf("round trip = %+v, want %+v", back, tt.action)
			}
		})
	}
}

// Equal backs the "did the pipeline change?" check on every agent update, so two
// actions differing only in reason must not compare equal — otherwise a
// corrected blocked reason would be silently dropped.
func TestAgentHooks_Equal_DistinguishesReason(t *testing.T) {
	a := &AgentHooks{OnComplete: []AgentHookAction{blocked("upstream API decision pending")}}
	b := &AgentHooks{OnComplete: []AgentHookAction{blocked("waiting on the schema review")}}
	if a.Equal(b) {
		t.Errorf("hooks differing only in reason compared equal: %+v vs %+v", a, b)
	}
	if !a.Equal(&AgentHooks{OnComplete: []AgentHookAction{blocked("upstream API decision pending")}}) {
		t.Error("identical hooks compared unequal")
	}
}
