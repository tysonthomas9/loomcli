// Package log resolves where loom's logs live and reads them back for the UI.
//
// The path helpers form a hierarchy — GetLogDir, then GetWorkspaceLogDir, then
// GetAgentLogPath and GetTaskLogDir / GetTaskLogPath — so a log is addressed
// by what produced it rather than by a path the caller assembles.
// ListTaskPhases enumerates the phases a task has logs for, which is what lets
// the UI offer them without knowing the naming convention.
//
// Reads are tail-oriented and bounded by LogReadDefaultLines: the UI wants the
// end of a growing file, and an unbounded read of an active agent's log is a
// way to exhaust memory.
package log
