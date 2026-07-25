package memstore

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type agentOwnershipLeaseStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.AgentOwnershipLease
	next  int64
	// commands is wired by Store.New. Acquire takes the command lock before the
	// ownership lock so checking an active lifecycle command and changing the
	// lease is atomic with command Ack/Complete.
	commands *agentCommandStore
}

func newAgentOwnershipLeaseStore() *agentOwnershipLeaseStore {
	return &agentOwnershipLeaseStore{items: make(map[string]map[string]*domain.AgentOwnershipLease)}
}

func (s *agentOwnershipLeaseStore) Acquire(_ context.Context, in store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	if err := validateAgentOwnershipLeaseAcquire(in); err != nil {
		return nil, err
	}
	if s.commands != nil {
		s.commands.mu.Lock()
		defer s.commands.mu.Unlock()
	}

	return s.acquireAgentOwnershipLeaseCommandLocked(in)
}

func validateAgentOwnershipLeaseAcquire(in store.AgentOwnershipLeaseAcquire) error {
	if in.WorkspaceKey == "" || in.AgentID == "" || in.OwnerID == "" {
		return fmt.Errorf("workspace_key + agent_id + owner_id required: %w", domain.ErrInvalid)
	}
	return nil
}

// acquireAgentOwnershipLeaseCommandLocked runs with the command lock held when
// the stores are wired together. That lock spans the command-fence check and
// the ownership lock's lease validation, publication, and generation advance.
func (s *agentOwnershipLeaseStore) acquireAgentOwnershipLeaseCommandLocked(
	in store.AgentOwnershipLeaseAcquire,
) (*domain.AgentOwnershipLease, error) {
	if err := s.validateAgentOwnershipCommandFenceLocked(in); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.AgentOwnershipLease)
	}
	now := time.Now().UTC()
	if err := s.validateExistingAgentOwnershipLeaseLocked(in, now); err != nil {
		return nil, err
	}
	lease := s.newAgentOwnershipLeaseLocked(in, now)
	s.items[in.WorkspaceKey][in.AgentID] = lease
	s.advanceActiveCommandOwnershipLocked(in, lease)
	return cloneAgentOwnershipLease(lease), nil
}

func (s *agentOwnershipLeaseStore) validateAgentOwnershipCommandFenceLocked(
	in store.AgentOwnershipLeaseAcquire,
) error {
	if s.commands == nil {
		return nil
	}
	command := s.commands.activeCommandOwnedByOtherLocked(in.WorkspaceKey, in.AgentID, in.OwnerID)
	if command == nil {
		return nil
	}
	return fmt.Errorf(
		"agent ownership lease %q is fenced by active command %q owned by %q: %w",
		in.AgentID,
		command.CommandID,
		command.AckedBy,
		domain.ErrAlreadyClaimed,
	)
}

func (s *agentOwnershipLeaseStore) validateExistingAgentOwnershipLeaseLocked(
	in store.AgentOwnershipLeaseAcquire,
	now time.Time,
) error {
	existing := s.items[in.WorkspaceKey][in.AgentID]
	if existing == nil || existing.Status != domain.AgentLeaseActive || !existing.ExpiresAt.After(now) {
		return nil
	}
	if existing.OwnerID != in.OwnerID {
		return fmt.Errorf("agent ownership lease %q in workspace %q: %w", in.AgentID, in.WorkspaceKey, domain.ErrAlreadyClaimed)
	}
	return nil
}

func (s *agentOwnershipLeaseStore) newAgentOwnershipLeaseLocked(
	in store.AgentOwnershipLeaseAcquire,
	now time.Time,
) *domain.AgentOwnershipLease {
	s.next++
	ttl := in.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	id := in.LeaseID
	if id == "" {
		id = fmt.Sprintf("agent-owner-%d", s.next)
	}
	provider := in.RuntimeProvider
	if provider == "" {
		provider = domain.RuntimeProviderLocal
	}
	token := fmt.Sprintf("ownership-token-%d", s.next)
	return &domain.AgentOwnershipLease{
		WorkspaceKey:    in.WorkspaceKey,
		AgentID:         in.AgentID,
		LeaseID:         id,
		OwnerID:         in.OwnerID,
		RuntimeProvider: provider,
		NodeID:          in.NodeID,
		Token:           token,
		FencingToken:    s.next,
		Status:          domain.AgentLeaseActive,
		ExpiresAt:       now.Add(ttl),
		LastHeartbeat:   now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func (s *agentOwnershipLeaseStore) advanceActiveCommandOwnershipLocked(
	in store.AgentOwnershipLeaseAcquire,
	lease *domain.AgentOwnershipLease,
) {
	if s.commands == nil {
		return
	}
	s.commands.advanceActiveCommandOwnershipLocked(
		in.WorkspaceKey,
		in.AgentID,
		in.OwnerID,
		lease.LeaseID,
		lease.FencingToken,
	)
}

func (s *agentOwnershipLeaseStore) Get(_ context.Context, ws, agentID string) (*domain.AgentOwnershipLease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lease, ok := s.items[ws][agentID]
	if !ok {
		return nil, fmt.Errorf("agent ownership lease %q in workspace %q: %w", agentID, ws, domain.ErrNotFound)
	}
	return cloneAgentOwnershipLease(lease), nil
}

func (s *agentOwnershipLeaseStore) List(_ context.Context, ws string, filter store.AgentOwnershipLeaseFilter) ([]*domain.AgentOwnershipLease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.AgentOwnershipLease, 0, len(s.items[ws]))
	now := time.Now().UTC()
	for _, stored := range s.items[ws] {
		lease := cloneAgentOwnershipLease(stored)
		lease.Status = effectiveAgentOwnershipLeaseStatusMem(lease, now)
		if ownershipLeaseMatchesMem(lease, filter) {
			out = append(out, lease)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *agentOwnershipLeaseStore) Heartbeat(_ context.Context, ws, agentID, token string, ttl time.Duration) (*domain.AgentOwnershipLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.items[ws][agentID]
	if !ok || lease.Token != token || lease.Status != domain.AgentLeaseActive || !lease.ExpiresAt.After(time.Now().UTC()) {
		return nil, fmt.Errorf("agent ownership lease %q in workspace %q: %w", agentID, ws, domain.ErrConflict)
	}
	now := time.Now().UTC()
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	lease.LastHeartbeat = now
	lease.ExpiresAt = now.Add(ttl)
	lease.UpdatedAt = now
	return cloneAgentOwnershipLease(lease), nil
}

func (s *agentOwnershipLeaseStore) Release(_ context.Context, ws, agentID, token string) (*domain.AgentOwnershipLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.items[ws][agentID]
	if !ok || lease.Token != token {
		return nil, fmt.Errorf("agent ownership lease %q in workspace %q: %w", agentID, ws, domain.ErrConflict)
	}
	lease.Status = domain.AgentLeaseReleased
	lease.UpdatedAt = time.Now().UTC()
	return cloneAgentOwnershipLease(lease), nil
}

func cloneAgentOwnershipLease(l *domain.AgentOwnershipLease) *domain.AgentOwnershipLease {
	out := *l
	return &out
}

func ownershipLeaseMatchesMem(l *domain.AgentOwnershipLease, f store.AgentOwnershipLeaseFilter) bool {
	return (f.OwnerID == "" || l.OwnerID == f.OwnerID) && (f.NodeID == "" || l.NodeID == f.NodeID) && (f.RuntimeProvider == "" || l.RuntimeProvider == f.RuntimeProvider) && (f.Status == "" || l.Status == f.Status)
}

func effectiveAgentOwnershipLeaseStatusMem(
	lease *domain.AgentOwnershipLease,
	now time.Time,
) domain.AgentLeaseStatus {
	if lease != nil &&
		lease.Status == domain.AgentLeaseActive &&
		!lease.ExpiresAt.After(now) {
		return domain.AgentLeaseExpired
	}
	if lease == nil {
		return ""
	}
	return lease.Status
}

type agentCommandStore struct {
	mu        sync.RWMutex
	items     map[string]map[string]*domain.AgentCommand
	next      int64
	ownership *agentOwnershipLeaseStore
}

func newAgentCommandStore(ownership *agentOwnershipLeaseStore) *agentCommandStore {
	return &agentCommandStore{
		items:     make(map[string]map[string]*domain.AgentCommand),
		ownership: ownership,
	}
}

func (s *agentCommandStore) Create(_ context.Context, in store.AgentCommandCreate) (*domain.AgentCommand, error) {
	if in.WorkspaceKey == "" || in.Type == "" {
		return nil, fmt.Errorf("workspace_key + type required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.AgentCommand)
	}
	if in.CommandID != "" {
		if existing, exists := s.items[in.WorkspaceKey][in.CommandID]; exists {
			if sameAgentCommandCreateIntent(existing, in) {
				return cloneAgentCommand(existing), nil
			}
			return nil, fmt.Errorf(
				"agent command %q in workspace %q: %w",
				in.CommandID,
				in.WorkspaceKey,
				domain.ErrAlreadyExists,
			)
		}
	}
	s.next++
	now := time.Now().UTC()
	id := in.CommandID
	if id == "" {
		id = fmt.Sprintf("cmd-%d", s.next)
	}
	cmd := &domain.AgentCommand{WorkspaceKey: in.WorkspaceKey, CommandID: id, Cursor: s.next, TargetAgentID: in.TargetAgentID, TargetNodeID: in.TargetNodeID, SessionID: in.SessionID, Type: in.Type, Payload: cloneMap(in.Payload), Status: domain.AgentCommandQueued, CreatedAt: now, UpdatedAt: now}
	s.items[in.WorkspaceKey][id] = cmd
	return cloneAgentCommand(cmd), nil
}

func (s *agentCommandStore) Get(_ context.Context, ws, commandID string) (*domain.AgentCommand, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cmd, ok := s.items[ws][commandID]
	if !ok {
		return nil, fmt.Errorf("agent command %q in workspace %q: %w", commandID, ws, domain.ErrNotFound)
	}
	return cloneAgentCommand(cmd), nil
}

func (s *agentCommandStore) List(_ context.Context, ws string, filter store.AgentCommandFilter) ([]*domain.AgentCommand, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.AgentCommand, 0, len(s.items[ws]))
	for _, cmd := range s.items[ws] {
		if commandMatchesMem(cmd, filter) {
			out = append(out, cloneAgentCommand(cmd))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cursor < out[j].Cursor })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *agentCommandStore) Ack(
	_ context.Context,
	ws,
	commandID string,
	ack store.AgentCommandAck,
) (*domain.AgentCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.items[ws][commandID]
	if !ok {
		return nil, fmt.Errorf("agent command %q in workspace %q: %w", commandID, ws, domain.ErrNotFound)
	}
	if err := s.validateAgentCommandAckLocked(ws, cmd, ack); err != nil {
		return nil, err
	}
	applyAgentCommandAck(cmd, ack, time.Now().UTC())
	return cloneAgentCommand(cmd), nil
}

func (s *agentCommandStore) validateAgentCommandAckLocked(
	ws string,
	cmd *domain.AgentCommand,
	ack store.AgentCommandAck,
) error {
	if err := validateAgentCommandAckClaim(cmd, ack); err != nil {
		return err
	}
	if err := s.validateAgentCommandAckFIFOLocked(ws, cmd); err != nil {
		return err
	}
	return s.validateAckOwnershipLocked(ws, cmd, ack)
}

func validateAgentCommandAckClaim(
	cmd *domain.AgentCommand,
	ack store.AgentCommandAck,
) error {
	if cmd.Status != domain.AgentCommandQueued {
		return fmt.Errorf(
			"agent command %q status %q cannot be acknowledged: %w",
			cmd.CommandID,
			cmd.Status,
			domain.ErrInvalidTransition,
		)
	}
	if ack.NodeID == "" {
		return fmt.Errorf("agent command %q claimant node is required: %w", cmd.CommandID, domain.ErrInvalid)
	}
	if ack.OwnerID == "" {
		return fmt.Errorf("agent command %q claimant owner is required: %w", cmd.CommandID, domain.ErrInvalid)
	}
	if cmd.TargetNodeID != "" && cmd.TargetNodeID != ack.NodeID {
		return fmt.Errorf(
			"agent command %q targets node %q, not claimant %q: %w",
			cmd.CommandID,
			cmd.TargetNodeID,
			ack.NodeID,
			domain.ErrNotOwner,
		)
	}
	if cmd.Cursor <= 0 {
		return fmt.Errorf(
			"agent command %q has invalid cursor %d: %w",
			cmd.CommandID,
			cmd.Cursor,
			domain.ErrInvalidTransition,
		)
	}
	return nil
}

func (s *agentCommandStore) validateAgentCommandAckFIFOLocked(
	ws string,
	cmd *domain.AgentCommand,
) error {
	if cmd.TargetAgentID == "" {
		return nil
	}
	for _, prior := range s.items[ws] {
		if prior.CommandID == cmd.CommandID ||
			prior.TargetAgentID != cmd.TargetAgentID ||
			!activeAgentCommandStatus(prior.Status) {
			continue
		}
		if prior.Cursor <= 0 {
			return fmt.Errorf(
				"active agent command %q has invalid cursor %d: %w",
				prior.CommandID,
				prior.Cursor,
				domain.ErrInvalidTransition,
			)
		}
		if prior.Cursor >= cmd.Cursor {
			continue
		}
		return fmt.Errorf(
			"agent command %q is behind active command %q for agent %q: %w",
			cmd.CommandID,
			prior.CommandID,
			cmd.TargetAgentID,
			domain.ErrInvalidTransition,
		)
	}
	return nil
}

func applyAgentCommandAck(
	cmd *domain.AgentCommand,
	ack store.AgentCommandAck,
	now time.Time,
) {
	cmd.Status = domain.AgentCommandAcked
	cmd.TargetNodeID = ack.NodeID
	cmd.AckedBy = ack.OwnerID
	cmd.AckedAt = &now
	cmd.OwnershipLeaseID = ack.OwnershipLeaseID
	cmd.OwnershipFencingToken = ack.OwnershipFencingToken
	cmd.UpdatedAt = now
}

func (s *agentCommandStore) Complete(_ context.Context, ws, commandID string, update store.AgentCommandComplete) (*domain.AgentCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.items[ws][commandID]
	if !ok {
		return nil, fmt.Errorf("agent command %q in workspace %q: %w", commandID, ws, domain.ErrNotFound)
	}
	if update.NodeID == "" || update.OwnerID == "" {
		return nil, fmt.Errorf("agent command %q completion claimant is required: %w", commandID, domain.ErrInvalid)
	}
	if update.Status == "" {
		update.Status = domain.AgentCommandSucceeded
	}
	if !completableAgentCommandStatus(update.Status) {
		return nil, fmt.Errorf("agent command %q completion status %q is not allowed: %w", commandID, update.Status, domain.ErrInvalidTransition)
	}
	if partialAgentCommandOwnershipProof(update.AgentCommandOwnershipProof) {
		return nil, fmt.Errorf(
			"agent command %q has partial completion ownership proof: %w",
			commandID,
			domain.ErrInvalid,
		)
	}
	if cmd.AckedBy != update.OwnerID {
		return nil, fmt.Errorf("agent command %q is owned by %q, not %q: %w", commandID, cmd.AckedBy, update.OwnerID, domain.ErrNotOwner)
	}
	if cmd.Status == update.Status && cmd.Result == update.Result && cmd.ErrorClass == update.ErrorClass {
		return cloneAgentCommand(cmd), nil
	}
	if update.Status == domain.AgentCommandRunning {
		if cmd.Status != domain.AgentCommandAcked {
			return nil, fmt.Errorf("agent command %q status %q cannot transition to running: %w", commandID, cmd.Status, domain.ErrInvalidTransition)
		}
	} else if cmd.Status != domain.AgentCommandAcked && cmd.Status != domain.AgentCommandRunning {
		return nil, fmt.Errorf("agent command %q status %q cannot be completed: %w", commandID, cmd.Status, domain.ErrInvalidTransition)
	}
	if err := s.validateCompleteOwnershipLocked(ws, cmd, update); err != nil {
		return nil, err
	}
	cmd.Status = update.Status
	if update.OwnershipLeaseID != "" {
		cmd.OwnershipLeaseID = update.OwnershipLeaseID
		cmd.OwnershipFencingToken = update.OwnershipFencingToken
	}
	cmd.Result = update.Result
	cmd.ErrorClass = update.ErrorClass
	cmd.UpdatedAt = time.Now().UTC()
	return cloneAgentCommand(cmd), nil
}

func (s *agentCommandStore) validateAckOwnershipLocked(
	ws string,
	cmd *domain.AgentCommand,
	ack store.AgentCommandAck,
) error {
	proof := ack.AgentCommandOwnershipProof
	if partialAgentCommandOwnershipProof(proof) {
		return fmt.Errorf("agent command %q has partial ownership proof: %w", cmd.CommandID, domain.ErrInvalid)
	}
	if cmd.TargetAgentID == "" {
		return nil
	}
	lease := s.currentOwnershipLeaseLocked(ws, cmd.TargetAgentID)
	if emptyAgentCommandOwnershipProof(proof) {
		if knownAgentLifecycleCommandType(cmd.Type) && !liveAgentOwnershipLease(lease) {
			return nil
		}
		return fmt.Errorf(
			"agent command %q proofless acknowledgement requires a known lifecycle command with no live owner: %w",
			cmd.CommandID,
			domain.ErrNotOwner,
		)
	}
	return validateCurrentAgentCommandOwnership(
		cmd,
		ack.NodeID,
		ack.OwnerID,
		proof,
		lease,
		false,
	)
}

func (s *agentCommandStore) validateCompleteOwnershipLocked(
	ws string,
	cmd *domain.AgentCommand,
	update store.AgentCommandComplete,
) error {
	proof := update.AgentCommandOwnershipProof
	if cmd.TargetAgentID == "" {
		return nil
	}
	lease := s.currentOwnershipLeaseLocked(ws, cmd.TargetAgentID)
	if emptyAgentCommandOwnershipProof(proof) {
		if cmd.OwnershipFencingToken == 0 &&
			knownAgentLifecycleCommandType(cmd.Type) &&
			!liveAgentOwnershipLease(lease) &&
			prooflessAbsentLifecycleTerminal(cmd.Type, update.Status) {
			return nil
		}
		return fmt.Errorf(
			"agent command %q proofless completion is not authorized by agent absence: %w",
			cmd.CommandID,
			domain.ErrNotOwner,
		)
	}
	if cmd.OwnershipFencingToken > 0 && proof.OwnershipFencingToken < cmd.OwnershipFencingToken {
		return fmt.Errorf(
			"agent command %q completion fence %d is older than acknowledged fence %d: %w",
			cmd.CommandID,
			proof.OwnershipFencingToken,
			cmd.OwnershipFencingToken,
			domain.ErrConflict,
		)
	}
	allowReleased := cmd.Type == "stop" &&
		proof.OwnershipLeaseID == cmd.OwnershipLeaseID &&
		proof.OwnershipFencingToken == cmd.OwnershipFencingToken
	return validateCurrentAgentCommandOwnership(
		cmd,
		update.NodeID,
		update.OwnerID,
		proof,
		lease,
		allowReleased,
	)
}

func (s *agentCommandStore) currentOwnershipLeaseLocked(
	ws,
	agentID string,
) *domain.AgentOwnershipLease {
	if s.ownership == nil {
		return nil
	}
	s.ownership.mu.RLock()
	defer s.ownership.mu.RUnlock()
	lease := s.ownership.items[ws][agentID]
	if lease == nil {
		return nil
	}
	return cloneAgentOwnershipLease(lease)
}

func validateCurrentAgentCommandOwnership(
	cmd *domain.AgentCommand,
	nodeID,
	ownerID string,
	proof store.AgentCommandOwnershipProof,
	lease *domain.AgentOwnershipLease,
	allowReleased bool,
) error {
	if lease == nil {
		return fmt.Errorf("agent command %q ownership lease is missing: %w", cmd.CommandID, domain.ErrNotOwner)
	}
	statusAllowed := liveAgentOwnershipLease(lease)
	if allowReleased && lease.Status == domain.AgentLeaseReleased {
		statusAllowed = true
	}
	if !statusAllowed ||
		lease.AgentID != cmd.TargetAgentID ||
		lease.LeaseID != proof.OwnershipLeaseID ||
		lease.OwnerID != ownerID ||
		lease.NodeID != nodeID ||
		lease.Token != proof.OwnershipToken ||
		lease.FencingToken != proof.OwnershipFencingToken {
		return fmt.Errorf("agent command %q ownership proof does not match the current lease generation: %w", cmd.CommandID, domain.ErrConflict)
	}
	return nil
}

func emptyAgentCommandOwnershipProof(proof store.AgentCommandOwnershipProof) bool {
	return proof.OwnershipLeaseID == "" &&
		proof.OwnershipToken == "" &&
		proof.OwnershipFencingToken == 0
}

func completeAgentCommandOwnershipProof(proof store.AgentCommandOwnershipProof) bool {
	return proof.OwnershipLeaseID != "" &&
		proof.OwnershipToken != "" &&
		proof.OwnershipFencingToken > 0
}

func partialAgentCommandOwnershipProof(proof store.AgentCommandOwnershipProof) bool {
	return !emptyAgentCommandOwnershipProof(proof) &&
		!completeAgentCommandOwnershipProof(proof)
}

func liveAgentOwnershipLease(lease *domain.AgentOwnershipLease) bool {
	return lease != nil &&
		lease.Status == domain.AgentLeaseActive &&
		lease.ExpiresAt.After(time.Now().UTC())
}

func (s *agentCommandStore) activeCommandOwnedByOtherLocked(
	ws,
	agentID,
	ownerID string,
) *domain.AgentCommand {
	for _, command := range s.items[ws] {
		if command.TargetAgentID == agentID &&
			(command.Status == domain.AgentCommandAcked || command.Status == domain.AgentCommandRunning) &&
			command.AckedBy != "" &&
			command.AckedBy != ownerID {
			return command
		}
	}
	return nil
}

func (s *agentCommandStore) advanceActiveCommandOwnershipLocked(
	ws,
	agentID,
	ownerID,
	leaseID string,
	fencingToken int64,
) {
	for _, command := range s.items[ws] {
		if command.TargetAgentID != agentID ||
			command.AckedBy != ownerID ||
			(command.Status != domain.AgentCommandAcked && command.Status != domain.AgentCommandRunning) {
			continue
		}
		command.OwnershipLeaseID = leaseID
		command.OwnershipFencingToken = fencingToken
	}
}

func knownAgentLifecycleCommandType(commandType string) bool {
	switch commandType {
	case "start", "stop", "restart", "yield":
		return true
	default:
		return false
	}
}

func prooflessAbsentLifecycleTerminal(
	commandType string,
	status domain.AgentCommandStatus,
) bool {
	if status == domain.AgentCommandFailed || status == domain.AgentCommandCancelled {
		return true
	}
	return commandType == "stop" && status == domain.AgentCommandSucceeded
}

func sameAgentCommandCreateIntent(existing *domain.AgentCommand, in store.AgentCommandCreate) bool {
	return existing != nil &&
		existing.WorkspaceKey == in.WorkspaceKey &&
		existing.CommandID == in.CommandID &&
		existing.TargetAgentID == in.TargetAgentID &&
		(in.TargetNodeID == "" || existing.TargetNodeID == in.TargetNodeID) &&
		existing.SessionID == in.SessionID &&
		existing.Type == in.Type &&
		maps.Equal(existing.Payload, in.Payload)
}

func completableAgentCommandStatus(status domain.AgentCommandStatus) bool {
	switch status {
	case domain.AgentCommandRunning, domain.AgentCommandSucceeded, domain.AgentCommandFailed, domain.AgentCommandCancelled:
		return true
	default:
		return false
	}
}

func activeAgentCommandStatus(status domain.AgentCommandStatus) bool {
	switch status {
	case domain.AgentCommandQueued, domain.AgentCommandAcked, domain.AgentCommandRunning:
		return true
	default:
		return false
	}
}

func cloneAgentCommand(c *domain.AgentCommand) *domain.AgentCommand {
	out := *c
	out.AckedAt = clonePtr(c.AckedAt)
	out.Payload = cloneMap(c.Payload)
	return &out
}

func commandMatchesMem(c *domain.AgentCommand, f store.AgentCommandFilter) bool {
	return (f.TargetAgentID == "" || c.TargetAgentID == f.TargetAgentID) && (f.TargetNodeID == "" || c.TargetNodeID == f.TargetNodeID) && (f.Status == "" || c.Status == f.Status) && (f.AfterCursor <= 0 || c.Cursor > f.AfterCursor)
}
