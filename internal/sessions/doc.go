// Package sessions locates, identifies, and summarizes agent harness sessions.
//
// A loom session and the harness session underneath it are not the same
// object, and reconciling them is most of what this package does.
// GenerateSessionID mints loom's own identifier, while the ClaudeConfigDir,
// ClaudeProjectsRootFor, and CodexSessionsRoot helpers resolve where a given
// backend keeps its state for a project and agent. LatestHarnessSessionID then
// picks the harness session that corresponds to a loom session, using the work
// directory, an optional hint UUID, and a start time to disambiguate.
//
// Sessions also carry a result. DiffStats and the EncodeDiffStatsMetadata /
// DecodeDiffStatsMetadata pair round-trip what a session changed through the
// string-keyed metadata map that storage exposes, and NotifyWebUI posts a
// session's terminal status to a running server.
//
// StaleSessionThreshold defines when a session with no progress is treated as
// abandoned rather than merely slow.
package sessions
