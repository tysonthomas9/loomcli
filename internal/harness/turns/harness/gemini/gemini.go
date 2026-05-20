// Package gemini provides a turn-detection adapter for Google's Gemini
// CLI (@google/gemini-cli).
//
// Current state — v0.1, ahead of corpus recording:
//
//   - End-of-turn screen marker: NOT yet identified. The adapter embeds
//     generic.Adapter so turn-complete signals still flow through the
//     wrapper.StatusWaitingForInput path (driven by the per-harness
//     prompt patterns in pkg/wrapper/internal/harness/gemini/). Once a
//     recording exists under test/corpus/gemini/, replace the
//     OnScreen-derived fingerprint here, mirroring codex's Token-usage
//     footer match or claude-code's "✻ <verb> for Ns" line.
//
//   - Session ID extraction: NOT yet identified. Gemini's resume UX
//     uses user-chosen tags (`/chat save <tag>`) rather than a visible
//     UUID, and we have not yet seen the harness print its internal
//     session UUID on screen. ExtractSessionID returns ("", false)
//     until a corpus reveals otherwise; History() therefore falls back
//     to the in-memory Store mid-session for gemini conversations,
//     matching the conservative posture documented in the architecture
//     discussion.
//
//   - Transcript reading: implemented and ready to fire as soon as a
//     harness session ID is known (e.g. set externally on the chat
//     Session record).
//
// Markers may shift across upstream versions; the golden-recording
// tests under test/corpus/gemini/ will be the early-warning signal when
// they're added.
package gemini

import (
	"github.com/tysonthomas9/loomcli/internal/harness/screen"
	"github.com/tysonthomas9/loomcli/internal/harness/transcript"
	transcriptgemini "github.com/tysonthomas9/loomcli/internal/harness/transcript/gemini"
	"github.com/tysonthomas9/loomcli/internal/harness/turns/generic"
)

// Adapter implements turns.Adapter for Gemini CLI.
//
// It currently delegates OnScreen to the embedded generic.Adapter (no
// per-screen signals yet) and inherits OnWrapperStatus. Once
// corpus-driven markers land, override OnScreen here.
type Adapter struct {
	generic.Adapter
}

// New constructs a Gemini adapter.
func New() *Adapter { return &Adapter{} }

// Name returns "gemini".
func (*Adapter) Name() string { return "gemini" }

// ExtractSessionID is a placeholder. Gemini's TUI surface for the
// internal session UUID is not yet known; until a recording confirms a
// marker, return ("", false). Implements turns.SessionIDExtractor only
// so future changes are a one-method edit.
func (*Adapter) ExtractSessionID(_ screen.Snapshot) (string, bool) {
	return "", false
}

// ReadTranscript reads the on-disk Gemini session log. Implements
// turns.TranscriptReader. Becomes useful as soon as a harness session
// ID is known — see the package-level comment for the open question
// around how that ID is sourced.
func (*Adapter) ReadTranscript(harnessSessionID, workingDir string) ([]transcript.Turn, error) {
	return transcriptgemini.New().Read(harnessSessionID, workingDir)
}
