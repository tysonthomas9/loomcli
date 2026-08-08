package backends

import (
	"os"
	"strings"

	hwharness "github.com/olesho/harness-wrapper/pkg/harness"
	"github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/infra/sessionstoreadapter"
)

// The loom-side P3 rollout flags (env-gated, default OFF so there is no behavior
// change until a deployment opts in):
//   - LOOM_TRANSCRIPT_MODE (F1): off|stream|hooks|auto — the acquisition mode.
//   - LOOM_EVENTSTORE_WRITE (F2): whether to shadow-write OnEvent to the event
//     store. (Serving from the store is a separate per-workspace flag, F3, not
//     wired here.)

// transcriptModeFromEnv maps LOOM_TRANSCRIPT_MODE to a harness acquisition mode.
// Unset / unrecognized ⇒ Off (no acquisition).
func transcriptModeFromEnv() hwharness.Mode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_TRANSCRIPT_MODE"))) {
	case "stream", "streamparse":
		return hwharness.TranscriptStreamParse
	case "hooks":
		return hwharness.TranscriptHooks
	case "auto":
		return hwharness.TranscriptAuto
	default:
		return hwharness.TranscriptOff
	}
}

// eventStoreWriteEnabled reports LOOM_EVENTSTORE_WRITE (F2). Default false.
func eventStoreWriteEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_EVENTSTORE_WRITE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// loomHookCommand is the argv the wrapper templates into a harness's hook config
// (the fired hook runs `<loom> hooks <harness> <event>`). Returns nil if the
// executable path can't be resolved (hooks then can't be installed; the
// orchestrator falls back to StreamParse).
func loomHookCommand() []string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return nil
	}
	// The wrapper renders `<exe> hooks dispatch <harness> <event>` — loom's
	// generic forwarder (internal/cli/hooks hooksDispatchCmd → HandleHookEvent).
	return []string{exe, "hooks", "dispatch"}
}

// eventStoreSink builds the durable OnEvent sink + the RunID for the active loom
// session, or (nil, "") when the event store is disabled (F2 off) or there is no
// active session (standalone mode). The sink is a LOCAL append only — no
// network/UI — honoring harness.Run's bounded-OnEvent contract.
func eventStoreSink(workDir string) (func(transcript.EventEnvelope) error, string) {
	if !eventStoreWriteEnabled() {
		return nil, ""
	}
	runtimeDir, sid := GetActiveSessionRuntimeEnv()
	if runtimeDir == "" || sid == "" {
		return nil, "" // standalone / no session ⇒ nothing to key the store by
	}
	// Resolve the session dir through the SAME source of truth the serving side
	// reads from (sessions.Store.SessionDir), so writer + reader can't diverge.
	store, err := sessionstoreadapter.New(runtimeDir)
	if err != nil {
		return nil, ""
	}
	sessionDir := store.SessionDir(sid)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, "" // can't place the store; skip rather than fail the run
	}
	runID := sid // fallback when no lock RunID (e.g. standalone)
	if info, err := cli.ReadLockFile(workDir); err == nil && info != nil && info.RunID != "" {
		runID = info.RunID // stable across resume — the dedup/replay-collapse key
	}
	return sessionstoreadapter.EnvelopeAppender(store, sid), runID
}
