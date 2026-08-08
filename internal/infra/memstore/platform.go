package memstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type driverStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*workflowcatalog.Driver
}

func newDriverStore() *driverStore {
	return &driverStore{items: make(map[string]map[string]*workflowcatalog.Driver)}
}

var _ store.DriverStore = (*driverStore)(nil)

func (s *driverStore) Create(_ context.Context, in store.DriverCreate) (*workflowcatalog.Driver, error) {
	if in.WorkspaceKey == "" || in.DriverID == "" || in.Name == "" {
		return nil, fmt.Errorf("workspace_key + driver_id + name required: %w", domain.ErrInvalid)
	}
	for key := range in.Metadata {
		if strings.HasPrefix(key, workflowcatalog.ApprovedVersionMetadataPrefix) {
			return nil, fmt.Errorf(
				"metadata %q is lifecycle-owned; use Workflow Catalog approve: %w",
				key,
				domain.ErrInvalid,
			)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*workflowcatalog.Driver)
	}
	if _, ok := s.items[in.WorkspaceKey][in.DriverID]; ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", in.DriverID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	ownerType := in.OwnerType
	if ownerType == "" {
		ownerType = workflowcatalog.DriverOwnerUser
	}
	status := in.Status
	if status == "" {
		status = workflowcatalog.DriverStatusDraft
	}
	trust := in.TrustLevel
	if trust == "" {
		// Unknown/missing = untrusted (fail closed) — mirrors fleet-db's
		// Driver.Validate stamp; registration paths set an explicit level.
		trust = workflowcatalog.DriverTrustUntrusted
	}
	driver := &workflowcatalog.Driver{
		WorkspaceKey: in.WorkspaceKey,
		DriverID:     in.DriverID,
		Name:         in.Name,
		OwnerType:    ownerType,
		OwnerRef:     in.OwnerRef,
		Description:  in.Description,
		Status:       status,
		TrustLevel:   trust,
		Metadata:     cloneMap(in.Metadata),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.items[in.WorkspaceKey][in.DriverID] = driver
	return cloneDriver(driver), nil
}

func (s *driverStore) Get(_ context.Context, ws, driverID string) (*workflowcatalog.Driver, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	driver, ok := s.items[ws][driverID]
	if !ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", driverID, ws, domain.ErrNotFound)
	}
	return cloneDriver(driver), nil
}

func (s *driverStore) List(_ context.Context, ws string, filter store.DriverFilter) ([]*workflowcatalog.Driver, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*workflowcatalog.Driver, 0, len(s.items[ws]))
	for _, driver := range s.items[ws] {
		if driverMatchesMem(driver, filter) {
			out = append(out, cloneDriver(driver))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *driverStore) Update(_ context.Context, ws, driverID string, patch store.DriverUpdate) (*workflowcatalog.Driver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	driver, ok := s.items[ws][driverID]
	if !ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", driverID, ws, domain.ErrNotFound)
	}
	if patch.Status != nil && *patch.Status == workflowcatalog.DriverStatusActive {
		return nil, fmt.Errorf("active status is lifecycle-owned; use Workflow Catalog ActivateVersion: %w", domain.ErrInvalid)
	}
	var replacementMetadata map[string]string
	if patch.Metadata != nil {
		var err error
		replacementMetadata, err = preserveApprovalMetadataForGenericUpdate(driver.Metadata, *patch.Metadata)
		if err != nil {
			return nil, err
		}
	}
	if patch.Name != nil {
		driver.Name = *patch.Name
	}
	if patch.OwnerType != nil {
		driver.OwnerType = *patch.OwnerType
	}
	if patch.OwnerRef != nil {
		driver.OwnerRef = *patch.OwnerRef
	}
	if patch.Description != nil {
		driver.Description = *patch.Description
	}
	if patch.Status != nil {
		driver.Status = *patch.Status
	}
	if patch.TrustLevel != nil {
		driver.TrustLevel = *patch.TrustLevel
	}
	if patch.Metadata != nil {
		driver.Metadata = replacementMetadata
	}
	driver.UpdatedAt = time.Now().UTC()
	return cloneDriver(driver), nil
}

// ApproveDriverVersionForTest is the memstore test double for Workflow
// Catalog's typed ApproveVersion command. memstore is test-only; production
// calls the durable FleetDB command through the capability adapter.
func (s *Store) ApproveDriverVersionForTest(
	ctx context.Context,
	workspace, driverID, versionID string,
) (*workflowcatalog.Driver, error) {
	version, err := s.versions.Get(ctx, workspace, versionID)
	if err != nil {
		return nil, err
	}
	if version.DriverID != driverID {
		return nil, fmt.Errorf("version %q is not owned by driver %q: %w", versionID, driverID, domain.ErrInvalid)
	}
	if version.ValidationStatus != workflowcatalog.DriverVersionValidationPassed {
		return nil, fmt.Errorf("version %q has not passed validation: %w", versionID, domain.ErrInvalid)
	}

	s.drivers.mu.Lock()
	defer s.drivers.mu.Unlock()
	driver, ok := s.drivers.items[workspace][driverID]
	if !ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", driverID, workspace, domain.ErrNotFound)
	}
	if driver.Metadata == nil {
		driver.Metadata = map[string]string{}
	}
	driver.Metadata[workflowcatalog.ApprovedVersionMetadataKey(versionID)] = version.SourceDigest
	driver.Revision++
	driver.UpdatedAt = time.Now().UTC()
	return cloneDriver(driver), nil
}

// UnapproveDriverVersionForTest is the memstore test double for Workflow
// Catalog's typed UnapproveVersion command.
func (s *Store) UnapproveDriverVersionForTest(
	ctx context.Context,
	workspace, driverID, versionID string,
) (*workflowcatalog.Driver, error) {
	version, err := s.versions.Get(ctx, workspace, versionID)
	if err != nil {
		return nil, err
	}
	if version.DriverID != driverID {
		return nil, fmt.Errorf("version %q is not owned by driver %q: %w", versionID, driverID, domain.ErrInvalid)
	}

	s.drivers.mu.Lock()
	defer s.drivers.mu.Unlock()
	driver, ok := s.drivers.items[workspace][driverID]
	if !ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", driverID, workspace, domain.ErrNotFound)
	}
	delete(driver.Metadata, workflowcatalog.ApprovedVersionMetadataKey(versionID))
	driver.Revision++
	driver.UpdatedAt = time.Now().UTC()
	return cloneDriver(driver), nil
}

// ActivateDriverVersionForTest is the memstore test double for Workflow
// Catalog's typed ActivateVersion command. It applies the same ownership,
// validation, and prior-approval preconditions as FleetDB.
func (s *Store) ActivateDriverVersionForTest(
	ctx context.Context,
	workspace, driverID, versionID string,
) (*workflowcatalog.Driver, error) {
	version, err := s.versions.Get(ctx, workspace, versionID)
	if err != nil {
		return nil, err
	}
	if version.DriverID != driverID {
		return nil, fmt.Errorf("version %q is not owned by driver %q: %w", versionID, driverID, domain.ErrInvalid)
	}
	if version.ValidationStatus != workflowcatalog.DriverVersionValidationPassed {
		return nil, fmt.Errorf("version %q has not passed validation: %w", versionID, domain.ErrInvalid)
	}

	s.drivers.mu.Lock()
	defer s.drivers.mu.Unlock()
	driver, ok := s.drivers.items[workspace][driverID]
	if !ok {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", driverID, workspace, domain.ErrNotFound)
	}
	if !workflowcatalog.VersionApproved(driver, version) {
		return nil, fmt.Errorf("version %q is not approved: %w", versionID, domain.ErrInvalid)
	}
	driver.ActiveVersionID = versionID
	driver.Status = workflowcatalog.DriverStatusActive
	driver.Revision++
	driver.UpdatedAt = time.Now().UTC()
	return cloneDriver(driver), nil
}

func preserveApprovalMetadataForGenericUpdate(
	current, replacement map[string]string,
) (map[string]string, error) {
	for key, value := range replacement {
		if !strings.HasPrefix(key, workflowcatalog.ApprovedVersionMetadataPrefix) {
			continue
		}
		if currentValue, ok := current[key]; !ok || currentValue != value {
			return nil, fmt.Errorf(
				"metadata %q is lifecycle-owned; use Workflow Catalog approve/unapprove: %w",
				key,
				domain.ErrInvalid,
			)
		}
	}
	out := cloneMap(replacement)
	if out == nil {
		out = map[string]string{}
	}
	for key, value := range current {
		if strings.HasPrefix(key, workflowcatalog.ApprovedVersionMetadataPrefix) {
			out[key] = value
		}
	}
	return out, nil
}

func (s *driverStore) exists(ws, driverID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.items[ws][driverID]
	return ok
}

type driverVersionStore struct {
	mu      sync.RWMutex
	items   map[string]map[string]*workflowcatalog.DriverVersion
	drivers *driverStore
}

func newDriverVersionStore(drivers *driverStore) *driverVersionStore {
	return &driverVersionStore{items: make(map[string]map[string]*workflowcatalog.DriverVersion), drivers: drivers}
}

var _ store.DriverVersionStore = (*driverVersionStore)(nil)

func (s *driverVersionStore) Create(_ context.Context, in store.DriverVersionCreate) (*workflowcatalog.DriverVersion, error) {
	if in.WorkspaceKey == "" || in.VersionID == "" || in.DriverID == "" || in.Version <= 0 || in.SourceDigest == "" || in.BundleDigest == "" {
		return nil, fmt.Errorf("workspace_key + version_id + driver_id + version + source_digest + bundle_digest required: %w", domain.ErrInvalid)
	}
	if s.drivers != nil && !s.drivers.exists(in.WorkspaceKey, in.DriverID) {
		return nil, fmt.Errorf("driver %q in workspace %q: %w", in.DriverID, in.WorkspaceKey, domain.ErrNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*workflowcatalog.DriverVersion)
	}
	if _, ok := s.items[in.WorkspaceKey][in.VersionID]; ok {
		return nil, fmt.Errorf("driver version %q in workspace %q: %w", in.VersionID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	status := in.ValidationStatus
	if status == "" {
		status = workflowcatalog.DriverVersionValidationPending
	}
	version := &workflowcatalog.DriverVersion{
		WorkspaceKey:     in.WorkspaceKey,
		VersionID:        in.VersionID,
		DriverID:         in.DriverID,
		Version:          in.Version,
		SourceRef:        in.SourceRef,
		SourceDigest:     in.SourceDigest,
		BundleRef:        in.BundleRef,
		BundleDigest:     in.BundleDigest,
		Runtime:          in.Runtime,
		Manifest:         cloneMap(in.Manifest),
		BuildDiagnostics: in.BuildDiagnostics,
		ValidationStatus: status,
		CreatedBy:        in.CreatedBy,
		CreatedAt:        time.Now().UTC(),
	}
	s.items[in.WorkspaceKey][in.VersionID] = version
	return cloneDriverVersion(version), nil
}

func (s *driverVersionStore) Get(_ context.Context, ws, versionID string) (*workflowcatalog.DriverVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	version, ok := s.items[ws][versionID]
	if !ok {
		return nil, fmt.Errorf("driver version %q in workspace %q: %w", versionID, ws, domain.ErrNotFound)
	}
	return cloneDriverVersion(version), nil
}

func (s *driverVersionStore) List(_ context.Context, ws string, filter store.DriverVersionFilter) ([]*workflowcatalog.DriverVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*workflowcatalog.DriverVersion, 0, len(s.items[ws]))
	for _, version := range s.items[ws] {
		if driverVersionMatchesMem(version, filter) {
			out = append(out, cloneDriverVersion(version))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *driverVersionStore) belongsToDriver(ws, versionID, driverID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	version, ok := s.items[ws][versionID]
	return ok && version.DriverID == driverID
}

type triggerBindingStore struct {
	mu       sync.RWMutex
	items    map[string]map[string]*automation.Binding
	versions *driverVersionStore
	services *agentServiceStore
}

func newTriggerBindingStore(versions *driverVersionStore, services *agentServiceStore) *triggerBindingStore {
	return &triggerBindingStore{items: make(map[string]map[string]*automation.Binding), versions: versions, services: services}
}

var _ store.TriggerBindingStore = (*triggerBindingStore)(nil)

func (s *triggerBindingStore) Create(_ context.Context, in store.TriggerBindingCreate) (*automation.Binding, error) {
	in = in.WithDerivedRoute()
	if err := s.validateTriggerBindingCreate(in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*automation.Binding)
	}
	if _, ok := s.items[in.WorkspaceKey][in.BindingID]; ok {
		return nil, fmt.Errorf("trigger binding %q in workspace %q: %w", in.BindingID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	if err := s.ensureTriggerBindingRouteAvailableLocked(in); err != nil {
		return nil, err
	}
	binding := newTriggerBindingMem(in)
	s.items[in.WorkspaceKey][in.BindingID] = binding
	return redactedTriggerBinding(binding), nil
}

func (s *triggerBindingStore) validateTriggerBindingCreate(in store.TriggerBindingCreate) error {
	if in.WorkspaceKey == "" || in.BindingID == "" || in.Name == "" || in.SourceKind == "" || in.DriverID == "" || in.DriverVersionID == "" {
		return fmt.Errorf("workspace_key + binding_id + name + source_kind + driver_id + driver_version_id required: %w", domain.ErrInvalid)
	}
	if s.versions != nil && !s.versions.belongsToDriver(in.WorkspaceKey, in.DriverVersionID, in.DriverID) {
		return fmt.Errorf("driver version %q for driver %q in workspace %q: %w", in.DriverVersionID, in.DriverID, in.WorkspaceKey, domain.ErrNotFound)
	}
	if in.TargetAgentServiceID != "" && s.services != nil && !s.services.exists(in.WorkspaceKey, in.TargetAgentServiceID) {
		return fmt.Errorf("target agent service %q in workspace %q: %w", in.TargetAgentServiceID, in.WorkspaceKey, domain.ErrNotFound)
	}
	if in.RetryMaxAttempts < 0 || in.RetryBackoffSeconds < 0 {
		return fmt.Errorf("retry_max_attempts and retry_backoff_seconds must be non-negative: %w", domain.ErrInvalid)
	}
	return nil
}

func (s *triggerBindingStore) ensureTriggerBindingRouteAvailableLocked(in store.TriggerBindingCreate) error {
	if in.RouteKey == "" {
		return nil
	}
	for _, binding := range s.items[in.WorkspaceKey] {
		if binding.RouteKey == in.RouteKey {
			return fmt.Errorf("trigger binding route %q in workspace %q: %w", in.RouteKey, in.WorkspaceKey, domain.ErrAlreadyExists)
		}
	}
	return nil
}

func newTriggerBindingMem(in store.TriggerBindingCreate) *automation.Binding {
	now := time.Now().UTC()
	return &automation.Binding{
		WorkspaceKey:         in.WorkspaceKey,
		BindingID:            in.BindingID,
		Name:                 in.Name,
		SourceKind:           in.SourceKind,
		SourceRef:            in.SourceRef,
		SourceConfigRef:      in.SourceConfigRef,
		RouteKey:             in.RouteKey,
		Method:               in.Method,
		PathTemplate:         in.PathTemplate,
		Topic:                in.Topic,
		EventTypePatterns:    append([]string(nil), in.EventTypePatterns...),
		FilterRef:            in.FilterRef,
		DriverID:             in.DriverID,
		DriverVersionID:      in.DriverVersionID,
		TargetEntrypoint:     in.TargetEntrypoint,
		TargetAgentServiceID: in.TargetAgentServiceID,
		ConcurrencyPolicy:    defaultTriggerBindingConcurrencyMem(in.ConcurrencyPolicy),
		IdempotencyPolicy:    firstNonEmptyMem(in.IdempotencyPolicy, "header:Idempotency-Key"),
		AuthPolicy:           firstNonEmptyMem(in.AuthPolicy, "workspace_user"),
		WebhookSecret:        in.WebhookSecret,
		SubjectKeyTemplate:   in.SubjectKeyTemplate,
		ActorFilter:          normalizedActorFilterMem(in.ActorFilter),
		RetryMaxAttempts:     defaultRetryFieldMem(in.RetryMaxAttempts, automation.DefaultTriggerRetryMaxAttempts),
		RetryBackoffSeconds:  defaultRetryFieldMem(in.RetryBackoffSeconds, automation.DefaultTriggerRetryBackoffSeconds),
		Schedule:             in.Schedule,
		ScheduleTimezone:     in.ScheduleTimezone,
		Permissions:          append([]string(nil), in.Permissions...),
		Enabled:              in.Enabled,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func defaultTriggerBindingConcurrencyMem(policy automation.BindingConcurrencyPolicy) automation.BindingConcurrencyPolicy {
	if policy == "" {
		return automation.ConcurrencyOneActivePerEpic
	}
	return policy
}

func (s *triggerBindingStore) Get(_ context.Context, ws, bindingID string) (*automation.Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.items[ws][bindingID]
	if !ok {
		return nil, fmt.Errorf("trigger binding %q in workspace %q: %w", bindingID, ws, domain.ErrNotFound)
	}
	return redactedTriggerBinding(binding), nil
}

func (s *triggerBindingStore) GetByRouteKey(_ context.Context, ws, routeKey string) (*automation.Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, binding := range s.items[ws] {
		if binding.RouteKey == routeKey {
			return redactedTriggerBinding(binding), nil
		}
	}
	return nil, fmt.Errorf("trigger binding route %q in workspace %q: %w", routeKey, ws, domain.ErrNotFound)
}

// ResolveWebhookSecret returns the stored plaintext secret (never redacted),
// mirroring fleet-db's privileged webhook-secret endpoint.
func (s *triggerBindingStore) ResolveWebhookSecret(_ context.Context, ws, bindingID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.items[ws][bindingID]
	if !ok {
		return "", fmt.Errorf("trigger binding %q in workspace %q: %w", bindingID, ws, domain.ErrNotFound)
	}
	return binding.WebhookSecret, nil
}

func (s *triggerBindingStore) List(_ context.Context, ws string, filter store.TriggerBindingFilter) ([]*automation.Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*automation.Binding, 0, len(s.items[ws]))
	for _, binding := range s.items[ws] {
		if triggerBindingMatchesMem(binding, filter) {
			out = append(out, redactedTriggerBinding(binding))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *triggerBindingStore) Update(_ context.Context, ws, bindingID string, patch store.TriggerBindingUpdate) (*automation.Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.items[ws][bindingID]
	if !ok {
		return nil, fmt.Errorf("trigger binding %q in workspace %q: %w", bindingID, ws, domain.ErrNotFound)
	}
	oldRoute := binding.RouteKey
	updated := cloneTriggerBinding(binding)
	applyTriggerBindingUpdateMem(updated, patch)
	if s.versions != nil && !s.versions.belongsToDriver(updated.WorkspaceKey, updated.DriverVersionID, updated.DriverID) {
		return nil, fmt.Errorf("driver version %q for driver %q in workspace %q: %w", updated.DriverVersionID, updated.DriverID, updated.WorkspaceKey, domain.ErrNotFound)
	}
	if updated.TargetAgentServiceID != "" && s.services != nil && !s.services.exists(updated.WorkspaceKey, updated.TargetAgentServiceID) {
		return nil, fmt.Errorf("target agent service %q in workspace %q: %w", updated.TargetAgentServiceID, updated.WorkspaceKey, domain.ErrNotFound)
	}
	if s.services != nil && !s.services.triggerRefTargetCompatible(updated.WorkspaceKey, updated.BindingID, updated.TargetAgentServiceID) {
		return nil, fmt.Errorf("trigger binding %q target %q would invalidate agent service trigger refs: %w", updated.BindingID, updated.TargetAgentServiceID, domain.ErrInvalidTransition)
	}
	if updated.RouteKey != "" && updated.RouteKey != oldRoute {
		for id, existing := range s.items[ws] {
			if id != bindingID && existing.RouteKey == updated.RouteKey {
				return nil, fmt.Errorf("trigger binding route %q in workspace %q: %w", updated.RouteKey, ws, domain.ErrAlreadyExists)
			}
		}
	}
	updated.UpdatedAt = time.Now().UTC()
	s.items[ws][bindingID] = updated
	return redactedTriggerBinding(updated), nil
}

// Delete removes a binding. Connector-grant revocation is the caller's
// responsibility (Decision 6) — grants are standalone records keyed by
// binding_id, not fields on the binding, so deleting the binding here never
// touches them. A missing binding wraps domain.ErrNotFound.
func (s *triggerBindingStore) Delete(_ context.Context, ws, bindingID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[ws][bindingID]; !ok {
		return fmt.Errorf("trigger binding %q in workspace %q: %w", bindingID, ws, domain.ErrNotFound)
	}
	delete(s.items[ws], bindingID)
	return nil
}

func (s *triggerBindingStore) getForValidation(ws, bindingID string) (*automation.Binding, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.items[ws][bindingID]
	if !ok {
		return nil, false
	}
	return cloneTriggerBinding(binding), true
}

func (s *triggerBindingStore) hasTargetAgentService(ws, serviceID string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, binding := range s.items[ws] {
		if binding.TargetAgentServiceID == serviceID {
			return true
		}
	}
	return false
}
