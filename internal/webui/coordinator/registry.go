package coordinator

import (
	"fmt"
	"log/slog"
	"sync"
)

// activeWorkspace holds per-workspace state: the resource handle produced by
// hooks during registration and the list of hooks that succeeded (needed for
// reverse-order deregistration).
type activeWorkspace struct {
	handle    *WorkspaceHandle
	succeeded []string
}

// WorkspaceRegistry orchestrates LifecycleHook callbacks for workspace
// registration and deregistration. Hooks are called in registration order
// on Register and in reverse order on Deregister. Critical hook failures
// during Register trigger rollback of previously-succeeded hooks.
type WorkspaceRegistry struct {
	mu     sync.RWMutex
	hooks  []LifecycleHook
	active map[string]*activeWorkspace
	closed bool
	logger *slog.Logger
}

// NewWorkspaceRegistry creates a new WorkspaceRegistry.
func NewWorkspaceRegistry(logger *slog.Logger) *WorkspaceRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	return &WorkspaceRegistry{
		active: make(map[string]*activeWorkspace),
		logger: logger,
	}
}

// AddHook appends a lifecycle hook. Fails if Name() duplicates an existing hook
// or if the registry is closed. Must be called before any Register calls
// (during server init).
func (r *WorkspaceRegistry) AddHook(hook LifecycleHook) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrRegistryClosed
	}

	name := hook.Name()
	for _, h := range r.hooks {
		if h.Name() == name {
			return fmt.Errorf("%w: %q", ErrDuplicateHookName, name)
		}
	}

	r.hooks = append(r.hooks, hook)
	return nil
}

// Register runs each hook's OnRegister in order for the given workspace. On
// critical failure, previously-succeeded hooks are rolled back in reverse order.
//
// If the workspace ID is already registered, it is deregistered first (the
// previous hooks are cleaned up) before re-registering.
func (r *WorkspaceRegistry) Register(id, path string) error {
	if id == "" {
		return ErrEmptyWorkspaceID
	}
	if path == "" {
		return ErrEmptyWorkspacePath
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrRegistryClosed
	}

	// Double-register: clean up the previous registration first.
	if _, exists := r.active[id]; exists {
		r.deregisterLocked(id)
	}

	ctx := &RegistrationContext{
		WorkspaceID:   id,
		WorkspacePath: path,
		Logger:        r.logger,
	}

	var succeeded []string

	for _, hook := range r.hooks {
		err := hook.OnRegister(ctx)
		if err != nil {
			if hook.Critical() {
				r.logger.Error("critical hook failed during register, rolling back",
					"hook", hook.Name(), "workspace", id, "err", err)
				r.rollback(id, succeeded)
				return fmt.Errorf("hook %q: %w", hook.Name(), err)
			}
			r.logger.Warn("non-critical hook failed during register",
				"hook", hook.Name(), "workspace", id, "err", err)
			continue
		}
		succeeded = append(succeeded, hook.Name())
	}

	handle := NewWorkspaceHandle(id, path, ctx.Resources())
	r.active[id] = &activeWorkspace{
		handle:    handle,
		succeeded: succeeded,
	}
	return nil
}

// Deregister runs each hook's OnDeregister in reverse order for the given
// workspace. No-op for empty ID or unknown workspace.
func (r *WorkspaceRegistry) Deregister(id string) {
	if id == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.deregisterLocked(id)
}

// deregisterLocked performs deregistration under the existing lock.
func (r *WorkspaceRegistry) deregisterLocked(id string) {
	aw, ok := r.active[id]
	if !ok {
		return
	}

	deregCtx := DeregistrationContext{
		WorkspaceID: id,
		Logger:      r.logger,
	}

	// Call OnDeregister in reverse order, only for hooks that succeeded.
	for i := len(aw.succeeded) - 1; i >= 0; i-- {
		hook := r.hookByName(aw.succeeded[i])
		if hook == nil {
			continue
		}
		r.safeDeregister(hook, deregCtx)
	}

	delete(r.active, id)
}

// rollback calls OnRollback in reverse order for hooks that succeeded.
func (r *WorkspaceRegistry) rollback(id string, succeeded []string) {
	deregCtx := DeregistrationContext{
		WorkspaceID: id,
		Logger:      r.logger,
	}

	for i := len(succeeded) - 1; i >= 0; i-- {
		hook := r.hookByName(succeeded[i])
		if hook == nil {
			continue
		}
		r.safeRollback(hook, deregCtx)
	}
}

// safeDeregister calls OnDeregister with panic recovery.
func (r *WorkspaceRegistry) safeDeregister(hook LifecycleHook, ctx DeregistrationContext) {
	defer func() {
		if rv := recover(); rv != nil {
			r.logger.Error("panic in hook OnDeregister",
				"hook", hook.Name(), "workspace", ctx.WorkspaceID, "panic", rv)
		}
	}()
	hook.OnDeregister(ctx)
}

// safeRollback calls OnRollback with panic recovery.
func (r *WorkspaceRegistry) safeRollback(hook LifecycleHook, ctx DeregistrationContext) {
	defer func() {
		if rv := recover(); rv != nil {
			r.logger.Error("panic in hook OnRollback",
				"hook", hook.Name(), "workspace", ctx.WorkspaceID, "panic", rv)
		}
	}()
	hook.OnRollback(ctx)
}

// hookByName returns the hook with the given name, or nil if not found.
func (r *WorkspaceRegistry) hookByName(name string) LifecycleHook {
	for _, h := range r.hooks {
		if h.Name() == name {
			return h
		}
	}
	return nil
}

// ForWorkspace returns the WorkspaceHandle for a registered workspace, or nil
// if the workspace is not registered. The returned handle is immutable and safe
// for concurrent use. Callers can safely chain calls on a nil result because
// WorkspaceHandle methods are nil-receiver safe.
func (r *WorkspaceRegistry) ForWorkspace(id string) *WorkspaceHandle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	aw, ok := r.active[id]
	if !ok {
		return nil
	}
	return aw.handle
}

// Registered reports whether a workspace with the given ID is currently
// registered. It is safe to call on a closed registry.
func (r *WorkspaceRegistry) Registered(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.active[id]
	return ok
}

// subscriberActivator is implemented by hooks that support deferred per-workspace
// subscriber activation (e.g., the notification subscriber hook).
type subscriberActivator interface {
	Activate(wsID string) error
}

// ActivateSubscriber activates the deferred SSE subscriber for a registered
// workspace by delegating to every hook that implements subscriberActivator
// (typically the notification subscriber hook). Returns nil if no activator
// hook is present or the workspace is not currently registered — activation
// is best-effort and must not resurrect a deregistered workspace.
func (r *WorkspaceRegistry) ActivateSubscriber(id string) error {
	if id == "" {
		return ErrEmptyWorkspaceID
	}
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return ErrRegistryClosed
	}
	// Skip silently if the workspace was deregistered before the caller
	// (e.g., DaemonStartupFn's onReady callback) fired. Otherwise we'd
	// resurrect a torn-down workspace by starting a subscriber with no pool.
	if _, ok := r.active[id]; !ok {
		r.mu.RUnlock()
		return nil
	}
	hooks := append([]LifecycleHook(nil), r.hooks...)
	r.mu.RUnlock()
	var firstErr error
	for _, h := range hooks {
		activator, ok := h.(subscriberActivator)
		if !ok {
			continue
		}
		if err := activator.Activate(id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// WorkspaceIDs returns the IDs of all currently registered workspaces.
func (r *WorkspaceRegistry) WorkspaceIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	return ids
}

// Close prevents new registrations. Does NOT deregister existing workspaces —
// the server shutdown sequence handles that via individual Deregister calls.
func (r *WorkspaceRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

// HookNames returns registered hook names in order.
func (r *WorkspaceRegistry) HookNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, len(r.hooks))
	for i, h := range r.hooks {
		names[i] = h.Name()
	}
	return names
}
