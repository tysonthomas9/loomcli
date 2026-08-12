package sessions

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/artifacttranscript"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts/transcript"
)

// Portions of the OpenCode export mapping were ported from
// github.com/entireio/cli cmd/entire/cli/agent/opencode/ (MIT, (c) 2026 Entire
// Inc.). See internal/infra/artifacttranscript/ORIGIN.md for attribution.
type openCodeExport struct {
	Messages []openCodeMessage `json:"messages"`
}

type openCodeMessage struct {
	Info  openCodeMessageInfo `json:"info"`
	Parts []openCodePart      `json:"parts"`
}

type openCodeMessageInfo struct {
	ID   string       `json:"id"`
	Role string       `json:"role"`
	Time openCodeTime `json:"time"`
}

type openCodeTime struct {
	Created int64 `json:"created"`
}

type openCodePart struct {
	Type   string             `json:"type"`
	Text   string             `json:"text,omitempty"`
	Tool   string             `json:"tool,omitempty"`
	CallID string             `json:"callID,omitempty"`
	State  *openCodeToolState `json:"state,omitempty"`
}

type openCodeToolState struct {
	Input  map[string]any `json:"input,omitempty"`
	Output string         `json:"output,omitempty"`
}

func parseOpenCodeEvents(data []byte) ([]transcript.Event, error) {
	var export openCodeExport
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("parse OpenCode transcript: %w", err)
	}

	var events []transcript.Event
	for _, message := range export.Messages {
		timestamp := time.UnixMilli(message.Info.Time.Created)
		role := openCodeCanonicalRole(message.Info.Role)
		for _, part := range message.Parts {
			switch part.Type {
			case "text":
				text := part.Text
				if role == transcript.RoleUser {
					text = artifacttranscript.StripIDEContextTags(text)
				}
				if text == "" {
					continue
				}
				events = append(events, transcript.Event{
					Seq: len(events), Timestamp: timestamp, Role: role,
					Type: transcript.EventText, Text: text, UUID: message.Info.ID,
				})
			case "tool":
				var input json.RawMessage
				if part.State != nil {
					input, _ = json.Marshal(part.State.Input)
				}
				events = append(events, transcript.Event{
					Seq: len(events), Timestamp: timestamp, Role: transcript.RoleAssistant,
					Type: transcript.EventToolUse, ToolName: part.Tool,
					ToolUseID: part.CallID, ToolInput: input, UUID: message.Info.ID,
				})
				if part.State != nil && part.State.Output != "" {
					events = append(events, transcript.Event{
						Seq: len(events), Timestamp: timestamp, Role: transcript.RoleTool,
						Type: transcript.EventToolResult, ToolName: part.Tool,
						ToolUseID: part.CallID, Output: part.State.Output, UUID: message.Info.ID,
					})
				}
			}
		}
	}
	return events, nil
}

func openCodeCanonicalRole(role string) string {
	switch role {
	case "user":
		return transcript.RoleUser
	case "assistant":
		return transcript.RoleAssistant
	default:
		return transcript.RoleSystem
	}
}
