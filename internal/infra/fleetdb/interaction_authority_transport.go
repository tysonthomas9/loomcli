package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	ErrInteractionInvalid               = errors.New("fleetdb: interaction invalid request")
	ErrInteractionNotFound              = errors.New("fleetdb: interaction not found")
	ErrInteractionNotOwner              = errors.New("fleetdb: interaction not owner")
	ErrInteractionConflict              = errors.New("fleetdb: interaction conflict")
	ErrInteractionInvalidTransition     = errors.New("fleetdb: interaction invalid transition")
	ErrInteractionInvalidPersistedState = errors.New("fleetdb: interaction invalid persisted state")
	ErrInteractionUnavailable           = errors.New("fleetdb: interaction unavailable")
)

// InteractionSessionAuthorityProof is the raw, one-use credential envelope
// accepted by FleetDB's service-authenticated validation endpoint. LeaseToken
// is header-only and must never appear in JSON, returned state, or errors.
type InteractionSessionAuthorityProof struct {
	WorkspaceKey string
	SessionID    string
	AgentID      string
	TerminalID   string
	NodeID       string
	LeaseID      string
	LeaseToken   string `json:"-"`
	FencingToken int64
}

// InteractionSessionAuthorityValidation contains only durable identity and
// the validated lease lifetime. It deliberately omits the raw token and its
// hash.
type InteractionSessionAuthorityValidation struct {
	WorkspaceKey string
	SessionID    string
	AgentID      string
	TerminalID   string
	NodeID       string
	LeaseID      string
	FencingToken int64
	ExpiresAt    time.Time
}

// InteractionAuthorityTransport is the only Interaction transport FleetDB
// currently supports safely. Mutation methods are intentionally absent:
// FleetDB does not yet expose the compound owner-fenced commands needed to
// implement them without time-of-check/time-of-use races.
type InteractionAuthorityTransport interface {
	ValidateSessionAuthority(
		context.Context,
		InteractionSessionAuthorityProof,
	) (*InteractionSessionAuthorityValidation, error)
}

// InteractionTransport is the complete Phase 5 Interaction wire. Production
// composition requires the authority validator and every atomic command from
// one shared FleetDB client; it cannot publish a partially migrated capability.
type InteractionTransport interface {
	InteractionAuthorityTransport
	InteractionMutationTransport
}

// interactionRequester is the complete dependency surface for Interaction's atomic
// commands and post-validation immutable AgentSession identity read.
type interactionRequester interface {
	fleetRequester
	GetAgentSession(context.Context, string, string) (*domain.AgentSession, error)
	ListAgentSessions(context.Context, string, store.AgentSessionFilter) ([]*domain.AgentSession, error)
}

type interactionStore struct {
	client interactionRequester
}

var _ InteractionAuthorityTransport = (*interactionStore)(nil)

func newInteractionTransport(client interactionRequester) InteractionTransport {
	return &interactionStore{client: client}
}

type interactionAuthorityRequestWire struct {
	SessionID    string `json:"session_id"`
	AgentID      string `json:"agent_id"`
	NodeID       string `json:"node_id"`
	LeaseID      string `json:"lease_id"`
	FencingToken int64  `json:"fencing_token"`
}

type interactionAuthorityResponseWire struct {
	Lease *domain.AgentLease `json:"lease"`
}

func (store *interactionStore) ValidateSessionAuthority(
	ctx context.Context,
	proof InteractionSessionAuthorityProof,
) (*InteractionSessionAuthorityValidation, error) {
	if store == nil || store.client == nil {
		return nil, ErrInteractionUnavailable
	}
	if err := validateInteractionSessionAuthorityProof(proof); err != nil {
		return nil, err
	}

	request := interactionAuthorityRequestWire{
		SessionID: proof.SessionID, AgentID: proof.AgentID, NodeID: proof.NodeID,
		LeaseID: proof.LeaseID, FencingToken: proof.FencingToken,
	}
	var response interactionAuthorityResponseWire
	path := "/api/v1/" + pathEscape(proof.WorkspaceKey) + "/agent-session-authority/validate"
	err := store.client.DoWithHeaders(ctx,
		http.MethodPost,
		path,
		request,
		&response,
		map[string]string{"X-Agent-Lease-Token": proof.LeaseToken},
	)
	if err != nil {
		return nil, mapInteractionAuthorityError("validate session credential", err)
	}
	if err := validateInteractionLeaseResponse(proof, response.Lease); err != nil {
		return nil, err
	}

	// TerminalID is immutable AgentSession identity but is not included in the
	// current FleetDB validation response. Read it only after the exact live
	// lease generation has been validated. No mutation is sequenced here.
	session, err := store.client.GetAgentSession(ctx, proof.WorkspaceKey, proof.SessionID)
	if err != nil {
		return nil, mapInteractionAuthorityError("read validated AgentSession identity", err)
	}
	if err := validateInteractionSessionIdentity(proof, session); err != nil {
		return nil, err
	}
	return &InteractionSessionAuthorityValidation{
		WorkspaceKey: response.Lease.WorkspaceKey,
		SessionID:    response.Lease.SessionID,
		AgentID:      response.Lease.AgentID,
		TerminalID:   session.TerminalID,
		NodeID:       response.Lease.NodeID,
		LeaseID:      response.Lease.LeaseID,
		FencingToken: response.Lease.FencingToken,
		ExpiresAt:    response.Lease.ExpiresAt,
	}, nil
}

func validateInteractionSessionAuthorityProof(proof InteractionSessionAuthorityProof) error {
	for name, value := range map[string]string{
		"workspace": proof.WorkspaceKey,
		"session":   proof.SessionID,
		"agent":     proof.AgentID,
		"node":      proof.NodeID,
		"lease":     proof.LeaseID,
		"token":     proof.LeaseToken,
	} {
		if value == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%s is required and must be canonical: %w", name, ErrInteractionInvalid)
		}
	}
	if proof.TerminalID != strings.TrimSpace(proof.TerminalID) || proof.FencingToken <= 0 {
		return fmt.Errorf("terminal identity must be canonical and fencing token must be positive: %w", ErrInteractionInvalid)
	}
	return nil
}

func validateInteractionLeaseResponse(
	proof InteractionSessionAuthorityProof,
	lease *domain.AgentLease,
) error {
	if lease == nil ||
		lease.WorkspaceKey != proof.WorkspaceKey ||
		lease.SessionID != proof.SessionID ||
		lease.AgentID != proof.AgentID ||
		lease.NodeID != proof.NodeID ||
		lease.LeaseID != proof.LeaseID ||
		lease.FencingToken != proof.FencingToken ||
		lease.Status != domain.AgentLeaseActive ||
		lease.ExpiresAt.IsZero() ||
		lease.Token != "" {
		return fmt.Errorf("FleetDB returned a mismatched or credential-bearing lease: %w", ErrInteractionInvalidPersistedState)
	}
	return nil
}

func validateInteractionSessionIdentity(
	proof InteractionSessionAuthorityProof,
	session *domain.AgentSession,
) error {
	if session == nil ||
		session.WorkspaceKey != proof.WorkspaceKey ||
		session.SessionID != proof.SessionID ||
		session.AgentID != proof.AgentID ||
		session.NodeID != proof.NodeID ||
		(proof.TerminalID != "" && session.TerminalID != proof.TerminalID) ||
		!interactionSessionStatusLive(session.Status) {
		return fmt.Errorf("FleetDB returned a mismatched or non-live AgentSession: %w", ErrInteractionInvalidPersistedState)
	}
	return nil
}

func interactionSessionStatusLive(status domain.AgentSessionStatus) bool {
	switch status {
	case domain.AgentSessionQueued, domain.AgentSessionLeased,
		domain.AgentSessionStarting, domain.AgentSessionRunning,
		domain.AgentSessionIdle, domain.AgentSessionYielded:
		return true
	default:
		return false
	}
}

func mapInteractionAuthorityError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var sentinel error
	switch {
	case errors.Is(err, domain.ErrNotFound):
		sentinel = ErrInteractionNotFound
	case errors.Is(err, domain.ErrNotOwner), errors.Is(err, domain.ErrConflict),
		errors.Is(err, domain.ErrGone):
		sentinel = ErrInteractionNotOwner
	case errors.Is(err, domain.ErrInvalidTransition):
		sentinel = ErrInteractionInvalidTransition
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrAlreadyClaimed):
		sentinel = ErrInteractionConflict
	case errors.Is(err, domain.ErrInvalid):
		sentinel = ErrInteractionInvalid
	default:
		sentinel = ErrInteractionUnavailable
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(sentinel, err))
}
