// Package codex parses OpenAI Codex CLI's native rollout JSONL into the
// canonical transcript.Event stream by DELEGATING to harness-wrapper's codex
// parser — the per-harness Codex parsing knowledge lives in one place (the
// wrapper). The wrapper's tool-aware events are mapped into loom's
// transcript.Event. Field-level parity with loom's former in-tree parser is
// guarded by wrapper_parity_test.go.
//
// codex-cli >= 0.144 records its freeform `exec` tool as response_item payloads
// of type "custom_tool_call" / "custom_tool_call_output" rather than the legacy
// "function_call" schema. The wrapper handles both natively as of v0.7.7;
// codex_customtool_test.go pins that both shapes still reach the canonical
// stream, so a dependency regression fails here rather than rendering
// transcripts with silently zero tool calls.
package codex

import (
	hwcodex "github.com/olesho/harness-wrapper/pkg/transcript/codex"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// Events parses a Codex rollout JSONL into the canonical event stream
// (response_item message → text, function_call/custom_tool_call → tool_use,
// *_output → tool_result), delegating the parse to harness-wrapper's codex
// reader.
func Events(data []byte) ([]transcript.Event, error) {
	wevs, err := hwcodex.Events(data)
	if err != nil {
		return nil, err //nolint:wrapcheck // caller adds context
	}
	out := make([]transcript.Event, len(wevs))
	for i, w := range wevs {
		out[i] = transcript.FromWrapper(w)
	}
	return out, nil
}
