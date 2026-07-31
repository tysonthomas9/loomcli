// Package agentprovisioning coordinates the durable Role -> Agent -> Binding
// -> Grant workflow. It owns only coordination progress; each aggregate remains
// owned by its capability.
package agentprovisioning

import "time"

type State string

const (
	StatePending         State = "pending"
	StateRunning         State = "running"
	StateRetryableFailed State = "retryable_failed"
	// StatePermanentFailed is terminal because the immutable intent cannot
	// repair an invalid or conflicting capability command by replaying it.
	StatePermanentFailed State = "permanent_failed"
	StateCompleted       State = "completed"
)

func (state State) valid() bool {
	switch state {
	case StatePending, StateRunning, StateRetryableFailed, StatePermanentFailed, StateCompleted:
		return true
	default:
		return false
	}
}

func (state State) pendingRecovery() bool {
	switch state {
	case StatePending, StateRunning, StateRetryableFailed:
		return true
	default:
		return false
	}
}

type Step string

const (
	StepRole    Step = "role"
	StepAgent   Step = "agent"
	StepBinding Step = "binding"
	StepGrant   Step = "grant"
)

// UnusedRolePolicy is explicit because Role is independently committed before
// the other aggregates. Phase 5 deliberately retains an unused Role after a
// later failure; retry or an operator may safely reuse/delete it.
type UnusedRolePolicy string

const (
	UnusedRoleRetain UnusedRolePolicy = "retain"
)

type RoleSpec struct {
	Name           string
	Kind           string
	Description    string
	Prompt         string
	PromptFile     string
	Model          string
	TaskFilter     string
	Backend        string
	Effort         string
	PathPatterns   []string
	Skills         []string
	MaxPriority    *int
	MaxConcurrency *int
	ReadOnly       bool
	AllowedTools   []string
	DeniedTools    []string
	MaxBudgetUSD   *float64
}

type AgentSpec struct {
	AgentID      string
	Name         string
	Kind         string
	DesiredState string
	RoleName     string
	BudgetPolicy string
	Metadata     map[string]string
}

type BindingSpec struct {
	BindingID         string
	Name              string
	SourceKind        string
	SourceConfigRef   string
	RouteKey          string
	EventPatterns     []string
	DriverID          string
	DriverVersionID   string
	Entrypoint        string
	ConcurrencyPolicy string
	Schedule          string
	ScheduleZone      string
	Enabled           bool
}

type GrantSpec struct {
	GrantID         string
	ConnectorID     string
	Action          string
	ResourcePattern string
}

type Spec struct {
	ProvisioningID string
	WorkspaceKey   string
	Role           RoleSpec
	Agent          AgentSpec
	Binding        BindingSpec
	Grants         []GrantSpec
}

type Record struct {
	ProvisioningID           string
	ProvisioningGenerationID string
	WorkspaceKey             string
	RequestedBy              string
	SpecFingerprint          string
	Spec                     Spec
	State                    State
	CompletedSteps           []Step
	CompletedGrants          []string
	UnusedRolePolicy         UnusedRolePolicy
	LastErrorClass           string
	Version                  int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
	CompletedAt              *time.Time
}

func cloneSpec(in Spec) Spec {
	out := in
	out.Role.PathPatterns = append([]string(nil), in.Role.PathPatterns...)
	out.Role.Skills = append([]string(nil), in.Role.Skills...)
	out.Role.MaxPriority = cloneInt(in.Role.MaxPriority)
	out.Role.MaxConcurrency = cloneInt(in.Role.MaxConcurrency)
	out.Role.AllowedTools = append([]string(nil), in.Role.AllowedTools...)
	out.Role.DeniedTools = append([]string(nil), in.Role.DeniedTools...)
	out.Role.MaxBudgetUSD = cloneFloat64(in.Role.MaxBudgetUSD)
	out.Agent.Metadata = cloneMap(in.Agent.Metadata)
	out.Binding.EventPatterns = append([]string(nil), in.Binding.EventPatterns...)
	out.Grants = append([]GrantSpec(nil), in.Grants...)
	return out
}

func cloneRecord(in *Record) *Record {
	if in == nil {
		return nil
	}
	out := *in
	out.Spec = cloneSpec(in.Spec)
	out.CompletedSteps = append([]Step(nil), in.CompletedSteps...)
	out.CompletedGrants = append([]string(nil), in.CompletedGrants...)
	if in.CompletedAt != nil {
		value := *in.CompletedAt
		out.CompletedAt = &value
	}
	return &out
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneInt(in *int) *int {
	if in == nil {
		return nil
	}
	value := *in
	return &value
}

func cloneFloat64(in *float64) *float64 {
	if in == nil {
		return nil
	}
	value := *in
	return &value
}
