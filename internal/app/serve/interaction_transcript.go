package serve

import (
	"context"
	"errors"
	"fmt"
	"time"

	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// interactionTranscriptArtifacts is the composition-owned bridge from
// Interaction's narrow transcript port to the session-scoped Artifacts API.
// Retry, checksum, ownership, and lifecycle policy now live in Artifacts; this
// bridge only derives bounded cross-capability authority and maps DTOs.
type interactionTranscriptArtifacts struct {
	api                  artifacts.SessionAPI
	issuer               *authority.Issuer
	interactionAdmission *authority.Admission
}

var _ interaction.TranscriptArtifactStore = (*interactionTranscriptArtifacts)(nil)

func newInteractionTranscriptArtifactStore(
	transport infrafleetdb.SessionArtifactTransport,
	issuer *authority.Issuer,
) (*interactionTranscriptArtifacts, error) {
	store := newInteractionArtifactsFleetDBTransport(transport)
	if store == nil {
		return nil, fmt.Errorf("compose Interaction transcript Artifacts adapter: %w", artifacts.ErrUnavailable)
	}
	if issuer == nil {
		return nil, fmt.Errorf("compose Interaction transcript Artifacts authority: %w", interaction.ErrUnavailable)
	}
	interactionAdmission, err := issuer.NewAdmission(interaction.OperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose Interaction transcript admission: %w", err)
	}
	admission, err := issuer.NewAdmission(artifacts.OperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose Interaction transcript Artifacts admission: %w", err)
	}
	service, err := artifacts.NewSession(store, admission)
	if err != nil {
		return nil, err
	}
	return &interactionTranscriptArtifacts{
		api: service, issuer: issuer, interactionAdmission: interactionAdmission,
	}, nil
}

func (adapter *interactionTranscriptArtifacts) CreateContent(
	ctx context.Context,
	auth authority.SessionAuthority,
	command interaction.TranscriptArtifactCreate,
) (string, error) {
	if adapter == nil || adapter.api == nil || adapter.issuer == nil || adapter.interactionAdmission == nil {
		return "", interaction.ErrUnavailable
	}
	if err := adapter.interactionAdmission.RequireSession(
		interaction.ActionPublishTranscript,
		command.WorkspaceKey,
		auth,
	); err != nil {
		return "", fmt.Errorf("validate transcript publish authority: %w", errors.Join(interaction.ErrNotOwner, err))
	}
	if auth.Action() != interaction.ActionPublishTranscript || auth.Workspace() != command.WorkspaceKey ||
		auth.SessionID() != command.SessionID || auth.AgentID() != command.AgentID {
		return "", interaction.ErrNotOwner
	}
	authorities, err := adapter.deriveAuthorities(auth)
	if err != nil {
		return "", fmt.Errorf("derive transcript Artifacts authority: %w", err)
	}
	artifact, err := adapter.api.CreateContent(ctx, authorities, artifacts.SessionOwner{
		WorkspaceKey: command.WorkspaceKey, SessionID: command.SessionID, AgentID: command.AgentID,
		NodeID: auth.NodeID(), LeaseID: auth.LeaseID(), FencingToken: auth.FencingToken(),
	}, artifacts.SessionContentCommand{
		ArtifactID: command.ArtifactID, TaskID: command.TaskID, Type: "transcript",
		Summary: "interactive session transcript", MIMEType: "application/x-ndjson",
		Metadata: cloneInteractionMap(command.Metadata), Content: append([]byte(nil), command.Content...),
	})
	if err != nil {
		return "", mapInteractionArtifactError(err)
	}
	if artifact == nil || artifact.ArtifactID != command.ArtifactID {
		return "", fmt.Errorf("create session transcript returned mismatched artifact: %w", interaction.ErrInvalidPersistedState)
	}
	return artifact.ArtifactID, nil
}

// deriveAuthorities converts one already-admitted Interaction publish grant
// into the four exact Artifacts lifecycle grants needed by the bounded
// transcript saga. No new identity or lease credential is introduced, and the
// original authority remains responsible for the final session attachment.
func (adapter *interactionTranscriptArtifacts) deriveAuthorities(
	auth authority.SessionAuthority,
) (artifacts.SessionContentAuthorities, error) {
	actions := []authority.Action{artifacts.ActionDeclare, artifacts.ActionGet, artifacts.ActionUpload, artifacts.ActionFinalize}
	principal, err := adapter.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: auth.Subject(), Class: authority.ClassSession, Workspace: auth.Workspace(),
		Actions: actions, ExpiresAt: auth.ExpiresAt(),
	})
	if err != nil {
		return artifacts.SessionContentAuthorities{}, err
	}
	issue := func(action authority.Action) (authority.SessionAuthority, error) {
		return adapter.issuer.IssueSessionForOwner(principal, auth.Workspace(), action, auth.SessionOwner())
	}
	declare, err := issue(artifacts.ActionDeclare)
	if err != nil {
		return artifacts.SessionContentAuthorities{}, err
	}
	get, err := issue(artifacts.ActionGet)
	if err != nil {
		return artifacts.SessionContentAuthorities{}, err
	}
	upload, err := issue(artifacts.ActionUpload)
	if err != nil {
		return artifacts.SessionContentAuthorities{}, err
	}
	finalize, err := issue(artifacts.ActionFinalize)
	if err != nil {
		return artifacts.SessionContentAuthorities{}, err
	}
	return artifacts.SessionContentAuthorities{Declare: declare, Get: get, Upload: upload, Finalize: finalize}, nil
}

func mapInteractionArtifactError(err error) error {
	if err == nil {
		return nil
	}
	var mapped error
	switch {
	case errors.Is(err, artifacts.ErrAlreadyExists), errors.Is(err, artifacts.ErrInvalidTransition):
		mapped = interaction.ErrConflict
	case errors.Is(err, artifacts.ErrNotOwner):
		mapped = interaction.ErrNotOwner
	case errors.Is(err, artifacts.ErrInvalid):
		mapped = interaction.ErrInvalid
	case errors.Is(err, artifacts.ErrInvalidPersistedState):
		mapped = interaction.ErrInvalidPersistedState
	default:
		mapped = interaction.ErrUnavailable
	}
	return fmt.Errorf("session transcript Artifacts lifecycle: %w", errors.Join(mapped, err))
}

type interactionArtifactsFleetDBTransport struct {
	transport infrafleetdb.SessionArtifactTransport
}

var _ artifacts.SessionStore = (*interactionArtifactsFleetDBTransport)(nil)

func newInteractionArtifactsFleetDBTransport(transport infrafleetdb.SessionArtifactTransport) artifacts.SessionStore {
	if transport == nil {
		return nil
	}
	return &interactionArtifactsFleetDBTransport{transport: transport}
}

func (transport *interactionArtifactsFleetDBTransport) CreateSession(
	ctx context.Context,
	owner artifacts.SessionOwner,
	command artifacts.CreateCommand,
) (*artifacts.Artifact, error) {
	value, err := transport.transport.CreateSession(ctx, interactionArtifactInfraOwner(owner), infrafleetdb.SessionArtifactCreateCommand{
		ArtifactID: command.ArtifactID, TaskID: command.TaskID, Type: command.Type,
		Summary: command.Summary, MIMEType: command.MIMEType, SizeBytes: command.SizeBytes,
		ContentHash: command.ContentHash, Visibility: command.Visibility,
		RedactionStatus: command.RedactionStatus, Metadata: cloneInteractionMap(command.Metadata),
	})
	return interactionArtifactFromInfra(value), translateInteractionArtifactTransportError(err)
}

func (transport *interactionArtifactsFleetDBTransport) UploadSession(
	ctx context.Context,
	owner artifacts.SessionOwner,
	command artifacts.UploadCommand,
) (*artifacts.Artifact, error) {
	value, err := transport.transport.UploadSession(ctx, interactionArtifactInfraOwner(owner), infrafleetdb.ArtifactUploadCommand{
		ArtifactID: command.ArtifactID, Content: append([]byte(nil), command.Content...), MIMEType: command.MIMEType,
	})
	return interactionArtifactFromInfra(value), translateInteractionArtifactTransportError(err)
}

func (transport *interactionArtifactsFleetDBTransport) FinalizeSession(
	ctx context.Context,
	owner artifacts.SessionOwner,
	command artifacts.FinalizeCommand,
) (*artifacts.Artifact, error) {
	value, err := transport.transport.FinalizeSession(ctx, interactionArtifactInfraOwner(owner), infrafleetdb.ArtifactFinalizeCommand{
		ArtifactID: command.ArtifactID, URI: cloneInteractionStringPointer(command.URI),
		Summary: cloneInteractionStringPointer(command.Summary), MIMEType: cloneInteractionStringPointer(command.MIMEType),
		SizeBytes: cloneInteractionInt64Pointer(command.SizeBytes), Checksum: cloneInteractionStringPointer(command.Checksum),
		ContentHash: cloneInteractionStringPointer(command.ContentHash), Visibility: cloneInteractionStringPointer(command.Visibility),
		RedactionStatus: cloneInteractionStringPointer(command.RedactionStatus), Metadata: cloneInteractionMapPointer(command.Metadata),
	})
	return interactionArtifactFromInfra(value), translateInteractionArtifactTransportError(err)
}

func (transport *interactionArtifactsFleetDBTransport) GetSession(
	ctx context.Context,
	owner artifacts.SessionOwner,
	query artifacts.GetQuery,
) (*artifacts.Artifact, error) {
	value, err := transport.transport.GetSession(ctx, interactionArtifactInfraOwner(owner), query.ArtifactID)
	return interactionArtifactFromInfra(value), translateInteractionArtifactTransportError(err)
}

func interactionArtifactInfraOwner(owner artifacts.SessionOwner) infrafleetdb.SessionArtifactOwner {
	return infrafleetdb.SessionArtifactOwner{WorkspaceKey: owner.WorkspaceKey, SessionID: owner.SessionID, AgentID: owner.AgentID}
}

func interactionArtifactFromInfra(value *infrafleetdb.Artifact) *artifacts.Artifact {
	if value == nil {
		return nil
	}
	return &artifacts.Artifact{
		WorkspaceKey: value.WorkspaceKey, ArtifactID: value.ArtifactID, AgentID: value.AgentID,
		SessionID: value.SessionID, TaskID: value.TaskID, OwnerType: artifacts.OwnerType(value.OwnerType), OwnerID: value.OwnerID,
		Type: value.Type, URI: value.URI, Summary: value.Summary, MIMEType: value.MIMEType, SizeBytes: value.SizeBytes,
		Checksum: value.Checksum, ContentHash: value.ContentHash, Visibility: value.Visibility,
		RedactionStatus: value.RedactionStatus, DurableStatus: artifacts.DurableStatus(value.DurableStatus),
		Metadata: cloneInteractionMap(value.Metadata), FinalizedAt: cloneInteractionTimePointer(value.FinalizedAt),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func translateInteractionArtifactTransportError(err error) error {
	if err == nil {
		return nil
	}
	var mapped error
	switch {
	case errors.Is(err, infrafleetdb.ErrArtifactsNotFound):
		mapped = artifacts.ErrNotFound
	case errors.Is(err, infrafleetdb.ErrArtifactsInvalid):
		mapped = artifacts.ErrInvalid
	case errors.Is(err, infrafleetdb.ErrArtifactsConflict):
		mapped = artifacts.ErrAlreadyExists
	case errors.Is(err, infrafleetdb.ErrArtifactsNotOwner):
		mapped = artifacts.ErrNotOwner
	case errors.Is(err, infrafleetdb.ErrArtifactsInvalidTransition):
		mapped = artifacts.ErrInvalidTransition
	case errors.Is(err, infrafleetdb.ErrArtifactsUnavailable):
		mapped = artifacts.ErrUnavailable
	default:
		return err
	}
	return errors.Join(mapped, err)
}

func cloneInteractionStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneInteractionInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneInteractionMapPointer(value *map[string]string) *map[string]string {
	if value == nil {
		return nil
	}
	result := cloneInteractionMap(*value)
	return &result
}

func cloneInteractionTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
