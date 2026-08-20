package domain

import (
	"testing"
	"time"
)

func TestLeadRuntimeStatusFor(t *testing.T) {
	startedAt := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		node       *Node
		attempt    LeadProvisionAttempt
		wantStatus LeadRuntimeStatus
		wantError  string
	}{
		{
			name:       "no placement without attempt evidence",
			wantStatus: LeadRuntimeNotProvisioned,
		},
		{
			name:       "no placement failed attempt",
			attempt:    LeadProvisionAttempt{Outcome: LeadProvisionOutcomeFailed, Error: "credentials rejected"},
			wantStatus: LeadRuntimeNotProvisioned,
			wantError:  "credentials rejected",
		},
		{
			name:       "no placement in progress",
			attempt:    LeadProvisionAttempt{Outcome: LeadProvisionOutcomeInProgress},
			wantStatus: LeadRuntimeProvisioning,
		},
		{
			name:       "placement provisioning",
			node:       leadRuntimeTestNode(NodePlacement{State: PlacementStateProvisioning}),
			wantStatus: LeadRuntimeProvisioning,
		},
		{
			name: "active without lead boot evidence",
			node: leadRuntimeTestNode(NodePlacement{
				State:     PlacementStateActive,
				SandboxID: "sandbox-1",
			}),
			wantStatus: LeadRuntimeDegraded,
			wantError:  "lead sandbox active placement has no durable lead-boot evidence",
		},
		{
			name: "active with lead boot evidence",
			node: leadRuntimeTestNode(NodePlacement{
				State:                PlacementStateActive,
				SandboxID:            "sandbox-1",
				LeadProcessStartedAt: &startedAt,
			}),
			wantStatus: LeadRuntimeReady,
		},
		{
			name: "parked active with lead boot evidence stays ready",
			node: &Node{
				Labels: []string{"daytona-sandbox-state=stopped"},
				Placement: &NodePlacement{
					State:                PlacementStateActive,
					SandboxID:            "sandbox-parked",
					LeadProcessStartedAt: &startedAt,
				},
			},
			wantStatus: LeadRuntimeReady,
		},
		{
			name:       "releasing",
			node:       leadRuntimeTestNode(NodePlacement{State: PlacementStateReleasing}),
			wantStatus: LeadRuntimeReleasing,
		},
		{
			name:       "released",
			node:       leadRuntimeTestNode(NodePlacement{State: PlacementStateReleased}),
			wantStatus: LeadRuntimeReleased,
		},
		{
			name: "released with reason",
			node: leadRuntimeTestNode(NodePlacement{
				State:           PlacementStateReleased,
				ReleaseReason:   PlacementReleaseReasonLostConfirmedAbsent,
				LastDeleteError: "lower-priority delete error",
			}),
			wantStatus: LeadRuntimeReleased,
			wantError:  string(PlacementReleaseReasonLostConfirmedAbsent),
		},
		{
			name:       "lost",
			node:       leadRuntimeTestNode(NodePlacement{State: PlacementStateLost}),
			wantStatus: LeadRuntimeLost,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, runtimeError := LeadRuntimeStatusFor(tt.node, tt.attempt)
			if status != tt.wantStatus || runtimeError != tt.wantError {
				t.Fatalf("LeadRuntimeStatusFor() = (%q, %q), want (%q, %q)", status, runtimeError, tt.wantStatus, tt.wantError)
			}
		})
	}
}

func leadRuntimeTestNode(placement NodePlacement) *Node {
	return &Node{Placement: &placement}
}
