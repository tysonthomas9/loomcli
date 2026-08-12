package sessions

import (
	"strings"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// FinalAssistantReply returns the prose the run ended on: the last contiguous
// block of assistant text events in the session's native transcript, joined
// with blank lines. It returns "" (with a nil error) when the transcript does
// not exist yet or the run ended on something other than assistant prose — a
// caller that needs the reply must decide for itself whether that is fatal.
func (s *Store) FinalAssistantReply(sessionID string) (string, error) {
	events, err := s.LoadNativeEvents(sessionID)
	if err != nil {
		return "", err
	}
	return terminalAssistantText(events), nil
}

// terminalAssistantText joins the run's last contiguous block of assistant text
// events, skipping the trailing session/result metadata the leaf appends.
//
// The boundary is structural, not identity-based: the canonical TS-leaf stream
// leaves Event.UUID empty, so grouping by UUID would merge intermediate
// narration with the final reply. Collection stops at the first tool cycle,
// reasoning block, or user turn walking backwards, which yields exactly the
// prose the agent ended on for both canonical and raw backend transcripts.
func terminalAssistantText(events []transcript.Event) string {
	var parts []string
	started := false
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Role == transcript.RoleAssistant && ev.Type == transcript.EventText {
			if text := strings.TrimSpace(ev.Text); text != "" {
				parts = append(parts, text)
				started = true
			}
			continue
		}
		if started || !isTrailingMetadataEvent(ev) {
			break
		}
	}
	// parts was collected back-to-front.
	for l, r := 0, len(parts)-1; l < r; l, r = l+1, r-1 {
		parts[l], parts[r] = parts[r], parts[l]
	}
	return strings.Join(parts, "\n\n")
}

// isTrailingMetadataEvent reports whether an event is leaf bookkeeping that may
// legitimately sit after the final reply (the terminal `result` record and
// session metadata), as opposed to real conversation content.
func isTrailingMetadataEvent(ev transcript.Event) bool {
	return ev.Type == transcript.EventResult ||
		ev.Type == transcript.EventSessionMeta ||
		ev.Role == transcript.RoleSystem
}
