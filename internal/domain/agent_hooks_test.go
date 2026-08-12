package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func comment() AgentHookAction {
	return AgentHookAction{Type: AgentHookActionComment, Source: AgentHookCommentSourceFinalReply}
}

func label(v string) AgentHookAction {
	return AgentHookAction{Type: AgentHookActionAddLabel, Value: v}
}

func TestAgentHookActionType_IsValid(t *testing.T) {
	for _, valid := range []AgentHookActionType{AgentHookActionComment, AgentHookActionAddLabel} {
		if !valid.IsValid() {
			t.Errorf("%q should be valid", valid)
		}
	}
	for _, invalid := range []AgentHookActionType{"", "run_command", "webhook", "Comment"} {
		if invalid.IsValid() {
			t.Errorf("%q should not be valid", invalid)
		}
	}
}

func TestAgentHooks_Validate(t *testing.T) {
	tests := []struct {
		name    string
		hooks   *AgentHooks
		wantErr string // substring; empty means the pipeline must be accepted
	}{
		{name: "nil hooks", hooks: nil},
		{name: "empty pipeline", hooks: &AgentHooks{}},
		{name: "comment only", hooks: &AgentHooks{OnComplete: []AgentHookAction{comment()}}},
		{name: "label only", hooks: &AgentHooks{OnComplete: []AgentHookAction{label("done")}}},
		{
			name:  "comment then several labels",
			hooks: &AgentHooks{OnComplete: []AgentHookAction{comment(), label("a"), label("b")}},
		},
		{
			name:    "unknown action type",
			hooks:   &AgentHooks{OnComplete: []AgentHookAction{{Type: "run_command", Value: "rm -rf /"}}},
			wantErr: "unknown action type",
		},
		{
			name:    "empty action type",
			hooks:   &AgentHooks{OnComplete: []AgentHookAction{{}}},
			wantErr: "unknown action type",
		},
		{
			name:    "comment missing source",
			hooks:   &AgentHooks{OnComplete: []AgentHookAction{{Type: AgentHookActionComment}}},
			wantErr: "requires source",
		},
		{
			name:    "comment with a foreign source",
			hooks:   &AgentHooks{OnComplete: []AgentHookAction{{Type: AgentHookActionComment, Source: "http://evil"}}},
			wantErr: "must be final_reply",
		},
		{
			name: "comment with a value",
			hooks: &AgentHooks{OnComplete: []AgentHookAction{
				{Type: AgentHookActionComment, Source: AgentHookCommentSourceFinalReply, Value: "x"},
			}},
			wantErr: "must not set value",
		},
		{
			name:    "blank label",
			hooks:   &AgentHooks{OnComplete: []AgentHookAction{label("")}},
			wantErr: "non-blank value",
		},
		{
			name:    "whitespace-only label",
			hooks:   &AgentHooks{OnComplete: []AgentHookAction{label("  \t ")}},
			wantErr: "non-blank value",
		},
		{
			name: "add_label with a source",
			hooks: &AgentHooks{OnComplete: []AgentHookAction{
				{Type: AgentHookActionAddLabel, Value: "done", Source: AgentHookCommentSourceFinalReply},
			}},
			wantErr: "must not set source",
		},
		{
			// The write-before-stamp invariant: a label must never be able to
			// precede the artifact it certifies.
			name:    "comment after add_label",
			hooks:   &AgentHooks{OnComplete: []AgentHookAction{label("done"), comment()}},
			wantErr: "must not follow an add_label",
		},
		{
			name:    "comment after a label later in a longer pipeline",
			hooks:   &AgentHooks{OnComplete: []AgentHookAction{comment(), label("a"), comment()}},
			wantErr: "must not follow an add_label",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.hooks.Validate()
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

func TestAgentHooks_ValidateReportsIndex(t *testing.T) {
	err := (&AgentHooks{OnComplete: []AgentHookAction{comment(), label("a"), comment()}}).Validate()
	if err == nil || !strings.Contains(err.Error(), "on_complete[2]") {
		t.Fatalf("Validate() = %v, want it to name index 2", err)
	}
}

func TestAgentHooks_IsEmpty(t *testing.T) {
	var nilHooks *AgentHooks
	if !nilHooks.IsEmpty() {
		t.Error("nil hooks should be empty")
	}
	if !(&AgentHooks{}).IsEmpty() {
		t.Error("zero-value hooks should be empty")
	}
	if !(&AgentHooks{OnComplete: []AgentHookAction{}}).IsEmpty() {
		t.Error("hooks with an empty slice should be empty")
	}
	if (&AgentHooks{OnComplete: []AgentHookAction{comment()}}).IsEmpty() {
		t.Error("hooks with an action should not be empty")
	}
}

func TestAgentHooks_CloneIsDeepCopy(t *testing.T) {
	var nilHooks *AgentHooks
	if nilHooks.Clone() != nil {
		t.Error("cloning nil should yield nil")
	}

	orig := &AgentHooks{OnComplete: []AgentHookAction{comment(), label("criticized")}}
	clone := orig.Clone()
	if !orig.Equal(clone) {
		t.Fatal("clone should equal the original")
	}

	clone.OnComplete[1].Value = "mutated"
	if orig.OnComplete[1].Value != "criticized" {
		t.Error("mutating the clone changed the original")
	}
	orig.OnComplete[0].Source = "changed"
	if clone.OnComplete[0].Source != AgentHookCommentSourceFinalReply {
		t.Error("mutating the original changed the clone")
	}

	clone.OnComplete = append(clone.OnComplete, label("extra"))
	if len(orig.OnComplete) != 2 {
		t.Error("appending to the clone grew the original")
	}
}

func TestAgentHooks_Equal(t *testing.T) {
	var nilHooks *AgentHooks
	full := &AgentHooks{OnComplete: []AgentHookAction{comment(), label("a")}}

	tests := []struct {
		name string
		a, b *AgentHooks
		want bool
	}{
		{name: "nil vs nil", a: nilHooks, b: nilHooks, want: true},
		{name: "nil vs empty struct", a: nilHooks, b: &AgentHooks{}, want: true},
		{name: "nil vs empty slice", a: nilHooks, b: &AgentHooks{OnComplete: []AgentHookAction{}}, want: true},
		{name: "identical", a: full, b: full.Clone(), want: true},
		{name: "nil vs populated", a: nilHooks, b: full},
		{name: "populated vs nil", a: full, b: nilHooks},
		{
			name: "different length",
			a:    full,
			b:    &AgentHooks{OnComplete: []AgentHookAction{comment()}},
		},
		{
			name: "different label value",
			a:    full,
			b:    &AgentHooks{OnComplete: []AgentHookAction{comment(), label("b")}},
		},
		{
			name: "order matters",
			a:    &AgentHooks{OnComplete: []AgentHookAction{label("a"), label("b")}},
			b:    &AgentHooks{OnComplete: []AgentHookAction{label("b"), label("a")}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Equal(tt.b); got != tt.want {
				t.Errorf("a.Equal(b) = %v, want %v", got, tt.want)
			}
			if got := tt.b.Equal(tt.a); got != tt.want {
				t.Errorf("Equal is not symmetric: b.Equal(a) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgent_HooksJSONRoundTrip(t *testing.T) {
	// A hookless agent must not emit a hooks key: omitted hooks preserve the
	// pre-hook wire shape exactly.
	data, err := json.Marshal(Agent{Name: "plain"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "hooks") {
		t.Errorf("hookless agent emitted a hooks key: %s", data)
	}

	orig := Agent{Name: "critic", Hooks: &AgentHooks{OnComplete: []AgentHookAction{comment(), label("criticized")}}}
	data, err = json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Agent
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !orig.Hooks.Equal(back.Hooks) {
		t.Errorf("round trip lost the pipeline: %+v", back.Hooks)
	}
}
