package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// testAgentProvisioning composes the real process manager around lightweight,
// store-backed capability adapters. Handler tests therefore exercise the same
// durable state transitions and issuer-bound admission as production.
type testAgentProvisioning struct {
	manager    *agentprovisioning.Manager
	issuer     *authority.Issuer
	progress   *testAgentProvisioningProgressStore
	operations *testAgentProvisioningOperations

	onResolve func(string, authority.Action)
}

func newTestAgentProvisioning(
	st store.Store,
	bindings automation.BindingOperations,
) *testAgentProvisioning {
	return newTestAgentProvisioningWithBindingAuthority(
		st,
		bindings,
		authority.SystemAuthority{},
	)
}

func newTestAgentProvisioningWithBindingAuthority(
	st store.Store,
	bindings automation.BindingOperations,
	bindingAuthority authority.SystemAuthority,
) *testAgentProvisioning {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(agentprovisioning.OperationRules()...)
	if err != nil {
		panic(fmt.Sprintf("compose test AgentProvisioning admission: %v", err))
	}
	progress := newTestAgentProvisioningProgressStore()
	operations := &testAgentProvisioningOperations{
		store: st, bindings: bindings, bindingAuthority: bindingAuthority,
	}
	manager, err := agentprovisioning.New(
		progress,
		operations,
		operations,
		operations,
		operations,
		admission,
		nil,
		time.Now,
	)
	if err != nil {
		panic(fmt.Sprintf("compose test AgentProvisioning manager: %v", err))
	}
	return &testAgentProvisioning{
		manager: manager, issuer: issuer, progress: progress, operations: operations,
	}
}

func (provisioning *testAgentProvisioning) Begin(
	ctx context.Context,
	auth authority.OperatorAuthority,
	spec agentprovisioning.Spec,
) (*agentprovisioning.Record, error) {
	if provisioning == nil || provisioning.manager == nil {
		return nil, agentprovisioning.ErrUnavailable
	}
	return provisioning.manager.Begin(ctx, auth, spec)
}

func (provisioning *testAgentProvisioning) Run(
	ctx context.Context,
	workspace,
	provisioningID string,
) (*agentprovisioning.Record, error) {
	if provisioning == nil || provisioning.manager == nil {
		return nil, agentprovisioning.ErrUnavailable
	}
	return provisioning.manager.Run(ctx, workspace, provisioningID)
}

func (provisioning *testAgentProvisioning) ResolveOperatorAuthority(
	r *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	if r == nil || strings.TrimSpace(r.Header.Get("Authorization")) == "" {
		return authority.OperatorAuthority{}, workflowcataloghttp.ErrUnauthenticated
	}
	if provisioning == nil || provisioning.issuer == nil {
		return authority.OperatorAuthority{}, agentprovisioning.ErrUnavailable
	}
	if action != agentprovisioning.ActionBeginProvisioning {
		return authority.OperatorAuthority{}, authority.ErrActionNotAllowed
	}
	if provisioning.onResolve != nil {
		provisioning.onResolve(workspace, action)
	}
	principal, err := provisioning.issuer.DeriveVerifiedPrincipal(
		authority.PrincipalClaims{
			Subject:   "test-operator",
			Class:     authority.ClassOperator,
			Workspace: workspace,
			Actions:   []authority.Action{agentprovisioning.ActionBeginProvisioning},
			ExpiresAt: time.Now().Add(time.Hour),
		},
	)
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return provisioning.issuer.IssueOperator(
		principal,
		workspace,
		agentprovisioning.ActionBeginProvisioning,
	)
}

type testAgentProvisioningProgressStore struct {
	mu      sync.Mutex
	records map[string]*agentprovisioning.Record
	order   []string
}

func newTestAgentProvisioningProgressStore() *testAgentProvisioningProgressStore {
	return &testAgentProvisioningProgressStore{
		records: make(map[string]*agentprovisioning.Record),
	}
}

func testProvisioningRecordKey(workspace, provisioningID string) string {
	return workspace + "\x00" + provisioningID
}

func (progress *testAgentProvisioningProgressStore) Begin(
	_ context.Context,
	spec agentprovisioning.Spec,
	requestedBy string,
) (*agentprovisioning.Record, error) {
	progress.mu.Lock()
	defer progress.mu.Unlock()

	key := testProvisioningRecordKey(spec.WorkspaceKey, spec.ProvisioningID)
	if existing := progress.records[key]; existing != nil {
		if !reflect.DeepEqual(existing.Spec, spec) {
			return nil, agentprovisioning.ErrConflict
		}
		return cloneTestProvisioningRecord(existing), nil
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	now := time.Now()
	record := &agentprovisioning.Record{
		ProvisioningID: spec.ProvisioningID,
		ProvisioningGenerationID: fmt.Sprintf(
			"%032x",
			len(progress.order)+1,
		),
		WorkspaceKey:     spec.WorkspaceKey,
		RequestedBy:      requestedBy,
		SpecFingerprint:  fmt.Sprintf("sha256:%x", sum),
		Spec:             cloneTestProvisioningSpec(spec),
		State:            agentprovisioning.StatePending,
		UnusedRolePolicy: agentprovisioning.UnusedRoleRetain,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	progress.records[key] = record
	progress.order = append(progress.order, key)
	return cloneTestProvisioningRecord(record), nil
}

func (progress *testAgentProvisioningProgressStore) Get(
	_ context.Context,
	workspace,
	provisioningID string,
) (*agentprovisioning.Record, error) {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	record := progress.records[testProvisioningRecordKey(workspace, provisioningID)]
	if record == nil {
		return nil, agentprovisioning.ErrNotFound
	}
	return cloneTestProvisioningRecord(record), nil
}

func (progress *testAgentProvisioningProgressStore) Save(
	_ context.Context,
	record *agentprovisioning.Record,
	expectedVersion int64,
) (*agentprovisioning.Record, error) {
	if record == nil {
		return nil, agentprovisioning.ErrInvalid
	}
	progress.mu.Lock()
	defer progress.mu.Unlock()

	key := testProvisioningRecordKey(record.WorkspaceKey, record.ProvisioningID)
	existing := progress.records[key]
	if existing == nil {
		return nil, agentprovisioning.ErrNotFound
	}
	if existing.Version != expectedVersion {
		return nil, agentprovisioning.ErrConcurrentWrite
	}
	if existing.ProvisioningID != record.ProvisioningID ||
		existing.ProvisioningGenerationID !=
			record.ProvisioningGenerationID ||
		existing.WorkspaceKey != record.WorkspaceKey ||
		existing.RequestedBy != record.RequestedBy ||
		existing.SpecFingerprint != record.SpecFingerprint ||
		existing.UnusedRolePolicy != record.UnusedRolePolicy ||
		!existing.CreatedAt.Equal(record.CreatedAt) ||
		!reflect.DeepEqual(existing.Spec, record.Spec) {
		return nil, agentprovisioning.ErrConflict
	}
	saved := cloneTestProvisioningRecord(record)
	saved.Version = expectedVersion + 1
	progress.records[key] = saved
	return cloneTestProvisioningRecord(saved), nil
}

func (progress *testAgentProvisioningProgressStore) ListPending(
	_ context.Context,
	workspace string,
	limit int,
) ([]*agentprovisioning.Record, error) {
	progress.mu.Lock()
	defer progress.mu.Unlock()

	records := make([]*agentprovisioning.Record, 0)
	for _, key := range progress.order {
		record := progress.records[key]
		if record == nil || record.WorkspaceKey != workspace {
			continue
		}
		switch record.State {
		case agentprovisioning.StatePending,
			agentprovisioning.StateRunning,
			agentprovisioning.StateRetryableFailed:
			records = append(records, cloneTestProvisioningRecord(record))
		}
		if limit > 0 && len(records) >= limit {
			break
		}
	}
	return records, nil
}

type testAgentProvisioningOperations struct {
	store            store.Store
	bindings         automation.BindingOperations
	bindingAuthority authority.SystemAuthority
}

func (operations *testAgentProvisioningOperations) EnsureRole(
	ctx context.Context,
	command agentprovisioning.EnsureRoleCommand,
) error {
	if operations == nil || operations.store == nil {
		return agentprovisioning.ErrUnavailable
	}
	existing, err := operations.store.Roles().Get(
		ctx,
		command.WorkspaceKey,
		command.Role.Name,
	)
	switch {
	case err == nil:
		return testExactProvisionedRole(existing, command.Role)
	case !errors.Is(err, domain.ErrNotFound):
		return mapTestProvisioningOperationError(err)
	}
	_, err = operations.store.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: command.WorkspaceKey,
		Name:         command.Role.Name,
		Kind:         command.Role.Kind,
		Description:  command.Role.Description,
		Prompt:       command.Role.Prompt,
		PromptFile:   command.Role.PromptFile,
		Model:        command.Role.Model,
		TaskFilter:   command.Role.TaskFilter,
		Backend:      command.Role.Backend,
		Effort:       command.Role.Effort,
		PathPatterns: append([]string(nil), command.Role.PathPatterns...),
		Skills:       append([]string(nil), command.Role.Skills...),
		MaxPriority:  clonePromptAgentInt(command.Role.MaxPriority),
		MaxConcurrency: clonePromptAgentInt(
			command.Role.MaxConcurrency,
		),
		ReadOnly:     command.Role.ReadOnly,
		AllowedTools: append([]string(nil), command.Role.AllowedTools...),
		DeniedTools:  append([]string(nil), command.Role.DeniedTools...),
		MaxBudgetUSD: clonePromptAgentFloat64(command.Role.MaxBudgetUSD),
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return mapTestProvisioningOperationError(err)
	}
	existing, getErr := operations.store.Roles().Get(
		ctx,
		command.WorkspaceKey,
		command.Role.Name,
	)
	if getErr != nil {
		return mapTestProvisioningOperationError(errors.Join(err, getErr))
	}
	return testExactProvisionedRole(existing, command.Role)
}

func testExactProvisionedRole(
	existing *domain.Role,
	expected agentprovisioning.RoleSpec,
) error {
	if existing != nil {
		actual := promptAgentRoleSpec(existing)
		if reflect.DeepEqual(actual, expected) {
			return nil
		}
		mismatches := make([]string, 0, 4)
		if actual.Prompt != expected.Prompt ||
			actual.PromptFile != expected.PromptFile {
			mismatches = append(mismatches, "prompt")
		}
		if actual.TaskFilter != expected.TaskFilter {
			mismatches = append(mismatches, "task_filter")
		}
		if actual.ReadOnly != expected.ReadOnly {
			mismatches = append(mismatches, "read_only")
		}
		if len(mismatches) == 0 {
			mismatches = append(mismatches, "role policy")
		}
		return fmt.Errorf(
			"role %q already exists with incompatible configuration (%s): %w",
			expected.Name,
			strings.Join(mismatches, ", "),
			agentprovisioning.ErrConflict,
		)
	}
	return fmt.Errorf(
		"role %q already exists with a different definition: %w",
		expected.Name,
		agentprovisioning.ErrConflict,
	)
}

func (operations *testAgentProvisioningOperations) EnsureAgent(
	ctx context.Context,
	command agentprovisioning.EnsureAgentCommand,
) error {
	if operations == nil || operations.store == nil {
		return agentprovisioning.ErrUnavailable
	}
	existing, err := operations.store.AgentServices().Get(
		ctx,
		command.WorkspaceKey,
		command.Agent.AgentID,
	)
	switch {
	case err == nil:
		return testExactProvisionedAgent(existing, command.Agent)
	case !errors.Is(err, domain.ErrNotFound):
		return mapTestProvisioningOperationError(err)
	}
	_, err = operations.store.AgentServices().Create(
		ctx,
		store.AgentServiceCreate{
			WorkspaceKey: command.WorkspaceKey,
			ServiceID:    command.Agent.AgentID,
			Name:         command.Agent.Name,
			Kind:         domain.AgentServiceKind(command.Agent.Kind),
			DesiredState: domain.AgentServiceDesiredState(command.Agent.DesiredState),
			RoleName:     command.Agent.RoleName,
			MaxInstances: 1,
			BudgetPolicy: command.Agent.BudgetPolicy,
			Metadata:     clonePromptAgentMap(command.Agent.Metadata),
		},
	)
	if err == nil {
		return nil
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return mapTestProvisioningOperationError(err)
	}
	existing, getErr := operations.store.AgentServices().Get(
		ctx,
		command.WorkspaceKey,
		command.Agent.AgentID,
	)
	if getErr != nil {
		return mapTestProvisioningOperationError(errors.Join(err, getErr))
	}
	return testExactProvisionedAgent(existing, command.Agent)
}

func testExactProvisionedAgent(
	existing *domain.AgentService,
	expected agentprovisioning.AgentSpec,
) error {
	if existing != nil &&
		existing.ServiceID == expected.AgentID &&
		existing.Name == expected.Name &&
		string(existing.Kind) == expected.Kind &&
		string(existing.DesiredState) == expected.DesiredState &&
		existing.RoleName == expected.RoleName &&
		existing.DriverID == "" &&
		existing.DriverVersionID == "" &&
		existing.ProfileName == "" &&
		existing.ScheduleID == "" &&
		len(existing.EventSources) == 0 &&
		len(existing.TriggerRefs) == 0 &&
		existing.PlacementPolicy == "" &&
		existing.MaxInstances == 1 &&
		existing.LeaseID == "" &&
		existing.RestartPolicy == "" &&
		len(existing.Permissions) == 0 &&
		existing.BudgetPolicy == expected.BudgetPolicy &&
		existing.StateRef == "" &&
		reflect.DeepEqual(existing.Metadata, expected.Metadata) &&
		existing.DeletedAt == nil {
		return nil
	}
	return fmt.Errorf(
		"agent %q already exists with a different definition: %w",
		expected.AgentID,
		agentprovisioning.ErrConflict,
	)
}

func (operations *testAgentProvisioningOperations) EnsureBinding(
	ctx context.Context,
	command agentprovisioning.EnsureBindingCommand,
) error {
	if operations == nil || operations.bindings == nil {
		return agentprovisioning.ErrUnavailable
	}
	_, err := operations.bindings.EnsureManagedBinding(
		ctx,
		operations.bindingAuthority,
		automation.EnsureManagedBindingCommand{
			RequestID:      command.CommandID,
			WorkspaceKey:   command.WorkspaceKey,
			AgentServiceID: command.AgentID,
			Definition: automation.BindingDefinition{
				BindingID:            command.Binding.BindingID,
				Name:                 command.Binding.Name,
				SourceKind:           command.Binding.SourceKind,
				SourceConfigRef:      command.Binding.SourceConfigRef,
				RouteKey:             command.Binding.RouteKey,
				EventTypePatterns:    append([]string(nil), command.Binding.EventPatterns...),
				DriverID:             command.Binding.DriverID,
				DriverVersionID:      command.Binding.DriverVersionID,
				TargetEntrypoint:     command.Binding.Entrypoint,
				TargetAgentServiceID: command.AgentID,
				ConcurrencyPolicy: automation.BindingConcurrencyPolicy(
					command.Binding.ConcurrencyPolicy,
				),
				Schedule:         command.Binding.Schedule,
				ScheduleTimezone: command.Binding.ScheduleZone,
				Enabled:          command.Binding.Enabled,
			},
		},
	)
	return mapTestProvisioningOperationError(err)
}

func (operations *testAgentProvisioningOperations) EnsureGrant(
	ctx context.Context,
	command agentprovisioning.EnsureGrantCommand,
) error {
	if operations == nil || operations.store == nil {
		return agentprovisioning.ErrUnavailable
	}
	existing, err := operations.store.Connectors().ListGrantRecordsByBinding(
		ctx,
		command.WorkspaceKey,
		command.BindingID,
	)
	if err != nil {
		return mapTestProvisioningOperationError(err)
	}
	if found, exact := testFindProvisionedGrant(existing, command); found {
		if exact {
			return nil
		}
		return fmt.Errorf(
			"grant %q already exists with a different definition: %w",
			command.Grant.GrantID,
			agentprovisioning.ErrConflict,
		)
	}
	_, err = operations.store.Connectors().CreateManagementGrant(
		ctx,
		connectorsmodule.CreateGrantMutation{
			WorkspaceKey:    command.WorkspaceKey,
			GrantID:         command.Grant.GrantID,
			ConnectorID:     command.Grant.ConnectorID,
			BindingID:       command.BindingID,
			Action:          command.Grant.Action,
			ResourcePattern: command.Grant.ResourcePattern,
		},
	)
	if err == nil {
		return nil
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return mapTestProvisioningOperationError(err)
	}
	existing, getErr := operations.store.Connectors().ListGrantRecordsByBinding(
		ctx,
		command.WorkspaceKey,
		command.BindingID,
	)
	if getErr != nil {
		return mapTestProvisioningOperationError(errors.Join(err, getErr))
	}
	if found, exact := testFindProvisionedGrant(existing, command); found && exact {
		return nil
	}
	return fmt.Errorf(
		"grant %q create raced with a different definition: %w",
		command.Grant.GrantID,
		agentprovisioning.ErrConflict,
	)
}

func testFindProvisionedGrant(
	grants []*connectorsmodule.ConnectorGrant,
	command agentprovisioning.EnsureGrantCommand,
) (found, exact bool) {
	for _, grant := range grants {
		if grant == nil || grant.GrantID != command.Grant.GrantID {
			continue
		}
		return true,
			grant.WorkspaceKey == command.WorkspaceKey &&
				grant.BindingID == command.BindingID &&
				grant.ConnectorID == command.Grant.ConnectorID &&
				grant.Action == command.Grant.Action &&
				grant.ResourcePattern == command.Grant.ResourcePattern &&
				!grant.Revoked()
	}
	return false, false
}

func mapTestProvisioningOperationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, agentprovisioning.ErrInvalid),
		errors.Is(err, agentprovisioning.ErrConflict),
		errors.Is(err, agentprovisioning.ErrNotFound),
		errors.Is(err, agentprovisioning.ErrUnavailable):
		return err
	case errors.Is(err, domain.ErrInvalid),
		errors.Is(err, automation.ErrInvalid):
		return errors.Join(agentprovisioning.ErrInvalid, err)
	case errors.Is(err, domain.ErrAlreadyExists),
		errors.Is(err, domain.ErrConflict),
		errors.Is(err, domain.ErrNotFound),
		errors.Is(err, automation.ErrConflict),
		errors.Is(err, automation.ErrManagedBinding),
		errors.Is(err, automation.ErrNotFound):
		return errors.Join(agentprovisioning.ErrConflict, err)
	default:
		return fmt.Errorf("%w: %w", agentprovisioning.ErrUnavailable, err)
	}
}

func cloneTestProvisioningSpec(
	spec agentprovisioning.Spec,
) agentprovisioning.Spec {
	out := spec
	out.Role.PathPatterns = append([]string(nil), spec.Role.PathPatterns...)
	out.Role.Skills = append([]string(nil), spec.Role.Skills...)
	out.Role.MaxPriority = clonePromptAgentInt(spec.Role.MaxPriority)
	out.Role.MaxConcurrency = clonePromptAgentInt(spec.Role.MaxConcurrency)
	out.Role.AllowedTools = append([]string(nil), spec.Role.AllowedTools...)
	out.Role.DeniedTools = append([]string(nil), spec.Role.DeniedTools...)
	out.Role.MaxBudgetUSD = clonePromptAgentFloat64(spec.Role.MaxBudgetUSD)
	out.Agent.Metadata = clonePromptAgentMap(spec.Agent.Metadata)
	out.Binding.EventPatterns = append([]string(nil), spec.Binding.EventPatterns...)
	out.Grants = append([]agentprovisioning.GrantSpec(nil), spec.Grants...)
	return out
}

func cloneTestProvisioningRecord(
	record *agentprovisioning.Record,
) *agentprovisioning.Record {
	if record == nil {
		return nil
	}
	out := *record
	out.Spec = cloneTestProvisioningSpec(record.Spec)
	out.CompletedSteps = append(
		[]agentprovisioning.Step(nil),
		record.CompletedSteps...,
	)
	out.CompletedGrants = append([]string(nil), record.CompletedGrants...)
	if record.CompletedAt != nil {
		value := *record.CompletedAt
		out.CompletedAt = &value
	}
	return &out
}
