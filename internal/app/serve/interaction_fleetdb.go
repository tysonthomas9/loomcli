package serve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	interactionfleetdb "github.com/tysonthomas9/loomcli/internal/modules/interaction/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// interactionFleetDBAuthorityTransport is the composition-owned bridge from
// the one process-wide FleetDB client to Interaction's credential-validation
// and atomic command adapter. It exposes no legacy independent session,
// terminal, lease, or inbox mutator.
type interactionFleetDBAuthorityTransport struct {
	transport infrafleetdb.InteractionTransport
}

var _ interactionfleetdb.AuthorityTransport = (*interactionFleetDBAuthorityTransport)(nil)
var _ interactionfleetdb.MutationTransport = (*interactionFleetDBAuthorityTransport)(nil)

func newInteractionFleetDBAuthorityTransport(
	client *infrafleetdb.Client,
) *interactionFleetDBAuthorityTransport {
	if client == nil {
		return nil
	}
	return &interactionFleetDBAuthorityTransport{transport: client.Interaction()}
}

// NewInteractionCapabilityWithFleetDB composes the complete production
// Interaction capability against one shared FleetDB client. Construction
// fails unless authority validation and every compound mutation command are
// available through that client.
func NewInteractionCapabilityWithFleetDB(
	config InteractionConfig,
	client *infrafleetdb.Client,
) (*InteractionCapability, error) {
	return newInteractionCapabilityWithFleetDB(config, client, nil)
}

// NewInteractionCapabilityWithFleetDBIssuer shares a caller-owned issuer while
// retaining the same complete FleetDB command requirement as the standalone
// production constructor.
func NewInteractionCapabilityWithFleetDBIssuer(
	config InteractionConfig,
	client *infrafleetdb.Client,
	issuer *authority.Issuer,
) (*InteractionCapability, error) {
	if issuer == nil {
		return nil, fmt.Errorf("compose Interaction authority: Workflow Catalog authority is unavailable")
	}
	return newInteractionCapabilityWithFleetDB(config, client, issuer)
}

func newInteractionCapabilityWithFleetDB(
	config InteractionConfig,
	client *infrafleetdb.Client,
	issuer *authority.Issuer,
) (*InteractionCapability, error) {
	if issuer == nil {
		issuer = authority.NewIssuer()
	}
	transport := newInteractionFleetDBAuthorityTransport(client)
	if transport == nil {
		return nil, fmt.Errorf("compose Interaction FleetDB transport: %w", interaction.ErrUnavailable)
	}
	adapter, err := interactionfleetdb.New(transport, transport)
	if err != nil {
		return nil, err
	}
	transcripts, err := newInteractionTranscriptArtifactStore(client.SessionArtifacts(), issuer)
	if err != nil {
		return nil, err
	}
	dependencies := InteractionDependencies{
		Sessions: adapter, Terminals: adapter.Terminals(), Inbox: adapter,
		Activity: adapter, SessionAuthority: adapter,
		Transcripts:     transcripts,
		WorkspaceLister: config.WorkspaceLister,
	}
	return NewInteractionCapabilityWithIssuer(config, dependencies, issuer)
}

// NewInteractionSessionAuthorityResolver is retained for isolated inbound
// adapter composition. Production serve uses the complete constructor above
// so authority validation cannot be published without atomic commands.
func NewInteractionSessionAuthorityResolver(
	client *infrafleetdb.Client,
	issuer *authority.Issuer,
) (InteractionSessionAuthorityResolver, error) {
	if client == nil || issuer == nil {
		return nil, fmt.Errorf("compose Interaction session authority: shared FleetDB client and issuer are required")
	}
	adapter, err := interactionfleetdb.NewAuthorityAdapter(
		newInteractionFleetDBAuthorityTransport(client),
	)
	if err != nil {
		return nil, err
	}
	resolver := newInteractionSessionAuthorityResolver(adapter, issuer, time.Now)
	if resolver == nil {
		return nil, fmt.Errorf("compose Interaction session authority: %w", interaction.ErrUnavailable)
	}
	return resolver, nil
}

func (transport *interactionFleetDBAuthorityTransport) ValidateSessionAuthority(
	ctx context.Context,
	proof interactionfleetdb.SessionAuthorityProofWire,
) (*interactionfleetdb.SessionAuthorityValidationWire, error) {
	if transport == nil || transport.transport == nil {
		return nil, interactionfleetdb.ErrTransportUnavailable
	}
	value, err := transport.transport.ValidateSessionAuthority(
		ctx,
		infrafleetdb.InteractionSessionAuthorityProof{
			WorkspaceKey: proof.WorkspaceKey,
			SessionID:    proof.SessionID,
			AgentID:      proof.AgentID,
			TerminalID:   proof.TerminalID,
			NodeID:       proof.NodeID,
			LeaseID:      proof.LeaseID,
			LeaseToken:   string(proof.LeaseToken),
			FencingToken: proof.FencingToken,
		},
	)
	if err != nil {
		return nil, translateInteractionFleetDBError(err)
	}
	if value == nil {
		return nil, nil
	}
	return &interactionfleetdb.SessionAuthorityValidationWire{
		WorkspaceKey: value.WorkspaceKey,
		SessionID:    value.SessionID,
		AgentID:      value.AgentID,
		TerminalID:   value.TerminalID,
		NodeID:       value.NodeID,
		LeaseID:      value.LeaseID,
		FencingToken: value.FencingToken,
		ExpiresAt:    value.ExpiresAt,
	}, nil
}

func (transport *interactionFleetDBAuthorityTransport) StartSession(
	ctx context.Context,
	command interaction.StartSessionCommand,
) (interaction.SessionStart, error) {
	if transport == nil || transport.transport == nil {
		return interaction.SessionStart{}, interactionfleetdb.ErrTransportUnavailable
	}
	value, err := transport.transport.StartInteractionSession(
		ctx,
		infrafleetdb.InteractionSessionStartInput{
			WorkspaceKey: command.WorkspaceKey, SessionID: command.SessionID,
			AgentID: command.AgentID, NodeID: command.NodeID, Kind: string(command.Kind),
			TaskID: command.TaskID, TerminalID: command.TerminalID,
			ParentSessionID: command.ParentSessionID, Phase: command.Phase,
			Attempt: command.Attempt, LeaseID: command.LeaseID,
			LeaseTTL: command.LeaseTTL, Metadata: cloneInteractionMap(command.Metadata),
		},
	)
	if err != nil {
		return interaction.SessionStart{}, translateInteractionFleetDBError(err)
	}
	if value == nil {
		return interaction.SessionStart{}, nil
	}
	raw := []byte(value.Token)
	value.Token = ""
	result := interaction.SessionStart{
		Session: interactionSession(value.Session),
		Lease:   interactionLease(value.Lease),
		Token:   interaction.NewLeaseToken(raw),
	}
	clear(raw)
	return result, nil
}

func (transport *interactionFleetDBAuthorityTransport) RecoverSessionStart(
	ctx context.Context,
	command interaction.RecoverSessionStartCommand,
) (interaction.SessionStart, error) {
	if transport == nil || transport.transport == nil {
		return interaction.SessionStart{}, interactionfleetdb.ErrTransportUnavailable
	}
	original := command.Original
	value, err := transport.transport.RecoverInteractionSessionStart(
		ctx,
		infrafleetdb.InteractionSessionStartRecoveryInput{
			Original: infrafleetdb.InteractionSessionStartInput{
				WorkspaceKey:    original.WorkspaceKey,
				SessionID:       original.SessionID,
				AgentID:         original.AgentID,
				NodeID:          original.NodeID,
				Kind:            string(original.Kind),
				TaskID:          original.TaskID,
				TerminalID:      original.TerminalID,
				ParentSessionID: original.ParentSessionID,
				Phase:           original.Phase,
				Attempt:         original.Attempt,
				LeaseID:         original.LeaseID,
				LeaseTTL:        original.LeaseTTL,
				Metadata:        cloneInteractionMap(original.Metadata),
			},
			ExpectedLeaseID:           command.ExpectedLeaseID,
			ExpectedLeaseFencingToken: command.ExpectedLeaseFencingToken,
			ReplacementLeaseID:        command.ReplacementLeaseID,
			ReplacementLeaseTTL:       command.ReplacementLeaseTTL,
		},
	)
	if err != nil {
		return interaction.SessionStart{}, translateInteractionFleetDBError(err)
	}
	if value == nil {
		return interaction.SessionStart{}, nil
	}
	raw := []byte(value.Token)
	value.Token = ""
	result := interaction.SessionStart{
		Session: interactionSession(value.Session),
		Lease:   interactionLease(value.Lease),
		Token:   interaction.NewLeaseToken(raw),
	}
	clear(raw)
	return result, nil
}

func (transport *interactionFleetDBAuthorityTransport) GetSession(
	ctx context.Context,
	workspace,
	sessionID string,
) (*interaction.AgentSession, error) {
	if transport == nil || transport.transport == nil {
		return nil, interactionfleetdb.ErrTransportUnavailable
	}
	value, err := transport.transport.GetInteractionSession(ctx, workspace, sessionID)
	return interactionSession(value), translateInteractionFleetDBError(err)
}

func (transport *interactionFleetDBAuthorityTransport) ListSessions(
	ctx context.Context,
	query interaction.SessionArchiveQuery,
) ([]*interaction.AgentSession, error) {
	if transport == nil || transport.transport == nil {
		return nil, interactionfleetdb.ErrTransportUnavailable
	}
	values, err := transport.transport.ListInteractionSessions(ctx, infrafleetdb.InteractionSessionQuery{
		WorkspaceKey: query.WorkspaceKey,
		AgentID:      query.AgentID,
		WorkItemID:   query.WorkItemID,
		Limit:        query.Limit,
	})
	if err != nil {
		return nil, translateInteractionFleetDBError(err)
	}
	out := make([]*interaction.AgentSession, 0, len(values))
	for _, value := range values {
		out = append(out, interactionSession(value))
	}
	return out, nil
}

func (transport *interactionFleetDBAuthorityTransport) HeartbeatSessionOwned(
	ctx context.Context,
	workspace string,
	owner authority.SessionOwner,
	heartbeat interaction.SessionHeartbeat,
) (*interaction.AgentSession, *interaction.SessionLease, error) {
	if transport == nil || transport.transport == nil {
		return nil, nil, interactionfleetdb.ErrTransportUnavailable
	}
	proof, closeProof, err := interactionOwnedProof(workspace, owner)
	if err != nil {
		return nil, nil, err
	}
	defer closeProof()
	value, err := transport.transport.HeartbeatInteractionSession(
		ctx,
		infrafleetdb.InteractionSessionHeartbeatInput{
			Proof: proof, Phase: heartbeat.Phase, LeaseTTL: heartbeat.LeaseTTL,
		},
	)
	if err != nil {
		return nil, nil, translateInteractionFleetDBError(err)
	}
	if value == nil {
		return nil, nil, nil
	}
	return interactionSession(value.Session), interactionLease(value.Lease), nil
}

func (transport *interactionFleetDBAuthorityTransport) PatchSessionOwned(
	ctx context.Context,
	workspace string,
	owner authority.SessionOwner,
	patch interaction.SessionPatch,
) (*interaction.AgentSession, *interaction.SessionLease, error) {
	if transport == nil || transport.transport == nil {
		return nil, nil, interactionfleetdb.ErrTransportUnavailable
	}
	proof, closeProof, err := interactionOwnedProof(workspace, owner)
	if err != nil {
		return nil, nil, err
	}
	defer closeProof()
	value, err := transport.transport.PatchInteractionSession(
		ctx,
		infrafleetdb.InteractionSessionPatchInput{
			Proof: proof, Phase: cloneInteractionString(patch.Phase),
			MetadataUpserts:      cloneInteractionMap(patch.MetadataUpserts),
			MetadataRemovals:     append([]string(nil), patch.MetadataRemovals...),
			TranscriptArtifactID: cloneInteractionString(patch.TranscriptArtifactID),
		},
	)
	if err != nil {
		return nil, nil, translateInteractionFleetDBError(err)
	}
	if value == nil {
		return nil, nil, nil
	}
	return interactionSession(value.Session), interactionLease(value.Lease), nil
}

func (transport *interactionFleetDBAuthorityTransport) FinishSessionOwned(
	ctx context.Context,
	workspace string,
	owner authority.SessionOwner,
	finish interaction.SessionFinish,
) (interaction.SessionFinishResult, error) {
	if transport == nil || transport.transport == nil {
		return interaction.SessionFinishResult{}, interactionfleetdb.ErrTransportUnavailable
	}
	proof, closeProof, err := interactionOwnedProof(workspace, owner)
	if err != nil {
		return interaction.SessionFinishResult{}, err
	}
	defer closeProof()
	value, err := transport.transport.FinishInteractionSession(
		ctx,
		infrafleetdb.InteractionSessionFinishInput{
			Proof: proof, Status: string(finish.Status),
			Summary: finish.Summary, ErrorClass: finish.ErrorClass,
			ExitCode:             cloneInteractionInt(finish.ExitCode),
			TranscriptArtifactID: finish.TranscriptArtifactID,
		},
	)
	if err != nil {
		return interaction.SessionFinishResult{}, translateInteractionFleetDBError(err)
	}
	if value == nil {
		return interaction.SessionFinishResult{}, nil
	}
	return interaction.SessionFinishResult{
		Session:  interactionSession(value.Session),
		Terminal: interactionTerminal(value.Terminal),
		Lease:    interactionLease(value.Lease),
	}, nil
}

func (transport *interactionFleetDBAuthorityTransport) ForceInterrupt(
	ctx context.Context,
	command interaction.ForceInterruptCommand,
) (interaction.ForceInterruptResult, error) {
	if transport == nil || transport.transport == nil {
		return interaction.ForceInterruptResult{}, interactionfleetdb.ErrTransportUnavailable
	}
	value, err := transport.transport.ForceInterruptInteractionSession(
		ctx,
		infrafleetdb.InteractionSessionForceInterruptInput{
			WorkspaceKey: command.WorkspaceKey, SessionID: command.SessionID,
			AgentID: command.AgentID, TerminalID: command.TerminalID,
			ExpectedLeaseID:           command.ExpectedLeaseID,
			ExpectedLeaseFencingToken: command.ExpectedLeaseFencingToken,
			StreamRef:                 command.StreamRef, TerminalTab: command.TerminalTab,
			Reason: command.Reason,
		},
	)
	if err != nil {
		return interaction.ForceInterruptResult{}, translateInteractionFleetDBError(err)
	}
	if value == nil {
		return interaction.ForceInterruptResult{}, nil
	}
	return interaction.ForceInterruptResult{
		Session: interactionSession(value.Session), Terminal: interactionTerminal(value.Terminal),
		Lease: interactionLease(value.Lease), Changed: value.Changed,
	}, nil
}

func (transport *interactionFleetDBAuthorityTransport) InterruptSessionIfLeaseMissing(
	ctx context.Context,
	workspace,
	sessionID string,
	_ time.Time,
) (*interaction.AgentSession, bool, error) {
	if transport == nil || transport.transport == nil {
		return nil, false, interactionfleetdb.ErrTransportUnavailable
	}
	value, err := transport.transport.InterruptInteractionSessionIfLeaseMissing(
		ctx,
		workspace,
		sessionID,
	)
	if err != nil {
		return nil, false, translateInteractionFleetDBError(err)
	}
	if value == nil {
		return nil, false, nil
	}
	return interactionSession(value.Session), value.Changed, nil
}

func (transport *interactionFleetDBAuthorityTransport) ListRecoverableSessions(
	ctx context.Context,
	workspace string,
	_ time.Time,
) ([]*interaction.AgentSession, error) {
	if transport == nil || transport.transport == nil {
		return nil, interactionfleetdb.ErrTransportUnavailable
	}
	values, err := transport.transport.ListRecoverableInteractionSessions(ctx, workspace)
	if err != nil {
		return nil, translateInteractionFleetDBError(err)
	}
	result := make([]*interaction.AgentSession, len(values))
	for index, value := range values {
		result[index] = interactionSession(value)
	}
	return result, nil
}

func (transport *interactionFleetDBAuthorityTransport) CreateTerminalOwned(
	ctx context.Context,
	owner authority.SessionOwner,
	command interaction.OpenTerminalCommand,
) (*interaction.TerminalSession, error) {
	if transport == nil || transport.transport == nil {
		return nil, interactionfleetdb.ErrTransportUnavailable
	}
	proof, closeProof, err := interactionOwnedProof(command.WorkspaceKey, owner)
	if err != nil {
		return nil, err
	}
	defer closeProof()
	value, err := transport.transport.CreateInteractionTerminal(
		ctx,
		infrafleetdb.InteractionTerminalCreateInput{
			Proof: proof, TerminalID: command.TerminalID, TaskID: command.TaskID,
			Title: command.Title, Kind: command.Kind, PTYProvider: command.PTYProvider,
			StreamRef: command.StreamRef, Metadata: cloneInteractionMap(command.Metadata),
		},
	)
	return interactionTerminal(value), translateInteractionFleetDBError(err)
}

func (transport *interactionFleetDBAuthorityTransport) GetTerminal(
	ctx context.Context,
	workspace,
	terminalID string,
) (*interaction.TerminalSession, error) {
	if transport == nil || transport.transport == nil {
		return nil, interactionfleetdb.ErrTransportUnavailable
	}
	value, err := transport.transport.GetInteractionTerminal(ctx, workspace, terminalID)
	return interactionTerminal(value), translateInteractionFleetDBError(err)
}

func (transport *interactionFleetDBAuthorityTransport) UpdateTerminalOwned(
	ctx context.Context,
	owner authority.SessionOwner,
	workspace,
	terminalID string,
	update interaction.TerminalUpdate,
) (*interaction.TerminalSession, error) {
	if transport == nil || transport.transport == nil {
		return nil, interactionfleetdb.ErrTransportUnavailable
	}
	proof, closeProof, err := interactionOwnedProof(workspace, owner)
	if err != nil {
		return nil, err
	}
	defer closeProof()
	if update.Status == nil {
		return nil, interactionfleetdb.ErrTransportInvalid
	}
	value, err := transport.transport.UpdateInteractionTerminal(
		ctx,
		infrafleetdb.InteractionTerminalUpdateInput{
			Proof: proof, TerminalID: terminalID, Status: string(*update.Status),
			StreamRef:            cloneInteractionString(update.StreamRef),
			TranscriptArtifactID: cloneInteractionString(update.TranscriptArtifactID),
			AttachedClients:      cloneInteractionInt(update.AttachedClients),
		},
	)
	return interactionTerminal(value), translateInteractionFleetDBError(err)
}

func (transport *interactionFleetDBAuthorityTransport) EnqueueInbox(
	ctx context.Context,
	command interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	if transport == nil || transport.transport == nil {
		return nil, interactionfleetdb.ErrTransportUnavailable
	}
	value, err := transport.transport.EnqueueInteractionInbox(
		ctx,
		infrafleetdb.InteractionInboxEnqueueInput{
			WorkspaceKey: command.WorkspaceKey, MessageID: command.MessageID,
			TargetAgentID: command.TargetAgentID, SessionID: command.SessionID,
			Body: command.Body, SourceKind: command.SourceKind, SourceRef: command.SourceRef,
			DriverRunID: command.DriverRunID, TaskRunID: command.TaskRunID,
			TriggerEventID: command.TriggerEventID, TriggerDeliveryID: command.TriggerDeliveryID,
			DedupeKey: command.DedupeKey,
		},
	)
	return interactionInbox(value), translateInteractionFleetDBError(err)
}

func (transport *interactionFleetDBAuthorityTransport) ClaimInboxOwned(
	ctx context.Context,
	owner authority.SessionOwner,
	command interaction.ClaimInboxCommand,
) (*interaction.InboxMessage, error) {
	if transport == nil || transport.transport == nil {
		return nil, interactionfleetdb.ErrTransportUnavailable
	}
	proof, closeProof, err := interactionOwnedProof(command.WorkspaceKey, owner)
	if err != nil {
		return nil, err
	}
	defer closeProof()
	value, err := transport.transport.ClaimInteractionInbox(
		ctx,
		infrafleetdb.InteractionInboxClaimInput{Proof: proof, LeaseTTL: command.LeaseTTL},
	)
	return interactionInbox(value), translateInteractionFleetDBError(err)
}

func (transport *interactionFleetDBAuthorityTransport) CompleteInboxOwned(
	ctx context.Context,
	owner authority.SessionOwner,
	command interaction.CompleteInboxCommand,
) (*interaction.InboxMessage, error) {
	if transport == nil || transport.transport == nil {
		return nil, interactionfleetdb.ErrTransportUnavailable
	}
	proof, closeProof, err := interactionOwnedProof(command.WorkspaceKey, owner)
	if err != nil {
		return nil, err
	}
	defer closeProof()
	value, err := transport.transport.CompleteInteractionInbox(
		ctx,
		infrafleetdb.InteractionInboxCompleteInput{
			Proof: proof, InboxMessageID: command.MessageID, Attempt: command.Attempt,
			Status:            string(command.Status),
			DeliveredThreadID: command.DeliveredThreadID, ErrorClass: command.ErrorClass,
		},
	)
	return interactionInbox(value), translateInteractionFleetDBError(err)
}

func (transport *interactionFleetDBAuthorityTransport) ListActivity(
	ctx context.Context,
	workspace,
	agentID string,
	limit int,
) ([]interaction.Activity, error) {
	if transport == nil || transport.transport == nil {
		return nil, interactionfleetdb.ErrTransportUnavailable
	}
	values, err := transport.transport.ListInteractionActivity(ctx, workspace, agentID, limit)
	return interactionActivities(values), translateInteractionFleetDBError(err)
}

func interactionOwnedProof(
	workspace string,
	owner authority.SessionOwner,
) (infrafleetdb.InteractionSessionAuthorityProof, func(), error) {
	raw := owner.ConsumeLeaseCredential()
	if len(raw) == 0 {
		return infrafleetdb.InteractionSessionAuthorityProof{}, func() {},
			fmt.Errorf("session authority has no one-use lease credential: %w", interactionfleetdb.ErrTransportInvalid)
	}
	proof := infrafleetdb.InteractionSessionAuthorityProof{
		WorkspaceKey: workspace, SessionID: owner.SessionID, AgentID: owner.AgentID,
		TerminalID: owner.TerminalID, NodeID: owner.NodeID, LeaseID: owner.LeaseID,
		LeaseToken: string(raw), FencingToken: owner.FencingToken,
	}
	clear(raw)
	closeProof := func() {
		proof.LeaseToken = ""
		owner.CloseLeaseCredential()
	}
	return proof, closeProof, nil
}

func interactionSession(value *domain.AgentSession) *interaction.AgentSession {
	if value == nil {
		return nil
	}
	return &interaction.AgentSession{
		WorkspaceKey: value.WorkspaceKey, SessionID: value.SessionID,
		AgentID: value.AgentID, NodeID: value.NodeID, Kind: interaction.SessionKind(value.Kind),
		TaskID: value.TaskID, TerminalID: value.TerminalID, ParentSessionID: value.ParentSessionID,
		Status: interaction.SessionStatus(value.Status), Phase: value.Phase, Attempt: value.Attempt,
		CurrentLeaseID:           value.CurrentLeaseID,
		CurrentLeaseFencingToken: value.CurrentLeaseFencingToken,
		Summary:                  value.Summary, ErrorClass: value.ErrorClass, ExitCode: cloneInteractionInt(value.ExitCode),
		TranscriptArtifactID: strings.TrimPrefix(value.Metadata["transcript_ref"], "artifact://"),
		Metadata:             cloneInteractionMap(value.Metadata),
		StartedAt:            value.StartedAt, LastHeartbeat: value.LastHeartbeat,
		FinishedAt: cloneInteractionTime(value.FinishedAt), CreatedAt: value.CreatedAt,
		UpdatedAt: value.UpdatedAt,
	}
}

func interactionLease(value *domain.AgentLease) *interaction.SessionLease {
	if value == nil {
		return nil
	}
	return &interaction.SessionLease{
		WorkspaceKey: value.WorkspaceKey, LeaseID: value.LeaseID,
		SessionID: value.SessionID, AgentID: value.AgentID, NodeID: value.NodeID,
		FencingToken: value.FencingToken, Status: string(value.Status),
		ExpiresAt: value.ExpiresAt, LastHeartbeat: value.LastHeartbeat,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func interactionTerminal(value *domain.TerminalSession) *interaction.TerminalSession {
	if value == nil {
		return nil
	}
	return &interaction.TerminalSession{
		WorkspaceKey: value.WorkspaceKey, TerminalID: value.TerminalID,
		AgentID: value.AgentID, SessionID: value.SessionID, NodeID: value.NodeID,
		TaskID: value.TaskID, Title: value.Title, Kind: value.Kind,
		Status: interaction.TerminalStatus(value.Status), PTYProvider: value.PTYProvider,
		StreamRef: value.StreamRef, TranscriptArtifactID: value.TranscriptRef,
		AttachedClients: value.AttachedClients, Metadata: cloneInteractionMap(value.Metadata),
		StartedAt: value.StartedAt, LastSeenAt: value.LastSeenAt,
		EndedAt: cloneInteractionTime(value.EndedAt), CreatedAt: value.CreatedAt,
		UpdatedAt: value.UpdatedAt,
	}
}

func interactionInbox(value *domain.AgentInboxMessage) *interaction.InboxMessage {
	if value == nil {
		return nil
	}
	return &interaction.InboxMessage{
		WorkspaceKey: value.WorkspaceKey, MessageID: value.InboxMessageID,
		Cursor: value.Cursor, TargetAgentID: value.TargetAgentID, SessionID: value.SessionID,
		Body: value.Body, Status: interaction.InboxStatus(value.Status),
		SourceKind: value.SourceKind, SourceRef: value.SourceRef,
		DriverRunID: value.DriverRunID, TaskRunID: value.TaskRunID,
		TriggerEventID: value.TriggerEventID, TriggerDeliveryID: value.TriggerDeliveryID,
		DedupeKey: value.DedupeKey, Attempt: value.Attempt, ClaimedBy: value.ClaimedBy,
		ClaimExpiresAt: cloneInteractionTime(value.ClaimExpiresAt), ErrorClass: value.ErrorClass,
		DeliveredThreadID: value.DeliveredThreadID,
		DeliveredAt:       cloneInteractionTime(value.DeliveredAt),
		CreatedAt:         value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func interactionActivities(values []infrafleetdb.InteractionActivity) []interaction.Activity {
	if values == nil {
		return nil
	}
	result := make([]interaction.Activity, len(values))
	for index, value := range values {
		result[index] = interaction.Activity{
			WorkspaceKey: value.WorkspaceKey, AgentID: value.AgentID,
			Kind: interaction.ActivityKind(value.Kind), SourceID: value.SourceID,
			TaskID: value.TaskID, Status: value.Status, Summary: value.Summary,
			StartedAt: value.StartedAt, FinishedAt: cloneInteractionTime(value.FinishedAt),
		}
	}
	return result
}

func cloneInteractionMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneInteractionString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneInteractionInt(value *int) *int {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneInteractionTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func translateInteractionFleetDBError(err error) error {
	if err == nil {
		return nil
	}
	var translated error
	switch {
	case errors.Is(err, infrafleetdb.ErrInteractionInvalid):
		translated = interactionfleetdb.ErrTransportInvalid
	case errors.Is(err, infrafleetdb.ErrInteractionNotFound):
		translated = interactionfleetdb.ErrTransportNotFound
	case errors.Is(err, infrafleetdb.ErrInteractionNotOwner):
		translated = interactionfleetdb.ErrTransportNotOwner
	case errors.Is(err, infrafleetdb.ErrInteractionConflict):
		translated = interactionfleetdb.ErrTransportConflict
	case errors.Is(err, infrafleetdb.ErrInteractionInvalidTransition):
		translated = interactionfleetdb.ErrTransportInvalidTransition
	case errors.Is(err, infrafleetdb.ErrInteractionInvalidPersistedState):
		translated = interactionfleetdb.ErrTransportInvalidPersistedState
	default:
		translated = interactionfleetdb.ErrTransportUnavailable
	}
	return errors.Join(translated, err)
}
