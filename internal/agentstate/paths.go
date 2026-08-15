// Package agentstate owns the workspace-local filesystem conventions for
// durable scripted-role instance state.
package agentstate

import "path/filepath"

const PendingAgentsFilename = "agents.md.pending"

// InstanceDir returns the state directory for one grammar-validated agent
// service ID. Callers own validation before passing an ID here.
func InstanceDir(workspaceRoot, serviceID string) string {
	return filepath.Join(workspaceRoot, ".loom", "agents", serviceID)
}

// JournalPath returns the catalog-named journal path for one agent instance.
func JournalPath(workspaceRoot, serviceID, filename string) string {
	return filepath.Join(InstanceDir(workspaceRoot, serviceID), filename)
}

// PendingAgentsPath returns the per-instance staged agents.md path.
func PendingAgentsPath(workspaceRoot, serviceID string) string {
	return filepath.Join(InstanceDir(workspaceRoot, serviceID), PendingAgentsFilename)
}
