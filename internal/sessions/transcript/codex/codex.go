// Package codex parses OpenAI Codex CLI's native rollout JSONL into the
// canonical transcript.Event stream by DELEGATING to harness-wrapper's codex
// parser — the per-harness Codex parsing knowledge lives in one place (the
// wrapper). The wrapper's tool-aware events are mapped into loom's
// transcript.Event. Field-level parity with loom's former in-tree parser is
// guarded by wrapper_parity_test.go.
//
// SHIM (stopgap — remove once upstream lands): codex-cli >= 0.144 records its
// freeform `exec` tool as response_item payloads of type "custom_tool_call" /
// "custom_tool_call_output", whereas harness-wrapper (through v0.7.5) only
// understands the legacy "function_call" / "function_call_output" schema. The
// wrapper silently drops unrecognized tool items, so codex transcripts render
// with ZERO tool calls. normalizeCustomToolCalls rewrites those items into the
// function_call schema the wrapper understands, BEFORE delegating. Tracking: an
// upstream PR to olesho/harness-wrapper adds native handling; drop this shim and
// bump the dep once it releases.
package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"

	hwcodex "github.com/olesho/harness-wrapper/pkg/transcript/codex"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// Events parses a Codex rollout JSONL into the canonical event stream
// (response_item message → text, function_call → tool_use, function_call_output
// → tool_result), delegating the parse to harness-wrapper's codex reader. A
// normalization pass first rewrites codex's newer custom_tool_call* tool items
// into the function_call* schema the wrapper understands (see package doc).
func Events(data []byte) ([]transcript.Event, error) {
	normalized, unknown := normalizeCustomToolCalls(data)
	if len(unknown) > 0 {
		slog.Warn("codex transcript: unrecognized response_item payload type(s); tool calls may be dropped (possible codex schema drift)",
			"types", unknown)
	}
	wevs, err := hwcodex.Events(normalized)
	if err != nil {
		return nil, err //nolint:wrapcheck // caller adds context
	}
	out := make([]transcript.Event, len(wevs))
	for i, w := range wevs {
		out[i] = transcript.FromWrapper(w)
	}
	return out, nil
}

// normalizeCustomToolCalls rewrites custom_tool_call / custom_tool_call_output
// response_item lines into the legacy function_call / function_call_output
// shape. Every other line passes through byte-for-byte. It returns the rewritten
// stream plus a sorted list of any UNKNOWN response_item payload types seen (for
// drift alerting); known-but-skipped types such as "reasoning" are not reported.
func normalizeCustomToolCalls(data []byte) ([]byte, []string) {
	var buf bytes.Buffer
	// Most lines pass through verbatim, so the output is ~the size of the input.
	buf.Grow(len(data))
	unknown := map[string]struct{}{}
	// bufio.ReadBytes (not Scanner) — codex tool I/O lines have no length cap,
	// matching the wrapper's own ParseRollout.
	r := bufio.NewReader(bytes.NewReader(data))
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			buf.Write(rewriteLine(line, unknown))
		}
		if err != nil {
			break
		}
	}
	types := make([]string, 0, len(unknown))
	for t := range unknown {
		types = append(types, t)
	}
	sort.Strings(types)
	return buf.Bytes(), types
}

// rewriteLine rewrites a single rollout line when it is a response_item carrying
// a custom_tool_call(_output); otherwise it returns the original bytes unchanged
// (preserving timestamps, key order, and the trailing newline).
func rewriteLine(line []byte, unknown map[string]struct{}) []byte {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return line
	}
	// Only response_item lines can be rewritten or count as payload drift. A
	// substring probe skips the two map unmarshals below for every other line
	// (event_msg, turn_context, session_meta) — most of a rollout.
	if !bytes.Contains(trimmed, []byte(`"response_item"`)) {
		return line
	}
	var env map[string]json.RawMessage
	if json.Unmarshal(trimmed, &env) != nil {
		return line
	}
	if jsonString(env["type"]) != "response_item" {
		return line
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(env["payload"], &payload) != nil {
		return line
	}

	switch pt := jsonString(payload["type"]); pt {
	case "custom_tool_call":
		payload["type"] = jsonBytes("function_call")
		// codex records the freeform tool script under "input" (a JSON string
		// whose CONTENT is a JS script, not JSON). The wrapper does
		// json.RawMessage(item.Arguments), so "arguments" must decode to a valid
		// JSON value; re-encode the input so its decoded form is a valid JSON
		// string literal of the script.
		if in, ok := payload["input"]; ok {
			payload["arguments"] = jsonBytes(string(in))
			delete(payload, "input")
		}
	case "custom_tool_call_output":
		payload["type"] = jsonBytes("function_call_output")
		// output is an array of {type,text} blocks; flatten to one string so the
		// wrapper's decodeFunctionOutput returns readable text (an array would be
		// stringified as raw JSON).
		if out, ok := payload["output"]; ok {
			payload["output"] = jsonBytes(flattenToolOutput(out))
		}
	case "message", "reasoning", "function_call", "function_call_output", "web_search_call":
		return line // known type; nothing to rewrite
	default:
		// Outside the cases above = schema drift: report it (fail-loud) rather
		// than letting the wrapper silently drop the item.
		if pt != "" {
			unknown[pt] = struct{}{}
		}
		return line
	}

	np, err := json.Marshal(payload)
	if err != nil {
		return line
	}
	env["payload"] = np
	nl, err := json.Marshal(env)
	if err != nil {
		return line
	}
	if bytes.HasSuffix(line, []byte("\n")) {
		nl = append(nl, '\n')
	}
	return nl
}

// flattenToolOutput turns a custom_tool_call_output "output" value into a single
// string. Known text blocks are joined, empty arrays stay empty, and unknown
// structured output is preserved as raw JSON rather than silently discarded.
func flattenToolOutput(raw json.RawMessage) string {
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		if len(blocks) == 0 {
			return ""
		}
		var b strings.Builder
		for _, bl := range blocks {
			b.WriteString(bl.Text)
		}
		if b.Len() > 0 {
			return b.String()
		}
		return string(raw)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var block struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &block) == nil && block.Text != "" {
		return block.Text
	}
	return string(raw)
}

// jsonString decodes a JSON string RawMessage to its Go string value ("" on
// error or non-string).
func jsonString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// jsonBytes marshals v to a RawMessage (used for small, always-encodable values).
func jsonBytes(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
