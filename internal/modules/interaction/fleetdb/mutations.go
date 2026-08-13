package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// MutationTransport is the exact missing FleetDB contract for Interaction.
// Each owned method represents one service-only durable command that validates
// the complete SessionOwner generation in the same transaction as the named
// mutation. Implementations must not decompose any method into generic
// session, lease, terminal, or inbox requests.
type MutationTransport interface {
	StartSession(context.Context, interaction.StartSessionCommand) (interaction.SessionStart, error)
	RecoverSessionStart(
		context.Context,
		interaction.RecoverSessionStartCommand,
	) (interaction.SessionStart, error)
	GetSession(context.Context, string, string) (*interaction.AgentSession, error)
	ListSessions(context.Context, interaction.SessionArchiveQuery) ([]*interaction.AgentSession, error)
	PatchSessionOwned(
		context.Context,
		string,
		authority.SessionOwner,
		interaction.SessionPatch,
	) (*interaction.AgentSession, *interaction.SessionLease, error)
	HeartbeatSessionOwned(
		context.Context,
		string,
		authority.SessionOwner,
		interaction.SessionHeartbeat,
	) (*interaction.AgentSession, *interaction.SessionLease, error)
	FinishSessionOwned(
		context.Context,
		string,
		authority.SessionOwner,
		interaction.SessionFinish,
	) (interaction.SessionFinishResult, error)
	ForceInterrupt(
		context.Context,
		interaction.ForceInterruptCommand,
	) (interaction.ForceInterruptResult, error)
	InterruptSessionIfLeaseMissing(
		context.Context,
		string,
		string,
		time.Time,
	) (*interaction.AgentSession, bool, error)
	ListRecoverableSessions(context.Context, string, time.Time) ([]*interaction.AgentSession, error)

	CreateTerminalOwned(
		context.Context,
		authority.SessionOwner,
		interaction.OpenTerminalCommand,
	) (*interaction.TerminalSession, error)
	GetTerminal(context.Context, string, string) (*interaction.TerminalSession, error)
	UpdateTerminalOwned(
		context.Context,
		authority.SessionOwner,
		string,
		string,
		interaction.TerminalUpdate,
	) (*interaction.TerminalSession, error)

	EnqueueInbox(context.Context, interaction.EnqueueInboxCommand) (*interaction.InboxMessage, error)
	ClaimInboxOwned(
		context.Context,
		authority.SessionOwner,
		interaction.ClaimInboxCommand,
	) (*interaction.InboxMessage, error)
	CompleteInboxOwned(
		context.Context,
		authority.SessionOwner,
		interaction.CompleteInboxCommand,
	) (*interaction.InboxMessage, error)

	ListActivity(context.Context, string, string, int) ([]interaction.Activity, error)
}

// Adapter is the complete capability-local FleetDB adapter. Construction
// requires both the credential validator and every compound mutation command;
// production cannot accidentally publish a partial Interaction capability.
type Adapter struct {
	*AuthorityAdapter
	mutations  MutationTransport
	newLeaseID func() (string, error)
}

var (
	_ interaction.SessionStore   = (*Adapter)(nil)
	_ interaction.InboxStore     = (*Adapter)(nil)
	_ interaction.ActivitySource = (*Adapter)(nil)
)

func New(
	authorityTransport AuthorityTransport,
	mutationTransport MutationTransport,
) (*Adapter, error) {
	authorityAdapter, err := NewAuthorityAdapter(authorityTransport)
	if err != nil {
		return nil, err
	}
	if mutationTransport == nil {
		return nil, fmt.Errorf(
			"interaction fleetdb adapter: compound mutation transport is required: %w",
			interaction.ErrUnavailable,
		)
	}
	return &Adapter{
		AuthorityAdapter: authorityAdapter,
		mutations:        mutationTransport,
		newLeaseID:       interaction.NewUUID,
	}, nil
}

func (adapter *Adapter) Start(
	ctx context.Context,
	command interaction.StartSessionCommand,
) (interaction.SessionStart, error) {
	value, err := adapter.mutations.StartSession(ctx, command)
	if err == nil {
		return value, nil
	}
	if !ambiguousStartTransportError(err) {
		return interaction.SessionStart{}, mapError("atomically start AgentSession", err)
	}
	return adapter.recoverAmbiguousStart(ctx, command, err)
}

func (adapter *Adapter) RecoverStart(
	ctx context.Context,
	command interaction.RecoverSessionStartCommand,
) (interaction.SessionStart, error) {
	value, err := adapter.mutations.RecoverSessionStart(ctx, command)
	return value, mapError("recover AgentSession start", err)
}

func (adapter *Adapter) Get(
	ctx context.Context,
	workspace,
	sessionID string,
) (*interaction.AgentSession, error) {
	value, err := adapter.mutations.GetSession(ctx, workspace, sessionID)
	return value, mapError("get AgentSession", err)
}

func (adapter *Adapter) List(
	ctx context.Context,
	query interaction.SessionArchiveQuery,
) ([]*interaction.AgentSession, error) {
	values, err := adapter.mutations.ListSessions(ctx, query)
	return values, mapError("list AgentSessions", err)
}

func (adapter *Adapter) HeartbeatOwned(
	ctx context.Context,
	workspace string,
	owner authority.SessionOwner,
	heartbeat interaction.SessionHeartbeat,
) (*interaction.AgentSession, *interaction.SessionLease, error) {
	session, lease, err := adapter.mutations.HeartbeatSessionOwned(ctx, workspace, owner, heartbeat)
	return session, lease, mapError("heartbeat owned AgentSession", err)
}

func (adapter *Adapter) PatchOwned(
	ctx context.Context,
	workspace string,
	owner authority.SessionOwner,
	patch interaction.SessionPatch,
) (*interaction.AgentSession, *interaction.SessionLease, error) {
	session, lease, err := adapter.mutations.PatchSessionOwned(ctx, workspace, owner, patch)
	return session, lease, mapError("patch owned AgentSession", err)
}

func (adapter *Adapter) FinishOwned(
	ctx context.Context,
	workspace string,
	owner authority.SessionOwner,
	finish interaction.SessionFinish,
) (interaction.SessionFinishResult, error) {
	result, err := adapter.mutations.FinishSessionOwned(ctx, workspace, owner, finish)
	return result, mapError("finish owned AgentSession", err)
}

func (adapter *Adapter) ForceInterrupt(
	ctx context.Context,
	command interaction.ForceInterruptCommand,
) (interaction.ForceInterruptResult, error) {
	result, err := adapter.mutations.ForceInterrupt(ctx, command)
	return result, mapError("force interrupt AgentSession lifecycle", err)
}

func (adapter *Adapter) InterruptIfLeaseMissing(
	ctx context.Context,
	workspace,
	sessionID string,
	now time.Time,
) (*interaction.AgentSession, bool, error) {
	session, changed, err := adapter.mutations.InterruptSessionIfLeaseMissing(ctx, workspace, sessionID, now)
	return session, changed, mapError("interrupt unowned AgentSession", err)
}

func (adapter *Adapter) ListRecoverable(
	ctx context.Context,
	workspace string,
	now time.Time,
) ([]*interaction.AgentSession, error) {
	values, err := adapter.mutations.ListRecoverableSessions(ctx, workspace, now)
	return values, mapError("list recoverable AgentSessions", err)
}

const interactionStartRecoveryLimit = 3

//nolint:cyclop,funlen,gocognit // Ambiguous-start recovery exhaustively distinguishes replay, ownership loss, retry, and terminal outcomes.
func (adapter *Adapter) recoverAmbiguousStart(
	ctx context.Context,
	command interaction.StartSessionCommand,
	startErr error,
) (interaction.SessionStart, error) {
	session, err := adapter.mutations.GetSession(
		ctx,
		command.WorkspaceKey,
		command.SessionID,
	)
	if errors.Is(err, ErrTransportNotFound) {
		retried, retryErr := adapter.mutations.StartSession(ctx, command)
		if retryErr == nil {
			return retried, nil
		}
		if !ambiguousStartTransportError(retryErr) {
			return interaction.SessionStart{}, mapError(
				"retry absent ambiguous AgentSession start",
				retryErr,
			)
		}
		startErr = errors.Join(startErr, retryErr)
		session, err = adapter.mutations.GetSession(
			ctx,
			command.WorkspaceKey,
			command.SessionID,
		)
	}
	if err != nil {
		return interaction.SessionStart{}, mapError(
			"inspect ambiguous AgentSession start",
			errors.Join(startErr, err),
		)
	}
	if !exactStartingDefinition(session, command) {
		return interaction.SessionStart{}, fmt.Errorf(
			"inspect ambiguous AgentSession start: durable session does not match exact starting definition: %w",
			interaction.ErrInvalidPersistedState,
		)
	}
	if session.CurrentLeaseID == "" || session.CurrentLeaseFencingToken <= 0 {
		return interaction.SessionStart{}, fmt.Errorf(
			"inspect ambiguous AgentSession start: missing current lease generation: %w",
			interaction.ErrInvalidPersistedState,
		)
	}
	if session.CurrentLeaseID != command.LeaseID {
		return interaction.SessionStart{}, fmt.Errorf(
			"inspect ambiguous AgentSession start: original lease generation was already replaced: %w",
			interaction.ErrConflict,
		)
	}

	expectedLeaseID := session.CurrentLeaseID
	expectedFence := session.CurrentLeaseFencingToken
	var recoveryErr error
	for attempt := 0; attempt < interactionStartRecoveryLimit; attempt++ {
		replacementLeaseID, generationErr := adapter.newLeaseID()
		if generationErr != nil {
			return interaction.SessionStart{}, fmt.Errorf(
				"generate replacement Interaction lease ID: %w",
				errors.Join(interaction.ErrUnavailable, generationErr),
			)
		}
		if replacementLeaseID == "" || replacementLeaseID == expectedLeaseID {
			return interaction.SessionStart{}, fmt.Errorf(
				"generate replacement Interaction lease ID: %w",
				interaction.ErrUnavailable,
			)
		}
		recovered, err := adapter.mutations.RecoverSessionStart(
			ctx,
			interaction.RecoverSessionStartCommand{
				Original:                  command,
				ExpectedLeaseID:           expectedLeaseID,
				ExpectedLeaseFencingToken: expectedFence,
				ReplacementLeaseID:        replacementLeaseID,
				ReplacementLeaseTTL:       command.LeaseTTL,
			},
		)
		if err == nil {
			return recovered, nil
		}
		if !ambiguousStartTransportError(err) {
			return interaction.SessionStart{}, mapError(
				"recover ambiguous AgentSession start",
				err,
			)
		}
		recoveryErr = errors.Join(recoveryErr, err)
		current, inspectErr := adapter.mutations.GetSession(
			ctx,
			command.WorkspaceKey,
			command.SessionID,
		)
		if inspectErr != nil {
			return interaction.SessionStart{}, mapError(
				"inspect lost AgentSession start recovery response",
				errors.Join(recoveryErr, inspectErr),
			)
		}
		if !exactStartingDefinition(current, command) {
			return interaction.SessionStart{}, fmt.Errorf(
				"inspect lost AgentSession start recovery response: durable session changed: %w",
				interaction.ErrInvalidPersistedState,
			)
		}
		switch current.CurrentLeaseID {
		case replacementLeaseID:
			if current.CurrentLeaseFencingToken <= expectedFence {
				return interaction.SessionStart{}, fmt.Errorf(
					"inspect lost AgentSession start recovery response: non-increasing fence: %w",
					interaction.ErrInvalidPersistedState,
				)
			}
			expectedLeaseID = current.CurrentLeaseID
			expectedFence = current.CurrentLeaseFencingToken
		case expectedLeaseID:
			if current.CurrentLeaseFencingToken != expectedFence {
				return interaction.SessionStart{}, fmt.Errorf(
					"inspect lost AgentSession start recovery response: mismatched expected fence: %w",
					interaction.ErrInvalidPersistedState,
				)
			}
		default:
			return interaction.SessionStart{}, fmt.Errorf(
				"inspect lost AgentSession start recovery response: concurrent generation won: %w",
				interaction.ErrConflict,
			)
		}
	}
	return interaction.SessionStart{}, fmt.Errorf(
		"recover ambiguous AgentSession start exhausted %d rotations: %w",
		interactionStartRecoveryLimit,
		errors.Join(interaction.ErrUnavailable, startErr, recoveryErr),
	)
}

func ambiguousStartTransportError(err error) bool {
	// A malformed 2xx response is authoritative evidence of a broken
	// contract, not evidence that the response was lost. Rotating on
	// ErrTransportInvalidPersistedState could hide corruption and invalidate
	// an otherwise delivered credential, so only genuine transport
	// unavailability enters recovery.
	return !errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) &&
		errors.Is(err, ErrTransportUnavailable)
}

func exactStartingDefinition(
	session *interaction.AgentSession,
	command interaction.StartSessionCommand,
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
		session.Status == interaction.SessionStarting &&
		session.Phase == command.Phase &&
		session.Attempt == command.Attempt &&
		interactionMetadataEqual(session.Metadata, command.Metadata)
}

func interactionMetadataEqual(left, right map[string]string) bool {
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

func (adapter *Adapter) Create(
	ctx context.Context,
	owner authority.SessionOwner,
	command interaction.OpenTerminalCommand,
) (*interaction.TerminalSession, error) {
	if adapter == nil || adapter.mutations == nil {
		return nil, interaction.ErrUnavailable
	}
	value, err := adapter.mutations.CreateTerminalOwned(ctx, owner, command)
	return value, mapError("create owned TerminalSession", err)
}

func (adapter *Adapter) GetTerminal(
	ctx context.Context,
	workspace,
	terminalID string,
) (*interaction.TerminalSession, error) {
	if adapter == nil || adapter.mutations == nil {
		return nil, interaction.ErrUnavailable
	}
	value, err := adapter.mutations.GetTerminal(ctx, workspace, terminalID)
	return value, mapError("get TerminalSession", err)
}

func (adapter *Adapter) Update(
	ctx context.Context,
	owner authority.SessionOwner,
	workspace,
	terminalID string,
	update interaction.TerminalUpdate,
) (*interaction.TerminalSession, error) {
	if adapter == nil || adapter.mutations == nil {
		return nil, interaction.ErrUnavailable
	}
	value, err := adapter.mutations.UpdateTerminalOwned(ctx, owner, workspace, terminalID, update)
	return value, mapError("update owned TerminalSession", err)
}

// TerminalAdapter resolves the unavoidable Get method collision between
// SessionStore and TerminalStore while sharing the same complete transport.
type TerminalAdapter struct {
	adapter *Adapter
}

var _ interaction.TerminalStore = (*TerminalAdapter)(nil)

func (adapter *Adapter) Terminals() interaction.TerminalStore {
	if adapter == nil {
		return nil
	}
	return &TerminalAdapter{adapter: adapter}
}

func (adapter *TerminalAdapter) Create(
	ctx context.Context,
	owner authority.SessionOwner,
	command interaction.OpenTerminalCommand,
) (*interaction.TerminalSession, error) {
	if adapter == nil || adapter.adapter == nil {
		return nil, interaction.ErrUnavailable
	}
	return adapter.adapter.Create(ctx, owner, command)
}

func (adapter *TerminalAdapter) Get(
	ctx context.Context,
	workspace,
	terminalID string,
) (*interaction.TerminalSession, error) {
	if adapter == nil || adapter.adapter == nil {
		return nil, interaction.ErrUnavailable
	}
	return adapter.adapter.GetTerminal(ctx, workspace, terminalID)
}

func (adapter *TerminalAdapter) Update(
	ctx context.Context,
	owner authority.SessionOwner,
	workspace,
	terminalID string,
	update interaction.TerminalUpdate,
) (*interaction.TerminalSession, error) {
	if adapter == nil || adapter.adapter == nil {
		return nil, interaction.ErrUnavailable
	}
	return adapter.adapter.Update(ctx, owner, workspace, terminalID, update)
}

func (adapter *Adapter) Enqueue(
	ctx context.Context,
	command interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	value, err := adapter.mutations.EnqueueInbox(ctx, command)
	return value, mapError("enqueue inbox", err)
}

func (adapter *Adapter) ClaimNext(
	ctx context.Context,
	owner authority.SessionOwner,
	command interaction.ClaimInboxCommand,
) (*interaction.InboxMessage, error) {
	value, err := adapter.mutations.ClaimInboxOwned(ctx, owner, command)
	return value, mapError("claim owned inbox", err)
}

func (adapter *Adapter) Complete(
	ctx context.Context,
	owner authority.SessionOwner,
	command interaction.CompleteInboxCommand,
) (*interaction.InboxMessage, error) {
	value, err := adapter.mutations.CompleteInboxOwned(ctx, owner, command)
	return value, mapError("complete owned inbox", err)
}

func (adapter *Adapter) ListActivity(
	ctx context.Context,
	workspace,
	agentID string,
	limit int,
) ([]interaction.Activity, error) {
	values, err := adapter.mutations.ListActivity(ctx, workspace, agentID, limit)
	return values, mapError("list combined activity", err)
}
