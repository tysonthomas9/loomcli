// Package workspaceid provides process-local formatting and environment
// lookup helpers for workspace identifiers. It owns no Workspace capability
// state or policy.
package workspaceid

import "os"

// Short returns the first 8 characters of a workspace UUID for use in local
// process names. It returns "default" when id is empty.
func Short(id string) string {
	if id == "" {
		return "default"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Resolve returns id when non-empty, otherwise LOOM_WORKSPACE_ID. An absent
// environment value produces the empty string.
func Resolve(id string) string {
	if id != "" {
		return id
	}
	return os.Getenv("LOOM_WORKSPACE_ID")
}
