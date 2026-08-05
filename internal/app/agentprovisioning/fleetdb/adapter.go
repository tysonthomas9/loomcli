// Package fleetdb adapts the shared low-level FleetDB transport to the
// AgentProvisioning workflow's owned durable progress port.
package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
)

type Transport interface {
	BeginAgentProvisioning(context.Context, string, infrafleetdb.AgentProvisioningBeginInput) (*infrafleetdb.AgentProvisioningRecord, error)
	GetAgentProvisioning(context.Context, string, string) (*infrafleetdb.AgentProvisioningRecord, error)
	ListPendingAgentProvisioning(context.Context, string, int) ([]*infrafleetdb.AgentProvisioningRecord, error)
	SaveAgentProvisioningProgress(context.Context, string, string, infrafleetdb.AgentProvisioningProgressInput) (*infrafleetdb.AgentProvisioningRecord, error)
	EnsureAgentProvisioningRole(context.Context, string, string, string) (*infrafleetdb.AgentProvisioningRoleResult, error)
	EnsureAgentProvisioningAgentService(context.Context, string, string, string) (*infrafleetdb.AgentProvisioningAgentResult, error)
	EnsureAgentProvisioningTriggerBinding(context.Context, string, string, string) (*infrafleetdb.AgentProvisioningBindingResult, error)
	EnsureAgentProvisioningConnectorGrant(context.Context, string, string, string, string) (*infrafleetdb.AgentProvisioningGrantResult, error)
}

type Adapter struct {
	transport Transport
}

var (
	_ agentprovisioning.ProgressStore     = (*Adapter)(nil)
	_ agentprovisioning.RoleOperations    = (*Adapter)(nil)
	_ agentprovisioning.AgentOperations   = (*Adapter)(nil)
	_ agentprovisioning.BindingOperations = (*Adapter)(nil)
	_ agentprovisioning.GrantOperations   = (*Adapter)(nil)
)

func New(transport Transport) (*Adapter, error) {
	if transport == nil {
		return nil, fmt.Errorf("compose AgentProvisioning FleetDB adapter: %w", agentprovisioning.ErrUnavailable)
	}
	return &Adapter{transport: transport}, nil
}

func (adapter *Adapter) Begin(
	ctx context.Context,
	spec agentprovisioning.Spec,
	requestedBy string,
) (*agentprovisioning.Record, error) {
	value, err := adapter.transport.BeginAgentProvisioning(
		ctx,
		spec.WorkspaceKey,
		infrafleetdb.AgentProvisioningBeginInput{
			ProvisioningID: spec.ProvisioningID,
			Role:           roleToWire(spec.Role),
			Agent:          agentToWire(spec.Agent),
			Binding:        bindingToWire(spec.Binding),
			Grants:         grantsToWire(spec.Grants),
			DelegatedActor: requestedBy,
		},
	)
	if err != nil {
		return nil, mapError("begin", err)
	}
	return recordFromWire("begin", value)
}

func (adapter *Adapter) Get(
	ctx context.Context,
	workspace,
	provisioningID string,
) (*agentprovisioning.Record, error) {
	value, err := adapter.transport.GetAgentProvisioning(ctx, workspace, provisioningID)
	if err != nil {
		return nil, mapError("get", err)
	}
	return recordFromWire("get", value)
}

func (adapter *Adapter) Save(
	ctx context.Context,
	record *agentprovisioning.Record,
	expectedVersion int64,
) (*agentprovisioning.Record, error) {
	if record == nil {
		return nil, fmt.Errorf("save AgentProvisioning record is required: %w", agentprovisioning.ErrInvalid)
	}
	value, err := adapter.transport.SaveAgentProvisioningProgress(
		ctx,
		record.WorkspaceKey,
		record.ProvisioningID,
		infrafleetdb.AgentProvisioningProgressInput{
			ExpectedProvisioningGenerationID: record.ProvisioningGenerationID,
			ExpectedVersion:                  expectedVersion,
			State:                            string(record.State),
			CompletedSteps:                   stepsToWire(record.CompletedSteps),
			CompletedGrants:                  append([]string(nil), record.CompletedGrants...),
			LastErrorClass:                   record.LastErrorClass,
		},
	)
	if err != nil {
		return nil, mapError("save", err)
	}
	return recordFromWire("save", value)
}

func (adapter *Adapter) ListPending(
	ctx context.Context,
	workspace string,
	limit int,
) ([]*agentprovisioning.Record, error) {
	values, err := adapter.transport.ListPendingAgentProvisioning(ctx, workspace, limit)
	if err != nil {
		return nil, mapError("list pending", err)
	}
	out := make([]*agentprovisioning.Record, 0, len(values))
	for index, value := range values {
		record, convertErr := recordFromWire(fmt.Sprintf("list pending record %d", index), value)
		if convertErr != nil {
			return nil, convertErr
		}
		out = append(out, record)
	}
	return out, nil
}

func (adapter *Adapter) EnsureRole(
	ctx context.Context,
	command agentprovisioning.EnsureRoleCommand,
) error {
	if err := validateStepCoordinates(
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
	); err != nil {
		return err
	}
	value, err := adapter.transport.EnsureAgentProvisioningRole(
		ctx,
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
	)
	if err != nil {
		return mapError("ensure Role", err)
	}
	if value == nil || value.WorkspaceKey != command.WorkspaceKey {
		return fmt.Errorf("ensure Role returned divergent owner state: %w", agentprovisioning.ErrConflict)
	}
	actual := agentprovisioning.RoleSpec{
		Name: value.Name, Kind: value.Kind, Description: value.Description,
		Prompt: value.Prompt, PromptFile: value.PromptFile, Model: value.Model,
		TaskFilter: value.TaskFilter, Backend: value.Backend, Effort: value.Effort,
		PathPatterns: append([]string(nil), value.PathPatterns...),
		Skills:       append([]string(nil), value.Skills...),
		MaxPriority:  cloneInt(value.MaxPriority), MaxConcurrency: cloneInt(value.MaxConcurrency),
		ReadOnly:     value.ReadOnly,
		AllowedTools: append([]string(nil), value.AllowedTools...),
		DeniedTools:  append([]string(nil), value.DeniedTools...),
		MaxBudgetUSD: cloneFloat64(value.MaxBudgetUSD),
	}
	if !reflect.DeepEqual(actual, command.Role) {
		return fmt.Errorf("ensure Role returned a different immutable definition: %w", agentprovisioning.ErrConflict)
	}
	return nil
}

func (adapter *Adapter) EnsureAgent(
	ctx context.Context,
	command agentprovisioning.EnsureAgentCommand,
) error {
	if err := validateStepCoordinates(
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
	); err != nil {
		return err
	}
	value, err := adapter.transport.EnsureAgentProvisioningAgentService(
		ctx,
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
	)
	if err != nil {
		return mapError("ensure AgentService", err)
	}
	if value == nil || value.WorkspaceKey != command.WorkspaceKey {
		return fmt.Errorf("ensure AgentService returned divergent owner state: %w", agentprovisioning.ErrConflict)
	}
	actual := agentprovisioning.AgentSpec{
		AgentID: value.ServiceID, Name: value.Name, Kind: value.Kind,
		DesiredState: value.DesiredState, RoleName: value.RoleName,
		BudgetPolicy: value.BudgetPolicy, Metadata: cloneMap(value.Metadata),
	}
	if !reflect.DeepEqual(actual, command.Agent) {
		return fmt.Errorf("ensure AgentService returned a different immutable definition: %w", agentprovisioning.ErrConflict)
	}
	return nil
}

func (adapter *Adapter) EnsureBinding(
	ctx context.Context,
	command agentprovisioning.EnsureBindingCommand,
) error {
	if err := validateStepCoordinates(
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
	); err != nil {
		return err
	}
	value, err := adapter.transport.EnsureAgentProvisioningTriggerBinding(
		ctx,
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
	)
	if err != nil {
		return mapError("ensure TriggerBinding", err)
	}
	if value == nil || value.WorkspaceKey != command.WorkspaceKey ||
		value.TargetAgentServiceID != command.AgentID {
		return fmt.Errorf("ensure TriggerBinding returned divergent owner state: %w", agentprovisioning.ErrConflict)
	}
	actual := agentprovisioning.BindingSpec{
		BindingID: value.BindingID, Name: value.Name, SourceKind: value.SourceKind,
		SourceConfigRef: value.SourceConfigRef, RouteKey: value.RouteKey,
		EventPatterns: append([]string(nil), value.EventTypePatterns...),
		DriverID:      value.DriverID, DriverVersionID: value.DriverVersionID,
		Entrypoint: value.TargetEntrypoint, ConcurrencyPolicy: value.ConcurrencyPolicy,
		Schedule: value.Schedule, ScheduleZone: value.ScheduleTimezone, Enabled: value.Enabled,
	}
	if !reflect.DeepEqual(actual, command.Binding) {
		return fmt.Errorf("ensure TriggerBinding returned a different immutable definition: %w", agentprovisioning.ErrConflict)
	}
	return nil
}

func (adapter *Adapter) EnsureGrant(
	ctx context.Context,
	command agentprovisioning.EnsureGrantCommand,
) error {
	if err := validateStepCoordinates(
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
	); err != nil {
		return err
	}
	value, err := adapter.transport.EnsureAgentProvisioningConnectorGrant(
		ctx,
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
		command.Grant.GrantID,
	)
	if err != nil {
		return mapError("ensure ConnectorGrant", err)
	}
	if value == nil ||
		value.WorkspaceKey != command.WorkspaceKey ||
		value.BindingID != command.BindingID ||
		value.GrantID != command.Grant.GrantID ||
		value.ConnectorID != command.Grant.ConnectorID ||
		value.Action != command.Grant.Action ||
		value.ResourcePattern != command.Grant.ResourcePattern ||
		(value.RevokedAt != nil && !value.RevokedAt.IsZero()) {
		return fmt.Errorf("ensure ConnectorGrant returned a different immutable definition: %w", agentprovisioning.ErrConflict)
	}
	return nil
}

func validateStepCoordinates(
	workspace,
	provisioningID,
	provisioningGenerationID string,
) error {
	if workspace == "" || provisioningID == "" ||
		!validProvisioningGenerationID(provisioningGenerationID) {
		return fmt.Errorf("guarded provisioning step coordinates are invalid: %w", agentprovisioning.ErrInvalid)
	}
	return nil
}

func validProvisioningGenerationID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for index := range value {
		character := value[index]
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func recordFromWire(
	operation string,
	value *infrafleetdb.AgentProvisioningRecord,
) (*agentprovisioning.Record, error) {
	if value == nil {
		return nil, nil
	}
	if value.ProvisioningID != value.Spec.ProvisioningID ||
		value.WorkspaceKey != value.Spec.WorkspaceKey ||
		value.RequestedBy != value.Spec.RequestedBy {
		return nil, fmt.Errorf(
			"%s AgentProvisioning returned internally divergent server-owned identity: %w",
			operation,
			agentprovisioning.ErrConflict,
		)
	}
	return &agentprovisioning.Record{
		ProvisioningID:           value.ProvisioningID,
		ProvisioningGenerationID: value.ProvisioningGenerationID,
		WorkspaceKey:             value.WorkspaceKey,
		RequestedBy:              value.RequestedBy,
		SpecFingerprint:          value.SpecFingerprint,
		Spec:                     specFromWire(value.Spec),
		State:                    agentprovisioning.State(value.State),
		CompletedSteps:           stepsFromWire(value.CompletedSteps),
		CompletedGrants:          append([]string(nil), value.CompletedGrants...),
		UnusedRolePolicy:         agentprovisioning.UnusedRolePolicy(value.UnusedRolePolicy),
		LastErrorClass:           value.LastErrorClass,
		Version:                  value.Version,
		CreatedAt:                value.CreatedAt,
		UpdatedAt:                value.UpdatedAt,
		CompletedAt:              cloneTime(value.CompletedAt),
	}, nil
}

func specFromWire(value infrafleetdb.AgentProvisioningSpec) agentprovisioning.Spec {
	return agentprovisioning.Spec{
		ProvisioningID: value.ProvisioningID,
		WorkspaceKey:   value.WorkspaceKey,
		Role: agentprovisioning.RoleSpec{
			Name: value.Role.Name, Kind: value.Role.Kind, Description: value.Role.Description,
			Prompt: value.Role.Prompt, PromptFile: value.Role.PromptFile, Model: value.Role.Model,
			TaskFilter: value.Role.TaskFilter, Backend: value.Role.Backend, Effort: value.Role.Effort,
			PathPatterns: append([]string(nil), value.Role.PathPatterns...),
			Skills:       append([]string(nil), value.Role.Skills...),
			MaxPriority:  cloneInt(value.Role.MaxPriority), MaxConcurrency: cloneInt(value.Role.MaxConcurrency),
			ReadOnly:     value.Role.ReadOnly,
			AllowedTools: append([]string(nil), value.Role.AllowedTools...),
			DeniedTools:  append([]string(nil), value.Role.DeniedTools...),
			MaxBudgetUSD: cloneFloat64(value.Role.MaxBudgetUSD),
		},
		Agent: agentprovisioning.AgentSpec{
			AgentID: value.Agent.AgentID, Name: value.Agent.Name, Kind: value.Agent.Kind,
			DesiredState: value.Agent.DesiredState, RoleName: value.Agent.RoleName,
			BudgetPolicy: value.Agent.BudgetPolicy, Metadata: cloneMap(value.Agent.Metadata),
		},
		Binding: agentprovisioning.BindingSpec{
			BindingID: value.Binding.BindingID, Name: value.Binding.Name,
			SourceKind: value.Binding.SourceKind, SourceConfigRef: value.Binding.SourceConfigRef,
			RouteKey:      value.Binding.RouteKey,
			EventPatterns: append([]string(nil), value.Binding.EventPatterns...),
			DriverID:      value.Binding.DriverID, DriverVersionID: value.Binding.DriverVersionID,
			Entrypoint: value.Binding.Entrypoint, ConcurrencyPolicy: value.Binding.ConcurrencyPolicy,
			Schedule:     value.Binding.Schedule,
			ScheduleZone: value.Binding.ScheduleZone, Enabled: value.Binding.Enabled,
		},
		Grants: grantsFromWire(value.Grants),
	}
}

func roleToWire(value agentprovisioning.RoleSpec) infrafleetdb.AgentProvisioningRoleSpec {
	return infrafleetdb.AgentProvisioningRoleSpec{
		Name: value.Name, Kind: value.Kind, Description: value.Description,
		Prompt: value.Prompt, PromptFile: value.PromptFile, Model: value.Model,
		TaskFilter: value.TaskFilter, Backend: value.Backend, Effort: value.Effort,
		PathPatterns: append([]string(nil), value.PathPatterns...),
		Skills:       append([]string(nil), value.Skills...),
		MaxPriority:  cloneInt(value.MaxPriority), MaxConcurrency: cloneInt(value.MaxConcurrency),
		ReadOnly:     value.ReadOnly,
		AllowedTools: append([]string(nil), value.AllowedTools...),
		DeniedTools:  append([]string(nil), value.DeniedTools...),
		MaxBudgetUSD: cloneFloat64(value.MaxBudgetUSD),
	}
}

func agentToWire(value agentprovisioning.AgentSpec) infrafleetdb.AgentProvisioningAgentSpec {
	return infrafleetdb.AgentProvisioningAgentSpec{
		AgentID: value.AgentID, Name: value.Name, Kind: value.Kind,
		DesiredState: value.DesiredState, RoleName: value.RoleName,
		BudgetPolicy: value.BudgetPolicy, Metadata: cloneMap(value.Metadata),
	}
}

func bindingToWire(value agentprovisioning.BindingSpec) infrafleetdb.AgentProvisioningBindingSpec {
	return infrafleetdb.AgentProvisioningBindingSpec{
		BindingID: value.BindingID, Name: value.Name, SourceKind: value.SourceKind,
		SourceConfigRef: value.SourceConfigRef, RouteKey: value.RouteKey,
		EventPatterns: append([]string(nil), value.EventPatterns...),
		DriverID:      value.DriverID, DriverVersionID: value.DriverVersionID,
		Entrypoint: value.Entrypoint, ConcurrencyPolicy: value.ConcurrencyPolicy,
		Schedule:     value.Schedule,
		ScheduleZone: value.ScheduleZone, Enabled: value.Enabled,
	}
}

func grantsToWire(values []agentprovisioning.GrantSpec) []infrafleetdb.AgentProvisioningGrantSpec {
	out := make([]infrafleetdb.AgentProvisioningGrantSpec, len(values))
	for index, value := range values {
		out[index] = infrafleetdb.AgentProvisioningGrantSpec{
			GrantID: value.GrantID, ConnectorID: value.ConnectorID, Action: value.Action,
			ResourcePattern: value.ResourcePattern,
		}
	}
	return out
}

func grantsFromWire(values []infrafleetdb.AgentProvisioningGrantSpec) []agentprovisioning.GrantSpec {
	out := make([]agentprovisioning.GrantSpec, len(values))
	for index, value := range values {
		out[index] = agentprovisioning.GrantSpec{
			GrantID: value.GrantID, ConnectorID: value.ConnectorID, Action: value.Action,
			ResourcePattern: value.ResourcePattern,
		}
	}
	return out
}

func stepsToWire(values []agentprovisioning.Step) []string {
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = string(value)
	}
	return out
}

func stepsFromWire(values []string) []agentprovisioning.Step {
	out := make([]agentprovisioning.Step, len(values))
	for index, value := range values {
		out[index] = agentprovisioning.Step(value)
	}
	return out
}

func cloneMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	out := make(map[string]string, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var mapped error
	switch {
	case errors.Is(err, infrafleetdb.ErrAgentProvisioningNotFound):
		mapped = agentprovisioning.ErrNotFound
	case errors.Is(err, infrafleetdb.ErrAgentProvisioningInvalid):
		mapped = agentprovisioning.ErrInvalid
	case errors.Is(err, infrafleetdb.ErrAgentProvisioningConflict):
		mapped = agentprovisioning.ErrConflict
	case errors.Is(err, infrafleetdb.ErrAgentProvisioningConcurrentWrite):
		mapped = agentprovisioning.ErrConcurrentWrite
	case errors.Is(err, infrafleetdb.ErrAgentProvisioningInvalidTransition):
		mapped = agentprovisioning.ErrInvalidTransition
	default:
		mapped = agentprovisioning.ErrUnavailable
	}
	return fmt.Errorf("%s AgentProvisioning: %w", operation, errors.Join(mapped, err))
}
