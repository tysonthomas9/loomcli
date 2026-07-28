package domain

import (
	"strings"
	"testing"
)

func cycle(threshold int) *AgentHookCycle {
	return &AgentHookCycle{Threshold: threshold, RearmLabel: "criticized", ShipLabel: "ready"}
}

// The counter is the MAX of the parsed labels, not a count of them. That is what
// makes the crash-safe ordering safe: a counter left behind by a cleanup that
// died mid-way cannot change the next decision.
func TestAgentHookCycle_CompletedRounds(t *testing.T) {
	c := cycle(3)

	tests := []struct {
		name   string
		labels []string
		want   int
	}{
		{name: "no labels", want: 0},
		{name: "no counters", labels: []string{"plan", "criticized"}, want: 0},
		{name: "single counter", labels: []string{"review-cycle=2"}, want: 2},
		{name: "stale leftovers take the max", labels: []string{"review-cycle=1", "review-cycle=3", "review-cycle=2"}, want: 3},
		{name: "unrelated prefix ignored", labels: []string{"cycle=9", "other-cycle=4"}, want: 0},
		{name: "decimal is not a round", labels: []string{"review-cycle=1.5"}, want: 0},
		{name: "zero is not a round", labels: []string{"review-cycle=0"}, want: 0},
		{name: "negative is not a round", labels: []string{"review-cycle=-2"}, want: 0},
		{name: "garbage is not a round", labels: []string{"review-cycle=two"}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.CompletedRounds(tt.labels); got != tt.want {
				t.Errorf("CompletedRounds(%v) = %d, want %d", tt.labels, got, tt.want)
			}
		})
	}
}

func TestAgentHookCycle_PrefixOverride(t *testing.T) {
	c := &AgentHookCycle{Threshold: 2, RearmLabel: "a", ShipLabel: "b", Prefix: "pass-"}

	if got := c.CounterLabel(3); got != "pass-3" {
		t.Errorf("CounterLabel(3) = %q, want %q", got, "pass-3")
	}
	if got := c.CompletedRounds([]string{"pass-4", "review-cycle=9"}); got != 4 {
		t.Errorf("CompletedRounds = %d, want 4 — the default prefix must not leak in", got)
	}
}

func TestAgentHooks_Validate_CycleAction(t *testing.T) {
	comment := AgentHookAction{Type: AgentHookActionComment, Source: AgentHookCommentSourceFinalReply}
	cyc := AgentHookAction{Type: AgentHookActionCycle, Cycle: cycle(3)}

	tests := []struct {
		name    string
		actions []AgentHookAction
		wantErr string
	}{
		{name: "cycle alone", actions: []AgentHookAction{cyc}},
		{name: "comment then cycle", actions: []AgentHookAction{comment, cyc}},
		{
			name:    "comment after cycle breaks write-before-stamp",
			actions: []AgentHookAction{cyc, comment},
			wantErr: "must not follow an add_label action",
		},
		{
			name:    "two cycles",
			actions: []AgentHookAction{cyc, cyc},
			wantErr: "only one cycle action is allowed",
		},
		{
			name:    "missing cycle block",
			actions: []AgentHookAction{{Type: AgentHookActionCycle}},
			wantErr: "requires a cycle block",
		},
		{
			name:    "threshold below one",
			actions: []AgentHookAction{{Type: AgentHookActionCycle, Cycle: &AgentHookCycle{Threshold: 0, RearmLabel: "a", ShipLabel: "b"}}},
			wantErr: "threshold must be >= 1",
		},
		{
			name:    "blank rearm label",
			actions: []AgentHookAction{{Type: AgentHookActionCycle, Cycle: &AgentHookCycle{Threshold: 2, RearmLabel: "  ", ShipLabel: "b"}}},
			wantErr: "non-blank rearm_label",
		},
		{
			// Re-arming the label it ships with could never terminate.
			name:    "rearm equals ship",
			actions: []AgentHookAction{{Type: AgentHookActionCycle, Cycle: &AgentHookCycle{Threshold: 2, RearmLabel: "same", ShipLabel: "same"}}},
			wantErr: "must differ",
		},
		{
			name:    "cycle block on a non-cycle action",
			actions: []AgentHookAction{{Type: AgentHookActionAddLabel, Value: "x", Cycle: cycle(2)}},
			wantErr: "must not set cycle",
		},
		{
			name:    "cycle carrying free text",
			actions: []AgentHookAction{{Type: AgentHookActionCycle, Value: "oops", Cycle: cycle(2)}},
			wantErr: "must not set value or source",
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
