// Package backends is the backend-dispatching entry point for transcript
// parsing. Import this package (not the sibling per-backend subpackages) to
// get a `ParseEvents(backend, data)` function that routes to the right
// parser by SessionRecord.Backend name.
package backends

import (
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript/claude"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript/codex"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript/opencode"
)

// ParseEvents routes to the right per-backend parser based on the
// SessionRecord.Backend string. Empty input returns (nil, nil). Unknown
// backends fall through to the Claude parser (Claude Code format is the
// most common and loom's primary auto-mode backend).
func ParseEvents(backend string, data []byte) ([]transcript.Event, error) {
	if len(data) == 0 {
		return nil, nil
	}
	switch backend {
	case "claude":
		return claude.Events(data)
	case "codex":
		return codex.Events(data)
	case "opencode":
		return opencode.Events(data)
	default:
		return claude.Events(data)
	}
}

// ParseEventsFromFile reads the file and parses it. Returns (nil, nil) when
// the file does not exist so callers can treat "no transcript yet" as a
// non-error condition.
func ParseEventsFromFile(backend, path string) ([]transcript.Event, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path controlled by session store
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	return ParseEvents(backend, data)
}
