// Package codex parses OpenAI Codex CLI's native rollout JSONL format into
// the canonical transcript.Event stream.
//
// Codex writes a rollout file per session at
// ~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl. Each line
// is an envelope {timestamp, type, payload}. The meaningful events are
// response_item entries (message / function_call / function_call_output);
// event_msg lines are higher-level duplicates we ignore.
package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// Envelope is Codex's top-level rollout line.
type Envelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"` // session_meta, event_msg, response_item, turn_context
	Payload   json.RawMessage `json:"payload"`
}

// ResponseItem is the shape of payload when Envelope.Type == "response_item".
type ResponseItem struct {
	Type      string          `json:"type"` // message, function_call, function_call_output, reasoning
	Role      string          `json:"role,omitempty"`
	Content   []ContentBlock  `json:"content,omitempty"`
	Name      string          `json:"name,omitempty"`      // function name for function_call
	Arguments string          `json:"arguments,omitempty"` // function call arguments (JSON string)
	CallID    string          `json:"call_id,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"` // function_call_output payload
}

// ContentBlock is a message content block. input_text for user/developer,
// output_text for assistant.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ParseRollout reads a Codex rollout JSONL byte slice and returns the
// envelope lines. Malformed lines are skipped.
func ParseRollout(data []byte) ([]Envelope, error) {
	var out []Envelope
	reader := bufio.NewReader(bytes.NewReader(data))

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("read rollout: %w", err)
		}
		if len(line) > 0 {
			var env Envelope
			if jerr := json.Unmarshal(line, &env); jerr == nil {
				out = append(out, env)
			}
		}
		if err == io.EOF {
			break
		}
	}
	return out, nil
}

// Events parses a Codex rollout JSONL and returns the canonical event stream.
// Only response_item entries are surfaced; the rest are operational noise.
func Events(data []byte) ([]transcript.Event, error) {
	envelopes, err := ParseRollout(data)
	if err != nil {
		return nil, err
	}

	var out []transcript.Event
	seq := 0
	for _, env := range envelopes {
		if env.Type != "response_item" {
			continue
		}
		ts := parseCodexTime(env.Timestamp)

		var item ResponseItem
		if err := json.Unmarshal(env.Payload, &item); err != nil {
			continue
		}

		switch item.Type {
		case "message":
			role := canonicalRole(item.Role)
			for _, block := range item.Content {
				if block.Text == "" {
					continue
				}
				text := block.Text
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
				})
				seq++
			}
		case "function_call":
			out = append(out, transcript.Event{
				Seq:       seq,
				Timestamp: ts,
				Role:      transcript.RoleAssistant,
				Type:      transcript.EventToolUse,
				ToolName:  item.Name,
				ToolUseID: item.CallID,
				ToolInput: json.RawMessage(item.Arguments),
			})
			seq++
		case "function_call_output":
			output := ""
			// Codex output is typically a string; sometimes a structured object.
			var str string
			if err := json.Unmarshal(item.Output, &str); err == nil {
				output = str
			} else {
				output = string(item.Output)
			}
			out = append(out, transcript.Event{
				Seq:       seq,
				Timestamp: ts,
				Role:      transcript.RoleTool,
				Type:      transcript.EventToolResult,
				ToolUseID: item.CallID,
				Output:    output,
			})
			seq++
		}
	}
	return out, nil
}

func canonicalRole(r string) string {
	switch r {
	case "user":
		return transcript.RoleUser
	case "assistant":
		return transcript.RoleAssistant
	case "developer", "system":
		return transcript.RoleSystem
	default:
		return transcript.RoleSystem
	}
}

func parseCodexTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return time.Time{}
}
