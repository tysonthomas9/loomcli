package prreview

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	codextranscript "github.com/tysonthomas9/loomcli/internal/sessions/transcript/codex"
)

// codexRolloutCache memoizes ONE reviewer thread's rollout: its resolved path
// (the sessions tree holds every rollout ever recorded, so the walk is paid
// once per thread) and the messages parsed from it, reused while the file's
// size and mtime are unchanged. Both serving paths land here — the 1s SSE poll
// and the frontend's conversation poll — and a rollout is an append-only file
// that routinely reaches megabytes, so re-walking, re-reading and re-parsing it
// on every tick is pure waste.
//
// Deliberately single-entry: the reviewer panel is a focused, one-at-a-time
// view. Two panels open at once simply fall back to re-reading per poll (the
// cost before this cache existed) rather than needing an eviction policy that
// could otherwise pin whole conversations in memory for the process's life.
type codexRolloutCache struct {
	mu       sync.Mutex
	threadID string
	path     string
	loaded   bool
	size     int64
	modTime  time.Time
	msgs     []reviewerStreamMessage
}

// messagesFor returns the chat messages parsed from threadID's on-disk rollout.
// ok is false when the thread has no readable rollout yet — the caller then
// falls back to the app-server's own (tool-less) view of the conversation. The
// returned slice is a fresh copy: callers redact message fields in place, which
// must never reach through to the cached parse.
func (c *codexRolloutCache) messagesFor(threadID string) ([]reviewerStreamMessage, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.threadID != threadID {
		c.threadID, c.path, c.loaded, c.size, c.modTime, c.msgs = threadID, "", false, 0, time.Time{}, nil
	}
	if c.path == "" {
		path, err := findCodexRolloutPath(threadID)
		if err != nil {
			return nil, false
		}
		c.path = path
	}
	info, err := os.Stat(c.path)
	if err != nil {
		c.path, c.loaded = "", false // rotated or removed — re-resolve next poll
		return nil, false
	}
	if c.loaded && info.Size() == c.size && info.ModTime().Equal(c.modTime) {
		return slices.Clone(c.msgs), len(c.msgs) > 0
	}
	// #nosec G304 — controlled path: findCodexRolloutPath only returns files
	// discovered under $CODEX_HOME/sessions matching the runtime's thread id.
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, false
	}
	msgs, err := reviewerMessagesFromCodexRollout(threadID, data)
	if err != nil {
		return nil, false
	}
	c.loaded, c.size, c.modTime, c.msgs = true, info.Size(), info.ModTime(), msgs
	return slices.Clone(msgs), len(msgs) > 0
}

// findCodexRolloutPath locates the on-disk rollout JSONL for a codex thread.
// App-server thread/read returns only chat-summary items (user/agent messages);
// tool calls live in the rollout as custom_tool_call* and must be parsed from
// the file — same path the Runs-tab transcript reader uses.
func findCodexRolloutPath(threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", fmt.Errorf("codex thread id required")
	}
	root := sessions.CodexSessionsRoot()
	if root == "" {
		return "", fmt.Errorf("codex sessions dir not found")
	}
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, "-"+threadID+".jsonl") {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("codex rollout not found for thread %s", threadID)
	}
	return found, nil
}

// reviewerMessagesFromCodexRollout parses a Codex rollout JSONL into chat
// messages, including collapsed tool_use pills for custom_tool_call items.
func reviewerMessagesFromCodexRollout(threadID string, data []byte) ([]reviewerStreamMessage, error) {
	events, err := codextranscript.Events(data)
	if err != nil {
		return nil, err
	}
	hw := make([]hwtranscript.Event, 0, len(events))
	for _, e := range events {
		h := hwtranscript.Event{
			Seq:       e.Seq,
			Timestamp: e.Timestamp,
			Role:      e.Role,
			Type:      e.Type,
			Text:      e.Text,
			ToolName:  e.ToolName,
			ToolUseID: e.ToolUseID,
			ToolInput: e.ToolInput,
			Output:    e.Output,
			UUID:      e.UUID,
		}
		// NativeID pins the identity these events are keyed by. Deliberately NOT
		// Event.ID()'s own fallback order (UUID first): a codex tool item carries
		// both a UUID and a call id, and grouping tools by message UUID would
		// collapse distinct calls onto one chat row.
		switch {
		case e.ToolUseID != "":
			h.NativeID = e.Type + ":" + e.ToolUseID
		case e.UUID != "":
			h.NativeID = "msg:" + e.UUID
			if e.Seq > 0 {
				// Distinguish multiple blocks that share a message UUID.
				h.NativeID += "#" + strconv.Itoa(e.Seq)
			}
		default:
			h.NativeID = "seq:" + strconv.Itoa(e.Seq)
		}
		hw = append(hw, h)
	}
	return reviewerMessagesFromEvents("codex", threadID, hw), nil
}
