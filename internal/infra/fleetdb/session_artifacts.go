package fleetdb

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
)

// SessionArtifactOwner is the server-derived identity persisted on an
// Interaction-produced Artifact. Lease generation admission remains inside
// the Artifacts module; this low-level transport never receives the raw
// session credential.
type SessionArtifactOwner struct {
	WorkspaceKey string
	SessionID    string
	AgentID      string
}

type SessionArtifactCreateCommand struct {
	ArtifactID      string
	TaskID          string
	Type            string
	Summary         string
	MIMEType        string
	SizeBytes       int64
	ContentHash     string
	Visibility      string
	RedactionStatus string
	Metadata        map[string]string
}

// SessionArtifactTransport is the shared Client's narrow generic-content
// surface used by the Artifacts session adapter. It reuses the existing
// FleetDB Artifact endpoints without publishing the legacy composite Store.
type SessionArtifactTransport interface {
	CreateSession(context.Context, SessionArtifactOwner, SessionArtifactCreateCommand) (*Artifact, error)
	UploadSession(context.Context, SessionArtifactOwner, ArtifactUploadCommand) (*Artifact, error)
	FinalizeSession(context.Context, SessionArtifactOwner, ArtifactFinalizeCommand) (*Artifact, error)
	FailSession(context.Context, SessionArtifactOwner, ArtifactFailCommand) (*Artifact, error)
	GetSession(context.Context, SessionArtifactOwner, string) (*Artifact, error)
}

type sessionArtifactStore struct{ client *Client }

var _ SessionArtifactTransport = (*sessionArtifactStore)(nil)

func (store *sessionArtifactStore) CreateSession(
	ctx context.Context,
	owner SessionArtifactOwner,
	command SessionArtifactCreateCommand,
) (*Artifact, error) {
	if err := validateSessionArtifactOwner(owner); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.ArtifactID) == "" || command.ArtifactID != strings.TrimSpace(command.ArtifactID) ||
		strings.TrimSpace(command.Type) == "" || command.Type != strings.TrimSpace(command.Type) || command.SizeBytes < 0 {
		return nil, fmt.Errorf("session artifact create identity, type, and non-negative size are required: %w", ErrArtifactsInvalid)
	}
	body := map[string]any{
		"artifact_id": command.ArtifactID, "agent_id": owner.AgentID,
		"session_id": owner.SessionID, "task_id": command.TaskID,
		"owner_type": "session", "owner_id": owner.SessionID,
		"type": command.Type, "summary": command.Summary, "mime_type": command.MIMEType,
		"size_bytes": command.SizeBytes, "content_hash": command.ContentHash,
		"visibility": command.Visibility, "redaction_status": command.RedactionStatus,
		"durable_status": "declared", "metadata": cloneArtifactMetadata(command.Metadata),
	}
	var artifact Artifact
	path := "/api/v1/" + pathEscape(owner.WorkspaceKey) + "/artifacts"
	if err := store.client.do(ctx, http.MethodPost, path, body, &artifact); err != nil {
		return nil, mapArtifactTransportError("create session artifact", err)
	}
	if err := validateSessionArtifactSnapshot(owner, command.ArtifactID, &artifact); err != nil {
		return nil, err
	}
	if artifact.Type != command.Type || artifact.TaskID != command.TaskID || artifact.Summary != command.Summary ||
		artifact.MIMEType != command.MIMEType || artifact.SizeBytes != command.SizeBytes ||
		!strings.EqualFold(artifact.ContentHash, command.ContentHash) || artifact.Visibility != command.Visibility ||
		artifact.RedactionStatus != command.RedactionStatus {
		return nil, fmt.Errorf("session artifact create returned divergent state: %w", ErrArtifactsUnavailable)
	}
	return &artifact, nil
}

func (store *sessionArtifactStore) UploadSession(
	ctx context.Context,
	owner SessionArtifactOwner,
	command ArtifactUploadCommand,
) (*Artifact, error) {
	if err := validateSessionArtifactOwner(owner); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.ArtifactID) == "" || command.ArtifactID != strings.TrimSpace(command.ArtifactID) {
		return nil, fmt.Errorf("session artifact upload identity is required: %w", ErrArtifactsInvalid)
	}
	var artifact Artifact
	path := "/api/v1/" + pathEscape(owner.WorkspaceKey) + "/artifacts/" + pathEscape(command.ArtifactID) + "/content"
	if err := store.client.doRaw(ctx, http.MethodPut, path, bytes.NewReader(command.Content), command.MIMEType, &artifact); err != nil {
		return nil, mapArtifactTransportError("upload session artifact", err)
	}
	if err := validateSessionArtifactSnapshot(owner, command.ArtifactID, &artifact); err != nil {
		return nil, err
	}
	digest := artifactContentDigest(command.Content)
	if artifact.SizeBytes != int64(len(command.Content)) || !strings.EqualFold(artifact.ContentHash, digest) ||
		(command.MIMEType != "" && artifact.MIMEType != command.MIMEType) {
		return nil, fmt.Errorf("session artifact upload returned divergent content state: %w", ErrArtifactsUnavailable)
	}
	return &artifact, nil
}

func (store *sessionArtifactStore) FinalizeSession(
	ctx context.Context,
	owner SessionArtifactOwner,
	command ArtifactFinalizeCommand,
) (*Artifact, error) {
	if err := validateSessionArtifactOwner(owner); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.ArtifactID) == "" || command.ArtifactID != strings.TrimSpace(command.ArtifactID) {
		return nil, fmt.Errorf("session artifact finalize identity is required: %w", ErrArtifactsInvalid)
	}
	body := map[string]any{}
	putOptionalSessionArtifactField(body, "uri", command.URI)
	putOptionalSessionArtifactField(body, "summary", command.Summary)
	putOptionalSessionArtifactField(body, "mime_type", command.MIMEType)
	if command.SizeBytes != nil {
		body["size_bytes"] = *command.SizeBytes
	}
	putOptionalSessionArtifactField(body, "checksum", command.Checksum)
	putOptionalSessionArtifactField(body, "content_hash", command.ContentHash)
	putOptionalSessionArtifactField(body, "visibility", command.Visibility)
	putOptionalSessionArtifactField(body, "redaction_status", command.RedactionStatus)
	if command.Metadata != nil {
		body["metadata"] = cloneArtifactMetadata(*command.Metadata)
	}
	var artifact Artifact
	path := "/api/v1/" + pathEscape(owner.WorkspaceKey) + "/artifacts/" + pathEscape(command.ArtifactID) + "/finalize"
	if err := store.client.do(ctx, http.MethodPost, path, body, &artifact); err != nil {
		return nil, mapArtifactTransportError("finalize session artifact", err)
	}
	if err := validateSessionArtifactSnapshot(owner, command.ArtifactID, &artifact); err != nil {
		return nil, err
	}
	if artifact.DurableStatus != "finalized" || artifact.FinalizedAt == nil ||
		(command.ContentHash != nil && !strings.EqualFold(artifact.ContentHash, *command.ContentHash)) {
		return nil, fmt.Errorf("session artifact finalize returned divergent state: %w", ErrArtifactsUnavailable)
	}
	return &artifact, nil
}

func (store *sessionArtifactStore) FailSession(
	ctx context.Context,
	owner SessionArtifactOwner,
	command ArtifactFailCommand,
) (*Artifact, error) {
	if err := validateSessionArtifactOwner(owner); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.ArtifactID) == "" || strings.TrimSpace(command.FailureClass) == "" {
		return nil, fmt.Errorf("session artifact fail identity and class are required: %w", ErrArtifactsInvalid)
	}
	metadata := cloneArtifactMetadata(command.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["loom.evidence.capture_status"] = "capture_failed"
	metadata["loom.evidence.failure_class"] = strings.TrimSpace(command.FailureClass)
	if message := strings.TrimSpace(command.FailureMessage); message != "" {
		metadata["loom.evidence.failure_message"] = message
	}
	var artifact Artifact
	path := "/api/v1/" + pathEscape(owner.WorkspaceKey) + "/artifacts/" + pathEscape(command.ArtifactID)
	if err := store.client.do(ctx, http.MethodPatch, path, map[string]any{
		"durable_status": "failed", "metadata": metadata,
	}, &artifact); err != nil {
		return nil, mapArtifactTransportError("fail session artifact", err)
	}
	if err := validateSessionArtifactSnapshot(owner, command.ArtifactID, &artifact); err != nil {
		return nil, err
	}
	if artifact.DurableStatus != "failed" || artifact.FinalizedAt != nil || artifact.Metadata["loom.evidence.failure_class"] != command.FailureClass {
		return nil, fmt.Errorf("session artifact fail returned divergent state: %w", ErrArtifactsUnavailable)
	}
	return &artifact, nil
}

func (store *sessionArtifactStore) GetSession(
	ctx context.Context,
	owner SessionArtifactOwner,
	artifactID string,
) (*Artifact, error) {
	if err := validateSessionArtifactOwner(owner); err != nil {
		return nil, err
	}
	if strings.TrimSpace(artifactID) == "" || artifactID != strings.TrimSpace(artifactID) {
		return nil, fmt.Errorf("session artifact identity is required: %w", ErrArtifactsInvalid)
	}
	var artifact Artifact
	path := "/api/v1/" + pathEscape(owner.WorkspaceKey) + "/artifacts/" + pathEscape(artifactID)
	if err := store.client.do(ctx, http.MethodGet, path, nil, &artifact); err != nil {
		return nil, mapArtifactTransportError("get session artifact", err)
	}
	if err := validateSessionArtifactSnapshot(owner, artifactID, &artifact); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func validateSessionArtifactOwner(owner SessionArtifactOwner) error {
	if strings.TrimSpace(owner.WorkspaceKey) == "" || owner.WorkspaceKey != strings.TrimSpace(owner.WorkspaceKey) ||
		strings.TrimSpace(owner.SessionID) == "" || owner.SessionID != strings.TrimSpace(owner.SessionID) ||
		strings.TrimSpace(owner.AgentID) == "" || owner.AgentID != strings.TrimSpace(owner.AgentID) {
		return fmt.Errorf("session artifact workspace, session, and agent are required: %w", ErrArtifactsInvalid)
	}
	return nil
}

func validateSessionArtifactSnapshot(owner SessionArtifactOwner, artifactID string, artifact *Artifact) error {
	if artifact == nil || artifact.WorkspaceKey != owner.WorkspaceKey || artifact.ArtifactID != artifactID ||
		artifact.OwnerType != "session" || artifact.OwnerID != owner.SessionID ||
		artifact.SessionID != owner.SessionID || artifact.AgentID != owner.AgentID ||
		strings.TrimSpace(artifact.Type) == "" {
		return fmt.Errorf("session artifact result escaped owner scope: %w", ErrArtifactsUnavailable)
	}
	return nil
}

func putOptionalSessionArtifactField(body map[string]any, key string, value *string) {
	if value != nil {
		body[key] = *value
	}
}
