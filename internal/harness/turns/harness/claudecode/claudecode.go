// Package claudecode provides a turn-detection adapter for Anthropic's
// Claude Code CLI (claude / @anthropic-ai/claude-code).
//
// Detection signals observed on 2.1.141:
//
//   - End of an assistant turn: a "✻ <verb> for Ns" thinking-summary
//     line appears, where <verb> is a colorful word like Baked, Brewed,
//     Crunched, Pondered, etc., and N is an integer second count.
//     The full line is a per-turn fingerprint: when a new one appears
//     on screen, the turn just completed.
//
//   - User interrupt: a "⎿  Interrupted · What should Claude do
//     instead?" line appears. The turn ended in a recoverable error
//     state.
//
// This adapter embeds generic.Adapter so wrapper-level status events
// (blocked_by_cost, retry_later, failed) keep flowing through.
//
// Markers may shift across upstream versions; the golden-recording
// tests under test/corpus/claude-code/ are the early-warning signal.
package claudecode

import (
	"regexp"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/harness/screen"
	"github.com/tysonthomas9/loomcli/internal/harness/transcript"
	transcriptcc "github.com/tysonthomas9/loomcli/internal/harness/transcript/claudecode"
	"github.com/tysonthomas9/loomcli/internal/harness/turns"
	"github.com/tysonthomas9/loomcli/internal/harness/turns/generic"
)

// thinkingRE matches the end-of-turn thinking-summary line, anchored
// at the start and end of a screen line so it does not mis-fire when
// the model echoes the marker shape as part of its reply content
// (e.g. "you'd see '✻ Baked for 5s' here" in explanatory prose).
//
// Format: U+273B (✻) + space + capitalized verb + " for " + N + "s",
// optionally surrounded by horizontal whitespace, on its own line.
// The marker text is the first capture group, so the fingerprint
// stored on the Adapter does not include the emulator's column
// padding.
//
// Examples that match: "✻ Baked for 5s", "✻ Brewed for 4s",
// "✻ Crunched for 2s" — each on a line by itself (trailing column
// padding from the emulator is allowed).
//
// Examples that do NOT match (and used to mis-fire): the same
// pattern surrounded by non-whitespace on the same line.
var thinkingRE = regexp.MustCompile(`(?m)^[^\S\r\n]*(✻ [A-Z][a-zA-Z]+ for \d+s)[^\S\r\n]*$`)

// resumeRE matches the "claude --resume <uuid>" hint Claude Code prints
// when it ends a session. The UUID names the on-disk transcript file.
var resumeRE = regexp.MustCompile(`claude --resume ([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`)

// interruptMarker is the literal text Claude Code writes after the user
// interrupts a streaming reply (Esc / Ctrl-C). Claude Code uses U+23BF
// (⎿), then a regular ASCII space, then U+00A0 (non-breaking space),
// then "Interrupted · ...". The NBSP is easy to miss — match it exactly.
const interruptMarker = "⎿  Interrupted · What should Claude do instead?"

// Adapter implements turns.Adapter for Claude Code.
type Adapter struct {
	generic.Adapter

	mu                sync.Mutex
	lastFingerprint   string
	lastInterruptSeen bool
}

// New constructs a Claude Code adapter.
func New() *Adapter { return &Adapter{} }

// Name returns "claude-code".
func (*Adapter) Name() string { return "claude-code" }

// OnScreen scans the snapshot for the thinking-summary and interrupt
// markers and emits TurnComplete / Errored when transitions occur.
func (a *Adapter) OnScreen(snap screen.Snapshot) []turns.Event {
	a.mu.Lock()
	defer a.mu.Unlock()

	var out []turns.Event

	// Interrupt detection — transition not-seen → seen.
	interruptNow := strings.Contains(snap.Text, interruptMarker)
	if interruptNow && !a.lastInterruptSeen {
		out = append(out, turns.Event{Kind: turns.Errored, Reason: "claude-code: " + interruptMarker})
	}
	a.lastInterruptSeen = interruptNow

	// Turn-complete detection — newest thinking marker differs from last fired.
	// Capture group 1 holds the marker text without surrounding column
	// padding so the fingerprint stays stable across redraws.
	matches := thinkingRE.FindAllStringSubmatch(snap.Text, -1)
	if len(matches) > 0 {
		latest := matches[len(matches)-1][1]
		if latest != a.lastFingerprint {
			a.lastFingerprint = latest
			out = append(out, turns.Event{Kind: turns.TurnComplete, Reason: "claude-code: " + latest})
		}
	}
	return out
}

// ExtractSessionID scrapes the "claude --resume <uuid>" hint that
// identifies the on-disk transcript file. Implements turns.SessionIDExtractor.
func (*Adapter) ExtractSessionID(snap screen.Snapshot) (string, bool) {
	m := resumeRE.FindStringSubmatch(snap.Text)
	if len(m) < 2 {
		return "", false
	}
	return m[1], true
}

// ReadTranscript reads the on-disk Claude Code session log. Implements
// turns.TranscriptReader.
func (*Adapter) ReadTranscript(harnessSessionID, workingDir string) ([]transcript.Turn, error) {
	return transcriptcc.New().Read(harnessSessionID, workingDir)
}
