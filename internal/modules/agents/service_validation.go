package agents

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func (s *Service) validateRoleBackedBehavior(ctx context.Context, workspace string, behavior BehaviorReference) error {
	if behavior.RoleName == "" {
		return nil
	}
	if s == nil || s.roles == nil {
		return ErrUnavailable
	}
	role, err := s.roles.GetRoleReference(ctx, workspace, behavior.RoleName)
	if err != nil {
		return fmt.Errorf("resolve role %q: %w", behavior.RoleName, err)
	}
	if role == nil || role.WorkspaceKey != workspace || role.RoleName != behavior.RoleName ||
		role.WorkspaceKey != strings.TrimSpace(role.WorkspaceKey) ||
		role.RoleName != strings.TrimSpace(role.RoleName) {
		return ErrInvalidPersistedState
	}
	return nil
}

func (s *Service) requireOperator(action authority.Action, workspace string, auth authority.OperatorAuthority) error {
	if s == nil || s.admission == nil {
		return authority.ErrAdmissionDenied
	}
	return s.admission.RequireOperator(action, workspace, auth)
}

func (s *Service) requireSystem(action authority.Action, workspace string, auth authority.SystemAuthority) error {
	if s == nil || s.admission == nil {
		return authority.ErrAdmissionDenied
	}
	return s.admission.RequireSystem(action, workspace, auth)
}

func (s *Service) requireSystemOwner(action authority.Action, ownership OwnershipProof, auth authority.SystemAuthority) error {
	if err := s.requireSystem(action, ownership.WorkspaceKey, auth); err != nil {
		return err
	}
	if auth.Subject() != ownership.OwnerID {
		return fmt.Errorf("system authority subject does not own agent generation: %w", ErrNotOwner)
	}
	return nil
}

func normalizeWorkspace(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("workspace is required: %w", ErrInvalid)
	}
	return value, nil
}

func requireCanonical(label, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required: %w", label, ErrInvalid)
	}
	if trimmed != value {
		return "", fmt.Errorf("%s must not contain surrounding whitespace: %w", label, ErrInvalid)
	}
	return value, nil
}

func requireAgentID(value string) (string, error) {
	value, err := requireCanonical("agent id", value)
	if err != nil {
		return "", err
	}
	if strings.Contains(value, ":") {
		return "", fmt.Errorf("agent id must not contain the reserved ':' delimiter: %w", ErrInvalid)
	}
	return value, nil
}

func normalizeMetadata(value map[string]string) (map[string]string, error) {
	if value == nil {
		return nil, nil
	}
	out := make(map[string]string, len(value))
	for key, item := range value {
		normalizedKey, err := requireCanonical("metadata key", key)
		if err != nil {
			return nil, err
		}
		if item != strings.TrimSpace(item) {
			return nil, fmt.Errorf("metadata value for %q must not contain surrounding whitespace: %w", key, ErrInvalid)
		}
		out[normalizedKey] = item
	}
	return out, nil
}

func equalStringMap(left, right map[string]string) bool {
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

func normalizeWorkspaceAndAgent(workspace, agentID string) (string, string, error) {
	workspace, err := normalizeWorkspace(workspace)
	if err != nil {
		return "", "", err
	}
	agentID, err = requireAgentID(agentID)
	if err != nil {
		return "", "", err
	}
	return workspace, agentID, nil
}

//nolint:funlen // Creation canonicalization validates every immutable identity, role, metadata, and budget field together.
func normalizeCreateCommand(command CreateAgentCommand) (CreateAgentCommand, error) {
	workspace, agentID, err := normalizeWorkspaceAndAgent(command.WorkspaceKey, command.AgentID)
	if err != nil {
		return CreateAgentCommand{}, err
	}
	name, err := requireCanonical("agent name", command.Name)
	if err != nil {
		return CreateAgentCommand{}, err
	}
	behavior, err := normalizeBehavior(command.Behavior)
	if err != nil {
		return CreateAgentCommand{}, err
	}
	if !validAgentKind(command.Kind) || !validDesiredState(command.DesiredState) {
		return CreateAgentCommand{}, ErrInvalid
	}
	if command.MaxInstances < 0 {
		return CreateAgentCommand{}, fmt.Errorf("max instances must not be negative: %w", ErrInvalid)
	}
	if command.MaxInstances == 0 {
		command.MaxInstances = 1
	}
	command.WorkspaceKey = workspace
	command.AgentID = agentID
	command.Name = name
	command.Behavior = behavior
	if command.PlacementPolicy, err = normalizeOptional("placement policy", command.PlacementPolicy); err != nil {
		return CreateAgentCommand{}, err
	}
	if command.RestartPolicy, err = normalizeOptional("restart policy", command.RestartPolicy); err != nil {
		return CreateAgentCommand{}, err
	}
	if command.BudgetPolicy, err = normalizeOptional("budget policy", command.BudgetPolicy); err != nil {
		return CreateAgentCommand{}, err
	}
	for label, value := range map[string]*string{
		"profile name": &command.ProfileName,
		"schedule id":  &command.ScheduleID,
		"lease id":     &command.LeaseID,
		"state ref":    &command.StateRef,
	} {
		if *value, err = normalizeOptional(label, *value); err != nil {
			return CreateAgentCommand{}, err
		}
	}
	if command.EventSources, err = normalizeCanonicalList("event source", command.EventSources); err != nil {
		return CreateAgentCommand{}, err
	}
	if command.TriggerRefs, err = normalizeCanonicalList("trigger ref", command.TriggerRefs); err != nil {
		return CreateAgentCommand{}, err
	}
	if command.Permissions, err = normalizeCanonicalList("permission", command.Permissions); err != nil {
		return CreateAgentCommand{}, err
	}
	if command.Metadata, err = normalizeMetadata(command.Metadata); err != nil {
		return CreateAgentCommand{}, err
	}
	return command, nil
}

func normalizeUpdateCommand(command UpdateAgentCommand) (UpdateAgentCommand, error) {
	workspace, agentID, err := normalizeWorkspaceAndAgent(command.WorkspaceKey, command.AgentID)
	if err != nil {
		return UpdateAgentCommand{}, err
	}
	if command.ExpectedUpdatedAt.IsZero() {
		return UpdateAgentCommand{}, fmt.Errorf("expected updated time is required: %w", ErrInvalid)
	}
	patch, err := normalizePatch(command.Patch)
	if err != nil {
		return UpdateAgentCommand{}, err
	}
	command.WorkspaceKey, command.AgentID, command.Patch = workspace, agentID, patch
	return command, nil
}

//nolint:cyclop,funlen,gocognit // Patch normalization explicitly canonicalizes and validates every independently optional field.
func normalizePatch(patch AgentPatch) (AgentPatch, error) {
	if patch.Name == nil && patch.Kind == nil && patch.Behavior == nil &&
		patch.ProfileName == nil && patch.ScheduleID == nil && patch.EventSources == nil &&
		patch.TriggerRefs == nil && patch.PlacementPolicy == nil && patch.MaxInstances == nil &&
		patch.LeaseID == nil && patch.RestartPolicy == nil && patch.Permissions == nil &&
		patch.BudgetPolicy == nil && patch.StateRef == nil && patch.Metadata == nil {
		return AgentPatch{}, fmt.Errorf("agent patch must change at least one field: %w", ErrInvalid)
	}
	if patch.Name != nil {
		value, valueErr := requireCanonical("agent name", *patch.Name)
		if valueErr != nil {
			return AgentPatch{}, valueErr
		}
		patch.Name = &value
	}
	if patch.Kind != nil && !validAgentKind(*patch.Kind) {
		return AgentPatch{}, ErrInvalid
	}
	if patch.Behavior != nil {
		value, valueErr := normalizeBehavior(*patch.Behavior)
		if valueErr != nil {
			return AgentPatch{}, valueErr
		}
		patch.Behavior = &value
	}
	if patch.MaxInstances != nil && *patch.MaxInstances <= 0 {
		return AgentPatch{}, fmt.Errorf("max instances must be positive: %w", ErrInvalid)
	}
	if patch.PlacementPolicy != nil {
		value, err := normalizeOptional("placement policy", *patch.PlacementPolicy)
		if err != nil {
			return AgentPatch{}, err
		}
		patch.PlacementPolicy = &value
	}
	if patch.RestartPolicy != nil {
		value, err := normalizeOptional("restart policy", *patch.RestartPolicy)
		if err != nil {
			return AgentPatch{}, err
		}
		patch.RestartPolicy = &value
	}
	if patch.BudgetPolicy != nil {
		value, err := normalizeOptional("budget policy", *patch.BudgetPolicy)
		if err != nil {
			return AgentPatch{}, err
		}
		patch.BudgetPolicy = &value
	}
	for label, value := range map[string]**string{
		"profile name": &patch.ProfileName,
		"schedule id":  &patch.ScheduleID,
		"lease id":     &patch.LeaseID,
		"state ref":    &patch.StateRef,
	} {
		if *value == nil {
			continue
		}
		normalized, normalizeErr := normalizeOptional(label, **value)
		if normalizeErr != nil {
			return AgentPatch{}, normalizeErr
		}
		*value = &normalized
	}
	for label, value := range map[string]*[]string{
		"event source": patch.EventSources,
		"trigger ref":  patch.TriggerRefs,
		"permission":   patch.Permissions,
	} {
		if value == nil {
			continue
		}
		normalized, normalizeErr := normalizeCanonicalList(label, *value)
		if normalizeErr != nil {
			return AgentPatch{}, normalizeErr
		}
		*value = normalized
	}
	if patch.Metadata != nil {
		value, err := normalizeMetadata(*patch.Metadata)
		if err != nil {
			return AgentPatch{}, err
		}
		patch.Metadata = &value
	}
	return patch, nil
}

func normalizeArchiveCommand(command ArchiveAgentCommand) (ArchiveAgentCommand, error) {
	workspace, agentID, err := normalizeWorkspaceAndAgent(command.WorkspaceKey, command.AgentID)
	if err != nil {
		return ArchiveAgentCommand{}, err
	}
	if command.ExpectedUpdatedAt.IsZero() {
		return ArchiveAgentCommand{}, fmt.Errorf("expected updated time is required: %w", ErrInvalid)
	}
	command.WorkspaceKey, command.AgentID = workspace, agentID
	return command, nil
}

func normalizeSetDesiredStateCommand(command SetDesiredStateCommand) (SetDesiredStateCommand, error) {
	workspace, agentID, err := normalizeWorkspaceAndAgent(command.WorkspaceKey, command.AgentID)
	if err != nil {
		return SetDesiredStateCommand{}, err
	}
	if !validDesiredState(command.ExpectedState) || !validDesiredState(command.DesiredState) ||
		command.ExpectedState == command.DesiredState || command.ExpectedUpdatedAt.IsZero() {
		return SetDesiredStateCommand{}, ErrInvalid
	}
	command.WorkspaceKey, command.AgentID = workspace, agentID
	return command, nil
}

func normalizeSetDesiredStateOwnedCommand(command SetDesiredStateOwnedCommand) (SetDesiredStateOwnedCommand, error) {
	if !validDesiredState(command.ExpectedState) || !validDesiredState(command.DesiredState) ||
		command.ExpectedState == command.DesiredState || command.ExpectedUpdatedAt.IsZero() {
		return SetDesiredStateOwnedCommand{}, ErrInvalid
	}
	key, err := requireCanonical("idempotency key", command.IdempotencyKey)
	if err != nil {
		return SetDesiredStateOwnedCommand{}, err
	}
	command.IdempotencyKey = key
	return command, nil
}

func normalizeBehavior(behavior BehaviorReference) (BehaviorReference, error) {
	var err error
	if behavior.RoleName != "" {
		if behavior.RoleName, err = requireCanonical("role name", behavior.RoleName); err != nil {
			return BehaviorReference{}, err
		}
	}
	if behavior.DriverID != "" {
		if behavior.DriverID, err = requireCanonical("driver id", behavior.DriverID); err != nil {
			return BehaviorReference{}, err
		}
	}
	if behavior.DriverVersionID != "" {
		if behavior.DriverVersionID, err = requireCanonical("driver version id", behavior.DriverVersionID); err != nil {
			return BehaviorReference{}, err
		}
	}
	roleBacked := behavior.RoleName != ""
	driverBacked := behavior.DriverID != "" || behavior.DriverVersionID != ""
	if roleBacked == driverBacked || driverBacked && (behavior.DriverID == "" || behavior.DriverVersionID == "") {
		return BehaviorReference{}, fmt.Errorf("behavior must contain exactly one role or complete driver version reference: %w", ErrInvalid)
	}
	return behavior, nil
}

func normalizeAgentFilter(filter AgentFilter) (AgentFilter, error) {
	if filter.Kind != "" && !validAgentKind(filter.Kind) ||
		filter.DesiredState != "" && !validDesiredState(filter.DesiredState) ||
		filter.Limit < 0 {
		return AgentFilter{}, ErrInvalid
	}
	if filter.RoleName != "" {
		value, err := requireCanonical("role name", filter.RoleName)
		if err != nil {
			return AgentFilter{}, err
		}
		filter.RoleName = value
	}
	return filter, nil
}

func normalizeAcquireOwnershipCommand(command AcquireOwnershipCommand) (AcquireOwnershipCommand, error) {
	workspace, agentID, err := normalizeWorkspaceAndAgent(command.WorkspaceKey, command.AgentID)
	if err != nil {
		return AcquireOwnershipCommand{}, err
	}
	command.WorkspaceKey, command.AgentID = workspace, agentID
	if command.LeaseID, err = requireCanonical("lease id", command.LeaseID); err != nil {
		return AcquireOwnershipCommand{}, err
	}
	if command.NodeID, err = requireCanonical("node id", command.NodeID); err != nil {
		return AcquireOwnershipCommand{}, err
	}
	if !validRuntimeProvider(command.RuntimeProvider) || command.TTL <= 0 {
		return AcquireOwnershipCommand{}, ErrInvalid
	}
	return command, nil
}

func normalizeOwnershipProof(proof OwnershipProof) (OwnershipProof, error) {
	workspace, agentID, err := normalizeWorkspaceAndAgent(proof.WorkspaceKey, proof.AgentID)
	if err != nil {
		return OwnershipProof{}, err
	}
	proof.WorkspaceKey, proof.AgentID = workspace, agentID
	if proof.LeaseID, err = requireCanonical("lease id", proof.LeaseID); err != nil {
		return OwnershipProof{}, err
	}
	if proof.OwnerID, err = requireCanonical("owner id", proof.OwnerID); err != nil {
		return OwnershipProof{}, err
	}
	if proof.NodeID, err = requireCanonical("node id", proof.NodeID); err != nil {
		return OwnershipProof{}, err
	}
	if strings.TrimSpace(proof.LeaseToken) == "" || proof.LeaseToken != strings.TrimSpace(proof.LeaseToken) ||
		proof.FencingToken <= 0 || !validRuntimeProvider(proof.RuntimeProvider) {
		return OwnershipProof{}, ErrInvalid
	}
	return proof, nil
}

func normalizeOwnershipFilter(filter OwnershipFilter) (OwnershipFilter, error) {
	var err error
	if filter.OwnerID != "" {
		if filter.OwnerID, err = requireCanonical("owner id", filter.OwnerID); err != nil {
			return OwnershipFilter{}, err
		}
	}
	if filter.NodeID != "" {
		if filter.NodeID, err = requireCanonical("node id", filter.NodeID); err != nil {
			return OwnershipFilter{}, err
		}
	}
	if filter.RuntimeProvider != "" && !validRuntimeProvider(filter.RuntimeProvider) ||
		filter.Status != "" && !validOwnershipStatus(filter.Status) ||
		filter.Limit < 0 {
		return OwnershipFilter{}, ErrInvalid
	}
	return filter, nil
}

func normalizeOptional(label, value string) (string, error) {
	if value != strings.TrimSpace(value) {
		return "", fmt.Errorf("%s must not contain surrounding whitespace: %w", label, ErrInvalid)
	}
	return value, nil
}

func validAgentKind(value AgentKind) bool {
	switch value {
	case AgentKindLead, AgentKindSupport, AgentKindTriage, AgentKindOnCall,
		AgentKindScheduled, AgentKindMaintenance, AgentKindOrchestrator,
		AgentKindAlwaysOn, AgentKindCron, AgentKindEvent,
		AgentKindCampaignOrchestrator:
		return true
	default:
		return false
	}
}

func validDesiredState(value DesiredState) bool {
	switch value {
	case DesiredRunning, DesiredStopped, DesiredPaused:
		return true
	default:
		return false
	}
}

func validRuntimeProvider(value RuntimeProvider) bool {
	switch value {
	case RuntimeProviderLocal, RuntimeProviderE2B, RuntimeProviderKubernetes,
		RuntimeProviderCI, RuntimeProviderOther:
		return true
	default:
		return false
	}
}

func validOwnershipStatus(value OwnershipStatus) bool {
	switch value {
	case OwnershipActive, OwnershipReleased, OwnershipExpired:
		return true
	default:
		return false
	}
}

//nolint:cyclop // Persisted aggregate validation checks every canonical identity and lifecycle invariant.
func validatePersistedAgent(agent *Agent, workspace, agentID string) error {
	if agent == nil || agent.WorkspaceKey == "" || agent.WorkspaceKey != strings.TrimSpace(agent.WorkspaceKey) ||
		agent.AgentID == "" || agent.AgentID != strings.TrimSpace(agent.AgentID) || strings.Contains(agent.AgentID, ":") ||
		!ValidGenerationID(agent.GenerationID) ||
		agent.Name == "" || agent.Name != strings.TrimSpace(agent.Name) ||
		!validAgentKind(agent.Kind) || !validDesiredState(agent.DesiredState) ||
		agent.MaxInstances <= 0 || agent.CreatedAt.IsZero() || agent.UpdatedAt.IsZero() ||
		agent.UpdatedAt.Before(agent.CreatedAt) {
		return ErrInvalidPersistedState
	}
	if _, err := normalizeBehavior(agent.Behavior); err != nil {
		return ErrInvalidPersistedState
	}
	if normalized, err := normalizeMetadata(agent.Metadata); err != nil ||
		!equalStringMap(agent.Metadata, normalized) {
		return ErrInvalidPersistedState
	}
	if agent.WorkspaceKey != workspace || agentID != "" && agent.AgentID != agentID {
		return ErrInvalidPersistedState
	}
	if agent.DeletedAt != nil && agent.DeletedAt.Before(agent.CreatedAt) {
		return ErrInvalidPersistedState
	}
	return nil
}

//nolint:cyclop,gocognit // Exact-result validation mirrors every optional patch field to detect divergent persistence results.
func validatePatchedAgent(agent *Agent, command UpdateAgentCommand) error {
	if agent.UpdatedAt.Before(command.ExpectedUpdatedAt) {
		return ErrInvalidPersistedState
	}
	patch := command.Patch
	if patch.Name != nil && agent.Name != *patch.Name ||
		patch.Kind != nil && agent.Kind != *patch.Kind ||
		patch.Behavior != nil && agent.Behavior != *patch.Behavior ||
		patch.ProfileName != nil && agent.ProfileName != *patch.ProfileName ||
		patch.ScheduleID != nil && agent.ScheduleID != *patch.ScheduleID ||
		patch.EventSources != nil && !slices.Equal(agent.EventSources, *patch.EventSources) ||
		patch.TriggerRefs != nil && !slices.Equal(agent.TriggerRefs, *patch.TriggerRefs) ||
		patch.PlacementPolicy != nil && agent.PlacementPolicy != *patch.PlacementPolicy ||
		patch.MaxInstances != nil && agent.MaxInstances != *patch.MaxInstances ||
		patch.LeaseID != nil && agent.LeaseID != *patch.LeaseID ||
		patch.RestartPolicy != nil && agent.RestartPolicy != *patch.RestartPolicy ||
		patch.Permissions != nil && !slices.Equal(agent.Permissions, *patch.Permissions) ||
		patch.BudgetPolicy != nil && agent.BudgetPolicy != *patch.BudgetPolicy ||
		patch.StateRef != nil && agent.StateRef != *patch.StateRef ||
		patch.Metadata != nil && !equalStringMap(agent.Metadata, *patch.Metadata) {
		return ErrInvalidPersistedState
	}
	return nil
}

func validateDesiredStateResult(agent *Agent, workspace, agentID string, state DesiredState) error {
	if err := validatePersistedAgent(agent, workspace, agentID); err != nil {
		return err
	}
	if agent.DesiredState != state {
		return ErrInvalidPersistedState
	}
	return nil
}

func validateGrant(grant *OwnershipGrant, command AcquireOwnershipCommand, ownerID string) error {
	if grant == nil || strings.TrimSpace(grant.LeaseToken) == "" ||
		grant.LeaseToken != strings.TrimSpace(grant.LeaseToken) {
		return ErrInvalidPersistedState
	}
	if err := validatePersistedLease(grant.Lease, command.WorkspaceKey, command.AgentID); err != nil {
		return err
	}
	lease := grant.Lease
	if lease.LeaseID != command.LeaseID || lease.OwnerID != ownerID ||
		lease.RuntimeProvider != command.RuntimeProvider || lease.NodeID != command.NodeID ||
		lease.Status != OwnershipActive {
		return ErrInvalidPersistedState
	}
	return nil
}

//nolint:cyclop // Lease validation keeps fencing, ownership, generation, and expiry invariants explicit.
func validatePersistedLease(lease *AgentOwnershipLease, workspace, agentID string) error {
	if lease == nil || lease.WorkspaceKey == "" || lease.WorkspaceKey != strings.TrimSpace(lease.WorkspaceKey) ||
		lease.AgentID == "" || lease.AgentID != strings.TrimSpace(lease.AgentID) ||
		lease.LeaseID == "" || lease.LeaseID != strings.TrimSpace(lease.LeaseID) ||
		lease.OwnerID == "" || lease.OwnerID != strings.TrimSpace(lease.OwnerID) ||
		lease.NodeID == "" || lease.NodeID != strings.TrimSpace(lease.NodeID) ||
		!validRuntimeProvider(lease.RuntimeProvider) || !validOwnershipStatus(lease.Status) ||
		lease.FencingToken <= 0 || lease.ExpiresAt.IsZero() || lease.CreatedAt.IsZero() ||
		lease.UpdatedAt.IsZero() || lease.UpdatedAt.Before(lease.CreatedAt) {
		return ErrInvalidPersistedState
	}
	if lease.WorkspaceKey != workspace || agentID != "" && lease.AgentID != agentID {
		return ErrInvalidPersistedState
	}
	return nil
}

func validateLeaseForProof(lease *AgentOwnershipLease, proof OwnershipProof, status OwnershipStatus) error {
	if err := validatePersistedLease(lease, proof.WorkspaceKey, proof.AgentID); err != nil {
		return err
	}
	if lease.LeaseID != proof.LeaseID || lease.OwnerID != proof.OwnerID ||
		lease.RuntimeProvider != proof.RuntimeProvider || lease.NodeID != proof.NodeID ||
		lease.FencingToken != proof.FencingToken || lease.Status != status {
		return ErrInvalidPersistedState
	}
	return nil
}
