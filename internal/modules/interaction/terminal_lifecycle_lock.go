package interaction

import "sync"

var agentLifecycleLocks sync.Map

// LockAgentLifecycle serializes lifecycle transitions with terminal metadata
// creation and PTY attachment for one agent. Callers must hold the returned
// unlock function across the complete operation that changes or relies on the
// agent's durable lifecycle state.
func LockAgentLifecycle(workspace, agentID string) func() {
	key := workspace + "\x00" + agentID
	actual, _ := agentLifecycleLocks.LoadOrStore(key, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
