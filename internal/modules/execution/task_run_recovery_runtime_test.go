package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type ownedRecoveryAPIStub struct {
	commands []RecoverStaleChildTaskRunsCommand
}

func (stub *ownedRecoveryAPIStub) RecoverStaleChildTaskRuns(
	_ context.Context,
	_ authority.ExecutionAuthority,
	command RecoverStaleChildTaskRunsCommand,
) (RecoverStaleTaskRunsResult, error) {
	stub.commands = append(stub.commands, command)
	return RecoverStaleTaskRunsResult{
		WorkspaceKey: command.WorkspaceKey,
		StaleBefore:  command.StaleBefore,
		RecoveredAt:  command.ObservedAt,
	}, nil
}

type ownedRecoveryAuthorityStub struct {
	workspace string
	action    authority.Action
	owner     Owner
}

func (stub *ownedRecoveryAuthorityStub) ResolveDriverRunAuthority(
	_ context.Context,
	workspace string,
	action authority.Action,
	owner Owner,
) (authority.ExecutionAuthority, error) {
	stub.workspace = workspace
	stub.action = action
	stub.owner = owner
	return authority.ExecutionAuthority{}, nil
}

func TestOwnedStaleTaskRunRecoveryUsesExactParentOwnerAndSafeCutoff(t *testing.T) {
	wall := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	monotonic := time.Duration(0)
	api := &ownedRecoveryAPIStub{}
	authorities := &ownedRecoveryAuthorityStub{}
	owner := Owner{
		ResourceKind: ResourceDriverRun,
		ResourceID:   "run-1",
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		LeaseToken:   "raw-parent-token",
		FencingToken: 7,
	}
	recovery := &OwnedStaleTaskRunRecovery{
		API: api, Authorities: authorities, WorkspaceKey: "TEST", ParentOwner: owner,
		MaxAge: 5 * time.Minute, Now: func() time.Time { return wall },
		MonotonicNow: func() time.Duration { return monotonic }, ClockOrigin: wall,
	}

	if _, err := recovery.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	first := api.commands[0]
	if !first.ObservedAt.Equal(wall) || !first.StaleBefore.Equal(wall.Add(-5*time.Minute)) {
		t.Fatalf("first recovery window = %s..%s, want %s..%s", first.StaleBefore, first.ObservedAt, wall.Add(-5*time.Minute), wall)
	}
	if first.ParentOwner != owner || first.DriverRunID != owner.ResourceID || first.ErrorClass != "stale_task_run" ||
		first.ErrorMessage != "task run heartbeat is stale" ||
		first.RequestID != RecoverStaleChildTaskRunsRequestID(owner.ResourceID, first.StaleBefore) {
		t.Fatalf("first recovery command = %+v", first)
	}
	if authorities.workspace != "TEST" || authorities.action != ActionRecoverStaleChildTaskRuns || authorities.owner != owner {
		t.Fatalf("authority request = %q/%q/%+v", authorities.workspace, authorities.action, authorities.owner)
	}

	// A two-hour wall jump advances the recovery window only by monotonic time.
	wall = wall.Add(2 * time.Hour)
	monotonic = 2 * time.Second
	if _, err := recovery.RunOnce(context.Background()); err != nil {
		t.Fatalf("forward-jump RunOnce: %v", err)
	}
	if got := api.commands[1].ObservedAt; !got.Equal(time.Date(2030, 1, 1, 0, 0, 2, 0, time.UTC)) {
		t.Fatalf("forward-jump observedAt = %s, want monotonic +2s", got)
	}

	// A backward jump uses the earlier wall clock, protecting new-epoch writes.
	wall = time.Date(2029, 12, 31, 22, 0, 0, 0, time.UTC)
	monotonic = 4 * time.Second
	if _, err := recovery.RunOnce(context.Background()); err != nil {
		t.Fatalf("backward-jump RunOnce: %v", err)
	}
	if got := api.commands[2].ObservedAt; !got.Equal(wall) {
		t.Fatalf("backward-jump observedAt = %s, want wall %s", got, wall)
	}
}

func TestOwnedStaleTaskRunRecoveryDefaultsAndRejectsMissingOwner(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	api := &ownedRecoveryAPIStub{}
	recovery := &OwnedStaleTaskRunRecovery{
		API: api, Authorities: &ownedRecoveryAuthorityStub{}, WorkspaceKey: "TEST",
		ParentOwner: Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "token", FencingToken: 1},
		Now:         func() time.Time { return now }, MonotonicNow: func() time.Duration { return 0 }, ClockOrigin: now,
	}
	if _, err := recovery.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := api.commands[0].StaleBefore; !got.Equal(now.Add(-DefaultStaleTaskRunMaxAge)) {
		t.Fatalf("default staleBefore = %s, want %s", got, now.Add(-DefaultStaleTaskRunMaxAge))
	}

	recovery.ParentOwner.LeaseToken = ""
	if _, err := recovery.RunOnce(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing owner token error = %v, want invalid", err)
	}
}
