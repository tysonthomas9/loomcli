// Package agentprofiles reports which agents a workspace has configured, by
// reading the per-agent directories under its runtime dir.
//
// It is a leaf: it imports only the standard library, so health readers can
// take the allowlist without pulling the cli package's dependencies in, and it
// keeps internal/cli at its recorded file-count ceiling (see
// scripts/package-size-allow.txt).
package agentprofiles

import (
	"os"
	"path/filepath"
	"strings"
)

// configuredAgentsDir is the subdirectory of a workspace runtime directory that
// holds one directory per configured agent.
const configuredAgentsDir = "profiles"

// ConfiguredAgentNames returns the agent names configured for the workspace
// rooted at runtimeDir: the subdirectories of <runtimeDir>/profiles, excluding
// names beginning with "_" (e.g. _templates).
//
// It returns nil when the directory is absent or unreadable. Callers MUST treat
// nil as "do not filter", so a misconfigured workspace degrades to unfiltered
// behavior rather than reporting zero runs.
//
// Trade-off: a retired or renamed agent disappears from historical statistics
// once its profile directory is removed. That is acceptable for the health
// checks, which look at short recent windows, and is why the resulting
// allowlist is not applied to the full-fidelity `loom sessions` view.
func ConfiguredAgentNames(runtimeDir string) []string {
	if strings.TrimSpace(runtimeDir) == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(runtimeDir, configuredAgentsDir))
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "_") {
			continue
		}
		names = append(names, name)
	}
	return names
}
