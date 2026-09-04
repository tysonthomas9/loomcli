package sessions

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/runtimectx"
	"github.com/tysonthomas9/loomcli/internal/sessions/redact"
)

// NativeTranscriptFile is the filename used for the backend's own JSONL
// transcript inside a session directory. Captured by SyncNativeTranscript
// on every hook invocation. Distinct from the lifecycle breadcrumb transcript.
const NativeTranscriptFile = "agent_transcript.jsonl"

// SyncNativeTranscript mirrors the agent's native JSONL transcript (from
// Claude Code, Codex, OpenCode, etc.) into the session directory at
// agent_transcript.jsonl. Intended to be called from hook dispatch on every
// event that carries a transcript_path — the capture is idempotent because
// the file is append-only and each call writes a larger snapshot.
//
// format records the encoding (TranscriptFormatRaw | TranscriptFormatCanonical)
// onto the session metadata so LoadNativeEvents dispatches deterministically.
// Redaction policy is format-independent: Go's canonical redactor is the
// authoritative host-side persistence boundary for raw and canonical streams.
//
// Returns nil if srcPath is empty or does not exist (the hook subprocess
// must never exit nonzero; errors are informational only).
//
// Always re-reads and re-writes atomically via atomicfile.WriteFile.
func (s *Store) SyncNativeTranscript(sessionID, srcPath, format string) error {
	if srcPath == "" {
		return nil
	}
	_, span := startSpan(runtimectx.RootContext(), "service.Sessions.SyncNativeTranscript",
		attrLoomSessionID(sessionID),
	)
	defer span.End()

	sessDir, err := s.resolveSessionDir(sessionID)
	if err != nil {
		recordErr(span, err)
		return err
	}
	if _, err := os.Stat(srcPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		recordErr(span, err)
		return fmt.Errorf("stat source transcript: %w", err)
	}

	// #nosec G304 — srcPath comes from the agent hook payload (trusted)
	data, err := os.ReadFile(srcPath)
	if err != nil {
		recordErr(span, err)
		return fmt.Errorf("read source transcript: %w", err)
	}
	if redact.Enabled() {
		redacted, rerr := redact.JSONLBytes(data)
		if rerr != nil {
			recordErr(span, rerr)
			return fmt.Errorf("redact native transcript: %w", rerr)
		}
		data = redacted
	}

	dstPath := filepath.Join(sessDir, NativeTranscriptFile)
	if err := atomicfile.WriteFile(dstPath, data, sessFilePerm); err != nil {
		recordErr(span, err)
		return fmt.Errorf("write native transcript: %w", err)
	}
	// Record the format alongside the file so the read path never has to guess.
	// Best-effort: the transcript is already written; a metadata hiccup must not
	// fail the hook. Idempotent, so this writes once per session, not per event.
	if format != "" {
		if ferr := s.recordTranscriptFormat(sessionID, format); ferr != nil {
			recordErr(span, ferr)
		}
	}
	return nil
}

// recordTranscriptFormat stamps SessionRecord.TranscriptFormat when it isn't
// already set to format. The compare-then-write keeps it a no-op after the first
// call for a given session (so the hot hook path writes metadata at most once).
func (s *Store) recordTranscriptFormat(sessionID, format string) error {
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		return err
	}
	if meta.TranscriptFormat == format {
		return nil
	}
	meta.TranscriptFormat = format
	return s.SaveMetadata(sessionID, meta)
}

// NativeTranscriptPath returns the on-disk path to a session's
// agent_transcript.jsonl. Does not check existence.
func (s *Store) NativeTranscriptPath(sessionID string) string {
	return filepath.Join(s.dir, sessionID, NativeTranscriptFile)
}
