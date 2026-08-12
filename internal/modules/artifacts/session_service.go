package artifacts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const maxSessionContentBytes = 64 << 20

// SessionService owns the retry-safe content lifecycle for artifacts produced
// by one live Interaction session generation. It intentionally has no access
// to AgentSession persistence; Interaction performs the final owner-fenced
// reference update after this service returns a finalized Artifact.
type SessionService struct {
	store     SessionStore
	admission *authority.Admission
	evidence  *EvidencePolicy
}

var _ SessionAPI = (*SessionService)(nil)

func NewSession(store SessionStore, admission *authority.Admission, evidence *EvidencePolicy) (*SessionService, error) {
	if store == nil || admission == nil || evidence == nil {
		return nil, fmt.Errorf("compose session Artifacts: durable port, admission, and evidence policy are required: %w", ErrUnavailable)
	}
	return &SessionService{store: store, admission: admission, evidence: evidence}, nil
}

//nolint:funlen // Creation keeps authority, owner, digest, and persisted projection checks together.
func (service *SessionService) CreateContent(
	ctx context.Context,
	auth SessionContentAuthorities,
	owner SessionOwner,
	command SessionContentCommand,
) (*Artifact, error) {
	authorized, err := service.authorizeContent(owner, auth)
	if err != nil {
		return nil, err
	}
	command, err = normalizeSessionContent(command)
	if err != nil {
		return nil, err
	}
	owner = authorized
	prepared, err := service.evidence.Prepare(ctx, command.Type, command.MIMEType, command.Content, command.Metadata)
	if err != nil {
		return nil, service.recordContentFailure(ctx, auth, owner, command, err)
	}
	command.Content = prepared.Content
	command.MIMEType = prepared.MIMEType
	command.RedactionStatus = prepared.RedactionStatus
	command.Metadata = prepared.Metadata
	hash := prepared.ContentHash
	create := CreateCommand{
		ArtifactID: command.ArtifactID, AgentID: owner.AgentID, SessionID: owner.SessionID,
		TaskID: command.TaskID, Type: command.Type, Summary: command.Summary,
		MIMEType: command.MIMEType, SizeBytes: int64(len(command.Content)), ContentHash: hash,
		Visibility: command.Visibility, RedactionStatus: command.RedactionStatus,
		Metadata: cloneMetadata(command.Metadata),
	}
	artifact, err := service.store.CreateSession(ctx, owner, cloneCreateCommand(create))
	if err != nil {
		if !errors.Is(err, ErrAlreadyExists) {
			return nil, fmt.Errorf("create session artifact %q: %w", command.ArtifactID, err)
		}
		artifact, err = service.store.GetSession(ctx, owner, GetQuery{ArtifactID: command.ArtifactID})
		if err != nil {
			return nil, fmt.Errorf("reuse existing session artifact %q: %w", command.ArtifactID, err)
		}
		if err := validateSessionArtifact(artifact, owner, command.ArtifactID); err != nil {
			return nil, err
		}
		if !reusableContentArtifact(artifact, create) {
			return nil, fmt.Errorf("session artifact %q retry changed its semantic envelope: %w", command.ArtifactID, ErrAlreadyExists)
		}
		if artifact.DurableStatus == StatusFinalized {
			if !strings.EqualFold(artifact.ContentHash, hash) {
				return nil, fmt.Errorf("session artifact %q retry changed content: %w", command.ArtifactID, ErrAlreadyExists)
			}
			return cloneArtifact(artifact), nil
		}
	} else {
		if err := validateSessionArtifact(artifact, owner, command.ArtifactID); err != nil {
			return nil, err
		}
		if err := validateCreatedArtifact(artifact, create); err != nil {
			return nil, err
		}
	}

	upload := UploadCommand{ArtifactID: command.ArtifactID, Content: command.Content, MIMEType: command.MIMEType}
	artifact, err = service.store.UploadSession(ctx, owner, cloneUploadCommand(upload))
	if err != nil {
		return nil, fmt.Errorf("upload session artifact %q: %w", command.ArtifactID, err)
	}
	if err := validateSessionArtifact(artifact, owner, command.ArtifactID); err != nil {
		return nil, err
	}
	if err := validateUploadedArtifact(artifact, upload); err != nil {
		return nil, err
	}

	finalize := FinalizeCommand{ArtifactID: command.ArtifactID, ContentHash: &hash}
	artifact, err = service.store.FinalizeSession(ctx, owner, finalize)
	if err != nil {
		return nil, fmt.Errorf("finalize session artifact %q: %w", command.ArtifactID, err)
	}
	if err := validateSessionArtifact(artifact, owner, command.ArtifactID); err != nil {
		return nil, err
	}
	if artifact.DurableStatus != StatusFinalized || artifact.FinalizedAt == nil {
		return nil, fmt.Errorf("finalize session artifact %q returned status %q: %w", command.ArtifactID, artifact.DurableStatus, ErrInvalidPersistedState)
	}
	if err := validateFinalizedArtifact(artifact, finalize); err != nil {
		return nil, err
	}
	return cloneArtifact(artifact), nil
}

func (service *SessionService) authorizeContent(
	owner SessionOwner,
	auth SessionContentAuthorities,
) (SessionOwner, error) {
	operations := []struct {
		action authority.Action
		auth   authority.SessionAuthority
	}{
		{ActionDeclare, auth.Declare},
		{ActionGet, auth.Get},
		{ActionUpload, auth.Upload},
		{ActionFinalize, auth.Finalize},
		{ActionFail, auth.Fail},
	}
	var authorized SessionOwner
	for _, operation := range operations {
		value, err := service.authorize(operation.action, owner, operation.auth)
		if err != nil {
			return SessionOwner{}, err
		}
		authorized = value
	}
	return authorized, nil
}

func (service *SessionService) recordContentFailure(
	ctx context.Context,
	auth SessionContentAuthorities,
	owner SessionOwner,
	command SessionContentCommand,
	cause error,
) error {
	failure := evidenceFailureCommand(command.ArtifactID, cause)
	failure, err := normalizeFail(failure)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("normalize failed session evidence: %w", err))
	}
	create := CreateCommand{
		ArtifactID: command.ArtifactID, AgentID: owner.AgentID, SessionID: owner.SessionID,
		TaskID: command.TaskID, Type: command.Type, Summary: "evidence capture failure",
		Metadata: cloneMetadata(failure.Metadata),
	}
	artifact, err := service.store.CreateSession(ctx, owner, create)
	if err != nil {
		if !errors.Is(err, ErrAlreadyExists) {
			return errors.Join(cause, fmt.Errorf("declare failed session evidence: %w", err))
		}
		artifact, err = service.store.GetSession(ctx, owner, GetQuery{ArtifactID: command.ArtifactID})
		if err != nil {
			return errors.Join(cause, fmt.Errorf("load failed session evidence: %w", err))
		}
		if err := validateSessionArtifact(artifact, owner, command.ArtifactID); err != nil {
			return errors.Join(cause, err)
		}
		if artifact.DurableStatus == StatusFailed && artifact.Metadata["loom.evidence.failure_class"] == failure.FailureClass {
			return cause
		}
	}
	failed, err := service.store.FailSession(ctx, owner, cloneFailCommand(failure))
	if err != nil {
		return errors.Join(cause, fmt.Errorf("persist failed session evidence: %w", err))
	}
	if err := validateSessionArtifact(failed, owner, command.ArtifactID); err != nil {
		return errors.Join(cause, err)
	}
	if failed.DurableStatus != StatusFailed || failed.FinalizedAt != nil ||
		failed.Metadata[MetadataEvidenceCaptureStatus] != "capture_failed" ||
		failed.Metadata["loom.evidence.failure_class"] != failure.FailureClass {
		return errors.Join(cause, ErrInvalidPersistedState)
	}
	return cause
}

func (service *SessionService) authorize(
	action authority.Action,
	owner SessionOwner,
	auth authority.SessionAuthority,
) (SessionOwner, error) {
	owner, err := normalizeSessionOwner(owner)
	if err != nil {
		return SessionOwner{}, err
	}
	if service == nil || service.store == nil || service.admission == nil {
		return SessionOwner{}, ErrUnavailable
	}
	if err := service.admission.RequireSession(action, owner.WorkspaceKey, auth); err != nil {
		return SessionOwner{}, err
	}
	if auth.SessionID() != owner.SessionID || auth.AgentID() != owner.AgentID ||
		auth.NodeID() != owner.NodeID || auth.LeaseID() != owner.LeaseID ||
		auth.FencingToken() != owner.FencingToken {
		return SessionOwner{}, ErrNotOwner
	}
	return owner, nil
}

func normalizeSessionOwner(owner SessionOwner) (SessionOwner, error) {
	var err error
	owner.WorkspaceKey, err = requireCanonical("workspace", owner.WorkspaceKey)
	if err != nil {
		return SessionOwner{}, err
	}
	owner.SessionID, err = requireCanonical("session id", owner.SessionID)
	if err != nil {
		return SessionOwner{}, err
	}
	owner.AgentID, err = requireCanonical("agent id", owner.AgentID)
	if err != nil {
		return SessionOwner{}, err
	}
	owner.NodeID, err = requireCanonical("node id", owner.NodeID)
	if err != nil {
		return SessionOwner{}, err
	}
	owner.LeaseID, err = requireCanonical("lease id", owner.LeaseID)
	if err != nil {
		return SessionOwner{}, err
	}
	if owner.FencingToken <= 0 {
		return SessionOwner{}, fmt.Errorf("positive fencing token is required: %w", ErrInvalid)
	}
	return owner, nil
}

func normalizeSessionContent(command SessionContentCommand) (SessionContentCommand, error) {
	var err error
	command.ArtifactID, err = requireCanonical("artifact id", command.ArtifactID)
	if err != nil {
		return SessionContentCommand{}, err
	}
	command.Type, err = requireCanonical("artifact type", command.Type)
	if err != nil {
		return SessionContentCommand{}, err
	}
	command.TaskID = strings.TrimSpace(command.TaskID)
	command.Summary = strings.TrimSpace(command.Summary)
	command.MIMEType = strings.TrimSpace(command.MIMEType)
	command.Visibility = strings.TrimSpace(command.Visibility)
	command.RedactionStatus = strings.TrimSpace(command.RedactionStatus)
	command.Metadata = cloneMetadata(command.Metadata)
	command.Content = append([]byte(nil), command.Content...)
	if len(command.Content) == 0 || len(command.Content) > maxSessionContentBytes {
		return SessionContentCommand{}, fmt.Errorf("session artifact content must contain 1..%d bytes: %w", maxSessionContentBytes, ErrInvalid)
	}
	return command, nil
}

func validateSessionArtifact(artifact *Artifact, owner SessionOwner, artifactID string) error {
	if artifact == nil {
		return fmt.Errorf("empty session artifact result: %w", ErrInvalidPersistedState)
	}
	if artifact.WorkspaceKey != owner.WorkspaceKey || artifact.OwnerType != OwnerSession ||
		artifact.OwnerID != owner.SessionID || artifact.SessionID != owner.SessionID ||
		artifact.AgentID != owner.AgentID {
		return fmt.Errorf("artifact %q escaped session owner scope: %w", artifact.ArtifactID, ErrInvalidPersistedState)
	}
	if artifactID != "" && artifact.ArtifactID != artifactID {
		return fmt.Errorf("artifact result id %q does not match %q: %w", artifact.ArtifactID, artifactID, ErrInvalidPersistedState)
	}
	if _, err := requireCanonical("persisted artifact id", artifact.ArtifactID); err != nil {
		return errors.Join(ErrInvalidPersistedState, err)
	}
	if _, err := requireCanonical("persisted artifact type", artifact.Type); err != nil {
		return errors.Join(ErrInvalidPersistedState, err)
	}
	if artifact.SizeBytes < 0 || !validDurableStatus(artifact.DurableStatus) {
		return fmt.Errorf("artifact %q returned invalid session content state: %w", artifact.ArtifactID, ErrInvalidPersistedState)
	}
	return nil
}
