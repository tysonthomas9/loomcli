// Package transcript is a read-only view of harness-owned conversation
// logs (Codex's ~/.codex/sessions/, Claude Code's ~/.claude/projects/).
//
// The harness-wrapper chat layer relies on the harness for durable
// transcript storage rather than duplicating it. When a caller asks
// for History, chat.Conversation locates the harness's own JSONL,
// parses it, and returns the result.
//
// Per-harness reader packages (pkg/transcript/codex,
// pkg/transcript/claudecode) implement the Reader interface here.
// The package intentionally has no dependency on pkg/turns or
// pkg/chat — it's a leaf package that can be imported anywhere.
package transcript

import "time"

// Turn is one message read from a harness session log. Fields are
// normalized across harnesses; per-harness particulars (tool calls,
// system messages, attachments) are folded into Role and Text.
type Turn struct {
	// Role is "user", "assistant", or "system". Tool-call entries that
	// don't fit those map to "system".
	Role string

	// Text is the message body. For multi-block messages the blocks are
	// joined with a single "\n\n".
	Text string

	// Timestamp is when the message was recorded. Zero if the source
	// log did not include a timestamp.
	Timestamp time.Time
}

// Reader reads a harness's persisted transcript for one session.
//
// harnessSessionID is the UUID the harness assigned to its own
// session (e.g. "019e2824-db19-72b2-bd4a-d5a5d80f74f0"). workingDir
// is the directory the chat session was opened in; some harnesses
// (Claude Code) index transcripts by working directory, so they need
// it; others (Codex) ignore it.
//
// Implementations must be safe for concurrent use. Errors surface
// for missing files, malformed lines, etc.; partial reads (some
// lines parsed, then a malformed one) should error rather than
// silently truncate.
type Reader interface {
	Read(harnessSessionID, workingDir string) ([]Turn, error)
}
