package runtime //nolint:revive // The approved target architecture names this platform mechanism runtime.

import "os"

// ShortWorkspaceID returns the first eight characters of a workspace UUID for
// use in process-local names. An empty identity maps to the stable "default"
// segment used by legacy local-mode sessions.
func ShortWorkspaceID(id string) string {
	if id == "" {
		return "default"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ResolveWorkspaceID returns the explicit identity when present, otherwise it
// reads the process-local LOOM_WORKSPACE_ID binding. Capability state and
// Workspace policy do not cross this platform helper.
func ResolveWorkspaceID(id string) string {
	if id != "" {
		return id
	}
	return os.Getenv("LOOM_WORKSPACE_ID")
}
