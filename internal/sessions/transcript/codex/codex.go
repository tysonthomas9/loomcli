// Package codex parses OpenAI Codex CLI's native rollout JSONL into the
// canonical transcript.Event stream by DELEGATING to harness-wrapper's codex
// parser — the per-harness Codex parsing knowledge now lives in one place (the
// wrapper). The wrapper's tool-aware events are mapped into loom's
// transcript.Event (identical public fields; the wrapper's internal
// Source/NativeID are not part of loom's Event). Field-level parity with loom's
// former in-tree parser is guarded by wrapper_parity_test.go.
package codex

import (
	"encoding/json"
	"strings"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"
	hwcodex "github.com/olesho/harness-wrapper/pkg/transcript/codex"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// Events parses a Codex rollout JSONL into the canonical event stream
// (response_item message → text, reasoning → reasoning, function_call →
// tool_use, function_call_output → tool_result), delegating existing surfaced
// items to harness-wrapper's codex reader and splicing in loom's reasoning
// event type from raw response_item/reasoning payloads.
func Events(data []byte) ([]transcript.Event, error) {
	if events, modern := modernEvents(data); modern {
		return events, nil
	}
	wevs, err := hwcodex.Events(data)
	if err != nil {
		return nil, err //nolint:wrapcheck // caller adds context
	}
	out := make([]transcript.Event, len(wevs))
	for i, w := range wevs {
		out[i] = transcript.FromWrapper(w)
	}
	return mergeReasoningEvents(data, out)
}

type responseItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`
	Content   []contentBlock  `json:"content,omitempty"`
	Summary   json.RawMessage `json:"summary,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func mergeReasoningEvents(data []byte, wrapperEvents []transcript.Event) ([]transcript.Event, error) {
	envelopes, err := hwcodex.ParseRollout(data)
	if err != nil {
		return nil, err //nolint:wrapcheck // same parser as hwcodex.Events
	}

	merged := make([]transcript.Event, 0, len(wrapperEvents))
	wrapperIndex := 0
	foundReasoning := false

	for _, env := range envelopes {
		if env.Type != "response_item" {
			continue
		}

		var item responseItem
		if err := json.Unmarshal(env.Payload, &item); err != nil {
			continue
		}

		switch item.Type {
		case "message":
			appendWrapperEvents(&merged, wrapperEvents, &wrapperIndex, wrapperMessageEventCount(item))
		case "function_call", "function_call_output":
			appendWrapperEvents(&merged, wrapperEvents, &wrapperIndex, 1)
		case "reasoning":
			text := extractReasoningText(item)
			if text == "" {
				continue
			}
			foundReasoning = true
			merged = append(merged, transcript.Event{
				Timestamp: parseCodexTime(env.Timestamp),
				Role:      transcript.RoleAssistant,
				Type:      transcript.EventReasoning,
				Text:      text,
			})
		}
	}

	appendWrapperEvents(&merged, wrapperEvents, &wrapperIndex, len(wrapperEvents)-wrapperIndex)

	if !foundReasoning {
		return wrapperEvents, nil
	}
	for i := range merged {
		merged[i].Seq = i
	}
	return merged, nil
}

func appendWrapperEvents(out *[]transcript.Event, wrapperEvents []transcript.Event, index *int, count int) {
	if count <= 0 || *index >= len(wrapperEvents) {
		return
	}
	end := *index + count
	if end > len(wrapperEvents) {
		end = len(wrapperEvents)
	}
	*out = append(*out, wrapperEvents[*index:end]...)
	*index = end
}

func wrapperMessageEventCount(item responseItem) int {
	role := canonicalWrapperRole(item.Role)
	count := 0
	for _, block := range item.Content {
		text := block.Text
		if role == hwtranscript.RoleUser {
			text = hwtranscript.StripIDEContextTags(text)
		}
		if text == "" {
			continue
		}
		count++
	}
	return count
}

func canonicalWrapperRole(role string) string {
	switch role {
	case hwtranscript.RoleUser:
		return hwtranscript.RoleUser
	case hwtranscript.RoleAssistant:
		return hwtranscript.RoleAssistant
	default:
		return hwtranscript.RoleSystem
	}
}

func extractReasoningText(item responseItem) string {
	var parts []string
	parts = appendReasoningSummary(parts, item.Summary)
	for _, block := range item.Content {
		if block.Type == "reasoning_text" {
			parts = appendNonEmptyReasoningText(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func appendReasoningSummary(parts []string, raw json.RawMessage) []string {
	if len(raw) == 0 {
		return parts
	}

	var summary string
	if err := json.Unmarshal(raw, &summary); err == nil {
		return appendNonEmptyReasoningText(parts, summary)
	}

	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return parts
	}
	for _, block := range blocks {
		if block.Type == "summary_text" {
			parts = appendNonEmptyReasoningText(parts, block.Text)
		}
	}
	return parts
}

func appendNonEmptyReasoningText(parts []string, text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return parts
	}
	return append(parts, text)
}

func parseCodexTime(value string) time.Time {
	if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return ts
	}
	return time.Time{}
}
