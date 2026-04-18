package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/sessions/redact"
)

// redactionEnabled reports whether transcript capture should run content
// through the gitleaks+entropy redactor. Default on. Set the env var
// LOOM_REDACT_TRANSCRIPTS=off to disable for local development.
func redactionEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_REDACT_TRANSCRIPTS")))
	return v != "off" && v != "0" && v != "false" && v != "no"
}

// NativeTranscriptFile is the filename used for the backend's own JSONL
// transcript inside a session directory. Captured by SyncNativeTranscript
// on every hook invocation. Distinct from the legacy thin transcript.jsonl
// written by AppendTranscript for lifecycle breadcrumbs.
const NativeTranscriptFile = "agent_transcript.jsonl"

// SyncNativeTranscript mirrors the agent's native JSONL transcript (from
// Claude Code, Codex, OpenCode, etc.) into the session directory at
// agent_transcript.jsonl. Intended to be called from hook dispatch on every
// event that carries a transcript_path — the capture is idempotent because
// the file is append-only and each call writes a larger snapshot.
//
// Returns nil if srcPath is empty or does not exist (the hook subprocess
// must never exit nonzero; errors are informational only).
//
// Always re-reads and re-writes atomically via atomicfile.WriteFile.
func (s *Store) SyncNativeTranscript(sessionID, srcPath string) error {
	if srcPath == "" {
		return nil
	}
	if strings.ContainsAny(sessionID, "/\\") {
		return fmt.Errorf("invalid session ID %q: contains path separator", sessionID)
	}

	sessDir := filepath.Join(s.dir, sessionID)
	cleanDir := filepath.Clean(sessDir)
	if !strings.HasPrefix(cleanDir+string(os.PathSeparator), filepath.Clean(s.dir)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid session ID %q", sessionID)
	}
	if _, err := os.Stat(sessDir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session %q does not exist", sessionID)
		}
		return fmt.Errorf("stat session dir: %w", err)
	}

	if _, err := os.Stat(srcPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat source transcript: %w", err)
	}

	dstPath := filepath.Join(sessDir, NativeTranscriptFile)

	// #nosec G304 — srcPath comes from the agent hook payload (trusted)
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read source transcript: %w", err)
	}

	if redactionEnabled() {
		redacted, rerr := redact.JSONLBytes(data)
		if rerr != nil {
			return fmt.Errorf("redact native transcript: %w", rerr)
		}
		data = redacted
	}

	if err := atomicfile.WriteFile(dstPath, data, sessFilePerm); err != nil {
		return fmt.Errorf("write native transcript: %w", err)
	}
	return nil
}

// NativeTranscriptPath returns the on-disk path to a session's
// agent_transcript.jsonl. Does not check existence.
func (s *Store) NativeTranscriptPath(sessionID string) string {
	return filepath.Join(s.dir, sessionID, NativeTranscriptFile)
}
