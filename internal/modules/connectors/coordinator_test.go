package connectors

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestGitReadCoordinatorRetainsCloneReplayAndCoordinateConflict(t *testing.T) {
	coordinator := newGitReadCoordinatorWithIdleLimit(2)
	first := gitReadTestCoordinates(1)
	second := gitReadTestCoordinates(2)
	third := gitReadTestCoordinates(3)

	completeGitRead(t, coordinator, first)
	completeGitRead(t, coordinator, second)

	release, replay, err := coordinator.acquireOperation(t.Context(), first)
	if err != nil {
		t.Fatalf("clone replay: %v", err)
	}
	if !replay {
		t.Fatal("retained completed clone was not replayed")
	}
	release(false)

	completeGitRead(t, coordinator, third)

	divergentFirst := first
	divergentFirst.remoteURL = "/srv/git/different.git"
	if release, replay, err := coordinator.acquireOperation(t.Context(), divergentFirst); !errors.Is(err, ErrIdempotencyConflict) {
		if release != nil {
			release(false)
		}
		t.Fatalf("retained divergent clone result = (replay=%v, err=%v), want %v", replay, err, ErrIdempotencyConflict)
	}

	divergentSecond := second
	divergentSecond.remoteURL = "/srv/git/replacement-after-eviction.git"
	release, replay, err = coordinator.acquireOperation(t.Context(), divergentSecond)
	if err != nil {
		t.Fatalf("evicted operation id was not reusable: %v", err)
	}
	if replay {
		t.Fatal("evicted clone unexpectedly replayed a discarded receipt")
	}
	release(true)

	assertGitReadCoordinatorSize(t, coordinator, 2, 2)
}

func TestGitReadCoordinatorRetainsFailedIntentForConflictAndRetry(t *testing.T) {
	coordinator := newGitReadCoordinatorWithIdleLimit(2)
	coordinates := gitReadTestCoordinates(1)

	release, replay, err := coordinator.acquireOperation(t.Context(), coordinates)
	if err != nil || replay {
		t.Fatalf("initial acquire = (replay=%v, err=%v)", replay, err)
	}
	release(false)

	divergent := coordinates
	divergent.repositoryRef = "different-repository"
	if release, replay, err := coordinator.acquireOperation(t.Context(), divergent); !errors.Is(err, ErrIdempotencyConflict) {
		if release != nil {
			release(false)
		}
		t.Fatalf("failed-intent conflict = (replay=%v, err=%v), want %v", replay, err, ErrIdempotencyConflict)
	}

	release, replay, err = coordinator.acquireOperation(t.Context(), coordinates)
	if err != nil {
		t.Fatalf("retry failed intent: %v", err)
	}
	if replay {
		t.Fatal("failed clone was replayed as completed")
	}
	release(true)
}

func TestGitReadCoordinatorPinsRunningBurstThenContractsToLimit(t *testing.T) {
	const (
		idleLimit = 8
		total     = 96
	)
	coordinator := newGitReadCoordinatorWithIdleLimit(idleLimit)
	releaseAll := make(chan struct{})
	errs := make(chan error, total)
	var acquired sync.WaitGroup
	var finished sync.WaitGroup
	acquired.Add(total)
	finished.Add(total)

	for index := range total {
		go func() {
			defer finished.Done()
			release, replay, err := coordinator.acquireOperation(
				t.Context(),
				gitReadTestCoordinates(index),
			)
			if err != nil {
				errs <- err
				acquired.Done()
				return
			}
			if replay {
				errs <- fmt.Errorf("new operation %d unexpectedly replayed", index)
				acquired.Done()
				return
			}
			acquired.Done()
			<-releaseAll
			release(true)
		}()
	}

	acquired.Wait()
	assertGitReadCoordinatorSize(t, coordinator, total, 0)
	close(releaseAll)
	finished.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent acquire: %v", err)
	}
	assertGitReadCoordinatorSize(t, coordinator, idleLimit, idleLimit)
}

func TestGitReadCoordinatorPinsCloneWaiterUntilReplay(t *testing.T) {
	coordinator := newGitReadCoordinatorWithIdleLimit(0)
	coordinates := gitReadTestCoordinates(1)
	firstRelease, replay, err := coordinator.acquireOperation(t.Context(), coordinates)
	if err != nil || replay {
		t.Fatalf("initial acquire = (replay=%v, err=%v)", replay, err)
	}

	secondResult := make(chan struct {
		release func(bool)
		replay  bool
		err     error
	}, 1)
	go func() {
		release, replay, acquireErr := coordinator.acquireOperation(t.Context(), coordinates)
		secondResult <- struct {
			release func(bool)
			replay  bool
			err     error
		}{release: release, replay: replay, err: acquireErr}
	}()
	waitForGitReadWaiters(t, coordinator, coordinates, 1)

	firstRelease(true)
	select {
	case result := <-secondResult:
		if result.err != nil {
			t.Fatalf("waiting clone replay: %v", result.err)
		}
		if !result.replay {
			t.Fatal("waiting clone reran after its completed receipt was released")
		}
		result.release(false)
	case <-time.After(time.Second):
		t.Fatal("waiting clone did not resume")
	}

	assertGitReadCoordinatorSize(t, coordinator, 0, 0)
}

func TestGitReadCoordinatorTargetLockSurvivesOperationEviction(t *testing.T) {
	coordinator := newGitReadCoordinatorWithIdleLimit(1)
	releaseTarget, err := coordinator.acquireTarget(t.Context(), "/workspace/repo")
	if err != nil {
		t.Fatal(err)
	}

	for index := range 32 {
		completeGitRead(t, coordinator, gitReadTestCoordinates(index))
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

func gitReadTestCoordinates(index int) gitReadCoordinates {
	return gitReadCoordinates{
		workspaceKey:    "WS-1",
		operationID:     fmt.Sprintf("git-read-%d", index),
		repositoryRef:   fmt.Sprintf("repo-%d", index),
		operation:       GitReadClone,
		remoteURL:       fmt.Sprintf("/srv/git/repo-%d.git", index),
		workspacePath:   "/workspace",
		targetPath:      fmt.Sprintf("/workspace/repo-%d", index),
		remoteName:      "origin",
		canonicalTarget: fmt.Sprintf("/workspace/repo-%d", index),
	}
}

func completeGitRead(
	t *testing.T,
	coordinator *gitReadCoordinator,
	coordinates gitReadCoordinates,
) {
	t.Helper()
	release, replay, err := coordinator.acquireOperation(t.Context(), coordinates)
	if err != nil {
		t.Fatalf("acquire Git read: %v", err)
	}
	if replay {
		t.Fatal("new Git read unexpectedly replayed")
	}
	release(true)
}

func assertGitReadCoordinatorSize(
	t *testing.T,
	coordinator *gitReadCoordinator,
	operations int,
	idle int,
) {
	t.Helper()
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if got := len(coordinator.operations); got != operations {
		t.Fatalf("Git-read operations = %d, want %d", got, operations)
	}
	if got := coordinator.idle.Len(); got != idle {
		t.Fatalf("Git-read idle operations = %d, want %d", got, idle)
	}
}

func waitForGitReadWaiters(
	t *testing.T,
	coordinator *gitReadCoordinator,
	coordinates gitReadCoordinates,
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
	t.Fatalf("Git-read waiters did not reach %d", want)
}
