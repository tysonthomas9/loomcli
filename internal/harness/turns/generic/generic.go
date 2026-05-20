// Package generic provides a fallback turn-detection adapter that maps
// wrapper.Status transitions directly to turn events without looking at
// screen contents.
//
// Use this adapter when no per-harness adapter is available, or as a
// safety net while a per-harness adapter is in development. Its
// fidelity is bounded by the wrapper's built-in classifier vocabulary:
// it can only signal turn-complete after the wrapper itself detects
// waiting_for_input.
package generic

import (
	"github.com/tysonthomas9/loomcli/internal/harness/screen"
	"github.com/tysonthomas9/loomcli/internal/harness/turns"
	"github.com/tysonthomas9/loomcli/internal/harness/wrapper"
)

// Adapter is the generic, screen-agnostic turn detector.
type Adapter struct{}

// New constructs the generic adapter. The adapter is stateless and a
// single instance can be shared across sessions.
func New() *Adapter { return &Adapter{} }

// Name returns "generic".
func (*Adapter) Name() string { return "generic" }

// OnScreen ignores screen content; the generic adapter relies entirely
// on wrapper status transitions.
func (*Adapter) OnScreen(_ screen.Snapshot) []turns.Event { return nil }

// OnWrapperStatus maps wrapper.Status to turn events:
//
//   - waiting_for_input → TurnComplete (best generic signal that the
//     assistant has finished and the user can speak)
//   - blocked_by_cost / retry_later / api_error → Blocked
//     (api_error keeps the harness alive at the wrapper layer, but
//     from a chat consumer's perspective the current turn cannot
//     progress until the consumer takes action — same external shape
//     as a transient block)
//   - failed / interrupted → Errored
//   - idle (terminal) → Errored, because in a chat-style context the
//     harness exiting is unrecoverable
//   - stale / unknown → no event (advisory only)
func (*Adapter) OnWrapperStatus(status wrapper.Status, reason string) []turns.Event {
	switch status {
	case wrapper.StatusWaitingForInput:
		return []turns.Event{{Kind: turns.TurnComplete, Reason: reason}}
	case wrapper.StatusBlockedByCost, wrapper.StatusRetryLater, wrapper.StatusAPIError:
		return []turns.Event{{Kind: turns.Blocked, Reason: reason}}
	case wrapper.StatusFailed, wrapper.StatusInterrupted:
		return []turns.Event{{Kind: turns.Errored, Reason: reason}}
	case wrapper.StatusIdle:
		return []turns.Event{{Kind: turns.Errored, Reason: "harness exited"}}
	}
	return nil
}
