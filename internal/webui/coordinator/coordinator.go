// Package coordinator provides the LifecycleHook interface and WorkspaceRegistry
// for managing per-workspace subsystem lifecycle in the webui layer. Hooks
// register at startup; the registry orchestrates them on workspace Register
// (in order) and Deregister (in reverse order), with rollback on critical failure.
package coordinator

import (
	"errors"
	"log/slog"
)

// Sentinel errors for WorkspaceRegistry operations.
var (
	ErrRegistryClosed     = errors.New("workspace registry is closed")
	ErrEmptyWorkspaceID   = errors.New("workspace ID must not be empty")
	ErrEmptyWorkspacePath = errors.New("workspace path must not be empty")
	ErrDuplicateHookName  = errors.New("duplicate hook name")
)

// LifecycleHook defines a per-workspace lifecycle callback. Implementations
// manage one subsystem (e.g., connection pool, notification subscriber, fleet
// store). The registry calls hooks in registration order on Register and in
// reverse order on Deregister/Rollback.
type LifecycleHook interface {
	// Name returns a human-readable name for logging (e.g., "daemon-pool",
	// "notification-subscriber"). Must be unique per registry instance.
	Name() string

	// OnRegister is called when a workspace is being registered. The
	// RegistrationContext carries the workspace ID, path, and any resources
	// produced by earlier hooks (via the Provide/Resolve pattern). The context
	// is shared across hooks — Provide in one hook is visible via Resolve in
	// the next.
	//
	// Return nil to indicate success. Return a non-nil error to indicate failure.
	// If Critical() returns true, a failure here triggers rollback of all
	// previously-succeeded hooks for this workspace.
	OnRegister(ctx *RegistrationContext) error

	// OnDeregister is called when a workspace is being removed. Errors are
	// logged but never propagated — deregistration is best-effort and must
	// not block.
	OnDeregister(ctx DeregistrationContext)

	// Critical returns whether a failure in OnRegister should trigger rollback
	// of previously-succeeded hooks. Non-critical hooks log errors and continue.
	Critical() bool

	// OnRollback is called (in reverse order) for hooks that previously
	// succeeded in OnRegister, when a later critical hook fails. Errors are
	// logged but never propagated.
	OnRollback(ctx DeregistrationContext)
}

// RegistrationContext carries workspace metadata and a resource bag through the
// hook chain during workspace registration.
type RegistrationContext struct {
	WorkspaceID   string
	WorkspacePath string
	Logger        *slog.Logger
	resources     map[string]any
}

// Provide stores a named resource for downstream hooks to consume.
// For example, the daemon-pool hook provides the daemon.Pool it created,
// and the notification-subscriber hook resolves it.
func (rc *RegistrationContext) Provide(key string, value any) {
	if rc.resources == nil {
		rc.resources = make(map[string]any)
	}
	rc.resources[key] = value
}

// Resolve retrieves a named resource provided by an earlier hook.
// Returns (nil, false) if the key was not provided.
func (rc *RegistrationContext) Resolve(key string) (any, bool) {
	if rc.resources == nil {
		return nil, false
	}
	v, ok := rc.resources[key]
	return v, ok
}

// Resources returns a shallow copy of the resource map. Used by the registry
// to snapshot resources into a WorkspaceHandle after all hooks have run.
func (rc *RegistrationContext) Resources() map[string]any {
	copied := make(map[string]any, len(rc.resources))
	for k, v := range rc.resources {
		copied[k] = v
	}
	return copied
}

// DeregistrationContext carries workspace metadata for deregistration and
// rollback callbacks. It does not carry a resource bag — hooks are expected
// to track their own per-workspace state internally.
type DeregistrationContext struct {
	WorkspaceID string
	Logger      *slog.Logger
}
