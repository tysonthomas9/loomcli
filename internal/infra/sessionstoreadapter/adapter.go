// Package sessionstoreadapter is the Interaction-owned adapter for Loom's
// local compatibility session store. CLI entrypoints call these commands
// instead of mutating the persistence implementation directly.
package sessionstoreadapter

import (
	"time"

	"github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/eventstore"
)

func New(runtimeDir string) (*sessions.Store, error) {
	return sessions.NewStore(runtimeDir)
}

func Create(store *sessions.Store, options sessions.CreateOptions) (*sessions.Session, error) {
	return store.CreateSession(options)
}

func PurgeOlderThan(store *sessions.Store, age time.Duration) (int, error) {
	return store.PurgeOlderThan(age)
}

func CompactIndex(store *sessions.Store) (int, error) {
	return store.CompactIndex()
}

func ReIndex(store *sessions.Store, record sessions.SessionRecord) error {
	return store.ReIndex(record)
}

func SaveMetadata(store *sessions.Store, sessionID string, metadata *sessions.SessionMetadata) error {
	return store.SaveMetadata(sessionID, metadata)
}

func SyncNativeTranscript(store *sessions.Store, sessionID, sourcePath, format string) error {
	return store.SyncNativeTranscript(sessionID, sourcePath, format)
}

func SyncSubagentTranscript(store *sessions.Store, sessionID, subagentID, sourcePath string) error {
	return store.SyncSubagentTranscript(sessionID, subagentID, sourcePath)
}

func EnvelopeAppender(store *sessions.Store, sessionID string) func(transcript.EventEnvelope) error {
	return eventstore.Open(store.SessionDir(sessionID)).AppendEnvelope
}

func Finalize(session *sessions.Session, options sessions.FinalizeOptions) error {
	return session.Finalize(options)
}

func SyncLatestCodexRollout(session *sessions.Session, workDir string, since time.Time) (string, error) {
	return session.SyncLatestCodexRollout(workDir, since)
}

func SyncLatestClaudeTranscript(session *sessions.Session, workDir, claudeSessionID string, since time.Time) (string, error) {
	return session.SyncLatestClaudeTranscript(workDir, claudeSessionID, since)
}
