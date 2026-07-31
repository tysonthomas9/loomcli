package sourcecontrol

import (
	"container/list"
	"context"
	"sync"
)

// sourceControlIdleOperationLimit bounds the completed operation intents kept
// for exact-coordinate conflict detection. Running operations and callers
// already waiting on them are pinned until they drain; the idle cache then
// contracts back to this limit.
const sourceControlIdleOperationLimit = 1024

type materializationCoordinates struct {
	workspaceKey      string
	materializationID string
	repositoryRef     string
	remoteURL         string
	remoteName        string
	workspacePath     string
	targetPath        string
	canonicalTarget   string
}

type materializationIntent struct {
	coordinates materializationCoordinates
	running     chan struct{}
	waiters     int
	idle        *list.Element
}

type materializationCoordinator struct {
	mu         sync.Mutex
	operations map[string]*materializationIntent
	idle       list.List
	idleLimit  int
	targets    map[string]chan struct{}
}

func newMaterializationCoordinator() *materializationCoordinator {
	return newMaterializationCoordinatorWithIdleLimit(sourceControlIdleOperationLimit)
}

func newMaterializationCoordinatorWithIdleLimit(idleLimit int) *materializationCoordinator {
	if idleLimit < 0 {
		idleLimit = 0
	}
	return &materializationCoordinator{
		operations: make(map[string]*materializationIntent),
		idleLimit:  idleLimit,
		targets:    make(map[string]chan struct{}),
	}
}

//nolint:funlen // Lock ownership, waiter accounting, idempotency, and wakeup stay in one critical-section loop.
func (coordinator *materializationCoordinator) acquireOperation(
	ctx context.Context,
	coordinates materializationCoordinates,
) (func(), error) {
	key := coordinates.workspaceKey + "\x00" + coordinates.materializationID
	var waitingOn *materializationIntent
	for {
		coordinator.mu.Lock()
		if waitingOn != nil {
			waitingOn.waiters--
			waitingOn = nil
		}
		intent := coordinator.operations[key]
		if intent == nil {
			intent = &materializationIntent{coordinates: coordinates}
			coordinator.operations[key] = intent
		} else if intent.coordinates != coordinates {
			coordinator.touchMaterializationIntent(intent)
			coordinator.mu.Unlock()
			return nil, ErrIdempotencyConflict
		}
		if intent.running == nil {
			coordinator.removeMaterializationIntentFromIdle(intent)
			intent.running = make(chan struct{})
			done := intent.running
			coordinator.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					coordinator.mu.Lock()
					if intent.running == done {
						intent.running = nil
						close(done)
						coordinator.cacheMaterializationIntent(key, intent)
					}
					coordinator.mu.Unlock()
				})
			}, nil
		}
		done := intent.running
		intent.waiters++
		waitingOn = intent
		coordinator.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			coordinator.mu.Lock()
			waitingOn.waiters--
			coordinator.cacheMaterializationIntent(key, waitingOn)
			waitingOn = nil
			coordinator.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

func (coordinator *materializationCoordinator) touchMaterializationIntent(intent *materializationIntent) {
	if intent.idle != nil {
		coordinator.idle.MoveToBack(intent.idle)
	}
}

func (coordinator *materializationCoordinator) removeMaterializationIntentFromIdle(intent *materializationIntent) {
	if intent.idle == nil {
		return
	}
	coordinator.idle.Remove(intent.idle)
	intent.idle = nil
}

func (coordinator *materializationCoordinator) cacheMaterializationIntent(
	key string,
	intent *materializationIntent,
) {
	if intent.running != nil || intent.waiters != 0 {
		return
	}
	if intent.idle == nil {
		intent.idle = coordinator.idle.PushBack(key)
	} else {
		coordinator.idle.MoveToBack(intent.idle)
	}
	for coordinator.idle.Len() > coordinator.idleLimit {
		oldest := coordinator.idle.Front()
		oldestKey := oldest.Value.(string)
		oldestIntent := coordinator.operations[oldestKey]
		coordinator.idle.Remove(oldest)
		if oldestIntent != nil && oldestIntent.idle == oldest {
			oldestIntent.idle = nil
			delete(coordinator.operations, oldestKey)
		}
	}
}

func (coordinator *materializationCoordinator) acquireTarget(
	ctx context.Context,
	target string,
) (func(), error) {
	for {
		coordinator.mu.Lock()
		done := coordinator.targets[target]
		if done == nil {
			done = make(chan struct{})
			coordinator.targets[target] = done
			coordinator.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					coordinator.mu.Lock()
					if coordinator.targets[target] == done {
						delete(coordinator.targets, target)
						close(done)
					}
					coordinator.mu.Unlock()
				})
			}, nil
		}
		coordinator.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
