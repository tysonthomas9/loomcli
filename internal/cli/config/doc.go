// Package config loads, validates, and resolves loom's on-disk configuration.
//
// GetConfigDir and GetWorkspaceDir locate the config tree; the loaded result is
// cached, and InvalidateConfigCache drops that cache when a command has
// rewritten configuration underneath it.
//
// Resolution is where the real work is. An agent entry names repos that must be
// checked against the workspace's repo list — ResolveAgentRepos expands that
// reference and ValidateAgentRepos rejects an agent pointing at a repo the
// workspace does not have. OverlayDaemonSettings merges a narrower settings
// block over a broader one, which is how per-role settings override daemon
// defaults without either side needing to know the other's full shape.
//
// The package also owns agent checkpoints: SaveCheckpoint and ClearCheckpoint
// persist a Checkpoint to .agent.checkpoint.json in an agent's lock directory,
// letting an interrupted run resume rather than restart. TruncateDiff bounds a
// captured diff to MaxDiffBytes before it is stored or sent to a model.
//
// BoolPtr and IntPtr exist because configuration distinguishes "unset" from
// "set to the zero value", which requires pointer fields.
package config
