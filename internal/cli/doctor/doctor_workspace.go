package doctor

import (
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// unionWorkspacePath returns the active workspace directory, or "" when there
// is none. It is a var so tests can supply a directory without a live fleet-db
// binary on PATH, the same seam fleetHealthProbe uses.
//
// It originated in PUPPET-349's union_merged_not_closed check, which now lives
// outside loomcli (loom-local-ops' union-closed-watch). The decomposed-label
// checks (#635, #709) still read the workspace's integration.yaml through it,
// so the helper stays here on its own.
var unionWorkspacePath = func() string {
	ws, err := cfgpkg.ResolveActiveWorkspace()
	if err != nil || ws == nil {
		return ""
	}
	return ws.Path
}
