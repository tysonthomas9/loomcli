package workspace

import "os"

// ShortWorkspaceID returns the first 8 characters of a workspace UUID for use
// in tmux session names. Returns "_default" if the ID is empty.
func ShortWorkspaceID(id string) string {
	if id == "" {
		return "_default"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ResolveWorkspaceID returns the workspace ID from the given value, falling
// back to the LOOM_WORKSPACE_ID environment variable, then empty string.
// Use ShortWorkspaceID on the result to get a "_default" fallback for session naming.
func ResolveWorkspaceID(id string) string {
	if id != "" {
		return id
	}
	if env := os.Getenv("LOOM_WORKSPACE_ID"); env != "" {
		return env
	}
	return ""
}
