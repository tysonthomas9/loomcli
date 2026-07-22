package automation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func (s *Service) CreateBinding(ctx context.Context, auth authority.OperatorAuthority, command CreateBindingCommand) (*Binding, error) {
	workspace, err := normalizeRequired("workspace", command.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.authority.RequireOperator(ActionCreateBinding, workspace, auth); err != nil {
		return nil, err
	}
	if s.bindings == nil {
		return nil, ErrUnavailable
	}
	if strings.TrimSpace(command.Definition.TargetAgentServiceID) != "" {
		return nil, ErrManagedBinding
	}

	binding, err := s.bindingFromDefinition(ctx, workspace, command.Definition, true)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	binding.CreatedAt = now
	binding.UpdatedAt = now
	persisted, err := s.bindings.CreateBinding(ctx, cloneBinding(binding))
	if err != nil {
		return nil, fmt.Errorf("create binding %q: %w", binding.BindingID, err)
	}
	if err := validatePersistedBinding(persisted, workspace, binding.BindingID); err != nil {
		return nil, err
	}
	return cloneBinding(persisted), nil
}

func (s *Service) UpdateBinding(ctx context.Context, auth authority.OperatorAuthority, command UpdateBindingCommand) (*Binding, error) {
	normalized, err := normalizeBindingCommand(BindingCommand{WorkspaceKey: command.WorkspaceKey, BindingID: command.BindingID})
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.authority.RequireOperator(ActionUpdateBinding, normalized.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s.bindings == nil || s.unmanagedBindings == nil {
		return nil, ErrUnavailable
	}
	if command.Patch.TargetAgentServiceID != nil {
		return nil, ErrManagedBinding
	}
	existing, err := s.loadBinding(ctx, normalized.WorkspaceKey, normalized.BindingID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(existing.TargetAgentServiceID) != "" {
		return nil, ErrManagedBinding
	}
	updated, err := s.bindingFromPatch(ctx, normalized.WorkspaceKey, existing, command.Patch)
	if err != nil {
		return nil, err
	}
	persisted, err := s.unmanagedBindings.ReplaceUnmanagedBinding(ctx, UnmanagedBindingReplacement{
		Expected: unmanagedBindingSnapshot(existing),
		Binding:  cloneBinding(updated),
	})
	if err != nil {
		return nil, fmt.Errorf("update binding %q: %w", updated.BindingID, err)
	}
	if err := validatePersistedBinding(persisted, normalized.WorkspaceKey, normalized.BindingID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(persisted.TargetAgentServiceID) != "" {
		return nil, ErrInvalidPersistedState
	}
	return cloneBinding(persisted), nil
}

func (s *Service) bindingFromPatch(ctx context.Context, workspace string, existing *Binding, patch BindingPatch) (*Binding, error) {
	definition := definitionFromBinding(existing)
	applyBindingPatch(&definition, patch)
	targetChanged := patch.DriverID != nil || patch.DriverVersionID != nil
	if patch.DriverID != nil && patch.DriverVersionID == nil {
		definition.DriverVersionID = ""
	}
	updated, err := s.bindingFromDefinition(ctx, workspace, definition, targetChanged)
	if err != nil {
		return nil, err
	}
	if updated.BindingID != existing.BindingID {
		return nil, fmt.Errorf("binding id is immutable: %w", ErrInvalid)
	}
	updated.Enabled = existing.Enabled
	updated.CreatedAt = existing.CreatedAt
	updated.UpdatedAt = nextBindingRevision(existing.UpdatedAt, s.now())
	return updated, nil
}

func (s *Service) EnableBinding(ctx context.Context, auth authority.OperatorAuthority, command BindingCommand) (*Binding, error) {
	return s.setBindingEnabled(ctx, auth, command, true)
}

func (s *Service) DisableBinding(ctx context.Context, auth authority.OperatorAuthority, command BindingCommand) (*Binding, error) {
	return s.setBindingEnabled(ctx, auth, command, false)
}

func (s *Service) setBindingEnabled(ctx context.Context, auth authority.OperatorAuthority, command BindingCommand, enabled bool) (*Binding, error) {
	command, err := normalizeBindingCommand(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	action := ActionDisableBinding
	if enabled {
		action = ActionEnableBinding
	}
	if err := s.authority.RequireOperator(action, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s.bindings == nil || s.unmanagedBindings == nil {
		return nil, ErrUnavailable
	}
	binding, err := s.loadBinding(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(binding.TargetAgentServiceID) != "" {
		return nil, ErrManagedBinding
	}
	expected := unmanagedBindingSnapshot(binding)
	binding.Enabled = enabled
	binding.UpdatedAt = nextBindingRevision(binding.UpdatedAt, s.now())
	persisted, err := s.unmanagedBindings.ReplaceUnmanagedBinding(ctx, UnmanagedBindingReplacement{
		Expected: expected,
		Binding:  cloneBinding(binding),
	})
	if err != nil {
		return nil, fmt.Errorf("set binding %q enabled=%t: %w", binding.BindingID, enabled, err)
	}
	if err := validatePersistedBinding(persisted, command.WorkspaceKey, command.BindingID); err != nil {
		return nil, err
	}
	if persisted.Enabled != enabled || strings.TrimSpace(persisted.TargetAgentServiceID) != "" {
		return nil, ErrInvalidPersistedState
	}
	return cloneBinding(persisted), nil
}

func (s *Service) DeleteBinding(ctx context.Context, auth authority.OperatorAuthority, command BindingCommand) error {
	command, err := normalizeBindingCommand(command)
	if err != nil {
		return err
	}
	if s == nil || s.authority == nil {
		return authority.ErrAdmissionDenied
	}
	if err := s.authority.RequireOperator(ActionDeleteBinding, command.WorkspaceKey, auth); err != nil {
		return err
	}
	if s.bindings == nil || s.unmanagedBindings == nil {
		return ErrUnavailable
	}
	binding, err := s.loadBinding(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(binding.TargetAgentServiceID) != "" {
		return ErrManagedBinding
	}
	if binding.Enabled {
		return ErrBindingEnabled
	}
	if err := s.unmanagedBindings.DeleteUnmanagedBindingIfUnchanged(ctx, unmanagedBindingSnapshot(binding)); err != nil {
		return fmt.Errorf("delete binding %q: %w", command.BindingID, err)
	}
	return nil
}

func unmanagedBindingSnapshot(binding *Binding) UnmanagedBindingSnapshot {
	if binding == nil {
		return UnmanagedBindingSnapshot{}
	}
	return UnmanagedBindingSnapshot{
		WorkspaceKey: binding.WorkspaceKey, BindingID: binding.BindingID,
		ExpectedRouteKey: binding.RouteKey, ExpectedCreatedAt: binding.CreatedAt, ExpectedUpdatedAt: binding.UpdatedAt,
	}
}

func unmanagedBindingMatchesSnapshot(binding *Binding, expected UnmanagedBindingSnapshot) bool {
	return binding != nil && binding.WorkspaceKey == expected.WorkspaceKey && binding.BindingID == expected.BindingID &&
		strings.TrimSpace(binding.TargetAgentServiceID) == "" && binding.RouteKey == expected.ExpectedRouteKey &&
		binding.CreatedAt.Equal(expected.ExpectedCreatedAt) && binding.UpdatedAt.Equal(expected.ExpectedUpdatedAt)
}

func (s *Service) bindingFromDefinition(ctx context.Context, workspace string, definition BindingDefinition, resolveTarget bool) (*Binding, error) {
	definition, err := prepareBindingDefinition(definition)
	if err != nil {
		return nil, err
	}
	driverID, versionID, err := s.resolveBindingTarget(ctx, workspace, definition, resolveTarget)
	if err != nil {
		return nil, err
	}
	return &Binding{
		WorkspaceKey:         workspace,
		BindingID:            definition.BindingID,
		Name:                 definition.Name,
		SourceKind:           definition.SourceKind,
		SourceRef:            definition.SourceRef,
		SourceConfigRef:      definition.SourceConfigRef,
		RouteKey:             definition.RouteKey,
		Method:               definition.Method,
		PathTemplate:         definition.PathTemplate,
		Topic:                definition.Topic,
		EventTypePatterns:    append([]string(nil), definition.EventTypePatterns...),
		FilterRef:            definition.FilterRef,
		DriverID:             driverID,
		DriverVersionID:      versionID,
		TargetEntrypoint:     definition.TargetEntrypoint,
		TargetAgentServiceID: definition.TargetAgentServiceID,
		ConcurrencyPolicy:    definition.ConcurrencyPolicy,
		IdempotencyPolicy:    definition.IdempotencyPolicy,
		AuthPolicy:           definition.AuthPolicy,
		SubjectKeyTemplate:   definition.SubjectKeyTemplate,
		ActorFilter:          cloneActorFilter(definition.ActorFilter),
		RetryMaxAttempts:     definition.RetryMaxAttempts,
		RetryBackoffSeconds:  definition.RetryBackoffSeconds,
		Schedule:             definition.Schedule,
		ScheduleTimezone:     definition.ScheduleTimezone,
		Permissions:          append([]string(nil), definition.Permissions...),
		Enabled:              definition.Enabled,
	}, nil
}

func prepareBindingDefinition(definition BindingDefinition) (BindingDefinition, error) {
	definition = normalizeDefinition(definition)
	if definition.BindingID == "" && definition.RouteKey != "" {
		definition.BindingID = "binding-" + strings.ReplaceAll(definition.RouteKey, ".", "-")
	}
	if definition.BindingID == "" {
		return BindingDefinition{}, fmt.Errorf("binding id is required when route key is empty: %w", ErrInvalid)
	}
	deriveBindingRoute(&definition)
	if definition.Name == "" {
		definition.Name = firstNonEmpty(definition.RouteKey, definition.BindingID)
	}
	defaultBindingPolicies(&definition)
	if err := validateBindingDefinition(definition); err != nil {
		return BindingDefinition{}, err
	}
	return definition, nil
}

func deriveBindingRoute(definition *BindingDefinition) {
	if definition.RouteKey != "" {
		return
	}
	switch definition.SourceKind {
	case SourceKindCron:
		definition.RouteKey = "cron:" + definition.BindingID
	case SourceKindInternal:
		definition.RouteKey = "internal:" + definition.BindingID
	}
}

func defaultBindingPolicies(definition *BindingDefinition) {
	if definition.RetryMaxAttempts == 0 {
		definition.RetryMaxAttempts = DefaultRetryMaxAttempts
	}
	if definition.RetryBackoffSeconds == 0 {
		definition.RetryBackoffSeconds = DefaultRetryBackoffSeconds
	}
	if definition.ConcurrencyPolicy == "" {
		definition.ConcurrencyPolicy = ConcurrencyOneActivePerEpic
	}
}

func validateBindingDefinition(definition BindingDefinition) error {
	if definition.SourceKind == "" || definition.DriverID == "" {
		return fmt.Errorf("source kind and driver id are required: %w", ErrInvalid)
	}
	if definition.RouteKey == "" && len(definition.EventTypePatterns) == 0 {
		return fmt.Errorf("route key or event type patterns are required: %w", ErrInvalid)
	}
	if definition.RetryMaxAttempts < 0 || definition.RetryBackoffSeconds < 0 {
		return fmt.Errorf("retry values cannot be negative: %w", ErrInvalid)
	}
	if !validConcurrencyPolicy(definition.ConcurrencyPolicy) {
		return fmt.Errorf("unsupported concurrency policy %q: %w", definition.ConcurrencyPolicy, ErrInvalid)
	}
	for _, pattern := range definition.EventTypePatterns {
		if err := validatePattern(pattern); err != nil {
			return err
		}
	}
	if bindingMatchesInternalIssueEvent(definition) && !excludesWorkflowActor(definition.ActorFilter) {
		return fmt.Errorf("internal issue-event bindings must include %q in actor_filter.exclude_actor_kinds; hop depth does not stop issue-journal system-root loops: %w", EventOriginWorkflow, ErrInvalid)
	}
	if err := validateSubjectTemplate(definition.SubjectKeyTemplate); err != nil {
		return err
	}
	return validateBindingSchedule(definition)
}

// bindingMatchesInternalIssueEvent reports whether the binding can receive an
// event in the issue-journal bridge's internal.issue.* namespace. Route keys
// are exact addresses, while EventTypePatterns use Automation's segment
// matcher; checking the namespace structurally also catches broad patterns
// such as internal.*.* and *.*.* before they can admit a depth-zero loop.
func bindingMatchesInternalIssueEvent(definition BindingDefinition) bool {
	if isInternalIssueRoute(definition.RouteKey) {
		return true
	}
	for _, pattern := range definition.EventTypePatterns {
		segments, err := parsePattern(pattern)
		if err != nil || len(segments) != 3 {
			continue
		}
		if segments[0].matches("internal") && segments[1].matches("issue") {
			return true
		}
	}
	return false
}

func isInternalIssueRoute(routeKey string) bool {
	segments := strings.Split(routeKey, ".")
	return len(segments) >= 3 && segments[0] == "internal" && segments[1] == "issue"
}

func excludesWorkflowActor(filter *ActorFilter) bool {
	if filter == nil {
		return false
	}
	for _, excluded := range filter.ExcludeActorKinds {
		if strings.ToLower(strings.TrimSpace(excluded)) == string(EventOriginWorkflow) {
			return true
		}
	}
	return false
}

func validateBindingSchedule(definition BindingDefinition) error {
	if definition.SourceKind != SourceKindCron {
		if definition.Schedule != "" || definition.ScheduleTimezone != "" {
			return fmt.Errorf("schedule and schedule timezone require cron source kind: %w", ErrInvalid)
		}
		return nil
	}
	if definition.Schedule == "" {
		return fmt.Errorf("cron schedule is required: %w", ErrInvalid)
	}
	if _, err := cron.ParseStandard(definition.Schedule); err != nil {
		return fmt.Errorf("invalid cron schedule %q: %v: %w", definition.Schedule, err, ErrInvalid)
	}
	if definition.ScheduleTimezone == "" {
		return nil
	}
	if _, err := time.LoadLocation(definition.ScheduleTimezone); err != nil {
		return fmt.Errorf("invalid schedule timezone %q: %v: %w", definition.ScheduleTimezone, err, ErrInvalid)
	}
	return nil
}

func (s *Service) resolveBindingTarget(ctx context.Context, workspace string, definition BindingDefinition, resolveTarget bool) (string, string, error) {
	if !resolveTarget {
		if definition.DriverVersionID == "" {
			return "", "", fmt.Errorf("persisted driver version is required: %w", ErrInvalidPersistedState)
		}
		return definition.DriverID, definition.DriverVersionID, nil
	}
	effective, err := s.resolveEffectiveVersion(ctx, workspace, definition.DriverID, "automation binding target")
	if err != nil {
		return "", "", err
	}
	if definition.DriverVersionID != "" && definition.DriverVersionID != effective.Version.VersionID {
		return "", "", fmt.Errorf("requested driver version %q is not activated version %q: %w", definition.DriverVersionID, effective.Version.VersionID, ErrConflict)
	}
	return effective.Driver.DriverID, effective.Version.VersionID, nil
}

func (s *Service) resolveEffectiveVersion(ctx context.Context, workspace, driverRef, reason string) (*workflowcatalog.EffectiveVersion, error) {
	if s == nil || s.catalog == nil || s.catalogAuthority == nil {
		return nil, ErrUnavailable
	}
	auth, err := s.catalogAuthority.AuthorityForEffectiveVersion(ctx, workspace, reason)
	if err != nil {
		return nil, fmt.Errorf("authorize effective version resolution: %w", err)
	}
	effective, err := s.catalog.ResolveEffectiveVersion(ctx, auth, workspace, driverRef)
	if err != nil {
		return nil, fmt.Errorf("resolve effective version for driver %q: %w", driverRef, err)
	}
	if effective == nil || effective.Driver == nil || effective.Version == nil {
		return nil, ErrInvalidPersistedState
	}
	if err := validateWorkspace(effective.Driver.WorkspaceKey, workspace); err != nil {
		return nil, err
	}
	if err := validateWorkspace(effective.Version.WorkspaceKey, workspace); err != nil {
		return nil, err
	}
	if strings.TrimSpace(effective.Driver.DriverID) == "" ||
		effective.Version.DriverID != effective.Driver.DriverID ||
		strings.TrimSpace(effective.Version.VersionID) == "" ||
		effective.Driver.ActiveVersionID != effective.Version.VersionID {
		return nil, ErrInvalidPersistedState
	}
	return effective, nil
}

func normalizeDefinition(definition BindingDefinition) BindingDefinition {
	definition.BindingID = strings.TrimSpace(definition.BindingID)
	definition.Name = strings.TrimSpace(definition.Name)
	definition.SourceKind = strings.ToLower(strings.TrimSpace(definition.SourceKind))
	definition.SourceRef = strings.TrimSpace(definition.SourceRef)
	definition.SourceConfigRef = strings.TrimSpace(definition.SourceConfigRef)
	definition.RouteKey = strings.TrimSpace(definition.RouteKey)
	definition.Method = strings.TrimSpace(definition.Method)
	definition.PathTemplate = strings.TrimSpace(definition.PathTemplate)
	definition.Topic = strings.TrimSpace(definition.Topic)
	definition.FilterRef = strings.TrimSpace(definition.FilterRef)
	definition.DriverID = strings.TrimSpace(definition.DriverID)
	definition.DriverVersionID = strings.TrimSpace(definition.DriverVersionID)
	definition.TargetEntrypoint = strings.TrimSpace(definition.TargetEntrypoint)
	definition.TargetAgentServiceID = strings.TrimSpace(definition.TargetAgentServiceID)
	definition.IdempotencyPolicy = strings.TrimSpace(definition.IdempotencyPolicy)
	definition.AuthPolicy = strings.TrimSpace(definition.AuthPolicy)
	definition.SubjectKeyTemplate = strings.TrimSpace(definition.SubjectKeyTemplate)
	definition.Schedule = strings.TrimSpace(definition.Schedule)
	definition.ScheduleTimezone = strings.TrimSpace(definition.ScheduleTimezone)
	definition.EventTypePatterns = normalizeStrings(definition.EventTypePatterns)
	definition.Permissions = normalizeStrings(definition.Permissions)
	if definition.ActorFilter != nil {
		definition.ActorFilter = &ActorFilter{
			ExcludeActorKinds: normalizeLowerStrings(definition.ActorFilter.ExcludeActorKinds),
			AllowActors:       normalizeStrings(definition.ActorFilter.AllowActors),
		}
	}
	return definition
}

func definitionFromBinding(binding *Binding) BindingDefinition {
	return BindingDefinition{
		BindingID:            binding.BindingID,
		Name:                 binding.Name,
		SourceKind:           binding.SourceKind,
		SourceRef:            binding.SourceRef,
		SourceConfigRef:      binding.SourceConfigRef,
		RouteKey:             binding.RouteKey,
		Method:               binding.Method,
		PathTemplate:         binding.PathTemplate,
		Topic:                binding.Topic,
		EventTypePatterns:    append([]string(nil), binding.EventTypePatterns...),
		FilterRef:            binding.FilterRef,
		DriverID:             binding.DriverID,
		DriverVersionID:      binding.DriverVersionID,
		TargetEntrypoint:     binding.TargetEntrypoint,
		TargetAgentServiceID: binding.TargetAgentServiceID,
		ConcurrencyPolicy:    binding.ConcurrencyPolicy,
		IdempotencyPolicy:    binding.IdempotencyPolicy,
		AuthPolicy:           binding.AuthPolicy,
		SubjectKeyTemplate:   binding.SubjectKeyTemplate,
		ActorFilter:          cloneActorFilter(binding.ActorFilter),
		RetryMaxAttempts:     binding.RetryMaxAttempts,
		RetryBackoffSeconds:  binding.RetryBackoffSeconds,
		Schedule:             binding.Schedule,
		ScheduleTimezone:     binding.ScheduleTimezone,
		Permissions:          append([]string(nil), binding.Permissions...),
		Enabled:              binding.Enabled,
	}
}

func applyBindingPatch(definition *BindingDefinition, patch BindingPatch) {
	applyBindingIdentityPatch(definition, patch)
	applyBindingTargetPatch(definition, patch)
	applyBindingPolicyPatch(definition, patch)
	applyBindingRetryPatch(definition, patch)
}

func applyBindingIdentityPatch(definition *BindingDefinition, patch BindingPatch) {
	if patch.Name != nil {
		definition.Name = *patch.Name
	}
	if patch.SourceKind != nil {
		definition.SourceKind = *patch.SourceKind
	}
	if patch.SourceRef != nil {
		definition.SourceRef = *patch.SourceRef
	}
	if patch.SourceConfigRef != nil {
		definition.SourceConfigRef = *patch.SourceConfigRef
	}
	if patch.RouteKey != nil {
		definition.RouteKey = *patch.RouteKey
	}
	if patch.Method != nil {
		definition.Method = *patch.Method
	}
	if patch.PathTemplate != nil {
		definition.PathTemplate = *patch.PathTemplate
	}
	if patch.Topic != nil {
		definition.Topic = *patch.Topic
	}
	if patch.EventTypePatterns != nil {
		definition.EventTypePatterns = append([]string(nil), (*patch.EventTypePatterns)...)
	}
}

func applyBindingTargetPatch(definition *BindingDefinition, patch BindingPatch) {
	if patch.FilterRef != nil {
		definition.FilterRef = *patch.FilterRef
	}
	if patch.DriverID != nil {
		definition.DriverID = *patch.DriverID
	}
	if patch.DriverVersionID != nil {
		definition.DriverVersionID = *patch.DriverVersionID
	}
	if patch.TargetEntrypoint != nil {
		definition.TargetEntrypoint = *patch.TargetEntrypoint
	}
	if patch.TargetAgentServiceID != nil {
		definition.TargetAgentServiceID = *patch.TargetAgentServiceID
	}
}

func applyBindingPolicyPatch(definition *BindingDefinition, patch BindingPatch) {
	if patch.ConcurrencyPolicy != nil {
		definition.ConcurrencyPolicy = *patch.ConcurrencyPolicy
	}
	if patch.IdempotencyPolicy != nil {
		definition.IdempotencyPolicy = *patch.IdempotencyPolicy
	}
	if patch.AuthPolicy != nil {
		definition.AuthPolicy = *patch.AuthPolicy
	}
	if patch.SubjectKeyTemplate != nil {
		definition.SubjectKeyTemplate = *patch.SubjectKeyTemplate
	}
	if patch.ClearActorFilter {
		definition.ActorFilter = nil
	} else if patch.ActorFilter != nil {
		definition.ActorFilter = cloneActorFilter(patch.ActorFilter)
	}
}

func applyBindingRetryPatch(definition *BindingDefinition, patch BindingPatch) {
	if patch.RetryMaxAttempts != nil {
		definition.RetryMaxAttempts = *patch.RetryMaxAttempts
	}
	if patch.RetryBackoffSeconds != nil {
		definition.RetryBackoffSeconds = *patch.RetryBackoffSeconds
	}
	if patch.Schedule != nil {
		definition.Schedule = *patch.Schedule
	}
	if patch.ScheduleTimezone != nil {
		definition.ScheduleTimezone = *patch.ScheduleTimezone
	}
	if patch.Permissions != nil {
		definition.Permissions = append([]string(nil), (*patch.Permissions)...)
	}
}

func normalizeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizeLowerStrings(values []string) []string {
	values = normalizeStrings(values)
	for index := range values {
		values[index] = strings.ToLower(values[index])
	}
	return values
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
