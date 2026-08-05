package connectors

import (
	"container/list"
	"context"
	"sync"
)

// gitReadIdleOperationLimit bounds completed Git-read intents and clone
// receipts retained for exact-coordinate conflicts and clone replay. Running
// operations and callers already waiting on them remain pinned; once they
// drain, the idle cache contracts back to this limit.
const gitReadIdleOperationLimit = 1024

type gitReadCoordinates struct {
	workspaceKey    string
	operationID     string
	repositoryRef   string
	operation       GitReadOperation
	remoteURL       string
	workspacePath   string
	targetPath      string
	remoteName      string
	sourceRef       string
	destinationRef  string
	canonicalTarget string
}

type gitReadIntent struct {
	coordinates gitReadCoordinates
	running     chan struct{}
	completed   bool
	waiters     int
	idle        *list.Element
}

type gitReadCoordinator struct {
	mu         sync.Mutex
	operations map[string]*gitReadIntent
	idle       list.List
	idleLimit  int
	targets    map[string]chan struct{}
}

func newGitReadCoordinator() *gitReadCoordinator {
	return newGitReadCoordinatorWithIdleLimit(gitReadIdleOperationLimit)
}

func newGitReadCoordinatorWithIdleLimit(idleLimit int) *gitReadCoordinator {
	if idleLimit < 0 {
		idleLimit = 0
	}
	return &gitReadCoordinator{
		operations: make(map[string]*gitReadIntent),
		idleLimit:  idleLimit,
		targets:    make(map[string]chan struct{}),
	}
}

//nolint:funlen // Lock ownership, waiter accounting, replay state, and wakeup stay in one critical-section loop.
func (coordinator *gitReadCoordinator) acquireOperation(
	ctx context.Context,
	coordinates gitReadCoordinates,
) (release func(bool), replay bool, err error) {
	key := coordinates.workspaceKey + "\x00" + coordinates.operationID
	var waitingOn *gitReadIntent
	for {
		coordinator.mu.Lock()
		if waitingOn != nil {
			waitingOn.waiters--
			waitingOn = nil
		}
		intent := coordinator.operations[key]
		if intent == nil {
			intent = &gitReadIntent{coordinates: coordinates}
			coordinator.operations[key] = intent
		} else if intent.coordinates != coordinates {
			coordinator.touchGitReadIntent(intent)
			coordinator.mu.Unlock()
			return nil, false, ErrIdempotencyConflict
		}
		if intent.completed && coordinates.operation == GitReadClone {
			coordinator.cacheGitReadIntent(key, intent)
			coordinator.mu.Unlock()
			return func(bool) {}, true, nil
		}
		if intent.running == nil {
			coordinator.removeGitReadIntentFromIdle(intent)
			intent.running = make(chan struct{})
			done := intent.running
			coordinator.mu.Unlock()
			var once sync.Once
			return func(completed bool) {
				once.Do(func() {
					coordinator.mu.Lock()
					if intent.running == done {
						intent.completed = completed
						intent.running = nil
						close(done)
						coordinator.cacheGitReadIntent(key, intent)
					}
					coordinator.mu.Unlock()
				})
			}, false, nil
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
			coordinator.cacheGitReadIntent(key, waitingOn)
			waitingOn = nil
			coordinator.mu.Unlock()
			return nil, false, ctx.Err()
		}
	}
}

func (coordinator *gitReadCoordinator) touchGitReadIntent(intent *gitReadIntent) {
	if intent.idle != nil {
		coordinator.idle.MoveToBack(intent.idle)
	}
}

func (coordinator *gitReadCoordinator) removeGitReadIntentFromIdle(intent *gitReadIntent) {
	if intent.idle == nil {
		return
	}
	coordinator.idle.Remove(intent.idle)
	intent.idle = nil
}

func (coordinator *gitReadCoordinator) cacheGitReadIntent(
	key string,
	intent *gitReadIntent,
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

func (coordinator *gitReadCoordinator) acquireTarget(
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
