package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// Requeued task runs become claimable only after NextEligibleAt; a zero value
// keeps the run immediately claimable (back-compat).
func TestTaskRunRequeueNextEligibleAtGatesClaim(t *testing.T) {
	base := time.Now().UTC()
	cases := []struct {
		name           string
		nextEligibleAt time.Time
		claimAt        time.Time
		wantClaimable  bool
	}{
		{
			name:           "zero value immediately claimable",
			nextEligibleAt: time.Time{},
			claimAt:        base,
			wantClaimable:  true,
		},
		{
			name:           "not claimable before NextEligibleAt",
			nextEligibleAt: base.Add(10 * time.Second),
			claimAt:        base,
			wantClaimable:  false,
		},
		{
			name:           "claimable once backoff elapses",
			nextEligibleAt: base.Add(10 * time.Second),
			claimAt:        base.Add(10 * time.Second),
			wantClaimable:  true,
		},
		{
			name:           "past NextEligibleAt claimable",
			nextEligibleAt: base.Add(-time.Second),
			claimAt:        base,
			wantClaimable:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			s := New()
			if _, err := s.Nodes().Create(ctx, execution.NodeCreate{
				WorkspaceKey:    "WS",
				NodeID:          "node-1",
				RuntimeProvider: execution.RuntimeProviderLocal,
				DrainState:      execution.WorkerNodeActive,
				TTL:             time.Hour,
			}); err != nil {
				t.Fatalf("Create node: %v", err)
			}
			if _, err := s.TaskRuns().Create(ctx, execution.TaskRunCreate{
				WorkspaceKey: "WS",
				TaskRunID:    "run-1",
				TaskID:       "WS-1",
				Status:       execution.TaskRunRecordQueued,
			}); err != nil {
				t.Fatalf("Create task run: %v", err)
			}
			claimed, err := s.TaskRuns().ClaimQueued(ctx, "WS", execution.TaskRunClaim{
				NodeID:    "node-1",
				LeaseID:   "lease-1",
				ClaimedAt: base.Add(-time.Minute),
			})
			if err != nil {
				t.Fatalf("first ClaimQueued: %v", err)
			}

			requeued, err := s.TaskRuns().Requeue(ctx, "WS", claimed.TaskRunID, execution.TaskRunRequeue{
				NodeID:         claimed.NodeID,
				LeaseID:        claimed.LeaseID,
				FencingToken:   claimed.FencingToken,
				ErrorClass:     "task_failed",
				RequeuedAt:     base.Add(-30 * time.Second),
				NextEligibleAt: tc.nextEligibleAt,
			})
			if err != nil {
				t.Fatalf("Requeue: %v", err)
			}
			if !requeued.NextEligibleAt.Equal(tc.nextEligibleAt) {
				t.Fatalf("requeued NextEligibleAt = %v, want %v", requeued.NextEligibleAt, tc.nextEligibleAt)
			}

			reclaimed, err := s.TaskRuns().ClaimQueued(ctx, "WS", execution.TaskRunClaim{
				NodeID:    "node-1",
				LeaseID:   "lease-2",
				ClaimedAt: tc.claimAt,
			})
			if tc.wantClaimable {
				if err != nil {
					t.Fatalf("ClaimQueued after requeue: %v", err)
				}
				if reclaimed.TaskRunID != "run-1" || reclaimed.Status != execution.TaskRunRecordRunning {
					t.Fatalf("reclaimed = %+v, want running run-1", reclaimed)
				}
				return
			}
			if !errors.Is(err, persistence.ErrNotFound) {
				t.Fatalf("ClaimQueued before NextEligibleAt err = %v, want ErrNotFound", err)
			}
		})
	}
}
