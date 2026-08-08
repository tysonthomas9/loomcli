package interaction

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type Service struct {
	sessions    SessionStore
	transcripts TranscriptArtifactStore
	terminals   TerminalStore
	inbox       InboxStore
	activity    ActivitySource
	admission   *authority.Admission
	now         func() time.Time
}

var _ API = (*Service)(nil)

func New(
	sessions SessionStore,
	transcripts TranscriptArtifactStore,
	terminals TerminalStore,
	inbox InboxStore,
	activity ActivitySource,
	admission *authority.Admission,
	now func() time.Time,
) (*Service, error) {
	if sessions == nil || transcripts == nil || terminals == nil || inbox == nil ||
		activity == nil || admission == nil || now == nil {
		return nil, fmt.Errorf("compose Interaction: every owned port, combined activity projection, admission, and clock are required: %w", ErrUnavailable)
	}
	return &Service{
		sessions: sessions, transcripts: transcripts, terminals: terminals, inbox: inbox,
		activity: activity, admission: admission, now: now,
	}, nil
}

const maxSessionTranscriptBytes = (64 << 20) - (1 << 20)

// PublishTranscript persists canonical transcript bytes through Interaction's
// narrow Artifacts port, then links the deterministic artifact to the exact
// still-live session generation. Authority is resolved before the upload and
// the final PatchOwned revalidates the lease and fence, so a stale child cannot
// attach evidence to a successor session.
func (service *Service) PublishTranscript(
	ctx context.Context,
	auth authority.SessionAuthority,
	command PublishTranscriptCommand,
) (*AgentSession, error) {
	command = normalizeTranscriptPublish(command)
	if err := service.requireSession(
		ctx,
		ActionPublishTranscript,
		command.WorkspaceKey,
		command.SessionID,
		"",
		auth,
	); err != nil {
		return nil, err
	}
	if err := validateTranscriptPublish(command); err != nil {
		return nil, err
	}
	session, err := service.sessions.Get(ctx, command.WorkspaceKey, command.SessionID)
	if err != nil {
		return nil, fmt.Errorf("load transcript AgentSession: %w", err)
	}
	if err := validateSession(session, command.WorkspaceKey, command.SessionID, auth.AgentID()); err != nil {
		return nil, err
	}
	return service.persistOwnedTranscript(ctx, auth, command, session)
}

func normalizeTranscriptPublish(command PublishTranscriptCommand) PublishTranscriptCommand {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.Content = append([]byte(nil), command.Content...)
	command.Metadata = cloneMetadata(command.Metadata)
	return command
}

func (service *Service) persistOwnedTranscript(
	ctx context.Context,
	auth authority.SessionAuthority,
	command PublishTranscriptCommand,
	session *AgentSession,
) (*AgentSession, error) {
	artifactID := "transcript-" + command.SessionID
	persistedID, err := service.transcripts.CreateContent(ctx, auth, TranscriptArtifactCreate{
		WorkspaceKey: command.WorkspaceKey,
		ArtifactID:   artifactID,
		AgentID:      session.AgentID,
		SessionID:    session.SessionID,
		TaskID:       session.TaskID,
		Content:      command.Content,
		Metadata:     command.Metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("publish session transcript artifact: %w", err)
	}
	if strings.TrimSpace(persistedID) != artifactID {
		return nil, fmt.Errorf("transcript artifact store returned mismatched identity: %w", ErrInvalidPersistedState)
	}
	now := service.now()
	updated, lease, err := service.sessions.PatchOwned(
		ctx,
		command.WorkspaceKey,
		sessionOwner(auth),
		SessionPatch{TranscriptArtifactID: &artifactID, At: now},
	)
	if err != nil {
		return nil, fmt.Errorf("link owned AgentSession transcript: %w", err)
	}
	if err := validateSession(updated, command.WorkspaceKey, command.SessionID, auth.AgentID()); err != nil {
		return nil, err
	}
	if err := validateOwnedLease(lease, command.WorkspaceKey, sessionOwner(auth), now, true); err != nil {
		return nil, err
	}
	if updated.TranscriptArtifactID != artifactID {
		return nil, fmt.Errorf("session store did not link transcript artifact: %w", ErrInvalidPersistedState)
	}
	return cloneSession(updated), nil
}

func validateTranscriptPublish(command PublishTranscriptCommand) error {
	if command.WorkspaceKey == "" || command.SessionID == "" {
		return fmt.Errorf("canonical transcript workspace and session are required: %w", ErrInvalid)
	}
	if len(command.Content) == 0 || len(command.Content) > maxSessionTranscriptBytes {
		return fmt.Errorf("canonical transcript must contain 1..%d bytes: %w", maxSessionTranscriptBytes, ErrInvalid)
	}
	if len(command.Metadata) > maxSessionPatchMetadataItems {
		return fmt.Errorf("transcript metadata exceeds %d entries: %w", maxSessionPatchMetadataItems, ErrInvalid)
	}
	for key, value := range command.Metadata {
		if !validSessionPatchMetadata(key, value) {
			return fmt.Errorf("invalid transcript metadata %q: %w", key, ErrInvalid)
		}
	}
	return nil
}

func (service *Service) StartSession(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command StartSessionCommand,
) (SessionStart, error) {
	command = normalizeStart(command)
	if err := service.requireOperator(ActionStartSession, command.WorkspaceKey, auth); err != nil {
		return SessionStart{}, err
	}
	if err := validateStart(command); err != nil {
		return SessionStart{}, err
	}
	start, err := service.sessions.Start(ctx, command)
	if err != nil {
		return SessionStart{}, fmt.Errorf("atomically start AgentSession %q: %w", command.SessionID, err)
	}
	if err := validateSession(start.Session, command.WorkspaceKey, command.SessionID, command.AgentID); err != nil {
		if start.Token != nil {
			start.Token.Close()
		}
		return SessionStart{}, err
	}
	if err := validateStartLease(start.Session, start.Lease, start.Token, command, service.now()); err != nil {
		if start.Token != nil {
			start.Token.Close()
		}
		return SessionStart{}, err
	}
	return SessionStart{
		Session: cloneSession(start.Session),
		Lease:   cloneLease(start.Lease),
		Token:   start.Token,
	}, nil
}

func (service *Service) RecoverSessionStart(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command RecoverSessionStartCommand,
) (SessionStart, error) {
	command = normalizeRecoveryStart(command)
	if err := service.requireOperator(
		ActionRecoverStart,
		command.Original.WorkspaceKey,
		auth,
	); err != nil {
		return SessionStart{}, err
	}
	return service.recoverSessionStart(ctx, command)
}

func (service *Service) RecoverSessionStartAsSystem(
	ctx context.Context,
	auth authority.SystemAuthority,
	command RecoverSessionStartCommand,
) (SessionStart, error) {
	command = normalizeRecoveryStart(command)
	if err := service.requireSystem(
		ActionRecoverStart,
		command.Original.WorkspaceKey,
		auth,
	); err != nil {
		return SessionStart{}, err
	}
	return service.recoverSessionStart(ctx, command)
}

//nolint:funlen // Recovery keeps replay, owner-fence, terminal, and ambiguous-start outcomes in one deterministic flow.
func (service *Service) recoverSessionStart(
	ctx context.Context,
	command RecoverSessionStartCommand,
) (SessionStart, error) {
	if err := validateRecoveryStart(command); err != nil {
		return SessionStart{}, err
	}
	start, err := service.sessions.RecoverStart(ctx, command)
	if err != nil {
		return SessionStart{}, fmt.Errorf(
			"atomically recover AgentSession start %q: %w",
			command.Original.SessionID,
			err,
		)
	}
	if err := validateSession(
		start.Session,
		command.Original.WorkspaceKey,
		command.Original.SessionID,
		command.Original.AgentID,
	); err != nil {
		if start.Token != nil {
			start.Token.Close()
		}
		return SessionStart{}, err
	}
	if !sessionMatchesStartDefinition(start.Session, command.Original) ||
		start.Session.Status != SessionStarting ||
		start.Session.CurrentLeaseID != command.ReplacementLeaseID ||
		start.Session.CurrentLeaseFencingToken <= command.ExpectedLeaseFencingToken ||
		start.Lease == nil ||
		start.Lease.LeaseID != command.ReplacementLeaseID {
		if start.Token != nil {
			start.Token.Close()
		}
		return SessionStart{}, fmt.Errorf(
			"start recovery returned a mismatched generation: %w",
			ErrInvalidPersistedState,
		)
	}
	if err := validateStartLease(
		start.Session,
		start.Lease,
		start.Token,
		command.Original,
		service.now(),
	); err != nil {
		if start.Token != nil {
			start.Token.Close()
		}
		return SessionStart{}, err
	}
	return SessionStart{
		Session: cloneSession(start.Session),
		Lease:   cloneLease(start.Lease),
		Token:   start.Token,
	}, nil
}

func sessionMatchesStartDefinition(
	session *AgentSession,
	command StartSessionCommand,
) bool {
	return session != nil &&
		session.WorkspaceKey == command.WorkspaceKey &&
		session.SessionID == command.SessionID &&
		session.AgentID == command.AgentID &&
		session.NodeID == command.NodeID &&
		session.Kind == command.Kind &&
		session.TaskID == command.TaskID &&
		session.TerminalID == command.TerminalID &&
		session.ParentSessionID == command.ParentSessionID &&
		session.Phase == command.Phase &&
		session.Attempt == command.Attempt &&
		metadataMatches(session.Metadata, command.Metadata)
}

func metadataMatches(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

//nolint:funlen // Session patching keeps admission, fence validation, persistence, and exact-result checks atomic.
func (service *Service) PatchSession(
	ctx context.Context,
	auth authority.SessionAuthority,
	command PatchSessionCommand,
) (*AgentSession, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.MetadataUpserts = cloneMetadata(command.MetadataUpserts)
	command.MetadataRemovals = append([]string(nil), command.MetadataRemovals...)
	if err := service.requireSession(
		ctx,
		ActionPatchSession,
		command.WorkspaceKey,
		command.SessionID,
		"",
		auth,
	); err != nil {
		return nil, err
	}
	if err := validateSessionPatch(command); err != nil {
		return nil, err
	}
	now := service.now()
	updated, lease, err := service.sessions.PatchOwned(
		ctx,
		command.WorkspaceKey,
		sessionOwner(auth),
		SessionPatch{
			Phase: command.Phase, MetadataUpserts: command.MetadataUpserts,
			MetadataRemovals:     command.MetadataRemovals,
			TranscriptArtifactID: command.TranscriptArtifactID,
			At:                   now,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("patch owned AgentSession: %w", err)
	}
	if err := validateSession(updated, command.WorkspaceKey, command.SessionID, auth.AgentID()); err != nil {
		return nil, err
	}
	if err := validateOwnedLease(
		lease,
		command.WorkspaceKey,
		sessionOwner(auth),
		now,
		true,
	); err != nil {
		return nil, err
	}
	if err := validatePatchedSession(updated, command); err != nil {
		return nil, err
	}
	return cloneSession(updated), nil
}

func (service *Service) HeartbeatSession(
	ctx context.Context,
	auth authority.SessionAuthority,
	command HeartbeatSessionCommand,
) (*AgentSession, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.SessionID = strings.TrimSpace(command.SessionID)
	if err := service.requireSession(ctx, ActionHeartbeatSession, command.WorkspaceKey, command.SessionID, "", auth); err != nil {
		return nil, err
	}
	if command.LeaseTTL <= 0 {
		return nil, fmt.Errorf("positive session lease ttl is required: %w", ErrInvalid)
	}
	owner := sessionOwner(auth)
	now := service.now()
	phase := strings.TrimSpace(command.Phase)
	updated, lease, err := service.sessions.HeartbeatOwned(ctx, command.WorkspaceKey, owner, SessionHeartbeat{
		Phase: phase, At: now, LeaseTTL: command.LeaseTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("heartbeat owned AgentSession: %w", err)
	}
	if err := validateSession(updated, command.WorkspaceKey, command.SessionID, auth.AgentID()); err != nil {
		return nil, err
	}
	if err := validateOwnedLease(lease, command.WorkspaceKey, owner, now, true); err != nil {
		return nil, err
	}
	return cloneSession(updated), nil
}

const (
	maxSessionPatchPhaseBytes    = 256
	maxSessionPatchMetadataItems = 64
	maxSessionPatchMetadataKey   = 128
	maxSessionPatchMetadataValue = 1024
)

//nolint:cyclop // Session patch validation exhaustively checks each optional fenced-state transition.
func validateSessionPatch(command PatchSessionCommand) error {
	if command.Phase == nil && command.TranscriptArtifactID == nil &&
		len(command.MetadataUpserts) == 0 && len(command.MetadataRemovals) == 0 {
		return fmt.Errorf("at least one session patch field is required: %w", ErrInvalid)
	}
	if command.Phase != nil {
		if *command.Phase != strings.TrimSpace(*command.Phase) ||
			len(*command.Phase) > maxSessionPatchPhaseBytes {
			return fmt.Errorf("session phase must be canonical and at most %d bytes: %w", maxSessionPatchPhaseBytes, ErrInvalid)
		}
	}
	if len(command.MetadataUpserts) > maxSessionPatchMetadataItems ||
		len(command.MetadataRemovals) > maxSessionPatchMetadataItems {
		return fmt.Errorf("session metadata patch exceeds %d entries: %w", maxSessionPatchMetadataItems, ErrInvalid)
	}
	removals := make(map[string]struct{}, len(command.MetadataRemovals))
	for key, value := range command.MetadataUpserts {
		if !validSessionPatchMetadata(key, value) {
			return fmt.Errorf("invalid session metadata upsert %q: %w", key, ErrInvalid)
		}
	}
	for _, key := range command.MetadataRemovals {
		if !validSessionPatchMetadata(key, "") {
			return fmt.Errorf("invalid session metadata removal %q: %w", key, ErrInvalid)
		}
		if _, exists := removals[key]; exists {
			return fmt.Errorf("duplicate session metadata removal %q: %w", key, ErrInvalid)
		}
		if _, conflict := command.MetadataUpserts[key]; conflict {
			return fmt.Errorf("session metadata key %q cannot be upserted and removed: %w", key, ErrInvalid)
		}
		removals[key] = struct{}{}
	}
	if command.TranscriptArtifactID != nil {
		value := *command.TranscriptArtifactID
		if value != strings.TrimSpace(value) || len(value) > maxSessionPatchMetadataValue {
			return fmt.Errorf("transcript artifact id must be canonical and bounded: %w", ErrInvalid)
		}
		if _, conflict := command.MetadataUpserts["transcript_ref"]; conflict {
			return fmt.Errorf("transcript_ref must use transcript_artifact_id: %w", ErrInvalid)
		}
		if _, conflict := removals["transcript_ref"]; conflict {
			return fmt.Errorf("transcript_ref must use transcript_artifact_id: %w", ErrInvalid)
		}
	}
	return nil
}

func validSessionPatchMetadata(key, value string) bool {
	return key != "" && key == strings.TrimSpace(key) &&
		len(key) <= maxSessionPatchMetadataKey && len(value) <= maxSessionPatchMetadataValue
}

func validatePatchedSession(session *AgentSession, command PatchSessionCommand) error {
	if command.Phase != nil && session.Phase != *command.Phase {
		return fmt.Errorf("session store did not apply phase patch: %w", ErrInvalidPersistedState)
	}
	for key, value := range command.MetadataUpserts {
		if session.Metadata[key] != value {
			return fmt.Errorf("session store did not apply metadata upsert %q: %w", key, ErrInvalidPersistedState)
		}
	}
	for _, key := range command.MetadataRemovals {
		if _, exists := session.Metadata[key]; exists {
			return fmt.Errorf("session store did not apply metadata removal %q: %w", key, ErrInvalidPersistedState)
		}
	}
	if command.TranscriptArtifactID != nil && session.TranscriptArtifactID != *command.TranscriptArtifactID {
		return fmt.Errorf("session store did not apply transcript linkage: %w", ErrInvalidPersistedState)
	}
	return nil
}

func (service *Service) FinishSession(
	ctx context.Context,
	auth authority.SessionAuthority,
	command FinishSessionCommand,
) (*AgentSession, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.SessionID = strings.TrimSpace(command.SessionID)
	if err := service.requireSession(ctx, ActionFinishSession, command.WorkspaceKey, command.SessionID, "", auth); err != nil {
		return nil, err
	}
	if !command.Status.Terminal() {
		return nil, fmt.Errorf("finish status %q is not terminal: %w", command.Status, ErrInvalid)
	}
	owner := sessionOwner(auth)
	finishedAt := service.now()
	summary := strings.TrimSpace(command.Summary)
	errorClass := strings.TrimSpace(command.ErrorClass)
	transcript := strings.TrimSpace(command.TranscriptArtifactID)
	result, err := service.sessions.FinishOwned(ctx, command.WorkspaceKey, owner, SessionFinish{
		Status: command.Status, FinishedAt: finishedAt, Summary: summary,
		ErrorClass: errorClass, ExitCode: command.ExitCode, TranscriptArtifactID: transcript,
	})
	if err != nil {
		return nil, fmt.Errorf("finish owned AgentSession: %w", err)
	}
	if err := validateFinishedSession(result.Session, command.WorkspaceKey, command.SessionID, auth.AgentID(), command.Status); err != nil {
		return nil, err
	}
	if err := validateOwnedLease(result.Lease, command.WorkspaceKey, owner, finishedAt, false); err != nil {
		return nil, err
	}
	if result.Session.Kind == SessionKindInteractive {
		if err := validateAtomicFinishedTerminal(
			result.Session,
			result.Terminal,
			result.Lease,
			owner,
			command.Status,
		); err != nil {
			return nil, err
		}
	} else if result.Terminal != nil {
		return nil, fmt.Errorf(
			"non-interactive finish returned a terminal: %w",
			ErrInvalidPersistedState,
		)
	}
	return cloneSession(result.Session), nil
}

func validateAtomicFinishedTerminal(
	session *AgentSession,
	terminal *TerminalSession,
	lease *SessionLease,
	owner authority.SessionOwner,
	status SessionStatus,
) error {
	expectedTerminalStatus := TerminalExited
	if status == SessionFailed || status == SessionInterrupted {
		expectedTerminalStatus = TerminalFailed
	}
	if terminal == nil || lease == nil ||
		session.TerminalID == "" ||
		session.TerminalID != owner.TerminalID ||
		terminal.WorkspaceKey != session.WorkspaceKey ||
		terminal.TerminalID != session.TerminalID ||
		terminal.SessionID != session.SessionID ||
		terminal.AgentID != session.AgentID ||
		terminal.NodeID != session.NodeID ||
		terminal.Status != expectedTerminalStatus ||
		terminal.EndedAt == nil ||
		terminal.AttachedClients != 0 ||
		session.CurrentLeaseID != lease.LeaseID ||
		session.CurrentLeaseFencingToken != lease.FencingToken {
		return fmt.Errorf(
			"interactive finish store returned a cross-owner or non-atomic result: %w",
			ErrInvalidPersistedState,
		)
	}
	return nil
}

func (service *Service) ForceInterrupt(
	ctx context.Context,
	auth authority.SystemAuthority,
	command ForceInterruptCommand,
) (ForceInterruptResult, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.AgentID = strings.TrimSpace(command.AgentID)
	command.TerminalID = strings.TrimSpace(command.TerminalID)
	command.ExpectedLeaseID = strings.TrimSpace(command.ExpectedLeaseID)
	command.StreamRef = strings.TrimSpace(command.StreamRef)
	command.TerminalTab = strings.TrimSpace(command.TerminalTab)
	command.Reason = strings.TrimSpace(command.Reason)
	if err := service.requireSystem(ActionForceInterrupt, command.WorkspaceKey, auth); err != nil {
		return ForceInterruptResult{}, err
	}
	if command.SessionID == "" || command.AgentID == "" ||
		command.TerminalID == "" || command.ExpectedLeaseID == "" ||
		command.ExpectedLeaseFencingToken <= 0 ||
		command.StreamRef == "" || command.TerminalTab == "" {
		return ForceInterruptResult{}, fmt.Errorf(
			"session, agent, terminal, expected lease generation, stream, and terminal tab are required: %w",
			ErrInvalid,
		)
	}
	result, err := service.sessions.ForceInterrupt(ctx, command)
	if err != nil {
		return ForceInterruptResult{}, fmt.Errorf(
			"force interrupt exact interactive lifecycle: %w",
			err,
		)
	}
	if err := validateForceInterruptResult(result, command); err != nil {
		return ForceInterruptResult{}, err
	}
	return ForceInterruptResult{
		Session: cloneSession(result.Session), Terminal: cloneTerminal(result.Terminal),
		Lease: cloneLease(result.Lease), Changed: result.Changed,
	}, nil
}

//nolint:cyclop // Exact-result validation keeps every terminal state, owner fence, and interruption invariant explicit.
func validateForceInterruptResult(
	result ForceInterruptResult,
	command ForceInterruptCommand,
) error {
	if result.Session == nil || result.Terminal == nil || result.Lease == nil ||
		result.Session.WorkspaceKey != command.WorkspaceKey ||
		result.Session.SessionID != command.SessionID ||
		result.Session.AgentID != command.AgentID ||
		result.Session.TerminalID != command.TerminalID ||
		result.Session.Kind != SessionKindInteractive ||
		result.Session.FinishedAt == nil ||
		result.Session.NodeID == "" ||
		result.Terminal.WorkspaceKey != command.WorkspaceKey ||
		result.Terminal.SessionID != command.SessionID ||
		result.Terminal.AgentID != command.AgentID ||
		result.Terminal.TerminalID != command.TerminalID ||
		result.Terminal.NodeID != result.Session.NodeID ||
		result.Terminal.StreamRef != command.StreamRef ||
		result.Terminal.Metadata["terminal_tab"] != command.TerminalTab ||
		result.Terminal.EndedAt == nil ||
		result.Terminal.AttachedClients != 0 ||
		result.Lease.WorkspaceKey != command.WorkspaceKey ||
		result.Lease.SessionID != command.SessionID ||
		result.Lease.AgentID != command.AgentID ||
		result.Lease.NodeID != result.Session.NodeID ||
		result.Lease.LeaseID != command.ExpectedLeaseID ||
		result.Lease.FencingToken != command.ExpectedLeaseFencingToken ||
		result.Session.CurrentLeaseID != result.Lease.LeaseID ||
		result.Session.CurrentLeaseFencingToken != result.Lease.FencingToken ||
		result.Lease.Status != "released" ||
		result.Lease.FencingToken <= 0 {
		return fmt.Errorf(
			"force-interrupt store returned a cross-owner or unconverged result: %w",
			ErrInvalidPersistedState,
		)
	}
	terminalized := result.Session.Status.Terminal() &&
		(result.Terminal.Status == TerminalExited ||
			result.Terminal.Status == TerminalFailed) &&
		result.Lease.Status == "released"
	if !terminalized {
		return fmt.Errorf(
			"force-interrupt store returned an unconverged lifecycle: %w",
			ErrInvalidPersistedState,
		)
	}
	return nil
}

func (service *Service) OpenTerminal(
	ctx context.Context,
	auth authority.SessionAuthority,
	command OpenTerminalCommand,
) (*TerminalSession, error) {
	command = normalizeTerminal(command)
	if err := service.requireSession(ctx, ActionOpenTerminal, command.WorkspaceKey, command.SessionID, command.TerminalID, auth); err != nil {
		return nil, err
	}
	if command.AgentID == "" || command.AgentID != auth.AgentID() || command.NodeID == "" || command.NodeID != auth.NodeID() {
		return nil, fmt.Errorf("terminal agent/node does not match session authority: %w", ErrNotOwner)
	}
	owner := sessionOwner(auth)
	value, err := service.terminals.Create(ctx, owner, command)
	if err != nil {
		return nil, fmt.Errorf("open terminal %q: %w", command.TerminalID, err)
	}
	if err := validateTerminal(value, command); err != nil {
		return nil, err
	}
	return cloneTerminal(value), nil
}

func (service *Service) UpdateTerminal(
	ctx context.Context,
	auth authority.SessionAuthority,
	command UpdateTerminalCommand,
) (*TerminalSession, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.TerminalID = strings.TrimSpace(command.TerminalID)
	if err := service.requireSession(ctx, ActionUpdateTerminal, command.WorkspaceKey, auth.SessionID(), command.TerminalID, auth); err != nil {
		return nil, err
	}
	if command.Status == "" {
		return nil, fmt.Errorf("terminal status is required: %w", ErrInvalid)
	}
	switch command.Status {
	case TerminalStarting, TerminalRunning, TerminalExited, TerminalFailed:
	default:
		return nil, fmt.Errorf("terminal status %q is unsupported: %w", command.Status, ErrInvalid)
	}
	if command.AttachedClients != nil && *command.AttachedClients < 0 {
		return nil, fmt.Errorf("attached client count cannot be negative: %w", ErrInvalid)
	}
	now := service.now()
	update := TerminalUpdate{
		Status:          &command.Status,
		AttachedClients: command.AttachedClients,
		LastSeenAt:      &now,
	}
	if command.StreamRef != nil {
		stream := strings.TrimSpace(*command.StreamRef)
		update.StreamRef = &stream
	}
	if command.TranscriptArtifactID != nil {
		transcript := strings.TrimSpace(*command.TranscriptArtifactID)
		update.TranscriptArtifactID = &transcript
	}
	if command.Status == TerminalExited || command.Status == TerminalFailed {
		update.EndedAt = &now
	}
	value, err := service.terminals.Update(ctx, sessionOwner(auth), command.WorkspaceKey, command.TerminalID, update)
	if err != nil {
		return nil, fmt.Errorf("update terminal %q: %w", command.TerminalID, err)
	}
	if value == nil || value.WorkspaceKey != command.WorkspaceKey ||
		value.TerminalID != command.TerminalID || value.SessionID != auth.SessionID() ||
		value.AgentID != auth.AgentID() || value.NodeID != auth.NodeID() ||
		value.Status != command.Status || value.AttachedClients < 0 {
		return nil, fmt.Errorf("terminal store returned a cross-owner or invalid update: %w", ErrInvalidPersistedState)
	}
	return cloneTerminal(value), nil
}

func (service *Service) EnqueueInbox(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command EnqueueInboxCommand,
) (*InboxMessage, error) {
	if err := service.requireOperator(ActionEnqueueInbox, strings.TrimSpace(command.WorkspaceKey), auth); err != nil {
		return nil, err
	}
	return service.enqueueInbox(ctx, command)
}

func (service *Service) EnqueueInboxAsSystem(
	ctx context.Context,
	auth authority.SystemAuthority,
	command EnqueueInboxCommand,
) (*InboxMessage, error) {
	if err := service.requireSystem(ActionEnqueueInbox, strings.TrimSpace(command.WorkspaceKey), auth); err != nil {
		return nil, err
	}
	return service.enqueueInbox(ctx, command)
}

func (service *Service) enqueueInbox(
	ctx context.Context,
	command EnqueueInboxCommand,
) (*InboxMessage, error) {
	command = normalizeInbox(command)
	if command.MessageID == "" || command.TargetAgentID == "" || command.Body == "" {
		return nil, fmt.Errorf("message id, target agent, and body are required: %w", ErrInvalid)
	}
	value, err := service.inbox.Enqueue(ctx, command)
	if err != nil {
		return nil, fmt.Errorf("enqueue inbox message %q: %w", command.MessageID, err)
	}
	if value == nil || value.WorkspaceKey != command.WorkspaceKey ||
		value.MessageID != command.MessageID || value.TargetAgentID != command.TargetAgentID ||
		value.SessionID != command.SessionID || value.Status != InboxQueued {
		return nil, fmt.Errorf("inbox store returned a cross-scope enqueue: %w", ErrInvalidPersistedState)
	}
	return cloneInbox(value), nil
}

func (service *Service) ClaimInbox(
	ctx context.Context,
	auth authority.SessionAuthority,
	command ClaimInboxCommand,
) (*InboxMessage, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.AgentID = strings.TrimSpace(command.AgentID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	if err := service.requireSession(ctx, ActionClaimInbox, command.WorkspaceKey, command.SessionID, "", auth); err != nil {
		return nil, err
	}
	if command.AgentID != auth.AgentID() || command.LeaseTTL <= 0 {
		return nil, fmt.Errorf("claim agent must match authority and ttl must be positive: %w", ErrNotOwner)
	}
	value, err := service.inbox.ClaimNext(ctx, sessionOwner(auth), command)
	if err != nil {
		return nil, fmt.Errorf("claim inbox: %w", err)
	}
	now := service.now()
	if value == nil || value.WorkspaceKey != command.WorkspaceKey ||
		value.TargetAgentID != auth.AgentID() ||
		(value.SessionID != "" && value.SessionID != auth.SessionID()) ||
		value.ClaimedBy != auth.SessionID() || value.Status != InboxQueued ||
		value.Attempt <= 0 || value.ClaimExpiresAt == nil ||
		!now.Before(*value.ClaimExpiresAt) {
		return nil, fmt.Errorf("inbox store returned an invalid or cross-owner claim: %w", ErrInvalidPersistedState)
	}
	return cloneInbox(value), nil
}

func (service *Service) CompleteInbox(
	ctx context.Context,
	auth authority.SessionAuthority,
	command CompleteInboxCommand,
) (*InboxMessage, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.MessageID = strings.TrimSpace(command.MessageID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	if err := service.requireSession(ctx, ActionCompleteInbox, command.WorkspaceKey, command.SessionID, "", auth); err != nil {
		return nil, err
	}
	if command.MessageID == "" || command.Attempt <= 0 ||
		(command.Status != InboxQueued &&
			command.Status != InboxDelivered &&
			command.Status != InboxFailed) {
		return nil, fmt.Errorf(
			"message id, positive attempt, and queued or terminal inbox status are required: %w",
			ErrInvalid,
		)
	}
	value, err := service.inbox.Complete(ctx, sessionOwner(auth), command)
	if err != nil {
		return nil, fmt.Errorf("complete inbox message %q: %w", command.MessageID, err)
	}
	if value == nil || value.WorkspaceKey != command.WorkspaceKey ||
		value.MessageID != command.MessageID ||
		value.TargetAgentID != auth.AgentID() ||
		(value.SessionID != "" && value.SessionID != auth.SessionID()) ||
		value.Attempt != command.Attempt || value.Status != command.Status ||
		value.ClaimedBy != "" || value.ClaimExpiresAt != nil {
		return nil, fmt.Errorf("inbox store returned a cross-owner completion: %w", ErrInvalidPersistedState)
	}
	return cloneInbox(value), nil
}

func (service *Service) ListActivity(
	ctx context.Context,
	auth authority.OperatorAuthority,
	query ActivityQuery,
) ([]Activity, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.AgentID = strings.TrimSpace(query.AgentID)
	if err := service.requireOperator(ActionReadActivity, query.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if query.AgentID == "" || query.Limit < 0 {
		return nil, fmt.Errorf("agent id is required and limit cannot be negative: %w", ErrInvalid)
	}
	out, err := service.activity.ListActivity(ctx, query.WorkspaceKey, query.AgentID, query.Limit)
	if err != nil {
		return nil, fmt.Errorf("read combined activity: %w", err)
	}
	out = append([]Activity(nil), out...)
	for index := range out {
		if out[index].WorkspaceKey != query.WorkspaceKey || out[index].AgentID != query.AgentID ||
			(out[index].Kind != ActivitySession && out[index].Kind != ActivityBatchRun) || out[index].SourceID == "" {
			return nil, fmt.Errorf("activity source returned an invalid or cross-scope row: %w", ErrInvalidPersistedState)
		}
	}
	sort.SliceStable(out, func(left, right int) bool { return out[left].StartedAt.After(out[right].StartedAt) })
	if query.Limit > 0 && len(out) > query.Limit {
		out = out[:query.Limit]
	}
	return out, nil
}

func (service *Service) ReconcileSessions(
	ctx context.Context,
	auth authority.SystemAuthority,
	workspace string,
	now time.Time,
) (int, error) {
	workspace = strings.TrimSpace(workspace)
	if service == nil || service.admission == nil {
		return 0, ErrUnavailable
	}
	if err := service.admission.RequireSystem(ActionReconcileSessions, workspace, auth); err != nil {
		return 0, err
	}
	sessions, err := service.sessions.ListRecoverable(ctx, workspace, now)
	if err != nil {
		return 0, fmt.Errorf("list recoverable sessions: %w", err)
	}
	repaired := 0
	var reconcileErrors []error
	for _, session := range sessions {
		if session == nil || session.Status.Terminal() {
			continue
		}
		updated, changed, err := service.sessions.InterruptIfLeaseMissing(ctx, workspace, session.SessionID, now)
		if err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("interrupt session %q if unowned: %w", session.SessionID, err))
			continue
		}
		if !changed {
			continue
		}
		if err := validateFinishedSession(updated, workspace, session.SessionID, session.AgentID, SessionInterrupted); err != nil {
			reconcileErrors = append(reconcileErrors, err)
			continue
		}
		repaired++
	}
	return repaired, errors.Join(reconcileErrors...)
}
