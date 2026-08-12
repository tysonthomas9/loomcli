package domain

import (
	"strings"
	"testing"
)

// close must be terminal. Every other action writes to the task, and a closed
// issue rejects mutation, so anything after a close is unsatisfiable by
// construction — Validate rejects it here rather than letting the supervisor
// discover it mid-pipeline.
func TestAgentHooks_Validate_CloseMustBeLast(t *testing.T) {
	comment := AgentHookAction{Type: AgentHookActionComment, Source: AgentHookCommentSourceFinalReply}
	label := AgentHookAction{Type: AgentHookActionAddLabel, Value: "stage-reviewed"}
	closeAct := AgentHookAction{Type: AgentHookActionClose}

	tests := []struct {
		name    string
		actions []AgentHookAction
		wantErr string
	}{
		{name: "close alone", actions: []AgentHookAction{closeAct}},
		{name: "comment, stamp, close", actions: []AgentHookAction{comment, label, closeAct}},
		{name: "stamp then close", actions: []AgentHookAction{label, closeAct}},
		{
			name:    "add_label after close",
			actions: []AgentHookAction{closeAct, label},
			wantErr: "must not follow a close action",
		},
		{
			name:    "comment after close",
			actions: []AgentHookAction{closeAct, comment},
			wantErr: "must not follow a close action",
		},
		{
			name:    "two closes",
			actions: []AgentHookAction{closeAct, closeAct},
			wantErr: "must not follow a close action",
		},
		{
			name:    "close with a value",
			actions: []AgentHookAction{{Type: AgentHookActionClose, Value: "done"}},
			wantErr: "must not set value",
		},
		{
			name:    "close with a source",
			actions: []AgentHookAction{{Type: AgentHookActionClose, Source: AgentHookCommentSourceFinalReply}},
			wantErr: "must not set source",
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
				t.Fatalf("Validate() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// The two repos each carry their own copy of this enum and validate
// independently; fleet-db rejects an unknown type on write. If they drift, a
// pipeline accepted by one is refused by the other.
func TestAgentHookActionType_CloseIsRecognized(t *testing.T) {
	if !AgentHookActionClose.IsValid() {
		t.Error("close must be recognized or the supervisor cannot execute a stored pipeline")
	}
	if AgentHookActionType("exec").IsValid() {
		t.Error("the action vocabulary must stay closed")
	}
}
