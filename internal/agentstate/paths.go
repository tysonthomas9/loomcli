// Package agentstate owns the workspace-local filesystem conventions for
// durable scripted-role instance state.
package agentstate

import "path/filepath"

const (
	// AgentsFilename is the workspace-root notes file. It is explicitly
	// multi-writer: human-authored bytes plus one fenced region per agent
	// instance, which is why merging goes through MergePendingFence rather
	// than a whole-file write.
	AgentsFilename        = "agents.md"
	PendingAgentsFilename = AgentsFilename + ".pending"
)

// AgentsPath returns the workspace-root agents.md path.
func AgentsPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, AgentsFilename)
}

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
