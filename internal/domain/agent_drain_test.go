package domain

import (
	"testing"
	"time"
)

func TestResolveDrain(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	const thisNode = "loom-supervisor-h-222"
	const otherNode = "loom-supervisor-h-111"

	tests := []struct {
		name          string
		desired       AgentDesiredState
		drainNodeID   string
		drainExpires  *time.Time
		currentNodeID string
		want          DrainDecision
	}{
		{
			name: "desired empty is not a drain", desired: "",
			currentNodeID: thisNode, want: DrainNotApplicable,
		},
		{
			name: "stopped is not a drain", desired: AgentDesiredStopped,
			drainNodeID: otherNode, drainExpires: &past,
			currentNodeID: thisNode, want: DrainNotApplicable,
		},
		{
			name: "running is not a drain", desired: AgentDesiredRunning,
			currentNodeID: thisNode, want: DrainNotApplicable,
		},
		{
			name: "draining with no metadata is untargeted", desired: AgentDesiredDraining,
			currentNodeID: thisNode, want: DrainUntargeted,
		},
		{
			name: "drain for another node is superseded", desired: AgentDesiredDraining,
			drainNodeID: otherNode, drainExpires: &future,
			currentNodeID: thisNode, want: DrainSuperseded,
		},
		{
			name: "supersession is reported ahead of expiry", desired: AgentDesiredDraining,
			drainNodeID: otherNode, drainExpires: &past,
			currentNodeID: thisNode, want: DrainSuperseded,
		},
		{
			name: "drain for this node inside its ttl is active", desired: AgentDesiredDraining,
			drainNodeID: thisNode, drainExpires: &future,
			currentNodeID: thisNode, want: DrainActive,
		},
		{
			name: "drain for this node past its ttl is expired", desired: AgentDesiredDraining,
			drainNodeID: thisNode, drainExpires: &past,
			currentNodeID: thisNode, want: DrainExpired,
		},
		{
			name: "expiry exactly now is expired", desired: AgentDesiredDraining,
			drainNodeID: thisNode, drainExpires: &now,
			currentNodeID: thisNode, want: DrainExpired,
		},
		{
			name: "node id with no expiry never lapses on time", desired: AgentDesiredDraining,
			drainNodeID: thisNode, currentNodeID: thisNode, want: DrainActive,
		},
		{
			name: "expiry with no node id is active while unexpired", desired: AgentDesiredDraining,
			drainExpires: &future, currentNodeID: thisNode, want: DrainActive,
		},
		{
			name: "expiry with no node id expires on time", desired: AgentDesiredDraining,
			drainExpires: &past, currentNodeID: thisNode, want: DrainExpired,
		},
		{
			// An unknown identity is not evidence the drain belongs elsewhere.
			name: "unknown current node is never superseded", desired: AgentDesiredDraining,
			drainNodeID: otherNode, drainExpires: &future,
			currentNodeID: "", want: DrainActive,
		},
		{
			name: "unknown current node still expires", desired: AgentDesiredDraining,
			drainNodeID: otherNode, drainExpires: &past,
			currentNodeID: "", want: DrainExpired,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveDrain(tc.desired, tc.drainNodeID, tc.drainExpires, tc.currentNodeID, now)
			if got != tc.want {
				t.Errorf("ResolveDrain() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDrainPolicySplit pins the deliberate disagreement between the two
// policies. They differ on exactly one decision — DrainUntargeted — and
// collapsing them into a single predicate reintroduces either the permanent
// park or a silent regression of yield.
func TestDrainPolicySplit(t *testing.T) {
	tests := []struct {
		decision      DrainDecision
		wantParks     bool
		wantClearable bool
	}{
		{DrainNotApplicable, false, false},
		{DrainActive, true, false},
		{DrainUntargeted, true, true},
		{DrainSuperseded, false, true},
		{DrainExpired, false, true},
	}

	for _, tc := range tests {
		t.Run(tc.decision.String(), func(t *testing.T) {
			if got := DrainParks(tc.decision); got != tc.wantParks {
				t.Errorf("DrainParks(%v) = %v, want %v", tc.decision, got, tc.wantParks)
			}
			if got := DrainClearableAtStartup(tc.decision); got != tc.wantClearable {
				t.Errorf("DrainClearableAtStartup(%v) = %v, want %v", tc.decision, got, tc.wantClearable)
			}
		})
	}

	if !DrainParks(DrainUntargeted) || !DrainClearableAtStartup(DrainUntargeted) {
		t.Error("DrainUntargeted must both park and be clearable at startup")
	}
	if DrainParks(DrainSuperseded) || DrainParks(DrainExpired) {
		t.Error("a superseded or expired drain must never park")
	}
}
