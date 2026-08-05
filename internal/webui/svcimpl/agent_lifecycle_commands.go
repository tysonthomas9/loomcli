package svcimpl

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// RequestAgentLifecycle updates FleetDB state and creates a queued command for
// the daemon poller that owns this workspace.
func (s *agentServiceImpl) RequestAgentLifecycle(ctx context.Context, wsKey, name string, in service.AgentLifecycleInput) (*service.AgentLifecycleResult, error) {
	if s.store == nil {
		return nil, service.ErrUnavailable("fleet-db store not configured")
	}
	name = normalizeStoredAgentName(name)
	if err := validateStoredAgentName(name); err != nil {
		return nil, err
	}
	if err := validateAgentCommandType(in.CommandType); err != nil {
		return nil, err
	}
	agent, err := s.store.Agents().Get(ctx, wsKey, name)
	if err != nil {
		return nil, classifyStoreError("load agent lifecycle target", err)
	}
	role, err := s.loadAgentRoleForKind(ctx, wsKey, agent.RoleName)
	if err != nil {
		return nil, err
	}
	if domain.ResolveRoleKind(role, agent.RoleName) == domain.RoleKindInteractive {
		updated, err := s.requestInteractiveAgentLifecycle(ctx, wsKey, agent, in)
		if err != nil {
			return nil, err
		}
		return &service.AgentLifecycleResult{Agent: updated, Status: domain.AgentCommandSucceeded}, nil
	}
	return s.requestSupervisedAgentLifecycle(ctx, wsKey, name, agent, in)
}

// GetAgentLifecycleCommand returns a command only when the durable command is
// scoped to the agent named in the route. Treat a scope mismatch as not found
// so command IDs cannot be used to inspect another agent's lifecycle.
func (s *agentServiceImpl) GetAgentLifecycleCommand(
	ctx context.Context,
	wsKey, name, commandID string,
) (*service.AgentLifecycleCommandResult, error) {
	if s.store == nil || s.store.AgentCommands() == nil {
		return nil, service.ErrUnavailable("fleet-db agent command store not configured")
	}
	name = normalizeStoredAgentName(name)
	if err := validateStoredAgentName(name); err != nil {
		return nil, err
	}
	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return nil, service.ErrValidation("missing lifecycle command id")
	}
	command, err := s.store.AgentCommands().Get(ctx, wsKey, commandID)
	if err != nil {
		return nil, classifyStoreError("load agent lifecycle command", err)
	}
	if command == nil || command.TargetAgentID != name {
		return nil, service.ErrNotFound("agent lifecycle command not found")
	}
	return &service.AgentLifecycleCommandResult{
		CommandID:  command.CommandID,
		Action:     command.Type,
		Status:     command.Status,
		Result:     command.Result,
		ErrorClass: command.ErrorClass,
		CreatedAt:  command.CreatedAt,
		UpdatedAt:  command.UpdatedAt,
		AckedAt:    command.AckedAt,
	}, nil
}

func (s *agentServiceImpl) requestSupervisedAgentLifecycle(
	ctx context.Context,
	wsKey string,
	name string,
	agent *domain.Agent,
	in service.AgentLifecycleInput,
) (*service.AgentLifecycleResult, error) {
	// The daemon owns supervised lifecycle completion. Publish a distinct
	// pending projection so the UI cannot claim that an offline or backlogged
	// runtime owner already completed the request.
	in = pendingSupervisedLifecycle(agent, in)
	commands := s.store.AgentCommands()
	if commands == nil {
		updated, err := s.UpdateAgent(ctx, wsKey, name, service.AgentUpdateInput{
			State:        &in.State,
			DesiredState: &in.DesiredState,
		})
		if err != nil {
			return nil, err
		}
		return &service.AgentLifecycleResult{Agent: updated, Status: domain.AgentCommandSucceeded}, nil
	}

	// Persist the executable intent before exposing a desired-state transition.
	// The daemon reconciler may act on desired=draining immediately; queue-first
	// guarantees that it can still distinguish a force Stop from a graceful
	// Stop. It also means a command-create failure leaves no lifecycle state to
	// roll back with a cancelled context or a stale pre-update snapshot.
	create := store.AgentCommandCreate{
		WorkspaceKey:  wsKey,
		CommandID:     "agent-lifecycle-" + uuid.NewString(),
		TargetAgentID: name,
		Type:          in.CommandType,
		Payload:       in.Payload,
	}
	command, err := s.createSupervisedLifecycleCommand(ctx, commands, create)
	if err != nil {
		return nil, err
	}

	// AgentCommand is the only durable pending-state record. Request-side
	// writes to Agent.State/DesiredState can race a faster or newer command and
	// overwrite the daemon's authoritative terminal projection. Return the
	// synthetic pending state to this caller; the per-agent daemon FIFO owns
	// every persistent lifecycle projection after acceptance.
	return acceptedSupervisedLifecycleResult(agent, in, command), nil
}

// createSupervisedLifecycleCommand closes the ambiguous-Create window. The
// caller chooses the command ID before issuing Create; if the transport loses
// a response, cancellation-independent retries reuse that exact ID. Create is
// idempotent for identical immutable intent, and Get observes a command that
// may already have progressed through Ack before the response was recovered.
func (s *agentServiceImpl) createSupervisedLifecycleCommand(
	ctx context.Context,
	commands store.AgentCommandStore,
	create store.AgentCommandCreate,
) (*domain.AgentCommand, error) {
	command, createErr := commands.Create(ctx, create)
	if createErr == nil {
		return command, nil
	}

	resolveCtx, cancelResolve := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancelResolve()
	for {
		retried, retryErr := commands.Create(resolveCtx, create)
		if retryErr == nil {
			return retried, nil
		}

		current, retryImmediately, recoverErr := recoverSupervisedLifecycleCommand(resolveCtx, commands, create)
		if recoverErr != nil {
			return nil, recoverErr
		}
		if current != nil {
			return current, nil
		}
		if retryImmediately {
			continue
		}
		if !waitForSupervisedLifecycleCommandRecovery(resolveCtx) {
			return nil, classifyStoreError("create agent command", createErr)
		}
	}
}

func recoverSupervisedLifecycleCommand(
	ctx context.Context,
	commands store.AgentCommandStore,
	create store.AgentCommandCreate,
) (*domain.AgentCommand, bool, error) {
	current, getErr := commands.Get(ctx, create.WorkspaceKey, create.CommandID)
	if getErr != nil {
		return nil, false, nil
	}
	if err := validateRecoveredSupervisedLifecycleCommand(current, create); err != nil {
		return nil, false, err
	}

	// Get alone is not sufficient recovery evidence: a legacy or partially
	// failed Redis write may leave a command hash that the daemon's List poller
	// cannot discover. Anchor directly before this command so backend page caps
	// cannot hide a valid high-cursor recovery row.
	listed, listErr := commands.List(ctx, create.WorkspaceKey, store.AgentCommandFilter{
		TargetAgentID: create.TargetAgentID,
		AfterCursor:   current.Cursor - 1,
		Limit:         1,
	})
	if listErr != nil {
		return nil, true, nil
	}
	if err := validateListedSupervisedLifecycleCommand(listed, create); err != nil {
		return nil, false, err
	}
	return current, false, nil
}

func validateRecoveredSupervisedLifecycleCommand(
	current *domain.AgentCommand,
	create store.AgentCommandCreate,
) error {
	if !sameAgentCommandCreate(current, create) {
		return service.ErrConflict("agent lifecycle command id resolved to different immutable intent")
	}
	if current.Cursor <= 0 {
		return service.ErrUnavailable("agent lifecycle command has invalid durable cursor")
	}
	return nil
}

func validateListedSupervisedLifecycleCommand(
	listed []*domain.AgentCommand,
	create store.AgentCommandCreate,
) error {
	for _, candidate := range listed {
		if candidate == nil || candidate.CommandID != create.CommandID {
			continue
		}
		if !sameAgentCommandCreate(candidate, create) {
			return service.ErrConflict("listed agent lifecycle command resolved to different immutable intent")
		}
		return nil
	}
	return service.ErrUnavailable("agent lifecycle command is not dispatchable")
}

func waitForSupervisedLifecycleCommandRecovery(ctx context.Context) bool {
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func sameAgentCommandCreate(command *domain.AgentCommand, create store.AgentCommandCreate) bool {
	if command == nil ||
		command.WorkspaceKey != create.WorkspaceKey ||
		command.CommandID != create.CommandID ||
		command.TargetAgentID != create.TargetAgentID ||
		(create.TargetNodeID != "" && command.TargetNodeID != create.TargetNodeID) ||
		command.SessionID != create.SessionID ||
		command.Type != create.Type ||
		len(command.Payload) != len(create.Payload) {
		return false
	}
	for key, value := range create.Payload {
		if command.Payload[key] != value {
			return false
		}
	}
	return true
}

func acceptedSupervisedLifecycleResult(
	agent *domain.Agent,
	in service.AgentLifecycleInput,
	command *domain.AgentCommand,
) *service.AgentLifecycleResult {
	synthetic := *agent
	synthetic.State = in.State
	synthetic.DesiredState = in.DesiredState
	return &service.AgentLifecycleResult{
		Agent:     &synthetic,
		Pending:   true,
		CommandID: command.CommandID,
		Status:    command.Status,
	}
}

func pendingSupervisedLifecycle(
	agent *domain.Agent,
	in service.AgentLifecycleInput,
) service.AgentLifecycleInput {
	switch in.CommandType {
	case "start":
		in.State = agent.State
		in.DesiredState = domain.AgentDesiredRunning
	case "restart":
		in.State = domain.AgentStateIdle
		in.DesiredState = domain.AgentDesiredRunning
	case "stop", "yield":
		in.State = agent.State
		in.DesiredState = domain.AgentDesiredDraining
	}
	return in
}
