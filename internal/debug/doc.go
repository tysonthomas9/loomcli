// Package debug is loom's process-wide output verbosity switch.
//
// SetVerbose and SetQuiet establish the mode once at startup; Logf and Printf
// then emit only when verbose is enabled, while PrintNormal and PrintlnNormal
// carry output that should survive everything except quiet mode. Enabled and
// IsQuiet let callers skip expensive formatting they would otherwise discard.
//
// This is deliberately global state rather than a passed-in logger: it governs
// what a single CLI invocation prints to its own terminal, and threading a
// logger through every command for that purpose would be noise.
package debug
