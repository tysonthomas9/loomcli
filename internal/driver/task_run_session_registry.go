package driver

import (
	"sort"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/store"
)

// TaskRunSessionOpenRegistry is the serve-process-only live view populated by
// taskrunapi after a session-open succeeds. It is deliberately advisory: the
// reconciler always queries AgentSessions for correctness.
type TaskRunSessionOpenRegistry struct {
	mu   sync.RWMutex
	refs map[taskRunSessionRegistryKey]map[string]store.SessionRef
}

type taskRunSessionRegistryKey struct {
	workspace string
	taskRunID string
	attempt   int
	fence     int64
}

func NewTaskRunSessionOpenRegistry() *TaskRunSessionOpenRegistry {
	return &TaskRunSessionOpenRegistry{refs: make(map[taskRunSessionRegistryKey]map[string]store.SessionRef)}
}

// Record posts one successfully-opened session into the run-scoped registry.
func (r *TaskRunSessionOpenRegistry) Record(run store.SessionRunContext, ref store.SessionRef) {
	if r == nil || run.WorkspaceKey == "" || run.TaskRunID == "" || ref.SessionID == "" {
		return
	}
	key := taskRunSessionRegistryKey{run.WorkspaceKey, run.TaskRunID, run.Attempt, run.FencingToken}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refs[key] == nil {
		r.refs[key] = make(map[string]store.SessionRef)
	}
	r.refs[key][ref.SessionID] = ref
}

// Live returns a stable snapshot for serve-hosted bridge visibility.
func (r *TaskRunSessionOpenRegistry) Live(run store.SessionRunContext) []store.SessionRef {
	if r == nil {
		return nil
	}
	key := taskRunSessionRegistryKey{run.WorkspaceKey, run.TaskRunID, run.Attempt, run.FencingToken}
	r.mu.RLock()
	defer r.mu.RUnlock()
	refs := r.refs[key]
	out := make([]store.SessionRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

// Forget drops the advisory live view once the bridge reaches its finish
// barrier. Store discovery remains authoritative for any later backstop pass.
func (r *TaskRunSessionOpenRegistry) Forget(run store.SessionRunContext) {
	if r == nil {
		return
	}
	key := taskRunSessionRegistryKey{run.WorkspaceKey, run.TaskRunID, run.Attempt, run.FencingToken}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.refs, key)
}
