package sourcecontrol

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMaterializationCoordinatorRetainsRecentReplayCoordinates(t *testing.T) {
	coordinator := newMaterializationCoordinatorWithIdleLimit(2)
	first := materializationTestCoordinates(1)
	second := materializationTestCoordinates(2)
	third := materializationTestCoordinates(3)

	acquireAndReleaseMaterialization(t, coordinator, first)
	acquireAndReleaseMaterialization(t, coordinator, second)

	// Reusing the exact coordinates refreshes the entry, so the next insert
	// evicts the older second operation.
	acquireAndReleaseMaterialization(t, coordinator, first)
	acquireAndReleaseMaterialization(t, coordinator, third)

	divergentFirst := first
	divergentFirst.repositoryRef = "different-repository"
	if release, err := coordinator.acquireOperation(t.Context(), divergentFirst); !errors.Is(err, ErrIdempotencyConflict) {
		if release != nil {
			release()
		}
		t.Fatalf("retained divergent materialization error = %v, want %v", err, ErrIdempotencyConflict)
	}

	divergentSecond := second
	divergentSecond.repositoryRef = "replacement-after-eviction"
	release, err := coordinator.acquireOperation(t.Context(), divergentSecond)
	if err != nil {
		t.Fatalf("evicted operation id was not reusable: %v", err)
	}
	release()

	assertMaterializationCoordinatorSize(t, coordinator, 2, 2)
}

func TestMaterializationCoordinatorPinsRunningBurstThenContractsToLimit(t *testing.T) {
	const (
		idleLimit = 8
		total     = 96
	)
	coordinator := newMaterializationCoordinatorWithIdleLimit(idleLimit)
	releaseAll := make(chan struct{})
	errs := make(chan error, total)
	var acquired sync.WaitGroup
	var finished sync.WaitGroup
	acquired.Add(total)
	finished.Add(total)

	for index := range total {
		go func() {
			defer finished.Done()
			release, err := coordinator.acquireOperation(
				t.Context(),
				materializationTestCoordinates(index),
			)
			if err != nil {
				errs <- err
				acquired.Done()
				return
			}
			acquired.Done()
			<-releaseAll
			release()
		}()
	}

	acquired.Wait()
	assertMaterializationCoordinatorSize(t, coordinator, total, 0)
	close(releaseAll)
	finished.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent acquire: %v", err)
	}
	assertMaterializationCoordinatorSize(t, coordinator, idleLimit, idleLimit)
}

func TestMaterializationCoordinatorPinsWaiterAcrossIdlePressure(t *testing.T) {
	coordinator := newMaterializationCoordinatorWithIdleLimit(1)
	coordinates := materializationTestCoordinates(1)
	firstRelease, err := coordinator.acquireOperation(t.Context(), coordinates)
	if err != nil {
		t.Fatal(err)
	}

	secondAcquired := make(chan func(), 1)
	secondErr := make(chan error, 1)
	go func() {
		release, acquireErr := coordinator.acquireOperation(t.Context(), coordinates)
		if acquireErr != nil {
			secondErr <- acquireErr
			return
		}
		secondAcquired <- release
	}()
	waitForMaterializationWaiters(t, coordinator, coordinates, 1)

	firstRelease()
	select {
	case release := <-secondAcquired:
		release()
	case err := <-secondErr:
		t.Fatalf("waiting acquire: %v", err)
	case <-time.After(time.Second):
		t.Fatal("waiting acquire did not resume")
	}

	assertMaterializationCoordinatorSize(t, coordinator, 1, 1)
}

func TestMaterializationCoordinatorTargetLockSurvivesOperationEviction(t *testing.T) {
	coordinator := newMaterializationCoordinatorWithIdleLimit(1)
	releaseTarget, err := coordinator.acquireTarget(t.Context(), "/workspace/repo")
	if err != nil {
		t.Fatal(err)
	}

	for index := range 32 {
		acquireAndReleaseMaterialization(t, coordinator, materializationTestCoordinates(index))
	}

	waitCtx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if secondRelease, lockErr := coordinator.acquireTarget(waitCtx, "/workspace/repo"); !errors.Is(lockErr, context.DeadlineExceeded) {
		if secondRelease != nil {
			secondRelease()
		}
		t.Fatalf("second target lock error = %v, want %v", lockErr, context.DeadlineExceeded)
	}
	releaseTarget()

	secondRelease, err := coordinator.acquireTarget(t.Context(), "/workspace/repo")
	if err != nil {
		t.Fatalf("target lock after release: %v", err)
	}
	secondRelease()

	coordinator.mu.Lock()
	targets := len(coordinator.targets)
	coordinator.mu.Unlock()
	if targets != 0 {
		t.Fatalf("target locks = %d, want 0", targets)
	}
}

func TestRefFetchCoordinatorRetainsRecentReplayCoordinates(t *testing.T) {
	coordinator := newRefFetchCoordinatorWithIdleLimit(2)
	first := refFetchTestCoordinates(1)
	second := refFetchTestCoordinates(2)
	third := refFetchTestCoordinates(3)

	acquireAndReleaseRefFetch(t, coordinator, first)
	acquireAndReleaseRefFetch(t, coordinator, second)
	acquireAndReleaseRefFetch(t, coordinator, first)
	acquireAndReleaseRefFetch(t, coordinator, third)

	divergentFirst := first
	divergentFirst.expectedCommit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if release, err := coordinator.acquireOperation(t.Context(), divergentFirst); !errors.Is(err, ErrIdempotencyConflict) {
		if release != nil {
			release()
		}
		t.Fatalf("retained divergent fetch error = %v, want %v", err, ErrIdempotencyConflict)
	}

	divergentSecond := second
	divergentSecond.expectedCommit = "cccccccccccccccccccccccccccccccccccccccc"
	release, err := coordinator.acquireOperation(t.Context(), divergentSecond)
	if err != nil {
		t.Fatalf("evicted fetch operation id was not reusable: %v", err)
	}
	release()

	assertRefFetchCoordinatorSize(t, coordinator, 2, 2)
}

func TestRefFetchCoordinatorPinsRunningBurstThenContractsToLimit(t *testing.T) {
	const (
		idleLimit = 8
		total     = 96
	)
	coordinator := newRefFetchCoordinatorWithIdleLimit(idleLimit)
	releaseAll := make(chan struct{})
	errs := make(chan error, total)
	var acquired sync.WaitGroup
	var finished sync.WaitGroup
	acquired.Add(total)
	finished.Add(total)

	for index := range total {
		go func() {
			defer finished.Done()
			release, err := coordinator.acquireOperation(
				t.Context(),
				refFetchTestCoordinates(index),
			)
			if err != nil {
				errs <- err
				acquired.Done()
				return
			}
			acquired.Done()
			<-releaseAll
			release()
		}()
	}

	acquired.Wait()
	assertRefFetchCoordinatorSize(t, coordinator, total, 0)
	close(releaseAll)
	finished.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent acquire: %v", err)
	}
	assertRefFetchCoordinatorSize(t, coordinator, idleLimit, idleLimit)
}

func TestRefFetchCoordinatorPinsWaiterAcrossIdlePressure(t *testing.T) {
	coordinator := newRefFetchCoordinatorWithIdleLimit(1)
	coordinates := refFetchTestCoordinates(1)
	firstRelease, err := coordinator.acquireOperation(t.Context(), coordinates)
	if err != nil {
		t.Fatal(err)
	}

	secondAcquired := make(chan func(), 1)
	secondErr := make(chan error, 1)
	go func() {
		release, acquireErr := coordinator.acquireOperation(t.Context(), coordinates)
		if acquireErr != nil {
			secondErr <- acquireErr
			return
		}
		secondAcquired <- release
	}()
	waitForRefFetchWaiters(t, coordinator, coordinates, 1)

	firstRelease()
	select {
	case release := <-secondAcquired:
		release()
	case err := <-secondErr:
		t.Fatalf("waiting acquire: %v", err)
	case <-time.After(time.Second):
		t.Fatal("waiting acquire did not resume")
	}

	assertRefFetchCoordinatorSize(t, coordinator, 1, 1)
}

func TestRefFetchCoordinatorTargetLockSurvivesOperationEviction(t *testing.T) {
	coordinator := newRefFetchCoordinatorWithIdleLimit(1)
	releaseTarget, err := coordinator.acquireTarget(t.Context(), "/workspace/repo")
	if err != nil {
		t.Fatal(err)
	}

	for index := range 32 {
		acquireAndReleaseRefFetch(t, coordinator, refFetchTestCoordinates(index))
	}

	waitCtx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if secondRelease, lockErr := coordinator.acquireTarget(waitCtx, "/workspace/repo"); !errors.Is(lockErr, context.DeadlineExceeded) {
		if secondRelease != nil {
			secondRelease()
		}
		t.Fatalf("second target lock error = %v, want %v", lockErr, context.DeadlineExceeded)
	}
	releaseTarget()

	secondRelease, err := coordinator.acquireTarget(t.Context(), "/workspace/repo")
	if err != nil {
		t.Fatalf("target lock after release: %v", err)
	}
	secondRelease()

	coordinator.mu.Lock()
	targets := len(coordinator.targets)
	coordinator.mu.Unlock()
	if targets != 0 {
		t.Fatalf("target locks = %d, want 0", targets)
	}
}

func materializationTestCoordinates(index int) materializationCoordinates {
	id := fmt.Sprintf("materialize-%d", index)
	return materializationCoordinates{
		workspaceKey:      "WS-1",
		materializationID: id,
		repositoryRef:     fmt.Sprintf("repo-%d", index),
		remoteURL:         fmt.Sprintf("/srv/git/repo-%d.git", index),
		remoteName:        "origin",
		workspacePath:     "/workspace",
		targetPath:        fmt.Sprintf("/workspace/repo-%d", index),
		canonicalTarget:   fmt.Sprintf("/workspace/repo-%d", index),
	}
}

func refFetchTestCoordinates(index int) refFetchCoordinates {
	id := fmt.Sprintf("fetch-%d", index)
	return refFetchCoordinates{
		workspaceKey:    "WS-1",
		operationID:     id,
		repositoryRef:   fmt.Sprintf("repo-%d", index),
		remoteURL:       fmt.Sprintf("/srv/git/repo-%d.git", index),
		workspacePath:   "/workspace",
		targetPath:      fmt.Sprintf("/workspace/repo-%d", index),
		remoteName:      "origin",
		sourceRef:       "refs/heads/main",
		destinationRef:  fmt.Sprintf("refs/loom/fetch/%d", index),
		expectedCommit:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		canonicalTarget: fmt.Sprintf("/workspace/repo-%d", index),
	}
}

func acquireAndReleaseMaterialization(
	t *testing.T,
	coordinator *materializationCoordinator,
	coordinates materializationCoordinates,
) {
	t.Helper()
	release, err := coordinator.acquireOperation(t.Context(), coordinates)
	if err != nil {
		t.Fatalf("acquire materialization: %v", err)
	}
	release()
}

func acquireAndReleaseRefFetch(
	t *testing.T,
	coordinator *refFetchCoordinator,
	coordinates refFetchCoordinates,
) {
	t.Helper()
	release, err := coordinator.acquireOperation(t.Context(), coordinates)
	if err != nil {
		t.Fatalf("acquire ref fetch: %v", err)
	}
	release()
}

func assertMaterializationCoordinatorSize(
	t *testing.T,
	coordinator *materializationCoordinator,
	operations int,
	idle int,
) {
	t.Helper()
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if got := len(coordinator.operations); got != operations {
		t.Fatalf("materialization operations = %d, want %d", got, operations)
	}
	if got := coordinator.idle.Len(); got != idle {
		t.Fatalf("materialization idle operations = %d, want %d", got, idle)
	}
}

func assertRefFetchCoordinatorSize(
	t *testing.T,
	coordinator *refFetchCoordinator,
	operations int,
	idle int,
) {
	t.Helper()
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if got := len(coordinator.operations); got != operations {
		t.Fatalf("ref-fetch operations = %d, want %d", got, operations)
	}
	if got := coordinator.idle.Len(); got != idle {
		t.Fatalf("ref-fetch idle operations = %d, want %d", got, idle)
	}
}

func waitForMaterializationWaiters(
	t *testing.T,
	coordinator *materializationCoordinator,
	coordinates materializationCoordinates,
	want int,
) {
	t.Helper()
	key := coordinates.workspaceKey + "\x00" + coordinates.materializationID
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		intent := coordinator.operations[key]
		got := 0
		if intent != nil {
			got = intent.waiters
		}
		coordinator.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("materialization waiters did not reach %d", want)
}

func waitForRefFetchWaiters(
	t *testing.T,
	coordinator *refFetchCoordinator,
	coordinates refFetchCoordinates,
	want int,
) {
	t.Helper()
	key := coordinates.workspaceKey + "\x00" + coordinates.operationID
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		intent := coordinator.operations[key]
		got := 0
		if intent != nil {
			got = intent.waiters
		}
		coordinator.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("ref-fetch waiters did not reach %d", want)
}
