package cli

import "os"

const readOnlyPreamble = `IMPORTANT: You are running in READ-ONLY mode. You MUST NOT modify any files, create new files, or run destructive commands. You may only read files, search code, and provide analysis/comments. Use bd commands to comment on tasks but do not make code changes.`

// ReadOnlyPreamble returns the read-only instruction preamble if LOOM_READ_ONLY is set.
// Returns empty string if not in read-only mode.
func ReadOnlyPreamble() string {
	if os.Getenv("LOOM_READ_ONLY") == "1" {
		return readOnlyPreamble
	}
	return ""
}
