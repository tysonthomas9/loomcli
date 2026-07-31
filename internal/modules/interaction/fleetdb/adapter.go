// Package fleetdb adapts the shared FleetDB authority-validation transport to
// Interaction's credential-verification port. It deliberately implements no
// mutation store: FleetDB must first expose compound owner-fenced Interaction
// commands before production may publish those capabilities.
package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

var (
	ErrTransportInvalid               = errors.New("interaction fleetdb transport: invalid request")
	ErrTransportNotFound              = errors.New("interaction fleetdb transport: not found")
	ErrTransportNotOwner              = errors.New("interaction fleetdb transport: not owner")
	ErrTransportConflict              = errors.New("interaction fleetdb transport: conflict")
	ErrTransportInvalidTransition     = errors.New("interaction fleetdb transport: invalid transition")
	ErrTransportInvalidPersistedState = errors.New("interaction fleetdb transport: invalid persisted state")
	ErrTransportUnavailable           = errors.New("interaction fleetdb transport: unavailable")
)

type SessionAuthorityProofWire struct {
	WorkspaceKey string
	SessionID    string
	AgentID      string
	TerminalID   string
	NodeID       string
	LeaseID      string
	LeaseToken   []byte
	FencingToken int64
}

type SessionAuthorityValidationWire struct {
	WorkspaceKey string
	SessionID    string
	AgentID      string
	TerminalID   string
	NodeID       string
	LeaseID      string
	FencingToken int64
	ExpiresAt    time.Time
}

type AuthorityTransport interface {
	ValidateSessionAuthority(
		context.Context,
		SessionAuthorityProofWire,
	) (*SessionAuthorityValidationWire, error)
}

type AuthorityAdapter struct {
	transport AuthorityTransport
}

var _ interaction.SessionAuthorityValidator = (*AuthorityAdapter)(nil)

func NewAuthorityAdapter(transport AuthorityTransport) (*AuthorityAdapter, error) {
	if transport == nil {
		return nil, fmt.Errorf("interaction fleetdb authority adapter: nil transport: %w", interaction.ErrUnavailable)
	}
	return &AuthorityAdapter{transport: transport}, nil
}

func (adapter *AuthorityAdapter) ValidateSessionAuthority(
	ctx context.Context,
	proof interaction.SessionAuthorityProof,
) (*interaction.SessionAuthorityValidation, error) {
	if adapter == nil || adapter.transport == nil {
		return nil, interaction.ErrUnavailable
	}
	if proof.Token == nil {
		return nil, fmt.Errorf("session lease token is required: %w", interaction.ErrInvalid)
	}
	token := proof.Token.Bytes()
	defer zeroInteractionBytes(token)
	if len(token) == 0 {
		return nil, fmt.Errorf("session lease token is required: %w", interaction.ErrInvalid)
	}
	value, err := adapter.transport.ValidateSessionAuthority(ctx, SessionAuthorityProofWire{
		WorkspaceKey: proof.WorkspaceKey,
		SessionID:    proof.SessionID,
		AgentID:      proof.AgentID,
		TerminalID:   proof.TerminalID,
		NodeID:       proof.NodeID,
		LeaseID:      proof.LeaseID,
		LeaseToken:   token,
		FencingToken: proof.FencingToken,
	})
	if err != nil {
		return nil, mapError("validate session authority", err)
	}
	if !matchesSessionAuthorityProof(value, proof) {
		return nil, fmt.Errorf(
			"session authority transport returned a mismatched validation: %w",
			interaction.ErrInvalidPersistedState,
		)
	}
	return &interaction.SessionAuthorityValidation{
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

func matchesSessionAuthorityProof(
	value *SessionAuthorityValidationWire,
	proof interaction.SessionAuthorityProof,
) bool {
	return value != nil &&
		value.WorkspaceKey == proof.WorkspaceKey &&
		value.SessionID == proof.SessionID &&
		value.AgentID == proof.AgentID &&
		value.NodeID == proof.NodeID &&
		value.LeaseID == proof.LeaseID &&
		value.FencingToken == proof.FencingToken &&
		(proof.TerminalID == "" || value.TerminalID == proof.TerminalID) &&
		!value.ExpiresAt.IsZero()
}

func zeroInteractionBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var sentinel error
	switch {
	case errors.Is(err, ErrTransportInvalid):
		sentinel = interaction.ErrInvalid
	case errors.Is(err, ErrTransportNotFound):
		sentinel = interaction.ErrNotFound
	case errors.Is(err, ErrTransportNotOwner):
		sentinel = interaction.ErrNotOwner
	case errors.Is(err, ErrTransportConflict):
		sentinel = interaction.ErrConflict
	case errors.Is(err, ErrTransportInvalidTransition):
		sentinel = interaction.ErrInvalidTransition
	case errors.Is(err, ErrTransportInvalidPersistedState):
		sentinel = interaction.ErrInvalidPersistedState
	default:
		sentinel = interaction.ErrUnavailable
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(sentinel, err))
}
