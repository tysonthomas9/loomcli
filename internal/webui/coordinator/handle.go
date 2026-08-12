package coordinator

// Resource key constants for per-workspace subsystem resources. Hooks use these
// keys with RegistrationContext.Provide and callers use them with
// WorkspaceHandle.Resource. Keys follow the "subsystem.Type" convention.
const (
	ResourceKeyPool             = "daemon.Pool"
	ResourceKeySubscriber       = "notification.Subscriber"
	ResourceKeyTerminal         = "terminal.Manager"
	ResourceKeyFleetStore       = "fleet.Store"
	ResourceKeyWorkItemsFleetDB = "workitems.fleetdb"
)

// WorkspaceHandle holds the per-workspace resources produced by LifecycleHooks
// during workspace registration. It captures the resource bag from
// RegistrationContext paired with workspace metadata. The key set is fixed at
// construction; values are shared references (not deep copies).
//
// Fields are unexported. All access goes through accessor methods.
// WorkspaceHandle is safe for concurrent read access after construction.
type WorkspaceHandle struct {
	id        string
	path      string
	resources map[string]any
}

// NewWorkspaceHandle creates a WorkspaceHandle from the given workspace
// metadata and resource map. The map is shallow-copied so that adding or
// removing entries in the original after construction has no effect. Values
// themselves are not deep-copied; pointer-typed resources (pools, subscribers)
// remain shared. A nil resources map is valid.
func NewWorkspaceHandle(id, path string, resources map[string]any) *WorkspaceHandle {
	copied := make(map[string]any, len(resources))
	for k, v := range resources {
		copied[k] = v
	}
	return &WorkspaceHandle{
		id:        id,
		path:      path,
		resources: copied,
	}
}

// ID returns the workspace ID. Returns "" on a nil receiver.
func (h *WorkspaceHandle) ID() string {
	if h == nil {
		return ""
	}
	return h.id
}

// Path returns the workspace filesystem path. Returns "" on a nil receiver.
func (h *WorkspaceHandle) Path() string {
	if h == nil {
		return ""
	}
	return h.path
}

// Resource retrieves a named resource by key. Returns (value, true) if the key
// exists, or (nil, false) otherwise. Returns (nil, false) on a nil receiver.
func (h *WorkspaceHandle) Resource(key string) (any, bool) {
	if h == nil {
		return nil, false
	}
	v, ok := h.resources[key]
	return v, ok
}
