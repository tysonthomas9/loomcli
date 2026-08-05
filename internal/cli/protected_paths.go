package cli

import (
	"path/filepath"
	"strings"
)

// ProtectedRuntimePaths are workspace-local state roots that cleanup and
// recovery must preserve.
var ProtectedRuntimePaths = []string{
	".loom",
	"sessions",
	"AGENTS.md",
}

// IsProtectedRuntimePath reports whether relPath falls within protected local
// runtime state.
func IsProtectedRuntimePath(relPath string) bool {
	clean := filepath.ToSlash(filepath.Clean(relPath))
	clean = strings.TrimPrefix(clean, "./")
	for _, protected := range ProtectedRuntimePaths {
		if clean == protected || strings.HasPrefix(clean, protected+"/") {
			return true
		}
	}
	return false
}
