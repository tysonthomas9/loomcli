package transcript

import hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"

// FromWrapper down-casts a harness-wrapper transcript Event to loom's Event,
// copying the 10 public fields and dropping the wrapper's internal
// Source/NativeID/SchemaVersion (which are not part of loom's Event). It is the
// single shared projection used by the per-backend parsers (claude/codex) and
// the event-store serving DTO, which each re-derived this field-for-field copy.
func FromWrapper(w hwtranscript.Event) Event {
	return Event{
		Seq:       w.Seq,
		Timestamp: w.Timestamp,
		Role:      w.Role,
		Type:      w.Type,
		Text:      w.Text,
		ToolName:  w.ToolName,
		ToolUseID: w.ToolUseID,
		ToolInput: w.ToolInput,
		Output:    w.Output,
		UUID:      w.UUID,
	}
}
