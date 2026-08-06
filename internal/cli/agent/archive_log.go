package agent

import (
	"fmt"
	"os"
	"path/filepath"

	webuilog "github.com/tysonthomas9/loomcli/internal/logstore"
)

// OpenAgentArchiveLog opens (creating parent directories) the per-workspace
// agent archive log that the web UI "Logs" tab reads:
// ~/.loom/logs/<workspaceID>/agents/<agentName>.log, in append mode.
//
// The path is resolved via webuilog.GetAgentLogPath — the exact resolver the
// read side uses — so writer and reader never disagree (including the
// LOOM_WORKSPACE_RUNTIME_DIR / LOOM_CONFIG_DIR overrides). The caller owns the
// returned file and must Close it.
//
// This lives in the agent package (rather than each caller importing
// internal/webui/log directly) so daemon-side callers like the supervisor can
// archive agent output without taking on an extra cross-cutting import.
func OpenAgentArchiveLog(workspaceID, agentName string) (*os.File, error) {
	archivePath, err := webuilog.GetAgentLogPath(workspaceID, agentName)
	if err != nil {
		return nil, fmt.Errorf("resolve agent archive log path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o700); err != nil {
		return nil, fmt.Errorf("create agent archive log dir: %w", err)
	}
	f, err := os.OpenFile(archivePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // G304: path validated within ~/.loom/logs by webuilog.GetAgentLogPath
	if err != nil {
		return nil, fmt.Errorf("open agent archive log: %w", err)
	}
	return f, nil
}
