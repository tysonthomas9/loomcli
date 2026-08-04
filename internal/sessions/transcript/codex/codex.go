// Package codex parses OpenAI Codex CLI's native rollout JSONL into the
// canonical transcript.Event stream by DELEGATING to harness-wrapper's codex
// parser — the per-harness Codex parsing knowledge now lives in one place (the
// wrapper). The wrapper's tool-aware events are mapped into loom's
// transcript.Event (identical public fields; the wrapper's internal
// Source/NativeID are not part of loom's Event). Field-level parity with loom's
// former in-tree parser is guarded by wrapper_parity_test.go.
package codex

import (
	hwcodex "github.com/olesho/harness-wrapper/pkg/transcript/codex"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// Events parses a Codex rollout JSONL into the canonical event stream
// (response_item message → text, function_call → tool_use, function_call_output
// → tool_result), delegating the parse to harness-wrapper's codex reader.
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
