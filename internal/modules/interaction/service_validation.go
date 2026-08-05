package interaction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func (service *Service) requireOperator(action authority.Action, workspace string, auth authority.OperatorAuthority) error {
	if service == nil || service.admission == nil {
		return ErrUnavailable
	}
	return service.admission.RequireOperator(action, workspace, auth)
}

func (service *Service) requireSystem(action authority.Action, workspace string, auth authority.SystemAuthority) error {
	if service == nil || service.admission == nil {
		return ErrUnavailable
	}
	return service.admission.RequireSystem(action, workspace, auth)
}

func (service *Service) requireSession(
	_ context.Context,
	action authority.Action,
	workspace string,
	sessionID string,
	terminalID string,
	auth authority.SessionAuthority,
) error {
	if service == nil || service.admission == nil {
		return ErrUnavailable
	}
	if err := service.admission.RequireSession(action, workspace, auth); err != nil {
		return err
	}
	if auth.SessionID() == "" || auth.SessionID() != sessionID || auth.AgentID() == "" ||
		(terminalID != "" && auth.TerminalID() != terminalID) ||
		(terminalID == "" && auth.TerminalID() != "" && strings.TrimSpace(auth.TerminalID()) != auth.TerminalID()) {
		return ErrNotOwner
	}
	return nil
}

func sessionOwner(auth authority.SessionAuthority) authority.SessionOwner {
	return auth.SessionOwner()
}

func normalizeStart(command StartSessionCommand) StartSessionCommand {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.AgentID = strings.TrimSpace(command.AgentID)
	command.NodeID = strings.TrimSpace(command.NodeID)
	command.TaskID = strings.TrimSpace(command.TaskID)
	command.TerminalID = strings.TrimSpace(command.TerminalID)
	command.ParentSessionID = strings.TrimSpace(command.ParentSessionID)
	command.Phase = strings.TrimSpace(command.Phase)
	command.LeaseID = strings.TrimSpace(command.LeaseID)
	command.Metadata = cloneMetadata(command.Metadata)
	return command
}

func normalizeRecoveryStart(command RecoverSessionStartCommand) RecoverSessionStartCommand {
	command.Original = normalizeStart(command.Original)
	command.ExpectedLeaseID = strings.TrimSpace(command.ExpectedLeaseID)
	command.ReplacementLeaseID = strings.TrimSpace(command.ReplacementLeaseID)
	return command
}

func validateRecoveryStart(command RecoverSessionStartCommand) error {
	if err := validateStart(command.Original); err != nil {
		return err
	}
	if command.ExpectedLeaseID == "" ||
		command.ExpectedLeaseFencingToken <= 0 ||
		command.ReplacementLeaseID == "" ||
		command.ReplacementLeaseID == command.ExpectedLeaseID ||
		command.ReplacementLeaseTTL <= 0 {
		return fmt.Errorf(
			"expected generation, distinct replacement lease, and positive ttl are required: %w",
			ErrInvalid,
		)
	}
	return nil
}

func validateStart(command StartSessionCommand) error {
	if command.WorkspaceKey == "" || command.SessionID == "" || command.AgentID == "" ||
		command.NodeID == "" || command.LeaseID == "" || command.LeaseTTL <= 0 {
		return fmt.Errorf("workspace, session, agent, node, lease, and positive ttl are required: %w", ErrInvalid)
	}
	switch command.Kind {
	case SessionKindInteractive, SessionKindTask, SessionKindReview:
		return nil
	default:
		return fmt.Errorf("unsupported session kind %q: %w", command.Kind, ErrInvalid)
	}
}

func validateSession(session *AgentSession, workspace, sessionID, agentID string) error {
	if session == nil || session.WorkspaceKey != workspace || session.SessionID != sessionID ||
		session.AgentID != agentID || session.Status.Terminal() {
		return fmt.Errorf("session store returned a mismatched or terminal row: %w", ErrInvalidPersistedState)
	}
	return nil
}

func validateStartLease(
	session *AgentSession,
	lease *SessionLease,
	token *LeaseToken,
	command StartSessionCommand,
	now time.Time,
) error {
	if session == nil || lease == nil ||
		lease.WorkspaceKey != command.WorkspaceKey ||
		lease.SessionID != command.SessionID || lease.AgentID != command.AgentID || lease.NodeID != command.NodeID ||
		lease.FencingToken <= 0 || lease.Status != "active" || !lease.ExpiresAt.After(now) ||
		session.CurrentLeaseID != lease.LeaseID ||
		session.CurrentLeaseFencingToken != lease.FencingToken ||
		token == nil || len(token.value) == 0 || !validTokenHash(lease.TokenHash, token.value) {
		return fmt.Errorf("lease store returned an invalid, expired, or mismatched row: %w", ErrInvalidPersistedState)
	}
	return nil
}

func validateOwnedLease(
	lease *SessionLease,
	workspace string,
	owner authority.SessionOwner,
	at time.Time,
	active bool,
) error {
	if lease == nil || lease.WorkspaceKey != workspace || lease.LeaseID != owner.LeaseID ||
		lease.SessionID != owner.SessionID || lease.AgentID != owner.AgentID || lease.NodeID != owner.NodeID ||
		lease.FencingToken != owner.FencingToken || !validTokenHash(lease.TokenHash, nil) {
		return fmt.Errorf("session store returned a cross-owner lease: %w", ErrInvalidPersistedState)
	}
	if active {
		if lease.Status != "active" || !lease.ExpiresAt.After(at) {
			return fmt.Errorf("session store returned an inactive heartbeat lease: %w", ErrInvalidPersistedState)
		}
		return nil
	}
	if lease.Status != "released" && lease.Status != "expired" {
		return fmt.Errorf("session store returned an unreleased finished lease: %w", ErrInvalidPersistedState)
	}
	return nil
}

func validTokenHash(value string, token []byte) bool {
	if value == "" {
		return true
	}
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	if _, err := hex.DecodeString(value); err != nil {
		return false
	}
	if len(token) == 0 {
		return true
	}
	sum := sha256.Sum256(token)
	return value == hex.EncodeToString(sum[:])
}

func validateFinishedSession(
	session *AgentSession,
	workspace string,
	sessionID string,
	agentID string,
	status SessionStatus,
) error {
	if session == nil || session.WorkspaceKey != workspace || session.SessionID != sessionID ||
		session.AgentID != agentID || session.Status != status || !session.Status.Terminal() ||
		session.FinishedAt == nil {
		return fmt.Errorf("session store returned an invalid terminal row: %w", ErrInvalidPersistedState)
	}
	return nil
}

func normalizeTerminal(command OpenTerminalCommand) OpenTerminalCommand {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.TerminalID = strings.TrimSpace(command.TerminalID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.AgentID = strings.TrimSpace(command.AgentID)
	command.NodeID = strings.TrimSpace(command.NodeID)
	command.TaskID = strings.TrimSpace(command.TaskID)
	command.Title = strings.TrimSpace(command.Title)
	command.Kind = strings.TrimSpace(command.Kind)
	command.PTYProvider = strings.TrimSpace(command.PTYProvider)
	command.StreamRef = strings.TrimSpace(command.StreamRef)
	command.Metadata = cloneMetadata(command.Metadata)
	return command
}

func validateTerminal(value *TerminalSession, command OpenTerminalCommand) error {
	if value == nil || value.WorkspaceKey != command.WorkspaceKey || value.TerminalID != command.TerminalID ||
		value.SessionID != command.SessionID || value.AgentID != command.AgentID || value.NodeID != command.NodeID {
		return fmt.Errorf("terminal store returned a cross-scope row: %w", ErrInvalidPersistedState)
	}
	return nil
}

func normalizeInbox(command EnqueueInboxCommand) EnqueueInboxCommand {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.MessageID = strings.TrimSpace(command.MessageID)
	command.TargetAgentID = strings.TrimSpace(command.TargetAgentID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.Body = strings.TrimSpace(command.Body)
	command.SourceKind = strings.TrimSpace(command.SourceKind)
	command.SourceRef = strings.TrimSpace(command.SourceRef)
	command.DriverRunID = strings.TrimSpace(command.DriverRunID)
	command.TaskRunID = strings.TrimSpace(command.TaskRunID)
	command.TriggerEventID = strings.TrimSpace(command.TriggerEventID)
	command.TriggerDeliveryID = strings.TrimSpace(command.TriggerDeliveryID)
	command.DedupeKey = strings.TrimSpace(command.DedupeKey)
	return command
}
