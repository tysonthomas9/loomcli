package publish

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

// lockedApprovalForge serializes calls into the fake provider so concurrent
// approvals and reconcile passes race only on the journal, as they would
// against GitHub. While pair is set, each PR read waits briefly for a second
// caller, so two racers read the same PR state and then race to record the
// merge.
type lockedApprovalForge struct {
	*mergeForgeFake
	mu      sync.Mutex
	pair    bool
	pairMu  sync.Mutex
	waiting chan struct{}
}

func (forge *lockedApprovalForge) meet() {
	forge.pairMu.Lock()
	if !forge.pair {
		forge.pairMu.Unlock()
		return
	}
	if forge.waiting != nil {
		close(forge.waiting)
		forge.waiting = nil
		forge.pairMu.Unlock()
		return
	}
	waiting := make(chan struct{})
	forge.waiting = waiting
	forge.pairMu.Unlock()
	select {
	case <-waiting:
	case <-time.After(300 * time.Millisecond):
		forge.pairMu.Lock()
		if forge.waiting == waiting {
			forge.waiting = nil
		}
		forge.pairMu.Unlock()
	}
}

func (forge *lockedApprovalForge) PullByNumber(ctx context.Context, owner, repo string, number int) (stackpublish.PR, error) {
	forge.meet()
	forge.mu.Lock()
	defer forge.mu.Unlock()
	return forge.mergeForgeFake.PullByNumber(ctx, owner, repo, number)
}

func (forge *lockedApprovalForge) PRStatuses(ctx context.Context, owner, repo, prefix string) (map[string]stackpublish.PRStatus, error) {
	forge.mu.Lock()
	defer forge.mu.Unlock()
	return forge.mergeForgeFake.PRStatuses(ctx, owner, repo, prefix)
}

func (forge *lockedApprovalForge) QueuedPRNumbers(ctx context.Context, owner, repo string) (map[int]bool, error) {
	forge.mu.Lock()
	defer forge.mu.Unlock()
	return forge.mergeForgeFake.QueuedPRNumbers(ctx, owner, repo)
}

func (forge *lockedApprovalForge) FailedLoomChecks(ctx context.Context, owner, repo, sha string) ([]string, error) {
	forge.mu.Lock()
	defer forge.mu.Unlock()
	return forge.mergeForgeFake.FailedLoomChecks(ctx, owner, repo, sha)
}

func (forge *lockedApprovalForge) MergeLoomPull(ctx context.Context, owner, repo string, number int, head string) (stackpublish.LoomMergeResult, error) {
	forge.mu.Lock()
	defer forge.mu.Unlock()
	return forge.mergeForgeFake.MergeLoomPull(ctx, owner, repo, number, head)
}

func (forge *lockedApprovalForge) LoomMergeStatus(ctx context.Context, owner, repo string, number int, uuid string) (stackpublish.LoomMergeResult, error) {
	forge.mu.Lock()
	defer forge.mu.Unlock()
	return forge.mergeForgeFake.LoomMergeStatus(ctx, owner, repo, number, uuid)
}

func (forge *lockedApprovalForge) DeleteLoomBranch(ctx context.Context, owner, repo, branch string) error {
	forge.mu.Lock()
	defer forge.mu.Unlock()
	return forge.mergeForgeFake.DeleteLoomBranch(ctx, owner, repo, branch)
}

func (forge *lockedApprovalForge) mergeCalls() int {
	forge.mu.Lock()
	defer forge.mu.Unlock()
	return forge.merged
}

// raceApproveAndReconcile starts two human approvals of the same PR head and
// the given reconcile passes at once, each reconcile on its own journal
// connection, and waits for all of them.
func raceApproveAndReconcile(t *testing.T, item fixture, forge *lockedApprovalForge, change, head string,
	reconciles ...func(context.Context) error) {
	t.Helper()
	ctx := context.Background()
	start := make(chan struct{})
	errs := make(chan error, 2+len(reconciles))
	var group sync.WaitGroup
	run := func(step func() error) {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			errs <- step()
		}()
	}
	for range 2 {
		run(func() error {
			_, err := approveMerge(ctx, item.store, forge, "W", "L", change, head, tyson)
			return err
		})
	}
	for _, reconcile := range reconciles {
		run(func() error { return reconcile(ctx) })
	}
	forge.pairMu.Lock()
	forge.pair = true
	forge.pairMu.Unlock()
	close(start)
	group.Wait()
	forge.pairMu.Lock()
	forge.pair = false
	forge.pairMu.Unlock()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent approve or reconcile: %v", err)
		}
	}
}

// Two approvals of the same bottom PR and reconcile passes racing on the real
// journal hand the PR to the provider exactly once.
func TestApproveMergeConcurrentApprovalsAndReconcileMergeOnce(t *testing.T) {
	t.Run("trunk", func(t *testing.T) {
		item, fake, head := trunkApprovalFixture(t)
		fake.pending = true
		forge := &lockedApprovalForge{mergeForgeFake: fake}
		reconcile := func(ctx context.Context) error { return ReconcileMergeApprovalsAt(ctx, item.storePath, forge) }
		raceApproveAndReconcile(t, item, forge, "A", head, reconcile, reconcile)
		reconcileApprovals(t, item, forge)
		if got := approval(t, item, "A"); got.Status != MergeApprovalMerging || got.DispatchAttempts != 1 || forge.mergeCalls() != 1 {
			t.Fatalf("approval = %+v, provider merge calls = %d, want one", got, forge.mergeCalls())
		}
	})
	t.Run("loom", func(t *testing.T) {
		item, fake, heads := mergeApprovalFixture(t, "loom")
		forge := &lockedApprovalForge{mergeForgeFake: fake}
		// The Loom merge reads the workspace config from fleet-db. Keep one
		// fleet-db running, as loom serve does, so the racing passes attach to
		// it instead of each starting and stopping its own.
		handle, err := bootstrap.OpenStore(context.Background(), bootstrap.LoomDir(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = handle.Close() })
		approvals := func(ctx context.Context) error { return ReconcileMergeApprovalsAt(ctx, item.storePath, forge) }
		merges := func(ctx context.Context) error { return ReconcileLoomMergesAt(ctx, item.storePath, forge) }
		raceApproveAndReconcile(t, item, forge, "A", heads[0], approvals, merges, merges)
		if err := ReconcileLoomMergesAt(context.Background(), item.storePath, forge); err != nil {
			t.Fatal(err)
		}
		reconcileApprovals(t, item, forge)
		if got, merge := approval(t, item, "A"), leadMerge(t, item); got.Attempt != 1 ||
			merge.RequestID != "approval-merge:W:A:1" || forge.mergeCalls() != 1 {
			t.Fatalf("approval = %+v, merge = %+v, provider merge calls = %d, want one", got, merge, forge.mergeCalls())
		}
	})
}
