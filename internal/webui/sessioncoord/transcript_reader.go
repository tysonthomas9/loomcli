package sessioncoord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

const (
	maxCanonicalTranscriptInputBytes = 64 << 20
)

func (s *sessionServiceImpl) controlPlaneSessionTranscript(ctx context.Context, wsID, taskID, sessionID string) ([]transcript.Event, error) {
	run, runErr := s.executionTaskRunForSession(ctx, wsID, taskID, sessionID)
	if runErr == nil {
		return s.executionTaskRunTranscript(ctx, wsID, run)
	}
	if !serviceErrorNotFound(runErr) {
		return nil, runErr
	}
	rec, err := s.controlPlaneSessionRecord(ctx, wsID, taskID, sessionID)
	if err != nil {
		return nil, err
	}
	return s.agentSessionTranscript(ctx, wsID, rec)
}

// GetAgentSessionTranscript returns the durable canonical transcript for an
// agent session whose ownership is established directly by AgentSession.AgentID.
// This is the task-independent transcript path used by interactive terminal
// agents.
func (s *sessionServiceImpl) GetAgentSessionTranscript(
	ctx context.Context,
	wsID, agentID, sessionID string,
) ([]transcript.Event, error) {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	if agentID == "" {
		return nil, apperrors.ErrValidation("invalid agent ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, apperrors.ErrValidation("invalid session ID")
	}
	if s.store != nil && s.store.AgentSessions() != nil {
		rec, err := s.store.AgentSessions().Get(ctx, wsID, sessionID)
		if err == nil {
			// Return the same not-found signal for a missing session and a session
			// owned by another agent so callers cannot enumerate transcripts.
			if rec == nil || strings.TrimSpace(rec.AgentID) != agentID {
				return nil, apperrors.ErrNotFound("session not found")
			}
			return s.ownedAgentSessionTranscript(ctx, wsID, rec)
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, sessionControlPlaneReadError(
				"failed to load session",
				err,
			)
		}
	}
	return s.localAgentSessionTranscript(ctx, wsID, agentID, sessionID)
}

// localAgentSessionTranscript serves a daemon-local supervised session only
// after the workspace-scoped store metadata proves the exact agent and task
// relationship. The task transcript reader repeats task ownership validation,
// so knowledge of a session ID alone cannot cross either boundary.
func (s *sessionServiceImpl) localAgentSessionTranscript(
	ctx context.Context,
	wsID, agentID, sessionID string,
) ([]transcript.Event, error) {
	sessStore, err := s.findStoreForSession(ctx, wsID, sessionID)
	if err != nil {
		return nil, apperrors.ErrNotFound("session not found")
	}
	meta, err := sessStore.LoadMetadata(sessionID)
	if err != nil || meta == nil || strings.TrimSpace(meta.AgentName) != agentID ||
		strings.TrimSpace(meta.TaskID) == "" {
		return nil, apperrors.ErrNotFound("session not found")
	}
	return s.GetSessionTranscript(ctx, wsID, meta.TaskID, sessionID)
}

func (s *sessionServiceImpl) agentSessionTranscript(
	ctx context.Context,
	wsID string,
	rec *domain.AgentSession,
) ([]transcript.Event, error) {
	return s.loadAgentSessionTranscript(ctx, rec, func(ctx context.Context, ref string) ([]byte, error) {
		return s.readOwnedTaskSessionTranscriptArtifact(ctx, wsID, rec, ref)
	})
}

// ownedAgentSessionTranscript is the stricter reader used by the public
// agent/session route. Unlike the transitional task-session path above, it
// accepts only a durable Artifact owned by the exact agent/session tuple.
// Validating metadata before ReadContent prevents a forged transcript_ref from
// turning knowledge of another artifact ID into cross-agent content access.
func (s *sessionServiceImpl) ownedAgentSessionTranscript(
	ctx context.Context,
	wsID string,
	rec *domain.AgentSession,
) ([]transcript.Event, error) {
	return s.loadAgentSessionTranscript(ctx, rec, func(ctx context.Context, ref string) ([]byte, error) {
		return s.readOwnedAgentTranscriptArtifact(ctx, wsID, rec, ref)
	})
}

func (s *sessionServiceImpl) loadAgentSessionTranscript(
	ctx context.Context,
	rec *domain.AgentSession,
	read func(context.Context, string) ([]byte, error),
) ([]transcript.Event, error) {
	transcriptRef := ""
	if rec != nil && rec.Metadata != nil {
		transcriptRef = strings.TrimSpace(rec.Metadata["transcript_ref"])
	}
	return loadCanonicalTranscriptArtifact(ctx, transcriptRef, read)
}

func loadCanonicalTranscriptArtifact(
	ctx context.Context,
	transcriptRef string,
	read func(context.Context, string) ([]byte, error),
) ([]transcript.Event, error) {
	if transcriptRef == "" {
		return nil, apperrors.ErrNotFound("transcript not found")
	}
	data, err := read(ctx, transcriptRef)
	if err != nil {
		// The artifact record survives in the control plane but its content
		// blob is gone (e.g. a run predating the durable-artifact volume, whose
		// local content dir was wiped). Report it honestly as gone — a clean
		// not-found — rather than a generic internal failure, so the UI renders
		// "transcript content is no longer available" instead of a doubled 500.
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, domain.ErrNotFound) {
			return nil, apperrors.ErrNotFound("transcript content is no longer available")
		}
		if errors.Is(err, artifactsmodule.ErrContentUnavailable) {
			return nil, apperrors.ErrUnavailable("transcript content is temporarily unavailable")
		}
		return nil, sessionControlPlaneReadError(
			"failed to load transcript",
			err,
		)
	}
	events, err := parseCanonicalTranscriptBytes(data)
	if err != nil {
		return nil, apperrors.ErrInternal("failed to parse transcript", err)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

func (s *sessionServiceImpl) readOwnedAgentTranscriptArtifact(
	ctx context.Context,
	wsID string,
	rec *domain.AgentSession,
	ref string,
) ([]byte, error) {
	artifactID, ok := transcriptArtifactID(ref)
	if rec == nil || !ok {
		return nil, domain.ErrNotFound
	}
	if artifactID == "" || s.artifacts == nil {
		return nil, domain.ErrNotFound
	}
	artifact, err := s.artifacts.GetArtifact(ctx, artifactsmodule.Query{WorkspaceKey: wsID, ArtifactID: artifactID})
	if err != nil {
		return nil, err
	}
	if artifact == nil ||
		strings.TrimSpace(artifact.WorkspaceKey) != strings.TrimSpace(wsID) ||
		strings.TrimSpace(artifact.ArtifactID) != artifactID ||
		(strings.TrimSpace(artifact.AgentID) != "" &&
			strings.TrimSpace(artifact.AgentID) != strings.TrimSpace(rec.AgentID)) ||
		strings.TrimSpace(artifact.SessionID) != strings.TrimSpace(rec.SessionID) ||
		artifact.OwnerType != artifactsmodule.OwnerSession ||
		strings.TrimSpace(artifact.OwnerID) != strings.TrimSpace(rec.SessionID) ||
		strings.TrimSpace(artifact.Type) != "transcript" ||
		artifact.DurableStatus != artifactsmodule.StatusFinalized {
		return nil, domain.ErrNotFound
	}
	// Missing managed content is authoritative and must remain a clean 404;
	// falling through to a stale URI can turn it into a misleading 500.
	return s.artifacts.ReadArtifactContent(ctx, artifactsmodule.Query{WorkspaceKey: wsID, ArtifactID: artifactID})
}

func (s *sessionServiceImpl) readOwnedTaskSessionTranscriptArtifact(
	ctx context.Context,
	wsID string,
	rec *domain.AgentSession,
	ref string,
) ([]byte, error) {
	artifactID, ok := transcriptArtifactID(ref)
	if rec == nil || !ok {
		return nil, domain.ErrNotFound
	}
	if artifactID == "" || s.artifacts == nil {
		return nil, domain.ErrNotFound
	}
	artifact, err := s.artifacts.GetArtifact(ctx, artifactsmodule.Query{WorkspaceKey: wsID, ArtifactID: artifactID})
	if err != nil {
		return nil, err
	}
	taskID := agentSessionTaskID(rec)
	if !taskSessionTranscriptArtifactMatches(artifact, rec, wsID, artifactID, taskID) {
		return nil, domain.ErrNotFound
	}
	return s.readManagedArtifactContent(ctx, wsID, artifactID)
}

// transcriptArtifactID accepts both the raw artifact ID written by Fleet's
// interaction transcript command and the artifact:// reference emitted by
// older session writers. Other URI schemes remain invalid. Artifact ownership
// and finalization are validated by the caller before any content is read.
func transcriptArtifactID(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	if strings.HasPrefix(ref, "artifact://") {
		artifactID := strings.TrimSpace(strings.TrimPrefix(ref, "artifact://"))
		return artifactID, artifactID != ""
	}
	if strings.Contains(ref, "://") {
		return "", false
	}
	return ref, true
}

func taskSessionTranscriptArtifactMatches(
	artifact *artifactsmodule.Artifact,
	rec *domain.AgentSession,
	wsID, artifactID, taskID string,
) bool {
	if artifact == nil ||
		strings.TrimSpace(artifact.WorkspaceKey) != strings.TrimSpace(wsID) ||
		strings.TrimSpace(artifact.ArtifactID) != artifactID ||
		strings.TrimSpace(artifact.TaskID) != taskID ||
		strings.TrimSpace(artifact.Type) != "transcript" ||
		artifact.DurableStatus != artifactsmodule.StatusFinalized {
		return false
	}
	return taskSessionTranscriptArtifactOwnerMatches(artifact, rec)
}

func taskSessionTranscriptArtifactOwnerMatches(
	artifact *artifactsmodule.Artifact,
	rec *domain.AgentSession,
) bool {
	switch artifact.OwnerType {
	case artifactsmodule.OwnerSession:
		return strings.TrimSpace(artifact.OwnerID) == strings.TrimSpace(rec.SessionID) &&
			strings.TrimSpace(artifact.SessionID) == strings.TrimSpace(rec.SessionID) &&
			(strings.TrimSpace(artifact.AgentID) == "" ||
				strings.TrimSpace(artifact.AgentID) == strings.TrimSpace(rec.AgentID))
	case artifactsmodule.OwnerTaskRun:
		return taskRunArtifactMatchesSession(artifact, rec)
	default:
		return false
	}
}

func (s *sessionServiceImpl) readOwnedTaskRunArtifact(
	ctx context.Context,
	wsID string,
	rec *domain.AgentSession,
	artifactID, artifactType string,
) ([]byte, error) {
	artifactID = strings.TrimSpace(artifactID)
	if rec == nil || artifactID == "" || s.artifacts == nil {
		return nil, domain.ErrNotFound
	}
	taskID := agentSessionTaskID(rec)
	if taskID == "" {
		return nil, domain.ErrNotFound
	}
	artifact, err := s.artifacts.GetArtifact(ctx, artifactsmodule.Query{WorkspaceKey: wsID, ArtifactID: artifactID})
	if err != nil {
		return nil, err
	}
	if artifact == nil ||
		strings.TrimSpace(artifact.WorkspaceKey) != strings.TrimSpace(wsID) ||
		strings.TrimSpace(artifact.ArtifactID) != artifactID ||
		strings.TrimSpace(artifact.TaskID) != taskID ||
		strings.TrimSpace(artifact.Type) != artifactType ||
		artifact.DurableStatus != artifactsmodule.StatusFinalized ||
		!taskRunArtifactMatchesSession(artifact, rec) {
		return nil, domain.ErrNotFound
	}
	return s.readManagedArtifactContent(ctx, wsID, artifactID)
}

func taskRunArtifactMatchesSession(artifact *artifactsmodule.Artifact, rec *domain.AgentSession) bool {
	if artifact == nil || rec == nil {
		return false
	}
	taskRunID := ""
	if rec.Metadata != nil {
		taskRunID = strings.TrimSpace(rec.Metadata["task_run_id"])
	}
	return taskRunID != "" &&
		artifact.OwnerType == artifactsmodule.OwnerTaskRun &&
		strings.TrimSpace(artifact.OwnerID) == taskRunID &&
		(artifact.SessionID == "" ||
			strings.TrimSpace(artifact.SessionID) == strings.TrimSpace(rec.SessionID))
}

func agentSessionTaskID(rec *domain.AgentSession) string {
	if rec == nil {
		return ""
	}
	taskID := strings.TrimSpace(rec.TaskID)
	if taskID == "" && rec.Metadata != nil {
		taskID = strings.TrimSpace(rec.Metadata["task_id"])
	}
	return taskID
}

func (s *sessionServiceImpl) readManagedArtifactContent(
	ctx context.Context,
	wsID, artifactID string,
) ([]byte, error) {
	if s.artifacts == nil {
		return nil, artifactsmodule.ErrContentUnavailable
	}
	return s.artifacts.ReadArtifactContent(ctx, artifactsmodule.Query{WorkspaceKey: wsID, ArtifactID: artifactID})
}

func parseCanonicalTranscriptBytes(data []byte) ([]transcript.Event, error) {
	return parseCanonicalTranscriptBytesWithinLimits(
		data,
		maxCanonicalTranscriptInputBytes,
		transcript.MaxCanonicalEvents,
	)
}

func parseCanonicalTranscriptBytesWithinLimits(
	data []byte,
	maxBytes, maxEvents int,
) ([]transcript.Event, error) {
	if len(data) > maxBytes {
		return nil, fmt.Errorf("transcript exceeds %d-byte limit", maxBytes)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return []transcript.Event{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if trimmed[0] == '[' {
		return parseCanonicalTranscriptArray(decoder, maxEvents)
	}
	return parseCanonicalTranscriptStream(decoder, maxEvents)
}

func parseCanonicalTranscriptArray(
	decoder *json.Decoder,
	maxEvents int,
) ([]transcript.Event, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return nil, errors.New("transcript array is malformed")
	}
	events := make([]transcript.Event, 0, min(maxEvents, 256))
	for decoder.More() {
		if len(events) >= maxEvents {
			return nil, fmt.Errorf("transcript exceeds %d-event limit", maxEvents)
		}
		var event transcript.Event
		if err := decoder.Decode(&event); err != nil {
			return nil, err
		}
		if err := transcript.ValidateCanonicalEvent(event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	return events, nil
}

func parseCanonicalTranscriptStream(
	decoder *json.Decoder,
	maxEvents int,
) ([]transcript.Event, error) {
	events := make([]transcript.Event, 0, min(maxEvents, 256))
	for len(events) < maxEvents {
		var event transcript.Event
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			return events, nil
		} else if err != nil {
			return nil, err
		}
		if err := transcript.ValidateCanonicalEvent(event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return events, nil
	} else if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("transcript exceeds %d-event limit", maxEvents)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("transcript contains trailing JSON")
}
