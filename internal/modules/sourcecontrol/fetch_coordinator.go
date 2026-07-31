package sourcecontrol

import (
	"container/list"
	"context"
	"sync"
)

type refFetchCoordinates struct {
	workspaceKey    string
	operationID     string
	repositoryRef   string
	remoteURL       string
	workspacePath   string
	targetPath      string
	remoteName      string
	sourceRef       string
	destinationRef  string
	expectedCommit  string
	canonicalTarget string
}

type refFetchIntent struct {
	coordinates refFetchCoordinates
	running     chan struct{}
	waiters     int
	idle        *list.Element
}

type refFetchCoordinator struct {
	mu         sync.Mutex
	operations map[string]*refFetchIntent
	idle       list.List
	idleLimit  int
	targets    map[string]chan struct{}
}

func newRefFetchCoordinator() *refFetchCoordinator {
	return newRefFetchCoordinatorWithIdleLimit(sourceControlIdleOperationLimit)
}

func newRefFetchCoordinatorWithIdleLimit(idleLimit int) *refFetchCoordinator {
	if idleLimit < 0 {
		idleLimit = 0
	}
	return &refFetchCoordinator{
		operations: make(map[string]*refFetchIntent),
		idleLimit:  idleLimit,
		targets:    make(map[string]chan struct{}),
	}
}

//nolint:funlen // Lock ownership, waiter accounting, idempotency, and wakeup stay in one critical-section loop.
func (coordinator *refFetchCoordinator) acquireOperation(
	ctx context.Context,
	coordinates refFetchCoordinates,
) (func(), error) {
	key := coordinates.workspaceKey + "\x00" + coordinates.operationID
	var waitingOn *refFetchIntent
	for {
		coordinator.mu.Lock()
		if waitingOn != nil {
			waitingOn.waiters--
			waitingOn = nil
		}
		intent := coordinator.operations[key]
		if intent == nil {
			intent = &refFetchIntent{coordinates: coordinates}
			coordinator.operations[key] = intent
		} else if intent.coordinates != coordinates {
			coordinator.touchRefFetchIntent(intent)
			coordinator.mu.Unlock()
			return nil, ErrIdempotencyConflict
		}
		if intent.running == nil {
			coordinator.removeRefFetchIntentFromIdle(intent)
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
						coordinator.cacheRefFetchIntent(key, intent)
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
			coordinator.cacheRefFetchIntent(key, waitingOn)
			waitingOn = nil
			coordinator.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

func (coordinator *refFetchCoordinator) touchRefFetchIntent(intent *refFetchIntent) {
	if intent.idle != nil {
		coordinator.idle.MoveToBack(intent.idle)
	}
}

func (coordinator *refFetchCoordinator) removeRefFetchIntentFromIdle(intent *refFetchIntent) {
	if intent.idle == nil {
		return
	}
	coordinator.idle.Remove(intent.idle)
	intent.idle = nil
}

func (coordinator *refFetchCoordinator) cacheRefFetchIntent(
	key string,
	intent *refFetchIntent,
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

func (coordinator *refFetchCoordinator) acquireTarget(
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
