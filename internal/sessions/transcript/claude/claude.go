// Package claude parses Claude Code's native JSONL transcript format into the
// canonical transcript.Event stream.
//
// Portions ported from github.com/entireio/cli cmd/entire/cli/agent/claudecode/
// (MIT, (c) 2026 Entire Inc.). See ../ORIGIN.md for the shared attribution.
package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// File-modification tool names used by Claude Code. Used by ExtractModifiedFiles.
const (
	ToolWrite        = "Write"
	ToolEdit         = "Edit"
	ToolNotebookEdit = "NotebookEdit"
	ToolMCPWrite     = "mcp__acp__Write" //nolint:gosec // G101: tool name, not a credential
	ToolMCPEdit      = "mcp__acp__Edit"
)

// FileModificationTools lists tool names that create or modify files.
var FileModificationTools = []string{
	ToolWrite,
	ToolEdit,
	ToolNotebookEdit,
	ToolMCPWrite,
	ToolMCPEdit,
}

// SerializeTranscript converts transcript lines back to JSONL bytes.
func SerializeTranscript(lines []transcript.Line) ([]byte, error) {
	var buf bytes.Buffer
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			return nil, fmt.Errorf("marshal line: %w", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// TruncateAtUUID returns transcript lines up to and including the line with
// the given UUID. Returns the full slice if uuid is empty or not found.
func TruncateAtUUID(lines []transcript.Line, uuid string) []transcript.Line {
	if uuid == "" {
		return lines
	}
	for i, line := range lines {
		if line.UUID == uuid {
			return lines[:i+1]
		}
	}
	return lines
}

// ExtractModifiedFiles extracts files modified by tool calls from a transcript.
func ExtractModifiedFiles(lines []transcript.Line) []string {
	seen := make(map[string]bool)
	var files []string

	for _, line := range lines {
		if line.Type != transcript.TypeAssistant {
			continue
		}

		var msg transcript.AssistantMessage
		if err := json.Unmarshal(line.Message, &msg); err != nil {
			continue
		}

		for _, block := range msg.Content {
			if block.Type != transcript.ContentTypeToolUse {
				continue
			}
			if !isModifyTool(block.Name) {
				continue
			}

			var input transcript.ToolInput
			if err := json.Unmarshal(block.Input, &input); err != nil {
				continue
			}

			file := input.FilePath
			if file == "" {
				file = input.NotebookPath
			}
			if file != "" && !seen[file] {
				seen[file] = true
				files = append(files, file)
			}
		}
	}
	return files
}

func isModifyTool(name string) bool {
	for _, t := range FileModificationTools {
		if name == t {
			return true
		}
	}
	return false
}

// ExtractSpawnedAgentIDs returns a map of spawned subagent IDs to the Task
// tool's tool_use_id. Subagent IDs are parsed out of Task tool_result text
// where they appear as "agentId: <id>".
func ExtractSpawnedAgentIDs(lines []transcript.Line) map[string]string {
	ids := make(map[string]string)

	for _, line := range lines {
		if line.Type != transcript.TypeUser {
			continue
		}

		var msgEnvelope struct {
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(line.Message, &msgEnvelope); err != nil {
			continue
		}

		var blocks []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(msgEnvelope.Content, &blocks); err != nil {
			continue
		}

		for _, block := range blocks {
			if block.Type != "tool_result" {
				continue
			}

			text := extractToolResultText(block.Content)
			if agentID := extractAgentIDFromText(text); agentID != "" {
				ids[agentID] = block.ToolUseID
			}
		}
	}
	return ids
}

func extractToolResultText(raw json.RawMessage) string {
	// Try array of text blocks first.
	var textBlocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &textBlocks); err == nil {
		var sb strings.Builder
		for _, tb := range textBlocks {
			if tb.Type == "text" {
				sb.WriteString(tb.Text)
				sb.WriteByte('\n')
			}
		}
		return sb.String()
	}
	// Try plain string.
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	return ""
}

func extractAgentIDFromText(text string) string {
	const prefix = "agentId: "
	idx := strings.Index(text, prefix)
	if idx == -1 {
		return ""
	}
	start := idx + len(prefix)
	end := start
	for end < len(text) && isAlnum(text[end]) {
		end++
	}
	if end > start {
		return text[start:end]
	}
	return ""
}

func isAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// Events parses a Claude Code JSONL transcript and returns the canonical
// backend-agnostic event stream. Malformed lines are skipped.
func Events(data []byte) ([]transcript.Event, error) {
	lines, err := transcript.ParseFromBytes(data)
	if err != nil {
		return nil, err //nolint:wrapcheck // caller adds context
	}

	events := make([]transcript.Event, 0, len(lines))
	seq := 0

	for _, line := range lines {
		switch line.Type {
		case transcript.TypeUser:
			events = append(events, userLineEvents(line, &seq)...)
		case transcript.TypeAssistant:
			events = append(events, assistantLineEvents(line, &seq)...)
		}
	}
	return events, nil
}

func userLineEvents(line transcript.Line, seq *int) []transcript.Event {
	// User line content is either a string prompt or an array that may
	// contain text + tool_result blocks.
	var msgEnvelope struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(line.Message, &msgEnvelope); err != nil {
		return nil
	}

	// Try string content (direct user prompt).
	var str string
	if err := json.Unmarshal(msgEnvelope.Content, &str); err == nil {
		text := transcript.StripIDEContextTags(str)
		if text == "" {
			return nil
		}
		e := transcript.Event{
			Seq:       *seq,
			Role:      transcript.RoleUser,
			Type:      transcript.EventText,
			Text:      text,
			UUID:      line.UUID,
			Timestamp: time.Time{},
		}
		*seq++
		return []transcript.Event{e}
	}

	// Array content — text blocks and tool_result blocks.
	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(msgEnvelope.Content, &blocks); err != nil {
		return nil
	}

	var out []transcript.Event
	for _, b := range blocks {
		switch b.Type {
		case "text":
			txt := transcript.StripIDEContextTags(b.Text)
			if txt == "" {
				continue
			}
			out = append(out, transcript.Event{
				Seq:  *seq,
				Role: transcript.RoleUser,
				Type: transcript.EventText,
				Text: txt,
				UUID: line.UUID,
			})
			*seq++
		case "tool_result":
			out = append(out, transcript.Event{
				Seq:       *seq,
				Role:      transcript.RoleTool,
				Type:      transcript.EventToolResult,
				Output:    extractToolResultText(b.Content),
				ToolUseID: b.ToolUseID,
				UUID:      line.UUID,
			})
			*seq++
		}
	}
	return out
}

func assistantLineEvents(line transcript.Line, seq *int) []transcript.Event {
	var msg transcript.AssistantMessage
	if err := json.Unmarshal(line.Message, &msg); err != nil {
		return nil
	}

	var out []transcript.Event
	for _, block := range msg.Content {
		switch block.Type {
		case transcript.ContentTypeText:
			if block.Text == "" {
				continue
			}
			out = append(out, transcript.Event{
				Seq:  *seq,
				Role: transcript.RoleAssistant,
				Type: transcript.EventText,
				Text: block.Text,
				UUID: line.UUID,
			})
			*seq++
		case transcript.ContentTypeToolUse:
			out = append(out, transcript.Event{
				Seq:       *seq,
				Role:      transcript.RoleAssistant,
				Type:      transcript.EventToolUse,
				ToolName:  block.Name,
				ToolInput: block.Input,
				UUID:      line.UUID,
			})
			*seq++
		}
	}
	return out
}
