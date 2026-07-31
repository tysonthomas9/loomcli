package agentprovisioning

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type Manager struct {
	progress  ProgressStore
	roles     RoleOperations
	agents    AgentOperations
	bindings  BindingOperations
	grants    GrantOperations
	admission *authority.Admission
	faults    FaultInjector
	now       func() time.Time
}

func New(
	progress ProgressStore,
	roles RoleOperations,
	agents AgentOperations,
	bindings BindingOperations,
	grants GrantOperations,
	admission *authority.Admission,
	faults FaultInjector,
	now func() time.Time,
) (*Manager, error) {
	if progress == nil || roles == nil || agents == nil || bindings == nil || grants == nil ||
		admission == nil || now == nil {
		return nil, fmt.Errorf("compose AgentProvisioning: every capability port, progress store, admission, and clock are required: %w", ErrUnavailable)
	}
	return &Manager{
		progress: progress, roles: roles, agents: agents, bindings: bindings,
		grants: grants, admission: admission, faults: faults, now: now,
	}, nil
}

// Begin durably records the full secret-free provisioning intent before any
// capability mutation. The ProgressStore boundary owns authenticated
// requester attribution, canonical fingerprinting, and durable timestamps;
// callers cannot mint those fields in a request DTO. Reusing an ID with the
// same normalized intent is idempotent; divergent input is a conflict.
func (manager *Manager) Begin(
	ctx context.Context,
	auth authority.OperatorAuthority,
	spec Spec,
) (*Record, error) {
	spec = normalizeSpec(spec)
	if spec.ProvisioningID == "" || spec.WorkspaceKey == "" {
		return nil, fmt.Errorf("provisioning and workspace are required: %w", ErrInvalid)
	}
	if err := manager.admission.RequireOperator(ActionBeginProvisioning, spec.WorkspaceKey, auth); err != nil {
		return nil, fmt.Errorf("begin provisioning authority: %w", err)
	}
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	record, err := manager.progress.Begin(ctx, cloneSpec(spec), auth.Subject())
	if err != nil {
		return nil, fmt.Errorf("begin provisioning %q: %w", spec.ProvisioningID, err)
	}
	if record == nil ||
		record.WorkspaceKey != spec.WorkspaceKey || record.ProvisioningID != spec.ProvisioningID {
		return nil, fmt.Errorf("begin returned divergent durable intent: %w", ErrConflict)
	}
	if err := validateRecord(record, spec.WorkspaceKey, spec.ProvisioningID); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(normalizeSpec(record.Spec), spec) {
		return nil, fmt.Errorf("begin returned a different canonical provisioning intent: %w", ErrConflict)
	}
	return cloneRecord(record), nil
}

// Run converges one durable provisioning intent. All capability calls use
// deterministic command IDs. A crash after an external commit but before the
// progress save therefore replays safely on restart.
//
//nolint:funlen // Keeping the ordered, replay-safe provisioning state machine together makes crash boundaries auditable.
func (manager *Manager) Run(ctx context.Context, workspace, provisioningID string) (*Record, error) {
	workspace = strings.TrimSpace(workspace)
	provisioningID = strings.TrimSpace(provisioningID)
	if workspace == "" || provisioningID == "" {
		return nil, fmt.Errorf("workspace and provisioning id are required: %w", ErrInvalid)
	}
	record, err := manager.progress.Get(ctx, workspace, provisioningID)
	if err != nil {
		return nil, fmt.Errorf("get provisioning %q: %w", provisioningID, err)
	}
	if err := validateRecord(record, workspace, provisioningID); err != nil {
		return nil, err
	}
	if record.State == StateCompleted {
		return cloneRecord(record), nil
	}
	if record.State == StatePermanentFailed {
		return cloneRecord(record), fmt.Errorf(
			"provisioning %q ended permanently with %q: %w",
			record.ProvisioningID,
			record.LastErrorClass,
			ErrPermanentFailure,
		)
	}
	record, err = manager.saveState(ctx, record, StateRunning, "")
	if err != nil {
		return nil, err
	}

	steps := []struct {
		step Step
		run  func(context.Context, *Record) error
	}{
		{StepRole, manager.ensureRole},
		{StepAgent, manager.ensureAgent},
		{StepBinding, manager.ensureBinding},
	}
	for _, item := range steps {
		if slices.Contains(record.CompletedSteps, item.step) {
			continue
		}
		if err := item.run(ctx, record); err != nil {
			return manager.fail(ctx, record, item.step, err)
		}
		if err := manager.inject(item.step, ""); err != nil {
			return nil, err
		}
		next := cloneRecord(record)
		next.CompletedSteps = append(next.CompletedSteps, item.step)
		record, err = manager.save(ctx, record, next)
		if err != nil {
			return nil, err
		}
	}

	for _, grant := range record.Spec.Grants {
		if slices.Contains(record.CompletedGrants, grant.GrantID) {
			continue
		}
		if err := manager.grants.EnsureGrant(ctx, EnsureGrantCommand{
			CommandID:    commandID(record.ProvisioningID, StepGrant, grant.GrantID),
			WorkspaceKey: record.WorkspaceKey, ProvisioningID: record.ProvisioningID,
			ProvisioningGenerationID: record.ProvisioningGenerationID,
			BindingID:                record.Spec.Binding.BindingID,
			Grant:                    grant,
		}); err != nil {
			return manager.fail(ctx, record, StepGrant, err)
		}
		if err := manager.inject(StepGrant, grant.GrantID); err != nil {
			return nil, err
		}
		next := cloneRecord(record)
		next.CompletedGrants = append(next.CompletedGrants, grant.GrantID)
		record, err = manager.save(ctx, record, next)
		if err != nil {
			return nil, err
		}
	}
	next := cloneRecord(record)
	if !slices.Contains(next.CompletedSteps, StepGrant) {
		next.CompletedSteps = append(next.CompletedSteps, StepGrant)
	}
	completedAt := manager.now()
	next.State = StateCompleted
	next.LastErrorClass = ""
	next.CompletedAt = &completedAt
	record, err = manager.save(ctx, record, next)
	if err != nil {
		return nil, err
	}
	return cloneRecord(record), nil
}

func (manager *Manager) Recover(ctx context.Context, workspace string, limit int) (int, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || limit < 0 {
		return 0, fmt.Errorf("workspace is required and limit cannot be negative: %w", ErrInvalid)
	}
	records, err := manager.progress.ListPending(ctx, workspace, limit)
	if err != nil {
		return 0, fmt.Errorf("list pending provisioning: %w", err)
	}
	completed := 0
	var failures []error
	for index, record := range records {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			return completed, errors.Join(failures...)
		}
		if record == nil {
			failures = append(failures, fmt.Errorf(
				"pending provisioning list contained nil at index %d: %w",
				index,
				ErrConflict,
			))
			continue
		}
		if _, err := manager.Run(ctx, workspace, record.ProvisioningID); err != nil {
			failures = append(failures, fmt.Errorf(
				"recover provisioning %q: %w",
				record.ProvisioningID,
				err,
			))
			continue
		}
		completed++
	}
	return completed, errors.Join(failures...)
}

func (manager *Manager) ensureRole(ctx context.Context, record *Record) error {
	return manager.roles.EnsureRole(ctx, EnsureRoleCommand{
		CommandID:    commandID(record.ProvisioningID, StepRole, ""),
		WorkspaceKey: record.WorkspaceKey, ProvisioningID: record.ProvisioningID,
		ProvisioningGenerationID: record.ProvisioningGenerationID,
		Role:                     record.Spec.Role,
	})
}

func (manager *Manager) ensureAgent(ctx context.Context, record *Record) error {
	return manager.agents.EnsureAgent(ctx, EnsureAgentCommand{
		CommandID:    commandID(record.ProvisioningID, StepAgent, ""),
		WorkspaceKey: record.WorkspaceKey, ProvisioningID: record.ProvisioningID,
		ProvisioningGenerationID: record.ProvisioningGenerationID,
		Agent:                    record.Spec.Agent,
	})
}

func (manager *Manager) ensureBinding(ctx context.Context, record *Record) error {
	return manager.bindings.EnsureBinding(ctx, EnsureBindingCommand{
		CommandID:    commandID(record.ProvisioningID, StepBinding, ""),
		WorkspaceKey: record.WorkspaceKey, ProvisioningID: record.ProvisioningID,
		ProvisioningGenerationID: record.ProvisioningGenerationID,
		AgentID:                  record.Spec.Agent.AgentID,
		Binding:                  record.Spec.Binding,
	})
}

func (manager *Manager) inject(step Step, key string) error {
	if manager.faults == nil {
		return nil
	}
	if err := manager.faults.AfterExternalCommit(step, key); err != nil {
		return fmt.Errorf("fault after %s external commit: %w", step, err)
	}
	return nil
}

func (manager *Manager) fail(ctx context.Context, record *Record, step Step, cause error) (*Record, error) {
	failed, saveErr := manager.saveState(ctx, record, failureState(cause), errorClass(step, cause))
	if saveErr != nil {
		return nil, errors.Join(cause, saveErr)
	}
	return cloneRecord(failed), fmt.Errorf("provisioning step %s: %w", step, cause)
}

func (manager *Manager) saveState(
	ctx context.Context,
	record *Record,
	state State,
	errorClass string,
) (*Record, error) {
	next := cloneRecord(record)
	next.State = state
	next.LastErrorClass = errorClass
	return manager.save(ctx, record, next)
}

func (manager *Manager) save(ctx context.Context, previous, next *Record) (*Record, error) {
	expected := previous.Version
	next = cloneRecord(next)
	next.Version = expected
	next.UpdatedAt = manager.now()
	if err := validateTransition(previous, next); err != nil {
		return nil, err
	}
	saved, err := manager.progress.Save(ctx, cloneRecord(next), expected)
	if err != nil {
		return nil, fmt.Errorf("save provisioning %q version %d: %w", next.ProvisioningID, expected, err)
	}
	if saved == nil || saved.Version != expected+1 {
		return nil, fmt.Errorf("save returned invalid version or intent: %w", ErrConcurrentWrite)
	}
	if err := validateRecord(saved, next.WorkspaceKey, next.ProvisioningID); err != nil {
		return nil, err
	}
	if err := validateTransition(previous, saved); err != nil {
		return nil, err
	}
	if saved.State != next.State ||
		!slices.Equal(saved.CompletedSteps, next.CompletedSteps) ||
		!slices.Equal(saved.CompletedGrants, next.CompletedGrants) ||
		saved.LastErrorClass != next.LastErrorClass ||
		(saved.CompletedAt == nil) != (next.CompletedAt == nil) {
		return nil, fmt.Errorf("save returned divergent durable progress: %w", ErrConcurrentWrite)
	}
	return cloneRecord(saved), nil
}

//nolint:funlen // Canonicalization intentionally covers every immutable provisioning field in one reviewable contract.
func normalizeSpec(spec Spec) Spec {
	spec.ProvisioningID = strings.TrimSpace(spec.ProvisioningID)
	spec.WorkspaceKey = strings.TrimSpace(spec.WorkspaceKey)
	spec.Role.Name = strings.TrimSpace(spec.Role.Name)
	spec.Role.Kind = strings.TrimSpace(spec.Role.Kind)
	spec.Role.Description = strings.TrimSpace(spec.Role.Description)
	spec.Role.PromptFile = strings.TrimSpace(spec.Role.PromptFile)
	spec.Role.Model = strings.TrimSpace(spec.Role.Model)
	spec.Role.TaskFilter = strings.TrimSpace(spec.Role.TaskFilter)
	spec.Role.Backend = strings.TrimSpace(spec.Role.Backend)
	spec.Role.Effort = strings.TrimSpace(spec.Role.Effort)
	spec.Role.PathPatterns = normalizeStringList(spec.Role.PathPatterns)
	spec.Role.Skills = normalizeStringList(spec.Role.Skills)
	spec.Role.MaxPriority = cloneInt(spec.Role.MaxPriority)
	spec.Role.MaxConcurrency = cloneInt(spec.Role.MaxConcurrency)
	spec.Role.AllowedTools = normalizeStringList(spec.Role.AllowedTools)
	spec.Role.DeniedTools = normalizeStringList(spec.Role.DeniedTools)
	spec.Role.MaxBudgetUSD = cloneFloat64(spec.Role.MaxBudgetUSD)
	spec.Agent.AgentID = strings.TrimSpace(spec.Agent.AgentID)
	spec.Agent.Name = strings.TrimSpace(spec.Agent.Name)
	spec.Agent.Kind = strings.TrimSpace(spec.Agent.Kind)
	spec.Agent.DesiredState = strings.TrimSpace(spec.Agent.DesiredState)
	spec.Agent.RoleName = strings.TrimSpace(spec.Agent.RoleName)
	spec.Agent.BudgetPolicy = strings.TrimSpace(spec.Agent.BudgetPolicy)
	spec.Agent.Metadata = cloneMap(spec.Agent.Metadata)
	if len(spec.Agent.Metadata) == 0 {
		spec.Agent.Metadata = nil
	}
	spec.Binding.BindingID = strings.TrimSpace(spec.Binding.BindingID)
	spec.Binding.Name = strings.TrimSpace(spec.Binding.Name)
	spec.Binding.SourceKind = strings.TrimSpace(spec.Binding.SourceKind)
	spec.Binding.SourceConfigRef = strings.TrimSpace(spec.Binding.SourceConfigRef)
	spec.Binding.RouteKey = strings.TrimSpace(spec.Binding.RouteKey)
	if spec.Binding.RouteKey == "" {
		switch spec.Binding.SourceKind {
		case "cron":
			spec.Binding.RouteKey = "cron:" + spec.Binding.BindingID
		case "internal":
			spec.Binding.RouteKey = "internal:" + spec.Binding.BindingID
		}
	}
	if spec.Binding.Name == "" {
		spec.Binding.Name = spec.Binding.RouteKey
		if spec.Binding.Name == "" {
			spec.Binding.Name = spec.Binding.BindingID
		}
	}
	spec.Binding.DriverID = strings.TrimSpace(spec.Binding.DriverID)
	spec.Binding.DriverVersionID = strings.TrimSpace(spec.Binding.DriverVersionID)
	spec.Binding.Entrypoint = strings.TrimSpace(spec.Binding.Entrypoint)
	spec.Binding.ConcurrencyPolicy = strings.TrimSpace(spec.Binding.ConcurrencyPolicy)
	if spec.Binding.ConcurrencyPolicy == "" {
		spec.Binding.ConcurrencyPolicy = "one_active_per_epic"
	}
	spec.Binding.Schedule = strings.TrimSpace(spec.Binding.Schedule)
	spec.Binding.ScheduleZone = strings.TrimSpace(spec.Binding.ScheduleZone)
	for index := range spec.Binding.EventPatterns {
		spec.Binding.EventPatterns[index] = strings.TrimSpace(spec.Binding.EventPatterns[index])
	}
	sort.Strings(spec.Binding.EventPatterns)
	spec.Binding.EventPatterns = slices.Compact(spec.Binding.EventPatterns)
	if len(spec.Binding.EventPatterns) == 0 {
		spec.Binding.EventPatterns = nil
	}
	for index := range spec.Grants {
		spec.Grants[index].GrantID = strings.TrimSpace(spec.Grants[index].GrantID)
		spec.Grants[index].ConnectorID = strings.TrimSpace(spec.Grants[index].ConnectorID)
		spec.Grants[index].Action = strings.TrimSpace(spec.Grants[index].Action)
		spec.Grants[index].ResourcePattern = strings.TrimSpace(spec.Grants[index].ResourcePattern)
	}
	sort.Slice(spec.Grants, func(i, j int) bool {
		if spec.Grants[i].GrantID != spec.Grants[j].GrantID {
			return spec.Grants[i].GrantID < spec.Grants[j].GrantID
		}
		if spec.Grants[i].ConnectorID != spec.Grants[j].ConnectorID {
			return spec.Grants[i].ConnectorID < spec.Grants[j].ConnectorID
		}
		if spec.Grants[i].Action != spec.Grants[j].Action {
			return spec.Grants[i].Action < spec.Grants[j].Action
		}
		return spec.Grants[i].ResourcePattern < spec.Grants[j].ResourcePattern
	})
	if len(spec.Grants) == 0 {
		spec.Grants = nil
	}
	return spec
}

func normalizeStringList(values []string) []string {
	out := append([]string(nil), values...)
	for index := range out {
		out[index] = strings.TrimSpace(out[index])
	}
	return out
}

//nolint:cyclop // Exhaustive field validation preserves precise errors for the immutable provisioning contract.
func validateSpec(spec Spec) error {
	if spec.ProvisioningID == "" || spec.WorkspaceKey == "" ||
		spec.Role.Name == "" || spec.Agent.AgentID == "" || spec.Agent.RoleName == "" ||
		spec.Agent.Name == "" || spec.Agent.Kind == "" || spec.Agent.DesiredState == "" ||
		spec.Agent.RoleName != spec.Role.Name || spec.Binding.BindingID == "" ||
		spec.Binding.SourceKind == "" || spec.Binding.DriverID == "" ||
		spec.Binding.DriverVersionID == "" || spec.Binding.Entrypoint == "" {
		return fmt.Errorf("provisioning/workspace, matching role reference, complete agent identity, and executable binding are required: %w", ErrInvalid)
	}
	if spec.Role.MaxPriority != nil && *spec.Role.MaxPriority < 0 {
		return fmt.Errorf("role max priority cannot be negative: %w", ErrInvalid)
	}
	if spec.Role.MaxConcurrency != nil && *spec.Role.MaxConcurrency <= 0 {
		return fmt.Errorf("role max concurrency must be positive: %w", ErrInvalid)
	}
	if spec.Role.MaxBudgetUSD != nil && *spec.Role.MaxBudgetUSD < 0 {
		return fmt.Errorf("role max budget cannot be negative: %w", ErrInvalid)
	}
	for label, values := range map[string][]string{
		"path pattern": spec.Role.PathPatterns,
		"skill":        spec.Role.Skills,
		"allowed tool": spec.Role.AllowedTools,
		"denied tool":  spec.Role.DeniedTools,
	} {
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			if value == "" {
				return fmt.Errorf("role %s cannot be empty: %w", label, ErrInvalid)
			}
			if _, duplicate := seen[value]; duplicate {
				return fmt.Errorf("duplicate role %s %q: %w", label, value, ErrInvalid)
			}
			seen[value] = struct{}{}
		}
	}
	grantIDs := make(map[string]struct{}, len(spec.Grants))
	for _, grant := range spec.Grants {
		if grant.GrantID == "" || grant.ConnectorID == "" ||
			grant.Action == "" || grant.ResourcePattern == "" {
			return fmt.Errorf("every grant needs id, connector, action, and resource pattern: %w", ErrInvalid)
		}
		if _, exists := grantIDs[grant.GrantID]; exists {
			return fmt.Errorf("duplicate grant id %q: %w", grant.GrantID, ErrInvalid)
		}
		grantIDs[grant.GrantID] = struct{}{}
	}
	return nil
}

func validateRecord(record *Record, workspace, provisioningID string) error {
	if record == nil {
		return ErrNotFound
	}
	if record.WorkspaceKey != workspace || record.ProvisioningID != provisioningID ||
		!validProvisioningGenerationID(record.ProvisioningGenerationID) ||
		!validFingerprint(record.SpecFingerprint) || record.UnusedRolePolicy != UnusedRoleRetain ||
		record.Version <= 0 || record.RequestedBy == "" ||
		record.Spec.WorkspaceKey != record.WorkspaceKey ||
		record.Spec.ProvisioningID != record.ProvisioningID {
		return fmt.Errorf("durable provisioning record is divergent: %w", ErrConflict)
	}
	if err := validateSpec(record.Spec); err != nil {
		return fmt.Errorf("durable provisioning spec is invalid (%v): %w", err, ErrConflict)
	}
	if err := validateProgress(record); err != nil {
		return err
	}
	return nil
}

func validProvisioningGenerationID(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16 && strings.ToLower(value) == value
}

func validFingerprint(value string) bool {
	digest, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(digest) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == 32
}

func commandID(provisioningID string, step Step, key string) string {
	value := provisioningID + ":" + string(step)
	if key != "" {
		value += ":" + key
	}
	return value
}

func errorClass(step Step, err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrConflict):
		return string(step) + "_conflict"
	case errors.Is(err, ErrInvalid):
		return string(step) + "_invalid"
	default:
		return string(step) + "_unavailable"
	}
}

func failureState(err error) State {
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrConflict) {
		return StatePermanentFailed
	}
	return StateRetryableFailed
}
