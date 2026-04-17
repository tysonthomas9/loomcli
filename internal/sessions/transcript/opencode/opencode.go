// Package opencode parses OpenCode's native `opencode export` JSON transcript
// into the canonical transcript.Event stream.
//
// Portions ported from github.com/entireio/cli cmd/entire/cli/agent/opencode/
// (MIT, (c) 2026 Entire Inc.). See ../ORIGIN.md for the shared attribution.
package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// ExportSession represents the top-level structure of `opencode export` output.
type ExportSession struct {
	Info     SessionInfo     `json:"info"`
	Messages []ExportMessage `json:"messages"`
}

// SessionInfo contains session metadata.
type SessionInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// ExportMessage is a single message in the export format.
type ExportMessage struct {
	Info  MessageInfo `json:"info"`
	Parts []Part      `json:"parts"`
}

// MessageInfo is per-message metadata.
type MessageInfo struct {
	ID        string  `json:"id"`
	SessionID string  `json:"sessionID,omitempty"`
	Role      string  `json:"role"`
	Time      Time    `json:"time"`
	Tokens    *Tokens `json:"tokens,omitempty"`
	Cost      float64 `json:"cost,omitempty"`
}

const (
	roleAssistant = "assistant"
	roleUser      = "user"
)

// Time holds message timestamps (ms since epoch).
type Time struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed,omitempty"`
}

// Tokens holds token usage from an assistant message.
type Tokens struct {
	Input     int   `json:"input"`
	Output    int   `json:"output"`
	Reasoning int   `json:"reasoning"`
	Cache     Cache `json:"cache"`
}

// Cache holds cache-related token counts.
type Cache struct {
	Read  int `json:"read"`
	Write int `json:"write"`
}

// Part is a single message part (text / tool / etc).
type Part struct {
	Type   string     `json:"type"`
	Text   string     `json:"text,omitempty"`
	Tool   string     `json:"tool,omitempty"`
	CallID string     `json:"callID,omitempty"`
	State  *ToolState `json:"state,omitempty"`
}

// ToolState is a tool's execution state.
type ToolState struct {
	Status   string             `json:"status"`
	Input    map[string]any     `json:"input,omitempty"`
	Output   string             `json:"output,omitempty"`
	Metadata *ToolStateMetadata `json:"metadata,omitempty"`
}

// ToolStateMetadata holds metadata from tool execution.
type ToolStateMetadata struct {
	Files []ToolFileInfo `json:"files,omitempty"`
}

// ToolFileInfo describes a file affected by a tool operation.
type ToolFileInfo struct {
	FilePath     string `json:"filePath"`
	RelativePath string `json:"relativePath,omitempty"`
}

// FileModificationTools are the tool names in OpenCode that modify files.
var FileModificationTools = []string{
	"edit",
	"write",
	"apply_patch",
}

// ParseExportSession parses export JSON content into an ExportSession.
// Returns nil, nil for empty input.
func ParseExportSession(data []byte) (*ExportSession, error) {
	if len(data) == 0 {
		return nil, nil //nolint:nilnil // nil for empty data is expected
	}
	var session ExportSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parse export session: %w", err)
	}
	return &session, nil
}

// ParseExportFromFile reads and parses an export session file.
// Returns nil, nil when the file does not exist.
func ParseExportFromFile(path string) (*ExportSession, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path controlled by caller
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil //nolint:nilnil
		}
		return nil, fmt.Errorf("read export: %w", err)
	}
	return ParseExportSession(data)
}

// ExtractModifiedFiles returns the deduplicated list of files modified by
// file-modification tool calls in the session.
func ExtractModifiedFiles(data []byte) ([]string, error) {
	session, err := ParseExportSession(data)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, nil
	}

	seen := make(map[string]bool)
	var files []string
	for _, msg := range session.Messages {
		if msg.Info.Role != roleAssistant {
			continue
		}
		for _, part := range msg.Parts {
			if part.Type != "tool" || part.State == nil {
				continue
			}
			if !slices.Contains(FileModificationTools, part.Tool) {
				continue
			}
			for _, p := range extractFilePaths(part.State) {
				if !seen[p] {
					seen[p] = true
					files = append(files, p)
				}
			}
		}
	}
	return files, nil
}

func extractFilePaths(state *ToolState) []string {
	if state == nil {
		return nil
	}
	if state.Metadata != nil {
		var paths []string
		for _, f := range state.Metadata.Files {
			if f.FilePath != "" {
				paths = append(paths, f.FilePath)
			}
		}
		if len(paths) > 0 {
			return paths
		}
	}
	for _, key := range []string{"filePath", "path"} {
		if v, ok := state.Input[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return []string{s}
			}
		}
	}
	return nil
}

// Events parses an OpenCode export JSON transcript and returns the canonical
// backend-agnostic event stream.
func Events(data []byte) ([]transcript.Event, error) {
	session, err := ParseExportSession(data)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, nil
	}

	var out []transcript.Event
	seq := 0
	for _, msg := range session.Messages {
		ts := time.UnixMilli(msg.Info.Time.Created)
		role := canonicalRole(msg.Info.Role)

		for _, part := range msg.Parts {
			switch part.Type {
			case "text":
				if part.Text == "" {
					continue
				}
				text := part.Text
				if role == transcript.RoleUser {
					text = transcript.StripIDEContextTags(text)
					if text == "" {
						continue
					}
				}
				out = append(out, transcript.Event{
					Seq:       seq,
					Timestamp: ts,
					Role:      role,
					Type:      transcript.EventText,
					Text:      text,
					UUID:      msg.Info.ID,
				})
				seq++
			case "tool":
				input, _ := json.Marshal(part.State.Input)
				out = append(out, transcript.Event{
					Seq:       seq,
					Timestamp: ts,
					Role:      transcript.RoleAssistant,
					Type:      transcript.EventToolUse,
					ToolName:  part.Tool,
					ToolUseID: part.CallID,
					ToolInput: input,
					UUID:      msg.Info.ID,
				})
				seq++
				if part.State != nil && part.State.Output != "" {
					out = append(out, transcript.Event{
						Seq:       seq,
						Timestamp: ts,
						Role:      transcript.RoleTool,
						Type:      transcript.EventToolResult,
						ToolName:  part.Tool,
						ToolUseID: part.CallID,
						Output:    part.State.Output,
						UUID:      msg.Info.ID,
					})
					seq++
				}
			}
		}
	}
	return out, nil
}

func canonicalRole(r string) string {
	switch r {
	case roleUser:
		return transcript.RoleUser
	case roleAssistant:
		return transcript.RoleAssistant
	default:
		return transcript.RoleSystem
	}
}
