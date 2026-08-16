package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type ManagedReviewerDesiredState string

const (
	ManagedReviewerActive   ManagedReviewerDesiredState = "active"
	ManagedReviewerArchived ManagedReviewerDesiredState = "archived"
)

// ManagedReviewerRoleDefinition intentionally matches FleetDB's canonical
// fingerprint field order. It is distinct from generic RoleDefinition because
// this complete value belongs to one versioned PR Review preset.
type ManagedReviewerRoleDefinition struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Kind           string   `json:"kind,omitempty"`
	PromptFile     string   `json:"prompt_file,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
	Model          string   `json:"model,omitempty"`
	TaskFilter     string   `json:"task_filter,omitempty"`
	Backend        string   `json:"backend,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	PathPatterns   []string `json:"path_patterns,omitempty"`
	Skills         []string `json:"skills,omitempty"`
	MaxPriority    *int     `json:"max_priority,omitempty"`
	MaxConcurrency *int     `json:"max_concurrency,omitempty"`
	ReadOnly       bool     `json:"read_only,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`
	MaxBudgetUSD   *float64 `json:"max_budget_usd,omitempty"`
}

type ManagedReviewerAgentDefinition struct {
	Kind         AgentKind         `json:"kind"`
	DesiredState DesiredState      `json:"desired_state"`
	RoleName     string            `json:"role_name"`
	MaxInstances int               `json:"max_instances"`
	BudgetPolicy string            `json:"budget_policy,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type ManagedReviewerPreset struct {
	PresetID string                         `json:"preset_id"`
	Revision int64                          `json:"revision"`
	Role     ManagedReviewerRoleDefinition  `json:"role"`
	Agent    ManagedReviewerAgentDefinition `json:"agent"`
}

type ManagedReviewerCommand struct {
	WorkspaceKey string                      `json:"workspace_key"`
	AgentID      string                      `json:"agent_id"`
	DesiredState ManagedReviewerDesiredState `json:"desired_state"`
	Preset       ManagedReviewerPreset       `json:"preset"`
}

// ManagedReviewerMutation is the fully normalized atomic persistence intent.
// Fingerprint and ActorID are derived inside Agents and cannot be supplied by
// PR Review or an HTTP request.
type ManagedReviewerMutation struct {
	WorkspaceKey string
	AgentID      string
	DesiredState ManagedReviewerDesiredState
	Preset       ManagedReviewerPreset
	Fingerprint  string
	ActorID      string
}

type ManagedReviewerResult struct {
	PresetID          string `json:"preset_id"`
	PresetRevision    int64  `json:"preset_revision"`
	PresetFingerprint string `json:"preset_fingerprint"`
	Role              *Role  `json:"role"`
	Agent             *Agent `json:"agent"`
	Changed           bool   `json:"changed"`
}

func (s *Service) ConvergeManagedReviewer(
	ctx context.Context,
	auth authority.SystemAuthority,
	command ManagedReviewerCommand,
) (*ManagedReviewerResult, error) {
	command, fingerprint, err := normalizeManagedReviewerCommand(command)
	if err != nil {
		return nil, err
	}
	if err := s.requireSystem(ActionConvergeManagedReviewer, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s == nil || s.reviewers == nil {
		return nil, ErrUnavailable
	}

	result, err := s.reviewers.ConvergeManagedReviewer(ctx, ManagedReviewerMutation{
		WorkspaceKey: command.WorkspaceKey,
		AgentID:      command.AgentID,
		DesiredState: command.DesiredState,
		Preset:       cloneManagedReviewerPreset(command.Preset),
		Fingerprint:  fingerprint,
		ActorID:      auth.Subject(),
	})
	if err != nil {
		return nil, fmt.Errorf("converge managed reviewer %q: %w", command.AgentID, err)
	}
	if err := validateManagedReviewerResult(result, command, fingerprint); err != nil {
		return nil, err
	}
	return cloneManagedReviewerResult(result), nil
}

func normalizeManagedReviewerCommand(
	command ManagedReviewerCommand,
) (ManagedReviewerCommand, string, error) {
	workspace, agentID, err := normalizeWorkspaceAndAgent(command.WorkspaceKey, command.AgentID)
	if err != nil {
		return ManagedReviewerCommand{}, "", err
	}
	if command.DesiredState != ManagedReviewerActive && command.DesiredState != ManagedReviewerArchived {
		return ManagedReviewerCommand{}, "", fmt.Errorf("managed reviewer desired state is invalid: %w", ErrInvalid)
	}
	presetID, err := requireCanonical("managed reviewer preset id", command.Preset.PresetID)
	if err != nil || len(presetID) > 100 {
		return ManagedReviewerCommand{}, "", fmt.Errorf("managed reviewer preset id is invalid: %w", ErrInvalid)
	}
	if command.Preset.Revision <= 0 {
		return ManagedReviewerCommand{}, "", fmt.Errorf("managed reviewer preset revision must be positive: %w", ErrInvalid)
	}
	role, err := normalizeManagedReviewerRole(command.Preset.Role)
	if err != nil {
		return ManagedReviewerCommand{}, "", err
	}
	agent, err := normalizeManagedReviewerAgent(workspace, agentID, command.Preset.Agent)
	if err != nil {
		return ManagedReviewerCommand{}, "", err
	}
	if agent.RoleName != role.Name {
		return ManagedReviewerCommand{}, "", fmt.Errorf("managed reviewer preset must reference its shared role: %w", ErrInvalid)
	}
	command.WorkspaceKey = workspace
	command.AgentID = agentID
	command.Preset = ManagedReviewerPreset{
		PresetID: presetID,
		Revision: command.Preset.Revision,
		Role:     role,
		Agent:    agent,
	}
	fingerprint, err := managedReviewerPresetFingerprint(command.Preset)
	if err != nil {
		return ManagedReviewerCommand{}, "", err
	}
	return command, fingerprint, nil
}

func normalizeManagedReviewerRole(
	value ManagedReviewerRoleDefinition,
) (ManagedReviewerRoleDefinition, error) {
	normalized, err := normalizeRoleDefinition(RoleDefinition{
		Name: value.Name, Kind: value.Kind, Description: value.Description,
		Prompt: value.Prompt, PromptFile: value.PromptFile, Model: value.Model,
		TaskFilter: value.TaskFilter, Backend: value.Backend, Effort: value.Effort,
		PathPatterns: value.PathPatterns, Skills: value.Skills,
		MaxPriority: value.MaxPriority, MaxConcurrency: value.MaxConcurrency,
		ReadOnly: value.ReadOnly, AllowedTools: value.AllowedTools,
		DeniedTools: value.DeniedTools, MaxBudgetUSD: value.MaxBudgetUSD,
	})
	if err != nil {
		return ManagedReviewerRoleDefinition{}, err
	}
	if normalized.Kind != RoleKindInteractive && normalized.Kind != RoleKindWorker {
		return ManagedReviewerRoleDefinition{}, fmt.Errorf("managed reviewer role kind is invalid: %w", ErrInvalid)
	}
	return ManagedReviewerRoleDefinition{
		Name: normalized.Name, Description: normalized.Description, Kind: normalized.Kind,
		PromptFile: normalized.PromptFile, Prompt: normalized.Prompt, Model: normalized.Model,
		TaskFilter: normalized.TaskFilter, Backend: normalized.Backend, Effort: normalized.Effort,
		PathPatterns: normalized.PathPatterns, Skills: normalized.Skills,
		MaxPriority: normalized.MaxPriority, MaxConcurrency: normalized.MaxConcurrency,
		ReadOnly: normalized.ReadOnly, AllowedTools: normalized.AllowedTools,
		DeniedTools: normalized.DeniedTools, MaxBudgetUSD: normalized.MaxBudgetUSD,
	}, nil
}

func normalizeManagedReviewerAgent(
	workspace, agentID string,
	value ManagedReviewerAgentDefinition,
) (ManagedReviewerAgentDefinition, error) {
	normalized, err := normalizeCreateCommand(CreateAgentCommand{
		WorkspaceKey: workspace, AgentID: agentID, Name: agentID,
		Kind: value.Kind, DesiredState: value.DesiredState,
		Behavior:     BehaviorReference{RoleName: value.RoleName},
		MaxInstances: value.MaxInstances, BudgetPolicy: value.BudgetPolicy,
		Metadata: value.Metadata,
	})
	if err != nil {
		return ManagedReviewerAgentDefinition{}, err
	}
	return ManagedReviewerAgentDefinition{
		Kind: normalized.Kind, DesiredState: normalized.DesiredState,
		RoleName: normalized.Behavior.RoleName, MaxInstances: normalized.MaxInstances,
		BudgetPolicy: normalized.BudgetPolicy, Metadata: normalized.Metadata,
	}, nil
}

func managedReviewerPresetFingerprint(preset ManagedReviewerPreset) (string, error) {
	intent := struct {
		Domain   string                         `json:"domain"`
		PresetID string                         `json:"preset_id"`
		Revision int64                          `json:"revision"`
		Role     ManagedReviewerRoleDefinition  `json:"role"`
		Agent    ManagedReviewerAgentDefinition `json:"agent"`
	}{
		Domain: "fleetdb.managed_reviewer_preset.v1", PresetID: preset.PresetID,
		Revision: preset.Revision, Role: preset.Role, Agent: preset.Agent,
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return "", fmt.Errorf("encode managed reviewer preset fingerprint: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func validateManagedReviewerResult(
	result *ManagedReviewerResult,
	command ManagedReviewerCommand,
	fingerprint string,
) error {
	if result == nil || result.PresetID != command.Preset.PresetID ||
		result.PresetRevision != command.Preset.Revision ||
		result.PresetFingerprint != fingerprint || len(result.PresetFingerprint) != 64 {
		return ErrInvalidPersistedState
	}
	wantRole := RoleDefinition{
		Name: command.Preset.Role.Name, Kind: command.Preset.Role.Kind,
		Description: command.Preset.Role.Description, Prompt: command.Preset.Role.Prompt,
		PromptFile: command.Preset.Role.PromptFile, Model: command.Preset.Role.Model,
		TaskFilter: command.Preset.Role.TaskFilter, Backend: command.Preset.Role.Backend,
		Effort: command.Preset.Role.Effort, PathPatterns: command.Preset.Role.PathPatterns,
		Skills: command.Preset.Role.Skills, MaxPriority: command.Preset.Role.MaxPriority,
		MaxConcurrency: command.Preset.Role.MaxConcurrency, ReadOnly: command.Preset.Role.ReadOnly,
		AllowedTools: command.Preset.Role.AllowedTools, DeniedTools: command.Preset.Role.DeniedTools,
		MaxBudgetUSD: command.Preset.Role.MaxBudgetUSD,
	}
	if err := validateExactRole(result.Role, command.WorkspaceKey, wantRole); err != nil {
		return ErrInvalidPersistedState
	}
	if err := validatePersistedAgent(result.Agent, command.WorkspaceKey, command.AgentID); err != nil {
		return err
	}
	wantAgent := command.Preset.Agent
	if result.Agent.Name != command.AgentID || result.Agent.Kind != wantAgent.Kind ||
		result.Agent.DesiredState != wantAgent.DesiredState ||
		result.Agent.Behavior != (BehaviorReference{RoleName: wantAgent.RoleName}) ||
		result.Agent.MaxInstances != wantAgent.MaxInstances ||
		result.Agent.BudgetPolicy != wantAgent.BudgetPolicy ||
		!equalStringMap(result.Agent.Metadata, wantAgent.Metadata) {
		return ErrInvalidPersistedState
	}
	if command.DesiredState == ManagedReviewerActive && result.Agent.DeletedAt != nil {
		return ErrInvalidPersistedState
	}
	if command.DesiredState == ManagedReviewerArchived && result.Agent.DeletedAt == nil {
		return ErrInvalidPersistedState
	}
	return nil
}

func cloneManagedReviewerPreset(value ManagedReviewerPreset) ManagedReviewerPreset {
	value.Role.PathPatterns = slices.Clone(value.Role.PathPatterns)
	value.Role.Skills = slices.Clone(value.Role.Skills)
	value.Role.MaxPriority = cloneInt(value.Role.MaxPriority)
	value.Role.MaxConcurrency = cloneInt(value.Role.MaxConcurrency)
	value.Role.AllowedTools = slices.Clone(value.Role.AllowedTools)
	value.Role.DeniedTools = slices.Clone(value.Role.DeniedTools)
	value.Role.MaxBudgetUSD = cloneFloat64(value.Role.MaxBudgetUSD)
	value.Agent.Metadata = cloneStringMap(value.Agent.Metadata)
	return value
}

func cloneManagedReviewerResult(value *ManagedReviewerResult) *ManagedReviewerResult {
	if value == nil {
		return nil
	}
	out := *value
	out.Role = cloneRole(value.Role)
	out.Agent = cloneAgent(value.Agent)
	return &out
}
