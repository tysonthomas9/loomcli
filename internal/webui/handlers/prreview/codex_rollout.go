package prreview

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	codextranscript "github.com/tysonthomas9/loomcli/internal/sessions/transcript/codex"
)

// findCodexRolloutPath locates the on-disk rollout JSONL for a codex thread.
// App-server thread/read returns only chat-summary items (user/agent messages);
// tool calls live in the rollout as custom_tool_call* and must be parsed from
// the file — same path the Runs-tab transcript reader uses.
func findCodexRolloutPath(rt leadcontrol.CodexRuntimeMetadata) (string, error) {
	threadID := strings.TrimSpace(rt.ThreadID)
	if threadID == "" {
		return "", fmt.Errorf("codex thread id required")
	}
	if path := rolloutPathByGlob(threadID); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("codex rollout not found for thread %s", threadID)
}

func rolloutPathByGlob(threadID string) string {
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(userHome) == "" {
			return ""
		}
		home = filepath.Join(userHome, ".codex")
	}
	sessions := filepath.Join(home, "sessions")
	var found string
	_ = filepath.WalkDir(sessions, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "rollout-") && strings.Contains(name, threadID) && strings.HasSuffix(name, ".jsonl") {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
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
